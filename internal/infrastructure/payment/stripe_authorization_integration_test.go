package payment_test

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/liverty-music/backend/internal/infrastructure/payment"
	"github.com/stretchr/testify/require"
	stripe "github.com/stripe/stripe-go/v81"
	"github.com/stripe/stripe-go/v81/paymentintent"
)

// TestStripeAuthorizationPort_Integration exercises the real Stripe test API for
// the manual-capture authorization-hold model: authorize a hold, verify it, and
// then capture (win) or cancel (loss/withdrawal). It also asserts the
// idempotency-key hardening — a retried capture/cancel with the same
// deterministic key is a safe no-op (Stripe replays the original success)
// instead of erroring on an already-captured/cancelled intent, which is what
// keeps the draw job's batch retries correct.
//
// The test is opt-in so `make check` and CI (which have no Stripe key and no
// network dependency on Stripe) skip it. Run it locally with:
//
//	STRIPE_INTEGRATION_TEST=1 STRIPE_SECRET_KEY=sk_test_… \
//	  go test ./internal/infrastructure/payment/ -run Integration -v
func TestStripeAuthorizationPort_Integration(t *testing.T) {
	if os.Getenv("STRIPE_INTEGRATION_TEST") != "1" {
		t.Skip("opt-in: set STRIPE_INTEGRATION_TEST=1 and STRIPE_SECRET_KEY=sk_test_… to run")
	}
	key := os.Getenv("STRIPE_SECRET_KEY")
	if !strings.HasPrefix(key, "sk_test_") {
		t.Skip("STRIPE_SECRET_KEY must be a test-mode key (sk_test_…) to run the integration test")
	}

	logger := mustLogger(t)
	port := payment.NewStripeAuthorizationPort(key, logger)
	ctx := context.Background()

	const amountJPY int64 = 5000

	t.Run("win: authorize → verify → capture (idempotent retry)", func(t *testing.T) {
		ref, _, err := port.CreateAuthorization(ctx, amountJPY)
		require.NoError(t, err)
		confirmWithTestCard(t, key, ref)

		require.NoError(t, port.VerifyAuthorization(ctx, ref, amountJPY))
		require.NoError(t, port.CaptureAuthorization(ctx, ref))
		// A retry with the same deterministic idempotency key replays the
		// original success rather than failing on an already-captured intent.
		require.NoError(t, port.CaptureAuthorization(ctx, ref))
	})

	t.Run("lose: authorize → cancel (idempotent retry)", func(t *testing.T) {
		ref, _, err := port.CreateAuthorization(ctx, amountJPY)
		require.NoError(t, err)
		confirmWithTestCard(t, key, ref)

		require.NoError(t, port.CancelAuthorization(ctx, ref))
		// Retry is a safe no-op via the deterministic idempotency key.
		require.NoError(t, port.CancelAuthorization(ctx, ref))
	})

	t.Run("verify rejects a mismatched amount", func(t *testing.T) {
		ref, _, err := port.CreateAuthorization(ctx, amountJPY)
		require.NoError(t, err)
		confirmWithTestCard(t, key, ref)

		err = port.VerifyAuthorization(ctx, ref, amountJPY+1)
		require.Error(t, err)
		// Clean up the hold so it does not linger.
		require.NoError(t, port.CancelAuthorization(ctx, ref))
	})
}

// confirmWithTestCard confirms the PaymentIntent with a non-3DS test card,
// driving it to requires_capture — the state the frontend produces after the
// fan completes 3DS. Uses a per-call client (not the deprecated global key).
func confirmWithTestCard(t *testing.T, key, ref string) {
	t.Helper()
	client := paymentintent.Client{B: stripe.GetBackend(stripe.APIBackend), Key: key}
	pi, err := client.Confirm(ref, &stripe.PaymentIntentConfirmParams{
		PaymentMethod: stripe.String("pm_card_visa"),
	})
	require.NoError(t, err)
	require.Equal(t, stripe.PaymentIntentStatusRequiresCapture, pi.Status)
}
