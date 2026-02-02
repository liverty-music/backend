// Package rdb provides PostgreSQL database implementations of repository interfaces.
package rdb

import (
	"context"
	"log/slog"

	"github.com/liverty-music/backend/internal/entity"
)

// ArtistRepository implements entity.ArtistRepository interface for PostgreSQL.
type ArtistRepository struct {
	db *Database
}

const (
	insertArtistQuery = `
		INSERT INTO artists (id, name, created_at, updated_at)
		VALUES ($1, $2, $3, $4)
	`
	listArtistsQuery = `
		SELECT id, name, created_at, updated_at
		FROM artists
	`
	getArtistQuery = `
		SELECT id, name, created_at, updated_at
		FROM artists
		WHERE id = $1
	`
	getOfficialSiteQuery = `
		SELECT id, artist_id, url, created_at, updated_at
		FROM artist_official_sites
		WHERE artist_id = $1
	`
	insertOfficialSiteQuery = `
		INSERT INTO artist_official_sites (id, artist_id, url, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5)
	`
)

// NewArtistRepository creates a new artist repository instance.
func NewArtistRepository(db *Database) *ArtistRepository {
	return &ArtistRepository{db: db}
}

// Create creates a new artist in the database.
func (r *ArtistRepository) Create(ctx context.Context, artist *entity.Artist) error {
	_, err := r.db.Pool.Exec(ctx, insertArtistQuery, artist.ID, artist.Name, artist.CreateTime, artist.UpdateTime)
	if err != nil {
		return toAppErr(err, "failed to insert artist", slog.String("artist_id", artist.ID), slog.String("name", artist.Name))
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
		if err := rows.Scan(&a.ID, &a.Name, &a.CreateTime, &a.UpdateTime); err != nil {
			return nil, toAppErr(err, "failed to scan artist")
		}
		artists = append(artists, &a)
	}
	return artists, nil
}

// Get retrieves an artist by ID from the database.
func (r *ArtistRepository) Get(ctx context.Context, id string) (*entity.Artist, error) {
	var a entity.Artist
	err := r.db.Pool.QueryRow(ctx, getArtistQuery, id).Scan(&a.ID, &a.Name, &a.CreateTime, &a.UpdateTime)
	if err != nil {
		return nil, toAppErr(err, "failed to get artist", slog.String("artist_id", id))
	}
	return &a, nil
}

// CreateOfficialSite saves the official site for an artist.
func (r *ArtistRepository) CreateOfficialSite(ctx context.Context, site *entity.OfficialSite) error {
	_, err := r.db.Pool.Exec(ctx, insertOfficialSiteQuery, site.ID, site.ArtistID, site.URL, site.CreateTime, site.UpdateTime)
	if err != nil {
		return toAppErr(err, "failed to create official site", slog.String("artist_id", site.ArtistID))
	}
	return nil
}

// GetOfficialSite retrieves the official site for an artist.
func (r *ArtistRepository) GetOfficialSite(ctx context.Context, artistID string) (*entity.OfficialSite, error) {
	var s entity.OfficialSite
	err := r.db.Pool.QueryRow(ctx, getOfficialSiteQuery, artistID).Scan(
		&s.ID, &s.ArtistID, &s.URL, &s.CreateTime, &s.UpdateTime,
	)
	if err != nil {
		return nil, toAppErr(err, "failed to get official site", slog.String("artist_id", artistID))
	}
	return &s, nil
}
