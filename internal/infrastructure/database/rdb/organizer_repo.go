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
		INSERT INTO organizers (id, name, operator_email, zitadel_org_id, status)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, name, operator_email, zitadel_org_id, status,
			seller_legal_name, seller_representative_name, seller_address, seller_phone_number, seller_contact_email,
			platform_fee_rate_bps
	`
	getOrganizerQuery = `
		SELECT id, name, operator_email, zitadel_org_id, status,
			seller_legal_name, seller_representative_name, seller_address, seller_phone_number, seller_contact_email,
			platform_fee_rate_bps FROM organizers WHERE id = $1
	`
	getOrganizerByZitadelOrgIDQuery = `
		SELECT id, name, operator_email, zitadel_org_id, status,
			seller_legal_name, seller_representative_name, seller_address, seller_phone_number, seller_contact_email,
			platform_fee_rate_bps FROM organizers WHERE zitadel_org_id = $1
	`
	listOrganizersQuery = `
		SELECT id, name, operator_email, zitadel_org_id, status,
			seller_legal_name, seller_representative_name, seller_address, seller_phone_number, seller_contact_email,
			platform_fee_rate_bps FROM organizers ORDER BY name
	`
	listOrganizersByStatusQuery = `
		SELECT id, name, operator_email, zitadel_org_id, status,
			seller_legal_name, seller_representative_name, seller_address, seller_phone_number, seller_contact_email,
			platform_fee_rate_bps FROM organizers WHERE status = $1 ORDER BY name
	`
	setOrganizerZitadelOrgIDQuery = `
		UPDATE organizers SET zitadel_org_id = $2 WHERE id = $1
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
	setOrganizerSellerDetailsQuery = `
		UPDATE organizers
		SET seller_legal_name = $2, seller_representative_name = $3, seller_address = $4,
			seller_phone_number = $5, seller_contact_email = $6
		WHERE id = $1
	`
	setOrganizerPlatformFeeRateQuery = `
		UPDATE organizers SET platform_fee_rate_bps = $2 WHERE id = $1
	`
)

// scanOrganizer extracts an Organizer from a pgx row.
func scanOrganizer(scan func(dest ...any) error) (*entity.Organizer, error) {
	var (
		o                                                  entity.Organizer
		zitadelOrgID                                       *string
		status                                             int16
		legalName, representative, address, phone, contact sql.NullString
		feeRate                                            int32
	)

	if err := scan(&o.ID, &o.Name, &o.OperatorEmail, &zitadelOrgID, &status,
		&legalName, &representative, &address, &phone, &contact, &feeRate); err != nil {
		return nil, err
	}
	if zitadelOrgID != nil {
		o.ZitadelOrgID = *zitadelOrgID
	}
	o.Status = entity.OrganizerStatus(status)
	// The seller details are stored all-or-none (CHECK constraint).
	if legalName.Valid {
		o.SellerDetails = &entity.SellerDetails{
			LegalName:          legalName.String,
			RepresentativeName: representative.String,
			Address:            address.String,
			PhoneNumber:        phone.String,
			ContactEmail:       contact.String,
		}
	}
	o.PlatformFeeRateBps = int(feeRate)
	return &o, nil
}

// Create inserts a new Organizer row and returns the persisted record. The ID
// is generated from the entity if absent. zitadel_org_id is stored as NULL
// until provisioning completes, satisfying the partial unique index that only
// covers non-NULL values.
func (r *OrganizerRepository) Create(ctx context.Context, o *entity.Organizer) (*entity.Organizer, error) {
	if o.ID == "" {
		o.ID = entity.NewOrganizer(o.Name).ID
	}

	// Keep zitadel_org_id NULL until provisioning persists it (the partial unique
	// index only indexes non-NULL values).
	var zitadelOrgID *string
	if o.ZitadelOrgID != "" {
		zitadelOrgID = &o.ZitadelOrgID
	}

	row := r.db.Pool.QueryRow(ctx, insertOrganizerQuery, o.ID, o.Name, o.OperatorEmail, zitadelOrgID, int16(o.Status))
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

// GetByZitadelOrgID retrieves the Organizer whose zitadel_org_id matches the
// given Zitadel organization id. Returns NotFound when no row matches.
// The zitadel_org_id column carries a partial unique index (non-NULL values),
// so at most one row is returned.
func (r *OrganizerRepository) GetByZitadelOrgID(ctx context.Context, zitadelOrgID string) (*entity.Organizer, error) {
	row := r.db.Pool.QueryRow(ctx, getOrganizerByZitadelOrgIDQuery, zitadelOrgID)
	o, err := scanOrganizer(row.Scan)
	if err != nil {
		return nil, toAppErr(err, "failed to get organizer by zitadel_org_id", slog.String("zitadel_org_id", zitadelOrgID))
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

// SetZitadelOrgID persists the Zitadel org id on the Organizer row after
// successful tenant provisioning. Returns NotFound when the organizer no
// longer exists.
func (r *OrganizerRepository) SetZitadelOrgID(ctx context.Context, id, zitadelOrgID string) error {
	tag, err := r.db.Pool.Exec(ctx, setOrganizerZitadelOrgIDQuery, id, zitadelOrgID)
	if err != nil {
		return toAppErr(err, "failed to set organizer zitadel_org_id", slog.String("id", id))
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

// organizerOrderIDsSubquery selects the ids of every Order whose source — a
// won application of a lottery phase or a reservation of a ticket sale — is
// for an Event of the organizer bound to $1.
const organizerOrderIDsSubquery = `
	SELECT o.id FROM orders o
	JOIN ticket_applications ta ON ta.id = o.application_id
	JOIN lottery_sales_phases p ON p.id = ta.phase_id
	WHERE p.event_id IN (` + organizerEventIDsSubquery + `)
	UNION ALL
	SELECT o.id FROM orders o
	JOIN reservations rv ON rv.id = o.reservation_id
	JOIN ticket_sales ts ON ts.id = rv.ticket_sale_id
	WHERE ts.event_id IN (` + organizerEventIDsSubquery + `)
`

// organizerEventIDsSubquery selects the ids of every Event in a Series owned by
// the organizer bound to $1. It is shared by the deletion check and the
// deletion statements below.
const organizerEventIDsSubquery = `
	SELECT e.id FROM events e JOIN series s ON s.id = e.series_id WHERE s.organizer_id = $1
`

const (
	lockOrganizerStatusQuery = `
		SELECT status FROM organizers WHERE id = $1 FOR UPDATE
	`
	// organizerDeletionBlockersQuery reports, for the organizer bound to $1,
	// whether any of its Events has an Order that is not Refunded or a
	// Settlement that is not Reversed, and whether it has a payout account.
	organizerDeletionBlockersQuery = `
		SELECT
			EXISTS (
				SELECT 1 FROM orders o
				WHERE o.id IN (` + organizerOrderIDsSubquery + `) AND o.status <> $2
			) OR EXISTS (
				SELECT 1 FROM reservations rv
				JOIN ticket_sales ts ON ts.id = rv.ticket_sale_id
				WHERE ts.event_id IN (` + organizerEventIDsSubquery + `) AND rv.capture_at IS NOT NULL AND rv.status <> $4
			),
			EXISTS (
				SELECT 1 FROM settlements st
				WHERE st.event_id IN (` + organizerEventIDsSubquery + `) AND st.status <> $3
			),
			EXISTS (SELECT 1 FROM organizer_connected_accounts WHERE organizer_id = $1)
	`
	deleteOrganizerAdmissionsQuery = `
		DELETE FROM admissions WHERE event_id IN (` + organizerEventIDsSubquery + `)
	`
	deleteOrganizerRejectedScansQuery = `
		DELETE FROM rejected_scans WHERE event_id IN (` + organizerEventIDsSubquery + `)
	`
	deleteOrganizerReceptionLinksQuery = `
		DELETE FROM reception_links WHERE event_id IN (` + organizerEventIDsSubquery + `)
	`
	// The purchase deletions below are filtered by status as well as by event:
	// a Paid Order committed after the blocker check is then left in place, and
	// the Series delete fails on its RESTRICT foreign key instead of removing it.
	deleteOrganizerSettlementsQuery = `
		DELETE FROM settlements WHERE event_id IN (` + organizerEventIDsSubquery + `) AND status = $2
	`
	deleteOrganizerTicketsQuery = `
		DELETE FROM tickets t USING orders o
		WHERE t.order_id = o.id AND t.event_id IN (` + organizerEventIDsSubquery + `) AND o.status = $2
	`
	deleteOrganizerOrdersQuery = `
		DELETE FROM orders o
		WHERE o.id IN (` + organizerOrderIDsSubquery + `) AND o.status = $2
	`
	// A reservation still referenced by an Order (one not refunded) makes this
	// delete fail on the RESTRICT foreign key instead of removing it.
	deleteOrganizerReservationsQuery = `
		DELETE FROM reservations rv USING ticket_sales ts
		WHERE ts.id = rv.ticket_sale_id AND ts.event_id IN (` + organizerEventIDsSubquery + `)
	`
	deleteOrganizerTicketSalesQuery = `
		DELETE FROM ticket_sales WHERE event_id IN (` + organizerEventIDsSubquery + `)
	`
	deleteOrganizerSeriesQuery = `
		DELETE FROM series WHERE organizer_id = $1
	`
	deleteOrganizerMediaQuery = `
		DELETE FROM media WHERE organizer_id = $1
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
// The statements run in RESTRICT-safe order: reception records, then the
// refunded purchases, then the checkouts and ticket sales, then the Series (which cascades Events, phases,
// applications, journeys, performers and series_media), then the Media rows
// and finally the organizer row (which cascades organizer_artists).
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

	var unrefundedOrder, unreversedSettlement, payoutAccount bool
	if err := tx.QueryRow(ctx, organizerDeletionBlockersQuery, id,
		int16(entity.OrderStatusRefunded), int16(entity.SettlementStatusReversed),
		int16(entity.ReservationStatusCompleted),
	).Scan(&unrefundedOrder, &unreversedSettlement, &payoutAccount); err != nil {
		return toAppErr(err, "failed to check organizer deletion blockers", slog.String("id", id))
	}
	switch {
	case unrefundedOrder:
		return apperr.New(codes.FailedPrecondition, "an event of the organizer has an order that is not refunded", slog.String("id", id))
	case unreversedSettlement:
		return apperr.New(codes.FailedPrecondition, "an event of the organizer has a settlement that is not reversed", slog.String("id", id))
	case payoutAccount:
		return apperr.New(codes.FailedPrecondition, "the organizer has a payout account", slog.String("id", id))
	}
	if dryRun {
		return nil
	}

	statements := []struct {
		query string
		args  []any
		what  string
	}{
		{deleteOrganizerAdmissionsQuery, []any{id}, "admissions"},
		{deleteOrganizerRejectedScansQuery, []any{id}, "rejected scans"},
		{deleteOrganizerReceptionLinksQuery, []any{id}, "reception links"},
		{deleteOrganizerSettlementsQuery, []any{id, int16(entity.SettlementStatusReversed)}, "settlements"},
		{deleteOrganizerTicketsQuery, []any{id, int16(entity.OrderStatusRefunded)}, "tickets"},
		{deleteOrganizerOrdersQuery, []any{id, int16(entity.OrderStatusRefunded)}, "orders"},
		{deleteOrganizerReservationsQuery, []any{id}, "reservations"},
		{deleteOrganizerTicketSalesQuery, []any{id}, "ticket sales"},
		{deleteOrganizerSeriesQuery, []any{id}, "series"},
		{deleteOrganizerMediaQuery, []any{id}, "media"},
		{deleteOrganizerQuery, []any{id}, "organizer"},
	}
	for _, st := range statements {
		if _, err := tx.Exec(ctx, st.query, st.args...); err != nil {
			return toAppErr(err, "failed to delete organizer "+st.what, slog.String("id", id))
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return toAppErr(err, "failed to commit organizer delete", slog.String("id", id))
	}
	r.db.logger.Info(ctx, "organizer deleted", slog.String("entityType", "organizer"), slog.String("id", id))
	return nil
}

// SetSellerDetails replaces the organizer's seller details as a whole.
func (r *OrganizerRepository) SetSellerDetails(ctx context.Context, id string, details entity.SellerDetails) error {
	if err := details.Validate(); err != nil {
		return apperr.Wrap(err, codes.InvalidArgument, "invalid seller details")
	}
	tag, err := r.db.Pool.Exec(ctx, setOrganizerSellerDetailsQuery, id,
		details.LegalName, details.RepresentativeName, details.Address, details.PhoneNumber, details.ContactEmail)
	if err != nil {
		return toAppErr(err, "failed to set organizer seller details", slog.String("id", id))
	}
	if tag.RowsAffected() == 0 {
		return apperr.New(codes.NotFound, "organizer not found")
	}
	return nil
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
