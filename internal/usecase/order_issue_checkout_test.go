package usecase_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	entitymocks "github.com/liverty-music/backend/internal/entity/mocks"
	"github.com/liverty-music/backend/internal/usecase"
	ucmocks "github.com/liverty-music/backend/internal/usecase/mocks"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// checkoutIssuanceFixture holds the mocks of IssueFromCheckout under test.
type checkoutIssuanceFixture struct {
	issuance              *entitymocks.MockIssuanceRepository
	orders                *entitymocks.MockOrderRepository
	organizers            *entitymocks.MockOrganizerRepository
	checkouts             *entitymocks.MockCheckoutRepository
	sales                 *entitymocks.MockTicketSaleRepository
	identityVerifications *entitymocks.MockIdentityVerificationRepository
	events                *ucmocks.MockEventPublishStatePort
	auth                  *entitymocks.MockCardAuthorizationRepository
	entries               *entitymocks.MockLotteryEntryRepository
	publisher             *ucmocks.MockEventPublisher
	uc                    usecase.OrderUseCase
}

// newCheckoutIssuanceFixture builds the fixture whose ticket type "tt-1"
// belongs to a FirstCome sale with the given verification requirement.
func newCheckoutIssuanceFixture(t *testing.T, now time.Time, req ...entity.VerificationRequirement) *checkoutIssuanceFixture {
	t.Helper()
	f := &checkoutIssuanceFixture{
		issuance:              entitymocks.NewMockIssuanceRepository(t),
		orders:                entitymocks.NewMockOrderRepository(t),
		organizers:            entitymocks.NewMockOrganizerRepository(t),
		checkouts:             entitymocks.NewMockCheckoutRepository(t),
		sales:                 entitymocks.NewMockTicketSaleRepository(t),
		identityVerifications: entitymocks.NewMockIdentityVerificationRepository(t),
		events:                ucmocks.NewMockEventPublishStatePort(t),
		auth:                  entitymocks.NewMockCardAuthorizationRepository(t),
		entries:               entitymocks.NewMockLotteryEntryRepository(t),
		publisher:             ucmocks.NewMockEventPublisher(t),
	}
	// The charge's facets are read back from the provider at issuance.
	f.auth.EXPECT().Get(mock.Anything, "pi_1").Return(&entity.CardAuthorization{
		ID: "pi_1", Amount: 6000, CardBrand: "visa", Status: entity.CardAuthorizationStatusCaptured, CapturedPayment: coPayment,
	}, nil).Maybe()
	// Serialize runs the call under the lock.
	f.checkouts.EXPECT().Serialize(mock.Anything, mock.Anything, mock.Anything).RunAndReturn(
		func(ctx context.Context, _ entity.CheckoutID, fn func(context.Context) error) error {
			return fn(ctx)
		}).Maybe()
	f.organizers.EXPECT().Get(mock.Anything, "org-1").Return(&entity.Organizer{ID: "org-1", PlatformFeeRateBps: 800}, nil).Maybe()
	sale, tt := firstComeTicketType(150, 0, 2)
	if len(req) > 0 {
		sale.VerificationRequirement = req[0]
	}
	f.sales.EXPECT().GetTicketType(mock.Anything, entity.TicketTypeID("tt-1"), mock.Anything).Return(sale, tt, nil).Maybe()
	f.uc = usecase.NewOrderUseCase(usecase.OrderDeps{
		IssuanceRepo:             f.issuance,
		OrderRepo:                f.orders,
		EventOrganizerRepo:       &stubEventOrganizerRepo{getOrganizerIDFn: func(context.Context, string) (string, error) { return "org-1", nil }},
		OrganizerRepo:            f.organizers,
		IdentityVerificationRepo: f.identityVerifications,
		CheckoutRepo:             f.checkouts,
		TicketSaleRepo:           f.sales,
		EventState:               f.events,
		CardAuthorizations:       f.auth,
		EntryRepo:                f.entries,
		Publisher:                f.publisher,
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

// authorizesAs makes the next Get of pi_1 report the card authorization as
// ca, ahead of the fixture's captured default (testify matches expectations
// in registration order).
func (f *checkoutIssuanceFixture) authorizesAs(ca *entity.CardAuthorization) {
	call := f.auth.EXPECT().Get(mock.Anything, "pi_1").Return(ca, nil).Once()
	calls := f.auth.ExpectedCalls
	f.auth.ExpectedCalls = append([]*mock.Call{call}, calls[:len(calls)-1]...)
}

// authorizedPI is pi_1 authorized for the checkout's 6000 yen.
func authorizedPI() *entity.CardAuthorization {
	return &entity.CardAuthorization{ID: "pi_1", Amount: 6000, CardBrand: "visa", Status: entity.CardAuthorizationStatusAuthorized}
}

// authorizedCheckout is fan-1's holding, authorized checkout of 2 x 3000.
func authorizedCheckout() *entity.Checkout {
	c := fanCheckout(coStart)
	c.CardAuthorizationID = "pi_1"
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

func TestOrderUseCase_IssueFromCheckout(t *testing.T) {
	t.Parallel()
	placeAt := coStart.Add(10 * time.Minute)

	t.Run("fan places the order within the hold", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/order/issue-from-checkout "Fan places the order within the hold"
		// @spec components/usecase/order/issue-from-checkout "Checkout issued"
		// @spec components/usecase/order/issue-from-checkout "No requirement on a first-come sale"
		f := newCheckoutIssuanceFixture(t, placeAt)
		held := authorizedCheckout()
		f.checkouts.EXPECT().Get(mock.Anything, entity.CheckoutID("checkout-1")).Return(held, nil).Once()
		f.orders.EXPECT().GetByCheckoutID(mock.Anything, entity.CheckoutID("checkout-1")).Return(nil, apperr.ErrNotFound)
		f.events.EXPECT().IsEventPublished(mock.Anything, "event-1").Return(true, nil)
		f.authorizesAs(authorizedPI())
		f.checkouts.EXPECT().Commit(mock.Anything, entity.CheckoutID("checkout-1"), placeAt).Return(entity.CommitOutcomeCommitted, nil)
		f.checkouts.EXPECT().Get(mock.Anything, entity.CheckoutID("checkout-1")).Return(committedCopy(held, false), nil).Once()
		f.auth.EXPECT().Capture(mock.Anything, "pi_1").Return(nil).Once()
		f.checkouts.EXPECT().RecordCapture(mock.Anything, entity.CheckoutID("checkout-1"), placeAt).Return(nil)

		var gotOrder *entity.Order
		var gotTickets []*entity.Ticket
		var gotSettlement *entity.Settlement
		f.issuance.EXPECT().Issue(mock.Anything, mock.Anything, mock.Anything, mock.Anything).RunAndReturn(
			func(_ context.Context, o *entity.Order, ts []*entity.Ticket, s *entity.Settlement) error {
				gotOrder, gotTickets, gotSettlement = o, ts, s
				return nil
			})

		order, err := f.uc.IssueFromCheckout(context.Background(), "checkout-1", &fan1)

		require.NoError(t, err)
		assert.Same(t, gotOrder, order)
		assert.Equal(t, entity.OrderStatusPaid, order.Status)
		assert.Equal(t, entity.CheckoutID("checkout-1"), order.CheckoutID)
		assert.Empty(t, order.LotteryEntryID)
		assert.Equal(t, int64(6000), order.Amount)
		assert.Equal(t, "pi_1", order.Payment.ProviderPaymentID)
		assert.Equal(t, "4242", order.Payment.CardLast4)
		require.Len(t, gotTickets, 2)
		for _, tk := range gotTickets {
			assert.Equal(t, fan1, tk.UserID)
			assert.Equal(t, "event-1", tk.EventID)
			assert.Empty(t, tk.IdentityVerificationID, "the sale requires no verification")
			assert.Equal(t, entity.TicketStatusIssued, tk.Status)
		}
		require.Len(t, gotSettlement.Splits, 1)
		assert.Equal(t, int64(5520), gotSettlement.Splits[0].Amount, "6000 yen at 8%")
	})

	t.Run("hold lapsed before placing", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/order/issue-from-checkout "Hold lapsed before placing"
		f := newCheckoutIssuanceFixture(t, coStart.Add(16*time.Minute))
		f.checkouts.EXPECT().Get(mock.Anything, entity.CheckoutID("checkout-1")).Return(authorizedCheckout(), nil)
		f.orders.EXPECT().GetByCheckoutID(mock.Anything, entity.CheckoutID("checkout-1")).Return(nil, apperr.ErrNotFound)

		_, err := f.uc.IssueFromCheckout(context.Background(), "checkout-1", &fan1)

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
	})

	t.Run("concert cancelled during the checkout", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/order/issue-from-checkout "Concert cancelled during the checkout"
		f := newCheckoutIssuanceFixture(t, placeAt)
		f.checkouts.EXPECT().Get(mock.Anything, entity.CheckoutID("checkout-1")).Return(authorizedCheckout(), nil)
		f.orders.EXPECT().GetByCheckoutID(mock.Anything, entity.CheckoutID("checkout-1")).Return(nil, apperr.ErrNotFound)
		f.events.EXPECT().IsEventPublished(mock.Anything, "event-1").Return(false, nil)

		_, err := f.uc.IssueFromCheckout(context.Background(), "checkout-1", &fan1)

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
	})

	t.Run("card not yet authenticated", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/order/issue-from-checkout "Card not yet authenticated"
		f := newCheckoutIssuanceFixture(t, placeAt)
		f.checkouts.EXPECT().Get(mock.Anything, entity.CheckoutID("checkout-1")).Return(authorizedCheckout(), nil)
		f.orders.EXPECT().GetByCheckoutID(mock.Anything, entity.CheckoutID("checkout-1")).Return(nil, apperr.ErrNotFound)
		f.events.EXPECT().IsEventPublished(mock.Anything, "event-1").Return(true, nil)
		f.authorizesAs(&entity.CardAuthorization{ID: "pi_1", Amount: 6000, Status: entity.CardAuthorizationStatusPending})

		_, err := f.uc.IssueFromCheckout(context.Background(), "checkout-1", &fan1)

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
	})

	t.Run("someone else's checkout", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/order/issue-from-checkout "Someone else's checkout"
		f := newCheckoutIssuanceFixture(t, placeAt)
		paid := committedCopy(authorizedCheckout(), true)
		paid.Status = entity.CheckoutStatusCompleted
		f.checkouts.EXPECT().Get(mock.Anything, entity.CheckoutID("checkout-1")).Return(paid, nil)
		other := entity.UserID("fan-2")

		order, err := f.uc.IssueFromCheckout(context.Background(), "checkout-1", &other)

		assert.ErrorIs(t, err, apperr.ErrPermissionDenied)
		assert.Nil(t, order)
	})

	t.Run("existing order is returned", func(t *testing.T) {
		t.Parallel()
		f := newCheckoutIssuanceFixture(t, placeAt)
		paid := committedCopy(authorizedCheckout(), true)
		paid.Status = entity.CheckoutStatusCompleted
		f.checkouts.EXPECT().Get(mock.Anything, entity.CheckoutID("checkout-1")).Return(paid, nil)
		existing := &entity.Order{ID: "order-1", CheckoutID: "checkout-1"}
		f.orders.EXPECT().GetByCheckoutID(mock.Anything, entity.CheckoutID("checkout-1")).Return(existing, nil)

		order, err := f.uc.IssueFromCheckout(context.Background(), "checkout-1", &fan1)

		require.NoError(t, err)
		assert.Same(t, existing, order)
	})

	t.Run("card no longer chargeable", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/order/issue-from-checkout "Card no longer chargeable"
		f := newCheckoutIssuanceFixture(t, placeAt)
		committed := committedCopy(authorizedCheckout(), false)
		f.checkouts.EXPECT().Get(mock.Anything, entity.CheckoutID("checkout-1")).Return(committed, nil)
		f.orders.EXPECT().GetByCheckoutID(mock.Anything, entity.CheckoutID("checkout-1")).Return(nil, apperr.ErrNotFound)
		f.auth.EXPECT().Capture(mock.Anything, "pi_1").
			Return(apperr.New(apperr.ErrFailedPrecondition.Code, "the card hold was released"))
		f.checkouts.EXPECT().RevertCommit(mock.Anything, entity.CheckoutID("checkout-1")).Return(nil)

		_, err := f.uc.IssueFromCheckout(context.Background(), "checkout-1", nil)

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
	})

	t.Run("card payments unavailable during capture", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/order/issue-from-checkout "Payment provider unavailable during capture"
		f := newCheckoutIssuanceFixture(t, placeAt)
		committed := committedCopy(authorizedCheckout(), false)
		f.checkouts.EXPECT().Get(mock.Anything, entity.CheckoutID("checkout-1")).Return(committed, nil)
		f.orders.EXPECT().GetByCheckoutID(mock.Anything, entity.CheckoutID("checkout-1")).Return(nil, apperr.ErrNotFound)
		f.auth.EXPECT().Capture(mock.Anything, "pi_1").
			Return(apperr.New(apperr.ErrUnavailable.Code, "outcome not known yet"))

		_, err := f.uc.IssueFromCheckout(context.Background(), "checkout-1", nil)

		assert.ErrorIs(t, err, apperr.ErrUnavailable, "left Committed, no revert, no capture record")
	})

	t.Run("issuance fails after the charge", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/order/issue-from-checkout "Issuance fails after the charge"
		f := newCheckoutIssuanceFixture(t, placeAt)
		charged := committedCopy(authorizedCheckout(), true)
		f.checkouts.EXPECT().Get(mock.Anything, entity.CheckoutID("checkout-1")).Return(charged, nil)
		f.orders.EXPECT().GetByCheckoutID(mock.Anything, entity.CheckoutID("checkout-1")).Return(nil, apperr.ErrNotFound)
		f.issuance.EXPECT().Issue(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(apperr.New(apperr.ErrUnavailable.Code, "db down")).Once()

		_, err := f.uc.IssueFromCheckout(context.Background(), "checkout-1", nil)
		assert.ErrorIs(t, err, apperr.ErrUnavailable)

		// A later run issues the Order without charging again (no Capture call).
		f.issuance.EXPECT().Issue(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()
		order, err := f.uc.IssueFromCheckout(context.Background(), "checkout-1", nil)
		require.NoError(t, err)
		assert.Equal(t, "pi_1", order.Payment.ProviderPaymentID)
	})

	t.Run("concurrent issuance returns the first order", func(t *testing.T) {
		t.Parallel()
		f := newCheckoutIssuanceFixture(t, placeAt)
		charged := committedCopy(authorizedCheckout(), true)
		f.checkouts.EXPECT().Get(mock.Anything, entity.CheckoutID("checkout-1")).Return(charged, nil)
		winner := &entity.Order{ID: "order-1", CheckoutID: "checkout-1"}
		f.orders.EXPECT().GetByCheckoutID(mock.Anything, entity.CheckoutID("checkout-1")).Return(nil, apperr.ErrNotFound).Once()
		f.issuance.EXPECT().Issue(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(apperr.ErrAlreadyExists)
		f.orders.EXPECT().GetByCheckoutID(mock.Anything, entity.CheckoutID("checkout-1")).Return(winner, nil).Once()

		order, err := f.uc.IssueFromCheckout(context.Background(), "checkout-1", &fan1)

		require.NoError(t, err)
		assert.Same(t, winner, order)
	})

	t.Run("hold released before the commit", func(t *testing.T) {
		t.Parallel()
		f := newCheckoutIssuanceFixture(t, placeAt)
		f.checkouts.EXPECT().Get(mock.Anything, entity.CheckoutID("checkout-1")).Return(authorizedCheckout(), nil)
		f.orders.EXPECT().GetByCheckoutID(mock.Anything, entity.CheckoutID("checkout-1")).Return(nil, apperr.ErrNotFound)
		f.events.EXPECT().IsEventPublished(mock.Anything, "event-1").Return(true, nil)
		f.authorizesAs(authorizedPI())
		f.checkouts.EXPECT().Commit(mock.Anything, entity.CheckoutID("checkout-1"), placeAt).Return(entity.CommitOutcomeNotHeld, nil)

		_, err := f.uc.IssueFromCheckout(context.Background(), "checkout-1", &fan1)

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
	})
	t.Run("verified sale issued", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/order/issue-from-checkout "Verified sale issued"
		f := newCheckoutIssuanceFixture(t, placeAt, entity.VerificationRequirementJPKIOnly)
		held := authorizedCheckout()
		f.checkouts.EXPECT().Get(mock.Anything, entity.CheckoutID("checkout-1")).Return(held, nil).Once()
		f.orders.EXPECT().GetByCheckoutID(mock.Anything, entity.CheckoutID("checkout-1")).Return(nil, apperr.ErrNotFound)
		f.events.EXPECT().IsEventPublished(mock.Anything, "event-1").Return(true, nil)
		f.identityVerifications.EXPECT().GetByUserID(mock.Anything, "fan-1").Return(&entity.IdentityVerification{ID: "identity verification-1", UserID: "fan-1", Status: entity.VerificationStatusActive}, nil)
		f.authorizesAs(authorizedPI())
		f.checkouts.EXPECT().Commit(mock.Anything, entity.CheckoutID("checkout-1"), placeAt).Return(entity.CommitOutcomeCommitted, nil)
		f.checkouts.EXPECT().Get(mock.Anything, entity.CheckoutID("checkout-1")).Return(committedCopy(held, false), nil).Once()
		f.auth.EXPECT().Capture(mock.Anything, "pi_1").Return(nil).Once()
		f.checkouts.EXPECT().RecordCapture(mock.Anything, entity.CheckoutID("checkout-1"), placeAt).Return(nil)
		var gotTickets []*entity.Ticket
		f.issuance.EXPECT().Issue(mock.Anything, mock.Anything, mock.Anything, mock.Anything).RunAndReturn(
			func(_ context.Context, _ *entity.Order, ts []*entity.Ticket, _ *entity.Settlement) error {
				gotTickets = ts
				return nil
			})

		_, err := f.uc.IssueFromCheckout(context.Background(), "checkout-1", &fan1)

		require.NoError(t, err)
		require.Len(t, gotTickets, 2)
		for _, tk := range gotTickets {
			assert.Equal(t, "identity verification-1", tk.IdentityVerificationID)
		}
	})

	t.Run("identity verification gone before confirm", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/order/issue-from-checkout "IdentityVerification gone before confirm"
		f := newCheckoutIssuanceFixture(t, placeAt, entity.VerificationRequirementVerifiedAny)
		f.checkouts.EXPECT().Get(mock.Anything, entity.CheckoutID("checkout-1")).Return(authorizedCheckout(), nil)
		f.orders.EXPECT().GetByCheckoutID(mock.Anything, entity.CheckoutID("checkout-1")).Return(nil, apperr.ErrNotFound)
		f.events.EXPECT().IsEventPublished(mock.Anything, "event-1").Return(true, nil)
		f.identityVerifications.EXPECT().GetByUserID(mock.Anything, "fan-1").Return(nil, apperr.ErrNotFound)

		_, err := f.uc.IssueFromCheckout(context.Background(), "checkout-1", &fan1)

		// No card authorization read, Commit, Capture or Issue
		// expectation: nothing is charged and nothing is created.
		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})

	t.Run("identity verification gone before a stalled capture", func(t *testing.T) {
		t.Parallel()
		f := newCheckoutIssuanceFixture(t, placeAt, entity.VerificationRequirementVerifiedAny)
		f.checkouts.EXPECT().Get(mock.Anything, entity.CheckoutID("checkout-1")).Return(committedCopy(authorizedCheckout(), false), nil)
		f.orders.EXPECT().GetByCheckoutID(mock.Anything, entity.CheckoutID("checkout-1")).Return(nil, apperr.ErrNotFound)
		f.identityVerifications.EXPECT().GetByUserID(mock.Anything, "fan-1").Return(nil, apperr.ErrNotFound)

		_, err := f.uc.IssueFromCheckout(context.Background(), "checkout-1", nil)

		assert.ErrorIs(t, err, apperr.ErrNotFound, "the card is not charged")
	})

}

func TestOrderUseCase_IssueDueCheckouts(t *testing.T) {
	t.Parallel()

	t.Run("capture interrupted", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/order/issue-due-checkouts "Capture interrupted"
		now := coStart.Add(22 * time.Minute)
		f := newCheckoutIssuanceFixture(t, now)
		committed := committedCopy(authorizedCheckout(), false)
		f.checkouts.EXPECT().ListDue(mock.Anything, now).Return([]*entity.Checkout{committed}, nil)
		f.checkouts.EXPECT().Get(mock.Anything, entity.CheckoutID("checkout-1")).Return(committed, nil).Once()
		f.orders.EXPECT().GetByCheckoutID(mock.Anything, entity.CheckoutID("checkout-1")).Return(nil, apperr.ErrNotFound)
		f.auth.EXPECT().Capture(mock.Anything, "pi_1").Return(nil).Once()
		f.checkouts.EXPECT().RecordCapture(mock.Anything, entity.CheckoutID("checkout-1"), now).Return(nil)
		f.issuance.EXPECT().Issue(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

		require.NoError(t, f.uc.IssueDueCheckouts(context.Background()))
	})

	t.Run("charged but not issued", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/order/issue-due-checkouts "Charged but not issued"
		now := coStart.Add(22 * time.Minute)
		f := newCheckoutIssuanceFixture(t, now)
		charged := committedCopy(authorizedCheckout(), true)
		f.checkouts.EXPECT().ListDue(mock.Anything, now).Return([]*entity.Checkout{charged}, nil)
		f.checkouts.EXPECT().Get(mock.Anything, entity.CheckoutID("checkout-1")).Return(charged, nil)
		f.orders.EXPECT().GetByCheckoutID(mock.Anything, entity.CheckoutID("checkout-1")).Return(nil, apperr.ErrNotFound)
		f.issuance.EXPECT().Issue(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

		// No Capture expectation: the card is not charged again.
		require.NoError(t, f.uc.IssueDueCheckouts(context.Background()))
	})

	t.Run("one checkout fails", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/order/issue-due-checkouts "One checkout fails"
		now := coStart.Add(22 * time.Minute)
		f := newCheckoutIssuanceFixture(t, now)
		bad := committedCopy(authorizedCheckout(), true)
		bad.ID = "checkout-bad"
		good := committedCopy(authorizedCheckout(), true)
		lapsed := fanCheckout(coStart) // a Held row in the listing is left to ReleaseExpired
		f.checkouts.EXPECT().ListDue(mock.Anything, now).Return([]*entity.Checkout{lapsed, bad, good}, nil)
		f.checkouts.EXPECT().Get(mock.Anything, entity.CheckoutID("checkout-bad")).Return(nil, apperr.ErrInternal)
		f.checkouts.EXPECT().Get(mock.Anything, entity.CheckoutID("checkout-1")).Return(good, nil)
		f.orders.EXPECT().GetByCheckoutID(mock.Anything, entity.CheckoutID("checkout-1")).Return(nil, apperr.ErrNotFound)
		f.issuance.EXPECT().Issue(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

		require.NoError(t, f.uc.IssueDueCheckouts(context.Background()))
	})

	t.Run("organizer record broken", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/order/issue-due-checkouts "Organizer record broken"
		// Charged at 18:11 and still failing at 18:31: reported and tried again.
		now := coStart.Add(31 * time.Minute)
		f := newCheckoutIssuanceFixture(t, now)
		charged := committedCopy(authorizedCheckout(), true)
		f.checkouts.EXPECT().ListDue(mock.Anything, now).Return([]*entity.Checkout{charged}, nil)
		f.checkouts.EXPECT().Get(mock.Anything, entity.CheckoutID("checkout-1")).Return(charged, nil)
		f.orders.EXPECT().GetByCheckoutID(mock.Anything, entity.CheckoutID("checkout-1")).Return(nil, apperr.ErrNotFound)
		f.issuance.EXPECT().Issue(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(apperr.ErrInternal)

		require.NoError(t, f.uc.IssueDueCheckouts(context.Background()))
	})

	t.Run("listing fails", func(t *testing.T) {
		t.Parallel()
		now := coStart.Add(22 * time.Minute)
		f := newCheckoutIssuanceFixture(t, now)
		f.checkouts.EXPECT().ListDue(mock.Anything, now).Return(nil, apperr.ErrInternal)

		assert.ErrorIs(t, f.uc.IssueDueCheckouts(context.Background()), apperr.ErrInternal)
	})
}

func TestOrderUseCase_FulfillPayment(t *testing.T) {
	t.Parallel()
	now := coStart.Add(12 * time.Minute)
	checkoutOrder := &entity.Order{ID: "order-1", UserID: fan1, CheckoutID: "checkout-1", Amount: 6000, Currency: "JPY"}

	t.Run("checkout charged", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/order/fulfill-payment "Checkout charged"
		f := newCheckoutIssuanceFixture(t, now)
		completed := committedCopy(authorizedCheckout(), true)
		completed.Status = entity.CheckoutStatusCompleted
		f.checkouts.EXPECT().GetByCardAuthorizationID(mock.Anything, "pi_1").Return(completed, nil)
		f.checkouts.EXPECT().Get(mock.Anything, entity.CheckoutID("checkout-1")).Return(completed, nil)
		f.orders.EXPECT().GetByCheckoutID(mock.Anything, entity.CheckoutID("checkout-1")).Return(checkoutOrder, nil)
		f.publisher.EXPECT().PublishEventWithID(mock.Anything, entity.SubjectOrderPaid, "order-1", &entity.OrderPaidData{
			OrderID: "order-1", UserID: "fan-1", EventID: "event-1", TicketCount: 2, Amount: 6000, Currency: "JPY", CheckoutID: "checkout-1",
		}).Return(nil).Once()

		require.NoError(t, f.uc.FulfillPayment(context.Background(), "pi_1"))
	})

	t.Run("lottery win charged", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/order/fulfill-payment "Lottery win charged"
		f := newCheckoutIssuanceFixture(t, now)
		f.checkouts.EXPECT().GetByCardAuthorizationID(mock.Anything, "pi_won").Return(nil, apperr.ErrNotFound)
		entry := &entity.LotteryEntry{
			ID: "entry-1", TicketTypeID: "tt-1", UserID: "user-1", TicketCount: 2,
			CardAuthorizationID: "pi_won", State: entity.LotteryEntryStateWon,
		}
		f.entries.EXPECT().GetByCardAuthorizationID(mock.Anything, "pi_won").Return(entry, nil)
		winOrder := &entity.Order{ID: "order-2", UserID: entry.UserID, LotteryEntryID: entry.ID, Amount: 10000, Currency: "JPY"}
		f.orders.EXPECT().GetByLotteryEntryID(mock.Anything, entry.ID).Return(winOrder, nil)
		f.publisher.EXPECT().PublishEventWithID(mock.Anything, entity.SubjectOrderPaid, "order-2", &entity.OrderPaidData{
			OrderID: "order-2", UserID: "user-1", EventID: "event-1", TicketCount: 2, Amount: 10000, Currency: "JPY", LotteryEntryID: "entry-1",
		}).Return(nil).Once()

		require.NoError(t, f.uc.FulfillPayment(context.Background(), "pi_won"))
	})

	t.Run("charge reported twice", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/order/fulfill-payment "Charge reported twice"
		// Both reports find the same Order and announce it under the same
		// message id, which JetStream deduplicates; no second Order is issued.
		f := newCheckoutIssuanceFixture(t, now)
		completed := committedCopy(authorizedCheckout(), true)
		completed.Status = entity.CheckoutStatusCompleted
		f.checkouts.EXPECT().GetByCardAuthorizationID(mock.Anything, "pi_1").Return(completed, nil).Twice()
		f.checkouts.EXPECT().Get(mock.Anything, entity.CheckoutID("checkout-1")).Return(completed, nil).Twice()
		f.orders.EXPECT().GetByCheckoutID(mock.Anything, entity.CheckoutID("checkout-1")).Return(checkoutOrder, nil).Twice()
		f.publisher.EXPECT().PublishEventWithID(mock.Anything, entity.SubjectOrderPaid, "order-1", mock.Anything).Return(nil).Twice()

		require.NoError(t, f.uc.FulfillPayment(context.Background(), "pi_1"))
		require.NoError(t, f.uc.FulfillPayment(context.Background(), "pi_1"))
	})

	t.Run("messaging unavailable", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/order/fulfill-payment "Messaging unavailable"
		f := newCheckoutIssuanceFixture(t, now)
		completed := committedCopy(authorizedCheckout(), true)
		completed.Status = entity.CheckoutStatusCompleted
		f.checkouts.EXPECT().GetByCardAuthorizationID(mock.Anything, "pi_1").Return(completed, nil)
		f.checkouts.EXPECT().Get(mock.Anything, entity.CheckoutID("checkout-1")).Return(completed, nil)
		f.orders.EXPECT().GetByCheckoutID(mock.Anything, entity.CheckoutID("checkout-1")).Return(checkoutOrder, nil)
		f.publisher.EXPECT().PublishEventWithID(mock.Anything, entity.SubjectOrderPaid, "order-1", mock.Anything).Return(errors.New("nats down"))

		assert.Error(t, f.uc.FulfillPayment(context.Background(), "pi_1"))
	})

	t.Run("charge of another system", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/order/fulfill-payment "Charge of another system"
		f := newCheckoutIssuanceFixture(t, now)
		f.checkouts.EXPECT().GetByCardAuthorizationID(mock.Anything, "pi_other").Return(nil, apperr.ErrNotFound)
		f.entries.EXPECT().GetByCardAuthorizationID(mock.Anything, "pi_other").Return(nil, apperr.ErrNotFound)

		require.NoError(t, f.uc.FulfillPayment(context.Background(), "pi_other"))
	})
}
