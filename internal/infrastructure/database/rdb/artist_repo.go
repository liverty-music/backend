// Package rdb provides PostgreSQL database implementations of repository interfaces.
package rdb

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
)

// ArtistRepository implements entity.ArtistRepository interface for PostgreSQL.
type ArtistRepository struct {
	db *Database
}

const (
	insertArtistQuery = `
		INSERT INTO artists (
			id, name, spotify_id, musicbrainz_id, genres, country, image_url, created_at, updated_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, NOW(), NOW())
	`
	listArtistsQuery = `
		SELECT
			id, name, spotify_id, musicbrainz_id, genres, country, image_url, created_at, updated_at
		FROM artists
	`
	getArtistQuery = `
		SELECT
			id, name, spotify_id, musicbrainz_id, genres, country, image_url, created_at, updated_at
		FROM artists
		WHERE id = $1
	`
	listArtistMediaQuery = `
		SELECT id, artist_id, type, url, created_at, updated_at
		FROM artist_media
		WHERE artist_id = $1
	`
	insertArtistMediaQuery = `
		INSERT INTO artist_media (id, artist_id, type, url, created_at, updated_at)
		VALUES ($1, $2, $3, $4, NOW(), NOW())
	`
	deleteArtistMediaQuery = `
		DELETE FROM artist_media WHERE id = $1
	`
)

// NewArtistRepository creates a new artist repository instance.
func NewArtistRepository(db *Database) *ArtistRepository {
	return &ArtistRepository{db: db}
}

// Create creates a new artist in the database.
func (r *ArtistRepository) Create(ctx context.Context, artist *entity.Artist) error {
	tx, err := r.db.Pool.Begin(ctx)
	if err != nil {
		return toAppErr(err, "failed to begin transaction")
	}
	defer func() {
		_ = tx.Rollback(ctx) // Rollback is safe to call even after commit
	}()

	_, err = tx.Exec(ctx, insertArtistQuery,
		artist.ID, artist.Name, artist.SpotifyID, artist.MusicBrainzID, artist.Genres, artist.Country, artist.ImageURL,
	)
	if err != nil {
		return toAppErr(err, "failed to insert artist", slog.String("artist_id", artist.ID), slog.String("name", artist.Name))
	}

	for _, m := range artist.Media {
		if err := r.addMediaWithTx(ctx, tx, m); err != nil {
			return err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return toAppErr(err, "failed to commit transaction")
	}

	return nil
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
		err := rows.Scan(
			&a.ID, &a.Name, &a.SpotifyID, &a.MusicBrainzID, &a.Genres, &a.Country, &a.ImageURL, &a.CreatedAt, &a.UpdatedAt,
		)
		if err != nil {
			return nil, toAppErr(err, "failed to scan artist")
		}
		artists = append(artists, &a)
	}
	return artists, nil
}

// Get retrieves an artist by ID from the database.
func (r *ArtistRepository) Get(ctx context.Context, id string) (*entity.Artist, error) {
	var a entity.Artist
	err := r.db.Pool.QueryRow(ctx, getArtistQuery, id).Scan(
		&a.ID, &a.Name, &a.SpotifyID, &a.MusicBrainzID, &a.Genres, &a.Country, &a.ImageURL, &a.CreatedAt, &a.UpdatedAt,
	)
	if err != nil {
		return nil, toAppErr(err, "failed to get artist", slog.String("artist_id", id))
	}

	// Fetch media
	rows, err := r.db.Pool.Query(ctx, listArtistMediaQuery, id)
	if err != nil {
		return nil, toAppErr(err, "failed to query artist media")
	}
	defer rows.Close()

	for rows.Next() {
		var m entity.Media
		if err := rows.Scan(&m.ID, &m.ArtistID, &m.Type, &m.URL, &m.CreatedAt, &m.UpdatedAt); err != nil {
			return nil, toAppErr(err, "failed to scan artist media")
		}
		a.Media = append(a.Media, &m)
	}

	return &a, nil
}

// AddMedia adds a new media record for an artist.
func (r *ArtistRepository) AddMedia(ctx context.Context, media *entity.Media) error {
	_, err := r.db.Pool.Exec(ctx, insertArtistMediaQuery, media.ID, media.ArtistID, media.Type, media.URL)
	if err != nil {
		return toAppErr(err, "failed to add media", slog.String("media_id", media.ID), slog.String("artist_id", media.ArtistID))
	}
	return nil
}

// addMediaWithTx adds a new media record for an artist using a transaction.
func (r *ArtistRepository) addMediaWithTx(ctx context.Context, tx pgx.Tx, media *entity.Media) error {
	_, err := tx.Exec(ctx, insertArtistMediaQuery, media.ID, media.ArtistID, media.Type, media.URL)
	if err != nil {
		return toAppErr(err, "failed to add media with tx", slog.String("media_id", media.ID), slog.String("artist_id", media.ArtistID))
	}
	return nil
}

// DeleteMedia removes a media record from the database.
func (r *ArtistRepository) DeleteMedia(ctx context.Context, mediaID string) error {
	result, err := r.db.Pool.Exec(ctx, deleteArtistMediaQuery, mediaID)
	if err != nil {
		return toAppErr(err, "failed to delete media", slog.String("media_id", mediaID))
	}
	if result.RowsAffected() == 0 {
		return apperr.Wrap(apperr.ErrNotFound, codes.NotFound, fmt.Sprintf("media with ID %s not found", mediaID))
	}
	return nil
}
