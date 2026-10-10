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

// reservationFixture holds the mocks of a ReservationUseCase under test.
type reservationFixture struct {
	reservations *entitymocks.MockReservationRepository
	sales        *entitymocks.MockTicketSaleRepository
	users        *entitymocks.MockUserRepository
	events       *ucmocks.MockEventPublishStatePort
	auth         *entitymocks.MockReservationAuthorizationPort
	uc           usecase.ReservationUseCase
}

func newReservationFixture(t *testing.T, now time.Time) *reservationFixture {
	t.Helper()
	f := &reservationFixture{
		reservations: entitymocks.NewMockReservationRepository(t),
		sales:        entitymocks.NewMockTicketSaleRepository(t),
		users:        entitymocks.NewMockUserRepository(t),
		events:       ucmocks.NewMockEventPublishStatePort(t),
		auth:         entitymocks.NewMockReservationAuthorizationPort(t),
	}
	f.uc = usecase.NewReservationUseCase(f.reservations, f.sales, f.users, f.events, f.auth, fixedClock(now), newTestLogger(t))
	return f
}

// fanReservation returns fan-1's Held reservation of 2 tickets started at
// startedAt, expiring 15 minutes later.
func fanReservation(startedAt time.Time) *entity.Reservation {
	return &entity.Reservation{
		ID: "res-1", TicketSaleID: "sale-1", UserID: "fan-1", TicketCount: 2, Amount: 6000,
		Status: entity.ReservationStatusHeld, HoldExpireTime: startedAt.Add(15 * time.Minute),
	}
}

func TestReservationUseCase_Start(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 5, 18, 0, 0, 0, tsJST)

	t.Run("fan starts a checkout", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/reservation/start "Fan starts a checkout"
		f := newReservationFixture(t, now)
		f.sales.EXPECT().Get(mock.Anything, entity.TicketSaleID("sale-1"), now).Return(saleWith(150, 0, 0), nil)
		f.events.EXPECT().IsEventPublished(mock.Anything, "event-1").Return(true, nil)
		res := fanReservation(now)
		f.reservations.EXPECT().GetOrCreateHeld(mock.Anything, entity.TicketSaleID("sale-1"), entity.UserID("fan-1"), 2, now).Return(res, nil)

		got, err := f.uc.Start(context.Background(), "fan-1", "sale-1", 2)

		require.NoError(t, err)
		assert.Same(t, res, got)
	})

	t.Run("fan reloads mid-checkout", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/reservation/start "Fan reloads mid-checkout"
		f := newReservationFixture(t, now.Add(5*time.Minute))
		f.sales.EXPECT().Get(mock.Anything, entity.TicketSaleID("sale-1"), mock.Anything).Return(saleWith(150, 0, 2), nil)
		f.events.EXPECT().IsEventPublished(mock.Anything, "event-1").Return(true, nil)
		original := fanReservation(now)
		f.reservations.EXPECT().GetOrCreateHeld(mock.Anything, entity.TicketSaleID("sale-1"), entity.UserID("fan-1"), 2, mock.Anything).Return(original, nil)

		got, err := f.uc.Start(context.Background(), "fan-1", "sale-1", 2)

		require.NoError(t, err)
		assert.Equal(t, original.HoldExpireTime, got.HoldExpireTime)
	})

	t.Run("sale not open yet", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/reservation/start "Sale not open yet"
		early := time.Date(2026, 10, 31, 12, 0, 0, 0, tsJST)
		f := newReservationFixture(t, early)
		f.sales.EXPECT().Get(mock.Anything, entity.TicketSaleID("sale-1"), early).Return(saleWith(150, 0, 0), nil)
		f.events.EXPECT().IsEventPublished(mock.Anything, "event-1").Return(true, nil)

		_, err := f.uc.Start(context.Background(), "fan-1", "sale-1", 2)

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
	})

	t.Run("concert cancelled", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/reservation/start "Concert cancelled"
		f := newReservationFixture(t, now)
		f.sales.EXPECT().Get(mock.Anything, entity.TicketSaleID("sale-1"), now).Return(saleWith(150, 0, 0), nil)
		f.events.EXPECT().IsEventPublished(mock.Anything, "event-1").Return(false, nil)

		_, err := f.uc.Start(context.Background(), "fan-1", "sale-1", 2)

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
	})

	t.Run("last tickets in other checkouts", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/reservation/start "Last tickets in other checkouts"
		f := newReservationFixture(t, now)
		f.sales.EXPECT().Get(mock.Anything, entity.TicketSaleID("sale-1"), now).Return(saleWith(150, 147, 3), nil)
		f.events.EXPECT().IsEventPublished(mock.Anything, "event-1").Return(true, nil)
		f.reservations.EXPECT().GetOrCreateHeld(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(nil, apperr.New(apperr.ErrResourceExhausted.Code, "not enough tickets remain"))

		_, err := f.uc.Start(context.Background(), "fan-1", "sale-1", 2)

		assert.ErrorIs(t, err, apperr.ErrResourceExhausted)
	})

	t.Run("count above the limit", func(t *testing.T) {
		t.Parallel()
		f := newReservationFixture(t, now)
		f.sales.EXPECT().Get(mock.Anything, entity.TicketSaleID("sale-1"), now).Return(saleWith(150, 0, 0), nil)
		f.events.EXPECT().IsEventPublished(mock.Anything, "event-1").Return(true, nil)

		_, err := f.uc.Start(context.Background(), "fan-1", "sale-1", 5)

		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
	})
}

func TestReservationUseCase_Get(t *testing.T) {
	t.Parallel()
	start := time.Date(2026, 11, 5, 18, 0, 0, 0, tsJST)
	committed := start.Add(10 * time.Minute)

	t.Run("within the hold", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/reservation/get "Within the hold"
		f := newReservationFixture(t, start.Add(5*time.Minute))
		f.reservations.EXPECT().Get(mock.Anything, entity.ReservationID("res-1")).Return(fanReservation(start), nil)

		got, err := f.uc.Get(context.Background(), "fan-1", "res-1")

		require.NoError(t, err)
		assert.Equal(t, entity.ReservationStatusHeld, got.Status)
		assert.Equal(t, start.Add(15*time.Minute), got.HoldExpireTime)
	})

	t.Run("hold lapsed", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/reservation/get "Hold lapsed"
		f := newReservationFixture(t, start.Add(20*time.Minute))
		stored := fanReservation(start)
		f.reservations.EXPECT().Get(mock.Anything, entity.ReservationID("res-1")).Return(stored, nil)

		got, err := f.uc.Get(context.Background(), "fan-1", "res-1")

		require.NoError(t, err)
		assert.Equal(t, entity.ReservationStatusExpired, got.Status)
		assert.Equal(t, entity.ReservationStatusHeld, stored.Status, "the stored Reservation is not changed")
	})

	t.Run("card no longer chargeable", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/reservation/get "Card no longer chargeable"
		f := newReservationFixture(t, start.Add(12*time.Minute))
		res := fanReservation(start)
		res.Status, res.CommitTime, res.AuthorizationRef = entity.ReservationStatusReleased, &committed, "pi_1"
		f.reservations.EXPECT().Get(mock.Anything, entity.ReservationID("res-1")).Return(res, nil)

		got, err := f.uc.Get(context.Background(), "fan-1", "res-1")

		require.NoError(t, err)
		assert.Equal(t, entity.ReservationStatusReleased, got.Status)
		assert.NotNil(t, got.CommitTime, "ever committed")
		assert.Nil(t, got.CaptureTime, "not charged")
	})

	t.Run("replaced by a newer checkout", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/reservation/get "Replaced by a newer checkout"
		f := newReservationFixture(t, start.Add(5*time.Minute))
		res := fanReservation(start)
		res.Status = entity.ReservationStatusReleased
		f.reservations.EXPECT().Get(mock.Anything, entity.ReservationID("res-1")).Return(res, nil)

		got, err := f.uc.Get(context.Background(), "fan-1", "res-1")

		require.NoError(t, err)
		assert.Equal(t, entity.ReservationStatusReleased, got.Status)
		assert.Nil(t, got.CommitTime, "never committed")
	})

	t.Run("completed checkout stays completed after its hold expiry", func(t *testing.T) {
		t.Parallel()
		f := newReservationFixture(t, start.Add(30*time.Minute))
		res := fanReservation(start)
		res.Status, res.CommitTime, res.CaptureTime = entity.ReservationStatusCompleted, &committed, &committed
		f.reservations.EXPECT().Get(mock.Anything, entity.ReservationID("res-1")).Return(res, nil)

		got, err := f.uc.Get(context.Background(), "fan-1", "res-1")

		require.NoError(t, err)
		assert.Equal(t, entity.ReservationStatusCompleted, got.Status)
	})

	t.Run("someone else's checkout", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/reservation/get "Someone else's checkout"
		f := newReservationFixture(t, start)
		f.reservations.EXPECT().Get(mock.Anything, entity.ReservationID("res-1")).Return(fanReservation(start), nil)

		_, err := f.uc.Get(context.Background(), "fan-2", "res-1")

		assert.ErrorIs(t, err, apperr.ErrPermissionDenied)
	})

	t.Run("unknown checkout", func(t *testing.T) {
		t.Parallel()
		f := newReservationFixture(t, start)
		f.reservations.EXPECT().Get(mock.Anything, entity.ReservationID("res-x")).Return(nil, apperr.ErrNotFound)

		_, err := f.uc.Get(context.Background(), "fan-1", "res-x")

		assert.ErrorIs(t, err, apperr.ErrPermissionDenied, "does not reveal whether it exists")
	})
}

func TestReservationUseCase_Authorize(t *testing.T) {
	t.Parallel()
	start := time.Date(2026, 11, 5, 18, 0, 0, 0, tsJST)
	identity := entity.HolderIdentity{FullName: "山田 花子", PhoneNumber: "+819012345678"}

	t.Run("fan authorizes", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/reservation/authorize "Fan authorizes"
		f := newReservationFixture(t, start.Add(3*time.Minute))
		f.reservations.EXPECT().Get(mock.Anything, entity.ReservationID("res-1")).Return(fanReservation(start), nil)
		f.sales.EXPECT().Get(mock.Anything, entity.TicketSaleID("sale-1"), mock.Anything).Return(saleWith(150, 0, 2), nil)
		f.auth.EXPECT().CreateAuthorization(mock.Anything, int64(6000), entity.AuthorizationMetadata{
			ReservationID: "res-1", TicketSaleID: "sale-1", EventID: "event-1",
		}).Return("pi_1", "secret_1", nil)
		f.reservations.EXPECT().SetAuthorization(mock.Anything, entity.ReservationID("res-1"), identity, "pi_1").Return(nil)
		f.users.EXPECT().UpdateHolderIdentity(mock.Anything, "fan-1", identity).Return(&entity.User{ID: "fan-1"}, nil)

		secret, err := f.uc.Authorize(context.Background(), "fan-1", "res-1", identity)

		require.NoError(t, err)
		assert.Equal(t, "secret_1", secret)
	})

	t.Run("card declined, fan retries", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/reservation/authorize "Card declined, fan retries"
		f := newReservationFixture(t, start.Add(5*time.Minute))
		res := fanReservation(start)
		res.AuthorizationRef = "pi_1"
		f.reservations.EXPECT().Get(mock.Anything, entity.ReservationID("res-1")).Return(res, nil)
		f.sales.EXPECT().Get(mock.Anything, entity.TicketSaleID("sale-1"), mock.Anything).Return(saleWith(150, 0, 2), nil)
		f.auth.EXPECT().CreateAuthorization(mock.Anything, int64(6000), mock.Anything).Return("pi_1", "secret_1", nil)
		f.reservations.EXPECT().SetAuthorization(mock.Anything, entity.ReservationID("res-1"), identity, "pi_1").Return(nil)
		f.users.EXPECT().UpdateHolderIdentity(mock.Anything, "fan-1", identity).Return(&entity.User{ID: "fan-1"}, nil)

		secret, err := f.uc.Authorize(context.Background(), "fan-1", "res-1", identity)

		require.NoError(t, err)
		assert.Equal(t, "secret_1", secret, "the same hold for another attempt")
	})

	t.Run("someone else's checkout", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/reservation/authorize "Someone else's checkout"
		f := newReservationFixture(t, start)
		f.reservations.EXPECT().Get(mock.Anything, entity.ReservationID("res-1")).Return(fanReservation(start), nil)

		_, err := f.uc.Authorize(context.Background(), "fan-2", "res-1", identity)

		assert.ErrorIs(t, err, apperr.ErrPermissionDenied)
	})

	t.Run("hold lapsed", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/reservation/authorize "Hold lapsed"
		f := newReservationFixture(t, start.Add(16*time.Minute))
		f.reservations.EXPECT().Get(mock.Anything, entity.ReservationID("res-1")).Return(fanReservation(start), nil)

		_, err := f.uc.Authorize(context.Background(), "fan-1", "res-1", identity)

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
	})
}

func TestReservationUseCase_ReleaseExpired(t *testing.T) {
	t.Parallel()
	start := time.Date(2026, 11, 5, 18, 0, 0, 0, tsJST)
	now := start.Add(16 * time.Minute)

	t.Run("fan walked away", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/reservation/release-expired "Fan walked away"
		f := newReservationFixture(t, now)
		lapsed := fanReservation(start)
		lapsed.AuthorizationRef = "pi_1"
		f.reservations.EXPECT().ListDue(mock.Anything, now).Return([]*entity.Reservation{lapsed}, nil).Once()
		f.reservations.EXPECT().Release(mock.Anything, entity.ReservationID("res-1"), now).Return(true, nil).Once()

		require.NoError(t, f.uc.ReleaseExpired(context.Background()))

		// The next run finds it Expired with its card hold and gives it back.
		expired := *lapsed
		expired.Status = entity.ReservationStatusExpired
		f.reservations.EXPECT().ListDue(mock.Anything, now).Return([]*entity.Reservation{&expired}, nil).Once()
		f.auth.EXPECT().CancelAuthorization(mock.Anything, "pi_1").Return(nil)
		f.reservations.EXPECT().RecordAuthorizationRelease(mock.Anything, entity.ReservationID("res-1"), now).Return(nil)

		require.NoError(t, f.uc.ReleaseExpired(context.Background()))
	})

	t.Run("count changed after authorizing", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/reservation/release-expired "Count changed after authorizing"
		f := newReservationFixture(t, now)
		replaced := fanReservation(start)
		replaced.Status, replaced.AuthorizationRef = entity.ReservationStatusReleased, "pi_2"
		f.reservations.EXPECT().ListDue(mock.Anything, now).Return([]*entity.Reservation{replaced}, nil)
		f.auth.EXPECT().CancelAuthorization(mock.Anything, "pi_2").Return(nil)
		f.reservations.EXPECT().RecordAuthorizationRelease(mock.Anything, entity.ReservationID("res-1"), now).Return(nil)

		require.NoError(t, f.uc.ReleaseExpired(context.Background()))
	})

	t.Run("committed at the last moment", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/reservation/release-expired "Committed at the last moment"
		f := newReservationFixture(t, now)
		lapsed := fanReservation(start)
		lapsed.AuthorizationRef = "pi_3"
		f.reservations.EXPECT().ListDue(mock.Anything, now).Return([]*entity.Reservation{lapsed}, nil)
		// Committed meanwhile: Release changes nothing, and no card hold is cancelled.
		f.reservations.EXPECT().Release(mock.Anything, entity.ReservationID("res-1"), now).Return(false, nil)

		require.NoError(t, f.uc.ReleaseExpired(context.Background()))
	})

	t.Run("cancellation fails", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/reservation/release-expired "Cancellation fails"
		f := newReservationFixture(t, now)
		first := fanReservation(start)
		first.ID, first.Status, first.AuthorizationRef = "res-a", entity.ReservationStatusExpired, "pi_a"
		second := fanReservation(start)
		second.ID, second.Status, second.AuthorizationRef = "res-b", entity.ReservationStatusExpired, "pi_b"
		f.reservations.EXPECT().ListDue(mock.Anything, now).Return([]*entity.Reservation{first, second}, nil)
		f.auth.EXPECT().CancelAuthorization(mock.Anything, "pi_a").Return(apperr.New(apperr.ErrUnavailable.Code, "stripe down"))
		f.auth.EXPECT().CancelAuthorization(mock.Anything, "pi_b").Return(nil)
		f.reservations.EXPECT().RecordAuthorizationRelease(mock.Anything, entity.ReservationID("res-b"), now).Return(nil)

		require.NoError(t, f.uc.ReleaseExpired(context.Background()))
	})

	t.Run("money taken on an ended checkout", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/reservation/release-expired "Money taken on an ended checkout"
		f := newReservationFixture(t, now)
		released := fanReservation(start)
		released.Status, released.AuthorizationRef = entity.ReservationStatusReleased, "pi_charged"
		f.reservations.EXPECT().ListDue(mock.Anything, now).Return([]*entity.Reservation{released}, nil)
		f.auth.EXPECT().CancelAuthorization(mock.Anything, "pi_charged").
			Return(apperr.New(apperr.ErrFailedPrecondition.Code, "the card hold has already been charged"))

		// Reported (logged as needing an operator) and not recorded as released.
		require.NoError(t, f.uc.ReleaseExpired(context.Background()))
	})

	t.Run("listing fails", func(t *testing.T) {
		t.Parallel()
		f := newReservationFixture(t, now)
		f.reservations.EXPECT().ListDue(mock.Anything, now).Return(nil, apperr.ErrInternal)

		assert.ErrorIs(t, f.uc.ReleaseExpired(context.Background()), apperr.ErrInternal)
	})
}
