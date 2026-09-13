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

// TestIssuanceRepository_Integration exercises the ⑤ Order/Ticket persistence
// against a real local Postgres: the atomic Issue (Order + N tickets), the
// idempotency guard (one Order per application), the awaiting-issuance sweep
// work-list, the buyer read queries, and the refund-side status/void updates.
//
// Runs only when a local database is available (make test provides one); skipped
// otherwise, like the other rdb integration tests.
func TestIssuanceRepository_Integration(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	ctx := context.Background()

	phaseRepo := rdb.NewLotteryPhaseRepository(testDB)
	appRepo := rdb.NewTicketApplicationRepository(testDB)
	issuanceRepo := rdb.NewIssuanceRepository(testDB)
	orderRepo := rdb.NewOrderRepository(testDB)
	ticketRepo := rdb.NewTicketRepository(testDB)

	// -- seed a Won application (orders.application_id FK) on a phase whose event
	//    (tickets.event_id FK) we reuse for the issued tickets. --
	phase, eventID := seedLotteryPhase(t, phaseRepo)
	buyerID := seedUser(t, "buyer", entity.NewID()+"@example.test", entity.NewID())
	app := seedApplication(t, appRepo, phase.ID, buyerID, entity.TicketApplicationStateWon)

	// Before issuance, the Won application appears in the sweep work-list.
	awaiting, err := issuanceRepo.ListApplicationIDsAwaitingIssuance(ctx)
	require.NoError(t, err)
	assert.Contains(t, awaiting, app.ID, "a Won application without an Order must be awaiting issuance")

	// -- Issue: one Order + 2 covered tickets, atomically. --
	paidTime := time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC)
	order := &entity.Order{
		ID:            entity.OrderID(entity.NewID()),
		BuyerID:       entity.UserID(buyerID),
		ApplicationID: app.ID,
		Payment: entity.Payment{
			Provider:         entity.PaymentProviderStripe,
			PaymentIntentRef: "pi_itest_" + entity.NewID(),
			CardBrand:        "visa",
			CardLast4:        "4242",
		},
		Status:   entity.OrderStatusPaid,
		Amount:   10000,
		Currency: "JPY",
		PaidTime: paidTime,
	}
	tickets := make([]*entity.Ticket, 0, 2)
	for range 2 {
		tickets = append(tickets, &entity.Ticket{
			ID:                             entity.TicketID(entity.NewID()),
			OrderID:                        order.ID,
			HolderID:                       entity.UserID(buyerID),
			EventID:                        eventID,
			HolderIdentity:                 entity.ApplicantIdentity{FullName: "山田太郎", PhoneNumber: "+819012345678"},
			ResaleWithoutConsentProhibited: true,
			Status:                         entity.TicketStatusIssued,
			IssuedTime:                     paidTime,
		})
	}
	require.NoError(t, issuanceRepo.Issue(ctx, order, tickets))

	// -- Order reads. --
	gotByApp, err := orderRepo.GetByApplicationID(ctx, app.ID)
	require.NoError(t, err)
	assert.Equal(t, order.ID, gotByApp.ID)
	assert.Equal(t, entity.OrderStatusPaid, gotByApp.Status)
	assert.Equal(t, entity.PaymentProviderStripe, gotByApp.Payment.Provider)
	assert.Equal(t, "visa", gotByApp.Payment.CardBrand)
	assert.Equal(t, "4242", gotByApp.Payment.CardLast4)
	assert.Equal(t, int64(10000), gotByApp.Amount)
	assert.Equal(t, "JPY", gotByApp.Currency)

	gotByID, err := orderRepo.Get(ctx, order.ID)
	require.NoError(t, err)
	assert.Equal(t, order.ID, gotByID.ID)

	// -- Ticket reads: N covered tickets, bound to the buyer + event. --
	byOrder, err := ticketRepo.ListByOrder(ctx, order.ID)
	require.NoError(t, err)
	require.Len(t, byOrder, 2)
	for _, tk := range byOrder {
		assert.Equal(t, eventID, tk.EventID)
		assert.True(t, tk.ResaleWithoutConsentProhibited)
		assert.Equal(t, entity.TicketStatusIssued, tk.Status)
		assert.Empty(t, tk.VerifiedIdentityID)
	}
	byHolder, err := ticketRepo.ListByHolder(ctx, entity.UserID(buyerID))
	require.NoError(t, err)
	assert.Len(t, byHolder, 2)

	// After issuance the application is no longer awaiting.
	awaiting2, err := issuanceRepo.ListApplicationIDsAwaitingIssuance(ctx)
	require.NoError(t, err)
	assert.NotContains(t, awaiting2, app.ID, "an issued application must not be awaiting issuance")

	// -- Idempotency: a second Issue for the same application surfaces AlreadyExists. --
	dup := *order
	dup.ID = entity.OrderID(entity.NewID())
	err = issuanceRepo.Issue(ctx, &dup, nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, apperr.ErrAlreadyExists, "one Order per application (unique index)")

	// -- Refund side: status update + ticket void. --
	require.NoError(t, orderRepo.UpdateStatus(ctx, order.ID, entity.OrderStatusRefunded))
	refunded, err := orderRepo.Get(ctx, order.ID)
	require.NoError(t, err)
	assert.Equal(t, entity.OrderStatusRefunded, refunded.Status)

	require.NoError(t, ticketRepo.VoidByOrder(ctx, order.ID))
	voided, err := ticketRepo.ListByOrder(ctx, order.ID)
	require.NoError(t, err)
	for _, tk := range voided {
		assert.Equal(t, entity.TicketStatusVoided, tk.Status)
	}

	// -- NotFound paths. --
	_, err = orderRepo.Get(ctx, entity.OrderID(entity.NewID()))
	assert.ErrorIs(t, err, apperr.ErrNotFound)
	_, err = orderRepo.GetByApplicationID(ctx, entity.TicketApplicationID(entity.NewID()))
	assert.ErrorIs(t, err, apperr.ErrNotFound)
}
