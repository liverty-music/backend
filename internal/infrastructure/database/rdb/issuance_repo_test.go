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

// TestIssuanceRepository_Integration exercises the ⑤ Order/Ticket/Settlement
// persistence against a real local Postgres: the atomic Issue (Order + N
// tickets + Held settlement with its split, backend#468), the idempotency
// guard (one Order per lottery entry), the user read queries, and the refund-side status/void updates.
//
// Runs only when a local database is available (make test provides one); skipped
// otherwise, like the other rdb integration tests.
func TestIssuanceRepository_Integration(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	ctx := context.Background()

	issuanceRepo := rdb.NewIssuanceRepository(testDB)
	orderRepo := rdb.NewOrderRepository(testDB)
	ticketRepo := rdb.NewTicketRepository(testDB)
	settlementRepo := rdb.NewSettlementRepository(testDB)

	// -- seed a Won lottery entry (orders.lottery_entry_id FK) on a ticket type
	//    whose event (tickets.event_id FK) we reuse for the issued tickets. --
	tt := seedTicketType(t, entity.TicketSaleMethodLottery, 100, 4)
	eventID := tt.EventID
	userID := seedUser(t, "fan", entity.NewID()+"@example.test", entity.NewID())
	entry := insertLotteryEntry(t, tt.ID, entity.UserID(userID), 2, entity.LotteryEntryStateWon)
	organizerID := seedOrganizer(t)

	// -- Issue: one Order + 2 covered tickets + a Held settlement, atomically. --
	// @spec components/entity/order/issue "Order issued"
	paidTime := time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC)
	order := &entity.Order{
		ID:             entity.OrderID(entity.NewID()),
		UserID:         entity.UserID(userID),
		LotteryEntryID: entry.ID,
		Payment: entity.Payment{
			Provider:          entity.PaymentProviderStripe,
			ProviderPaymentID: "pi_itest_" + entity.NewID(),
			CardBrand:         "visa",
			CardLast4:         "4242",
		},
		Status:   entity.OrderStatusPaid,
		Amount:   10000,
		Currency: "JPY",
		PayTime:  paidTime,
	}
	tickets := make([]*entity.Ticket, 0, 2)
	for range 2 {
		tickets = append(tickets, &entity.Ticket{
			ID:                             entity.TicketID(entity.NewID()),
			OrderID:                        order.ID,
			UserID:                         entity.UserID(userID),
			EventID:                        eventID,
			ResaleWithoutConsentProhibited: true,
			Status:                         entity.TicketStatusIssued,
			IssueTime:                      paidTime,
		})
	}
	settlement := &entity.Settlement{
		ID:          entity.SettlementID(entity.NewID()),
		OrderID:     order.ID,
		OrganizerID: organizerID,
		EventID:     eventID,
		Status:      entity.SettlementStatusHeld,
		Splits: []entity.SettlementSplit{
			{PayeeOrganizerID: organizerID, Amount: order.Amount},
		},
		CreateTime: paidTime,
	}
	require.NoError(t, issuanceRepo.Issue(ctx, order, tickets, settlement))

	// -- Order reads. --
	gotByApp, err := orderRepo.GetByLotteryEntryID(ctx, entry.ID)
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

	// -- Ticket reads: N covered tickets, bound to the user + event. --
	byOrder := ticketsOfOrder(t, ticketRepo, entity.UserID(userID), order.ID)
	require.Len(t, byOrder, 2)
	for _, tk := range byOrder {
		assert.Equal(t, eventID, tk.EventID)
		assert.True(t, tk.ResaleWithoutConsentProhibited)
		assert.Equal(t, entity.TicketStatusIssued, tk.Status)
		assert.Empty(t, tk.IdentityVerificationID)
	}
	byUser, err := ticketRepo.ListByUser(ctx, entity.UserID(userID))
	require.NoError(t, err)
	assert.Len(t, byUser, 2)

	// -- Settlement read: a Held settlement for the Order's Organizer, with one
	//    split for the Order's full amount (backend#468). --
	gotSettlement, err := settlementRepo.GetByOrderID(ctx, order.ID)
	require.NoError(t, err)
	assert.Equal(t, settlement.ID, gotSettlement.ID)
	assert.Equal(t, order.ID, gotSettlement.OrderID)
	assert.Equal(t, organizerID, gotSettlement.OrganizerID)
	assert.Equal(t, eventID, gotSettlement.EventID)
	assert.Equal(t, entity.SettlementStatusHeld, gotSettlement.Status)
	require.Len(t, gotSettlement.Splits, 1)
	assert.Equal(t, organizerID, gotSettlement.Splits[0].PayeeOrganizerID)
	assert.Equal(t, order.Amount, gotSettlement.Splits[0].Amount)
	assert.Empty(t, gotSettlement.Splits[0].ProviderPayoutID, "not released yet")

	// -- Idempotency: a second Issue for the same entry surfaces
	//    AlreadyExists and rolls back the whole transaction, including the
	//    settlement that would otherwise have been inserted. --
	// @spec components/entity/order/issue "Second order for the lottery entry"
	dup := *order
	dup.ID = entity.OrderID(entity.NewID())
	dupSettlement := &entity.Settlement{
		ID:          entity.SettlementID(entity.NewID()),
		OrderID:     dup.ID,
		OrganizerID: organizerID,
		EventID:     eventID,
		Status:      entity.SettlementStatusHeld,
		Splits:      []entity.SettlementSplit{{PayeeOrganizerID: organizerID, Amount: dup.Amount}},
		CreateTime:  paidTime,
	}
	err = issuanceRepo.Issue(ctx, &dup, nil, dupSettlement)
	require.Error(t, err)
	assert.ErrorIs(t, err, apperr.ErrAlreadyExists, "one Order per lottery entry (unique index)")

	_, err = settlementRepo.GetByOrderID(ctx, dup.ID)
	assert.ErrorIs(t, err, apperr.ErrNotFound, "the rolled-back transaction must not leave an orphan settlement")

	// -- NotFound paths. --
	_, err = orderRepo.Get(ctx, entity.OrderID(entity.NewID()))
	assert.ErrorIs(t, err, apperr.ErrNotFound)
	_, err = orderRepo.GetByLotteryEntryID(ctx, entity.LotteryEntryID(entity.NewID()))
	assert.ErrorIs(t, err, apperr.ErrNotFound)
}

// TestIssuanceRepository_Issue_FailureStoresNothing asserts that Issue is
// truly atomic: when any statement in the transaction fails (here, a Ticket
// whose event_id violates the tickets.event_id foreign key), neither the
// Order, any Ticket, nor the Settlement is left behind.
//
// @spec components/entity/order/issue "Failure stores nothing"
func TestIssuanceRepository_Issue_FailureStoresNothing(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	ctx := context.Background()

	issuanceRepo := rdb.NewIssuanceRepository(testDB)
	orderRepo := rdb.NewOrderRepository(testDB)
	ticketRepo := rdb.NewTicketRepository(testDB)
	settlementRepo := rdb.NewSettlementRepository(testDB)

	tt := seedTicketType(t, entity.TicketSaleMethodLottery, 100, 4)
	eventID := tt.EventID
	userID := seedUser(t, "failure-fan", entity.NewID()+"@example.test", entity.NewID())
	entry := insertLotteryEntry(t, tt.ID, entity.UserID(userID), 2, entity.LotteryEntryStateWon)
	organizerID := seedOrganizer(t)

	paidTime := time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC)
	order := &entity.Order{
		ID:             entity.OrderID(entity.NewID()),
		UserID:         entity.UserID(userID),
		LotteryEntryID: entry.ID,
		Payment: entity.Payment{
			Provider:          entity.PaymentProviderStripe,
			ProviderPaymentID: "pi_fail_" + entity.NewID(),
		},
		Status:   entity.OrderStatusPaid,
		Amount:   8000,
		Currency: "JPY",
		PayTime:  paidTime,
	}
	tickets := []*entity.Ticket{
		{
			ID:                             entity.TicketID(entity.NewID()),
			OrderID:                        order.ID,
			UserID:                         entity.UserID(userID),
			EventID:                        eventID,
			ResaleWithoutConsentProhibited: true,
			Status:                         entity.TicketStatusIssued,
			IssueTime:                      paidTime,
		},
		{
			// A non-existent event id violates tickets.event_id's foreign key,
			// forcing the second ticket insert (and therefore the whole
			// transaction) to fail.
			ID:                             entity.TicketID(entity.NewID()),
			OrderID:                        order.ID,
			UserID:                         entity.UserID(userID),
			EventID:                        entity.NewID(),
			ResaleWithoutConsentProhibited: true,
			Status:                         entity.TicketStatusIssued,
			IssueTime:                      paidTime,
		},
	}
	settlement := &entity.Settlement{
		ID:          entity.SettlementID(entity.NewID()),
		OrderID:     order.ID,
		OrganizerID: organizerID,
		EventID:     eventID,
		Status:      entity.SettlementStatusHeld,
		Splits:      []entity.SettlementSplit{{PayeeOrganizerID: organizerID, Amount: order.Amount}},
		CreateTime:  paidTime,
	}

	err := issuanceRepo.Issue(ctx, order, tickets, settlement)
	require.Error(t, err)

	_, err = orderRepo.Get(ctx, order.ID)
	assert.ErrorIs(t, err, apperr.ErrNotFound, "no Order must be stored when any Ticket fails to insert")

	byOrder := ticketsOfOrder(t, ticketRepo, entity.UserID(userID), order.ID)
	assert.Empty(t, byOrder, "no Ticket must be stored when any Ticket fails to insert")

	_, err = settlementRepo.GetByOrderID(ctx, order.ID)
	assert.ErrorIs(t, err, apperr.ErrNotFound, "no Settlement must be stored when the transaction fails")
}

// TestIssuanceRepository_IssueFromCheckout covers issuance whose source is a
// Checkout: it is completed in the same transaction, only when Committed and
// charged, and at most once.
func TestIssuanceRepository_IssueFromCheckout(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	ctx := context.Background()
	checkouts := rdb.NewCheckoutRepository(testDB)
	issuanceRepo := rdb.NewIssuanceRepository(testDB)
	orderRepo := rdb.NewOrderRepository(testDB)
	start := saleWindowStart.Add(time.Hour)

	t.Run("order issued from a checkout", func(t *testing.T) {
		// @spec components/entity/order/issue "Order issued from a checkout"
		c := issuanceChargedCheckout(t, checkouts, start)
		order, tickets, settlement := checkoutOrder(t, c, start.Add(3*time.Minute))

		require.NoError(t, issuanceRepo.Issue(ctx, order, tickets, settlement))

		got, err := checkouts.Get(ctx, c.ID)
		require.NoError(t, err)
		assert.Equal(t, entity.CheckoutStatusCompleted, got.Status)

		byCheckout, err := orderRepo.GetByCheckoutID(ctx, c.ID)
		require.NoError(t, err)
		assert.Equal(t, order.ID, byCheckout.ID)
		assert.Equal(t, c.ID, byCheckout.CheckoutID)
		assert.Empty(t, byCheckout.LotteryEntryID)

		// @spec components/entity/order/issue "Second order for the checkout"
		dup, dupTickets, dupSettlement := checkoutOrder(t, c, start.Add(4*time.Minute))
		err = issuanceRepo.Issue(ctx, dup, dupTickets, dupSettlement)
		assert.ErrorIs(t, err, apperr.ErrAlreadyExists)
		_, err = orderRepo.Get(ctx, dup.ID)
		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})

	t.Run("checkout not charged", func(t *testing.T) {
		// @spec components/entity/order/issue "Checkout not charged"
		c := heldCheckout(t, checkouts, 2, start)
		require.NoError(t, checkouts.SetCardAuthorization(ctx, c.ID, "pi_nc_"+string(c.ID)))
		_, err := checkouts.Commit(ctx, c.ID, start.Add(time.Minute))
		require.NoError(t, err)
		order, tickets, settlement := checkoutOrder(t, c, start.Add(2*time.Minute))

		err = issuanceRepo.Issue(ctx, order, tickets, settlement)

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
		_, err = orderRepo.Get(ctx, order.ID)
		assert.ErrorIs(t, err, apperr.ErrNotFound)
		got, err := checkouts.Get(ctx, c.ID)
		require.NoError(t, err)
		assert.Equal(t, entity.CheckoutStatusCommitted, got.Status)
	})

	t.Run("failure stores nothing and the checkout stays committed", func(t *testing.T) {
		c := issuanceChargedCheckout(t, checkouts, start)
		order, tickets, settlement := checkoutOrder(t, c, start.Add(3*time.Minute))
		tickets[1].EventID = entity.NewID() // violates the tickets.event_id foreign key

		err := issuanceRepo.Issue(ctx, order, tickets, settlement)

		require.Error(t, err)
		_, err = orderRepo.Get(ctx, order.ID)
		assert.ErrorIs(t, err, apperr.ErrNotFound)
		got, err := checkouts.Get(ctx, c.ID)
		require.NoError(t, err)
		assert.Equal(t, entity.CheckoutStatusCommitted, got.Status)
	})

	t.Run("two sources", func(t *testing.T) {
		c := issuanceChargedCheckout(t, checkouts, start)
		order, tickets, settlement := checkoutOrder(t, c, start.Add(3*time.Minute))
		order.LotteryEntryID = entity.LotteryEntryID(entity.NewID())

		err := issuanceRepo.Issue(ctx, order, tickets, settlement)

		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
	})
}

// issuanceChargedCheckout returns a Committed checkout of 2 tickets with a
// recorded charge.
func issuanceChargedCheckout(t *testing.T, repo *rdb.CheckoutRepository, start time.Time) *entity.Checkout {
	t.Helper()
	ctx := context.Background()
	c := heldCheckout(t, repo, 2, start)
	require.NoError(t, repo.SetCardAuthorization(ctx, c.ID, "pi_"+string(c.ID)))
	_, err := repo.Commit(ctx, c.ID, start.Add(time.Minute))
	require.NoError(t, err)
	require.NoError(t, repo.RecordCapture(ctx, c.ID, start.Add(2*time.Minute)))
	return c
}

func TestIssuanceRepository_ListLotteryEntryIDsAwaitingIssuance(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	ctx := context.Background()
	issuanceRepo := rdb.NewIssuanceRepository(testDB)
	tt := seedTicketType(t, entity.TicketSaleMethodLottery, 100, 4)

	// awaiting lists the work-list now.
	awaiting := func(t *testing.T) []entity.LotteryEntryID {
		t.Helper()
		ids, err := issuanceRepo.ListLotteryEntryIDsAwaitingIssuance(ctx)
		require.NoError(t, err)
		return ids
	}

	t.Run("won without order", func(t *testing.T) {
		// @spec components/entity/order/list-lottery-entry-ids-awaiting-issuance "Won without order"
		won := insertLotteryEntry(t, tt.ID, newUserID(), 2, entity.LotteryEntryStateWon)

		assert.Contains(t, awaiting(t), won.ID)
	})

	t.Run("already issued", func(t *testing.T) {
		// @spec components/entity/order/list-lottery-entry-ids-awaiting-issuance "Already issued"
		won := insertLotteryEntry(t, tt.ID, newUserID(), 1, entity.LotteryEntryStateWon)
		order := &entity.Order{
			ID:             entity.OrderID(entity.NewID()),
			UserID:         won.UserID,
			LotteryEntryID: won.ID,
			Payment:        entity.Payment{Provider: entity.PaymentProviderStripe, ProviderPaymentID: won.CardAuthorizationID},
			Status:         entity.OrderStatusPaid,
			Amount:         3000,
			Currency:       "JPY",
			PayTime:        saleWindowStart,
		}
		settlement := entity.NewHeldSettlement(order, seedOrganizer(t), tt.EventID, 240, saleWindowStart)
		require.NoError(t, issuanceRepo.Issue(ctx, order, nil, settlement))

		assert.NotContains(t, awaiting(t), won.ID)
	})

	t.Run("not won", func(t *testing.T) {
		// @spec components/entity/order/list-lottery-entry-ids-awaiting-issuance "Not won"
		ids := []entity.LotteryEntryID{}
		for _, state := range []entity.LotteryEntryState{
			entity.LotteryEntryStateEntered, entity.LotteryEntryStateLost,
			entity.LotteryEntryStateWithdrawn, entity.LotteryEntryStateDraft,
		} {
			ids = append(ids, insertLotteryEntry(t, tt.ID, newUserID(), 1, state).ID)
		}

		got := awaiting(t)
		for _, id := range ids {
			assert.NotContains(t, got, id)
		}
	})
}

// ticketsOfOrder returns the user's tickets that belong to the order.
func ticketsOfOrder(t *testing.T, repo *rdb.TicketRepository, userID entity.UserID, orderID entity.OrderID) []*entity.Ticket {
	t.Helper()
	all, err := repo.ListByUser(context.Background(), userID)
	require.NoError(t, err)
	var out []*entity.Ticket
	for _, tk := range all {
		if tk.OrderID == orderID {
			out = append(out, tk)
		}
	}
	return out
}
