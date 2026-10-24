package usecase_test

import (
	"context"
	"testing"

	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/liverty-music/backend/internal/entity"
	entitymocks "github.com/liverty-music/backend/internal/entity/mocks"
	"github.com/liverty-music/backend/internal/usecase"
)

func TestOrderUseCase_GetWithSettlement(t *testing.T) {
	t.Parallel()

	const orderID = entity.OrderID("019a0000-0000-7000-8000-0000000000a1")
	order := &entity.Order{ID: orderID, Status: entity.OrderStatusPaid, Amount: 6000, Currency: "JPY"}
	held := &entity.Settlement{
		ID: "019a0000-0000-7000-8000-0000000000b1", OrderID: orderID, Status: entity.SettlementStatusHeld,
		Splits: []entity.SettlementSplit{{PayeeOrganizerID: "org-1", Amount: 5520}},
	}
	dbDown := apperr.New(codes.Internal, "db down")

	type want struct {
		order      *entity.Order
		settlement *entity.Settlement
	}
	tests := []struct {
		name    string
		setup   func(orders *entitymocks.MockOrderRepository, settlements *entitymocks.MockSettlementRepository)
		want    want
		wantErr error
	}{
		{
			// @spec components/usecase/order/get-with-settlement "Order with a held settlement"
			name: "return the order and its held settlement with its splits",
			setup: func(orders *entitymocks.MockOrderRepository, settlements *entitymocks.MockSettlementRepository) {
				orders.EXPECT().Get(mock.Anything, orderID).Return(order, nil)
				settlements.EXPECT().GetByOrderID(mock.Anything, orderID).Return(held, nil)
			},
			want: want{order: order, settlement: held},
		},
		{
			// @spec components/usecase/order/get-with-settlement "Order with no settlement yet"
			name: "return the order and no settlement when it has none",
			setup: func(orders *entitymocks.MockOrderRepository, settlements *entitymocks.MockSettlementRepository) {
				orders.EXPECT().Get(mock.Anything, orderID).Return(order, nil)
				settlements.EXPECT().GetByOrderID(mock.Anything, orderID).Return(nil, apperr.New(codes.NotFound, "no settlement"))
			},
			want: want{order: order},
		},
		{
			// @spec components/usecase/order/get-with-settlement "Order not found"
			name: "return NotFound when no order has the id",
			setup: func(orders *entitymocks.MockOrderRepository, _ *entitymocks.MockSettlementRepository) {
				orders.EXPECT().Get(mock.Anything, orderID).Return(nil, apperr.New(codes.NotFound, "no order"))
			},
			wantErr: apperr.ErrNotFound,
		},
		{
			name: "return a settlement read failure unchanged",
			setup: func(orders *entitymocks.MockOrderRepository, settlements *entitymocks.MockSettlementRepository) {
				orders.EXPECT().Get(mock.Anything, orderID).Return(order, nil)
				settlements.EXPECT().GetByOrderID(mock.Anything, orderID).Return(nil, dbDown)
			},
			wantErr: apperr.ErrInternal,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			orders := entitymocks.NewMockOrderRepository(t)
			settlements := entitymocks.NewMockSettlementRepository(t)
			tt.setup(orders, settlements)

			gotOrder, gotSettlement, err := usecase.NewOrderUseCase(usecase.OrderDeps{OrderRepo: orders, SettlementRepo: settlements}).GetWithSettlement(context.Background(), orderID)

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				assert.Nil(t, gotOrder)
				return
			}
			assert.NoError(t, err)
			assert.Equal(t, tt.want.order, gotOrder)
			assert.Equal(t, tt.want.settlement, gotSettlement)
		})
	}
}

func TestOrderUseCase_ListByEvent(t *testing.T) {
	t.Parallel()
	const eventID = "event-cancelled"

	t.Run("return the event's paid and refunded orders", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/order/list-by-event "Cancelled event with refunded and paid orders"
		want := []*entity.Order{
			{ID: "order-paid", Status: entity.OrderStatusPaid},
			{ID: "order-refunded", Status: entity.OrderStatusRefunded},
		}
		orders := entitymocks.NewMockOrderRepository(t)
		orders.EXPECT().ListByEvent(mock.Anything, eventID).Return(want, nil)

		got, err := usecase.NewOrderUseCase(usecase.OrderDeps{OrderRepo: orders}).ListByEvent(context.Background(), eventID)

		require.NoError(t, err)
		assert.Equal(t, want, got)
	})

	t.Run("return an empty list for an unknown event", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/order/list-by-event "Unknown event"
		orders := entitymocks.NewMockOrderRepository(t)
		orders.EXPECT().ListByEvent(mock.Anything, "event-unknown").Return([]*entity.Order{}, nil)

		got, err := usecase.NewOrderUseCase(usecase.OrderDeps{OrderRepo: orders}).ListByEvent(context.Background(), "event-unknown")

		require.NoError(t, err)
		assert.Empty(t, got)
	})
}
