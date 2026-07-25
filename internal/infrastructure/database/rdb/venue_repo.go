package rdb

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
)

// VenueRepository implements entity.VenueRepository for PostgreSQL.
type VenueRepository struct {
	db *Database
}

const (
	// insertVenueQuery uses an untargeted ON CONFLICT DO NOTHING so a lost race or
	// a violation on EITHER partial-unique index (idx_venues_google_place_id or
	// idx_venues_listed_name_admin_area) suppresses the insert instead of erroring;
	// RETURNING id yields the new row only when the insert actually happened.
	insertVenueQuery = `
		INSERT INTO venues (id, name, admin_area, google_place_id, latitude, longitude, listed_venue_name)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT DO NOTHING
		RETURNING id
	`
	// backfillVenuePlaceIDQuery fills a NULL google_place_id in place; the WHERE
	// guard keeps it a no-op when another writer already set a non-NULL value.
	backfillVenuePlaceIDQuery = `
		UPDATE venues
		SET google_place_id = $2
		WHERE id = $1
		  AND google_place_id IS NULL
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

// Create inserts a new venue and returns the id of the surviving row. It is an
// idempotent get-or-create: the insert uses ON CONFLICT DO NOTHING, so a lost
// race or a violation on either partial-unique index resolves to the existing
// row instead of erroring. When the insert is suppressed, it re-SELECTs by
// google_place_id first (the more canonical identity) and then by
// (listed_venue_name, admin_area), returning whichever row won.
func (r *VenueRepository) Create(ctx context.Context, venue *entity.Venue) (string, error) {
	var lat, lng *float64
	if venue.Coordinates != nil {
		lat = &venue.Coordinates.Latitude
		lng = &venue.Coordinates.Longitude
	}

	var id string
	err := r.db.Pool.QueryRow(ctx, insertVenueQuery, venue.ID, venue.Name, venue.AdminArea, venue.GooglePlaceID, lat, lng, venue.ListedVenueName).Scan(&id)
	if err == nil {
		r.db.logger.Info(ctx, "venue created",
			slog.String("entityType", "venue"),
			slog.String("venueID", id),
			slog.String("name", venue.Name),
		)
		return id, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", toAppErr(err, "failed to create venue", slog.String("venue_id", venue.ID), slog.String("name", venue.Name))
	}

	// ON CONFLICT suppressed the insert — another row already holds this venue's
	// identity on one of the partial-unique indexes. Re-SELECT the survivor.
	r.db.logger.Warn(ctx, "venue create suppressed by conflict — resolving existing row",
		slog.String("entityType", "venue"),
		slog.String("venueID", venue.ID),
		slog.String("name", venue.Name),
	)

	if venue.GooglePlaceID != nil {
		existing, selErr := r.GetByPlaceID(ctx, *venue.GooglePlaceID)
		if selErr == nil {
			return existing.ID, nil
		}
		if !errors.Is(selErr, apperr.ErrNotFound) {
			return "", fmt.Errorf("re-select venue by place ID after conflict: %w", selErr)
		}
	}

	if venue.ListedVenueName != nil {
		existing, selErr := r.GetByListedName(ctx, *venue.ListedVenueName, venue.AdminArea)
		if selErr == nil {
			return existing.ID, nil
		}
		if !errors.Is(selErr, apperr.ErrNotFound) {
			return "", fmt.Errorf("re-select venue by listed name after conflict: %w", selErr)
		}
	}

	// The insert was suppressed but neither key found a surviving row — should be
	// unreachable, but surface it rather than returning an empty id.
	return "", apperr.New(codes.Internal, "venue create suppressed by conflict but no surviving row found")
}

// BackfillPlaceID sets google_place_id on a venue that currently has none. It is
// a no-op when the row already carries a non-NULL place_id. A concurrent backfill
// that races another writer can still collide on idx_venues_google_place_id (the
// WHERE guard cannot be expressed as an untargeted ON CONFLICT on an UPDATE); that
// unique violation is treated as a benign no-op — the place_id is already set.
func (r *VenueRepository) BackfillPlaceID(ctx context.Context, venueID, placeID string) error {
	_, err := r.db.Pool.Exec(ctx, backfillVenuePlaceIDQuery, venueID, placeID)
	if err != nil {
		if IsUniqueViolation(err) {
			r.db.logger.Warn(ctx, "venue place_id backfill lost a race — leaving existing value",
				slog.String("entityType", "venue"),
				slog.String("venueID", venueID),
				slog.String("place_id", placeID),
			)
			return nil
		}
		return toAppErr(err, "failed to backfill venue place ID", slog.String("venue_id", venueID), slog.String("place_id", placeID))
	}
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
