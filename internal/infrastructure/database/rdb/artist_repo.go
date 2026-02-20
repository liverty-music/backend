// Package rdb provides PostgreSQL database implementations of repository interfaces.
package rdb

import (
	"context"
	"log/slog"

	"github.com/google/uuid"
	"github.com/liverty-music/backend/internal/entity"
)

// ArtistRepository implements entity.ArtistRepository interface for PostgreSQL.
type ArtistRepository struct {
	db *Database
}

const (
	// Artists with non-empty MBID: deduplicate via ON CONFLICT.
	insertArtistsWithMBIDUnnestQuery = `
		INSERT INTO artists (id, name, mbid)
		SELECT * FROM unnest($1::uuid[], $2::text[], $3::varchar[])
		ON CONFLICT (mbid) DO NOTHING
	`
	// Artists without MBID: no conflict target, always insert as new rows.
	insertArtistsNoMBIDUnnestQuery = `
		INSERT INTO artists (id, name)
		SELECT * FROM unnest($1::uuid[], $2::text[])
	`
	selectArtistsByMBIDsQuery = `
		SELECT id, name, COALESCE(mbid, '')
		FROM artists
		WHERE mbid = ANY($1)
	`
	selectArtistsByIDsQuery = `
		SELECT id, name, COALESCE(mbid, '')
		FROM artists
		WHERE id = ANY($1)
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
	listFollowedQuery = `
		SELECT a.id, a.name, a.mbid
		FROM artists a
		JOIN followed_artists fa ON a.id = fa.artist_id
		WHERE fa.user_id = $1
	`
	listAllFollowedQuery = `
		SELECT DISTINCT a.id, a.name, a.mbid
		FROM artists a
		JOIN followed_artists fa ON a.id = fa.artist_id
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
	var withMBIDIDs, withMBIDNames, withMBIDs []string
	var noMBIDIDs, noMBIDNames []string
	var mbidList []string

	for _, a := range artists {
		if a.ID == "" {
			id, _ := uuid.NewV7()
			a.ID = id.String()
		}
		if a.MBID != "" {
			withMBIDIDs = append(withMBIDIDs, a.ID)
			withMBIDNames = append(withMBIDNames, a.Name)
			withMBIDs = append(withMBIDs, a.MBID)
			mbidList = append(mbidList, a.MBID)
		} else {
			noMBIDIDs = append(noMBIDIDs, a.ID)
			noMBIDNames = append(noMBIDNames, a.Name)
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

	// Fetch back all persisted artists (both new and pre-existing) by MBID and ID.
	var result []*entity.Artist

	if len(mbidList) > 0 {
		rows, err := r.db.Pool.Query(ctx, selectArtistsByMBIDsQuery, mbidList)
		if err != nil {
			return nil, toAppErr(err, "failed to select artists by mbids")
		}
		defer rows.Close()

		for rows.Next() {
			var a entity.Artist
			if err := rows.Scan(&a.ID, &a.Name, &a.MBID); err != nil {
				return nil, toAppErr(err, "failed to scan artist")
			}
			result = append(result, &a)
		}
	}

	if len(noMBIDIDs) > 0 {
		rows, err := r.db.Pool.Query(ctx, selectArtistsByIDsQuery, noMBIDIDs)
		if err != nil {
			return nil, toAppErr(err, "failed to select artists by ids")
		}
		defer rows.Close()

		for rows.Next() {
			var a entity.Artist
			if err := rows.Scan(&a.ID, &a.Name, &a.MBID); err != nil {
				return nil, toAppErr(err, "failed to scan artist")
			}
			result = append(result, &a)
		}
	}

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
	return nil
}

// Unfollow removes a follow relationship.
func (r *ArtistRepository) Unfollow(ctx context.Context, userID, artistID string) error {
	_, err := r.db.Pool.Exec(ctx, deleteFollowQuery, userID, artistID)
	if err != nil {
		return toAppErr(err, "failed to unfollow artist", slog.String("user_id", userID), slog.String("artist_id", artistID))
	}
	return nil
}

// ListFollowed retrieves the list of artists followed by a user.
func (r *ArtistRepository) ListFollowed(ctx context.Context, userID string) ([]*entity.Artist, error) {
	rows, err := r.db.Pool.Query(ctx, listFollowedQuery, userID)
	if err != nil {
		return nil, toAppErr(err, "failed to list followed artists", slog.String("user_id", userID))
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
	return artists, nil
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
	return artists, nil
}
