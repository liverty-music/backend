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
