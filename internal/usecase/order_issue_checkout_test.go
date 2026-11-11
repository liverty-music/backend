package usecase_test

import (
	"context"
	"sync"
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

// checkoutOrderFixture holds the mocks of the Checkout source of Issue and of
// Place. order is the stored Order that Order.Get returns, made Paid by a
// successful Order.Issue.
type checkoutOrderFixture struct {
	issuance              *entitymocks.MockIssuanceRepository
	orders                *entitymocks.MockOrderRepository
	organizers            *entitymocks.MockOrganizerRepository
	checkouts             *entitymocks.MockCheckoutRepository
	sales                 *entitymocks.MockTicketSaleRepository
	stocks                *entitymocks.MockTicketStockRepository
	identityVerifications *entitymocks.MockIdentityVerificationRepository
	events                *ucmocks.MockEventPublishStatePort
	auth                  *entitymocks.MockCardAuthorizationRepository
	workflows             *ucmocks.MockWorkflows
	uc                    usecase.OrderUseCase

	mu    sync.Mutex
	order *entity.Order
}

// newCheckoutOrderFixture builds the fixture of fan-1's authorized Checkout
// "checkout-1" of 2 x 3000 yen, of the FirstCome sale "sale-1" of event-1 with
// the given verification requirement, of an Organizer at 8%.
func newCheckoutOrderFixture(t *testing.T, now time.Time, req ...entity.VerificationRequirement) *checkoutOrderFixture {
	t.Helper()
	f := &checkoutOrderFixture{
		issuance:              entitymocks.NewMockIssuanceRepository(t),
		orders:                entitymocks.NewMockOrderRepository(t),
		organizers:            entitymocks.NewMockOrganizerRepository(t),
		checkouts:             entitymocks.NewMockCheckoutRepository(t),
		sales:                 entitymocks.NewMockTicketSaleRepository(t),
		stocks:                entitymocks.NewMockTicketStockRepository(t),
		identityVerifications: entitymocks.NewMockIdentityVerificationRepository(t),
		events:                ucmocks.NewMockEventPublishStatePort(t),
		auth:                  entitymocks.NewMockCardAuthorizationRepository(t),
		workflows:             ucmocks.NewMockWorkflows(t),
		order:                 authorizedCheckout().Order,
	}
	f.orders.EXPECT().Get(mock.Anything, entity.OrderID("order-1")).RunAndReturn(
		func(context.Context, entity.OrderID) (*entity.Order, error) {
			f.mu.Lock()
			defer f.mu.Unlock()
			o := *f.order
			return &o, nil
		}).Maybe()
	f.stocks.EXPECT().Get(mock.Anything, entity.TicketStockID("stock-1"), mock.Anything).Return(coStock(150, 0, 2), nil).Maybe()
	sale := coSale()
	if len(req) > 0 {
		sale.VerificationRequirement = req[0]
	}
	f.sales.EXPECT().Get(mock.Anything, entity.TicketSaleID("sale-1")).Return(sale, nil).Maybe()
	f.organizers.EXPECT().Get(mock.Anything, "org-1").Return(&entity.Organizer{ID: "org-1", PlatformFeeRateBps: 800}, nil).Maybe()
	f.uc = usecase.NewOrderUseCase(usecase.OrderDeps{
		IssuanceRepo:             f.issuance,
		OrderRepo:                f.orders,
		EventOrganizerRepo:       eventOrganizer(t, "org-1"),
		OrganizerRepo:            f.organizers,
		IdentityVerificationRepo: f.identityVerifications,
		CheckoutRepo:             f.checkouts,
		TicketSaleRepo:           f.sales,
		StockRepo:                f.stocks,
		EventState:               f.events,
		CardAuthorizations:       f.auth,
		Workflows:                f.workflows,
		Clock:                    fixedClock(now),
		Logger:                   newTestLogger(t),
	})
	return f
}

var (
	coStart   = time.Date(2026, 11, 5, 18, 0, 0, 0, tsJST)
	coPayment = &entity.CapturedPayment{Provider: entity.PaymentProviderStripe, ProviderPaymentID: "pi_1", AmountJPY: 6000, Currency: "JPY", CardBrand: "visa", CardLast4: "4242"}
	fan1      = entity.UserID("fan-1")
)

// authorizedCheckout is fan-1's holding, authorized checkout of 2 x 3000.
func authorizedCheckout() *entity.Checkout {
	c := fanCheckout(coStart)
	c.Order.CardAuthorizationID = "pi_1"
	return c
}

// committedCopy returns c Committed (and charged when captured is set).
func committedCopy(r *entity.Checkout, captured bool) *entity.Checkout {
	c := *r
	committedAt := coStart.Add(10 * time.Minute)
	c.Status, c.CommitTime = entity.CheckoutStatusCommitted, &committedAt
	if captured {
		capturedAt := coStart.Add(11 * time.Minute)
		c.CaptureTime = &capturedAt
	}
	return &c
}

// capturesPI1 makes pi_1 read back as captured for 6000 yen.
func (f *checkoutOrderFixture) capturesPI1() {
	f.auth.EXPECT().Get(mock.Anything, "pi_1").Return(&entity.CardAuthorization{
		ID: "pi_1", Amount: 6000, CardBrand: "visa", Status: entity.CardAuthorizationStatusCaptured, CapturedPayment: coPayment,
	}, nil)
}

// recordsIssue makes Order.Issue succeed, storing the Order Paid, and
// returns where it records what was issued.
func (f *checkoutOrderFixture) recordsIssue() (*entity.Order, *[]*entity.Ticket, *entity.Settlement) {
	var tickets []*entity.Ticket
	order, settlement := &entity.Order{}, &entity.Settlement{}
	f.issuance.EXPECT().Issue(mock.Anything, mock.Anything, mock.Anything, mock.Anything).RunAndReturn(
		func(_ context.Context, o *entity.Order, ts []*entity.Ticket, s *entity.Settlement) error {
			f.mu.Lock()
			defer f.mu.Unlock()
			*order, tickets, *settlement = *o, ts, *s
			f.order = o
			return nil
		}).Once()
	return order, &tickets, settlement
}

// issueCheckout runs Issue for checkout-1 with every step at once.
func issueCheckout(uc usecase.OrderUseCase) (usecase.IssueResult, error) {
	return uc.Issue(context.Background(), &inlineFlow{}, usecase.IssueRequest{CheckoutID: "checkout-1"})
}

func TestOrderUseCase_IssueCheckout(t *testing.T) {
	t.Parallel()
	commitAt := coStart.Add(10 * time.Minute)

	t.Run("checkout issued", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/order/issue "Checkout issued"
		// @spec components/usecase/order/issue "No requirement on a first-come sale"
		f := newCheckoutOrderFixture(t, commitAt)
		f.checkouts.EXPECT().Get(mock.Anything, entity.CheckoutID("checkout-1")).Return(authorizedCheckout(), nil).Once()
		f.checkouts.EXPECT().Commit(mock.Anything, entity.CheckoutID("checkout-1"), commitAt).Return(entity.CommitOutcomeCommitted, nil).Once()
		f.checkouts.EXPECT().Get(mock.Anything, entity.CheckoutID("checkout-1")).Return(committedCopy(authorizedCheckout(), false), nil).Once()
		f.auth.EXPECT().Capture(mock.Anything, "pi_1").Return(nil).Once()
		f.checkouts.EXPECT().RecordCapture(mock.Anything, entity.CheckoutID("checkout-1"), commitAt).Return(nil).Once()
		f.capturesPI1()
		order, tickets, settlement := f.recordsIssue()

		got, err := issueCheckout(f.uc)

		require.NoError(t, err)
		assert.Equal(t, usecase.IssueResult{OrderID: "order-1", Paid: true}, got)
		assert.Equal(t, entity.OrderStatusPaid, order.Status)
		assert.Equal(t, entity.CheckoutID("checkout-1"), order.CheckoutID)
		assert.Equal(t, int64(6000), order.Amount)
		assert.Equal(t, "pi_1", order.Payment.ProviderPaymentID)
		assert.Equal(t, "visa", order.Payment.CardBrand)
		assert.Equal(t, "4242", order.Payment.CardLast4)
		require.NotNil(t, order.PayTime)
		assert.Equal(t, commitAt, *order.PayTime)
		require.Len(t, *tickets, 2)
		for _, tk := range *tickets {
			assert.Equal(t, fan1, tk.UserID)
			assert.Equal(t, "event-1", tk.EventID)
			assert.Equal(t, entity.TicketStatusIssued, tk.Status)
			assert.Empty(t, tk.IdentityVerificationID, "the sale requires no verification")
		}
		assert.Equal(t, entity.SettlementStatusHeld, settlement.Status)
		require.Len(t, settlement.Splits, 1)
		assert.Equal(t, int64(5520), settlement.Splits[0].Amount, "6000 yen at 8%")
	})

	t.Run("hold already over", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/order/issue "Hold already over"
		f := newCheckoutOrderFixture(t, coStart.Add(15*time.Minute))
		f.checkouts.EXPECT().Get(mock.Anything, entity.CheckoutID("checkout-1")).Return(authorizedCheckout(), nil).Once()
		// The commit is judged at the time it runs, after the hold expiry.
		f.checkouts.EXPECT().Commit(mock.Anything, entity.CheckoutID("checkout-1"), coStart.Add(15*time.Minute)).
			Return(entity.CommitOutcomeNotHeld, nil).Once()

		got, err := issueCheckout(f.uc)

		require.NoError(t, err)
		assert.Equal(t, usecase.IssueResult{OrderID: "order-1"}, got, "nothing is charged and the Order is not Paid")
	})

	t.Run("card no longer chargeable", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/order/issue "Card no longer chargeable"
		f := newCheckoutOrderFixture(t, commitAt)
		f.checkouts.EXPECT().Get(mock.Anything, entity.CheckoutID("checkout-1")).Return(authorizedCheckout(), nil).Once()
		f.checkouts.EXPECT().Commit(mock.Anything, entity.CheckoutID("checkout-1"), commitAt).Return(entity.CommitOutcomeCommitted, nil).Once()
		f.checkouts.EXPECT().Get(mock.Anything, entity.CheckoutID("checkout-1")).Return(committedCopy(authorizedCheckout(), false), nil).Once()
		f.auth.EXPECT().Capture(mock.Anything, "pi_1").Return(apperr.New(codes.FailedPrecondition, "card closed")).Once()
		// RevertCommit makes the Checkout Released and its Order Void, so the
		// tickets go back on sale.
		f.checkouts.EXPECT().RevertCommit(mock.Anything, entity.CheckoutID("checkout-1")).Return(nil).Once()

		got, err := issueCheckout(f.uc)

		require.NoError(t, err)
		assert.Equal(t, usecase.IssueResult{OrderID: "order-1"}, got)
	})

	t.Run("payment provider unavailable during capture", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/order/issue "Payment provider unavailable during capture"
		f := newCheckoutOrderFixture(t, commitAt)
		f.checkouts.EXPECT().Get(mock.Anything, entity.CheckoutID("checkout-1")).Return(authorizedCheckout(), nil).Once()
		f.checkouts.EXPECT().Commit(mock.Anything, entity.CheckoutID("checkout-1"), commitAt).Return(entity.CommitOutcomeCommitted, nil).Once()
		f.checkouts.EXPECT().Get(mock.Anything, entity.CheckoutID("checkout-1")).Return(committedCopy(authorizedCheckout(), false), nil).Once()
		f.auth.EXPECT().Capture(mock.Anything, "pi_1").Return(unavailable).Once()
		f.auth.EXPECT().Capture(mock.Anything, "pi_1").Return(nil).Once()
		f.checkouts.EXPECT().RecordCapture(mock.Anything, entity.CheckoutID("checkout-1"), commitAt).Return(nil).Once()
		f.capturesPI1()
		f.recordsIssue()
		flow := newReplayFlow()
		in := usecase.IssueRequest{CheckoutID: "checkout-1"}

		// The failed step is tried again by the runtime; the commit is not.
		_, err := f.uc.Issue(context.Background(), flow, in)
		require.ErrorIs(t, err, apperr.ErrUnavailable, "the Checkout stays Committed")
		got, err := f.uc.Issue(context.Background(), flow, in)

		require.NoError(t, err)
		assert.True(t, got.Paid, "once the provider answers the Order becomes Paid")
	})

	t.Run("charged but never issued", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/order/issue "Charged but never issued"
		f := newCheckoutOrderFixture(t, commitAt)
		f.checkouts.EXPECT().Get(mock.Anything, entity.CheckoutID("checkout-1")).Return(authorizedCheckout(), nil).Once()
		f.checkouts.EXPECT().Commit(mock.Anything, entity.CheckoutID("checkout-1"), commitAt).Return(entity.CommitOutcomeCommitted, nil).Once()
		f.checkouts.EXPECT().Get(mock.Anything, entity.CheckoutID("checkout-1")).Return(committedCopy(authorizedCheckout(), false), nil).Once()
		f.auth.EXPECT().Capture(mock.Anything, "pi_1").Return(nil).Once()
		f.checkouts.EXPECT().RecordCapture(mock.Anything, entity.CheckoutID("checkout-1"), commitAt).Return(nil).Once()
		f.capturesPI1()
		f.issuance.EXPECT().Issue(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(unavailable)

		_, err := issueCheckout(f.uc)

		// Tried again until the 24-hour deadline, then reported to an admin;
		// the commit is never reverted and the Order stays Pending.
		require.ErrorIs(t, err, apperr.ErrUnavailable)
		assert.Equal(t, entity.OrderStatusPending, f.order.Status)
	})

	t.Run("checkout charged before", func(t *testing.T) {
		t.Parallel()
		f := newCheckoutOrderFixture(t, commitAt)
		f.checkouts.EXPECT().Get(mock.Anything, entity.CheckoutID("checkout-1")).Return(committedCopy(authorizedCheckout(), true), nil)
		f.checkouts.EXPECT().Commit(mock.Anything, entity.CheckoutID("checkout-1"), commitAt).Return(entity.CommitOutcomeCommitted, nil).Once()
		f.capturesPI1()
		f.recordsIssue()

		got, err := issueCheckout(f.uc)

		require.NoError(t, err)
		assert.True(t, got.Paid, "issued without charging the card again")
	})

	t.Run("order already paid", func(t *testing.T) {
		t.Parallel()
		f := newCheckoutOrderFixture(t, commitAt)
		f.order.Status = entity.OrderStatusPaid
		f.checkouts.EXPECT().Get(mock.Anything, entity.CheckoutID("checkout-1")).Return(authorizedCheckout(), nil).Once()

		got, err := issueCheckout(f.uc)

		require.NoError(t, err)
		assert.Equal(t, usecase.IssueResult{OrderID: "order-1", Paid: true}, got)
	})

	t.Run("verified sale issued", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/order/issue "Verified sale issued"
		f := newCheckoutOrderFixture(t, commitAt, entity.VerificationRequirementVerifiedAny)
		f.identityVerifications.EXPECT().GetByUserID(mock.Anything, "fan-1").
			Return(&entity.IdentityVerification{ID: "iv-1", UserID: "fan-1", Status: entity.VerificationStatusNeedsReverification}, nil)
		f.checkouts.EXPECT().Get(mock.Anything, entity.CheckoutID("checkout-1")).Return(authorizedCheckout(), nil).Once()
		f.checkouts.EXPECT().Commit(mock.Anything, entity.CheckoutID("checkout-1"), commitAt).Return(entity.CommitOutcomeCommitted, nil).Once()
		f.checkouts.EXPECT().Get(mock.Anything, entity.CheckoutID("checkout-1")).Return(committedCopy(authorizedCheckout(), false), nil).Once()
		f.auth.EXPECT().Capture(mock.Anything, "pi_1").Return(nil).Once()
		f.checkouts.EXPECT().RecordCapture(mock.Anything, entity.CheckoutID("checkout-1"), commitAt).Return(nil).Once()
		f.capturesPI1()
		_, tickets, _ := f.recordsIssue()

		_, err := issueCheckout(f.uc)

		require.NoError(t, err)
		require.Len(t, *tickets, 2)
		for _, tk := range *tickets {
			assert.Equal(t, "iv-1", tk.IdentityVerificationID, "bound whatever the IdentityVerification's status")
		}
	})

	t.Run("no identity verification", func(t *testing.T) {
		t.Parallel()
		f := newCheckoutOrderFixture(t, commitAt, entity.VerificationRequirementVerifiedAny)
		f.identityVerifications.EXPECT().GetByUserID(mock.Anything, "fan-1").Return(nil, apperr.New(codes.NotFound, "none"))
		f.checkouts.EXPECT().Get(mock.Anything, entity.CheckoutID("checkout-1")).Return(authorizedCheckout(), nil).Once()

		_, err := issueCheckout(f.uc)

		assert.ErrorIs(t, err, apperr.ErrNotFound, "nothing is committed or charged")
	})

	t.Run("no source or both", func(t *testing.T) {
		t.Parallel()
		f := newCheckoutOrderFixture(t, commitAt)
		for _, in := range []usecase.IssueRequest{{}, {CheckoutID: "checkout-1", LotteryEntryID: "entry-1"}} {
			_, err := f.uc.Issue(context.Background(), &inlineFlow{}, in)
			assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
		}
	})
}

// expectIssueRun makes Workflows start the issue of order-1 for checkout-1 on
// the checkout queue and end it with result, made Paid in the stored Order
// when it says so; awaitErr instead fails the wait.
func (f *checkoutOrderFixture) expectIssueRun(t *testing.T, now time.Time, result usecase.IssueResult, awaitErr error) {
	t.Helper()
	f.workflows.EXPECT().Start(mock.Anything, usecase.WorkflowIssue, "issue:order-1", usecase.IssueRequest{CheckoutID: "checkout-1"}, mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, _, _ string, _ any, opts ...usecase.StartOption) error {
			o := usecase.NewStartOptions(opts...)
			assert.Equal(t, usecase.CheckoutQueue, o.Queue)
			assert.Equal(t, now.Add(24*time.Hour), o.Deadline)
			return nil
		}).Once()
	f.workflows.EXPECT().Await(mock.Anything, "issue:order-1", 30*time.Second, mock.Anything).
		RunAndReturn(func(_ context.Context, _ string, _ time.Duration, out any) error {
			if awaitErr != nil {
				return awaitErr
			}
			*out.(*usecase.IssueResult) = result
			if result.Paid {
				f.mu.Lock()
				f.order.Status = entity.OrderStatusPaid
				f.mu.Unlock()
			}
			return nil
		}).Once()
}

// authorizesAs makes the next read of pi_1 report ca.
func (f *checkoutOrderFixture) authorizesAs(ca *entity.CardAuthorization) {
	f.auth.EXPECT().Get(mock.Anything, "pi_1").Return(ca, nil).Once()
}

// authorizedPI is pi_1 authorized for the checkout's 6000 yen.
func authorizedPI() *entity.CardAuthorization {
	return &entity.CardAuthorization{ID: "pi_1", Amount: 6000, CardBrand: "visa", Status: entity.CardAuthorizationStatusAuthorized}
}

// place runs Place for checkout-1 as fan-1 at now.
func place(f *checkoutOrderFixture, now time.Time) (*entity.Order, error) {
	return f.uc.Place(context.Background(), "checkout-1", fan1, now)
}

func TestOrderUseCase_Place(t *testing.T) {
	t.Parallel()
	placeAt := coStart.Add(10 * time.Minute)
	paid := usecase.IssueResult{OrderID: "order-1", Paid: true}

	// placeable expects the reads of a placeable Held Checkout.
	placeable := func(f *checkoutOrderFixture) {
		f.checkouts.EXPECT().Get(mock.Anything, entity.CheckoutID("checkout-1")).Return(authorizedCheckout(), nil)
		f.events.EXPECT().IsEventPublished(mock.Anything, "event-1").Return(true, nil)
	}

	t.Run("fan places the order within the hold", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/order/place "Fan places the order within the hold"
		f := newCheckoutOrderFixture(t, placeAt)
		placeable(f)
		f.authorizesAs(authorizedPI())
		f.expectIssueRun(t, placeAt, paid, nil)

		order, err := place(f, placeAt)

		require.NoError(t, err)
		assert.Equal(t, entity.OrderID("order-1"), order.ID)
		assert.Equal(t, entity.OrderStatusPaid, order.Status)
	})

	t.Run("hold lapsed before placing", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/order/place "Hold lapsed before placing"
		f := newCheckoutOrderFixture(t, coStart.Add(16*time.Minute))
		f.checkouts.EXPECT().Get(mock.Anything, entity.CheckoutID("checkout-1")).Return(authorizedCheckout(), nil)

		// No card authorization is read and no workflow starts (strict mocks).
		_, err := place(f, coStart.Add(16*time.Minute))

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
	})

	t.Run("checkout already expired", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/order/place "Checkout already expired"
		f := newCheckoutOrderFixture(t, placeAt)
		expired := authorizedCheckout()
		expired.Status = entity.CheckoutStatusExpired
		f.checkouts.EXPECT().Get(mock.Anything, entity.CheckoutID("checkout-1")).Return(expired, nil)

		_, err := place(f, placeAt)

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition, "Issue does not run")
	})

	t.Run("concert cancelled during the checkout", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/order/place "Concert cancelled during the checkout"
		f := newCheckoutOrderFixture(t, placeAt)
		f.checkouts.EXPECT().Get(mock.Anything, entity.CheckoutID("checkout-1")).Return(authorizedCheckout(), nil)
		f.events.EXPECT().IsEventPublished(mock.Anything, "event-1").Return(false, nil)

		_, err := place(f, placeAt)

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
	})

	t.Run("card not yet authenticated", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/order/place "Card not yet authenticated"
		f := newCheckoutOrderFixture(t, placeAt)
		placeable(f)
		f.authorizesAs(&entity.CardAuthorization{ID: "pi_1", Amount: 6000, Status: entity.CardAuthorizationStatusPending})
		f.authorizesAs(authorizedPI())
		f.expectIssueRun(t, placeAt, paid, nil)

		_, err := place(f, placeAt)
		require.ErrorIs(t, err, apperr.ErrFailedPrecondition, "the Checkout stays Held; no workflow starts")
		order, err := place(f, placeAt)

		require.NoError(t, err)
		assert.Equal(t, entity.OrderStatusPaid, order.Status)
	})

	t.Run("amount differs", func(t *testing.T) {
		t.Parallel()
		f := newCheckoutOrderFixture(t, placeAt)
		placeable(f)
		f.authorizesAs(&entity.CardAuthorization{ID: "pi_1", Amount: 9000, CardBrand: "visa", Status: entity.CardAuthorizationStatusAuthorized})

		_, err := place(f, placeAt)

		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
	})

	t.Run("no card authorization", func(t *testing.T) {
		t.Parallel()
		f := newCheckoutOrderFixture(t, placeAt)
		f.order.CardAuthorizationID = ""
		f.checkouts.EXPECT().Get(mock.Anything, entity.CheckoutID("checkout-1")).Return(fanCheckout(coStart), nil)
		f.events.EXPECT().IsEventPublished(mock.Anything, "event-1").Return(true, nil)

		_, err := place(f, placeAt)

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
	})

	t.Run("identity verification gone before placing", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/order/place "IdentityVerification gone before placing"
		f := newCheckoutOrderFixture(t, placeAt, entity.VerificationRequirementVerifiedAny)
		placeable(f)
		f.authorizesAs(authorizedPI())
		f.identityVerifications.EXPECT().GetByUserID(mock.Anything, "fan-1").Return(nil, apperr.New(codes.NotFound, "none"))

		_, err := place(f, placeAt)

		assert.ErrorIs(t, err, apperr.ErrNotFound, "nothing is charged and the Order stays Pending")
	})

	t.Run("double tap on the action", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/order/place "Double tap on the action"
		f := newCheckoutOrderFixture(t, placeAt)
		placeable(f)
		f.auth.EXPECT().Get(mock.Anything, "pi_1").Return(authorizedPI(), nil).Twice()
		// Both calls start the same id, which the workflow runtime runs once,
		// and both wait for its outcome: the run ends once both wait.
		f.workflows.EXPECT().Start(mock.Anything, usecase.WorkflowIssue, "issue:order-1", usecase.IssueRequest{CheckoutID: "checkout-1"}, mock.Anything, mock.Anything).
			Return(nil).Twice()
		var waiting sync.WaitGroup
		waiting.Add(2)
		f.workflows.EXPECT().Await(mock.Anything, "issue:order-1", 30*time.Second, mock.Anything).
			RunAndReturn(func(_ context.Context, _ string, _ time.Duration, out any) error {
				waiting.Done()
				waiting.Wait()
				*out.(*usecase.IssueResult) = paid
				f.mu.Lock()
				f.order.Status = entity.OrderStatusPaid
				f.mu.Unlock()
				return nil
			}).Twice()

		var wg sync.WaitGroup
		orders := make([]*entity.Order, 2)
		errs := make([]error, 2)
		for i := range 2 {
			wg.Go(func() { orders[i], errs[i] = place(f, placeAt) })
		}
		wg.Wait()

		require.NoError(t, errs[0])
		require.NoError(t, errs[1])
		assert.Equal(t, orders[0].ID, orders[1].ID)
	})

	t.Run("committed checkout waits for its run", func(t *testing.T) {
		t.Parallel()
		f := newCheckoutOrderFixture(t, placeAt)
		f.checkouts.EXPECT().Get(mock.Anything, entity.CheckoutID("checkout-1")).Return(committedCopy(authorizedCheckout(), false), nil)
		f.expectIssueRun(t, placeAt, paid, nil)

		order, err := place(f, placeAt)

		require.NoError(t, err)
		assert.Equal(t, entity.OrderStatusPaid, order.Status)
	})

	t.Run("someone else's checkout", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/order/place "Someone else's checkout"
		for _, status := range []entity.OrderStatus{entity.OrderStatusPending, entity.OrderStatusPaid} {
			f := newCheckoutOrderFixture(t, placeAt)
			f.order.Status = status
			f.checkouts.EXPECT().Get(mock.Anything, entity.CheckoutID("checkout-1")).Return(authorizedCheckout(), nil)

			order, err := f.uc.Place(context.Background(), "checkout-1", "fan-2", placeAt)

			assert.ErrorIs(t, err, apperr.ErrPermissionDenied, status.String())
			assert.Nil(t, order)
		}

		f := newCheckoutOrderFixture(t, placeAt)
		f.checkouts.EXPECT().Get(mock.Anything, entity.CheckoutID("checkout-1")).Return(nil, apperr.New(codes.NotFound, "no checkout"))
		_, err := place(f, placeAt)
		assert.ErrorIs(t, err, apperr.ErrPermissionDenied, "a missing Checkout is not revealed")
	})

	t.Run("order already paid", func(t *testing.T) {
		t.Parallel()
		f := newCheckoutOrderFixture(t, placeAt)
		f.order.Status = entity.OrderStatusPaid
		completed := committedCopy(authorizedCheckout(), true)
		completed.Status = entity.CheckoutStatusCompleted
		f.checkouts.EXPECT().Get(mock.Anything, entity.CheckoutID("checkout-1")).Return(completed, nil)

		order, err := place(f, placeAt)

		require.NoError(t, err)
		assert.Equal(t, entity.OrderStatusPaid, order.Status)
	})

	t.Run("card no longer chargeable", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/order/place "Card no longer chargeable"
		f := newCheckoutOrderFixture(t, placeAt)
		placeable(f)
		f.authorizesAs(authorizedPI())
		f.expectIssueRun(t, placeAt, usecase.IssueResult{OrderID: "order-1"}, nil)

		_, err := place(f, placeAt)

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
	})

	t.Run("charge still completing", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/order/place "Charge still completing"
		f := newCheckoutOrderFixture(t, placeAt)
		placeable(f)
		f.authorizesAs(authorizedPI())
		f.expectIssueRun(t, placeAt, usecase.IssueResult{}, apperr.New(codes.DeadlineExceeded, "still running"))

		_, err := place(f, placeAt)

		// Issue goes on in the worker and makes the Order Paid once the
		// provider answers (components/usecase/order/issue).
		assert.ErrorIs(t, err, apperr.ErrUnavailable)
	})
}
