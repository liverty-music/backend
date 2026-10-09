package rdb

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
)

// IssuanceRepository implements entity.IssuanceRepository for PostgreSQL: it
// persists an Order, its N tickets, its Held settlement and its ORDER.paid
// outbox row atomically in one transaction, and completes a source
// reservation (backend#468).
type IssuanceRepository struct {
	db *Database
}

// Compile-time interface compliance check.
var _ entity.IssuanceRepository = (*IssuanceRepository)(nil)

const (
	issuanceInsertOrderQuery = `
		INSERT INTO orders (
			id, buyer_id, application_id, reservation_id, provider, payment_intent_ref, payment_method_ref,
			card_brand, card_last4, status, amount, currency, paid_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
	`
	issuanceInsertTicketQuery = `
		INSERT INTO tickets (
			id, order_id, holder_id, event_id, holder_full_name, holder_phone_number,
			verified_identity_id, resale_without_consent_prohibited, status, issued_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	`
	// issuanceInsertSettlementQuery inserts the Held settlement row for the
	// issued Order. Settlement.organizer_id/event_id are NOT NULL, so the
	// caller (IssuanceUseCase) must resolve the Organizer before calling Issue.
	issuanceInsertSettlementQuery = `
		INSERT INTO settlements (id, order_id, organizer_id, event_id, status, settled_at, platform_fee_rate_bps)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`
	// issuanceInsertSettlementSplitQuery inserts one payout split for the
	// settlement just created. transfer_ref/transfer_reversal_ref default to
	// NULL until the payout sweeper releases the settlement.
	issuanceInsertSettlementSplitQuery = `
		INSERT INTO settlement_splits (settlement_id, payee_organizer_id, amount)
		VALUES ($1, $2, $3)
	`
	// issuanceCompleteReservationQuery completes the source reservation, only
	// when it is Committed and charged.
	issuanceCompleteReservationQuery = `
		UPDATE reservations SET status = 3
		WHERE id = $1 AND status = 2 AND capture_at IS NOT NULL
	`
	issuanceReservationHasOrderQuery = `SELECT EXISTS (SELECT 1 FROM orders WHERE reservation_id = $1)`
	// issuanceInsertOutboxQuery records the Order's ORDER.paid announcement in
	// the issuance transaction; the outbox relay publishes it.
	issuanceInsertOutboxQuery = `
		INSERT INTO outbox (id, subject, message_id, payload, recorded_at)
		VALUES ($1, $2, $3, $4, $5)
	`
	// issuanceListAwaitingQuery returns Won (state 2) applications that have no
	// order yet — the issuance sweeper work-list.
	issuanceListAwaitingQuery = `
		SELECT ta.id
		FROM ticket_applications ta
		LEFT JOIN orders o ON o.application_id = ta.id
		WHERE ta.state = 2 AND o.id IS NULL
		ORDER BY ta.id
	`
)

// NewIssuanceRepository creates a new issuance repository instance.
func NewIssuanceRepository(db *Database) *IssuanceRepository {
	return &IssuanceRepository{db: db}
}

// Issue atomically inserts the order, its N tickets, and its Held settlement
// (with splits) in one transaction. A duplicate order for the same application
// (unique index on orders.application_id) surfaces as apperr.ErrAlreadyExists
// so the caller re-reads the existing order instead of double-issuing.
//
// settlement is required (non-nil); the caller resolves the event's Organizer
// before calling Issue so the Order is never committed without its payout record.
func (r *IssuanceRepository) Issue(ctx context.Context, order *entity.Order, tickets []*entity.Ticket, settlement *entity.Settlement) error {
	if err := order.ValidateSource(); err != nil {
		return apperr.Wrap(err, codes.InvalidArgument, "invalid order source")
	}
	payload, err := json.Marshal(entity.OrderPaidData{
		OrderID:       string(order.ID),
		BuyerID:       string(order.BuyerID),
		EventID:       settlement.EventID,
		TicketCount:   len(tickets),
		Amount:        order.Amount,
		Currency:      order.Currency,
		ApplicationID: string(order.ApplicationID),
		ReservationID: string(order.ReservationID),
	})
	if err != nil {
		return apperr.Wrap(err, codes.Internal, "failed to encode the order paid event")
	}

	tx, err := r.db.Pool.Begin(ctx)
	if err != nil {
		return toAppErr(err, "failed to begin issuance transaction")
	}
	// Rollback is a no-op after a successful Commit.
	defer func() { _ = tx.Rollback(ctx) }()

	// A reservation is completed first, so an uncharged one stores nothing.
	if order.ReservationID != "" {
		tag, err := tx.Exec(ctx, issuanceCompleteReservationQuery, string(order.ReservationID))
		if err != nil {
			return toAppErr(err, "failed to complete reservation", slog.String("reservation_id", string(order.ReservationID)))
		}
		if tag.RowsAffected() == 0 {
			// A reservation already issued is Completed: report the duplicate.
			var issued bool
			if err := tx.QueryRow(ctx, issuanceReservationHasOrderQuery, string(order.ReservationID)).Scan(&issued); err != nil {
				return toAppErr(err, "failed to check the reservation's order", slog.String("reservation_id", string(order.ReservationID)))
			}
			if issued {
				return apperr.New(codes.AlreadyExists, "an order already exists for the reservation",
					slog.String("reservation_id", string(order.ReservationID)))
			}
			return apperr.New(codes.FailedPrecondition, "reservation is not Committed and charged",
				slog.String("reservation_id", string(order.ReservationID)))
		}
	}

	if _, err := tx.Exec(ctx, issuanceInsertOrderQuery,
		string(order.ID), string(order.BuyerID), nullableUUID(string(order.ApplicationID)),
		nullableUUID(string(order.ReservationID)), int16(order.Payment.Provider), order.Payment.PaymentIntentRef, order.Payment.PaymentMethodRef,
		order.Payment.CardBrand, order.Payment.CardLast4,
		int16(order.Status), order.Amount, order.Currency, order.PaidTime,
	); err != nil {
		return toAppErr(err, "failed to insert order",
			slog.String("application_id", string(order.ApplicationID)),
			slog.String("reservation_id", string(order.ReservationID)))
	}

	for _, t := range tickets {
		if _, err := tx.Exec(ctx, issuanceInsertTicketQuery,
			string(t.ID), string(t.OrderID), string(t.HolderID), t.EventID,
			t.HolderIdentity.FullName, t.HolderIdentity.PhoneNumber,
			nullableUUID(t.VerifiedIdentityID), t.ResaleWithoutConsentProhibited,
			int16(t.Status), t.IssuedTime,
		); err != nil {
			return toAppErr(err, "failed to insert ticket", slog.String("ticket_id", string(t.ID)))
		}
	}

	if _, err := tx.Exec(ctx, issuanceInsertSettlementQuery,
		string(settlement.ID), string(settlement.OrderID), settlement.OrganizerID, settlement.EventID,
		int16(settlement.Status), settlement.CreatedTime, settlement.PlatformFeeRateBps,
	); err != nil {
		return toAppErr(err, "failed to insert settlement", slog.String("settlement_id", string(settlement.ID)))
	}
	for _, split := range settlement.Splits {
		if _, err := tx.Exec(ctx, issuanceInsertSettlementSplitQuery,
			string(settlement.ID), split.PayeeOrganizerID, split.Amount,
		); err != nil {
			return toAppErr(err, "failed to insert settlement split",
				slog.String("settlement_id", string(settlement.ID)),
				slog.String("payee_organizer_id", split.PayeeOrganizerID))
		}
	}

	if _, err := tx.Exec(ctx, issuanceInsertOutboxQuery,
		entity.NewID(), entity.SubjectOrderPaid, string(order.ID), payload, order.PaidTime,
	); err != nil {
		return toAppErr(err, "failed to record the order paid event", slog.String("order_id", string(order.ID)))
	}

	if err := tx.Commit(ctx); err != nil {
		return toAppErr(err, "failed to commit issuance transaction")
	}

	r.db.logger.Info(ctx, "issued order and tickets",
		slog.String("entityType", "orders"),
		slog.String("orderID", string(order.ID)),
		slog.String("applicationID", string(order.ApplicationID)),
		slog.String("reservationID", string(order.ReservationID)),
		slog.Int("ticketCount", len(tickets)),
	)
	return nil
}

// ListApplicationIDsAwaitingIssuance returns Won applications without an order.
func (r *IssuanceRepository) ListApplicationIDsAwaitingIssuance(ctx context.Context) ([]entity.TicketApplicationID, error) {
	rows, err := r.db.Pool.Query(ctx, issuanceListAwaitingQuery)
	if err != nil {
		return nil, toAppErr(err, "failed to list applications awaiting issuance")
	}
	defer rows.Close()

	var ids []entity.TicketApplicationID
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, toAppErr(err, "failed to scan application id awaiting issuance")
		}
		ids = append(ids, entity.TicketApplicationID(id))
	}
	if err := rows.Err(); err != nil {
		return nil, toAppErr(err, "error iterating applications awaiting issuance")
	}
	return ids, nil
}

// nullableUUID returns nil for an empty id so a NULL is written to a nullable
// UUID column, or the id string otherwise.
func nullableUUID(id string) any {
	if id == "" {
		return nil
	}
	return id
}
