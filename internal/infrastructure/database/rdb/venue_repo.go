package rdb

import (
	"context"
	"log/slog"

	"github.com/liverty-music/backend/internal/entity"
)

// VenueRepository implements entity.VenueRepository interface for PostgreSQL.
type VenueRepository struct {
	db *Database
}

const (
	insertVenueQuery = `
		INSERT INTO venues (id, name, created_at, updated_at)
		VALUES ($1, $2, $3, $4)
	`
	getVenueQuery = `
		SELECT id, name, created_at, updated_at
		FROM venues
		WHERE id = $1
	`
	getVenueByNameQuery = `
		SELECT id, name, created_at, updated_at
		FROM venues
		WHERE name = $1
	`

)

// NewVenueRepository creates a new venue repository instance.
func NewVenueRepository(db *Database) *VenueRepository {
	return &VenueRepository{db: db}
}

// Create creates a new venue in the database.
func (r *VenueRepository) Create(ctx context.Context, venue *entity.Venue) error {
	_, err := r.db.Pool.Exec(ctx, insertVenueQuery, venue.ID, venue.Name, venue.CreateTime, venue.UpdateTime)
	if err != nil {
		return toAppErr(err, "failed to create venue", slog.String("venue_id", venue.ID), slog.String("name", venue.Name))
	}
	return nil
}

// Get retrieves a venue by ID from the database.
func (r *VenueRepository) Get(ctx context.Context, id string) (*entity.Venue, error) {
	var v entity.Venue
	err := r.db.Pool.QueryRow(ctx, getVenueQuery, id).Scan(&v.ID, &v.Name, &v.CreateTime, &v.UpdateTime)
	if err != nil {
		return nil, toAppErr(err, "failed to get venue", slog.String("venue_id", id))
	}
	return &v, nil
}

// GetByName retrieves a venue by Name from the database.
func (r *VenueRepository) GetByName(ctx context.Context, name string) (*entity.Venue, error) {
	var v entity.Venue
	err := r.db.Pool.QueryRow(ctx, getVenueByNameQuery, name).Scan(&v.ID, &v.Name, &v.CreateTime, &v.UpdateTime)
	if err != nil {
		return nil, toAppErr(err, "failed to get venue by name", slog.String("name", name))
	}
	return &v, nil
}
