package rdb_test

import (
	"context"
	"testing"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// seedOrderForSettlement issues a Paid order together with its Held
// settlement, atomically, via IssuanceRepository.Issue (backend#468: Issue now
// creates the Order, its Tickets, and its Settlement together). Returns the
// created Order and Settlement so callers can exercise ListHeld/MarkReleased
// against a real settlement row.
func seedOrderForSettlement(
	t *testing.T,
	issuanceRepo *rdb.IssuanceRepository,
	appID entity.TicketApplicationID,
	buyerID, organizerID, eventID string,
	settledAt time.Time,
) (*entity.Order, *entity.Settlement) {
	t.Helper()
	order := &entity.Order{
		ID:            entity.OrderID(entity.NewID()),
		BuyerID:       entity.UserID(buyerID),
		ApplicationID: appID,
		Payment: entity.Payment{
			Provider:         entity.PaymentProviderStripe,
			PaymentIntentRef: "pi_settlement_itest_" + entity.NewID(),
			CardBrand:        "visa",
			CardLast4:        "4242",
		},
		Status:   entity.OrderStatusPaid,
		Amount:   10000,
		Currency: "JPY",
		PaidTime: settledAt,
	}
	settlement := &entity.Settlement{
		ID:          entity.SettlementID(entity.NewID()),
		OrderID:     order.ID,
		OrganizerID: organizerID,
		EventID:     eventID,
		Status:      entity.SettlementStatusHeld,
		CreatedTime: settledAt,
	}
	require.NoError(t, issuanceRepo.Issue(context.Background(), order, nil, settlement))
	return order, settlement
}

// insertSettlementSplit inserts a settlement_splits row directly, since no
// repository method creates splits yet (they are seeded by the payout
// sweeper's splits engine, out of scope here).
func insertSettlementSplit(t *testing.T, id entity.SettlementID, payeeOrganizerID string, amount int64) {
	t.Helper()
	_, err := testDB.Pool.Exec(context.Background(),
		`INSERT INTO settlement_splits (settlement_id, payee_organizer_id, amount) VALUES ($1, $2, $3)`,
		string(id), payeeOrganizerID, amount,
	)
	require.NoError(t, err)
}

// TestSettlementRepository_Integration exercises the Settlement payout
// persistence against a real local Postgres: the Held settlement Order.Issue
// creates (backend#468), ListHeld, and MarkReleased — both the happy path
// and, per #477, the atomicity of the status flip together with every
// split's transfer_ref update.
func TestSettlementRepository_Integration(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	ctx := context.Background()

	phaseRepo := rdb.NewLotteryPhaseRepository(testDB)
	appRepo := rdb.NewTicketApplicationRepository(testDB)
	issuanceRepo := rdb.NewIssuanceRepository(testDB)
	settlementRepo := rdb.NewSettlementRepository(testDB)

	phase, eventID := seedLotteryPhase(t, phaseRepo)
	buyerID := seedUser(t, "settlement-buyer", entity.NewID()+"@example.test", entity.NewID())
	app := seedApplication(t, appRepo, phase.ID, buyerID, entity.TicketApplicationStateWon)

	organizerID := entity.NewID()
	settledAt := time.Date(2026, 9, 13, 12, 30, 0, 0, time.UTC)

	// -- Order.Issue creates the Held settlement atomically with the Order
	//    (backend#468). --
	_, created := seedOrderForSettlement(t, issuanceRepo, app.ID, buyerID, organizerID, eventID, settledAt)
	assert.Equal(t, entity.SettlementStatusHeld, created.Status)

	// -- ListHeld includes the freshly created settlement. --
	held, err := settlementRepo.ListHeld(ctx)
	require.NoError(t, err)
	var found bool
	for _, h := range held {
		if h.ID == created.ID {
			found = true
		}
	}
	assert.True(t, found, "Held settlement must appear in ListHeld")

	// @spec components/entity/settlement/mark-released "Held settlement released"
	// @spec components/entity/settlement/mark-released "Already released or reversed"
	t.Run("MarkReleased happy path flips status and persists every split's transfer_ref together", func(t *testing.T) {
		payeeID := entity.NewID()
		insertSettlementSplit(t, created.ID, payeeID, 8000)

		releasedAt := settledAt.Add(48 * time.Hour)
		err := settlementRepo.MarkReleased(ctx, created.ID, "ch_settlement_itest", releasedAt, []entity.SettlementSplit{
			{PayeeOrganizerID: payeeID, Amount: 8000, TransferRef: "tr_settlement_itest"},
		})
		require.NoError(t, err)

		got, err := settlementRepo.Get(ctx, created.ID)
		require.NoError(t, err)
		assert.Equal(t, entity.SettlementStatusReleased, got.Status)
		assert.Equal(t, "ch_settlement_itest", got.ChargeRef)
		require.Len(t, got.Splits, 1)
		assert.Equal(t, "tr_settlement_itest", got.Splits[0].TransferRef)

		// A second MarkReleased on an already-Released settlement must fail the
		// status = Held guard and change nothing.
		err = settlementRepo.MarkReleased(ctx, created.ID, "ch_other", releasedAt, nil)
		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition, "re-releasing an already-Released settlement must be rejected")
	})

	// @spec components/entity/settlement/mark-released "Unknown id"
	t.Run("MarkReleased on an unknown id fails with FailedPrecondition", func(t *testing.T) {
		err := settlementRepo.MarkReleased(ctx, entity.SettlementID(entity.NewID()), "ch_unknown", settledAt, nil)
		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
	})

	// #477: a failing split update must roll back the whole MarkReleased,
	// leaving the settlement Held with no split touched (atomicity).
	// @spec components/entity/settlement/mark-released "Failure changes nothing"
	t.Run("MarkReleased rolls back the status flip when a split update fails", func(t *testing.T) {
		// A fresh buyer + application avoids the uq_ticket_applications_active
		// conflict with the outer test's Won application on the same phase.
		otherBuyerID := seedUser(t, "settlement-buyer-2", entity.NewID()+"@example.test", entity.NewID())
		otherApp := seedApplication(t, appRepo, phase.ID, otherBuyerID, entity.TicketApplicationStateWon)
		_, created := seedOrderForSettlement(t, issuanceRepo, otherApp.ID, otherBuyerID, organizerID, eventID, settledAt)

		payeeOK := entity.NewID()
		payeeBad := entity.NewID()
		insertSettlementSplit(t, created.ID, payeeOK, 3000)
		insertSettlementSplit(t, created.ID, payeeBad, 5000)

		releasedAt := settledAt.Add(48 * time.Hour)
		// The first split's transfer_ref update would succeed; the second's
		// empty TransferRef violates chk_settlement_splits_transfer_ref_not_empty,
		// forcing the whole transaction to roll back.
		err := settlementRepo.MarkReleased(ctx, created.ID, "ch_should_not_persist", releasedAt, []entity.SettlementSplit{
			{PayeeOrganizerID: payeeOK, Amount: 3000, TransferRef: "tr_would_succeed"},
			{PayeeOrganizerID: payeeBad, Amount: 5000, TransferRef: ""},
		})
		require.Error(t, err)
		assert.ErrorIs(t, err, apperr.ErrInvalidArgument, "a split constraint violation must surface as InvalidArgument")

		got, err := settlementRepo.Get(ctx, created.ID)
		require.NoError(t, err)
		assert.Equal(t, entity.SettlementStatusHeld, got.Status, "settlement must stay Held when the split update fails")
		assert.Empty(t, got.ChargeRef, "charge_ref must not be persisted on rollback")
		assert.True(t, got.ReleasedTime.IsZero(), "released_at must not be persisted on rollback")
		require.Len(t, got.Splits, 2)
		for _, split := range got.Splits {
			assert.Empty(t, split.TransferRef, "no split's transfer_ref may be persisted when the transaction rolls back")
		}

		// The settlement being still Held means a retry can succeed once the
		// bad split is fixed.
		err = settlementRepo.MarkReleased(ctx, created.ID, "ch_retry", releasedAt, []entity.SettlementSplit{
			{PayeeOrganizerID: payeeOK, Amount: 3000, TransferRef: "tr_retry_ok"},
			{PayeeOrganizerID: payeeBad, Amount: 5000, TransferRef: "tr_retry_bad"},
		})
		require.NoError(t, err, "a retry with valid splits must succeed after the rollback")
	})
}
