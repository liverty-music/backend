package entity_test

import (
	"testing"
	"time"

	"github.com/pannpers/go-apperr/apperr"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOrder_ValidateSource(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		order   *entity.Order
		wantErr bool
	}{
		{
			// @spec components/entity/order "Order from a checkout"
			name:  "order from a checkout",
			order: &entity.Order{CheckoutID: "checkout-1"},
		},
		{
			// @spec components/entity/order "Order from a won lottery entry"
			name:  "order from a won lottery entry",
			order: &entity.Order{LotteryEntryID: "entry-1"},
		},
		{
			// @spec components/entity/order "Order from a resale entry"
			name:  "order from a resale entry",
			order: &entity.Order{ResaleEntryID: "resale-entry-1"},
		},
		{
			// @spec components/entity/order "Two sources"
			name:    "two sources",
			order:   &entity.Order{LotteryEntryID: "entry-1", ResaleEntryID: "resale-entry-1"},
			wantErr: true,
		},
		{
			name:    "a checkout and a lottery entry",
			order:   &entity.Order{LotteryEntryID: "entry-1", CheckoutID: "checkout-1"},
			wantErr: true,
		},
		{
			name:    "no source",
			order:   &entity.Order{},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := tt.order.ValidateSource()

			if tt.wantErr {
				assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
				return
			}
			assert.NoError(t, err)
		})
	}
}

func TestNewPendingOrder(t *testing.T) {
	t.Parallel()

	// @spec components/entity/order "New order"
	stock := entity.NewTicketStock("event-1", "seat-s", 150)
	stock.SeatType = &entity.SeatType{ID: "seat-s", Name: "S席", Price: 8000}

	order := entity.NewPendingOrder("user-1", stock, 2, nil, entity.PaymentProviderStripe)

	assert.Equal(t, entity.OrderStatusPending, order.Status)
	assert.Equal(t, int64(16000), order.Amount)
	assert.Equal(t, stock.ID, order.TicketStockID)
	assert.Empty(t, order.Payment.ProviderPaymentID)
	assert.Nil(t, order.PayTime)
	assert.Nil(t, order.Refund)
}

func TestValidateTicketCount(t *testing.T) {
	t.Parallel()

	limit := 4
	event := &entity.Event{PerAccountLimit: &limit}
	tests := []struct {
		name  string
		count int
		valid bool
	}{
		// @spec components/entity/order "Count within the limit"
		{name: "2 of a limit of 4", count: 2, valid: true},
		// @spec components/entity/order "Zero tickets"
		{name: "0 tickets", count: 0},
		// @spec components/entity/order "Count over the limit"
		{name: "5 of a limit of 4", count: 5},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := entity.ValidateTicketCount(tt.count, event)
			if tt.valid {
				assert.NoError(t, err)
			} else {
				assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
			}
		})
	}
}

func TestOrder_CanBePaid(t *testing.T) {
	t.Parallel()

	paid := time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)

	t.Run("paid order", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/order "Paid order"
		order := &entity.Order{CardAuthorizationID: "pi_1", Payment: entity.Payment{ProviderPaymentID: "pi_1"}, PayTime: &paid}
		assert.True(t, order.CanBePaid())
	})

	t.Run("paid without a payment", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/order "Paid without a payment"
		order := &entity.Order{CardAuthorizationID: "pi_1", PayTime: &paid}
		assert.False(t, order.CanBePaid())
	})
}

func TestOrder_CanBecomeVoid(t *testing.T) {
	t.Parallel()

	t.Run("pending order", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/order "Pending order"
		assert.True(t, (&entity.Order{Status: entity.OrderStatusPending}).CanBecomeVoid())
	})

	t.Run("paid order", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/order "Paid order"
		assert.False(t, (&entity.Order{Status: entity.OrderStatusPaid}).CanBecomeVoid())
	})
}

var orderStatuses = []entity.OrderStatus{entity.OrderStatusPending, entity.OrderStatusPaid, entity.OrderStatusRefunded, entity.OrderStatusVoid}

func TestOrder_IsPaid(t *testing.T) {
	t.Parallel()
	want := map[entity.OrderStatus]bool{
		entity.OrderStatusPending: false, entity.OrderStatusPaid: true, entity.OrderStatusRefunded: true, entity.OrderStatusVoid: false,
	}
	for _, status := range orderStatuses {
		assert.Equal(t, want[status], (&entity.Order{Status: status}).IsPaid(), status.String())
	}
}

func TestOrder_IssuanceOutcome(t *testing.T) {
	t.Parallel()
	tests := []struct {
		status  entity.OrderStatus
		wantErr error
	}{
		{entity.OrderStatusPending, nil},
		{entity.OrderStatusPaid, apperr.ErrAlreadyExists},
		{entity.OrderStatusRefunded, apperr.ErrAlreadyExists},
		{entity.OrderStatusVoid, apperr.ErrFailedPrecondition},
	}
	for _, tt := range tests {
		t.Run(tt.status.String(), func(t *testing.T) {
			t.Parallel()
			err := (&entity.Order{Status: tt.status}).IssuanceOutcome()
			if tt.wantErr == nil {
				assert.NoError(t, err)
				return
			}
			assert.ErrorIs(t, err, tt.wantErr)
		})
	}
}

func TestOrder_CheckChargeable(t *testing.T) {
	t.Parallel()
	for _, status := range orderStatuses {
		err := (&entity.Order{Status: status}).CheckChargeable()
		if status == entity.OrderStatusPending {
			assert.NoError(t, err)
			continue
		}
		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition, status.String())
	}
}

func TestOrder_MarkPaid(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	o := &entity.Order{ID: "order-1", Status: entity.OrderStatusPending}

	o.MarkPaid(&entity.CapturedPayment{Provider: entity.PaymentProviderStripe, ProviderPaymentID: "pi_1", CardBrand: "visa", CardLast4: "4242"}, now)

	assert.Equal(t, entity.OrderStatusPaid, o.Status)
	require.NotNil(t, o.PayTime)
	assert.Equal(t, now, *o.PayTime)
	assert.Equal(t, entity.Payment{Provider: entity.PaymentProviderStripe, ProviderPaymentID: "pi_1", CardBrand: "visa", CardLast4: "4242"}, o.Payment)
}

func TestOrder_CheckRefundable(t *testing.T) {
	t.Parallel()

	cancelledState, publishedState := entity.PublishStateCancelled, entity.PublishStatePublished
	cancelled := &entity.Event{PublishState: &cancelledState}
	published := &entity.Event{PublishState: &publishedState}
	stripe := entity.Payment{Provider: entity.PaymentProviderStripe, ProviderPaymentID: "pi_1"}
	tests := []struct {
		name    string
		status  entity.OrderStatus
		payment entity.Payment
		reason  entity.RefundReason
		event   *entity.Event
		wantErr bool
	}{
		{"paid, cancellation, cancelled event", entity.OrderStatusPaid, stripe, entity.RefundReasonCancellation, cancelled, false},
		{"paid, cancellation, published event", entity.OrderStatusPaid, stripe, entity.RefundReasonCancellation, published, true},
		{"paid, cancellation, no event", entity.OrderStatusPaid, stripe, entity.RefundReasonCancellation, nil, true},
		{"paid, dispute, whatever the event", entity.OrderStatusPaid, stripe, entity.RefundReasonDispute, nil, false},
		{"paid, dispute without a provider payment", entity.OrderStatusPaid, entity.Payment{}, entity.RefundReasonDispute, nil, false},
		{"paid, non-Stripe provider", entity.OrderStatusPaid, entity.Payment{Provider: entity.PaymentProviderKOMOJU, ProviderPaymentID: "pi_1"}, entity.RefundReasonCancellation, cancelled, true},
		{"paid, no provider payment ID", entity.OrderStatusPaid, entity.Payment{Provider: entity.PaymentProviderStripe}, entity.RefundReasonCancellation, cancelled, true},
		{"pending", entity.OrderStatusPending, stripe, entity.RefundReasonDispute, nil, true},
		{"void", entity.OrderStatusVoid, stripe, entity.RefundReasonDispute, nil, true},
		{"refunded", entity.OrderStatusRefunded, stripe, entity.RefundReasonDispute, nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := (&entity.Order{ID: "o", Status: tt.status, Payment: tt.payment}).CheckRefundable(tt.reason, tt.event)
			if tt.wantErr {
				assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
				return
			}
			assert.NoError(t, err)
		})
	}
}

func TestOrder_RefundableAmount(t *testing.T) {
	t.Parallel()

	order := &entity.Order{ID: "o", Amount: 12000, TicketCount: 3}
	ticket := func(orderID entity.OrderID, status entity.TicketStatus) *entity.Ticket {
		return &entity.Ticket{OrderID: orderID, Status: status}
	}
	tests := []struct {
		name    string
		tickets []*entity.Ticket
		want    int64
	}{
		{"none resold", []*entity.Ticket{ticket("o", entity.TicketStatusIssued), ticket("o", entity.TicketStatusIssued), ticket("o", entity.TicketStatusIssued)}, 12000},
		{"one resold", []*entity.Ticket{ticket("o", entity.TicketStatusIssued), ticket("o", entity.TicketStatusIssued), ticket("o", entity.TicketStatusVoided)}, 8000},
		{"every ticket resold", []*entity.Ticket{ticket("o", entity.TicketStatusVoided), ticket("o", entity.TicketStatusVoided), ticket("o", entity.TicketStatusVoided)}, 0},
		{"a ticket of another order is ignored", []*entity.Ticket{ticket("o", entity.TicketStatusIssued), ticket("other", entity.TicketStatusIssued)}, 12000},
		{"no tickets", nil, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, order.RefundableAmount(tt.tickets))
		})
	}
}

func TestOrder_UnitPriceAndRefundedTicketCount(t *testing.T) {
	t.Parallel()

	order := &entity.Order{Amount: 12000, TicketCount: 3}
	assert.Equal(t, int64(4000), order.UnitPrice())
	assert.Equal(t, int64(0), order.RefundedTicketCount(), "no refund")
	order.Refund = &entity.OrderRefund{Amount: 8000}
	assert.Equal(t, int64(2), order.RefundedTicketCount())
	order.Refund.Amount = 0
	assert.Equal(t, int64(0), order.RefundedTicketCount())
	assert.Equal(t, int64(12000), (&entity.Order{Amount: 12000}).UnitPrice(), "an Order without a count")
	free := &entity.Order{TicketCount: 2, Refund: &entity.OrderRefund{}}
	assert.Equal(t, int64(2), free.RefundedTicketCount(), "a free Order counts every ticket")
}

func TestOrder_RefundPlan(t *testing.T) {
	t.Parallel()

	plan := (&entity.Order{}).RefundPlan()

	assert.Equal(t, entity.Transition[entity.OrderStatus]{From: []entity.OrderStatus{entity.OrderStatusPaid}, To: entity.OrderStatusRefunded}, plan.Order)
	assert.Equal(t, entity.Transition[entity.TicketStatus]{From: []entity.TicketStatus{entity.TicketStatusIssued}, To: entity.TicketStatusVoided}, plan.Tickets, "a resold ticket stays")
	assert.Equal(t, entity.Transition[entity.ResaleListingStatus]{From: []entity.ResaleListingStatus{entity.ResaleListingStatusListed}, To: entity.ResaleListingStatusCancelled}, plan.Listings, "a sold listing stays")
}

func TestOrder_Source(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		order *entity.Order
		want  entity.OrderSource
	}{
		{"lottery entry", &entity.Order{LotteryEntryID: "le-1"}, entity.OrderSourceLotteryEntry},
		{"checkout", &entity.Order{CheckoutID: "co-1"}, entity.OrderSourceCheckout},
		{"resale entry", &entity.Order{ResaleEntryID: "re-1"}, entity.OrderSourceResaleEntry},
		{"none", &entity.Order{}, entity.OrderSourceUnspecified},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, tt.order.Source())
		})
	}

	// The announcement of a paid Order names the same source from its ids.
	assert.Equal(t, entity.OrderSourceResaleEntry, entity.OrderPaidData{ResaleEntryID: "re-1"}.Source())
	assert.Equal(t, "resale_entry", entity.OrderSourceResaleEntry.String())
	assert.Equal(t, "lottery_entry", entity.OrderSourceLotteryEntry.String())
	assert.Equal(t, "checkout", entity.OrderSourceCheckout.String())
}

func TestOrder_TicketCountPredicates(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	co := func(status entity.CheckoutStatus, expire time.Time) entity.OrderSourceState {
		return entity.OrderSourceState{Checkout: &entity.Checkout{Status: status, HoldExpireTime: expire}}
	}
	le := func(state entity.LotteryEntryState) entity.OrderSourceState {
		return entity.OrderSourceState{LotteryEntry: &entity.LotteryEntry{State: state}}
	}
	re := func(state entity.ResaleEntryState) entity.OrderSourceState {
		return entity.OrderSourceState{ResaleEntry: &entity.ResaleEntry{State: state}}
	}
	holding, lapsed := now.Add(time.Minute), now.Add(-time.Minute)

	tests := []struct {
		name                string
		status              entity.OrderStatus
		src                 entity.OrderSourceState
		limit, sold, heldAt bool
	}{
		{"paid checkout", entity.OrderStatusPaid, co(entity.CheckoutStatusCompleted, lapsed), true, true, false},
		{"paid lottery entry", entity.OrderStatusPaid, le(entity.LotteryEntryStateWon), true, true, false},
		{"paid resale entry", entity.OrderStatusPaid, re(entity.ResaleEntryStateWon), true, false, false},
		{"refunded checkout", entity.OrderStatusRefunded, co(entity.CheckoutStatusCompleted, lapsed), false, true, false},
		{"pending, holding checkout", entity.OrderStatusPending, co(entity.CheckoutStatusHeld, holding), true, false, true},
		{"pending, lapsed checkout", entity.OrderStatusPending, co(entity.CheckoutStatusHeld, lapsed), false, false, false},
		{"pending, committed checkout", entity.OrderStatusPending, co(entity.CheckoutStatusCommitted, lapsed), true, true, false},
		{"pending, entered lottery entry", entity.OrderStatusPending, le(entity.LotteryEntryStateEntered), true, false, false},
		{"pending, won lottery entry", entity.OrderStatusPending, le(entity.LotteryEntryStateWon), true, true, false},
		{"pending, lost lottery entry", entity.OrderStatusPending, le(entity.LotteryEntryStateLost), false, false, false},
		{"pending, entered resale entry", entity.OrderStatusPending, re(entity.ResaleEntryStateEntered), true, false, false},
		{"pending, won resale entry", entity.OrderStatusPending, re(entity.ResaleEntryStateWon), true, false, false},
		{"pending, lapsed resale entry", entity.OrderStatusPending, re(entity.ResaleEntryStateLapsed), false, false, false},
		{"void", entity.OrderStatusVoid, le(entity.LotteryEntryStateLost), false, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			o := &entity.Order{Status: tt.status}
			assert.Equal(t, tt.limit, o.CountsTowardLimitAt(now, tt.src), "counts toward the limit")
			assert.Equal(t, tt.sold, o.IsSold(tt.src), "sold")
			assert.Equal(t, tt.heldAt, o.IsHeldAt(now, tt.src), "held")
		})
	}
}
