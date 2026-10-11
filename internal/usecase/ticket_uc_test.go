package usecase_test

import (
	"context"
	"testing"

	"github.com/liverty-music/backend/internal/entity"
	entitymocks "github.com/liverty-music/backend/internal/entity/mocks"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newOrderReadUC builds a TicketUseCase for the fan's Order and Ticket reads.
func newOrderReadUC(t *testing.T, orders entity.OrderRepository, tickets entity.TicketRepository) usecase.TicketUseCase {
	t.Helper()
	return usecase.NewTicketUseCase(orders, tickets, nil, nil, nil, nil, nil, newTestLogger(t))
}

func TestTicketUseCase_GetOrder(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	own := &entity.Order{ID: "order-1", UserID: "fan-1", Status: entity.OrderStatusPaid}

	type args struct {
		userID  entity.UserID
		orderID entity.OrderID
	}
	tests := []struct {
		name    string
		args    args
		setup   func(orders *entitymocks.MockOrderRepository)
		want    *entity.Order
		wantErr error
	}{
		{
			// @spec components/usecase/order/get-order "Own order"
			name: "own order",
			args: args{userID: "fan-1", orderID: "order-1"},
			setup: func(orders *entitymocks.MockOrderRepository) {
				orders.EXPECT().Get(ctx, entity.OrderID("order-1")).Return(own, nil)
			},
			want: own,
		},
		{
			// @spec components/usecase/order/get-order "Another buyer's order"
			name: "another account's order",
			args: args{userID: "fan-2", orderID: "order-1"},
			setup: func(orders *entitymocks.MockOrderRepository) {
				orders.EXPECT().Get(ctx, entity.OrderID("order-1")).Return(own, nil)
			},
			wantErr: apperr.ErrNotFound,
		},
		{
			// @spec components/usecase/order/get-order "Unknown order"
			name: "unknown order",
			args: args{userID: "fan-1", orderID: "order-x"},
			setup: func(orders *entitymocks.MockOrderRepository) {
				orders.EXPECT().Get(ctx, entity.OrderID("order-x")).Return(nil, apperr.ErrNotFound)
			},
			wantErr: apperr.ErrNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			orders := entitymocks.NewMockOrderRepository(t)
			tt.setup(orders)
			uc := newOrderReadUC(t, orders, entitymocks.NewMockTicketRepository(t))

			got, err := uc.GetOrder(ctx, tt.args.userID, tt.args.orderID)

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				assert.Nil(t, got)
				return
			}
			require.NoError(t, err)
			assert.Same(t, tt.want, got)
		})
	}
}

func TestTicketUseCase_GetMyTickets(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	issued := []*entity.Ticket{
		{ID: "ticket-1", UserID: "fan-1", Status: entity.TicketStatusIssued},
		{ID: "ticket-2", UserID: "fan-1", Status: entity.TicketStatusIssued},
	}

	tests := []struct {
		name string
		list []*entity.Ticket
		want []*entity.Ticket
	}{
		{
			// @spec components/usecase/ticket/get-my-tickets "Fan with tickets"
			name: "fan with tickets",
			list: issued,
			want: issued,
		},
		{
			// @spec components/usecase/ticket/get-my-tickets "Fan without tickets"
			name: "fan without tickets",
			list: []*entity.Ticket{},
			want: []*entity.Ticket{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tickets := entitymocks.NewMockTicketRepository(t)
			tickets.EXPECT().ListByUser(ctx, entity.UserID("fan-1")).Return(tt.list, nil)
			uc := newOrderReadUC(t, entitymocks.NewMockOrderRepository(t), tickets)

			got, err := uc.List(ctx, "fan-1")

			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}

	t.Run("listing fails", func(t *testing.T) {
		t.Parallel()
		tickets := entitymocks.NewMockTicketRepository(t)
		tickets.EXPECT().ListByUser(ctx, entity.UserID("fan-1")).Return(nil, apperr.ErrInternal)
		uc := newOrderReadUC(t, entitymocks.NewMockOrderRepository(t), tickets)

		_, err := uc.List(ctx, "fan-1")

		assert.ErrorIs(t, err, apperr.ErrInternal)
	})
}
