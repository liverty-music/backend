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
	"uuid"
)

func TestOrderRepository_ListByUser(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	ctx := context.Background()
	repo := rdb.NewOrderRepository(testDB)

	t.Run("returns the user's paid and refunded orders", func(t *testing.T) {
		// @spec components/entity/order/list-by-user "Paid and refunded orders"
		o := seedDeletableOrganizer(t, entity.OrganizerStatusActive)
		paid := seedPurchase(t, o, entity.OrderStatusPaid, entity.SettlementStatusHeld, false)
		refunded := seedPurchase(t, o, entity.OrderStatusRefunded, entity.SettlementStatusReversed, false)
		userID := entity.UserID(seedUser(t, "list-fan", uuid.NewV7().String()+"@example.test", uuid.NewV7().String()))
		_, err := testDB.Pool.Exec(ctx, `UPDATE orders SET user_id = $1 WHERE id IN ($2, $3)`,
			string(userID), paid.orderID, refunded.orderID)
		require.NoError(t, err)
		other := seedPurchase(t, o, entity.OrderStatusPaid, entity.SettlementStatusHeld, false)

		got, err := repo.ListByUser(ctx, userID)

		require.NoError(t, err)
		statuses := map[entity.OrderID]entity.OrderStatus{}
		for _, order := range got {
			assert.Equal(t, userID, order.UserID)
			statuses[order.ID] = order.Status
		}
		assert.Equal(t, map[entity.OrderID]entity.OrderStatus{
			entity.OrderID(paid.orderID):     entity.OrderStatusPaid,
			entity.OrderID(refunded.orderID): entity.OrderStatusRefunded,
		}, statuses)
		assert.NotContains(t, statuses, entity.OrderID(other.orderID))
	})

	t.Run("returns an empty list when the user bought nothing", func(t *testing.T) {
		// @spec components/entity/order/list-by-user "No orders"
		got, err := repo.ListByUser(ctx, entity.UserID(entity.NewID()))

		require.NoError(t, err)
		assert.Empty(t, got)
		assert.NotNil(t, got)
	})
}

func TestOrderRepository_GetByLotteryEntryID(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	ctx := context.Background()
	repo := rdb.NewOrderRepository(testDB)

	t.Run("issued entry", func(t *testing.T) {
		// @spec components/entity/order/get-by-lottery-entry-id "Issued entry"
		o := seedDeletableOrganizer(t, entity.OrganizerStatusActive)
		p := seedPurchase(t, o, entity.OrderStatusPaid, entity.SettlementStatusHeld, false)
		var entryID string
		require.NoError(t, testDB.Pool.QueryRow(ctx, `SELECT lottery_entry_id FROM orders WHERE id = $1`, p.orderID).Scan(&entryID))

		got, err := repo.GetByLotteryEntryID(ctx, entity.LotteryEntryID(entryID))

		require.NoError(t, err)
		assert.Equal(t, entity.OrderID(p.orderID), got.ID)
		assert.Equal(t, entity.LotteryEntryID(entryID), got.LotteryEntryID)
		assert.Empty(t, got.CheckoutID)
	})

	t.Run("not yet issued", func(t *testing.T) {
		// @spec components/entity/order/get-by-lottery-entry-id "Not yet issued"
		_, err := repo.GetByLotteryEntryID(ctx, entity.LotteryEntryID(entity.NewID()))

		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})
}

func TestOrderRepository_GetByCheckoutID(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	ctx := context.Background()
	repo := rdb.NewOrderRepository(testDB)
	checkouts := rdb.NewCheckoutRepository(testDB)
	issuance := rdb.NewIssuanceRepository(testDB)
	start := saleWindowStart.Add(time.Hour)

	t.Run("paid checkout", func(t *testing.T) {
		// @spec components/entity/order/get-by-checkout-id "Paid checkout"
		c := completedCheckout(t, checkouts, issuance, start)

		got, err := repo.GetByCheckoutID(ctx, c.ID)

		require.NoError(t, err)
		assert.Equal(t, c.ID, got.CheckoutID)
		assert.Equal(t, c.UserID, got.UserID)
		assert.Empty(t, got.LotteryEntryID)
	})

	t.Run("checkout not paid", func(t *testing.T) {
		// @spec components/entity/order/get-by-checkout-id "Checkout not paid"
		c := heldCheckout(t, checkouts, 1, start)

		_, err := repo.GetByCheckoutID(ctx, c.ID)

		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})
}

// seedCheckoutOrder seeds a Completed checkout of the ticket type, a resale
// checkout when resale is set, and an Order of it in status paid at payAt.
func seedCheckoutOrder(t *testing.T, typeID entity.TicketTypeID, status entity.OrderStatus, payAt time.Time, resale bool) entity.OrderID {
	t.Helper()
	ctx := context.Background()
	userID := seedFanUser(t)
	checkoutID, orderID := entity.NewID(), entity.NewID()
	_, err := testDB.Pool.Exec(ctx, `INSERT INTO checkouts (id, ticket_type_id, user_id, ticket_count, amount, card_authorization_id, status, hold_expire_at, commit_at, capture_at, resale)
		VALUES ($1, $2, $3, 1, 3000, $4, 3, $5, $5, $5, $6)`,
		checkoutID, string(typeID), userID, "pi_checkout_"+checkoutID, payAt, resale)
	require.NoError(t, err)
	_, err = testDB.Pool.Exec(ctx, `INSERT INTO orders (id, user_id, checkout_id, provider, provider_payment_id, status, amount, currency, pay_at)
		VALUES ($1, $2, $3, 1, $4, $5, 3000, 'JPY', $6)`,
		orderID, userID, checkoutID, "pi_"+orderID, int16(status), payAt)
	require.NoError(t, err)
	return entity.OrderID(orderID)
}

func TestOrderRepository_ListByEvent(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	ctx := context.Background()
	repo := rdb.NewOrderRepository(testDB)

	t.Run("returns the event's paid, refunded and resale orders, oldest first", func(t *testing.T) {
		// @spec components/entity/order/list-by-event "Paid, refunded and resale orders"
		o := seedDeletableOrganizer(t, entity.OrganizerStatusActive)
		firstCome := seedTicketTypeForEvent(t, o.eventID, entity.TicketSaleMethodFirstCome, 10, 4)
		paid := seedCheckoutOrder(t, firstCome.ID, entity.OrderStatusPaid, time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC), false)
		refunded := seedPurchase(t, o, entity.OrderStatusRefunded, entity.SettlementStatusReversed, false) // paid 2026-10-09
		resale := seedCheckoutOrder(t, o.typeID, entity.OrderStatusPaid, time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC), true)

		got, err := repo.ListByEvent(ctx, o.eventID)

		require.NoError(t, err)
		ids := make([]entity.OrderID, 0, len(got))
		for _, order := range got {
			ids = append(ids, order.ID)
		}
		assert.Equal(t, []entity.OrderID{paid, entity.OrderID(refunded.orderID), resale}, ids)
		assert.Equal(t, entity.OrderStatusRefunded, got[1].Status)
	})

	t.Run("returns an empty list when only another event of the series has orders", func(t *testing.T) {
		// @spec components/entity/order/list-by-event "Orders of another event"
		o := seedDeletableOrganizer(t, entity.OrganizerStatusActive)
		seedPurchase(t, o, entity.OrderStatusPaid, entity.SettlementStatusHeld, false)
		otherEventID := uuid.NewV7().String()
		_, err := testDB.Pool.Exec(ctx, `INSERT INTO events (id, series_id, venue_id, local_event_date, publish_state)
			SELECT $1, series_id, venue_id, '2026-11-21', 'PUBLISHED' FROM events WHERE id = $2`, otherEventID, o.eventID)
		require.NoError(t, err)

		got, err := repo.ListByEvent(ctx, otherEventID)

		require.NoError(t, err)
		assert.Empty(t, got)
		assert.NotNil(t, got)
	})

	t.Run("fails with InvalidArgument when no event is given", func(t *testing.T) {
		// @spec components/entity/order/list-by-event "No event given"
		_, err := repo.ListByEvent(ctx, "")

		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
	})
}

func TestOrderRepository_GetEventID(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	ctx := context.Background()
	repo := rdb.NewOrderRepository(testDB)

	t.Run("returns the event of a checkout's order", func(t *testing.T) {
		// @spec components/entity/order/get-event-id "Order from a checkout"
		o := seedDeletableOrganizer(t, entity.OrganizerStatusActive)
		tt := seedTicketTypeForEvent(t, o.eventID, entity.TicketSaleMethodFirstCome, 10, 4)
		orderID := seedCheckoutOrder(t, tt.ID, entity.OrderStatusPaid, time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC), false)

		got, err := repo.GetEventID(ctx, orderID)

		require.NoError(t, err)
		assert.Equal(t, o.eventID, got)
	})

	t.Run("returns the event of a won lottery entry's order", func(t *testing.T) {
		// @spec components/entity/order/get-event-id "Order from a won lottery entry"
		o := seedDeletableOrganizer(t, entity.OrganizerStatusActive)
		p := seedPurchase(t, o, entity.OrderStatusPaid, entity.SettlementStatusHeld, false)

		got, err := repo.GetEventID(ctx, entity.OrderID(p.orderID))

		require.NoError(t, err)
		assert.Equal(t, o.eventID, got)
	})

	t.Run("fails with NotFound for an unknown order", func(t *testing.T) {
		// @spec components/entity/order/get-event-id "Unknown order"
		_, err := repo.GetEventID(ctx, entity.OrderID(entity.NewID()))

		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})
}
