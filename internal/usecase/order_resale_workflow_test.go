package usecase_test

import (
	"context"
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

// resaleIssueFixture holds the mocks of the issue of a Won resale entry.
type resaleIssueFixture struct {
	now                time.Time
	entries            *entitymocks.MockResaleEntryRepository
	issuance           *entitymocks.MockIssuanceRepository
	orders             *entitymocks.MockOrderRepository
	stocks             *entitymocks.MockTicketStockRepository
	events             *entitymocks.MockEventRepository
	sales              *entitymocks.MockTicketSaleRepository
	organizers         *entitymocks.MockOrganizerRepository
	cardAuthorizations *entitymocks.MockCardAuthorizationRepository
	eventState         *ucmocks.MockEventPublishStatePort
	// published is what the event's publish state reads; true unless a test
	// cancels the concert.
	published bool
	// publishChecks counts the reads of the event's publish state.
	publishChecks int
	amount        int64
	uc            usecase.OrderUseCase
}

// newResaleIssueFixture builds the use case at now, for a Won entry for 2
// tickets of 5000 yen of an event starting at reStart.
func newResaleIssueFixture(t *testing.T, now time.Time) *resaleIssueFixture {
	t.Helper()
	return newResaleIssueFixtureOf(t, now, 2)
}

// newResaleIssueFixtureOf builds the use case at now, for a Won entry for
// count tickets of 5000 yen of a PUBLISHED event starting at reStart.
func newResaleIssueFixtureOf(t *testing.T, now time.Time, count int) *resaleIssueFixture {
	t.Helper()
	f := &resaleIssueFixture{
		eventState:         ucmocks.NewMockEventPublishStatePort(t),
		published:          true,
		amount:             int64(count) * 5000,
		now:                now,
		entries:            entitymocks.NewMockResaleEntryRepository(t),
		issuance:           entitymocks.NewMockIssuanceRepository(t),
		orders:             entitymocks.NewMockOrderRepository(t),
		stocks:             entitymocks.NewMockTicketStockRepository(t),
		events:             entitymocks.NewMockEventRepository(t),
		sales:              entitymocks.NewMockTicketSaleRepository(t),
		organizers:         entitymocks.NewMockOrganizerRepository(t),
		cardAuthorizations: entitymocks.NewMockCardAuthorizationRepository(t),
	}
	f.uc = usecase.NewOrderUseCase(usecase.OrderDeps{
		IssuanceRepo:       f.issuance,
		OrderRepo:          f.orders,
		ResaleEntryRepo:    f.entries,
		StockRepo:          f.stocks,
		EventRepo:          f.events,
		TicketSaleRepo:     f.sales,
		OrganizerRepo:      f.organizers,
		EventOrganizerRepo: &stubEventOrganizerRepo{getOrganizerIDFn: func(context.Context, string) (string, error) { return "organizer-1", nil }},
		CardAuthorizations: f.cardAuthorizations,
		EventState:         f.eventState,
		Clock:              fixedClock(now),
		Logger:             newTestLogger(t),
	})
	f.eventState.EXPECT().IsEventPublished(mock.Anything, "event-1").RunAndReturn(func(context.Context, string) (bool, error) {
		f.publishChecks++
		return f.published, nil
	}).Maybe()
	won := &entity.ResaleEntry{ID: "entry-1", OrderID: "order-1", State: entity.ResaleEntryStateWon, Order: f.pendingOrder(count)}
	f.entries.EXPECT().Get(mock.Anything, entity.ResaleEntryID("entry-1")).Return(won, nil).Maybe()
	f.stocks.EXPECT().Get(mock.Anything, entity.TicketStockID("stock-s"), now).Return(soldOutStock("stock-s", reSeatS, true), nil).Maybe()
	f.events.EXPECT().Get(mock.Anything, "event-1").Return(resaleEvent(), nil).Maybe()
	f.organizers.EXPECT().Get(mock.Anything, "organizer-1").Return(&entity.Organizer{ID: "organizer-1", PlatformFeeRateBps: 500}, nil).Maybe()
	f.sales.EXPECT().ListByEvent(mock.Anything, "event-1").Return([]*entity.TicketSale{{ID: "sale-1"}}, nil).Maybe()
	return f
}

// pendingOrder is the Won entry's Pending Order for count tickets.
func (f *resaleIssueFixture) pendingOrder(count int) *entity.Order {
	return &entity.Order{
		ID: "order-1", UserID: "fan-1", TicketStockID: "stock-s", TicketCount: count, Amount: int64(count) * 5000, Currency: "JPY",
		Status: entity.OrderStatusPending, CardAuthorizationID: "ca-1", ResaleEntryID: "entry-1",
	}
}

// expectCaptured makes the card authorization read back as Captured for the
// Order's amount.
func (f *resaleIssueFixture) expectCaptured() {
	f.cardAuthorizations.EXPECT().Get(mock.Anything, "ca-1").Return(&entity.CardAuthorization{
		ID: "ca-1", Status: entity.CardAuthorizationStatusCaptured,
		CapturedPayment: &entity.CapturedPayment{Provider: entity.PaymentProviderStripe, ProviderPaymentID: "pi_1", AmountJPY: f.amount, Currency: "JPY", CardBrand: "visa", CardLast4: "4242"},
	}, nil)
}

// expectIssue expects one attempt to make the Order Paid with its Tickets,
// which ends with err.
func (f *resaleIssueFixture) expectIssue(err error) {
	f.expectCaptured()
	f.orders.EXPECT().Get(mock.Anything, entity.OrderID("order-1")).RunAndReturn(func(context.Context, entity.OrderID) (*entity.Order, error) {
		return f.pendingOrder(int(f.amount / 5000)), nil
	})
	f.issuance.EXPECT().Issue(mock.Anything, mock.MatchedBy(func(o *entity.Order) bool {
		return o.ID == "order-1" && o.Status == entity.OrderStatusPaid && o.ResaleEntryID == "entry-1"
	}), mock.MatchedBy(func(ts []*entity.Ticket) bool { return int64(len(ts))*5000 == f.amount }), mock.Anything).Return(err).Once()
}

// expectIssued expects the Order to be made Paid with its Tickets.
func (f *resaleIssueFixture) expectIssued(t *testing.T) {
	t.Helper()
	f.expectIssue(nil)
}

// listingCancelled is the failure of Order.Issue when a listing allocated to
// the entry was cancelled.
var listingCancelled = apperr.Wrap(entity.ErrResaleListingCancelled, codes.FailedPrecondition, "a listing was cancelled")

func issueResale(f *resaleIssueFixture) (usecase.IssueResult, error) {
	return f.uc.Issue(context.Background(), &inlineFlow{}, usecase.IssueRequest{ResaleEntryID: "entry-1"})
}

func TestOrderUseCase_IssueResaleEntry(t *testing.T) {
	t.Parallel()
	beforeShow := time.Date(2026, 11, 10, 12, 30, 0, 0, reJST)
	atShow := reStart

	t.Run("won entry issued", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/order/issue "Won entry issued"
		f := newResaleIssueFixture(t, beforeShow)
		f.cardAuthorizations.EXPECT().Capture(mock.Anything, "ca-1").Return(nil).Once()
		f.expectIssued(t)

		got, err := issueResale(f)

		require.NoError(t, err)
		assert.Equal(t, usecase.IssueResult{OrderID: "order-1", Paid: true}, got, "charged once and issued with its listings (Order.Issue)")
	})

	t.Run("card can no longer be charged", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/order/issue "Card can no longer be charged"
		f := newResaleIssueFixture(t, beforeShow)
		f.cardAuthorizations.EXPECT().Capture(mock.Anything, "ca-1").Return(apperr.New(codes.FailedPrecondition, "canceled")).Once()
		f.entries.EXPECT().RevertWin(mock.Anything, entity.ResaleEntryID("entry-1"), entity.ResaleEntryLostReasonCardNotCharged).Return(nil).Once()

		got, err := issueResale(f)

		require.NoError(t, err)
		assert.False(t, got.Paid)
	})

	t.Run("provider unreachable until the show", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/order/issue "Provider unreachable until the show"
		// The capture step's last retry runs at the start time and charges
		// nothing; the release succeeds.
		f := newResaleIssueFixture(t, atShow)
		f.cardAuthorizations.EXPECT().Release(mock.Anything, "ca-1").Return(nil).Once()
		f.entries.EXPECT().RevertWin(mock.Anything, entity.ResaleEntryID("entry-1"), entity.ResaleEntryLostReasonPaymentNotCompletedBeforeShow).Return(nil).Once()

		got, err := issueResale(f)

		require.NoError(t, err)
		assert.False(t, got.Paid)
		f.cardAuthorizations.AssertNotCalled(t, "Capture", mock.Anything, mock.Anything)
	})

	t.Run("outcome unknown at the show", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/order/issue "Outcome unknown at the show"
		// Before the show a capture ends with Unavailable: the step fails and
		// is retried; nothing is reverted.
		f := newResaleIssueFixture(t, beforeShow)
		f.cardAuthorizations.EXPECT().Capture(mock.Anything, "ca-1").Return(unavailable).Once()
		_, err := issueResale(f)
		assert.ErrorIs(t, err, apperr.ErrUnavailable)

		// At the show the release cannot reach the provider either: still
		// retried, the entry stays Won.
		g := newResaleIssueFixture(t, atShow)
		g.cardAuthorizations.EXPECT().Release(mock.Anything, "ca-1").Return(unavailable).Once()
		_, err = issueResale(g)
		assert.ErrorIs(t, err, apperr.ErrUnavailable, "the release is tried again and an admin is told at the deadline")

		f.entries.AssertNotCalled(t, "RevertWin", mock.Anything, mock.Anything, mock.Anything)
		g.entries.AssertNotCalled(t, "RevertWin", mock.Anything, mock.Anything, mock.Anything)
	})

	t.Run("charged before the show after all", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/order/issue "Charged before the show after all"
		f := newResaleIssueFixture(t, atShow)
		f.cardAuthorizations.EXPECT().Release(mock.Anything, "ca-1").Return(apperr.New(codes.FailedPrecondition, "captured")).Once()
		f.expectIssued(t)

		got, err := issueResale(f)

		require.NoError(t, err)
		assert.True(t, got.Paid, "the fan holds the tickets they paid for")
		f.entries.AssertNotCalled(t, "RevertWin", mock.Anything, mock.Anything, mock.Anything)
	})

	t.Run("concert cancelled before the charge", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/order/issue "Concert cancelled before the charge"
		f := newResaleIssueFixture(t, beforeShow)
		f.published = false
		f.cardAuthorizations.EXPECT().Get(mock.Anything, "ca-1").Return(&entity.CardAuthorization{
			ID: "ca-1", Status: entity.CardAuthorizationStatusAuthorized,
		}, nil).Once()
		f.entries.EXPECT().RevertWin(mock.Anything, entity.ResaleEntryID("entry-1"), entity.ResaleEntryLostReasonConcertCancelled).Return(nil).Once()

		got, err := issueResale(f)

		require.NoError(t, err)
		assert.Equal(t, usecase.IssueResult{OrderID: "order-1"}, got)
		f.cardAuthorizations.AssertNotCalled(t, "Capture", mock.Anything, mock.Anything)
		f.cardAuthorizations.AssertNotCalled(t, "Release", mock.Anything, mock.Anything)
		f.issuance.AssertNotCalled(t, "Issue", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	})

	t.Run("concert cancelled after a capture whose outcome was unknown", func(t *testing.T) {
		t.Parallel()
		// An earlier try of the capture step ended with Unavailable but did
		// charge the card: the entry is issued, never made Lost while charged.
		f := newResaleIssueFixture(t, beforeShow)
		f.published = false
		f.expectIssued(t)

		got, err := issueResale(f)

		require.NoError(t, err)
		assert.True(t, got.Paid)
		f.cardAuthorizations.AssertNotCalled(t, "Capture", mock.Anything, mock.Anything)
		f.entries.AssertNotCalled(t, "RevertWin", mock.Anything, mock.Anything, mock.Anything)
	})

	t.Run("resumed after the capture does not check the event again", func(t *testing.T) {
		t.Parallel()
		f := newResaleIssueFixture(t, beforeShow)
		f.cardAuthorizations.EXPECT().Capture(mock.Anything, "ca-1").Return(nil).Once()
		flow := newReplayFlow()
		flow.stopAt = "read captured payment"
		_, err := f.uc.Issue(context.Background(), flow, usecase.IssueRequest{ResaleEntryID: "entry-1"})
		require.ErrorIs(t, err, errStopped)

		// The concert is cancelled while the worker is stopped.
		f.published = false
		flow.stopAt = ""
		f.expectIssued(t)
		got, err := f.uc.Issue(context.Background(), flow, usecase.IssueRequest{ResaleEntryID: "entry-1"})

		require.NoError(t, err)
		assert.True(t, got.Paid, "the charge already made is issued")
		assert.Equal(t, 1, f.publishChecks, "the recorded capture is not checked again")
	})

	t.Run("listing cancelled after the charge", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/order/issue "Listing cancelled after the charge"
		f := newResaleIssueFixtureOf(t, beforeShow, 1)
		f.cardAuthorizations.EXPECT().Capture(mock.Anything, "ca-1").Return(nil).Once()
		f.expectIssue(listingCancelled)
		f.orders.EXPECT().GetProviderChargeID(mock.Anything, "pi_1").Return("ch_1", nil)
		f.orders.EXPECT().CreateRefund(mock.Anything, entity.RefundParams{OrderID: "order-1", ProviderChargeID: "ch_1", Amount: 5000}).Return("re_1", nil).Once()
		f.entries.EXPECT().RevertWin(mock.Anything, entity.ResaleEntryID("entry-1"), entity.ResaleEntryLostReasonListingCancelled).Return(nil).Once()

		got, err := issueResale(f)

		require.NoError(t, err)
		assert.Equal(t, usecase.IssueResult{OrderID: "order-1"}, got, "5000 yen refunded, the entry Lost and no Ticket issued")
	})

	t.Run("listing cancelled: a resumed run refunds once", func(t *testing.T) {
		t.Parallel()
		f := newResaleIssueFixtureOf(t, beforeShow, 1)
		f.cardAuthorizations.EXPECT().Capture(mock.Anything, "ca-1").Return(nil).Once()
		f.expectIssue(listingCancelled)
		f.orders.EXPECT().GetProviderChargeID(mock.Anything, "pi_1").Return("ch_1", nil)
		f.orders.EXPECT().CreateRefund(mock.Anything, mock.Anything).Return("re_1", nil).Once()
		f.entries.EXPECT().RevertWin(mock.Anything, entity.ResaleEntryID("entry-1"), entity.ResaleEntryLostReasonListingCancelled).Return(nil).Once()
		flow := newReplayFlow()
		flow.stopAt = "revert win"
		_, err := f.uc.Issue(context.Background(), flow, usecase.IssueRequest{ResaleEntryID: "entry-1"})
		require.ErrorIs(t, err, errStopped)

		flow.stopAt = ""
		got, err := f.uc.Issue(context.Background(), flow, usecase.IssueRequest{ResaleEntryID: "entry-1"})

		require.NoError(t, err)
		assert.False(t, got.Paid)
	})

	t.Run("listing cancelled: refund unavailable keeps the entry won", func(t *testing.T) {
		t.Parallel()
		f := newResaleIssueFixtureOf(t, beforeShow, 1)
		f.cardAuthorizations.EXPECT().Capture(mock.Anything, "ca-1").Return(nil).Once()
		f.expectIssue(listingCancelled)
		f.orders.EXPECT().GetProviderChargeID(mock.Anything, "pi_1").Return("ch_1", nil)
		f.orders.EXPECT().CreateRefund(mock.Anything, mock.Anything).Return("", unavailable).Once()

		_, err := issueResale(f)

		assert.ErrorIs(t, err, apperr.ErrUnavailable, "the refund is tried again and an admin is told at the deadline")
		f.entries.AssertNotCalled(t, "RevertWin", mock.Anything, mock.Anything, mock.Anything)
	})

	t.Run("entry no longer won", func(t *testing.T) {
		t.Parallel()
		f := entitymocks.NewMockResaleEntryRepository(t)
		f.EXPECT().Get(mock.Anything, entity.ResaleEntryID("entry-1")).Return(&entity.ResaleEntry{ID: "entry-1", State: entity.ResaleEntryStateLost,
			Order: &entity.Order{ID: "order-1", Status: entity.OrderStatusVoid}}, nil)
		uc := usecase.NewOrderUseCase(usecase.OrderDeps{ResaleEntryRepo: f, Clock: fixedClock(beforeShow), Logger: newTestLogger(t)})

		_, err := uc.Issue(context.Background(), &inlineFlow{}, usecase.IssueRequest{ResaleEntryID: "entry-1"})

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition, "nothing is charged")
	})
}
