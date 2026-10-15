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
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// checkoutFixture holds the mocks of a CheckoutUseCase under test.
type checkoutFixture struct {
	checkouts             *entitymocks.MockCheckoutRepository
	referrals             *entitymocks.MockReferralRepository
	sales                 *entitymocks.MockTicketSaleRepository
	users                 *entitymocks.MockUserRepository
	identityVerifications *entitymocks.MockIdentityVerificationRepository
	events                *ucmocks.MockEventPublishStatePort
	auth                  *entitymocks.MockCardAuthorizationRepository
	uc                    usecase.CheckoutUseCase
}

func newCheckoutFixture(t *testing.T, now time.Time) *checkoutFixture {
	t.Helper()
	f := &checkoutFixture{
		checkouts:             entitymocks.NewMockCheckoutRepository(t),
		referrals:             entitymocks.NewMockReferralRepository(t),
		sales:                 entitymocks.NewMockTicketSaleRepository(t),
		users:                 entitymocks.NewMockUserRepository(t),
		identityVerifications: entitymocks.NewMockIdentityVerificationRepository(t),
		events:                ucmocks.NewMockEventPublishStatePort(t),
		auth:                  entitymocks.NewMockCardAuthorizationRepository(t),
	}
	f.uc = usecase.NewCheckoutUseCase(usecase.CheckoutDeps{
		CheckoutRepo:             f.checkouts,
		ReferralRepo:             f.referrals,
		SaleRepo:                 f.sales,
		UserRepo:                 f.users,
		IdentityVerificationRepo: f.identityVerifications,
		EventState:               f.events,
		CardAuthorizations:       f.auth,
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

// firstComeTicketType returns the FirstCome sale "sale-1" and its ticket type
// "tt-1" for event-1: 3000 yen, quantity tickets, 4 per account, with the
// given sold and held counts.
func firstComeTicketType(quantity, sold, held int) (*entity.TicketSale, *entity.TicketType) {
	sale := &entity.TicketSale{
		ID: "sale-1", SeriesID: "series-1", Name: "一般発売", Method: entity.TicketSaleMethodFirstCome,
		SaleStartTime: coSaleStart, SaleEndTime: coSaleEnd,
	}
	tt := &entity.TicketType{
		ID: "tt-1", TicketSaleID: sale.ID, EventID: "event-1",
		Price: 3000, Quantity: quantity, PerAccountLimit: 4, SoldCount: sold, HeldCount: held,
	}
	sale.TicketTypes = []*entity.TicketType{tt}
	return sale, tt
}

// expectTicketType makes the sale repository return sale and its first
// ticket type for "tt-1".
func expectTicketType(sales *entitymocks.MockTicketSaleRepository, sale *entity.TicketSale) {
	sales.EXPECT().GetTicketType(mock.Anything, entity.TicketTypeID("tt-1"), mock.Anything).Return(sale, sale.TicketTypes[0], nil)
}

// fanCheckout returns fan-1's Held checkout of 2 tickets started at
// startedAt, expiring 15 minutes later.
func fanCheckout(startedAt time.Time) *entity.Checkout {
	return &entity.Checkout{
		ID: "checkout-1", TicketTypeID: "tt-1", UserID: "fan-1", TicketCount: 2, Amount: 6000,
		Status: entity.CheckoutStatusHeld, HoldExpireTime: startedAt.Add(15 * time.Minute),
	}
}

func TestCheckoutUseCase_Start(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 5, 18, 0, 0, 0, tsJST)

	t.Run("fan starts a checkout", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/checkout/start "Fan starts a checkout"
		f := newCheckoutFixture(t, now)
		sale, _ := firstComeTicketType(150, 0, 0)
		expectTicketType(f.sales, sale)
		f.events.EXPECT().IsEventPublished(mock.Anything, "event-1").Return(true, nil)
		checkout := fanCheckout(now)
		f.checkouts.EXPECT().GetOrCreateHeld(mock.Anything, entity.TicketTypeID("tt-1"), entity.UserID("fan-1"), 2, now).Return(checkout, nil)

		got, err := f.uc.Start(context.Background(), "fan-1", "tt-1", 2, nil)

		require.NoError(t, err)
		assert.Same(t, checkout, got)
	})

	t.Run("fan reloads mid-checkout", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/checkout/start "Fan reloads mid-checkout"
		f := newCheckoutFixture(t, now.Add(5*time.Minute))
		sale, _ := firstComeTicketType(150, 0, 2)
		expectTicketType(f.sales, sale)
		f.events.EXPECT().IsEventPublished(mock.Anything, "event-1").Return(true, nil)
		original := fanCheckout(now)
		f.checkouts.EXPECT().GetOrCreateHeld(mock.Anything, entity.TicketTypeID("tt-1"), entity.UserID("fan-1"), 2, mock.Anything).Return(original, nil)

		got, err := f.uc.Start(context.Background(), "fan-1", "tt-1", 2, nil)

		require.NoError(t, err)
		assert.Equal(t, original.HoldExpireTime, got.HoldExpireTime)
	})

	t.Run("sale not open yet", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/checkout/start "Sale not open yet"
		f := newCheckoutFixture(t, time.Date(2026, 10, 31, 12, 0, 0, 0, tsJST))
		sale, _ := firstComeTicketType(150, 0, 0)
		expectTicketType(f.sales, sale)
		f.events.EXPECT().IsEventPublished(mock.Anything, "event-1").Return(true, nil)

		_, err := f.uc.Start(context.Background(), "fan-1", "tt-1", 2, nil)

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
	})

	t.Run("concert cancelled", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/checkout/start "Concert cancelled"
		f := newCheckoutFixture(t, now)
		sale, _ := firstComeTicketType(150, 0, 0)
		expectTicketType(f.sales, sale)
		f.events.EXPECT().IsEventPublished(mock.Anything, "event-1").Return(false, nil)

		_, err := f.uc.Start(context.Background(), "fan-1", "tt-1", 2, nil)

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
	})

	t.Run("last tickets in other checkouts", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/checkout/start "Last tickets in other checkouts"
		f := newCheckoutFixture(t, now)
		sale, _ := firstComeTicketType(150, 147, 3)
		expectTicketType(f.sales, sale)
		f.events.EXPECT().IsEventPublished(mock.Anything, "event-1").Return(true, nil)
		f.checkouts.EXPECT().GetOrCreateHeld(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(nil, apperr.New(apperr.ErrResourceExhausted.Code, "not enough tickets remain"))

		_, err := f.uc.Start(context.Background(), "fan-1", "tt-1", 2, nil)

		assert.ErrorIs(t, err, apperr.ErrResourceExhausted)
	})

	t.Run("lottery ticket type", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/checkout/start "Lottery ticket type"
		f := newCheckoutFixture(t, now)
		sale, _ := firstComeTicketType(150, 0, 0)
		sale.Method = entity.TicketSaleMethodLottery
		expectTicketType(f.sales, sale)

		_, err := f.uc.Start(context.Background(), "fan-1", "tt-1", 2, nil)

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
	})

	t.Run("count over the limit", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/checkout/start "Count over the limit"
		f := newCheckoutFixture(t, now)
		sale, _ := firstComeTicketType(150, 0, 0)
		expectTicketType(f.sales, sale)
		f.events.EXPECT().IsEventPublished(mock.Anything, "event-1").Return(true, nil)

		_, err := f.uc.Start(context.Background(), "fan-1", "tt-1", 5, nil)

		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
	})

	t.Run("unknown ticket type", func(t *testing.T) {
		t.Parallel()
		f := newCheckoutFixture(t, now)
		f.sales.EXPECT().GetTicketType(mock.Anything, entity.TicketTypeID("tt-x"), now).Return(nil, nil, apperr.ErrNotFound)

		_, err := f.uc.Start(context.Background(), "fan-1", "tt-x", 2, nil)

		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})

	t.Run("presale winner reaches the limit", func(t *testing.T) {
		t.Parallel()
		// Design D5: the per-account limit counts the fan's Issued Tickets for
		// the event; CheckoutRepository.GetOrCreateHeld checks it under the
		// ticket type's lock and reports FailedPrecondition, which Start
		// returns unchanged.
		f := newCheckoutFixture(t, now)
		sale, _ := firstComeTicketType(150, 0, 0)
		expectTicketType(f.sales, sale)
		f.events.EXPECT().IsEventPublished(mock.Anything, "event-1").Return(true, nil)
		f.checkouts.EXPECT().GetOrCreateHeld(mock.Anything, entity.TicketTypeID("tt-1"), entity.UserID("fan-1"), 1, now).
			Return(nil, apperr.New(apperr.ErrFailedPrecondition.Code, "the per-account limit would be exceeded"))

		_, err := f.uc.Start(context.Background(), "fan-1", "tt-1", 1, nil)

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
	})

	t.Run("first-come sale requires verification", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/checkout/start "First-come sale requires verification"
		f := newCheckoutFixture(t, now)
		sale, _ := firstComeTicketType(150, 0, 0)
		sale.VerificationRequirement = entity.VerificationRequirementJPKIOnly
		expectTicketType(f.sales, sale)
		f.events.EXPECT().IsEventPublished(mock.Anything, "event-1").Return(true, nil)
		f.identityVerifications.EXPECT().GetByUserID(mock.Anything, "fan-1").Return(nil, apperr.ErrNotFound)

		_, err := f.uc.Start(context.Background(), "fan-1", "tt-1", 2, nil)

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
	})

	t.Run("identity verification needs re-verification", func(t *testing.T) {
		t.Parallel()
		f := newCheckoutFixture(t, now)
		sale, _ := firstComeTicketType(150, 0, 0)
		sale.VerificationRequirement = entity.VerificationRequirementVerifiedAny
		expectTicketType(f.sales, sale)
		f.events.EXPECT().IsEventPublished(mock.Anything, "event-1").Return(true, nil)
		f.identityVerifications.EXPECT().GetByUserID(mock.Anything, "fan-1").
			Return(&entity.IdentityVerification{ID: "identity verification-1", UserID: "fan-1", Status: entity.VerificationStatusNeedsReverification}, nil)

		_, err := f.uc.Start(context.Background(), "fan-1", "tt-1", 2, nil)

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
	})

	t.Run("verified fan starts a checkout", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/checkout/start "Verified fan starts a checkout"
		f := newCheckoutFixture(t, now)
		sale, _ := firstComeTicketType(150, 0, 0)
		sale.VerificationRequirement = entity.VerificationRequirementJPKIOnly
		expectTicketType(f.sales, sale)
		f.events.EXPECT().IsEventPublished(mock.Anything, "event-1").Return(true, nil)
		f.identityVerifications.EXPECT().GetByUserID(mock.Anything, "fan-1").
			Return(&entity.IdentityVerification{ID: "identity verification-1", UserID: "fan-1", Status: entity.VerificationStatusActive}, nil)
		checkout := fanCheckout(now)
		f.checkouts.EXPECT().GetOrCreateHeld(mock.Anything, entity.TicketTypeID("tt-1"), entity.UserID("fan-1"), 2, now).Return(checkout, nil)

		got, err := f.uc.Start(context.Background(), "fan-1", "tt-1", 2, nil)

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
		checkout.Status, checkout.CommitTime, checkout.CardAuthorizationID = entity.CheckoutStatusReleased, &committed, "pi_1"
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
		sale, _ := firstComeTicketType(150, 0, 2)
		expectTicketType(f.sales, sale)
		updateDetails := f.users.EXPECT().UpdatePersonalDetails(mock.Anything, "fan-1", name, phone).
			Return(&entity.User{ID: "fan-1", FullName: name, PhoneNumber: phone}, nil).Call
		createHold := f.auth.EXPECT().Create(mock.Anything, entity.CardAuthorizationPurchase{
			CheckoutID: "checkout-1", TicketTypeID: "tt-1", EventID: "event-1",
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
		checkout.CardAuthorizationID = "pi_1"
		f.checkouts.EXPECT().Get(mock.Anything, entity.CheckoutID("checkout-1")).Return(checkout, nil)
		sale, _ := firstComeTicketType(150, 0, 2)
		expectTicketType(f.sales, sale)
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
		checkout.CardAuthorizationID = "pi_1"
		f.checkouts.EXPECT().Get(mock.Anything, entity.CheckoutID("checkout-1")).Return(checkout, nil)
		sale, _ := firstComeTicketType(150, 0, 2)
		expectTicketType(f.sales, sale)
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

func TestCheckoutUseCase_ReleaseExpired(t *testing.T) {
	t.Parallel()
	start := time.Date(2026, 11, 5, 18, 0, 0, 0, tsJST)
	now := start.Add(16 * time.Minute)

	t.Run("fan walked away", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/checkout/release-expired "Fan walked away"
		f := newCheckoutFixture(t, now)
		lapsed := fanCheckout(start)
		lapsed.CardAuthorizationID = "pi_1"
		f.checkouts.EXPECT().ListDue(mock.Anything, now).Return([]*entity.Checkout{lapsed}, nil).Once()
		f.checkouts.EXPECT().Release(mock.Anything, entity.CheckoutID("checkout-1"), now).Return(true, nil).Once()

		require.NoError(t, f.uc.ReleaseExpired(context.Background()))

		// The next run finds it Expired with its card authorization and gives it back.
		expired := *lapsed
		expired.Status = entity.CheckoutStatusExpired
		f.checkouts.EXPECT().ListDue(mock.Anything, now).Return([]*entity.Checkout{&expired}, nil).Once()
		f.auth.EXPECT().Release(mock.Anything, "pi_1").Return(nil)
		f.checkouts.EXPECT().RecordCardAuthorizationRelease(mock.Anything, entity.CheckoutID("checkout-1"), now).Return(nil)

		require.NoError(t, f.uc.ReleaseExpired(context.Background()))
	})

	t.Run("count changed after authorizing", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/checkout/release-expired "Count changed after authorizing"
		f := newCheckoutFixture(t, now)
		replaced := fanCheckout(start)
		replaced.Status, replaced.CardAuthorizationID = entity.CheckoutStatusReleased, "pi_2"
		f.checkouts.EXPECT().ListDue(mock.Anything, now).Return([]*entity.Checkout{replaced}, nil)
		f.auth.EXPECT().Release(mock.Anything, "pi_2").Return(nil)
		f.checkouts.EXPECT().RecordCardAuthorizationRelease(mock.Anything, entity.CheckoutID("checkout-1"), now).Return(nil)

		require.NoError(t, f.uc.ReleaseExpired(context.Background()))
	})

	t.Run("committed at the last moment", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/checkout/release-expired "Committed at the last moment"
		f := newCheckoutFixture(t, now)
		lapsed := fanCheckout(start)
		lapsed.CardAuthorizationID = "pi_3"
		f.checkouts.EXPECT().ListDue(mock.Anything, now).Return([]*entity.Checkout{lapsed}, nil)
		// Committed meanwhile: Release changes nothing, and no card authorization is cancelled.
		f.checkouts.EXPECT().Release(mock.Anything, entity.CheckoutID("checkout-1"), now).Return(false, nil)

		require.NoError(t, f.uc.ReleaseExpired(context.Background()))
	})

	t.Run("cancellation fails", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/checkout/release-expired "Release fails"
		f := newCheckoutFixture(t, now)
		first := fanCheckout(start)
		first.ID, first.Status, first.CardAuthorizationID = "checkout-a", entity.CheckoutStatusExpired, "pi_a"
		second := fanCheckout(start)
		second.ID, second.Status, second.CardAuthorizationID = "checkout-b", entity.CheckoutStatusExpired, "pi_b"
		f.checkouts.EXPECT().ListDue(mock.Anything, now).Return([]*entity.Checkout{first, second}, nil)
		f.auth.EXPECT().Release(mock.Anything, "pi_a").Return(apperr.New(apperr.ErrUnavailable.Code, "stripe down"))
		f.auth.EXPECT().Release(mock.Anything, "pi_b").Return(nil)
		f.checkouts.EXPECT().RecordCardAuthorizationRelease(mock.Anything, entity.CheckoutID("checkout-b"), now).Return(nil)

		require.NoError(t, f.uc.ReleaseExpired(context.Background()))
	})

	t.Run("money taken on an ended checkout", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/checkout/release-expired "Money taken on an ended checkout"
		f := newCheckoutFixture(t, now)
		released := fanCheckout(start)
		released.Status, released.CardAuthorizationID = entity.CheckoutStatusReleased, "pi_charged"
		f.checkouts.EXPECT().ListDue(mock.Anything, now).Return([]*entity.Checkout{released}, nil)
		f.auth.EXPECT().Release(mock.Anything, "pi_charged").
			Return(apperr.New(apperr.ErrFailedPrecondition.Code, "the card authorization has already been charged"))

		// Reported (logged as needing an operator) and not recorded as released.
		require.NoError(t, f.uc.ReleaseExpired(context.Background()))
	})

	t.Run("listing fails", func(t *testing.T) {
		t.Parallel()
		f := newCheckoutFixture(t, now)
		f.checkouts.EXPECT().ListDue(mock.Anything, now).Return(nil, apperr.ErrInternal)

		assert.ErrorIs(t, f.uc.ReleaseExpired(context.Background()), apperr.ErrInternal)
	})
}

func TestCheckoutUseCase_Start_RecordsReferral(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 5, 18, 0, 0, 0, tsJST)
	code := func(c entity.ReferralCode) *entity.ReferralCode { return &c }
	source := entity.ReferralSource{CheckoutID: "checkout-1"}
	// startsOnSale makes tt-1 OnSale and GetOrCreateHeld return checkout.
	startsOnSale := func(f *checkoutFixture, checkout *entity.Checkout) {
		sale, _ := firstComeTicketType(150, 0, 0)
		expectTicketType(f.sales, sale)
		f.events.EXPECT().IsEventPublished(mock.Anything, "event-1").Return(true, nil)
		f.checkouts.EXPECT().GetOrCreateHeld(mock.Anything, entity.TicketTypeID("tt-1"), entity.UserID("fan-1"), 2, now).Return(checkout, nil)
	}

	t.Run("checkout from a referral link", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/checkout/start "Checkout from a referral link"
		f := newCheckoutFixture(t, now)
		checkout := fanCheckout(now)
		startsOnSale(f, checkout)
		f.referrals.EXPECT().Record(mock.Anything, source, entity.ReferralCode("member-a"), now).Return(nil)

		got, err := f.uc.Start(context.Background(), "fan-1", "tt-1", 2, code("member-a"))

		require.NoError(t, err)
		assert.Same(t, checkout, got)
	})

	t.Run("another link before confirming", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/checkout/start "Another link before confirming"
		// The Held Checkout already has member-a; Record replaces it.
		f := newCheckoutFixture(t, now)
		checkout := fanCheckout(now.Add(-5 * time.Minute))
		startsOnSale(f, checkout)
		f.referrals.EXPECT().Record(mock.Anything, source, entity.ReferralCode("member-b"), now).Return(nil)

		got, err := f.uc.Start(context.Background(), "fan-1", "tt-1", 2, code("member-b"))

		require.NoError(t, err)
		assert.Same(t, checkout, got)
	})

	t.Run("started again without a code", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/checkout/start "Started again without a code"
		// No Record call: the Referral holding member-a stays as it is.
		f := newCheckoutFixture(t, now)
		checkout := fanCheckout(now.Add(-5 * time.Minute))
		startsOnSale(f, checkout)

		got, err := f.uc.Start(context.Background(), "fan-1", "tt-1", 2, nil)

		require.NoError(t, err)
		assert.Same(t, checkout, got)
		f.referrals.AssertNotCalled(t, "Record", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	})

	t.Run("no code", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/checkout/start "No code"
		f := newCheckoutFixture(t, now)
		startsOnSale(f, fanCheckout(now))

		_, err := f.uc.Start(context.Background(), "fan-1", "tt-1", 2, nil)

		require.NoError(t, err)
		f.referrals.AssertNotCalled(t, "Record", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	})

	t.Run("sale not open with a code", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/checkout/start "Sale not open with a code"
		f := newCheckoutFixture(t, time.Date(2026, 10, 31, 12, 0, 0, 0, tsJST))
		sale, _ := firstComeTicketType(150, 0, 0)
		expectTicketType(f.sales, sale)
		f.events.EXPECT().IsEventPublished(mock.Anything, "event-1").Return(true, nil)

		_, err := f.uc.Start(context.Background(), "fan-1", "tt-1", 2, code("member-a"))

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
		f.referrals.AssertNotCalled(t, "Record", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	})

	t.Run("record failure fails start", func(t *testing.T) {
		t.Parallel()
		f := newCheckoutFixture(t, now)
		startsOnSale(f, fanCheckout(now))
		f.referrals.EXPECT().Record(mock.Anything, source, entity.ReferralCode("member-a"), now).Return(apperr.ErrInternal)

		_, err := f.uc.Start(context.Background(), "fan-1", "tt-1", 2, code("member-a"))

		assert.ErrorIs(t, err, apperr.ErrInternal)
	})
}
