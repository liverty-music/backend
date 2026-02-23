// Package rdb provides PostgreSQL database implementations of repository interfaces.
package rdb

import (
	"context"
	"log/slog"

	"github.com/google/uuid"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
)

// ArtistRepository implements entity.ArtistRepository interface for PostgreSQL.
type ArtistRepository struct {
	db *Database
}

const (
	// Artists with non-empty MBID: deduplicate via ON CONFLICT.
	// The WHERE clause must match the partial unique index predicate on mbid.
	insertArtistsWithMBIDUnnestQuery = `
		INSERT INTO artists (id, name, mbid)
		SELECT * FROM unnest($1::uuid[], $2::text[], $3::varchar[])
		ON CONFLICT (mbid) WHERE mbid IS NOT NULL AND mbid != '' DO NOTHING
	`
	// Artists without MBID: no conflict target, always insert as new rows.
	insertArtistsNoMBIDUnnestQuery = `
		INSERT INTO artists (id, name)
		SELECT * FROM unnest($1::uuid[], $2::text[])
	`
	// Fetch back MBID artists preserving the input array order via WITH ORDINALITY.
	selectArtistsByMBIDsQuery = `
		SELECT a.id, a.name, COALESCE(a.mbid, '')
		FROM artists a
		JOIN unnest($1::varchar[]) WITH ORDINALITY AS t(mbid, ord) ON a.mbid = t.mbid
		ORDER BY t.ord
	`
	// Fetch back no-MBID artists preserving the input array order via WITH ORDINALITY.
	selectArtistsByIDsQuery = `
		SELECT a.id, a.name, COALESCE(a.mbid, '')
		FROM artists a
		JOIN unnest($1::uuid[]) WITH ORDINALITY AS t(id, ord) ON a.id = t.id
		ORDER BY t.ord
	`
	listArtistsQuery = `
		SELECT id, name, COALESCE(mbid, '')
		FROM artists
	`
	getArtistQuery = `
		SELECT id, name, COALESCE(mbid, '')
		FROM artists
		WHERE id = $1
	`
	getArtistByMBIDQuery = `
		SELECT id, name, COALESCE(mbid, '')
		FROM artists
		WHERE mbid = $1
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
	insertFollowQuery = `
		INSERT INTO followed_artists (user_id, artist_id)
		VALUES ($1, $2)
		ON CONFLICT DO NOTHING
	`
	deleteFollowQuery = `
		DELETE FROM followed_artists
		WHERE user_id = $1 AND artist_id = $2
	`
	setPassionLevelQuery = `
		UPDATE followed_artists
		SET passion_level = $3
		WHERE user_id = $1 AND artist_id = $2
	`
	listFollowedQuery = `
		SELECT a.id, a.name, COALESCE(a.mbid, ''), fa.passion_level
		FROM artists a
		JOIN followed_artists fa ON a.id = fa.artist_id
		WHERE fa.user_id = $1
	`
	listAllFollowedQuery = `
		SELECT DISTINCT a.id, a.name, COALESCE(a.mbid, '')
		FROM artists a
		JOIN followed_artists fa ON a.id = fa.artist_id
	`
	listFollowersQuery = `
		SELECT u.id, u.external_id, u.email, u.name, u.preferred_language, u.country, u.time_zone, COALESCE(u.safe_address, ''), u.is_active
		FROM users u
		JOIN followed_artists fa ON fa.user_id = u.id
		WHERE fa.artist_id = $1
	`
)

// NewArtistRepository creates a new artist repository instance.
func NewArtistRepository(db *Database) *ArtistRepository {
	return &ArtistRepository{db: db}
}

// Create persists one or more artists using unnest bulk upsert.
// Artists with matching MBIDs are deduplicated. Returns all artists with valid database IDs.
func (r *ArtistRepository) Create(ctx context.Context, artists ...*entity.Artist) ([]*entity.Artist, error) {
	if len(artists) == 0 {
		return []*entity.Artist{}, nil
	}

	// Split artists into two groups: those with MBID (deduplicatable) and those without.
	// Track each artist's original input index so we can restore order in the result.
	var withMBIDIDs, withMBIDNames, withMBIDs []string
	var noMBIDIDs, noMBIDNames []string
	var mbidList []string
	// mbidInputIdx[i] = original index of the i-th MBID artist in the input slice.
	var mbidInputIdx []int
	// noMBIDInputIdx[i] = original index of the i-th no-MBID artist in the input slice.
	var noMBIDInputIdx []int

	for origIdx, a := range artists {
		if a == nil {
			continue
		}
		if a.ID == "" {
			id, _ := uuid.NewV7()
			a.ID = id.String()
		}
		if a.MBID != "" {
			withMBIDIDs = append(withMBIDIDs, a.ID)
			withMBIDNames = append(withMBIDNames, a.Name)
			withMBIDs = append(withMBIDs, a.MBID)
			mbidList = append(mbidList, a.MBID)
			mbidInputIdx = append(mbidInputIdx, origIdx)
		} else {
			noMBIDIDs = append(noMBIDIDs, a.ID)
			noMBIDNames = append(noMBIDNames, a.Name)
			noMBIDInputIdx = append(noMBIDInputIdx, origIdx)
		}
	}

	if len(withMBIDIDs) > 0 {
		if _, err := r.db.Pool.Exec(ctx, insertArtistsWithMBIDUnnestQuery, withMBIDIDs, withMBIDNames, withMBIDs); err != nil {
			return nil, toAppErr(err, "failed to bulk insert artists with MBID", slog.Int("count", len(withMBIDIDs)))
		}
	}
	if len(noMBIDIDs) > 0 {
		if _, err := r.db.Pool.Exec(ctx, insertArtistsNoMBIDUnnestQuery, noMBIDIDs, noMBIDNames); err != nil {
			return nil, toAppErr(err, "failed to bulk insert artists without MBID", slog.Int("count", len(noMBIDIDs)))
		}
	}

	// Fetch back all persisted artists (both new and pre-existing) by MBID and ID,
	// preserving the input order via WITH ORDINALITY.
	// resultByOrigIdx maps original input index → fetched artist, so we can merge
	// the two groups (MBID and no-MBID) back into the caller's original order.
	resultByOrigIdx := make(map[int]*entity.Artist)

	if len(mbidList) > 0 {
		rows, err := r.db.Pool.Query(ctx, selectArtistsByMBIDsQuery, mbidList)
		if err != nil {
			return nil, toAppErr(err, "failed to select artists by mbids")
		}
		defer rows.Close()

		i := 0
		for rows.Next() {
			var a entity.Artist
			if err := rows.Scan(&a.ID, &a.Name, &a.MBID); err != nil {
				return nil, toAppErr(err, "failed to scan artist")
			}
			resultByOrigIdx[mbidInputIdx[i]] = &a
			i++
		}
		if err := rows.Err(); err != nil {
			return nil, toAppErr(err, "error iterating artist rows by mbids")
		}
	}

	if len(noMBIDIDs) > 0 {
		rows, err := r.db.Pool.Query(ctx, selectArtistsByIDsQuery, noMBIDIDs)
		if err != nil {
			return nil, toAppErr(err, "failed to select artists by ids")
		}
		defer rows.Close()

		i := 0
		for rows.Next() {
			var a entity.Artist
			if err := rows.Scan(&a.ID, &a.Name, &a.MBID); err != nil {
				return nil, toAppErr(err, "failed to scan artist")
			}
			resultByOrigIdx[noMBIDInputIdx[i]] = &a
			i++
		}
		if err := rows.Err(); err != nil {
			return nil, toAppErr(err, "error iterating artist rows by ids")
		}
	}

	// Reassemble in original input order, skipping nil entries.
	result := make([]*entity.Artist, 0, len(resultByOrigIdx))
	for origIdx := range artists {
		if a, ok := resultByOrigIdx[origIdx]; ok {
			result = append(result, a)
		}
	}

	r.db.logger.Info(ctx, "artists created",
		slog.String("entityType", "artist"),
		slog.Int("count", len(result)),
	)

	return result, nil
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
		var a entity.Artist
		if err := rows.Scan(&a.ID, &a.Name, &a.MBID); err != nil {
			return nil, toAppErr(err, "failed to scan artist")
		}
		artists = append(artists, &a)
	}
	if err := rows.Err(); err != nil {
		return nil, toAppErr(err, "error iterating artist rows")
	}
	return artists, nil
}

// Get retrieves an artist by ID.
func (r *ArtistRepository) Get(ctx context.Context, id string) (*entity.Artist, error) {
	var a entity.Artist
	err := r.db.Pool.QueryRow(ctx, getArtistQuery, id).Scan(&a.ID, &a.Name, &a.MBID)
	if err != nil {
		return nil, toAppErr(err, "failed to get artist", slog.String("id", id))
	}
	return &a, nil
}

// GetByMBID retrieves an artist by MusicBrainz ID.
func (r *ArtistRepository) GetByMBID(ctx context.Context, mbid string) (*entity.Artist, error) {
	var a entity.Artist
	err := r.db.Pool.QueryRow(ctx, getArtistByMBIDQuery, mbid).Scan(&a.ID, &a.Name, &a.MBID)
	if err != nil {
		return nil, toAppErr(err, "failed to get artist by mbid", slog.String("mbid", mbid))
	}
	return &a, nil
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

// Follow establishes a follow relationship between a user and an artist.
func (r *ArtistRepository) Follow(ctx context.Context, userID, artistID string) error {
	_, err := r.db.Pool.Exec(ctx, insertFollowQuery, userID, artistID)
	if err != nil {
		return toAppErr(err, "failed to follow artist", slog.String("user_id", userID), slog.String("artist_id", artistID))
	}

	r.db.logger.Info(ctx, "artist followed",
		slog.String("entityType", "followed_artists"),
		slog.String("userID", userID),
		slog.String("artistID", artistID),
	)
	return nil
}

// Unfollow removes a follow relationship.
func (r *ArtistRepository) Unfollow(ctx context.Context, userID, artistID string) error {
	_, err := r.db.Pool.Exec(ctx, deleteFollowQuery, userID, artistID)
	if err != nil {
		return toAppErr(err, "failed to unfollow artist", slog.String("user_id", userID), slog.String("artist_id", artistID))
	}

	r.db.logger.Info(ctx, "artist unfollowed",
		slog.String("entityType", "followed_artists"),
		slog.String("userID", userID),
		slog.String("artistID", artistID),
	)
	return nil
}

// SetPassionLevel updates the enthusiasm tier for a followed artist.
func (r *ArtistRepository) SetPassionLevel(ctx context.Context, userID, artistID string, level entity.PassionLevel) error {
	tag, err := r.db.Pool.Exec(ctx, setPassionLevelQuery, userID, artistID, string(level))
	if err != nil {
		return toAppErr(err, "failed to set passion level", slog.String("user_id", userID), slog.String("artist_id", artistID))
	}
	if tag.RowsAffected() == 0 {
		return apperr.New(codes.NotFound, "follow relationship not found")
	}

	r.db.logger.Info(ctx, "passion level updated",
		slog.String("entityType", "followed_artists"),
		slog.String("userID", userID),
		slog.String("artistID", artistID),
		slog.String("level", string(level)),
	)
	return nil
}

// ListFollowed retrieves the list of artists followed by a user, including passion level.
func (r *ArtistRepository) ListFollowed(ctx context.Context, userID string) ([]*entity.FollowedArtist, error) {
	rows, err := r.db.Pool.Query(ctx, listFollowedQuery, userID)
	if err != nil {
		return nil, toAppErr(err, "failed to list followed artists", slog.String("user_id", userID))
	}
	defer rows.Close()

	var followed []*entity.FollowedArtist
	for rows.Next() {
		var a entity.Artist
		var level string
		if err := rows.Scan(&a.ID, &a.Name, &a.MBID, &level); err != nil {
			return nil, toAppErr(err, "failed to scan followed artist")
		}
		followed = append(followed, &entity.FollowedArtist{
			Artist:       &a,
			PassionLevel: entity.PassionLevel(level),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, toAppErr(err, "error iterating followed artist rows")
	}
	return followed, nil
}

// ListAllFollowed retrieves all distinct artists followed by any user.
func (r *ArtistRepository) ListAllFollowed(ctx context.Context) ([]*entity.Artist, error) {
	rows, err := r.db.Pool.Query(ctx, listAllFollowedQuery)
	if err != nil {
		return nil, toAppErr(err, "failed to list all followed artists")
	}
	defer rows.Close()

	var artists []*entity.Artist
	for rows.Next() {
		var a entity.Artist
		if err := rows.Scan(&a.ID, &a.Name, &a.MBID); err != nil {
			return nil, toAppErr(err, "failed to scan followed artist")
		}
		artists = append(artists, &a)
	}
	if err := rows.Err(); err != nil {
		return nil, toAppErr(err, "error iterating all followed artist rows")
	}
	return artists, nil
}

// ListFollowers retrieves all users who follow the given artist.
func (r *ArtistRepository) ListFollowers(ctx context.Context, artistID string) ([]*entity.User, error) {
	rows, err := r.db.Pool.Query(ctx, listFollowersQuery, artistID)
	if err != nil {
		return nil, toAppErr(err, "failed to list followers", slog.String("artist_id", artistID))
	}
	defer rows.Close()

	var users []*entity.User
	for rows.Next() {
		var u entity.User
		if err := rows.Scan(
			&u.ID, &u.ExternalID, &u.Email, &u.Name,
			&u.PreferredLanguage, &u.Country, &u.TimeZone,
			&u.SafeAddress, &u.IsActive,
		); err != nil {
			return nil, toAppErr(err, "failed to scan follower row")
		}
		users = append(users, &u)
	}
	if err := rows.Err(); err != nil {
		return nil, toAppErr(err, "error iterating follower rows")
	}
	return users, nil
}
