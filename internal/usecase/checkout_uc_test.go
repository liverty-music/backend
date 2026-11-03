package usecase_test

import (
	"context"
	"testing"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	entitymocks "github.com/liverty-music/backend/internal/entity/mocks"
	"github.com/liverty-music/backend/internal/usecase"
	ucmocks "github.com/liverty-music/backend/internal/usecase/mocks"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// checkoutFixture holds the mocks of a CheckoutUseCase under test.
type checkoutFixture struct {
	checkouts             *entitymocks.MockCheckoutRepository
	sales                 *entitymocks.MockTicketSaleRepository
	stocks                *entitymocks.MockTicketStockRepository
	entries               *entitymocks.MockLotteryEntryRepository
	orders                *entitymocks.MockOrderRepository
	users                 *entitymocks.MockUserRepository
	identityVerifications *entitymocks.MockIdentityVerificationRepository
	events                *ucmocks.MockEventPublishStatePort
	eventRepo             *entitymocks.MockEventRepository
	auth                  *entitymocks.MockCardAuthorizationRepository
	workflows             *ucmocks.MockWorkflows
	uc                    usecase.CheckoutUseCase
}

func newCheckoutFixture(t *testing.T, now time.Time) *checkoutFixture {
	t.Helper()
	f := &checkoutFixture{
		checkouts:             entitymocks.NewMockCheckoutRepository(t),
		sales:                 entitymocks.NewMockTicketSaleRepository(t),
		stocks:                entitymocks.NewMockTicketStockRepository(t),
		entries:               entitymocks.NewMockLotteryEntryRepository(t),
		orders:                entitymocks.NewMockOrderRepository(t),
		users:                 entitymocks.NewMockUserRepository(t),
		identityVerifications: entitymocks.NewMockIdentityVerificationRepository(t),
		events:                ucmocks.NewMockEventPublishStatePort(t),
		eventRepo:             entitymocks.NewMockEventRepository(t),
		auth:                  entitymocks.NewMockCardAuthorizationRepository(t),
		workflows:             ucmocks.NewMockWorkflows(t),
	}
	f.uc = usecase.NewCheckoutUseCase(usecase.CheckoutDeps{
		CheckoutRepo:             f.checkouts,
		SaleRepo:                 f.sales,
		StockRepo:                f.stocks,
		EntryRepo:                f.entries,
		OrderRepo:                f.orders,
		UserRepo:                 f.users,
		IdentityVerificationRepo: f.identityVerifications,
		EventState:               f.events,
		EventRepo:                f.eventRepo,
		CardAuthorizations:       f.auth,
		Workflows:                f.workflows,
		Clock:                    fixedClock(now),
		Logger:                   newTestLogger(t),
	})
	return f
}

// coSaleStart and coSaleEnd are the window of the first-come sale "sale-1".
var (
	coSaleStart = time.Date(2026, 11, 1, 10, 0, 0, 0, tsJST)
	coSaleEnd   = time.Date(2026, 11, 20, 19, 0, 0, 0, tsJST)
)

// coSeatS is the S席 seat type, 3000 yen; coSeatA the A席 seat type.
var (
	coSeatS = &entity.SeatType{ID: "seat-s", SeriesID: "series-1", Name: "S席", Price: 3000}
	coSeatA = &entity.SeatType{ID: "seat-a", SeriesID: "series-1", Name: "A席", Price: 2000}
)

// coStock returns the S席 stock "stock-1" of event-1 with the given quantity,
// sold and held counts.
func coStock(quantity, sold, held int) *entity.TicketStock {
	return &entity.TicketStock{
		ID: "stock-1", EventID: "event-1", SeatTypeID: coSeatS.ID, Quantity: quantity,
		SoldCount: sold, HeldCount: held, SeatType: coSeatS,
	}
}

// coSale returns the first-come sale "sale-1" of event-1.
func coSale() *entity.TicketSale {
	return &entity.TicketSale{
		ID: "sale-1", SeriesID: "series-1", Name: "一般発売", Method: entity.TicketSaleMethodFirstCome,
		SaleStartTime: coSaleStart, SaleEndTime: coSaleEnd, EventIDs: []string{"event-1"},
	}
}

// coEvent returns event-1 with the given per-account limit.
func coEvent(limit int) *entity.Event {
	return &entity.Event{ID: "event-1", SeriesID: "series-1", PerAccountLimit: &limit}
}

// fanCheckout returns fan-1's Held checkout of the sale for 2 S席 tickets
// (6000 yen) started at startedAt, expiring 15 minutes later, with its
// Pending Order.
func fanCheckout(startedAt time.Time) *entity.Checkout {
	return &entity.Checkout{
		ID: "checkout-1", OrderID: "order-1", TicketSaleID: "sale-1",
		Status: entity.CheckoutStatusHeld, HoldExpireTime: startedAt.Add(15 * time.Minute),
		Order: &entity.Order{
			ID: "order-1", UserID: "fan-1", TicketStockID: "stock-1", TicketCount: 2, Amount: 6000,
			Currency: "JPY", Status: entity.OrderStatusPending, CheckoutID: "checkout-1",
		},
	}
}

// expectStartReads expects the reads Start makes before it checks the
// count: the stock, whether the event is published and the event's sales.
func expectStartReads(f *checkoutFixture, stock *entity.TicketStock, sales ...*entity.TicketSale) {
	f.stocks.EXPECT().Get(mock.Anything, stock.ID, mock.Anything).Return(stock, nil)
	f.events.EXPECT().IsEventPublished(mock.Anything, "event-1").Return(true, nil)
	f.sales.EXPECT().ListByEvent(mock.Anything, "event-1").Return(sales, nil)
}

// expectFitReads expects the event, the fan's checkouts and what the fan
// holds for the event, after the count is checked.
func expectFitReads(f *checkoutFixture, limit int, holding []*entity.Checkout, except *entity.OrderID, held entity.HeldTickets) {
	f.eventRepo.EXPECT().Get(mock.Anything, "event-1").Return(coEvent(limit), nil)
	f.checkouts.EXPECT().ListByUser(mock.Anything, entity.UserID("fan-1")).Return(holding, nil)
	f.orders.EXPECT().GetHeldForEvent(mock.Anything, entity.UserID("fan-1"), "event-1", except, mock.Anything).Return(held, nil)
}

func TestCheckoutUseCase_Start(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 5, 18, 0, 0, 0, tsJST)
	code := entity.ReferralCode("member-a")

	t.Run("fan starts a checkout", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/checkout/start "Fan starts a checkout"
		f := newCheckoutFixture(t, now)
		expectStartReads(f, coStock(150, 0, 0), coSale())
		expectFitReads(f, 4, nil, nil, entity.HeldTickets{})
		checkout := fanCheckout(now)
		f.checkouts.EXPECT().GetOrCreateHeld(mock.Anything, entity.TicketStockID("stock-1"), entity.TicketSaleID("sale-1"), entity.UserID("fan-1"), 2, (*entity.ReferralCode)(nil), now).Return(checkout, nil)

		got, err := f.uc.Start(context.Background(), "fan-1", "stock-1", 2, nil)

		require.NoError(t, err)
		assert.Same(t, checkout, got)
		assert.Equal(t, entity.OrderStatusPending, got.Order.Status)
	})

	t.Run("presale lottery not drawn yet", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/checkout/start "Presale lottery not drawn yet"
		f := newCheckoutFixture(t, coSaleStart.Add(30*time.Second))
		presale := &entity.TicketSale{
			ID: "sale-pre", SeriesID: "series-1", Name: "FC先行", Method: entity.TicketSaleMethodLottery,
			SaleStartTime: coSaleStart.Add(-72 * time.Hour), SaleEndTime: coSaleStart, EventIDs: []string{"event-1"},
		}
		expectStartReads(f, coStock(150, 0, 0), presale, coSale())
		f.entries.EXPECT().ListEnteredByTicketSaleAndTicketStock(mock.Anything, entity.TicketSaleID("sale-pre"), entity.TicketStockID("stock-1")).
			Return([]*entity.LotteryEntry{{ID: "entry-1"}}, nil)

		_, err := f.uc.Start(context.Background(), "fan-1", "stock-1", 2, nil)

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
	})

	t.Run("drawn presale does not block", func(t *testing.T) {
		t.Parallel()
		f := newCheckoutFixture(t, now)
		presale := &entity.TicketSale{
			ID: "sale-pre", SeriesID: "series-1", Name: "FC先行", Method: entity.TicketSaleMethodLottery,
			SaleStartTime: coSaleStart.Add(-72 * time.Hour), SaleEndTime: coSaleStart, EventIDs: []string{"event-1"},
		}
		expectStartReads(f, coStock(150, 120, 0), presale, coSale())
		f.entries.EXPECT().ListEnteredByTicketSaleAndTicketStock(mock.Anything, entity.TicketSaleID("sale-pre"), entity.TicketStockID("stock-1")).Return(nil, nil)
		expectFitReads(f, 4, nil, nil, entity.HeldTickets{})
		f.checkouts.EXPECT().GetOrCreateHeld(mock.Anything, entity.TicketStockID("stock-1"), entity.TicketSaleID("sale-1"), entity.UserID("fan-1"), 2, (*entity.ReferralCode)(nil), now).Return(fanCheckout(now), nil)

		_, err := f.uc.Start(context.Background(), "fan-1", "stock-1", 2, nil)

		require.NoError(t, err)
	})

	t.Run("fan reloads mid-checkout", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/checkout/start "Fan reloads mid-checkout"
		f := newCheckoutFixture(t, now.Add(5*time.Minute))
		original := fanCheckout(now)
		expectStartReads(f, coStock(150, 0, 2), coSale())
		except := original.OrderID
		expectFitReads(f, 4, []*entity.Checkout{original}, &except, entity.HeldTickets{})
		f.checkouts.EXPECT().GetOrCreateHeld(mock.Anything, entity.TicketStockID("stock-1"), entity.TicketSaleID("sale-1"), entity.UserID("fan-1"), 2, (*entity.ReferralCode)(nil), mock.Anything).Return(original, nil)

		got, err := f.uc.Start(context.Background(), "fan-1", "stock-1", 2, nil)

		require.NoError(t, err)
		assert.Equal(t, original.HoldExpireTime, got.HoldExpireTime)
	})

	t.Run("sale not open yet", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/checkout/start "Sale not open yet"
		f := newCheckoutFixture(t, time.Date(2026, 10, 31, 12, 0, 0, 0, tsJST))
		expectStartReads(f, coStock(150, 0, 0), coSale())

		_, err := f.uc.Start(context.Background(), "fan-1", "stock-1", 2, nil)

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
	})

	t.Run("every sale ended", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/checkout/start "Every sale ended"
		f := newCheckoutFixture(t, coSaleEnd.Add(time.Minute))
		expectStartReads(f, coStock(150, 0, 0), coSale())

		_, err := f.uc.Start(context.Background(), "fan-1", "stock-1", 2, nil)

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
	})

	t.Run("only a lottery is open", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/checkout/start "Only a lottery is open"
		f := newCheckoutFixture(t, now)
		lottery := coSale()
		lottery.Method = entity.TicketSaleMethodLottery
		lottery.SaleStartTime, lottery.SaleEndTime = now.Add(-24*time.Hour), now.Add(48*time.Hour)
		expectStartReads(f, coStock(150, 0, 0), lottery)

		_, err := f.uc.Start(context.Background(), "fan-1", "stock-1", 2, nil)

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
	})

	t.Run("concert cancelled", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/checkout/start "Concert cancelled"
		f := newCheckoutFixture(t, now)
		f.stocks.EXPECT().Get(mock.Anything, entity.TicketStockID("stock-1"), now).Return(coStock(150, 0, 0), nil)
		f.events.EXPECT().IsEventPublished(mock.Anything, "event-1").Return(false, nil)

		_, err := f.uc.Start(context.Background(), "fan-1", "stock-1", 2, nil)

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
	})

	t.Run("unknown stock", func(t *testing.T) {
		t.Parallel()
		f := newCheckoutFixture(t, now)
		f.stocks.EXPECT().Get(mock.Anything, entity.TicketStockID("stock-x"), now).Return(nil, apperr.ErrNotFound)

		_, err := f.uc.Start(context.Background(), "fan-1", "stock-x", 2, nil)

		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})

	t.Run("last tickets in other checkouts", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/checkout/start "Last tickets in other checkouts"
		f := newCheckoutFixture(t, now)
		expectStartReads(f, coStock(150, 147, 3), coSale())
		expectFitReads(f, 4, nil, nil, entity.HeldTickets{})
		f.checkouts.EXPECT().GetOrCreateHeld(mock.Anything, entity.TicketStockID("stock-1"), entity.TicketSaleID("sale-1"), entity.UserID("fan-1"), 2, (*entity.ReferralCode)(nil), now).
			Return(nil, apperr.New(codes.ResourceExhausted, "not enough tickets remain"))

		_, err := f.uc.Start(context.Background(), "fan-1", "stock-1", 2, nil)

		assert.ErrorIs(t, err, apperr.ErrResourceExhausted)
	})

	t.Run("count over the limit", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/checkout/start "Count over the limit"
		f := newCheckoutFixture(t, now)
		expectStartReads(f, coStock(150, 0, 0), coSale())
		f.eventRepo.EXPECT().Get(mock.Anything, "event-1").Return(coEvent(4), nil)

		_, err := f.uc.Start(context.Background(), "fan-1", "stock-1", 5, nil)

		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
	})

	t.Run("presale winner over the limit", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/checkout/start "Presale winner over the limit"
		f := newCheckoutFixture(t, now)
		expectStartReads(f, coStock(150, 0, 0), coSale())
		expectFitReads(f, 4, nil, nil, entity.HeldTickets{Count: 4, SeatTypeID: &coSeatS.ID})

		_, err := f.uc.Start(context.Background(), "fan-1", "stock-1", 1, nil)

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
	})

	t.Run("another seat type", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/checkout/start "Another seat type"
		f := newCheckoutFixture(t, now)
		expectStartReads(f, coStock(150, 0, 0), coSale())
		expectFitReads(f, 4, nil, nil, entity.HeldTickets{Count: 2, SeatTypeID: &coSeatA.ID})

		_, err := f.uc.Start(context.Background(), "fan-1", "stock-1", 1, nil)

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
	})

	t.Run("changing the count of a holding checkout", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/checkout/start "Changing the count of a holding checkout"
		f := newCheckoutFixture(t, now.Add(time.Minute))
		holding := fanCheckout(now)
		holding.Order.TicketCount = 4
		expectStartReads(f, coStock(150, 0, 4), coSale())
		except := holding.OrderID
		// The holding Checkout's Order is left out, so 3 more fit a limit of 4.
		expectFitReads(f, 4, []*entity.Checkout{holding}, &except, entity.HeldTickets{})
		replacement := fanCheckout(now.Add(time.Minute))
		replacement.ID, replacement.OrderID, replacement.Order.ID, replacement.Order.TicketCount = "checkout-2", "order-2", "order-2", 3
		f.checkouts.EXPECT().GetOrCreateHeld(mock.Anything, entity.TicketStockID("stock-1"), entity.TicketSaleID("sale-1"), entity.UserID("fan-1"), 3, (*entity.ReferralCode)(nil), mock.Anything).Return(replacement, nil)

		got, err := f.uc.Start(context.Background(), "fan-1", "stock-1", 3, nil)

		require.NoError(t, err)
		assert.Equal(t, 3, got.Order.TicketCount)
	})

	t.Run("checkout from a referral link", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/checkout/start "Checkout from a referral link"
		f := newCheckoutFixture(t, now)
		expectStartReads(f, coStock(150, 0, 0), coSale())
		expectFitReads(f, 4, nil, nil, entity.HeldTickets{})
		checkout := fanCheckout(now)
		checkout.Order.ReferralCode = &code
		f.checkouts.EXPECT().GetOrCreateHeld(mock.Anything, entity.TicketStockID("stock-1"), entity.TicketSaleID("sale-1"), entity.UserID("fan-1"), 2, &code, now).Return(checkout, nil)

		got, err := f.uc.Start(context.Background(), "fan-1", "stock-1", 2, &code)

		require.NoError(t, err)
		assert.Equal(t, &code, got.Order.ReferralCode)
	})

	t.Run("another link before confirming", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/checkout/start "Another link before confirming"
		f := newCheckoutFixture(t, now.Add(time.Minute))
		original := fanCheckout(now)
		original.Order.ReferralCode = &code
		other := entity.ReferralCode("member-b")
		expectStartReads(f, coStock(150, 0, 2), coSale())
		except := original.OrderID
		expectFitReads(f, 4, []*entity.Checkout{original}, &except, entity.HeldTickets{})
		// The repository resumes the Checkout; its Order keeps its code.
		f.checkouts.EXPECT().GetOrCreateHeld(mock.Anything, entity.TicketStockID("stock-1"), entity.TicketSaleID("sale-1"), entity.UserID("fan-1"), 2, &other, mock.Anything).Return(original, nil)

		got, err := f.uc.Start(context.Background(), "fan-1", "stock-1", 2, &other)

		require.NoError(t, err)
		assert.Equal(t, &code, got.Order.ReferralCode)
	})

	t.Run("no code", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/checkout/start "No code"
		f := newCheckoutFixture(t, now)
		expectStartReads(f, coStock(150, 0, 0), coSale())
		expectFitReads(f, 4, nil, nil, entity.HeldTickets{})
		f.checkouts.EXPECT().GetOrCreateHeld(mock.Anything, entity.TicketStockID("stock-1"), entity.TicketSaleID("sale-1"), entity.UserID("fan-1"), 2, (*entity.ReferralCode)(nil), now).Return(fanCheckout(now), nil)

		got, err := f.uc.Start(context.Background(), "fan-1", "stock-1", 2, nil)

		require.NoError(t, err)
		assert.Nil(t, got.Order.ReferralCode)
	})

	t.Run("sale not open with a code", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/checkout/start "Sale not open with a code"
		f := newCheckoutFixture(t, time.Date(2026, 10, 31, 12, 0, 0, 0, tsJST))
		expectStartReads(f, coStock(150, 0, 0), coSale())

		_, err := f.uc.Start(context.Background(), "fan-1", "stock-1", 2, &code)

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
	})

	t.Run("first-come sale requires verification", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/checkout/start "First-come sale requires verification"
		f := newCheckoutFixture(t, now)
		sale := coSale()
		sale.VerificationRequirement = entity.VerificationRequirementJPKIOnly
		expectStartReads(f, coStock(150, 0, 0), sale)
		f.eventRepo.EXPECT().Get(mock.Anything, "event-1").Return(coEvent(4), nil)
		f.identityVerifications.EXPECT().GetByUserID(mock.Anything, "fan-1").Return(nil, apperr.ErrNotFound)

		_, err := f.uc.Start(context.Background(), "fan-1", "stock-1", 2, nil)

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
	})

	t.Run("identity verification needs re-verification", func(t *testing.T) {
		t.Parallel()
		f := newCheckoutFixture(t, now)
		sale := coSale()
		sale.VerificationRequirement = entity.VerificationRequirementVerifiedAny
		expectStartReads(f, coStock(150, 0, 0), sale)
		f.eventRepo.EXPECT().Get(mock.Anything, "event-1").Return(coEvent(4), nil)
		f.identityVerifications.EXPECT().GetByUserID(mock.Anything, "fan-1").
			Return(&entity.IdentityVerification{ID: "identity verification-1", UserID: "fan-1", Status: entity.VerificationStatusNeedsReverification}, nil)

		_, err := f.uc.Start(context.Background(), "fan-1", "stock-1", 2, nil)

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
	})

	t.Run("verified fan starts a checkout", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/checkout/start "Verified fan starts a checkout"
		f := newCheckoutFixture(t, now)
		sale := coSale()
		sale.VerificationRequirement = entity.VerificationRequirementJPKIOnly
		expectStartReads(f, coStock(150, 0, 0), sale)
		f.identityVerifications.EXPECT().GetByUserID(mock.Anything, "fan-1").
			Return(&entity.IdentityVerification{ID: "identity verification-1", UserID: "fan-1", Status: entity.VerificationStatusActive}, nil)
		expectFitReads(f, 4, nil, nil, entity.HeldTickets{})
		checkout := fanCheckout(now)
		f.checkouts.EXPECT().GetOrCreateHeld(mock.Anything, entity.TicketStockID("stock-1"), entity.TicketSaleID("sale-1"), entity.UserID("fan-1"), 2, (*entity.ReferralCode)(nil), now).Return(checkout, nil)

		got, err := f.uc.Start(context.Background(), "fan-1", "stock-1", 2, nil)

		require.NoError(t, err)
		assert.Same(t, checkout, got)
	})
}

func TestCheckoutUseCase_Get(t *testing.T) {
	t.Parallel()
	start := time.Date(2026, 11, 5, 18, 0, 0, 0, tsJST)
	committed := start.Add(10 * time.Minute)

	t.Run("within the hold", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/checkout/get "Within the hold"
		f := newCheckoutFixture(t, start.Add(5*time.Minute))
		f.checkouts.EXPECT().Get(mock.Anything, entity.CheckoutID("checkout-1")).Return(fanCheckout(start), nil)

		got, err := f.uc.Get(context.Background(), "fan-1", "checkout-1")

		require.NoError(t, err)
		assert.Equal(t, entity.CheckoutStatusHeld, got.Status)
		assert.Equal(t, start.Add(15*time.Minute), got.HoldExpireTime)
	})

	t.Run("hold lapsed", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/checkout/get "Hold lapsed"
		f := newCheckoutFixture(t, start.Add(20*time.Minute))
		stored := fanCheckout(start)
		f.checkouts.EXPECT().Get(mock.Anything, entity.CheckoutID("checkout-1")).Return(stored, nil)

		got, err := f.uc.Get(context.Background(), "fan-1", "checkout-1")

		require.NoError(t, err)
		assert.Equal(t, entity.CheckoutStatusExpired, got.Status)
		assert.Equal(t, entity.CheckoutStatusHeld, stored.Status, "the stored Checkout is not changed")
	})

	t.Run("card no longer chargeable", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/checkout/get "Card no longer chargeable"
		f := newCheckoutFixture(t, start.Add(12*time.Minute))
		checkout := fanCheckout(start)
		checkout.Status, checkout.CommitTime, checkout.Order.CardAuthorizationID = entity.CheckoutStatusReleased, &committed, "pi_1"
		f.checkouts.EXPECT().Get(mock.Anything, entity.CheckoutID("checkout-1")).Return(checkout, nil)

		got, err := f.uc.Get(context.Background(), "fan-1", "checkout-1")

		require.NoError(t, err)
		assert.Equal(t, entity.CheckoutStatusReleased, got.Status)
		assert.NotNil(t, got.CommitTime, "ever committed")
		assert.Nil(t, got.CaptureTime, "not charged")
	})

	t.Run("replaced by a newer checkout", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/checkout/get "Replaced by a newer checkout"
		f := newCheckoutFixture(t, start.Add(5*time.Minute))
		checkout := fanCheckout(start)
		checkout.Status = entity.CheckoutStatusReleased
		f.checkouts.EXPECT().Get(mock.Anything, entity.CheckoutID("checkout-1")).Return(checkout, nil)

		got, err := f.uc.Get(context.Background(), "fan-1", "checkout-1")

		require.NoError(t, err)
		assert.Equal(t, entity.CheckoutStatusReleased, got.Status)
		assert.Nil(t, got.CommitTime, "never committed")
	})

	t.Run("completed checkout stays completed after its hold expiry", func(t *testing.T) {
		t.Parallel()
		f := newCheckoutFixture(t, start.Add(30*time.Minute))
		checkout := fanCheckout(start)
		checkout.Status, checkout.CommitTime, checkout.CaptureTime = entity.CheckoutStatusCompleted, &committed, &committed
		f.checkouts.EXPECT().Get(mock.Anything, entity.CheckoutID("checkout-1")).Return(checkout, nil)

		got, err := f.uc.Get(context.Background(), "fan-1", "checkout-1")

		require.NoError(t, err)
		assert.Equal(t, entity.CheckoutStatusCompleted, got.Status)
	})

	t.Run("someone else's checkout", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/checkout/get "Someone else's checkout"
		f := newCheckoutFixture(t, start)
		f.checkouts.EXPECT().Get(mock.Anything, entity.CheckoutID("checkout-1")).Return(fanCheckout(start), nil)

		_, err := f.uc.Get(context.Background(), "fan-2", "checkout-1")

		assert.ErrorIs(t, err, apperr.ErrPermissionDenied)
	})

	t.Run("unknown checkout", func(t *testing.T) {
		t.Parallel()
		f := newCheckoutFixture(t, start)
		f.checkouts.EXPECT().Get(mock.Anything, entity.CheckoutID("checkout-x")).Return(nil, apperr.ErrNotFound)

		_, err := f.uc.Get(context.Background(), "fan-1", "checkout-x")

		assert.ErrorIs(t, err, apperr.ErrPermissionDenied, "does not reveal whether it exists")
	})
}

func TestCheckoutUseCase_Authorize(t *testing.T) {
	t.Parallel()
	start := time.Date(2026, 11, 5, 18, 0, 0, 0, tsJST)
	const name, phone = "山田 花子", "+819012345678"

	t.Run("fan authorizes", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/checkout/authorize-card "Fan authorizes their card"
		f := newCheckoutFixture(t, start.Add(3*time.Minute))
		f.checkouts.EXPECT().Get(mock.Anything, entity.CheckoutID("checkout-1")).Return(fanCheckout(start), nil)
		f.stocks.EXPECT().Get(mock.Anything, entity.TicketStockID("stock-1"), mock.Anything).Return(coStock(150, 0, 2), nil)
		updateDetails := f.users.EXPECT().UpdatePersonalDetails(mock.Anything, "fan-1", name, phone).
			Return(&entity.User{ID: "fan-1", FullName: name, PhoneNumber: phone}, nil).Call
		createHold := f.auth.EXPECT().Create(mock.Anything, entity.CardAuthorizationPurchase{
			Purpose:    entity.CardAuthorizationPurposeCheckout,
			CheckoutID: "checkout-1", UserID: "fan-1", TicketStockID: "stock-1", EventID: "event-1", TicketCount: 2,
		}, int64(6000)).Return(&entity.CardAuthorization{ID: "pi_1", Secret: "secret_1", Status: entity.CardAuthorizationStatusPending}, nil).NotBefore(updateDetails)
		f.checkouts.EXPECT().SetCardAuthorization(mock.Anything, entity.CheckoutID("checkout-1"), "pi_1").Return(nil).NotBefore(createHold)

		secret, err := f.uc.AuthorizeCard(context.Background(), "fan-1", "checkout-1", name, phone)

		require.NoError(t, err)
		assert.Equal(t, "secret_1", secret)
	})

	t.Run("card declined, fan retries", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/checkout/authorize-card "Card declined, fan retries"
		f := newCheckoutFixture(t, start.Add(5*time.Minute))
		checkout := fanCheckout(start)
		checkout.Order.CardAuthorizationID = "pi_1"
		f.checkouts.EXPECT().Get(mock.Anything, entity.CheckoutID("checkout-1")).Return(checkout, nil)
		f.stocks.EXPECT().Get(mock.Anything, entity.TicketStockID("stock-1"), mock.Anything).Return(coStock(150, 0, 2), nil)
		f.users.EXPECT().UpdatePersonalDetails(mock.Anything, "fan-1", name, phone).Return(&entity.User{ID: "fan-1"}, nil)
		f.auth.EXPECT().Create(mock.Anything, mock.Anything, int64(6000)).Return(&entity.CardAuthorization{ID: "pi_1", Secret: "secret_1", Status: entity.CardAuthorizationStatusPending}, nil)
		f.checkouts.EXPECT().SetCardAuthorization(mock.Anything, entity.CheckoutID("checkout-1"), "pi_1").Return(nil)

		secret, err := f.uc.AuthorizeCard(context.Background(), "fan-1", "checkout-1", name, phone)

		require.NoError(t, err)
		assert.Equal(t, "secret_1", secret, "the same card authorization for another attempt")
	})

	t.Run("name corrected on retry", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/checkout/authorize-card "Name corrected on retry"
		f := newCheckoutFixture(t, start.Add(6*time.Minute))
		checkout := fanCheckout(start)
		checkout.Order.CardAuthorizationID = "pi_1"
		f.checkouts.EXPECT().Get(mock.Anything, entity.CheckoutID("checkout-1")).Return(checkout, nil)
		f.stocks.EXPECT().Get(mock.Anything, entity.TicketStockID("stock-1"), mock.Anything).Return(coStock(150, 0, 2), nil)
		const corrected = "佐藤 花子"
		f.users.EXPECT().UpdatePersonalDetails(mock.Anything, "fan-1", corrected, phone).
			Return(&entity.User{ID: "fan-1", FullName: corrected, PhoneNumber: phone}, nil).Once()
		f.auth.EXPECT().Create(mock.Anything, mock.Anything, int64(6000)).Return(&entity.CardAuthorization{ID: "pi_1", Secret: "secret_1", Status: entity.CardAuthorizationStatusPending}, nil)
		f.checkouts.EXPECT().SetCardAuthorization(mock.Anything, entity.CheckoutID("checkout-1"), "pi_1").Return(nil)

		secret, err := f.uc.AuthorizeCard(context.Background(), "fan-1", "checkout-1", corrected, phone)

		require.NoError(t, err)
		assert.Equal(t, "secret_1", secret, "the same card authorization")
	})

	t.Run("domestic-format phone number", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/checkout/authorize-card "Domestic-format phone number"
		f := newCheckoutFixture(t, start.Add(3*time.Minute))
		f.checkouts.EXPECT().Get(mock.Anything, entity.CheckoutID("checkout-1")).Return(fanCheckout(start), nil)

		_, err := f.uc.AuthorizeCard(context.Background(), "fan-1", "checkout-1", name, "090-1234-5678")

		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
		f.users.AssertNotCalled(t, "UpdatePersonalDetails", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
		f.auth.AssertNotCalled(t, "Create", mock.Anything, mock.Anything, mock.Anything)
	})

	t.Run("missing full name", func(t *testing.T) {
		t.Parallel()
		f := newCheckoutFixture(t, start.Add(3*time.Minute))
		f.checkouts.EXPECT().Get(mock.Anything, entity.CheckoutID("checkout-1")).Return(fanCheckout(start), nil)

		_, err := f.uc.AuthorizeCard(context.Background(), "fan-1", "checkout-1", "", phone)

		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
	})

	t.Run("someone else's checkout", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/checkout/authorize-card "Someone else's checkout"
		f := newCheckoutFixture(t, start)
		f.checkouts.EXPECT().Get(mock.Anything, entity.CheckoutID("checkout-1")).Return(fanCheckout(start), nil)

		_, err := f.uc.AuthorizeCard(context.Background(), "fan-2", "checkout-1", name, phone)

		assert.ErrorIs(t, err, apperr.ErrPermissionDenied)
	})

	t.Run("hold lapsed", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/checkout/authorize-card "Hold lapsed"
		f := newCheckoutFixture(t, start.Add(16*time.Minute))
		f.checkouts.EXPECT().Get(mock.Anything, entity.CheckoutID("checkout-1")).Return(fanCheckout(start), nil)

		_, err := f.uc.AuthorizeCard(context.Background(), "fan-1", "checkout-1", name, phone)

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
	})
}

func TestCheckoutUseCase_Expire(t *testing.T) {
	t.Parallel()
	start := time.Date(2026, 11, 5, 18, 0, 0, 0, tsJST)
	expiry := start.Add(15 * time.Minute)
	in := usecase.ExpireRequest{CheckoutID: "checkout-1", HoldExpireTime: expiry}

	t.Run("fan walked away", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/checkout/expire "Fan walked away"
		f := newCheckoutFixture(t, expiry)
		// Release makes the still-Held Checkout Expired and its Order Void,
		// judged at the hold expiry.
		f.checkouts.EXPECT().Release(mock.Anything, entity.CheckoutID("checkout-1"), expiry).Return(true, nil).Once()

		_, err := f.uc.Expire(context.Background(), &inlineFlow{}, in)

		require.NoError(t, err)
	})

	t.Run("committed before the expiry", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/checkout/expire "Committed before the expiry"
		f := newCheckoutFixture(t, expiry)
		f.checkouts.EXPECT().Release(mock.Anything, entity.CheckoutID("checkout-1"), expiry).Return(false, nil).Once()

		_, err := f.uc.Expire(context.Background(), &inlineFlow{}, in)

		require.NoError(t, err, "a committed Checkout is left as it is")
	})

	t.Run("process stopped while waiting", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/checkout/expire "Process stopped while waiting"
		// The expiry waits as the workflow's start delay, which the workflow
		// runtime keeps across a stop; a run resumed after its release
		// returns the recorded step and ends the hold only once.
		f := newCheckoutFixture(t, expiry.Add(5*time.Minute))
		f.checkouts.EXPECT().Release(mock.Anything, entity.CheckoutID("checkout-1"), expiry).Return(true, nil).Once()
		flow := newReplayFlow()

		_, err := f.uc.Expire(context.Background(), flow, in)
		require.NoError(t, err)
		_, err = f.uc.Expire(context.Background(), flow, in)

		require.NoError(t, err)
	})

	t.Run("release unavailable", func(t *testing.T) {
		t.Parallel()
		f := newCheckoutFixture(t, expiry)
		f.checkouts.EXPECT().Release(mock.Anything, entity.CheckoutID("checkout-1"), expiry).Return(false, unavailable).Once()

		_, err := f.uc.Expire(context.Background(), &inlineFlow{}, in)

		assert.ErrorIs(t, err, apperr.ErrUnavailable, "the step is tried again until the deadline")
	})
}

func TestCheckoutUseCase_StartCheckoutWorkflow(t *testing.T) {
	t.Parallel()
	start := time.Date(2026, 11, 5, 18, 0, 0, 0, tsJST)
	expiry := start.Add(15 * time.Minute)

	// startOptions returns the options a Start was given.
	startOptions := func(opts []usecase.StartOption) usecase.StartOptions {
		return usecase.NewStartOptions(opts...)
	}

	t.Run("hold starts its expiry", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/checkout/expire "Fan walked away"
		f := newCheckoutFixture(t, start)
		held := fanCheckout(start)
		f.workflows.EXPECT().Start(mock.Anything, usecase.WorkflowExpireCheckout, "expire:checkout-1",
			usecase.ExpireRequest{CheckoutID: "checkout-1", HoldExpireTime: expiry}, mock.Anything, mock.Anything).
			RunAndReturn(func(_ context.Context, _, _ string, _ any, opts ...usecase.StartOption) error {
				o := startOptions(opts)
				assert.Equal(t, 15*time.Minute, o.Delay, "it waits until the hold expiry")
				assert.Equal(t, expiry.Add(24*time.Hour), o.Deadline)
				return nil
			}).Once()

		require.NoError(t, f.uc.StartCheckoutWorkflow(context.Background(), held))
	})

	t.Run("late announcement expires at once", func(t *testing.T) {
		t.Parallel()
		f := newCheckoutFixture(t, expiry.Add(time.Minute))
		f.workflows.EXPECT().Start(mock.Anything, usecase.WorkflowExpireCheckout, "expire:checkout-1", mock.Anything, mock.Anything, mock.Anything).
			RunAndReturn(func(_ context.Context, _, _ string, _ any, opts ...usecase.StartOption) error {
				assert.Zero(t, startOptions(opts).Delay)
				return nil
			}).Once()

		require.NoError(t, f.uc.StartCheckoutWorkflow(context.Background(), fanCheckout(start)))
	})

	for _, tc := range []struct {
		name, scenario string
		status         entity.CheckoutStatus
	}{
		{"hold expired", "Hold expired", entity.CheckoutStatusExpired},
		{"count changed after authorizing", "Count changed after authorizing", entity.CheckoutStatusReleased},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			// @spec components/usecase/order/release-card-authorization "Hold expired"
			// @spec components/usecase/order/release-card-authorization "Count changed after authorizing"
			f := newCheckoutFixture(t, expiry)
			ended := fanCheckout(start)
			ended.Status = tc.status
			f.workflows.EXPECT().Start(mock.Anything, usecase.WorkflowReleaseCardAuthorization, "release:order-1",
				entity.OrderID("order-1"), mock.Anything, mock.Anything).
				RunAndReturn(func(_ context.Context, _, _ string, _ any, opts ...usecase.StartOption) error {
					o := startOptions(opts)
					assert.Equal(t, usecase.PaymentQueue, o.Queue)
					assert.Equal(t, expiry.Add(24*time.Hour), o.Deadline)
					return nil
				}).Once()

			require.NoError(t, f.uc.StartCheckoutWorkflow(context.Background(), ended), tc.scenario)
		})
	}

	t.Run("repeated delivery", func(t *testing.T) {
		t.Parallel()
		// A redelivered announcement starts the same id, which the workflow
		// runtime starts once.
		f := newCheckoutFixture(t, start)
		var ids []string
		f.workflows.EXPECT().Start(mock.Anything, usecase.WorkflowExpireCheckout, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			RunAndReturn(func(_ context.Context, _, id string, _ any, _ ...usecase.StartOption) error {
				ids = append(ids, id)
				return nil
			}).Twice()

		require.NoError(t, f.uc.StartCheckoutWorkflow(context.Background(), fanCheckout(start)))
		require.NoError(t, f.uc.StartCheckoutWorkflow(context.Background(), fanCheckout(start)))

		assert.Equal(t, []string{"expire:checkout-1", "expire:checkout-1"}, ids)
	})

	t.Run("committed checkout starts nothing", func(t *testing.T) {
		t.Parallel()
		f := newCheckoutFixture(t, start)
		committed := fanCheckout(start)
		committed.Status = entity.CheckoutStatusCommitted

		require.NoError(t, f.uc.StartCheckoutWorkflow(context.Background(), committed))
	})
}
