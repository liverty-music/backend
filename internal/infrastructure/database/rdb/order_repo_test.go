package rdb_test

import (
	"context"
	"testing"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"uuid"
)

func TestOrderRepository_ListByBuyer(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	ctx := context.Background()
	repo := rdb.NewOrderRepository(testDB)

	t.Run("returns the buyer's paid and refunded orders", func(t *testing.T) {
		// @spec components/entity/order/list-by-buyer "Paid and refunded orders"
		o := seedDeletableOrganizer(t, entity.OrganizerStatusActive)
		paid := seedPurchase(t, o, entity.OrderStatusPaid, entity.SettlementStatusHeld, false)
		refunded := seedPurchase(t, o, entity.OrderStatusRefunded, entity.SettlementStatusReversed, false)
		buyerID := entity.UserID(seedUser(t, "list-buyer", uuid.NewV7().String()+"@example.test", uuid.NewV7().String()))
		_, err := testDB.Pool.Exec(ctx, `UPDATE orders SET buyer_id = $1 WHERE id IN ($2, $3)`,
			string(buyerID), paid.orderID, refunded.orderID)
		require.NoError(t, err)
		other := seedPurchase(t, o, entity.OrderStatusPaid, entity.SettlementStatusHeld, false)

		got, err := repo.ListByBuyer(ctx, buyerID)

		require.NoError(t, err)
		statuses := map[entity.OrderID]entity.OrderStatus{}
		for _, order := range got {
			assert.Equal(t, buyerID, order.BuyerID)
			statuses[order.ID] = order.Status
		}
		assert.Equal(t, map[entity.OrderID]entity.OrderStatus{
			entity.OrderID(paid.orderID):     entity.OrderStatusPaid,
			entity.OrderID(refunded.orderID): entity.OrderStatusRefunded,
		}, statuses)
		assert.NotContains(t, statuses, entity.OrderID(other.orderID))
	})

	t.Run("returns an empty list when the user bought nothing", func(t *testing.T) {
		// @spec components/entity/order/list-by-buyer "No orders"
		got, err := repo.ListByBuyer(ctx, entity.UserID(entity.NewID()))

		require.NoError(t, err)
		assert.Empty(t, got)
		assert.NotNil(t, got)
	})
}
