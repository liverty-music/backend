package usecase_test

import (
	"context"
	"strings"
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

// TestNeedsOperatorReports checks that the two operator reports are logged
// under LogKeyNeedsOperator, with the reservation id and payment reference,
// which the log-based alert pages on.
func TestNeedsOperatorReports(t *testing.T) {
	t.Parallel()

	t.Run("money taken on an ended checkout", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/reservation/release-expired "Money taken on an ended checkout"
		logger, buf := newCaptureLogger(t)
		now := coStart.Add(16 * time.Minute)
		reservations := entitymocks.NewMockReservationRepository(t)
		auth := entitymocks.NewMockReservationAuthorizationPort(t)
		uc := usecase.NewReservationUseCase(reservations, entitymocks.NewMockTicketSaleRepository(t), entitymocks.NewMockOrderRepository(t),
			entitymocks.NewMockUserRepository(t), ucmocks.NewMockEventPublishStatePort(t), auth, fixedClock(now), logger)
		released := fanReservation(coStart)
		released.Status, released.AuthorizationRef = entity.ReservationStatusReleased, "pi_charged"
		reservations.EXPECT().ListDue(mock.Anything, now).Return([]*entity.Reservation{released}, nil).Twice()
		auth.EXPECT().CancelAuthorization(mock.Anything, "pi_charged").
			Return(apperr.New(apperr.ErrFailedPrecondition.Code, "already charged")).Twice()

		// Reported on every run until it is resolved.
		require.NoError(t, uc.ReleaseExpired(context.Background()))
		require.NoError(t, uc.ReleaseExpired(context.Background()))

		out := buf.String()
		assert.Equal(t, 2, strings.Count(out, usecase.LogKeyNeedsOperator))
		assert.Contains(t, out, `"reservation_id":"res-1"`)
		assert.Contains(t, out, `"payment_ref":"pi_charged"`)
	})

	t.Run("charged checkout left unissued", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/order/issue-due-reservations "Organizer record broken"
		logger, buf := newCaptureLogger(t)
		now := coStart.Add(31 * time.Minute) // charged at 18:11, reported from 18:21
		reservations := entitymocks.NewMockReservationRepository(t)
		charged := committedCopy(authorizedReservation(), true)
		reservations.EXPECT().ListDue(mock.Anything, now).Return([]*entity.Reservation{charged}, nil)
		reservations.EXPECT().Serialize(mock.Anything, mock.Anything, mock.Anything).Return(apperr.ErrInternal)
		uc := usecase.NewIssuanceUseCase(usecase.IssuanceDeps{ReservationRepo: reservations, Clock: fixedClock(now), Logger: logger})

		require.NoError(t, uc.IssueDueReservations(context.Background()))

		out := buf.String()
		assert.Contains(t, out, usecase.LogKeyNeedsOperator)
		assert.Contains(t, out, `"payment_ref":"pi_1"`)
	})

	t.Run("a fresh charge is not reported", func(t *testing.T) {
		t.Parallel()
		logger, buf := newCaptureLogger(t)
		now := coStart.Add(15 * time.Minute) // charged 4 minutes ago
		reservations := entitymocks.NewMockReservationRepository(t)
		reservations.EXPECT().ListDue(mock.Anything, now).Return([]*entity.Reservation{committedCopy(authorizedReservation(), true)}, nil)
		reservations.EXPECT().Serialize(mock.Anything, mock.Anything, mock.Anything).Return(apperr.ErrInternal)
		uc := usecase.NewIssuanceUseCase(usecase.IssuanceDeps{ReservationRepo: reservations, Clock: fixedClock(now), Logger: logger})

		require.NoError(t, uc.IssueDueReservations(context.Background()))

		assert.NotContains(t, buf.String(), usecase.LogKeyNeedsOperator)
	})
}
