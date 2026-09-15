package payment_test

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/liverty-music/backend/internal/infrastructure/payment"
	"github.com/stretchr/testify/require"
	stripe "github.com/stripe/stripe-go/v86"
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
	// Accept both a secret (sk_test_…) and a restricted (rk_test_…) test-mode key;
	// a restricted key is the recommended shape for CI/local sandboxes.
	if !strings.HasPrefix(key, "sk_test_") && !strings.HasPrefix(key, "rk_test_") {
		t.Skip("STRIPE_SECRET_KEY must be a test-mode key (sk_test_… or rk_test_…) to run the integration test")
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

	// GetCapturedPayment is the ④→⑤ handoff seam: ⑤ reads ④'s captured winning
	// payment to build the Order. This exercises it against a real captured
	// PaymentIntent (⑤ §7.2 end-to-end verify of the captured-payment read).
	t.Run("GetCapturedPayment reads a captured win (amount + currency + card facets)", func(t *testing.T) {
		ref, _, err := port.CreateAuthorization(ctx, amountJPY)
		require.NoError(t, err)
		confirmWithTestCard(t, key, ref)
		require.NoError(t, port.CaptureAuthorization(ctx, ref))

		got, err := port.GetCapturedPayment(ctx, ref)
		require.NoError(t, err)
		require.Equal(t, amountJPY, got.AmountJPY)
		require.Equal(t, "JPY", got.Currency)
		require.Equal(t, "visa", got.CardBrand) // pm_card_visa
		require.Equal(t, "4242", got.CardLast4)
	})

	t.Run("GetCapturedPayment rejects a not-yet-captured intent", func(t *testing.T) {
		ref, _, err := port.CreateAuthorization(ctx, amountJPY)
		require.NoError(t, err)
		confirmWithTestCard(t, key, ref) // requires_capture, not captured

		_, err = port.GetCapturedPayment(ctx, ref)
		require.Error(t, err) // FailedPrecondition: capture has not settled
		// Clean up the uncaptured hold.
		require.NoError(t, port.CancelAuthorization(ctx, ref))
	})
}

// confirmWithTestCard confirms the PaymentIntent with a non-3DS test card,
// driving it to requires_capture — the state the frontend produces after the
// fan completes 3DS. Uses a per-call client (not the deprecated global key).
func confirmWithTestCard(t *testing.T, key, ref string) {
	t.Helper()
	sc := stripe.NewClient(key)
	pi, err := sc.V1PaymentIntents.Confirm(context.Background(), ref, &stripe.PaymentIntentConfirmParams{
		PaymentMethod: stripe.String("pm_card_visa"),
	})
	require.NoError(t, err)
	require.Equal(t, stripe.PaymentIntentStatusRequiresCapture, pi.Status)
}
