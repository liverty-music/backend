package rdb

import (
	"context"
	"database/sql"
	"log/slog"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
)

// OrganizerRepository implements entity.OrganizerRepository for PostgreSQL.
type OrganizerRepository struct {
	db *Database
}

// NewOrganizerRepository creates a new PostgreSQL-backed OrganizerRepository.
func NewOrganizerRepository(db *Database) *OrganizerRepository {
	return &OrganizerRepository{db: db}
}

const (
	insertOrganizerQuery = `
		INSERT INTO organizers (id, name, short_name, owner_email, provider_organizer_id, status)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, name, short_name, owner_email, provider_organizer_id, status,
			legal_name, representative_name, business_address, business_phone_number, contact_email,
			platform_fee_rate_bps
	`
	getOrganizerQuery = `
		SELECT id, name, short_name, owner_email, provider_organizer_id, status,
			legal_name, representative_name, business_address, business_phone_number, contact_email,
			platform_fee_rate_bps FROM organizers WHERE id = $1
	`
	getOrganizerByProviderOrganizerIDQuery = `
		SELECT id, name, short_name, owner_email, provider_organizer_id, status,
			legal_name, representative_name, business_address, business_phone_number, contact_email,
			platform_fee_rate_bps FROM organizers WHERE provider_organizer_id = $1
	`
	listOrganizersQuery = `
		SELECT id, name, short_name, owner_email, provider_organizer_id, status,
			legal_name, representative_name, business_address, business_phone_number, contact_email,
			platform_fee_rate_bps FROM organizers ORDER BY name
	`
	listOrganizersByOwnerEmailQuery = `
		SELECT id, name, short_name, owner_email, provider_organizer_id, status,
			legal_name, representative_name, business_address, business_phone_number, contact_email,
			platform_fee_rate_bps FROM organizers WHERE lower(owner_email) = lower($1) AND status = 2 ORDER BY name
	`
	listOrganizersByStatusQuery = `
		SELECT id, name, short_name, owner_email, provider_organizer_id, status,
			legal_name, representative_name, business_address, business_phone_number, contact_email,
			platform_fee_rate_bps FROM organizers WHERE status = $1 ORDER BY name
	`
	setOrganizerProviderOrganizerIDQuery = `
		UPDATE organizers SET provider_organizer_id = $2 WHERE id = $1
	`
	setOrganizerStatusQuery = `
		UPDATE organizers SET status = $2 WHERE id = $1
	`
	compareAndSetOrganizerStatusQuery = `
		UPDATE organizers SET status = $3 WHERE id = $1 AND status = $2
	`
	insertOrganizerArtistQuery = `
		INSERT INTO organizer_artists (organizer_id, artist_id) VALUES ($1, $2)
	`
	deleteOrganizerArtistQuery = `
		DELETE FROM organizer_artists WHERE organizer_id = $1 AND artist_id = $2
	`
	listOrganizerArtistsQuery = `
		SELECT a.id, a.name, a.mbid, a.fanart, a.fanart_synced_at, a.official_site_checked_at
		FROM artists a
		JOIN organizer_artists oa ON oa.artist_id = a.id
		WHERE oa.organizer_id = $1
		ORDER BY a.id
	`
	deleteOrganizerArtistsQuery = `
		DELETE FROM organizer_artists WHERE organizer_id = $1
	`
	setOrganizerBusinessDetailsQuery = `
		UPDATE organizers
		SET legal_name = $2, representative_name = $3, business_address = $4,
			business_phone_number = $5, contact_email = $6
		WHERE id = $1
		RETURNING id, name, short_name, owner_email, provider_organizer_id, status,
			legal_name, representative_name, business_address, business_phone_number, contact_email,
			platform_fee_rate_bps
	`
	setOrganizerPlatformFeeRateQuery = `
		UPDATE organizers SET platform_fee_rate_bps = $2 WHERE id = $1
	`
)

// scanOrganizer extracts an Organizer from a pgx row.
func scanOrganizer(scan func(dest ...any) error) (*entity.Organizer, error) {
	var (
		o                                                  entity.Organizer
		shortName                                          sql.NullString
		providerOrganizerID                                *string
		status                                             int16
		legalName, representative, address, phone, contact sql.NullString
		feeRate                                            int32
	)

	if err := scan(&o.ID, &o.Name, &shortName, &o.OwnerEmail, &providerOrganizerID, &status,
		&legalName, &representative, &address, &phone, &contact, &feeRate); err != nil {
		return nil, err
	}
	// NULL only for the Organizer created before short names existed.
	if shortName.Valid {
		o.ShortName = shortName.String
	}
	if providerOrganizerID != nil {
		o.ProviderOrganizerID = *providerOrganizerID
	}
	o.Status = entity.OrganizerStatus(status)
	// The business details are stored all-or-none (CHECK constraint).
	if legalName.Valid {
		o.BusinessDetails = &entity.BusinessDetails{
			LegalName:           legalName.String,
			RepresentativeName:  representative.String,
			BusinessAddress:     address.String,
			BusinessPhoneNumber: phone.String,
			ContactEmail:        contact.String,
		}
	}
	o.PlatformFeeRateBps = int(feeRate)
	return &o, nil
}

// Create inserts a new Organizer row and returns the persisted record. The ID
// is generated from the entity if absent. provider_organizer_id is stored as NULL
// until provisioning completes, satisfying the partial unique index that only
// covers non-NULL values. A short name another Organizer already has violates
// uq_organizers_short_name and fails with AlreadyExists.
func (r *OrganizerRepository) Create(ctx context.Context, o *entity.Organizer) (*entity.Organizer, error) {
	if o.ID == "" {
		o.ID = entity.NewID()
	}

	// Keep provider_organizer_id NULL until provisioning persists it (the partial unique
	// index only indexes non-NULL values).
	var providerOrganizerID *string
	if o.ProviderOrganizerID != "" {
		providerOrganizerID = &o.ProviderOrganizerID
	}

	row := r.db.Pool.QueryRow(ctx, insertOrganizerQuery, o.ID, o.Name, o.ShortName, o.OwnerEmail, providerOrganizerID, int16(o.Status))
	created, err := scanOrganizer(row.Scan)
	if err != nil {
		return nil, toAppErr(err, "failed to insert organizer", slog.String("id", o.ID))
	}

	r.db.logger.Info(ctx, "organizer created", slog.String("entityType", "organizer"), slog.String("id", created.ID))
	return created, nil
}

// Get retrieves a single Organizer by its id. Returns NotFound when no row
// matches.
func (r *OrganizerRepository) Get(ctx context.Context, id string) (*entity.Organizer, error) {
	row := r.db.Pool.QueryRow(ctx, getOrganizerQuery, id)
	o, err := scanOrganizer(row.Scan)
	if err != nil {
		return nil, toAppErr(err, "failed to get organizer", slog.String("id", id))
	}
	return o, nil
}

// GetByProviderOrganizerID retrieves the Organizer whose provider_organizer_id matches the
// given Zitadel organization id. Returns NotFound when no row matches.
// The provider_organizer_id column carries a partial unique index (non-NULL values),
// so at most one row is returned.
func (r *OrganizerRepository) GetByProviderOrganizerID(ctx context.Context, providerOrganizerID string) (*entity.Organizer, error) {
	row := r.db.Pool.QueryRow(ctx, getOrganizerByProviderOrganizerIDQuery, providerOrganizerID)
	o, err := scanOrganizer(row.Scan)
	if err != nil {
		return nil, toAppErr(err, "failed to get organizer by provider_organizer_id", slog.String("provider_organizer_id", providerOrganizerID))
	}
	return o, nil
}

// List returns all Organizers ordered alphabetically by name.
func (r *OrganizerRepository) List(ctx context.Context) ([]*entity.Organizer, error) {
	rows, err := r.db.Pool.Query(ctx, listOrganizersQuery)
	if err != nil {
		return nil, toAppErr(err, "failed to list organizers")
	}
	defer rows.Close()

	var organizers []*entity.Organizer
	for rows.Next() {
		o, err := scanOrganizer(rows.Scan)
		if err != nil {
			return nil, toAppErr(err, "failed to scan organizer")
		}
		organizers = append(organizers, o)
	}
	if err := rows.Err(); err != nil {
		return nil, toAppErr(err, "failed to iterate organizers")
	}
	return organizers, nil
}

// ListByOwnerEmail returns the active Organizers whose owner email equals
// email, ignoring letter case, ordered alphabetically by name.
func (r *OrganizerRepository) ListByOwnerEmail(ctx context.Context, email string) ([]*entity.Organizer, error) {
	rows, err := r.db.Pool.Query(ctx, listOrganizersByOwnerEmailQuery, email)
	if err != nil {
		return nil, toAppErr(err, "failed to list organizers by owner email")
	}
	defer rows.Close()

	organizers := []*entity.Organizer{}
	for rows.Next() {
		o, err := scanOrganizer(rows.Scan)
		if err != nil {
			return nil, toAppErr(err, "failed to scan organizer")
		}
		organizers = append(organizers, o)
	}
	if err := rows.Err(); err != nil {
		return nil, toAppErr(err, "failed to iterate organizers")
	}
	return organizers, nil
}

// ListByStatus returns all Organizers whose status matches the given value,
// ordered alphabetically by name. Used by the reconciler to find rows stuck
// in the provisioning state.
func (r *OrganizerRepository) ListByStatus(ctx context.Context, status entity.OrganizerStatus) ([]*entity.Organizer, error) {
	rows, err := r.db.Pool.Query(ctx, listOrganizersByStatusQuery, int16(status))
	if err != nil {
		return nil, toAppErr(err, "failed to list organizers by status", slog.Int("status", int(status)))
	}
	defer rows.Close()

	var organizers []*entity.Organizer
	for rows.Next() {
		o, err := scanOrganizer(rows.Scan)
		if err != nil {
			return nil, toAppErr(err, "failed to scan organizer")
		}
		organizers = append(organizers, o)
	}
	if err := rows.Err(); err != nil {
		return nil, toAppErr(err, "failed to iterate organizers by status")
	}
	return organizers, nil
}

// SetProviderOrganizerID persists the Zitadel org id on the Organizer row after
// successful Organization provisioning. Returns NotFound when the organizer no
// longer exists.
func (r *OrganizerRepository) SetProviderOrganizerID(ctx context.Context, id, providerOrganizerID string) error {
	tag, err := r.db.Pool.Exec(ctx, setOrganizerProviderOrganizerIDQuery, id, providerOrganizerID)
	if err != nil {
		return toAppErr(err, "failed to set organizer provider_organizer_id", slog.String("id", id))
	}
	if tag.RowsAffected() == 0 {
		return apperr.New(codes.NotFound, "organizer not found")
	}
	return nil
}

// SetStatus updates the lifecycle status of an Organizer. Returns NotFound
// when the organizer no longer exists.
func (r *OrganizerRepository) SetStatus(ctx context.Context, id string, status entity.OrganizerStatus) error {
	tag, err := r.db.Pool.Exec(ctx, setOrganizerStatusQuery, id, int16(status))
	if err != nil {
		return toAppErr(err, "failed to set organizer status", slog.String("id", id))
	}
	if tag.RowsAffected() == 0 {
		return apperr.New(codes.NotFound, "organizer not found")
	}
	return nil
}

// CompareAndSetStatus atomically transitions status from `from` to `to`, and
// reports whether the transition was applied. A false result means the current
// status was not `from` (e.g. a concurrent Deactivate already moved the row) —
// the caller must not treat that as its own success. The row must still exist.
func (r *OrganizerRepository) CompareAndSetStatus(ctx context.Context, id string, from, to entity.OrganizerStatus) (bool, error) {
	tag, err := r.db.Pool.Exec(ctx, compareAndSetOrganizerStatusQuery, id, int16(from), int16(to))
	if err != nil {
		return false, toAppErr(err, "failed to compare-and-set organizer status", slog.String("id", id))
	}
	// 0 rows affected means the row either does not exist or is no longer in the
	// `from` status. Both mean the intended transition did not apply; the caller
	// treats it as "superseded" and skips, so it need not be distinguished here.
	return tag.RowsAffected() > 0, nil
}

// AssociateArtist inserts a row into organizer_artists to link an artist to an
// Organizer. The unique index on artist_id ensures an artist can only be
// represented by one organizer at a time; a duplicate insert is mapped to
// AlreadyExists by toAppErr.
func (r *OrganizerRepository) AssociateArtist(ctx context.Context, organizerID, artistID string) error {
	// The unique index on artist_id rejects an already-represented artist with a
	// unique violation, which toAppErr maps to AlreadyExists.
	if _, err := r.db.Pool.Exec(ctx, insertOrganizerArtistQuery, organizerID, artistID); err != nil {
		return toAppErr(err, "failed to associate artist",
			slog.String("organizer_id", organizerID), slog.String("artist_id", artistID))
	}
	return nil
}

// DisassociateArtist removes the link between an Organizer and an artist.
// The operation is idempotent: removing a non-existent association affects
// zero rows and succeeds without error.
func (r *OrganizerRepository) DisassociateArtist(ctx context.Context, organizerID, artistID string) error {
	// Idempotent: removing a non-existent association affects zero rows and succeeds.
	if _, err := r.db.Pool.Exec(ctx, deleteOrganizerArtistQuery, organizerID, artistID); err != nil {
		return toAppErr(err, "failed to disassociate artist",
			slog.String("organizer_id", organizerID), slog.String("artist_id", artistID))
	}
	return nil
}

// ListArtists returns the artists linked to an Organizer via organizer_artists,
// ordered by artist id.
func (r *OrganizerRepository) ListArtists(ctx context.Context, organizerID string) ([]*entity.Artist, error) {
	rows, err := r.db.Pool.Query(ctx, listOrganizerArtistsQuery, organizerID)
	if err != nil {
		return nil, toAppErr(err, "failed to list organizer artists", slog.String("organizer_id", organizerID))
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
		return nil, toAppErr(err, "failed to iterate organizer artists")
	}
	return artists, nil
}

// FreeArtists removes all artist associations for an Organizer so those
// artists can be re-associated with another organizer. Called during
// Deactivate to release the organizer's exclusive hold on each artist.
func (r *OrganizerRepository) FreeArtists(ctx context.Context, organizerID string) error {
	if _, err := r.db.Pool.Exec(ctx, deleteOrganizerArtistsQuery, organizerID); err != nil {
		return toAppErr(err, "failed to free organizer artists", slog.String("organizer_id", organizerID))
	}
	return nil
}

const isArtistRepresentedByActiveOrganizerQuery = `
	SELECT EXISTS (
		SELECT 1
		FROM organizer_artists oa
		JOIN organizers o ON o.id = oa.organizer_id
		WHERE oa.artist_id = $1
		  AND o.status = $2
	)
`

// IsArtistRepresentedByActiveOrganizer reports whether the given artist is
// linked to at least one active (status=OrganizerStatusActive) organizer.
func (r *OrganizerRepository) IsArtistRepresentedByActiveOrganizer(ctx context.Context, artistID string) (bool, error) {
	var exists bool
	err := r.db.Pool.QueryRow(ctx, isArtistRepresentedByActiveOrganizerQuery, artistID, int16(entity.OrganizerStatusActive)).Scan(&exists)
	if err != nil {
		return false, toAppErr(err, "failed to check if artist is represented by active organizer",
			slog.String("artist_id", artistID))
	}
	return exists, nil
}

const (
	lockOrganizerStatusQuery = `
		SELECT status FROM organizers WHERE id = $1 FOR UPDATE
	`
	// selectOrganizerEventIDsQuery selects the ids of every Event in a Series
	// owned by the organizer bound to $1.
	selectOrganizerEventIDsQuery = `
		SELECT e.id FROM events e JOIN series s ON s.id = e.series_id WHERE s.organizer_id = $1
	`
	organizerHasPayoutAccountQuery = `
		SELECT EXISTS (SELECT 1 FROM payout_accounts WHERE organizer_id = $1)
	`
	deleteOrganizerSeriesQuery = `
		DELETE FROM series WHERE organizer_id = $1
	`
	deleteOrganizerSeriesImagesQuery = `
		DELETE FROM series_images WHERE organizer_id = $1
	`
	deleteOrganizerQuery = `
		DELETE FROM organizers WHERE id = $1
	`
)

// Delete permanently removes a deactivated Organizer and every record kept for
// it, in one transaction (see entity.OrganizerRepository.Delete). The
// organizer row is locked first and the blockers are checked under that lock;
// with dryRun the transaction is rolled back after the check.
//
// Its Events go through the same removal as a Concert deletion (removeEvents),
// after the same locks and blocker check (lockAndCheckEventRemoval). No purchase path references the organizer row,
// so locking it first adds no lock-order inversion. Then the remaining
// Series (which cascades their TicketSales and series_cover_images), the
// SeriesImage rows and finally the organizer row (which cascades
// organizer_artists) are deleted.
func (r *OrganizerRepository) Delete(ctx context.Context, id string, dryRun bool) error {
	tx, err := r.db.Pool.Begin(ctx)
	if err != nil {
		return toAppErr(err, "failed to begin organizer delete transaction", slog.String("id", id))
	}
	// Rollback is a no-op after a successful Commit.
	defer func() { _ = tx.Rollback(ctx) }()

	var status int16
	if err := tx.QueryRow(ctx, lockOrganizerStatusQuery, id).Scan(&status); err != nil {
		return toAppErr(err, "failed to lock organizer", slog.String("id", id))
	}
	if entity.OrganizerStatus(status) != entity.OrganizerStatusDeactivated {
		return apperr.New(codes.FailedPrecondition, "organizer is not deactivated", slog.String("id", id))
	}

	eventIDs, err := collectIDs(ctx, tx, selectOrganizerEventIDsQuery, id)
	if err != nil {
		return toAppErr(err, "failed to read organizer events", slog.String("id", id))
	}
	if err := lockAndCheckEventRemoval(ctx, tx, eventIDs); err != nil {
		return err
	}
	var payoutAccount bool
	if err := tx.QueryRow(ctx, organizerHasPayoutAccountQuery, id).Scan(&payoutAccount); err != nil {
		return toAppErr(err, "failed to check organizer payout account", slog.String("id", id))
	}
	if payoutAccount {
		return apperr.New(codes.FailedPrecondition, "the organizer has a payout account", slog.String("id", id))
	}
	if dryRun {
		return nil
	}

	if err := removeEvents(ctx, tx, eventIDs); err != nil {
		return err
	}
	statements := []struct {
		query string
		what  string
	}{
		{deleteOrganizerSeriesQuery, "series"},
		{deleteOrganizerSeriesImagesQuery, "series images"},
		{deleteOrganizerQuery, "organizer"},
	}
	for _, st := range statements {
		if _, err := tx.Exec(ctx, st.query, id); err != nil {
			return toAppErr(err, "failed to delete organizer "+st.what, slog.String("id", id))
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return toAppErr(err, "failed to commit organizer delete", slog.String("id", id))
	}
	r.db.logger.Info(ctx, "organizer deleted", slog.String("entityType", "organizer"), slog.String("id", id))
	return nil
}

// SetBusinessDetails replaces the organizer's business details as a whole and
// returns the updated organizer.
func (r *OrganizerRepository) SetBusinessDetails(ctx context.Context, id string, details entity.BusinessDetails) (*entity.Organizer, error) {
	if err := details.Validate(); err != nil {
		return nil, apperr.Wrap(err, codes.InvalidArgument, "invalid business details")
	}
	row := r.db.Pool.QueryRow(ctx, setOrganizerBusinessDetailsQuery, id,
		details.LegalName, details.RepresentativeName, details.BusinessAddress,
		details.BusinessPhoneNumber, details.ContactEmail)
	o, err := scanOrganizer(row.Scan)
	if err != nil {
		return nil, toAppErr(err, "failed to set organizer business details", slog.String("id", id))
	}
	return o, nil
}

// SetPlatformFeeRate stores the platform fee rate applied to the organizer's
// future orders.
func (r *OrganizerRepository) SetPlatformFeeRate(ctx context.Context, id string, rateBps int) error {
	if err := entity.ValidatePlatformFeeRate(rateBps); err != nil {
		return apperr.Wrap(err, codes.InvalidArgument, "invalid platform fee rate")
	}
	tag, err := r.db.Pool.Exec(ctx, setOrganizerPlatformFeeRateQuery, id, rateBps)
	if err != nil {
		return toAppErr(err, "failed to set organizer platform fee rate", slog.String("id", id))
	}
	if tag.RowsAffected() == 0 {
		return apperr.New(codes.NotFound, "organizer not found")
	}
	return nil
}
