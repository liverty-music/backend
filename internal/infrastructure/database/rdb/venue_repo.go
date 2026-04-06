package rdb

import (
	"context"
	"log/slog"

	"github.com/liverty-music/backend/internal/entity"
)

// VenueRepository implements entity.VenueRepository for PostgreSQL.
type VenueRepository struct {
	db *Database
}

const (
	insertVenueQuery = `
		INSERT INTO venues (id, name, admin_area, google_place_id, latitude, longitude, listed_venue_name)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`
	getVenueQuery = `
		SELECT id, name, admin_area, google_place_id, latitude, longitude, listed_venue_name
		FROM venues
		WHERE id = $1
	`
	getVenueByPlaceIDQuery = `
		SELECT id, name, admin_area, google_place_id, latitude, longitude, listed_venue_name
		FROM venues
		WHERE google_place_id = $1
	`
	getVenueByListedNameQuery = `
		SELECT id, name, admin_area, google_place_id, latitude, longitude, listed_venue_name
		FROM venues
		WHERE listed_venue_name = $1
		  AND (admin_area = $2 OR (admin_area IS NULL AND $2 IS NULL))
		LIMIT 1
	`
)

// NewVenueRepository creates a new venue repository instance.
func NewVenueRepository(db *Database) *VenueRepository {
	return &VenueRepository{db: db}
}

// Create creates a new venue in the database.
func (r *VenueRepository) Create(ctx context.Context, venue *entity.Venue) error {
	var lat, lng *float64
	if venue.Coordinates != nil {
		lat = &venue.Coordinates.Latitude
		lng = &venue.Coordinates.Longitude
	}
	_, err := r.db.Pool.Exec(ctx, insertVenueQuery, venue.ID, venue.Name, venue.AdminArea, venue.GooglePlaceID, lat, lng, venue.ListedVenueName)
	if err != nil {
		if IsUniqueViolation(err) {
			r.db.logger.Warn(ctx, "duplicate venue",
				slog.String("entityType", "venue"),
				slog.String("venueID", venue.ID),
				slog.String("name", venue.Name),
			)
		}
		return toAppErr(err, "failed to create venue", slog.String("venue_id", venue.ID), slog.String("name", venue.Name))
	}

	r.db.logger.Info(ctx, "venue created",
		slog.String("entityType", "venue"),
		slog.String("venueID", venue.ID),
		slog.String("name", venue.Name),
	)
	return nil
}

// Get retrieves a venue by ID from the database.
func (r *VenueRepository) Get(ctx context.Context, id string) (*entity.Venue, error) {
	var v entity.Venue
	var lat, lng *float64
	err := r.db.Pool.QueryRow(ctx, getVenueQuery, id).Scan(
		&v.ID, &v.Name, &v.AdminArea, &v.GooglePlaceID,
		&lat, &lng, &v.ListedVenueName,
	)
	if err != nil {
		return nil, toAppErr(err, "failed to get venue", slog.String("venue_id", id))
	}
	if lat != nil && lng != nil {
		v.Coordinates = &entity.Coordinates{Latitude: *lat, Longitude: *lng}
	}
	return &v, nil
}

// GetByPlaceID retrieves a venue by Google Maps Place ID from the database.
func (r *VenueRepository) GetByPlaceID(ctx context.Context, placeID string) (*entity.Venue, error) {
	var v entity.Venue
	var lat, lng *float64
	err := r.db.Pool.QueryRow(ctx, getVenueByPlaceIDQuery, placeID).Scan(
		&v.ID, &v.Name, &v.AdminArea, &v.GooglePlaceID,
		&lat, &lng, &v.ListedVenueName,
	)
	if err != nil {
		return nil, toAppErr(err, "failed to get venue by place ID", slog.String("place_id", placeID))
	}
	if lat != nil && lng != nil {
		v.Coordinates = &entity.Coordinates{Latitude: *lat, Longitude: *lng}
	}
	return &v, nil
}

// GetByListedName retrieves a venue by the exact raw scraped name and optional admin area.
// Returns NotFound when no match exists.
func (r *VenueRepository) GetByListedName(ctx context.Context, listedVenueName string, adminArea *string) (*entity.Venue, error) {
	var v entity.Venue
	var lat, lng *float64
	err := r.db.Pool.QueryRow(ctx, getVenueByListedNameQuery, listedVenueName, adminArea).Scan(
		&v.ID, &v.Name, &v.AdminArea, &v.GooglePlaceID,
		&lat, &lng, &v.ListedVenueName,
	)
	if err != nil {
		return nil, toAppErr(err, "failed to get venue by listed name", slog.String("listed_venue_name", listedVenueName))
	}
	if lat != nil && lng != nil {
		v.Coordinates = &entity.Coordinates{Latitude: *lat, Longitude: *lng}
	}
	return &v, nil
}
