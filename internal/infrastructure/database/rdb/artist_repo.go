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
	// Artists: an artist is inserted only when neither of its MBIDs is the
	// mbid or listening_mbid of a registered artist. The check runs under
	// artistMBIDLockQuery; ON CONFLICT is the unique indexes' backstop.
	insertArtistsWithMBIDUnnestQuery = `
		INSERT INTO artists (id, name, mbid, listening_mbid, mbid_status, activity_ended)
		SELECT t.id, t.name, t.mbid, t.listening_mbid, t.mbid_status, t.activity_ended
		FROM unnest($1::uuid[], $2::text[], $3::text[], $4::text[], $5::text[], $6::bool[])
			AS t(id, name, mbid, listening_mbid, mbid_status, activity_ended)
		WHERE NOT EXISTS (
			SELECT 1 FROM artists x
			WHERE x.mbid IN (t.mbid, t.listening_mbid) OR x.listening_mbid IN (t.mbid, t.listening_mbid)
		)
		ON CONFLICT DO NOTHING
		RETURNING id
	`
	// artistMBIDLockQuery takes the lock UpdateMBID and Create share, so a
	// registration cannot slip an MBID in between UpdateMBID's check and its
	// update. It is held to the end of the transaction.
	artistMBIDLockQuery = `SELECT pg_advisory_xact_lock(hashtextextended('artists.mbid', 0))`
	// Fetch back the artists of the given MBIDs, an MBID matching the artist's
	// mbid or its listening_mbid, once per artist in the order of the first
	// given MBID that finds it (WITH ORDINALITY).
	selectArtistsByMBIDsQuery = `
		SELECT a.id, a.name, a.mbid, a.fanart, a.fanart_sync_time, a.official_site_check_time, a.activity_ended, a.listening_mbid, a.mbid_status, ` + artistOrganizerColumn + `
		FROM artists a
		JOIN LATERAL (
			SELECT min(t.ord) AS ord
			FROM unnest($1::varchar[]) WITH ORDINALITY AS t(mbid, ord)
			WHERE a.mbid = t.mbid OR a.listening_mbid = t.mbid
		) m ON m.ord IS NOT NULL
		ORDER BY m.ord
	`
	listArtistsQuery = `
		SELECT a.id, a.name, a.mbid, a.fanart, a.fanart_sync_time, a.official_site_check_time, a.activity_ended, a.listening_mbid, a.mbid_status, ` + artistOrganizerColumn + `
		FROM artists a
	`
	getArtistQuery = `
		SELECT a.id, a.name, a.mbid, a.fanart, a.fanart_sync_time, a.official_site_check_time, a.activity_ended, a.listening_mbid, a.mbid_status, ` + artistOrganizerColumn + `
		FROM artists a
		WHERE a.id = $1
	`
	getArtistByMBIDQuery = `
		SELECT a.id, a.name, a.mbid, a.fanart, a.fanart_sync_time, a.official_site_check_time, a.activity_ended, a.listening_mbid, a.mbid_status, ` + artistOrganizerColumn + `
		FROM artists a
		WHERE a.mbid = $1
	`
	updateFanartQuery = `
		UPDATE artists SET fanart = $2, fanart_sync_time = $3 WHERE id = $1
	`
	listStaleOrMissingFanartQuery = `
		SELECT a.id, a.name, a.mbid, a.fanart, a.fanart_sync_time, a.official_site_check_time, a.activity_ended, a.listening_mbid, a.mbid_status, ` + artistOrganizerColumn + `
		FROM artists a
		WHERE a.fanart_sync_time IS NULL
		   OR (a.fanart_sync_time < $1
		       AND EXISTS (SELECT 1 FROM followed_artists f WHERE f.artist_id = a.id))
		ORDER BY a.fanart_sync_time ASC NULLS FIRST, a.id ASC
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
		SELECT a.id, a.name, a.mbid, a.fanart, a.fanart_sync_time, a.official_site_check_time, a.activity_ended, a.listening_mbid, a.mbid_status, ` + artistOrganizerColumn + `
		FROM artists a
		WHERE ((get_byte(uuid_send(a.id), 14) << 8) | get_byte(uuid_send(a.id), 15)) % $1 = $2
		ORDER BY a.id
	`
	listStaleOfficialSiteQuery = `
		SELECT a.id, a.name, a.mbid, a.fanart, a.fanart_sync_time, a.official_site_check_time, a.activity_ended, a.listening_mbid, a.mbid_status, ` + artistOrganizerColumn + `
		FROM artists a
		WHERE EXISTS (SELECT 1 FROM followed_artists f WHERE f.artist_id = a.id)
		  AND (a.official_site_check_time IS NULL OR a.official_site_check_time < $1)
		ORDER BY a.official_site_check_time ASC NULLS FIRST, a.id ASC
		LIMIT $2
	`
	updateOfficialSiteURLQuery = `
		UPDATE artist_official_site SET url = $2 WHERE artist_id = $1
	`
	listArtistsByMBIDStatusQuery = `
		SELECT a.id, a.name, a.mbid, a.fanart, a.fanart_sync_time, a.official_site_check_time, a.activity_ended, a.listening_mbid, a.mbid_status, ` + artistOrganizerColumn + `
		FROM artists a
		WHERE a.mbid_status = $1
		ORDER BY a.id
	`
	markArtistMBIDAmbiguousQuery = `
		UPDATE artists SET mbid_status = 'ambiguous' WHERE id = $1
	`
	lockArtistForMBIDUpdateQuery = `
		SELECT mbid FROM artists WHERE id = $1 FOR UPDATE
	`
	artistMBIDTakenQuery = `
		SELECT EXISTS (
			SELECT 1 FROM artists
			WHERE id <> $1 AND (mbid = $2 OR listening_mbid = $2)
		)
	`
	// What was read from the replaced MBID's person is cleared with it;
	// listening_mbid is kept.
	updateArtistMBIDQuery = `
		UPDATE artists
		SET mbid = $2,
		    name = $3,
		    activity_ended = $4,
		    mbid_status = 'verified',
		    fanart = NULL,
		    fanart_sync_time = NULL,
		    official_site_check_time = NULL
		WHERE id = $1
	`
	verifyArtistMBIDQuery = `
		UPDATE artists SET name = $2, activity_ended = $3, mbid_status = 'verified' WHERE id = $1
	`
	deleteArtistOfficialSiteQuery = `
		DELETE FROM artist_official_site WHERE artist_id = $1
	`
	markOfficialSiteCheckedQuery = `
		UPDATE artists SET official_site_check_time = $2 WHERE id = $1
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
// fanart_sync_time, official_site_check_time, activity_ended, listening_mbid,
// mbid_status and artistOrganizerColumn, in that order.
func scanArtist(scan func(dest ...any) error) (*entity.Artist, error) {
	var a entity.Artist
	var fanartJSON []byte
	var syncedAt *time.Time
	var officialSiteCheckedAt sql.NullTime

	if err := scan(&a.ID, &a.Name, &a.MBID, &fanartJSON, &syncedAt, &officialSiteCheckedAt, &a.ActivityEnded, &a.ListeningMBID, &a.MBIDStatus, &a.OrganizerID); err != nil {
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

// Create registers the artists entity.MergeArtistsByMBID keeps, no two
// sharing an MBID, with an unnest bulk insert, after taking the lock
// UpdateMBID shares. An artist either of whose MBIDs is the mbid or
// listening_mbid of a registered artist (also one a concurrent call
// registered first) registers nothing. It returns, for each given artist in
// input order, the registered artist found under either of its MBIDs.
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
	cols := newArtistColumns(merged)

	tx, err := r.db.Pool.Begin(ctx)
	if err != nil {
		return nil, toAppErr(err, "failed to begin artist create transaction")
	}
	// Rollback is a no-op after a successful Commit.
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, artistMBIDLockQuery); err != nil {
		return nil, toAppErr(err, "failed to lock artist mbids")
	}

	inserted, err := insertArtists(ctx, tx, cols)
	if err != nil {
		return nil, err
	}

	// Fetch back all registered artists (new and pre-existing) by MBID.
	byMBID, created, err := fetchRegistered(ctx, tx, givenMBIDs(artists), inserted)
	if err != nil {
		return nil, err
	}
	if err := announceAll(ctx, r.outbox, tx, entity.SubjectArtistCreated, created); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, toAppErr(err, "failed to commit artist create transaction")
	}

	result := registeredInInputOrder(artists, byMBID)

	r.db.logger.Info(ctx, "artists created",
		slog.String("entityType", "artist"),
		slog.Int("count", len(result)),
	)

	return result, nil
}

// fetchRegistered reads the artists of the given MBIDs, an MBID matching an
// artist's mbid or listening_mbid. It returns them keyed by both MBIDs, and
// the ones whose ids are in inserted, in read order.
func fetchRegistered(ctx context.Context, tx pgx.Tx, mbids []string, inserted map[string]bool) (map[string]*entity.Artist, []*entity.Artist, error) {
	rows, err := tx.Query(ctx, selectArtistsByMBIDsQuery, mbids)
	if err != nil {
		return nil, nil, toAppErr(err, "failed to select artists by mbids")
	}
	registered, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (*entity.Artist, error) {
		return scanArtist(row.Scan)
	})
	if err != nil {
		return nil, nil, toAppErr(err, "failed to scan artists by mbids")
	}

	byMBID := make(map[string]*entity.Artist, len(registered))
	var created []*entity.Artist
	for _, a := range registered {
		byMBID[a.MBID] = a
		byMBID[a.ListeningMBID] = a
		if inserted[a.ID] {
			created = append(created, a)
		}
	}
	return byMBID, created, nil
}

// artistColumns are the parallel columns of the bulk insert.
type artistColumns struct {
	ids, names, mbids, listeningMBIDs, statuses []string
	activityEnded                               []bool
}

// newArtistColumns returns the artists as the parallel columns of the bulk
// insert, giving an artist with no id a new one and an artist with no
// MBIDStatus Unverified.
func newArtistColumns(artists []*entity.Artist) artistColumns {
	var c artistColumns
	for _, a := range artists {
		if a.ID == "" {
			a.ID = entity.NewID()
		}
		status := a.MBIDStatus
		if status == "" {
			status = entity.MBIDStatusUnverified
		}
		c.ids = append(c.ids, a.ID)
		c.names = append(c.names, a.Name)
		c.mbids = append(c.mbids, a.MBID)
		c.listeningMBIDs = append(c.listeningMBIDs, a.ListeningMBID)
		c.statuses = append(c.statuses, string(status))
		c.activityEnded = append(c.activityEnded, a.ActivityEnded)
	}
	return c
}

// givenMBIDs returns every MBID and listening MBID of the given artists (nil
// ones skipped).
func givenMBIDs(artists []*entity.Artist) []string {
	mbids := make([]string, 0, 2*len(artists))
	for _, a := range artists {
		if a != nil {
			mbids = append(mbids, a.ListeningMBID, a.MBID)
		}
	}
	return mbids
}

// registeredInInputOrder gives each given artist (nil ones skipped) the
// registered artist found under its listening MBID or its MBID, in input
// order.
func registeredInInputOrder(artists []*entity.Artist, byMBID map[string]*entity.Artist) []*entity.Artist {
	result := make([]*entity.Artist, 0, len(artists))
	for _, a := range artists {
		if a == nil {
			continue
		}
		if reg, ok := byMBID[a.ListeningMBID]; ok {
			result = append(result, reg)
		} else if reg, ok := byMBID[a.MBID]; ok {
			result = append(result, reg)
		}
	}
	return result
}

// insertArtists bulk-inserts the artists and returns the ids this call
// inserted: RETURNING yields only those, since an artist found under a
// registered MBID, also one a concurrent call inserted first, inserts nothing.
func insertArtists(ctx context.Context, tx pgx.Tx, c artistColumns) (map[string]bool, error) {
	inserted := map[string]bool{}
	if len(c.ids) == 0 {
		return inserted, nil
	}
	insRows, err := tx.Query(ctx, insertArtistsWithMBIDUnnestQuery, c.ids, c.names, c.mbids, c.listeningMBIDs, c.statuses, c.activityEnded)
	if err != nil {
		return nil, toAppErr(err, "failed to bulk insert artists with MBID", slog.Int("count", len(c.ids)))
	}
	newIDs, err := pgx.CollectRows(insRows, pgx.RowTo[string])
	if err != nil {
		return nil, toAppErr(err, "failed to bulk insert artists with MBID", slog.Int("count", len(c.ids)))
	}
	for _, id := range newIDs {
		inserted[id] = true
	}
	return inserted, nil
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

// UpdateMBID sets the artist's MBID, name and activity status and marks its
// MBID Verified, keeping its listening_mbid; a changed MBID also clears what
// was read from the replaced MBID's person (images, official site, check
// time). It runs in one transaction under the lock Create shares and the
// artist's row lock.
//
// # Announces
//
// Nothing.
func (r *ArtistRepository) UpdateMBID(ctx context.Context, id, mbid, name string, activityEnded bool) error {
	if err := entity.ValidateMBID(mbid); err != nil {
		return err
	}

	tx, err := r.db.Pool.Begin(ctx)
	if err != nil {
		return toAppErr(err, "failed to begin artist mbid update transaction")
	}
	// Rollback is a no-op after a successful Commit.
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, artistMBIDLockQuery); err != nil {
		return toAppErr(err, "failed to lock artist mbids")
	}

	var current string
	if err := tx.QueryRow(ctx, lockArtistForMBIDUpdateQuery, id).Scan(&current); err != nil {
		return toAppErr(err, "failed to lock artist for mbid update", slog.String("id", id))
	}
	if current == mbid {
		if _, err := tx.Exec(ctx, verifyArtistMBIDQuery, id, name, activityEnded); err != nil {
			return toAppErr(err, "failed to verify artist mbid", slog.String("id", id))
		}
		if err := tx.Commit(ctx); err != nil {
			return toAppErr(err, "failed to commit artist mbid update transaction")
		}
		return nil
	}

	var taken bool
	if err := tx.QueryRow(ctx, artistMBIDTakenQuery, id, mbid).Scan(&taken); err != nil {
		return toAppErr(err, "failed to check whether the mbid belongs to another artist", slog.String("id", id))
	}
	if taken {
		return apperr.New(codes.AlreadyExists, "another artist has the mbid", slog.String("id", id), slog.String("mbid", mbid))
	}

	if _, err := tx.Exec(ctx, updateArtistMBIDQuery, id, mbid, name, activityEnded); err != nil {
		return toAppErr(err, "failed to update artist mbid", slog.String("id", id))
	}
	if _, err := tx.Exec(ctx, deleteArtistOfficialSiteQuery, id); err != nil {
		return toAppErr(err, "failed to remove the official site of the replaced mbid", slog.String("id", id))
	}
	if err := tx.Commit(ctx); err != nil {
		return toAppErr(err, "failed to commit artist mbid update transaction")
	}

	r.db.logger.Info(ctx, "artist mbid replaced",
		slog.String("entityType", "artist"),
		slog.String("id", id),
		slog.String("old_mbid", current),
		slog.String("new_mbid", mbid),
	)
	return nil
}

// MarkMBIDAmbiguous sets the artist's MBID status to ambiguous.
//
// # Announces
//
// Nothing.
func (r *ArtistRepository) MarkMBIDAmbiguous(ctx context.Context, id string) error {
	tag, err := r.db.Pool.Exec(ctx, markArtistMBIDAmbiguousQuery, id)
	if err != nil {
		return toAppErr(err, "failed to mark artist mbid ambiguous", slog.String("id", id))
	}
	if tag.RowsAffected() == 0 {
		return apperr.New(codes.NotFound, "artist not found", slog.String("id", id))
	}
	return nil
}

// ListByMBIDStatus returns the artists in the MBID status, oldest first: ids
// are UUIDv7, so id order is registration order.
func (r *ArtistRepository) ListByMBIDStatus(ctx context.Context, status entity.MBIDStatus) ([]*entity.Artist, error) {
	rows, err := r.db.Pool.Query(ctx, listArtistsByMBIDStatusQuery, string(status))
	if err != nil {
		return nil, toAppErr(err, "failed to list artists by mbid status", slog.String("mbid_status", string(status)))
	}
	artists, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (*entity.Artist, error) {
		return scanArtist(row.Scan)
	})
	if err != nil {
		return nil, toAppErr(err, "failed to scan artists by mbid status")
	}
	if artists == nil {
		artists = []*entity.Artist{}
	}
	return artists, nil
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
