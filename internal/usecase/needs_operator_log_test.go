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
// under LogKeyNeedsOperator, with the checkout id and card authorization id,
// which the log-based alert pages on.
func TestNeedsOperatorReports(t *testing.T) {
	t.Parallel()

	t.Run("money taken on an ended checkout", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/checkout/release-expired "Money taken on an ended checkout"
		logger, buf := newCaptureLogger(t)
		now := coStart.Add(16 * time.Minute)
		checkouts := entitymocks.NewMockCheckoutRepository(t)
		auth := entitymocks.NewMockCheckoutAuthorizationPort(t)
		uc := usecase.NewCheckoutUseCase(usecase.CheckoutDeps{
			CheckoutRepo:             checkouts,
			SaleRepo:                 entitymocks.NewMockTicketSaleRepository(t),
			UserRepo:                 entitymocks.NewMockUserRepository(t),
			IdentityVerificationRepo: entitymocks.NewMockIdentityVerificationRepository(t),
			EventState:               ucmocks.NewMockEventPublishStatePort(t),
			Auth:                     auth,
			Clock:                    fixedClock(now),
			Logger:                   logger,
		})
		released := fanCheckout(coStart)
		released.Status, released.CardAuthorizationID = entity.CheckoutStatusReleased, "pi_charged"
		checkouts.EXPECT().ListDue(mock.Anything, now).Return([]*entity.Checkout{released}, nil).Twice()
		auth.EXPECT().CancelAuthorization(mock.Anything, "pi_charged").
			Return(apperr.New(apperr.ErrFailedPrecondition.Code, "already charged")).Twice()

		// Reported on every run until it is resolved.
		require.NoError(t, uc.ReleaseExpired(context.Background()))
		require.NoError(t, uc.ReleaseExpired(context.Background()))

		out := buf.String()
		assert.Equal(t, 2, strings.Count(out, usecase.LogKeyNeedsOperator))
		assert.Contains(t, out, `"checkout_id":"checkout-1"`)
		assert.Contains(t, out, `"card_authorization_id":"pi_charged"`)
	})

	t.Run("charged checkout left unissued", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/order/issue-due-checkouts "Organizer record broken"
		logger, buf := newCaptureLogger(t)
		now := coStart.Add(31 * time.Minute) // charged at 18:11, reported from 18:21
		checkouts := entitymocks.NewMockCheckoutRepository(t)
		charged := committedCopy(authorizedCheckout(), true)
		checkouts.EXPECT().ListDue(mock.Anything, now).Return([]*entity.Checkout{charged}, nil)
		checkouts.EXPECT().Serialize(mock.Anything, mock.Anything, mock.Anything).Return(apperr.ErrInternal)
		uc := usecase.NewOrderUseCase(usecase.OrderDeps{CheckoutRepo: checkouts, Clock: fixedClock(now), Logger: logger})

		require.NoError(t, uc.IssueDueCheckouts(context.Background()))

		out := buf.String()
		assert.Contains(t, out, usecase.LogKeyNeedsOperator)
		assert.Contains(t, out, `"card_authorization_id":"pi_1"`)
	})

	t.Run("a fresh charge is not reported", func(t *testing.T) {
		t.Parallel()
		logger, buf := newCaptureLogger(t)
		now := coStart.Add(15 * time.Minute) // charged 4 minutes ago
		checkouts := entitymocks.NewMockCheckoutRepository(t)
		checkouts.EXPECT().ListDue(mock.Anything, now).Return([]*entity.Checkout{committedCopy(authorizedCheckout(), true)}, nil)
		checkouts.EXPECT().Serialize(mock.Anything, mock.Anything, mock.Anything).Return(apperr.ErrInternal)
		uc := usecase.NewOrderUseCase(usecase.OrderDeps{CheckoutRepo: checkouts, Clock: fixedClock(now), Logger: logger})

		require.NoError(t, uc.IssueDueCheckouts(context.Background()))

		assert.NotContains(t, buf.String(), usecase.LogKeyNeedsOperator)
	})
}
