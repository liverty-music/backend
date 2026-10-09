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

// checkoutIssuanceFixture holds the mocks of IssueFromReservation under test.
type checkoutIssuanceFixture struct {
	issuance     *entitymocks.MockIssuanceRepository
	orders       *entitymocks.MockOrderRepository
	organizers   *entitymocks.MockOrganizerRepository
	reservations *entitymocks.MockReservationRepository
	sales        *entitymocks.MockTicketSaleRepository
	events       *ucmocks.MockEventPublishStatePort
	auth         *entitymocks.MockReservationAuthorizationPort
	uc           usecase.IssuanceUseCase
}

func newCheckoutIssuanceFixture(t *testing.T, now time.Time) *checkoutIssuanceFixture {
	t.Helper()
	f := &checkoutIssuanceFixture{
		issuance:     entitymocks.NewMockIssuanceRepository(t),
		orders:       entitymocks.NewMockOrderRepository(t),
		organizers:   entitymocks.NewMockOrganizerRepository(t),
		reservations: entitymocks.NewMockReservationRepository(t),
		sales:        entitymocks.NewMockTicketSaleRepository(t),
		events:       ucmocks.NewMockEventPublishStatePort(t),
		auth:         entitymocks.NewMockReservationAuthorizationPort(t),
	}
	// Serialize runs the call under the lock.
	f.reservations.EXPECT().Serialize(mock.Anything, mock.Anything, mock.Anything).RunAndReturn(
		func(ctx context.Context, _ entity.ReservationID, fn func(context.Context) error) error {
			return fn(ctx)
		}).Maybe()
	f.organizers.EXPECT().Get(mock.Anything, "org-1").Return(&entity.Organizer{ID: "org-1", PlatformFeeRateBps: 800}, nil).Maybe()
	f.sales.EXPECT().Get(mock.Anything, entity.TicketSaleID("sale-1"), mock.Anything).Return(saleWith(150, 0, 2), nil).Maybe()
	f.uc = usecase.NewIssuanceUseCase(usecase.IssuanceDeps{
		IssuanceRepo:       f.issuance,
		OrderRepo:          f.orders,
		EventOrganizerRepo: &stubEventOrganizerRepo{getOrganizerIDFn: func(context.Context, string) (string, error) { return "org-1", nil }},
		OrganizerRepo:      f.organizers,
		ReservationRepo:    f.reservations,
		TicketSaleRepo:     f.sales,
		EventState:         f.events,
		ReservationAuth:    f.auth,
		Clock:              fixedClock(now),
		Logger:             newTestLogger(t),
	})
	return f
}

var (
	coStart   = time.Date(2026, 11, 5, 18, 0, 0, 0, tsJST)
	coHolder  = &entity.HolderIdentity{FullName: "山田 花子", PhoneNumber: "+819012345678"}
	coPayment = &entity.CapturedPayment{Provider: entity.PaymentProviderStripe, PaymentIntentRef: "pi_1", AmountJPY: 6000, Currency: "JPY", CardBrand: "visa", CardLast4: "4242"}
	fan1      = entity.UserID("fan-1")
)

// authorizedReservation is fan-1's holding, authorized checkout of 2 x 3000.
func authorizedReservation() *entity.Reservation {
	r := fanReservation(coStart)
	r.AuthorizationRef, r.HolderIdentity = "pi_1", coHolder
	return r
}

// committedCopy returns r Committed (and charged when captured is set).
func committedCopy(r *entity.Reservation, captured bool) *entity.Reservation {
	c := *r
	committedAt := coStart.Add(10 * time.Minute)
	c.Status, c.CommitTime = entity.ReservationStatusCommitted, &committedAt
	if captured {
		capturedAt := coStart.Add(11 * time.Minute)
		c.CaptureTime, c.PaymentRef, c.CardBrand, c.CardLast4 = &capturedAt, "pi_1", "visa", "4242"
	}
	return &c
}

func TestIssuanceUseCase_IssueFromReservation(t *testing.T) {
	t.Parallel()
	placeAt := coStart.Add(10 * time.Minute)

	t.Run("fan places the order within the hold", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/order/issue-from-reservation "Fan places the order within the hold"
		// @spec components/usecase/order/issue-from-reservation "Checkout issued"
		f := newCheckoutIssuanceFixture(t, placeAt)
		held := authorizedReservation()
		f.reservations.EXPECT().Get(mock.Anything, entity.ReservationID("res-1")).Return(held, nil).Once()
		f.orders.EXPECT().GetByReservationID(mock.Anything, entity.ReservationID("res-1")).Return(nil, apperr.ErrNotFound)
		f.events.EXPECT().IsEventPublished(mock.Anything, "event-1").Return(true, nil)
		f.auth.EXPECT().VerifyAuthorization(mock.Anything, "pi_1", int64(6000)).Return(nil)
		f.reservations.EXPECT().Commit(mock.Anything, entity.ReservationID("res-1"), placeAt).Return(entity.CommitOutcomeCommitted, nil)
		f.reservations.EXPECT().Get(mock.Anything, entity.ReservationID("res-1")).Return(committedCopy(held, false), nil).Once()
		f.auth.EXPECT().CaptureAuthorization(mock.Anything, "pi_1").Return(coPayment, nil).Once()
		f.reservations.EXPECT().RecordCapture(mock.Anything, entity.ReservationID("res-1"), placeAt, coPayment).Return(nil)
		f.reservations.EXPECT().Get(mock.Anything, entity.ReservationID("res-1")).Return(committedCopy(held, true), nil).Once()

		var gotOrder *entity.Order
		var gotTickets []*entity.Ticket
		var gotSettlement *entity.Settlement
		f.issuance.EXPECT().Issue(mock.Anything, mock.Anything, mock.Anything, mock.Anything).RunAndReturn(
			func(_ context.Context, o *entity.Order, ts []*entity.Ticket, s *entity.Settlement) error {
				gotOrder, gotTickets, gotSettlement = o, ts, s
				return nil
			})

		order, err := f.uc.IssueFromReservation(context.Background(), "res-1", &fan1)

		require.NoError(t, err)
		assert.Same(t, gotOrder, order)
		assert.Equal(t, entity.OrderStatusPaid, order.Status)
		assert.Equal(t, entity.ReservationID("res-1"), order.ReservationID)
		assert.Empty(t, order.ApplicationID)
		assert.Equal(t, int64(6000), order.Amount)
		assert.Equal(t, "pi_1", order.Payment.PaymentIntentRef)
		assert.Equal(t, "4242", order.Payment.CardLast4)
		require.Len(t, gotTickets, 2)
		for _, tk := range gotTickets {
			assert.Equal(t, fan1, tk.HolderID)
			assert.Equal(t, "event-1", tk.EventID)
			assert.Equal(t, *coHolder, tk.HolderIdentity)
			assert.Equal(t, entity.TicketStatusIssued, tk.Status)
		}
		require.Len(t, gotSettlement.Splits, 1)
		assert.Equal(t, int64(5520), gotSettlement.Splits[0].Amount, "6000 yen at 8%")
		assert.Equal(t, 800, gotSettlement.PlatformFeeRateBps)
	})

	t.Run("hold lapsed before placing", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/order/issue-from-reservation "Hold lapsed before placing"
		f := newCheckoutIssuanceFixture(t, coStart.Add(16*time.Minute))
		f.reservations.EXPECT().Get(mock.Anything, entity.ReservationID("res-1")).Return(authorizedReservation(), nil)
		f.orders.EXPECT().GetByReservationID(mock.Anything, entity.ReservationID("res-1")).Return(nil, apperr.ErrNotFound)

		_, err := f.uc.IssueFromReservation(context.Background(), "res-1", &fan1)

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
	})

	t.Run("concert cancelled during the checkout", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/order/issue-from-reservation "Concert cancelled during the checkout"
		f := newCheckoutIssuanceFixture(t, placeAt)
		f.reservations.EXPECT().Get(mock.Anything, entity.ReservationID("res-1")).Return(authorizedReservation(), nil)
		f.orders.EXPECT().GetByReservationID(mock.Anything, entity.ReservationID("res-1")).Return(nil, apperr.ErrNotFound)
		f.events.EXPECT().IsEventPublished(mock.Anything, "event-1").Return(false, nil)

		_, err := f.uc.IssueFromReservation(context.Background(), "res-1", &fan1)

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
	})

	t.Run("card not yet authenticated", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/order/issue-from-reservation "Card not yet authenticated"
		f := newCheckoutIssuanceFixture(t, placeAt)
		f.reservations.EXPECT().Get(mock.Anything, entity.ReservationID("res-1")).Return(authorizedReservation(), nil)
		f.orders.EXPECT().GetByReservationID(mock.Anything, entity.ReservationID("res-1")).Return(nil, apperr.ErrNotFound)
		f.events.EXPECT().IsEventPublished(mock.Anything, "event-1").Return(true, nil)
		f.auth.EXPECT().VerifyAuthorization(mock.Anything, "pi_1", int64(6000)).
			Return(apperr.New(apperr.ErrFailedPrecondition.Code, "not authenticated"))

		_, err := f.uc.IssueFromReservation(context.Background(), "res-1", &fan1)

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
	})

	t.Run("someone else's checkout", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/order/issue-from-reservation "Someone else's checkout"
		f := newCheckoutIssuanceFixture(t, placeAt)
		paid := committedCopy(authorizedReservation(), true)
		paid.Status = entity.ReservationStatusCompleted
		f.reservations.EXPECT().Get(mock.Anything, entity.ReservationID("res-1")).Return(paid, nil)
		other := entity.UserID("fan-2")

		order, err := f.uc.IssueFromReservation(context.Background(), "res-1", &other)

		assert.ErrorIs(t, err, apperr.ErrPermissionDenied)
		assert.Nil(t, order)
	})

	t.Run("existing order is returned", func(t *testing.T) {
		t.Parallel()
		f := newCheckoutIssuanceFixture(t, placeAt)
		paid := committedCopy(authorizedReservation(), true)
		paid.Status = entity.ReservationStatusCompleted
		f.reservations.EXPECT().Get(mock.Anything, entity.ReservationID("res-1")).Return(paid, nil)
		existing := &entity.Order{ID: "order-1", ReservationID: "res-1"}
		f.orders.EXPECT().GetByReservationID(mock.Anything, entity.ReservationID("res-1")).Return(existing, nil)

		order, err := f.uc.IssueFromReservation(context.Background(), "res-1", &fan1)

		require.NoError(t, err)
		assert.Same(t, existing, order)
	})

	t.Run("card no longer chargeable", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/order/issue-from-reservation "Card no longer chargeable"
		f := newCheckoutIssuanceFixture(t, placeAt)
		committed := committedCopy(authorizedReservation(), false)
		f.reservations.EXPECT().Get(mock.Anything, entity.ReservationID("res-1")).Return(committed, nil)
		f.orders.EXPECT().GetByReservationID(mock.Anything, entity.ReservationID("res-1")).Return(nil, apperr.ErrNotFound)
		f.auth.EXPECT().CaptureAuthorization(mock.Anything, "pi_1").
			Return(nil, apperr.New(apperr.ErrFailedPrecondition.Code, "the card hold was released"))
		f.reservations.EXPECT().RevertCommit(mock.Anything, entity.ReservationID("res-1")).Return(nil)

		_, err := f.uc.IssueFromReservation(context.Background(), "res-1", nil)

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
	})

	t.Run("card payments unavailable during capture", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/order/issue-from-reservation "Card payments unavailable during capture"
		f := newCheckoutIssuanceFixture(t, placeAt)
		committed := committedCopy(authorizedReservation(), false)
		f.reservations.EXPECT().Get(mock.Anything, entity.ReservationID("res-1")).Return(committed, nil)
		f.orders.EXPECT().GetByReservationID(mock.Anything, entity.ReservationID("res-1")).Return(nil, apperr.ErrNotFound)
		f.auth.EXPECT().CaptureAuthorization(mock.Anything, "pi_1").
			Return(nil, apperr.New(apperr.ErrUnavailable.Code, "outcome not known yet"))

		_, err := f.uc.IssueFromReservation(context.Background(), "res-1", nil)

		assert.ErrorIs(t, err, apperr.ErrUnavailable, "left Committed, no revert, no capture record")
	})

	t.Run("issuance fails after the charge", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/order/issue-from-reservation "Issuance fails after the charge"
		f := newCheckoutIssuanceFixture(t, placeAt)
		charged := committedCopy(authorizedReservation(), true)
		f.reservations.EXPECT().Get(mock.Anything, entity.ReservationID("res-1")).Return(charged, nil)
		f.orders.EXPECT().GetByReservationID(mock.Anything, entity.ReservationID("res-1")).Return(nil, apperr.ErrNotFound)
		f.issuance.EXPECT().Issue(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(apperr.New(apperr.ErrUnavailable.Code, "db down")).Once()

		_, err := f.uc.IssueFromReservation(context.Background(), "res-1", nil)
		assert.ErrorIs(t, err, apperr.ErrUnavailable)

		// A later run issues the Order without charging again (no Capture call).
		f.issuance.EXPECT().Issue(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()
		order, err := f.uc.IssueFromReservation(context.Background(), "res-1", nil)
		require.NoError(t, err)
		assert.Equal(t, "pi_1", order.Payment.PaymentIntentRef)
	})

	t.Run("concurrent issuance returns the first order", func(t *testing.T) {
		t.Parallel()
		f := newCheckoutIssuanceFixture(t, placeAt)
		charged := committedCopy(authorizedReservation(), true)
		f.reservations.EXPECT().Get(mock.Anything, entity.ReservationID("res-1")).Return(charged, nil)
		winner := &entity.Order{ID: "order-1", ReservationID: "res-1"}
		f.orders.EXPECT().GetByReservationID(mock.Anything, entity.ReservationID("res-1")).Return(nil, apperr.ErrNotFound).Once()
		f.issuance.EXPECT().Issue(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(apperr.ErrAlreadyExists)
		f.orders.EXPECT().GetByReservationID(mock.Anything, entity.ReservationID("res-1")).Return(winner, nil).Once()

		order, err := f.uc.IssueFromReservation(context.Background(), "res-1", &fan1)

		require.NoError(t, err)
		assert.Same(t, winner, order)
	})

	t.Run("hold released before the commit", func(t *testing.T) {
		t.Parallel()
		f := newCheckoutIssuanceFixture(t, placeAt)
		f.reservations.EXPECT().Get(mock.Anything, entity.ReservationID("res-1")).Return(authorizedReservation(), nil)
		f.orders.EXPECT().GetByReservationID(mock.Anything, entity.ReservationID("res-1")).Return(nil, apperr.ErrNotFound)
		f.events.EXPECT().IsEventPublished(mock.Anything, "event-1").Return(true, nil)
		f.auth.EXPECT().VerifyAuthorization(mock.Anything, "pi_1", int64(6000)).Return(nil)
		f.reservations.EXPECT().Commit(mock.Anything, entity.ReservationID("res-1"), placeAt).Return(entity.CommitOutcomeNotHeld, nil)

		_, err := f.uc.IssueFromReservation(context.Background(), "res-1", &fan1)

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
	})
}

func TestIssuanceUseCase_IssueDueReservations(t *testing.T) {
	t.Parallel()

	t.Run("capture interrupted", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/order/issue-due-reservations "Capture interrupted"
		now := coStart.Add(22 * time.Minute)
		f := newCheckoutIssuanceFixture(t, now)
		committed := committedCopy(authorizedReservation(), false)
		f.reservations.EXPECT().ListDue(mock.Anything, now).Return([]*entity.Reservation{committed}, nil)
		f.reservations.EXPECT().Get(mock.Anything, entity.ReservationID("res-1")).Return(committed, nil).Once()
		f.orders.EXPECT().GetByReservationID(mock.Anything, entity.ReservationID("res-1")).Return(nil, apperr.ErrNotFound)
		f.auth.EXPECT().CaptureAuthorization(mock.Anything, "pi_1").Return(coPayment, nil).Once()
		f.reservations.EXPECT().RecordCapture(mock.Anything, entity.ReservationID("res-1"), now, coPayment).Return(nil)
		f.reservations.EXPECT().Get(mock.Anything, entity.ReservationID("res-1")).Return(committedCopy(authorizedReservation(), true), nil).Once()
		f.issuance.EXPECT().Issue(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

		require.NoError(t, f.uc.IssueDueReservations(context.Background()))
	})

	t.Run("charged but not issued", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/order/issue-due-reservations "Charged but not issued"
		now := coStart.Add(22 * time.Minute)
		f := newCheckoutIssuanceFixture(t, now)
		charged := committedCopy(authorizedReservation(), true)
		f.reservations.EXPECT().ListDue(mock.Anything, now).Return([]*entity.Reservation{charged}, nil)
		f.reservations.EXPECT().Get(mock.Anything, entity.ReservationID("res-1")).Return(charged, nil)
		f.orders.EXPECT().GetByReservationID(mock.Anything, entity.ReservationID("res-1")).Return(nil, apperr.ErrNotFound)
		f.issuance.EXPECT().Issue(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

		// No CaptureAuthorization expectation: the card is not charged again.
		require.NoError(t, f.uc.IssueDueReservations(context.Background()))
	})

	t.Run("one checkout fails", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/order/issue-due-reservations "One checkout fails"
		now := coStart.Add(22 * time.Minute)
		f := newCheckoutIssuanceFixture(t, now)
		bad := committedCopy(authorizedReservation(), true)
		bad.ID = "res-bad"
		good := committedCopy(authorizedReservation(), true)
		lapsed := fanReservation(coStart) // a Held row in the listing is left to ReleaseExpired
		f.reservations.EXPECT().ListDue(mock.Anything, now).Return([]*entity.Reservation{lapsed, bad, good}, nil)
		f.reservations.EXPECT().Get(mock.Anything, entity.ReservationID("res-bad")).Return(nil, apperr.ErrInternal)
		f.reservations.EXPECT().Get(mock.Anything, entity.ReservationID("res-1")).Return(good, nil)
		f.orders.EXPECT().GetByReservationID(mock.Anything, entity.ReservationID("res-1")).Return(nil, apperr.ErrNotFound)
		f.issuance.EXPECT().Issue(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

		require.NoError(t, f.uc.IssueDueReservations(context.Background()))
	})

	t.Run("organizer record broken", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/order/issue-due-reservations "Organizer record broken"
		// Charged at 18:11 and still failing at 18:31: reported and tried again.
		now := coStart.Add(31 * time.Minute)
		f := newCheckoutIssuanceFixture(t, now)
		charged := committedCopy(authorizedReservation(), true)
		f.reservations.EXPECT().ListDue(mock.Anything, now).Return([]*entity.Reservation{charged}, nil)
		f.reservations.EXPECT().Get(mock.Anything, entity.ReservationID("res-1")).Return(charged, nil)
		f.orders.EXPECT().GetByReservationID(mock.Anything, entity.ReservationID("res-1")).Return(nil, apperr.ErrNotFound)
		f.issuance.EXPECT().Issue(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(apperr.ErrInternal)

		require.NoError(t, f.uc.IssueDueReservations(context.Background()))
	})

	t.Run("listing fails", func(t *testing.T) {
		t.Parallel()
		now := coStart.Add(22 * time.Minute)
		f := newCheckoutIssuanceFixture(t, now)
		f.reservations.EXPECT().ListDue(mock.Anything, now).Return(nil, apperr.ErrInternal)

		assert.ErrorIs(t, f.uc.IssueDueReservations(context.Background()), apperr.ErrInternal)
	})
}
