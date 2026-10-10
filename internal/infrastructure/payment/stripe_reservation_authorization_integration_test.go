package payment_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/payment"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	stripe "github.com/stripe/stripe-go/v86"
)

// TestStripeReservationAuthorizationPort_Integration runs the checkout's card
// hold against the real Stripe test API. It is opt-in like
// TestStripeAuthorizationPort_Integration:
//
//	STRIPE_INTEGRATION_TEST=1 STRIPE_SECRET_KEY=sk_test_… \
//	  go test ./internal/infrastructure/payment/ -run Integration -v
func TestStripeReservationAuthorizationPort_Integration(t *testing.T) {
	if os.Getenv("STRIPE_INTEGRATION_TEST") != "1" {
		t.Skip("opt-in: set STRIPE_INTEGRATION_TEST=1 and STRIPE_SECRET_KEY=sk_test_… to run")
	}
	key := os.Getenv("STRIPE_SECRET_KEY")
	if !strings.HasPrefix(key, "sk_test_") && !strings.HasPrefix(key, "rk_test_") {
		t.Skip("STRIPE_SECRET_KEY must be a test-mode key (sk_test_… or rk_test_…) to run the integration test")
	}

	port := payment.NewStripeReservationAuthorizationPort(key, mustLogger(t))
	ctx := context.Background()
	const amountJPY int64 = 6000

	// newHold opens a hold for a fresh reservation id, so every subtest has
	// its own idempotency keys.
	newHold := func(t *testing.T) (string, entity.AuthorizationMetadata) {
		t.Helper()
		meta := entity.AuthorizationMetadata{
			ReservationID: entity.ReservationID(entity.NewID()),
			TicketSaleID:  entity.TicketSaleID(entity.NewID()),
			EventID:       entity.NewID(),
		}
		ref, secret, err := port.CreateAuthorization(ctx, amountJPY, meta)
		require.NoError(t, err)
		require.NotEmpty(t, secret)
		return ref, meta
	}

	t.Run("hold opened and retried after a lost response", func(t *testing.T) {
		// @spec components/entity/reservation/create-authorization "Hold opened"
		ref, meta := newHold(t)
		pi := retrievePaymentIntent(t, key, ref)
		assert.Equal(t, amountJPY, pi.Amount)
		assert.Equal(t, stripe.PaymentIntentCaptureMethodManual, pi.CaptureMethod)
		assert.Equal(t, string(meta.ReservationID), pi.Metadata["reservation_id"])

		// @spec components/entity/reservation/create-authorization "Retried after a lost response"
		again, _, err := port.CreateAuthorization(ctx, amountJPY, meta)
		require.NoError(t, err)
		assert.Equal(t, ref, again)
		require.NoError(t, port.CancelAuthorization(ctx, ref))
	})

	t.Run("verify a hold", func(t *testing.T) {
		ref, _ := newHold(t)
		// @spec components/entity/reservation/verify-authorization "Not authenticated"
		assert.ErrorIs(t, port.VerifyAuthorization(ctx, ref, amountJPY), apperr.ErrFailedPrecondition)

		confirmWith(t, key, ref, "pm_card_visa")
		// @spec components/entity/reservation/verify-authorization "Valid hold"
		require.NoError(t, port.VerifyAuthorization(ctx, ref, amountJPY))
		// @spec components/entity/reservation/verify-authorization "Amount mismatch"
		assert.ErrorIs(t, port.VerifyAuthorization(ctx, ref, 3000), apperr.ErrInvalidArgument)
		require.NoError(t, port.CancelAuthorization(ctx, ref))
	})

	t.Run("American Express card", func(t *testing.T) {
		// @spec components/entity/reservation/verify-authorization "American Express card"
		ref, _ := newHold(t)
		confirmWith(t, key, ref, "pm_card_amex")

		require.NoError(t, port.VerifyAuthorization(ctx, ref, amountJPY))
		require.NoError(t, port.CancelAuthorization(ctx, ref))
	})

	t.Run("capture once", func(t *testing.T) {
		ref, _ := newHold(t)
		confirmWith(t, key, ref, "pm_card_visa")

		// @spec components/entity/reservation/capture-authorization "Hold captured"
		first, err := port.CaptureAuthorization(ctx, ref)
		require.NoError(t, err)
		assert.Equal(t, ref, first.PaymentIntentRef)
		assert.Equal(t, amountJPY, first.AmountJPY)
		assert.Equal(t, "visa", first.CardBrand)
		assert.Equal(t, "4242", first.CardLast4)

		// @spec components/entity/reservation/capture-authorization "Repeated capture"
		second, err := port.CaptureAuthorization(ctx, ref)
		require.NoError(t, err)
		assert.Equal(t, first, second)

		// @spec components/entity/reservation/cancel-authorization "Charged hold"
		assert.ErrorIs(t, port.CancelAuthorization(ctx, ref), apperr.ErrFailedPrecondition)
	})

	t.Run("release a hold", func(t *testing.T) {
		ref, _ := newHold(t)
		confirmWith(t, key, ref, "pm_card_visa")

		// @spec components/entity/reservation/cancel-authorization "Hold released"
		require.NoError(t, port.CancelAuthorization(ctx, ref))
		// @spec components/entity/reservation/cancel-authorization "Repeated release"
		require.NoError(t, port.CancelAuthorization(ctx, ref))

		// @spec components/entity/reservation/capture-authorization "Hold no longer capturable"
		_, err := port.CaptureAuthorization(ctx, ref)
		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
	})
}

// TestStripeReservationAuthorizationPort_Unavailable checks that an
// unreachable payment service is reported as Unavailable.
func TestStripeReservationAuthorizationPort_Unavailable(t *testing.T) {
	t.Parallel()

	// @spec components/entity/reservation/create-authorization "Card payments unavailable"
	port := payment.NewStripeReservationAuthorizationPortForTest("sk_test_stub", "http://127.0.0.1:1", mustLogger(t))

	// Bound the client's network retries.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, _, err := port.CreateAuthorization(ctx, 6000, entity.AuthorizationMetadata{ReservationID: "res-1"})

	assert.ErrorIs(t, err, apperr.ErrUnavailable)
}

// confirmWith confirms the PaymentIntent with a non-3DS test payment method,
// driving it to requires_capture.
func confirmWith(t *testing.T, key, ref, paymentMethod string) {
	t.Helper()
	sc := stripe.NewClient(key)
	pi, err := sc.V1PaymentIntents.Confirm(context.Background(), ref, &stripe.PaymentIntentConfirmParams{
		PaymentMethod: stripe.String(paymentMethod),
	})
	require.NoError(t, err)
	require.Equal(t, stripe.PaymentIntentStatusRequiresCapture, pi.Status)
}

// retrievePaymentIntent reads a PaymentIntent with a per-call client.
func retrievePaymentIntent(t *testing.T, key, ref string) *stripe.PaymentIntent {
	t.Helper()
	pi, err := stripe.NewClient(key).V1PaymentIntents.Retrieve(context.Background(), ref, nil)
	require.NoError(t, err)
	return pi
}
