// Package rdb provides PostgreSQL database implementations of repository interfaces.
package rdb

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
)

// ArtistRepository implements entity.ArtistRepository interface for PostgreSQL.
type ArtistRepository struct {
	db     *Database
	outbox Announcer
}

const (
	// Artists: deduplicate via ON CONFLICT on the unique mbid index.
	insertArtistsWithMBIDUnnestQuery = `
		INSERT INTO artists (id, name, mbid)
		SELECT * FROM unnest($1::uuid[], $2::text[], $3::varchar[])
		ON CONFLICT (mbid) DO NOTHING
		RETURNING id
	`
	// Fetch back MBID artists preserving the input array order via WITH ORDINALITY.
	selectArtistsByMBIDsQuery = `
		SELECT a.id, a.name, a.mbid, a.fanart, a.fanart_synced_at, a.official_site_checked_at, a.activity_ended, ` + artistOrganizerColumn + `
		FROM artists a
		JOIN unnest($1::varchar[]) WITH ORDINALITY AS t(mbid, ord) ON a.mbid = t.mbid
		ORDER BY t.ord
	`
	listArtistsQuery = `
		SELECT a.id, a.name, a.mbid, a.fanart, a.fanart_synced_at, a.official_site_checked_at, a.activity_ended, ` + artistOrganizerColumn + `
		FROM artists a
	`
	getArtistQuery = `
		SELECT a.id, a.name, a.mbid, a.fanart, a.fanart_synced_at, a.official_site_checked_at, a.activity_ended, ` + artistOrganizerColumn + `
		FROM artists a
		WHERE a.id = $1
	`
	getArtistByMBIDQuery = `
		SELECT a.id, a.name, a.mbid, a.fanart, a.fanart_synced_at, a.official_site_checked_at, a.activity_ended, ` + artistOrganizerColumn + `
		FROM artists a
		WHERE a.mbid = $1
	`
	updateFanartQuery = `
		UPDATE artists SET fanart = $2, fanart_synced_at = $3 WHERE id = $1
	`
	listStaleOrMissingFanartQuery = `
		SELECT a.id, a.name, a.mbid, a.fanart, a.fanart_synced_at, a.official_site_checked_at, a.activity_ended, ` + artistOrganizerColumn + `
		FROM artists a
		WHERE a.fanart_synced_at IS NULL
		   OR (a.fanart_synced_at < $1
		       AND EXISTS (SELECT 1 FROM followed_artists f WHERE f.artist_id = a.id))
		ORDER BY a.fanart_synced_at ASC NULLS FIRST, a.id ASC
		LIMIT $2
	`
	getOfficialSiteQuery = `
		SELECT id, artist_id, url
		FROM artist_official_site
		WHERE artist_id = $1
	`
	insertOfficialSiteQuery = `
		INSERT INTO artist_official_site (id, artist_id, url)
		VALUES ($1, $2, $3)
	`
	updateArtistNameQuery = `
		UPDATE artists SET name = $2 WHERE id = $1
	`
	updateArtistActivityEndedQuery = `
		UPDATE artists SET activity_ended = $2 WHERE id = $1
	`
	// The slot comes from the UUID's last two bytes, which UUIDv7 fills with
	// random bits, so artists spread evenly and an artist never changes slot.
	listArtistsRefreshDueQuery = `
		SELECT a.id, a.name, a.mbid, a.fanart, a.fanart_synced_at, a.official_site_checked_at, a.activity_ended, ` + artistOrganizerColumn + `
		FROM artists a
		WHERE ((get_byte(uuid_send(a.id), 14) << 8) | get_byte(uuid_send(a.id), 15)) % $1 = $2
		ORDER BY a.id
	`
	listStaleOfficialSiteQuery = `
		SELECT a.id, a.name, a.mbid, a.fanart, a.fanart_synced_at, a.official_site_checked_at, a.activity_ended, ` + artistOrganizerColumn + `
		FROM artists a
		WHERE EXISTS (SELECT 1 FROM followed_artists f WHERE f.artist_id = a.id)
		  AND (a.official_site_checked_at IS NULL OR a.official_site_checked_at < $1)
		ORDER BY a.official_site_checked_at ASC NULLS FIRST, a.id ASC
		LIMIT $2
	`
	updateOfficialSiteURLQuery = `
		UPDATE artist_official_site SET url = $2 WHERE artist_id = $1
	`
	markOfficialSiteCheckedQuery = `
		UPDATE artists SET official_site_checked_at = $2 WHERE id = $1
	`
)

// NewArtistRepository creates a new artist repository instance.
func NewArtistRepository(db *Database, outbox Announcer) *ArtistRepository {
	return &ArtistRepository{db: db, outbox: outbox}
}

// artistOrganizerColumn selects, for an artist aliased a, the Organizer that
// represents it (NULL when none), in the last column scanArtist expects.
const artistOrganizerColumn = `(SELECT oa.organizer_id FROM organizer_artists oa WHERE oa.artist_id = a.id)`

// scanArtist scans a row into an Artist entity including nullable fanart and
// official site check columns. The row selects id, name, mbid, fanart,
// fanart_synced_at, official_site_checked_at, activity_ended and
// artistOrganizerColumn, in that order.
func scanArtist(scan func(dest ...any) error) (*entity.Artist, error) {
	var a entity.Artist
	var fanartJSON []byte
	var syncedAt *time.Time
	var officialSiteCheckedAt sql.NullTime

	if err := scan(&a.ID, &a.Name, &a.MBID, &fanartJSON, &syncedAt, &officialSiteCheckedAt, &a.ActivityEnded, &a.OrganizerID); err != nil {
		return nil, err
	}
	if officialSiteCheckedAt.Valid {
		a.OfficialSiteCheckTime = &officialSiteCheckedAt.Time
	}

	if len(fanartJSON) > 0 {
		var f entity.Fanart
		if err := json.Unmarshal(fanartJSON, &f); err != nil {
			return nil, err
		}
		a.Fanart = &f
	}
	a.FanartSyncTime = syncedAt

	return &a, nil
}

// Create registers the artists entity.MergeArtistsByMBID keeps, one per MBID,
// with an unnest bulk insert. The unique MBID index keeps an artist already
// registered (also by a concurrent call) unchanged. It returns, for each given
// artist in input order, the registered artist of its MBID.
//
// # Announces
//
// ARTIST.created with each Artist this call inserted, in the transaction of
// the insert. Nothing for an MBID already registered, also when a concurrent
// call registered it first.
func (r *ArtistRepository) Create(ctx context.Context, artists ...*entity.Artist) ([]*entity.Artist, error) {
	if len(artists) == 0 {
		return []*entity.Artist{}, nil
	}

	merged, err := entity.MergeArtistsByMBID(artists)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(merged))
	names := make([]string, 0, len(merged))
	mbids := make([]string, 0, len(merged))
	for _, a := range merged {
		if a.ID == "" {
			a.ID = entity.NewArtist(a.Name, a.MBID).ID
		}
		ids = append(ids, a.ID)
		names = append(names, a.Name)
		mbids = append(mbids, a.MBID)
	}

	tx, err := r.db.Pool.Begin(ctx)
	if err != nil {
		return nil, toAppErr(err, "failed to begin artist create transaction")
	}
	// Rollback is a no-op after a successful Commit.
	defer func() { _ = tx.Rollback(ctx) }()

	// RETURNING yields only the artists this call inserted: a known MBID,
	// also one a concurrent call inserted first, hits ON CONFLICT.
	inserted := map[string]bool{}
	if len(ids) > 0 {
		insRows, err := tx.Query(ctx, insertArtistsWithMBIDUnnestQuery, ids, names, mbids)
		if err != nil {
			return nil, toAppErr(err, "failed to bulk insert artists with MBID", slog.Int("count", len(ids)))
		}
		newIDs, err := pgx.CollectRows(insRows, pgx.RowTo[string])
		if err != nil {
			return nil, toAppErr(err, "failed to bulk insert artists with MBID", slog.Int("count", len(ids)))
		}
		for _, id := range newIDs {
			inserted[id] = true
		}
	}

	// Fetch back all registered artists (new and pre-existing) by MBID.
	rows, err := tx.Query(ctx, selectArtistsByMBIDsQuery, mbids)
	if err != nil {
		return nil, toAppErr(err, "failed to select artists by mbids")
	}
	registered, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (*entity.Artist, error) {
		return scanArtist(row.Scan)
	})
	if err != nil {
		return nil, toAppErr(err, "failed to scan artists by mbids")
	}

	byMBID := make(map[string]*entity.Artist, len(registered))
	var created []*entity.Artist
	for _, a := range registered {
		byMBID[a.MBID] = a
		if inserted[a.ID] {
			created = append(created, a)
		}
	}
	if err := announceAll(ctx, r.outbox, tx, entity.SubjectArtistCreated, created); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, toAppErr(err, "failed to commit artist create transaction")
	}

	// Each given artist gets the registered artist of its MBID, in input order.
	result := make([]*entity.Artist, 0, len(artists))
	for _, a := range artists {
		if a == nil {
			continue
		}
		if reg, ok := byMBID[a.MBID]; ok {
			result = append(result, reg)
		}
	}

	r.db.logger.Info(ctx, "artists created",
		slog.String("entityType", "artist"),
		slog.Int("count", len(result)),
	)

	return result, nil
}

// ListByMBIDs retrieves artists matching the provided MusicBrainz IDs.
// The result order matches the input mbids order. Unknown MBIDs are silently skipped.
func (r *ArtistRepository) ListByMBIDs(ctx context.Context, mbids []string) ([]*entity.Artist, error) {
	if len(mbids) == 0 {
		return []*entity.Artist{}, nil
	}

	rows, err := r.db.Pool.Query(ctx, selectArtistsByMBIDsQuery, mbids)
	if err != nil {
		return nil, toAppErr(err, "failed to list artists by mbids", slog.Int("count", len(mbids)))
	}
	defer rows.Close()

	var artists []*entity.Artist
	for rows.Next() {
		a, err := scanArtist(rows.Scan)
		if err != nil {
			return nil, toAppErr(err, "failed to scan artist")
		}
		artists = append(artists, a)
	}
	if err := rows.Err(); err != nil {
		return nil, toAppErr(err, "error iterating artist rows by mbids")
	}
	return artists, nil
}

// List retrieves all artists from the database.
func (r *ArtistRepository) List(ctx context.Context) ([]*entity.Artist, error) {
	rows, err := r.db.Pool.Query(ctx, listArtistsQuery)
	if err != nil {
		return nil, toAppErr(err, "failed to list artists")
	}
	defer rows.Close()

	var artists []*entity.Artist
	for rows.Next() {
		a, err := scanArtist(rows.Scan)
		if err != nil {
			return nil, toAppErr(err, "failed to scan artist")
		}
		artists = append(artists, a)
	}
	if err := rows.Err(); err != nil {
		return nil, toAppErr(err, "error iterating artist rows")
	}
	return artists, nil
}

// Get retrieves an artist by ID.
func (r *ArtistRepository) Get(ctx context.Context, id string) (*entity.Artist, error) {
	row := r.db.Pool.QueryRow(ctx, getArtistQuery, id)
	a, err := scanArtist(row.Scan)
	if err != nil {
		return nil, toAppErr(err, "failed to get artist", slog.String("id", id))
	}
	return a, nil
}

// GetByMBID retrieves an artist by MusicBrainz ID.
func (r *ArtistRepository) GetByMBID(ctx context.Context, mbid string) (*entity.Artist, error) {
	row := r.db.Pool.QueryRow(ctx, getArtistByMBIDQuery, mbid)
	a, err := scanArtist(row.Scan)
	if err != nil {
		return nil, toAppErr(err, "failed to get artist by mbid", slog.String("mbid", mbid))
	}
	return a, nil
}

// UpdateName updates the display name of an artist.
func (r *ArtistRepository) UpdateName(ctx context.Context, id string, name string) error {
	tag, err := r.db.Pool.Exec(ctx, updateArtistNameQuery, id, name)
	if err != nil {
		return toAppErr(err, "failed to update artist name", slog.String("id", id))
	}
	if tag.RowsAffected() == 0 {
		return apperr.New(codes.NotFound, "artist not found")
	}

	r.db.logger.Info(ctx, "artist name updated",
		slog.String("entityType", "artist"),
		slog.String("id", id),
		slog.String("name", name),
	)
	return nil
}

// UpdateActivityEnded sets whether the artist's activity has ended.
func (r *ArtistRepository) UpdateActivityEnded(ctx context.Context, id string, ended bool) error {
	tag, err := r.db.Pool.Exec(ctx, updateArtistActivityEndedQuery, id, ended)
	if err != nil {
		return toAppErr(err, "failed to update artist activity ended", slog.String("id", id))
	}
	if tag.RowsAffected() == 0 {
		return apperr.New(codes.NotFound, "artist not found")
	}
	return nil
}

// ListRefreshDue returns the artists in the given slot out of slots, ordered by id.
func (r *ArtistRepository) ListRefreshDue(ctx context.Context, slots, slot int) ([]*entity.Artist, error) {
	if slots <= 0 || slot < 0 || slot >= slots {
		return nil, apperr.New(codes.InvalidArgument, "slot must be in [0, slots)",
			slog.Int("slots", slots), slog.Int("slot", slot))
	}

	rows, err := r.db.Pool.Query(ctx, listArtistsRefreshDueQuery, slots, slot)
	if err != nil {
		return nil, toAppErr(err, "failed to list refresh-due artists", slog.Int("slot", slot))
	}
	defer rows.Close()

	artists := []*entity.Artist{}
	for rows.Next() {
		a, err := scanArtist(rows.Scan)
		if err != nil {
			return nil, toAppErr(err, "failed to scan artist")
		}
		artists = append(artists, a)
	}
	if err := rows.Err(); err != nil {
		return nil, toAppErr(err, "error iterating refresh-due artist rows")
	}
	return artists, nil
}

// CreateOfficialSite saves the official site for an artist.
func (r *ArtistRepository) CreateOfficialSite(ctx context.Context, site *entity.OfficialSite) error {
	_, err := r.db.Pool.Exec(ctx, insertOfficialSiteQuery, site.ID, site.ArtistID, site.URL)
	if err != nil {
		return toAppErr(err, "failed to create official site", slog.String("artist_id", site.ArtistID))
	}

	r.db.logger.Info(ctx, "official site created",
		slog.String("entityType", "artist_official_site"),
		slog.String("artistID", site.ArtistID),
	)
	return nil
}

// GetOfficialSite retrieves the official site for an artist.
func (r *ArtistRepository) GetOfficialSite(ctx context.Context, artistID string) (*entity.OfficialSite, error) {
	var s entity.OfficialSite
	err := r.db.Pool.QueryRow(ctx, getOfficialSiteQuery, artistID).Scan(
		&s.ID, &s.ArtistID, &s.URL,
	)
	if err != nil {
		return nil, toAppErr(err, "failed to get official site", slog.String("artist_id", artistID))
	}
	return &s, nil
}

// UpdateFanart replaces the cached fanart.tv data for an artist.
func (r *ArtistRepository) UpdateFanart(ctx context.Context, id string, fanart *entity.Fanart, syncTime time.Time) error {
	var fanartJSON []byte
	if fanart != nil {
		var err error
		fanartJSON, err = json.Marshal(fanart)
		if err != nil {
			return apperr.Wrap(err, codes.Internal, "failed to marshal fanart data")
		}
	}

	tag, err := r.db.Pool.Exec(ctx, updateFanartQuery, id, fanartJSON, syncTime)
	if err != nil {
		return toAppErr(err, "failed to update fanart", slog.String("id", id))
	}
	if tag.RowsAffected() == 0 {
		return apperr.New(codes.NotFound, "artist not found")
	}

	r.db.logger.Info(ctx, "artist fanart updated",
		slog.String("entityType", "artist"),
		slog.String("id", id),
	)
	return nil
}

// ListStaleOrMissingFanart returns artists whose images are due for a check at
// now: never checked, or followed by at least one user and last checked longer
// ago than staleAge. Never-checked artists come first, then the oldest checks.
func (r *ArtistRepository) ListStaleOrMissingFanart(ctx context.Context, now time.Time, staleAge time.Duration, limit int) ([]*entity.Artist, error) {
	staleThreshold := now.Add(-staleAge)

	rows, err := r.db.Pool.Query(ctx, listStaleOrMissingFanartQuery, staleThreshold, limit)
	if err != nil {
		return nil, toAppErr(err, "failed to list stale or missing fanart")
	}
	defer rows.Close()

	var artists []*entity.Artist
	for rows.Next() {
		a, err := scanArtist(rows.Scan)
		if err != nil {
			return nil, toAppErr(err, "failed to scan artist")
		}
		artists = append(artists, a)
	}
	if err := rows.Err(); err != nil {
		return nil, toAppErr(err, "error iterating stale fanart rows")
	}
	return artists, nil
}

// ListStaleOfficialSite returns followed artists whose official site is due
// for a check. Never-checked artists come first, then the oldest checks.
func (r *ArtistRepository) ListStaleOfficialSite(ctx context.Context, now time.Time, age time.Duration, limit int) ([]*entity.Artist, error) {
	staleThreshold := now.Add(-age)

	rows, err := r.db.Pool.Query(ctx, listStaleOfficialSiteQuery, staleThreshold, limit)
	if err != nil {
		return nil, toAppErr(err, "failed to list stale official sites")
	}
	defer rows.Close()

	artists := []*entity.Artist{}
	for rows.Next() {
		a, err := scanArtist(rows.Scan)
		if err != nil {
			return nil, toAppErr(err, "failed to scan artist")
		}
		artists = append(artists, a)
	}
	if err := rows.Err(); err != nil {
		return nil, toAppErr(err, "error iterating stale official site rows")
	}
	return artists, nil
}

// UpdateOfficialSiteURL replaces the URL of the artist's official site,
// keeping the site's id.
func (r *ArtistRepository) UpdateOfficialSiteURL(ctx context.Context, artistID, url string) error {
	if err := entity.ValidateOfficialSiteURL(url); err != nil {
		return err
	}

	tag, err := r.db.Pool.Exec(ctx, updateOfficialSiteURLQuery, artistID, url)
	if err != nil {
		return toAppErr(err, "failed to update official site url", slog.String("artist_id", artistID))
	}
	if tag.RowsAffected() == 0 {
		return apperr.New(codes.NotFound, "official site not found")
	}

	r.db.logger.Info(ctx, "official site url updated",
		slog.String("entityType", "artist_official_site"),
		slog.String("artistID", artistID),
	)
	return nil
}

// MarkOfficialSiteChecked records when the artist's official site was last checked.
func (r *ArtistRepository) MarkOfficialSiteChecked(ctx context.Context, artistID string, checkTime time.Time) error {
	tag, err := r.db.Pool.Exec(ctx, markOfficialSiteCheckedQuery, artistID, checkTime)
	if err != nil {
		return toAppErr(err, "failed to mark official site checked", slog.String("artist_id", artistID))
	}
	if tag.RowsAffected() == 0 {
		return apperr.New(codes.NotFound, "artist not found")
	}
	return nil
}
