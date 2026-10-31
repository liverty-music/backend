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

// issuancePayTime is when the payments of the issued Orders are captured.
var issuancePayTime = time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)

// issuancePlatformFeeBps is the platform fee rate of the issued Orders'
// Settlements.
const issuancePlatformFeeBps = 800

// issuanceOf returns what Issue takes for the Pending Order of an event: the
// Order with its captured payment, one Issued Ticket per ticket of it and a
// Held Settlement for a new Organizer.
func issuanceOf(t *testing.T, pending *entity.Order, eventID string) (*entity.Order, []*entity.Ticket, *entity.Settlement) {
	t.Helper()
	order := *pending
	order.Payment.ProviderPaymentID = order.CardAuthorizationID
	order.Payment.ProviderPaymentMethodID = "pm_card_visa"
	order.Payment.CardBrand = "visa"
	order.Payment.CardLast4 = "4242"
	payTime := issuancePayTime
	order.PayTime = &payTime
	tickets := make([]*entity.Ticket, 0, order.TicketCount)
	for range order.TicketCount {
		tickets = append(tickets, &entity.Ticket{
			ID: entity.TicketID(entity.NewID()), OrderID: order.ID, UserID: order.UserID, EventID: eventID,
			ResaleWithoutConsentProhibited: true, Status: entity.TicketStatusIssued, IssueTime: payTime,
		})
	}
	settlement := entity.NewHeldSettlement(&order, seedOrganizer(t), eventID, issuancePlatformFeeBps, payTime)
	return &order, tickets, settlement
}

// wonEntry stores a Won entry of a new fan for count tickets of a new
// lottery stock, with its Pending Order, and returns it with the stock.
func wonEntry(t *testing.T, count int) (*entity.LotteryEntry, *stockFixture) {
	t.Helper()
	f := seedStock(t, entity.TicketSaleMethodLottery, 100, 4)
	return insertLotteryEntry(t, f, f.SaleID, newUserID(t), count, entity.LotteryEntryStateWon), f
}

// issuanceCheckout starts a Checkout of a new fan for 2 tickets of a new
// first-come stock with its card authorization and commits it; captured
// records the capture too. It returns the Checkout as read back with its
// Order, and the stock.
func issuanceCheckout(t *testing.T, captured bool) (*entity.Checkout, *stockFixture) {
	t.Helper()
	ctx := context.Background()
	repo := rdb.NewCheckoutRepository(testDB, testOutbox)
	f := seedStock(t, entity.TicketSaleMethodFirstCome, 100, 4)
	start := saleWindowStart.Add(time.Hour)
	c, err := repo.GetOrCreateHeld(ctx, f.ID, f.SaleID, newUserID(t), 2, nil, start)
	require.NoError(t, err)
	require.NoError(t, repo.SetCardAuthorization(ctx, c.ID, "pi_"+string(c.ID)))
	outcome, err := repo.Commit(ctx, c.ID, start.Add(time.Minute))
	require.NoError(t, err)
	require.Equal(t, entity.CommitOutcomeCommitted, outcome)
	if captured {
		require.NoError(t, repo.RecordCapture(ctx, c.ID, start.Add(2*time.Minute)))
	}
	got, err := repo.Get(ctx, c.ID)
	require.NoError(t, err)
	return got, f
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

func TestIssuanceRepository_Issue(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	cleanDatabase(t)
	ctx := context.Background()
	issuance := rdb.NewIssuanceRepository(testDB)
	orders := rdb.NewOrderRepository(testDB)
	tickets := rdb.NewTicketRepository(testDB, testOutbox)
	settlements := rdb.NewSettlementRepository(testDB)
	checkouts := rdb.NewCheckoutRepository(testDB, testOutbox)

	// assertUnissued asserts the Order is still Pending with no Ticket and no
	// Settlement.
	assertUnissued := func(t *testing.T, order *entity.Order) {
		t.Helper()
		got, err := orders.Get(ctx, order.ID)
		require.NoError(t, err)
		assert.Equal(t, entity.OrderStatusPending, got.Status)
		assert.Empty(t, got.Payment.ProviderPaymentID)
		assert.Nil(t, got.PayTime)
		assert.Empty(t, ticketsOfOrder(t, tickets, order.UserID, order.ID), "no Ticket is stored")
		_, err = settlements.GetByOrderID(ctx, order.ID)
		assert.ErrorIs(t, err, apperr.ErrNotFound, "no Settlement is stored")
	}

	t.Run("order issued", func(t *testing.T) {
		// @spec components/entity/order/issue "Order issued"
		entry, f := wonEntry(t, 3)
		order, issued, settlement := issuanceOf(t, entry.Order, f.EventID)

		require.NoError(t, issuance.Issue(ctx, order, issued, settlement))

		got, err := orders.GetByLotteryEntryID(ctx, entry.ID)
		require.NoError(t, err)
		assert.Equal(t, order.ID, got.ID)
		assert.Equal(t, entity.OrderStatusPaid, got.Status)
		assert.Equal(t, order.Payment, got.Payment)
		require.NotNil(t, got.PayTime)
		assert.True(t, issuancePayTime.Equal(*got.PayTime))
		assert.Equal(t, entry.Order.Amount, got.Amount)

		byOrder := ticketsOfOrder(t, tickets, order.UserID, order.ID)
		require.Len(t, byOrder, 3)
		for _, tk := range byOrder {
			assert.Equal(t, f.EventID, tk.EventID)
			assert.Equal(t, entity.TicketStatusIssued, tk.Status)
			assert.True(t, tk.ResaleWithoutConsentProhibited)
		}

		gotSettlement, err := settlements.GetByOrderID(ctx, order.ID)
		require.NoError(t, err)
		assert.Equal(t, settlement.ID, gotSettlement.ID)
		assert.Equal(t, entity.SettlementStatusHeld, gotSettlement.Status)
		assert.Equal(t, settlement.OrganizerID, gotSettlement.OrganizerID)
		assert.Equal(t, f.EventID, gotSettlement.EventID)
		require.Len(t, gotSettlement.Splits, 1)
		assert.Equal(t, settlement.OrganizerID, gotSettlement.Splits[0].PayeeOrganizerID)
		assert.Equal(t, order.Amount-entity.PlatformFee(order.Amount, issuancePlatformFeeBps), gotSettlement.Splits[0].Amount)
		assert.Empty(t, gotSettlement.Splits[0].ProviderPayoutID, "not released yet")
	})

	t.Run("order issued from a checkout", func(t *testing.T) {
		// @spec components/entity/order/issue "Order issued from a checkout"
		c, f := issuanceCheckout(t, true)
		order, issued, settlement := issuanceOf(t, c.Order, f.EventID)

		require.NoError(t, issuance.Issue(ctx, order, issued, settlement))

		got, err := orders.GetByCheckoutID(ctx, c.ID)
		require.NoError(t, err)
		assert.Equal(t, order.ID, got.ID)
		assert.Equal(t, entity.OrderStatusPaid, got.Status)
		assert.Len(t, ticketsOfOrder(t, tickets, order.UserID, order.ID), 2)
		gotCheckout, err := checkouts.Get(ctx, c.ID)
		require.NoError(t, err)
		assert.Equal(t, entity.CheckoutStatusCompleted, gotCheckout.Status)
	})

	t.Run("checkout not charged", func(t *testing.T) {
		// @spec components/entity/order/issue "Checkout not charged"
		c, f := issuanceCheckout(t, false)
		order, issued, settlement := issuanceOf(t, c.Order, f.EventID)

		err := issuance.Issue(ctx, order, issued, settlement)

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
		assertUnissued(t, c.Order)
		gotCheckout, err := checkouts.Get(ctx, c.ID)
		require.NoError(t, err)
		assert.Equal(t, entity.CheckoutStatusCommitted, gotCheckout.Status)
	})

	t.Run("failure stores nothing", func(t *testing.T) {
		// @spec components/entity/order/issue "Failure stores nothing"
		t.Run("lottery entry", func(t *testing.T) {
			entry, f := wonEntry(t, 2)
			order, issued, settlement := issuanceOf(t, entry.Order, f.EventID)
			issued[1].EventID = entity.NewID() // violates the tickets.event_id foreign key

			err := issuance.Issue(ctx, order, issued, settlement)

			require.Error(t, err)
			assertUnissued(t, entry.Order)
		})
		t.Run("settlement", func(t *testing.T) {
			issuedBefore, f := wonEntry(t, 1)
			before, beforeTickets, beforeSettlement := issuanceOf(t, issuedBefore.Order, f.EventID)
			require.NoError(t, issuance.Issue(ctx, before, beforeTickets, beforeSettlement))
			entry := insertLotteryEntry(t, f, f.SaleID, newUserID(t), 2, entity.LotteryEntryStateWon)
			order, issued, settlement := issuanceOf(t, entry.Order, f.EventID)
			settlement.ID = beforeSettlement.ID // violates the settlements primary key

			err := issuance.Issue(ctx, order, issued, settlement)

			require.Error(t, err)
			assertUnissued(t, entry.Order)
		})
		t.Run("checkout", func(t *testing.T) {
			c, f := issuanceCheckout(t, true)
			order, issued, settlement := issuanceOf(t, c.Order, f.EventID)
			issued[1].EventID = entity.NewID()

			err := issuance.Issue(ctx, order, issued, settlement)

			require.Error(t, err)
			assertUnissued(t, c.Order)
			gotCheckout, err := checkouts.Get(ctx, c.ID)
			require.NoError(t, err)
			assert.Equal(t, entity.CheckoutStatusCommitted, gotCheckout.Status, "the checkout stays Committed")
		})
	})

	t.Run("second order for the lottery entry", func(t *testing.T) {
		// @spec components/entity/order/issue "Second order for the lottery entry"
		entry, f := wonEntry(t, 2)
		order, issued, settlement := issuanceOf(t, entry.Order, f.EventID)
		require.NoError(t, issuance.Issue(ctx, order, issued, settlement))
		paid, err := orders.Get(ctx, order.ID)
		require.NoError(t, err)

		again, againTickets, againSettlement := issuanceOf(t, entry.Order, f.EventID)
		again.Payment.ProviderPaymentID = "pi_other_" + entity.NewID()
		err = issuance.Issue(ctx, again, againTickets, againSettlement)

		assert.ErrorIs(t, err, apperr.ErrAlreadyExists)
		got, err := orders.Get(ctx, order.ID)
		require.NoError(t, err)
		assert.Equal(t, paid, got, "the Order is unchanged")
		assert.Len(t, ticketsOfOrder(t, tickets, order.UserID, order.ID), 2, "no Ticket is added")
		gotSettlement, err := settlements.GetByOrderID(ctx, order.ID)
		require.NoError(t, err)
		assert.Equal(t, settlement.ID, gotSettlement.ID, "no Settlement is added")
	})

	t.Run("second order for the checkout", func(t *testing.T) {
		// @spec components/entity/order/issue "Second order for the checkout"
		c, f := issuanceCheckout(t, true)
		order, issued, settlement := issuanceOf(t, c.Order, f.EventID)
		require.NoError(t, issuance.Issue(ctx, order, issued, settlement))

		again, againTickets, againSettlement := issuanceOf(t, c.Order, f.EventID)
		err := issuance.Issue(ctx, again, againTickets, againSettlement)

		assert.ErrorIs(t, err, apperr.ErrAlreadyExists)
		assert.Len(t, ticketsOfOrder(t, tickets, order.UserID, order.ID), 2, "no Ticket is added")
		gotSettlement, err := settlements.GetByOrderID(ctx, order.ID)
		require.NoError(t, err)
		assert.Equal(t, settlement.ID, gotSettlement.ID, "no Settlement is added")
		gotCheckout, err := checkouts.Get(ctx, c.ID)
		require.NoError(t, err)
		assert.Equal(t, entity.CheckoutStatusCompleted, gotCheckout.Status)
	})

	t.Run("refunded order", func(t *testing.T) {
		entry, f := wonEntry(t, 1)
		order, issued, settlement := issuanceOf(t, entry.Order, f.EventID)
		require.NoError(t, issuance.Issue(ctx, order, issued, settlement))
		_, err := testDB.Pool.Exec(ctx, `UPDATE orders SET status = 2 WHERE id = $1`, string(order.ID))
		require.NoError(t, err)

		again, againTickets, againSettlement := issuanceOf(t, entry.Order, f.EventID)
		err = issuance.Issue(ctx, again, againTickets, againSettlement)

		assert.ErrorIs(t, err, apperr.ErrAlreadyExists)
		assert.Equal(t, entity.OrderStatusRefunded, orderStatus(t, order.ID))
	})

	t.Run("void order", func(t *testing.T) {
		// @spec components/entity/order/issue "Void order"
		f := seedStock(t, entity.TicketSaleMethodLottery, 100, 4)
		lost := insertLotteryEntry(t, f, f.SaleID, newUserID(t), 2, entity.LotteryEntryStateLost)
		order, issued, settlement := issuanceOf(t, lost.Order, f.EventID)

		err := issuance.Issue(ctx, order, issued, settlement)

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
		assert.Equal(t, entity.OrderStatusVoid, orderStatus(t, order.ID))
		assert.Empty(t, ticketsOfOrder(t, tickets, order.UserID, order.ID))
		_, err = settlements.GetByOrderID(ctx, order.ID)
		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})

	t.Run("unknown order", func(t *testing.T) {
		entry, f := wonEntry(t, 1)
		order, issued, settlement := issuanceOf(t, entry.Order, f.EventID)
		order.ID = entity.OrderID(entity.NewID())

		err := issuance.Issue(ctx, order, issued, settlement)

		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})

	t.Run("order without its captured payment", func(t *testing.T) {
		entry, f := wonEntry(t, 1)
		order, issued, settlement := issuanceOf(t, entry.Order, f.EventID)
		order.PayTime = nil

		err := issuance.Issue(ctx, order, issued, settlement)

		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
		assertUnissued(t, entry.Order)
	})
}

func TestIssuanceRepository_ListLotteryEntryIDsAwaitingIssuance(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	cleanDatabase(t)
	ctx := context.Background()
	issuance := rdb.NewIssuanceRepository(testDB)

	// awaiting lists the work-list now.
	awaiting := func(t *testing.T) []entity.LotteryEntryID {
		t.Helper()
		ids, err := issuance.ListLotteryEntryIDsAwaitingIssuance(ctx)
		require.NoError(t, err)
		return ids
	}

	t.Run("empty", func(t *testing.T) {
		assert.Empty(t, awaiting(t))
	})

	t.Run("won without order", func(t *testing.T) {
		// @spec components/entity/order/list-lottery-entry-ids-awaiting-issuance "Won without order"
		won, _ := wonEntry(t, 2)

		assert.Contains(t, awaiting(t), won.ID)
	})

	t.Run("already issued", func(t *testing.T) {
		// @spec components/entity/order/list-lottery-entry-ids-awaiting-issuance "Already issued"
		won, f := wonEntry(t, 1)
		order, issued, settlement := issuanceOf(t, won.Order, f.EventID)
		require.NoError(t, issuance.Issue(ctx, order, issued, settlement))

		assert.NotContains(t, awaiting(t), won.ID)
	})

	t.Run("not won", func(t *testing.T) {
		// @spec components/entity/order/list-lottery-entry-ids-awaiting-issuance "Not won"
		f := seedStock(t, entity.TicketSaleMethodLottery, 100, 4)
		var ids []entity.LotteryEntryID
		for _, state := range []entity.LotteryEntryState{entity.LotteryEntryStateEntered, entity.LotteryEntryStateLost} {
			ids = append(ids, insertLotteryEntry(t, f, f.SaleID, newUserID(t), 1, state).ID)
		}

		got := awaiting(t)
		for _, id := range ids {
			assert.NotContains(t, got, id)
		}
	})
}
