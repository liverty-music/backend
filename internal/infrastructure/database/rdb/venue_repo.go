package rdb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
)

// VenueRepository implements entity.VenueRepository for PostgreSQL.
type VenueRepository struct {
	db *Database
}

// venueColumns are the columns scanVenue reads.
const venueColumns = `id, name, admin_area, time_zone, google_place_id, latitude, longitude, coordinates_fetch_time, listed_venue_name`

const (
	// insertVenueQuery uses an untargeted ON CONFLICT DO NOTHING so a lost race or
	// a violation on EITHER partial-unique index (idx_venues_google_place_id or
	// idx_venues_listed_name_admin_area) suppresses the insert instead of erroring;
	// RETURNING id yields the new row only when the insert actually happened.
	insertVenueQuery = `
		INSERT INTO venues (id, name, admin_area, time_zone, google_place_id, latitude, longitude, coordinates_fetch_time, listed_venue_name)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
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
	getVenueQuery             = `SELECT ` + venueColumns + ` FROM venues WHERE id = $1`
	getVenueByPlaceIDQuery    = `SELECT ` + venueColumns + ` FROM venues WHERE google_place_id = $1`
	getVenueByListedNameQuery = `
		SELECT ` + venueColumns + `
		FROM venues
		WHERE listed_venue_name = $1 AND admin_area = $2
		LIMIT 1
	`
	setVenueCoordinatesQuery = `
		UPDATE venues SET latitude = $2, longitude = $3, coordinates_fetch_time = $4 WHERE id = $1
	`
	listVenueCoordinatesDueQuery = `
		SELECT ` + venueColumns + `
		FROM venues
		WHERE google_place_id IS NOT NULL
		  AND (coordinates_fetch_time IS NULL OR coordinates_fetch_time < $1)
		ORDER BY coordinates_fetch_time ASC NULLS FIRST, id
		LIMIT $2
	`
	clearExpiredVenueCoordinatesQuery = `
		UPDATE venues SET latitude = NULL, longitude = NULL, coordinates_fetch_time = NULL
		WHERE coordinates_fetch_time < $1
	`
	// venueAreaQuery reads the Area a venue names and the time zones of its
	// country, for Venue.CheckArea.
	venueAreaQuery = `
		SELECT ` + areaColumns + `,
		       COALESCE((SELECT array_agg(c.time_zone) FROM country_time_zones c WHERE c.country_code = a.country_code), '{}')
		FROM areas a WHERE a.code = $1
	`
)

// NewVenueRepository creates a new venue repository instance.
func NewVenueRepository(db *Database) *VenueRepository {
	return &VenueRepository{db: db}
}

// CheckNew implements entity.VenueRepository.
func (r *VenueRepository) CheckNew(ctx context.Context, venue *entity.Venue) error {
	if err := r.checkArea(ctx, venue); err != nil {
		return err
	}
	return venue.CheckCoordinates()
}

// Create inserts a new venue and returns the id of the surviving row. It is an
// idempotent get-or-create: the insert uses ON CONFLICT DO NOTHING, so a lost
// race or a violation on either partial-unique index resolves to the existing
// row instead of erroring. When the insert is suppressed, it re-SELECTs by
// google_place_id first (the more canonical identity) and then by
// (listed_venue_name, admin_area), returning whichever row won.
func (r *VenueRepository) Create(ctx context.Context, venue *entity.Venue) (string, error) {
	if err := r.CheckNew(ctx, venue); err != nil {
		return "", err
	}
	var lat, lng *float64
	if venue.Coordinates != nil {
		lat = &venue.Coordinates.Latitude
		lng = &venue.Coordinates.Longitude
	}

	var id string
	err := r.db.Pool.QueryRow(ctx, insertVenueQuery, venue.ID, venue.Name, venue.AdminArea, venue.TimeZone,
		venue.GooglePlaceID, lat, lng, venue.CoordinatesFetchTime, venue.ListedVenueName).Scan(&id)
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
		existing, selErr := r.GetByListedName(ctx, *venue.ListedVenueName, *venue.AdminArea)
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

// SetCoordinates implements entity.VenueRepository.
func (r *VenueRepository) SetCoordinates(ctx context.Context, venueID string, coords *entity.PlaceCoordinates) error {
	var lat, lng *float64
	var fetch *time.Time
	if coords != nil {
		if coords.Coordinates == nil {
			return apperr.New(codes.InvalidArgument, "coordinates to set must be given; pass nil to remove them")
		}
		lat, lng, fetch = &coords.Coordinates.Latitude, &coords.Coordinates.Longitude, &coords.FetchTime
	}
	tag, err := r.db.Pool.Exec(ctx, setVenueCoordinatesQuery, venueID, lat, lng, fetch)
	if err != nil {
		return toAppErr(err, "failed to set venue coordinates", slog.String("venue_id", venueID))
	}
	if tag.RowsAffected() == 0 {
		return apperr.New(codes.NotFound, "venue not found")
	}
	return nil
}

// ListCoordinatesDue implements entity.VenueRepository.
func (r *VenueRepository) ListCoordinatesDue(ctx context.Context, at time.Time, limit int) ([]*entity.Venue, error) {
	if limit < 1 {
		return nil, apperr.New(codes.InvalidArgument, "the maximum count must be at least 1")
	}
	rows, err := r.db.Pool.Query(ctx, listVenueCoordinatesDueQuery, at.Add(-entity.CoordinatesRefreshAge), limit)
	if err != nil {
		return nil, toAppErr(err, "failed to list venues due for coordinates")
	}
	defer rows.Close()
	venues := []*entity.Venue{}
	for rows.Next() {
		v, err := scanVenue(rows)
		if err != nil {
			return nil, toAppErr(err, "failed to scan venue due for coordinates")
		}
		venues = append(venues, v)
	}
	if err := rows.Err(); err != nil {
		return nil, toAppErr(err, "failed to list venues due for coordinates")
	}
	return venues, nil
}

// ClearExpiredCoordinates implements entity.VenueRepository.
func (r *VenueRepository) ClearExpiredCoordinates(ctx context.Context, at time.Time) (int64, error) {
	tag, err := r.db.Pool.Exec(ctx, clearExpiredVenueCoordinatesQuery, at.Add(-entity.CoordinatesMaxAge))
	if err != nil {
		return 0, toAppErr(err, "failed to clear expired venue coordinates")
	}
	return tag.RowsAffected(), nil
}

func (r *VenueRepository) checkArea(ctx context.Context, venue *entity.Venue) error {
	if venue.AdminArea == nil {
		return venue.CheckArea(nil, nil)
	}
	var zones []string
	area, err := scanAreaRow(rowWithExtra{r.db.Pool.QueryRow(ctx, venueAreaQuery, *venue.AdminArea), &zones})
	if errors.Is(err, pgx.ErrNoRows) {
		return venue.CheckArea(nil, nil)
	}
	if err != nil {
		return toAppErr(err, "failed to read the venue's area", slog.String("admin_area", *venue.AdminArea))
	}
	return venue.CheckArea(area, zones)
}

// rowWithExtra scans a row whose last column goes to extra, after the
// columns another scanner reads.
type rowWithExtra struct {
	row   pgx.Row
	extra any
}

// Scan scans dest and then extra.
func (r rowWithExtra) Scan(dest ...any) error {
	return r.row.Scan(append(dest, r.extra)...)
}

// scanVenue reads the columns of venueColumns.
func scanVenue(row pgx.Row) (*entity.Venue, error) {
	var v entity.Venue
	var lat, lng sql.NullFloat64
	var fetch sql.NullTime
	var area, zone, place, listed sql.NullString
	if err := row.Scan(&v.ID, &v.Name, &area, &zone, &place, &lat, &lng, &fetch, &listed); err != nil {
		return nil, err
	}
	v.AdminArea, v.TimeZone, v.GooglePlaceID, v.ListedVenueName = nullString(area), nullString(zone), nullString(place), nullString(listed)
	if lat.Valid && lng.Valid {
		v.Coordinates = &entity.Coordinates{Latitude: lat.Float64, Longitude: lng.Float64}
	}
	if fetch.Valid {
		t := fetch.Time
		v.CoordinatesFetchTime = &t
	}
	return &v, nil
}

// Get retrieves a venue by ID from the database.
func (r *VenueRepository) Get(ctx context.Context, id string) (*entity.Venue, error) {
	v, err := scanVenue(r.db.Pool.QueryRow(ctx, getVenueQuery, id))
	if err != nil {
		return nil, toAppErr(err, "failed to get venue", slog.String("venue_id", id))
	}
	return v, nil
}

// GetByPlaceID retrieves a venue by Google Maps Place ID from the database.
func (r *VenueRepository) GetByPlaceID(ctx context.Context, placeID string) (*entity.Venue, error) {
	v, err := scanVenue(r.db.Pool.QueryRow(ctx, getVenueByPlaceIDQuery, placeID))
	if err != nil {
		return nil, toAppErr(err, "failed to get venue by place ID", slog.String("place_id", placeID))
	}
	return v, nil
}

// GetByListedName retrieves a venue by the exact raw scraped name and its
// admin area, both required. Returns NotFound when no match exists.
func (r *VenueRepository) GetByListedName(ctx context.Context, listedVenueName, adminArea string) (*entity.Venue, error) {
	if listedVenueName == "" || adminArea == "" {
		return nil, apperr.New(codes.InvalidArgument, "a listed venue name and an admin area are required")
	}
	v, err := scanVenue(r.db.Pool.QueryRow(ctx, getVenueByListedNameQuery, listedVenueName, adminArea))
	if err != nil {
		return nil, toAppErr(err, "failed to get venue by listed name", slog.String("listed_venue_name", listedVenueName))
	}
	return v, nil
}
