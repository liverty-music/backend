package rdb

import (
	"context"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/liverty-music/backend/internal/entity"
)

// VenueRepository implements entity.VenueRepository and entity.VenueEnrichmentRepository for PostgreSQL.
type VenueRepository struct {
	db *Database
}

const (
	insertVenueQuery = `
		INSERT INTO venues (id, name, admin_area, enrichment_status, raw_name)
		VALUES ($1, $2, $3, $4, $5)
	`
	getVenueQuery = `
		SELECT id, name, admin_area, mbid, google_place_id, enrichment_status, raw_name
		FROM venues
		WHERE id = $1
	`
	getVenueByNameQuery = `
		SELECT id, name, admin_area, mbid, google_place_id, enrichment_status, raw_name
		FROM venues
		WHERE name = $1 OR raw_name = $1
		LIMIT 1
	`
	listPendingVenuesQuery = `
		SELECT id, name, admin_area, mbid, google_place_id, enrichment_status, raw_name
		FROM venues
		WHERE enrichment_status = 'pending'
	`
	updateEnrichedVenueQuery = `
		UPDATE venues
		SET
			name = $2,
			raw_name = COALESCE(raw_name, $3),
			mbid = $4,
			google_place_id = $5,
			enrichment_status = 'enriched'
		WHERE id = $1
	`
	markVenueFailedQuery = `
		UPDATE venues
		SET enrichment_status = 'failed'
		WHERE id = $1
	`
)

// NewVenueRepository creates a new venue repository instance.
func NewVenueRepository(db *Database) *VenueRepository {
	return &VenueRepository{db: db}
}

// Create creates a new venue in the database.
func (r *VenueRepository) Create(ctx context.Context, venue *entity.Venue) error {
	status := venue.EnrichmentStatus
	if status == "" {
		status = entity.EnrichmentStatusPending
	}
	rawName := venue.RawName
	if rawName == "" {
		rawName = venue.Name
	}
	_, err := r.db.Pool.Exec(ctx, insertVenueQuery, venue.ID, venue.Name, venue.AdminArea, status, rawName)
	if err != nil {
		return toAppErr(err, "failed to create venue", slog.String("venue_id", venue.ID), slog.String("name", venue.Name))
	}
	return nil
}

// Get retrieves a venue by ID from the database.
func (r *VenueRepository) Get(ctx context.Context, id string) (*entity.Venue, error) {
	var v entity.Venue
	err := r.db.Pool.QueryRow(ctx, getVenueQuery, id).Scan(
		&v.ID, &v.Name, &v.AdminArea, &v.MBID, &v.GooglePlaceID, &v.EnrichmentStatus, &v.RawName,
	)
	if err != nil {
		return nil, toAppErr(err, "failed to get venue", slog.String("venue_id", id))
	}
	return &v, nil
}

// GetByName retrieves a venue by Name (or raw_name fallback) from the database.
func (r *VenueRepository) GetByName(ctx context.Context, name string) (*entity.Venue, error) {
	var v entity.Venue
	err := r.db.Pool.QueryRow(ctx, getVenueByNameQuery, name).Scan(
		&v.ID, &v.Name, &v.AdminArea, &v.MBID, &v.GooglePlaceID, &v.EnrichmentStatus, &v.RawName,
	)
	if err != nil {
		return nil, toAppErr(err, "failed to get venue by name", slog.String("name", name))
	}
	return &v, nil
}

// ListPending returns all venues with enrichment_status = 'pending'.
func (r *VenueRepository) ListPending(ctx context.Context) ([]*entity.Venue, error) {
	rows, err := r.db.Pool.Query(ctx, listPendingVenuesQuery)
	if err != nil {
		return nil, toAppErr(err, "failed to list pending venues")
	}
	defer rows.Close()

	venues, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (*entity.Venue, error) {
		var v entity.Venue
		if err := row.Scan(&v.ID, &v.Name, &v.AdminArea, &v.MBID, &v.GooglePlaceID, &v.EnrichmentStatus, &v.RawName); err != nil {
			return nil, err
		}
		return &v, nil
	})
	if err != nil {
		return nil, toAppErr(err, "failed to scan pending venues")
	}
	return venues, nil
}

// UpdateEnriched updates a venue to the enriched state.
func (r *VenueRepository) UpdateEnriched(ctx context.Context, venue *entity.Venue) error {
	_, err := r.db.Pool.Exec(ctx, updateEnrichedVenueQuery,
		venue.ID, venue.Name, venue.RawName, nullableString(venue.MBID), nullableString(venue.GooglePlaceID),
	)
	if err != nil {
		return toAppErr(err, "failed to update enriched venue", slog.String("venue_id", venue.ID))
	}
	return nil
}

// MarkFailed sets enrichment_status = 'failed' for the given venue ID.
func (r *VenueRepository) MarkFailed(ctx context.Context, id string) error {
	_, err := r.db.Pool.Exec(ctx, markVenueFailedQuery, id)
	if err != nil {
		return toAppErr(err, "failed to mark venue as failed", slog.String("venue_id", id))
	}
	return nil
}

// MergeVenues merges a duplicate venue into a canonical venue atomically.
func (r *VenueRepository) MergeVenues(ctx context.Context, canonicalID, duplicateID string) error {
	tx, err := r.db.Pool.Begin(ctx)
	if err != nil {
		return toAppErr(err, "failed to begin merge transaction")
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Step 1: DELETE duplicate events that share (artist_id, local_event_date, start_at) with canonical
	_, err = tx.Exec(ctx, `
		DELETE FROM events e
		WHERE e.venue_id = $1
		  AND EXISTS (
			SELECT 1 FROM events c
			JOIN concerts cc ON cc.event_id = c.id
			JOIN concerts dc ON dc.event_id = e.id
			WHERE c.venue_id = $2
			  AND cc.artist_id = dc.artist_id
			  AND c.local_event_date = e.local_event_date
			  AND c.start_at IS NOT DISTINCT FROM e.start_at
		  )
	`, duplicateID, canonicalID)
	if err != nil {
		return toAppErr(err, "failed to delete duplicate events during merge",
			slog.String("canonical_id", canonicalID), slog.String("duplicate_id", duplicateID))
	}

	// Step 2: Re-point remaining events to canonical venue
	_, err = tx.Exec(ctx, `
		UPDATE events SET venue_id = $1 WHERE venue_id = $2
	`, canonicalID, duplicateID)
	if err != nil {
		return toAppErr(err, "failed to re-point events during merge",
			slog.String("canonical_id", canonicalID), slog.String("duplicate_id", duplicateID))
	}

	// Step 2.5: Null out unique fields on the duplicate before merging them onto
	// the canonical. PostgreSQL checks unique partial indexes per-statement, so
	// without this the COALESCE UPDATE in Step 3 would transiently produce two
	// rows with the same non-NULL mbid or google_place_id and raise a constraint
	// violation — even though Step 4 deletes the duplicate in the same transaction.
	_, err = tx.Exec(ctx, `
		UPDATE venues SET mbid = NULL, google_place_id = NULL WHERE id = $1
	`, duplicateID)
	if err != nil {
		return toAppErr(err, "failed to clear unique fields on duplicate venue during merge",
			slog.String("duplicate_id", duplicateID))
	}

	// Step 3: COALESCE canonical venue fields with duplicate's values
	_, err = tx.Exec(ctx, `
		UPDATE venues c
		SET
			admin_area      = COALESCE(c.admin_area,      d.admin_area),
			mbid            = COALESCE(c.mbid,            d.mbid),
			google_place_id = COALESCE(c.google_place_id, d.google_place_id)
		FROM venues d
		WHERE c.id = $1 AND d.id = $2
	`, canonicalID, duplicateID)
	if err != nil {
		return toAppErr(err, "failed to update canonical venue fields during merge",
			slog.String("canonical_id", canonicalID), slog.String("duplicate_id", duplicateID))
	}

	// Step 4: Delete duplicate venue
	_, err = tx.Exec(ctx, `DELETE FROM venues WHERE id = $1`, duplicateID)
	if err != nil {
		return toAppErr(err, "failed to delete duplicate venue during merge",
			slog.String("duplicate_id", duplicateID))
	}

	if err := tx.Commit(ctx); err != nil {
		return toAppErr(err, "failed to commit merge transaction")
	}
	return nil
}

// nullableString returns nil if s is empty, allowing pgx to insert NULL.
func nullableString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
