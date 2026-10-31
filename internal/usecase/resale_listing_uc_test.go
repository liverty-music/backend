package usecase_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/liverty-music/backend/internal/entity"
	entitymocks "github.com/liverty-music/backend/internal/entity/mocks"
	"github.com/liverty-music/backend/internal/usecase"
	ucmocks "github.com/liverty-music/backend/internal/usecase/mocks"
)

// The resale fixtures are about event-1, starting 2026-11-20 19:00 JST, so
// its resale deadline is 16:00 that day.
var (
	resaleEventStart = time.Date(2026, 11, 20, 19, 0, 0, 0, tsJST)
	resaleDeadline   = time.Date(2026, 11, 20, 16, 0, 0, 0, tsJST)
	resaleWeekBefore = time.Date(2026, 11, 13, 12, 0, 0, 0, tsJST)
)

// resaleFixture holds the mocks of ResaleListingUseCase under test.
type resaleFixture struct {
	listings *entitymocks.MockResaleListingRepository
	tickets  *entitymocks.MockTicketRepository
	orders   *entitymocks.MockOrderRepository
	stocks   *entitymocks.MockTicketStockRepository
	state    *ucmocks.MockEventPublishStatePort
	events   *entitymocks.MockEventRepository
	uc       usecase.ResaleListingUseCase
}

func newResaleFixture(t *testing.T, now time.Time) *resaleFixture {
	t.Helper()
	f := &resaleFixture{
		listings: entitymocks.NewMockResaleListingRepository(t),
		tickets:  entitymocks.NewMockTicketRepository(t),
		orders:   entitymocks.NewMockOrderRepository(t),
		stocks:   entitymocks.NewMockTicketStockRepository(t),
		state:    ucmocks.NewMockEventPublishStatePort(t),
		events:   entitymocks.NewMockEventRepository(t),
	}
	f.uc = usecase.NewResaleListingUseCase(usecase.ResaleListingDeps{
		Listings: f.listings, Tickets: f.tickets, Orders: f.orders, Stocks: f.stocks,
		EventState: f.state, Events: f.events,
		Clock: fixedClock(now), Logger: newTestLogger(t),
	})
	return f
}

// expectEvent makes event-1 readable with its start time.
func (f *resaleFixture) expectEvent() {
	start := resaleEventStart
	f.events.EXPECT().Get(mock.Anything, "event-1").Return(&entity.Event{ID: "event-1", StartTime: &start}, nil)
}

// fanTicket returns fan-1's Issued ticket t-1 of order-1 for event-1.
func fanTicket() *entity.Ticket {
	return &entity.Ticket{ID: "t-1", OrderID: "order-1", UserID: "fan-1", EventID: "event-1", Status: entity.TicketStatusIssued}
}

// resaleStock returns the S席 stock-1 of event-1 priced 5000 yen, sold out
// when soldOut is true.
func resaleStock(soldOut bool) *entity.TicketStock {
	sold := 149
	if soldOut {
		sold = 150
	}
	return &entity.TicketStock{
		ID: "stock-1", EventID: "event-1", SeatTypeID: "seat-s", Quantity: 150, SoldCount: sold,
		SeatType: &entity.SeatType{ID: "seat-s", Name: "S席", Price: 5000},
	}
}

// expectTicketToListing sets up the reads Create makes for t-1 up to the
// published check, the Ticket's Order coming from source ("checkout" or
// "lottery").
func (f *resaleFixture) expectTicketToListing(ticket *entity.Ticket, soldOut bool, source string) {
	f.tickets.EXPECT().ListByUser(mock.Anything, entity.UserID("fan-1")).Return([]*entity.Ticket{ticket}, nil)
	order := &entity.Order{ID: "order-1", TicketStockID: "stock-1", Status: entity.OrderStatusPaid}
	if source == "lottery" {
		order.LotteryEntryID = "entry-1"
	} else {
		order.CheckoutID = "checkout-1"
	}
	f.orders.EXPECT().Get(mock.Anything, entity.OrderID("order-1")).Return(order, nil)
	f.stocks.EXPECT().Get(mock.Anything, entity.TicketStockID("stock-1"), mock.Anything).Return(resaleStock(soldOut), nil)
}

func TestResaleListingUseCase_Create(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("fan lists a ticket after the stock sold out", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/resale-listing/create "Fan lists a ticket after the stock sold out"
		f := newResaleFixture(t, resaleWeekBefore)
		f.expectTicketToListing(fanTicket(), true, "checkout")
		f.state.EXPECT().IsEventPublished(mock.Anything, "event-1").Return(true, nil)
		f.expectEvent()
		f.listings.EXPECT().Create(mock.Anything, mock.MatchedBy(func(l *entity.ResaleListing) bool {
			return l.TicketID == "t-1" && l.OrderID == "order-1" && l.UserID == "fan-1" &&
				l.TicketStockID == "stock-1" && l.Price == 5000 && l.CreateTime.Equal(resaleWeekBefore)
		})).RunAndReturn(func(_ context.Context, l *entity.ResaleListing) (*entity.ResaleListing, error) { return l, nil })

		got, err := f.uc.Create(ctx, "fan-1", "t-1")

		require.NoError(t, err)
		assert.Equal(t, entity.ResaleListingStatusListed, got.Status)
		assert.Equal(t, int64(5000), got.Listing.Price)
		assert.True(t, got.Deadline.Equal(resaleDeadline))
	})

	t.Run("stock not sold out", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/resale-listing/create "Stock not sold out"
		f := newResaleFixture(t, resaleWeekBefore)
		f.expectTicketToListing(fanTicket(), false, "checkout")
		f.state.EXPECT().IsEventPublished(mock.Anything, "event-1").Return(true, nil)
		f.expectEvent()

		_, err := f.uc.Create(ctx, "fan-1", "t-1")

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
	})

	t.Run("admission has opened", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/resale-listing/create "Admission has opened"
		f := newResaleFixture(t, resaleDeadline.Add(time.Minute))
		f.expectTicketToListing(fanTicket(), true, "checkout")
		f.state.EXPECT().IsEventPublished(mock.Anything, "event-1").Return(true, nil)
		f.expectEvent()

		_, err := f.uc.Create(ctx, "fan-1", "t-1")

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
	})

	t.Run("ticket already used", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/resale-listing/create "Ticket already used"
		f := newResaleFixture(t, resaleWeekBefore)
		admitted := fanTicket()
		at := resaleWeekBefore.Add(-time.Hour)
		admitted.AdmitTime = &at
		f.tickets.EXPECT().ListByUser(mock.Anything, entity.UserID("fan-1")).Return([]*entity.Ticket{admitted}, nil)

		_, err := f.uc.Create(ctx, "fan-1", "t-1")

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
	})

	t.Run("lottery ticket", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/resale-listing/create "Lottery ticket"
		f := newResaleFixture(t, resaleWeekBefore)
		f.expectTicketToListing(fanTicket(), true, "lottery")
		f.state.EXPECT().IsEventPublished(mock.Anything, "event-1").Return(true, nil)
		f.expectEvent()
		f.listings.EXPECT().Create(mock.Anything, mock.Anything).
			RunAndReturn(func(_ context.Context, l *entity.ResaleListing) (*entity.ResaleListing, error) { return l, nil })

		got, err := f.uc.Create(ctx, "fan-1", "t-1")

		require.NoError(t, err)
		assert.Equal(t, entity.ResaleListingStatusListed, got.Status)
	})

	t.Run("concert cancelled", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/resale-listing/create "Concert cancelled"
		f := newResaleFixture(t, resaleWeekBefore)
		f.expectTicketToListing(fanTicket(), true, "checkout")
		f.state.EXPECT().IsEventPublished(mock.Anything, "event-1").Return(false, nil)

		_, err := f.uc.Create(ctx, "fan-1", "t-1")

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
	})

	t.Run("someone else's ticket", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/resale-listing/create "Someone else's ticket"
		f := newResaleFixture(t, resaleWeekBefore)
		f.tickets.EXPECT().ListByUser(mock.Anything, entity.UserID("fan-1")).Return([]*entity.Ticket{}, nil)

		_, err := f.uc.Create(ctx, "fan-1", "t-other")

		assert.ErrorIs(t, err, apperr.ErrPermissionDenied)
	})

	t.Run("listed twice", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/resale-listing/create "Listed twice"
		f := newResaleFixture(t, resaleWeekBefore)
		f.expectTicketToListing(fanTicket(), true, "checkout")
		f.state.EXPECT().IsEventPublished(mock.Anything, "event-1").Return(true, nil)
		f.expectEvent()
		f.listings.EXPECT().Create(mock.Anything, mock.Anything).Return(nil, apperr.New(codes.AlreadyExists, "listed"))

		_, err := f.uc.Create(ctx, "fan-1", "t-1")

		assert.ErrorIs(t, err, apperr.ErrAlreadyExists)
	})
}

// fanListing returns fan-1's listing l-1 of t-1, priced 5000 yen, in status.
func fanListing(status entity.ResaleListingStatus) *entity.ResaleListing {
	return &entity.ResaleListing{
		ID: "l-1", TicketID: "t-1", OrderID: "order-1", UserID: "fan-1", EventID: "event-1",
		TicketStockID: "stock-1", Price: 5000, Status: status, CreateTime: resaleWeekBefore.Add(-24 * time.Hour),
	}
}

func TestResaleListingUseCase_Withdraw(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("fan can attend after all", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/resale-listing/withdraw "Fan can attend after all"
		f := newResaleFixture(t, resaleWeekBefore)
		f.listings.EXPECT().Get(mock.Anything, entity.ResaleListingID("l-1")).Return(fanListing(entity.ResaleListingStatusListed), nil).Once()
		f.listings.EXPECT().Withdraw(mock.Anything, entity.ResaleListingID("l-1"), resaleWeekBefore).Return(nil)
		f.listings.EXPECT().Get(mock.Anything, entity.ResaleListingID("l-1")).Return(fanListing(entity.ResaleListingStatusWithdrawn), nil).Once()
		f.expectEvent()

		got, err := f.uc.Withdraw(ctx, "fan-1", "l-1")

		require.NoError(t, err)
		assert.Equal(t, entity.ResaleListingStatusWithdrawn, got.Status)
	})

	t.Run("already sold", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/resale-listing/withdraw "Already sold"
		f := newResaleFixture(t, resaleWeekBefore)
		f.listings.EXPECT().Get(mock.Anything, entity.ResaleListingID("l-1")).Return(fanListing(entity.ResaleListingStatusSold), nil)
		f.listings.EXPECT().Withdraw(mock.Anything, entity.ResaleListingID("l-1"), resaleWeekBefore).
			Return(apperr.New(codes.FailedPrecondition, "sold"))

		_, err := f.uc.Withdraw(ctx, "fan-1", "l-1")

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
	})

	t.Run("someone else's listing", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/resale-listing/withdraw "Someone else's listing"
		f := newResaleFixture(t, resaleWeekBefore)
		f.listings.EXPECT().Get(mock.Anything, entity.ResaleListingID("l-1")).Return(fanListing(entity.ResaleListingStatusListed), nil)

		_, err := f.uc.Withdraw(ctx, "fan-2", "l-1")

		assert.ErrorIs(t, err, apperr.ErrPermissionDenied)
	})

	t.Run("unknown listing is not revealed", func(t *testing.T) {
		t.Parallel()
		f := newResaleFixture(t, resaleWeekBefore)
		f.listings.EXPECT().Get(mock.Anything, entity.ResaleListingID("l-9")).Return(nil, apperr.New(codes.NotFound, "none"))

		_, err := f.uc.Withdraw(ctx, "fan-1", "l-9")

		assert.ErrorIs(t, err, apperr.ErrPermissionDenied)
	})
}

func TestResaleListingUseCase_List(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("listing on sale", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/resale-listing/list "Listing on sale"
		f := newResaleFixture(t, resaleWeekBefore)
		f.listings.EXPECT().ListByUser(mock.Anything, entity.UserID("fan-1")).Return([]*entity.ResaleListing{fanListing(entity.ResaleListingStatusListed)}, nil)
		f.expectEvent()

		got, err := f.uc.List(ctx, "fan-1")

		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.Equal(t, entity.ResaleListingStatusListed, got[0].Status)
		assert.Equal(t, int64(500), got[0].Listing.Fee())
		assert.Equal(t, int64(4500), got[0].Listing.RefundAmount())
		assert.True(t, got[0].Deadline.Equal(resaleDeadline))
	})

	t.Run("listing that did not sell", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/resale-listing/list "Listing that did not sell"
		f := newResaleFixture(t, resaleDeadline.Add(time.Hour))
		f.listings.EXPECT().ListByUser(mock.Anything, entity.UserID("fan-1")).Return([]*entity.ResaleListing{fanListing(entity.ResaleListingStatusListed)}, nil)
		f.expectEvent()

		got, err := f.uc.List(ctx, "fan-1")

		require.NoError(t, err)
		assert.Equal(t, entity.ResaleListingStatusExpired, got[0].Status)
	})

	t.Run("sold and refunded", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/resale-listing/list "Sold and refunded"
		f := newResaleFixture(t, resaleWeekBefore)
		sold := fanListing(entity.ResaleListingStatusSold)
		soldAt := resaleWeekBefore.Add(-time.Hour)
		refundAt := soldAt.Add(time.Minute)
		sold.SoldTime, sold.RefundTime = &soldAt, &refundAt
		f.listings.EXPECT().ListByUser(mock.Anything, entity.UserID("fan-1")).Return([]*entity.ResaleListing{sold}, nil)
		f.expectEvent()

		got, err := f.uc.List(ctx, "fan-1")

		require.NoError(t, err)
		assert.Equal(t, entity.ResaleListingStatusSold, got[0].Status)
		assert.Equal(t, &soldAt, got[0].Listing.SoldTime)
		assert.Equal(t, &refundAt, got[0].Listing.RefundTime)
	})

	t.Run("no listings", func(t *testing.T) {
		t.Parallel()
		f := newResaleFixture(t, resaleWeekBefore)
		f.listings.EXPECT().ListByUser(mock.Anything, entity.UserID("fan-1")).Return([]*entity.ResaleListing{}, nil)

		got, err := f.uc.List(ctx, "fan-1")

		require.NoError(t, err)
		assert.Empty(t, got)
	})
}

// soldListing returns listing id of fan-1 priced 5000 yen, sold at soldAt.
func soldListing(id entity.ResaleListingID, soldAt time.Time) *entity.ResaleListing {
	l := fanListing(entity.ResaleListingStatusSold)
	l.ID = id
	l.SoldTime = &soldAt
	return l
}

// expectOrderCharge makes order-1 and its charge ch_1 readable.
func (f *resaleFixture) expectOrderCharge() {
	f.orders.EXPECT().Get(mock.Anything, entity.OrderID("order-1")).Return(&entity.Order{
		ID: "order-1", Payment: entity.Payment{ProviderPaymentID: "pi_1"},
	}, nil).Maybe()
	f.orders.EXPECT().GetProviderChargeID(mock.Anything, "pi_1").Return("ch_1", nil).Maybe()
}

func TestResaleListingUseCase_RefundSold(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	soldAt := time.Date(2026, 11, 13, 18, 20, 0, 0, tsJST)

	t.Run("ticket resold", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/resale-listing/refund-sold "Ticket resold"
		now := soldAt.Add(time.Minute)
		f := newResaleFixture(t, now)
		f.listings.EXPECT().ListSoldUnrefunded(mock.Anything).Return([]*entity.ResaleListing{soldListing("l-1", soldAt)}, nil)
		f.expectOrderCharge()
		f.orders.EXPECT().CreateRefund(mock.Anything, entity.RefundParams{
			OrderID: "order-1", ResaleListingID: "l-1", ProviderChargeID: "ch_1", Amount: 4500,
		}).Return("re_1", nil)
		f.listings.EXPECT().RecordRefund(mock.Anything, entity.ResaleListingID("l-1"), "re_1", now).Return(nil)

		require.NoError(t, f.uc.RefundSold(ctx))
	})

	t.Run("refund fails once", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/resale-listing/refund-sold "Refund fails once"
		now := soldAt.Add(time.Minute)
		f := newResaleFixture(t, now)
		f.listings.EXPECT().ListSoldUnrefunded(mock.Anything).Return([]*entity.ResaleListing{
			soldListing("l-1", soldAt), soldListing("l-2", soldAt),
		}, nil)
		f.expectOrderCharge()
		f.orders.EXPECT().CreateRefund(mock.Anything, mock.MatchedBy(func(p entity.RefundParams) bool { return p.ResaleListingID == "l-1" })).
			Return("", apperr.New(codes.Unavailable, "stripe down"))
		f.orders.EXPECT().CreateRefund(mock.Anything, mock.MatchedBy(func(p entity.RefundParams) bool { return p.ResaleListingID == "l-2" })).
			Return("re_2", nil)
		f.listings.EXPECT().RecordRefund(mock.Anything, entity.ResaleListingID("l-2"), "re_2", now).Return(nil)

		require.NoError(t, f.uc.RefundSold(ctx))
	})

	t.Run("refunded but not recorded", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/resale-listing/refund-sold "Refunded but not recorded"
		now := soldAt.Add(2 * time.Minute)
		f := newResaleFixture(t, now)
		f.listings.EXPECT().ListSoldUnrefunded(mock.Anything).Return([]*entity.ResaleListing{soldListing("l-1", soldAt)}, nil)
		f.expectOrderCharge()
		// The provider replays the first refund for the same purpose.
		f.orders.EXPECT().CreateRefund(mock.Anything, entity.RefundParams{
			OrderID: "order-1", ResaleListingID: "l-1", ProviderChargeID: "ch_1", Amount: 4500,
		}).Return("re_1", nil).Once()
		f.listings.EXPECT().RecordRefund(mock.Anything, entity.ResaleListingID("l-1"), "re_1", now).Return(nil)

		require.NoError(t, f.uc.RefundSold(ctx))
	})

	t.Run("charge cannot be refunded", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/resale-listing/refund-sold "Charge cannot be refunded"
		logger, buf := newCaptureLogger(t)
		listings := entitymocks.NewMockResaleListingRepository(t)
		orders := entitymocks.NewMockOrderRepository(t)
		uc := usecase.NewResaleListingUseCase(usecase.ResaleListingDeps{
			Listings: listings, Orders: orders, Clock: fixedClock(soldAt.Add(11 * time.Minute)), Logger: logger,
		})
		listings.EXPECT().ListSoldUnrefunded(mock.Anything).Return([]*entity.ResaleListing{soldListing("l-1", soldAt)}, nil).Twice()
		orders.EXPECT().Get(mock.Anything, entity.OrderID("order-1")).Return(&entity.Order{ID: "order-1", Payment: entity.Payment{ProviderPaymentID: "pi_1"}}, nil)
		orders.EXPECT().GetProviderChargeID(mock.Anything, "pi_1").Return("ch_1", nil)
		orders.EXPECT().CreateRefund(mock.Anything, mock.Anything).Return("", apperr.New(codes.FailedPrecondition, "refunded")).Twice()

		// Reported on every run, and tried again each time.
		require.NoError(t, uc.RefundSold(ctx))
		require.NoError(t, uc.RefundSold(ctx))

		out := buf.String()
		assert.Equal(t, 2, strings.Count(out, usecase.LogKeyNeedsOperator))
		assert.Contains(t, out, `"resale_listing_id":"l-1"`)
		assert.Contains(t, out, `"order_id":"order-1"`)
	})

	t.Run("a fresh sale is not reported", func(t *testing.T) {
		t.Parallel()
		logger, buf := newCaptureLogger(t)
		listings := entitymocks.NewMockResaleListingRepository(t)
		orders := entitymocks.NewMockOrderRepository(t)
		uc := usecase.NewResaleListingUseCase(usecase.ResaleListingDeps{
			Listings: listings, Orders: orders, Clock: fixedClock(soldAt.Add(9 * time.Minute)), Logger: logger,
		})
		listings.EXPECT().ListSoldUnrefunded(mock.Anything).Return([]*entity.ResaleListing{soldListing("l-1", soldAt)}, nil)
		orders.EXPECT().Get(mock.Anything, entity.OrderID("order-1")).Return(nil, apperr.New(codes.Unavailable, "db"))

		require.NoError(t, uc.RefundSold(ctx))

		assert.NotContains(t, buf.String(), usecase.LogKeyNeedsOperator)
	})

	t.Run("listing fails", func(t *testing.T) {
		t.Parallel()
		f := newResaleFixture(t, soldAt)
		f.listings.EXPECT().ListSoldUnrefunded(mock.Anything).Return(nil, errors.New("db down"))

		assert.Error(t, f.uc.RefundSold(ctx))
	})
}
