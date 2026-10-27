package payment_test

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/payment"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	stripe "github.com/stripe/stripe-go/v87"
)

// TestStripeCardAuthorizationRepository_Integration exercises the real Stripe
// test API for the manual-capture card authorization of a lottery entry and of
// a checkout: open, read, capture or release, each idempotent.
//
// The test is opt-in so `make check` and CI (which have no Stripe key and no
// network dependency on Stripe) skip it. Run it locally with:
//
//	STRIPE_INTEGRATION_TEST=1 STRIPE_SECRET_KEY=sk_test_… \
//	  go test ./internal/infrastructure/payment/ -run Integration -v
func TestStripeCardAuthorizationRepository_Integration(t *testing.T) {
	key := stripeTestKey(t)
	ctx := context.Background()
	lottery := payment.NewStripeCardAuthorizationRepository(key, payment.LotteryCardAuthorizationPolicy, mustLogger(t))
	checkout := payment.NewStripeCardAuthorizationRepository(key, payment.CheckoutCardAuthorizationPolicy, mustLogger(t))
	const amountJPY int64 = 6000
	fanID, ticketTypeID, eventID := entity.NewID(), entity.NewID(), entity.NewID()

	// open opens a card authorization for a fresh checkout, so every subtest
	// has its own idempotency keys.
	open := func(t *testing.T, repo *payment.StripeCardAuthorizationRepository) (*entity.CardAuthorization, entity.CardAuthorizationPurchase) {
		t.Helper()
		purchase := entity.CardAuthorizationPurchase{
			Purpose:      entity.CardAuthorizationPurposeCheckout,
			CheckoutID:   entity.CheckoutID(entity.NewID()),
			UserID:       entity.UserID(entity.NewID()),
			TicketTypeID: entity.TicketTypeID(entity.NewID()),
			EventID:      entity.NewID(),
			TicketCount:  2,
		}
		got, err := repo.Create(ctx, purchase, amountJPY)
		require.NoError(t, err)
		require.NotEmpty(t, got.Secret)
		return got, purchase
	}

	t.Run("opened and retried after a lost response", func(t *testing.T) {
		// @spec components/entity/card-authorization/create "Card authorization opened"
		// @spec components/entity/card-authorization/create "Retried after a lost response"
		opened, purchase := open(t, checkout)
		pi := retrievePaymentIntent(t, key, opened.ID)
		assert.Equal(t, amountJPY, pi.Amount)
		assert.Equal(t, stripe.PaymentIntentCaptureMethodManual, pi.CaptureMethod)
		assert.Equal(t, string(purchase.CheckoutID), pi.Metadata["checkout_id"])

		again, err := checkout.Create(ctx, purchase, amountJPY)
		require.NoError(t, err)
		assert.Equal(t, opened.ID, again.ID)
		require.NoError(t, checkout.Release(ctx, opened.ID))
	})

	t.Run("read before and after card authentication", func(t *testing.T) {
		opened, purchase := open(t, checkout)
		pending, err := checkout.Get(ctx, opened.ID)
		require.NoError(t, err)
		assert.Equal(t, entity.CardAuthorizationStatusPending, pending.Status)
		assert.Equal(t, opened.Secret, pending.Secret)

		confirmWith(t, key, opened.ID, "pm_card_amex")
		authorized, err := checkout.Get(ctx, opened.ID)
		require.NoError(t, err)
		assert.Equal(t, entity.CardAuthorizationStatusAuthorized, authorized.Status)
		require.NoError(t, authorized.Confirm(purchase, amountJPY), "a checkout accepts American Express")
		assert.Error(t, authorized.Confirm(entity.CardAuthorizationPurchase{Purpose: entity.CardAuthorizationPurposeLotteryEntry}, amountJPY),
			"a lottery entry does not")
		assert.Equal(t, purchase, authorized.Purchase, "the checkout it was opened for")
		require.NoError(t, checkout.Release(ctx, opened.ID))
	})

	t.Run("lottery entry authorized again, then read", func(t *testing.T) {
		// @spec components/entity/card-authorization/create "Lottery entry authorized again"
		// @spec components/entity/card-authorization/get "Waiting for card authentication"
		// @spec components/entity/card-authorization/get "Authorized card authorization"
		code := entity.ReferralCode("member-a")
		entry := func(count int) entity.CardAuthorizationPurchase {
			return entity.CardAuthorizationPurchase{
				Purpose:      entity.CardAuthorizationPurposeLotteryEntry,
				UserID:       entity.UserID(fanID),
				TicketTypeID: entity.TicketTypeID(ticketTypeID),
				EventID:      eventID,
				TicketCount:  count,
				ReferralCode: &code,
			}
		}
		two, err := lottery.Create(ctx, entry(2), 16000)
		require.NoError(t, err)
		three, err := lottery.Create(ctx, entry(3), 24000)
		require.NoError(t, err)
		require.NotEqual(t, two.ID, three.ID, "each call opens a new card authorization")

		pending, err := lottery.Get(ctx, two.ID)
		require.NoError(t, err)
		assert.Equal(t, entity.CardAuthorizationStatusPending, pending.Status)
		assert.Equal(t, two.Secret, pending.Secret)
		assert.Equal(t, entry(2), pending.Purchase)

		confirmWith(t, key, three.ID, "pm_card_visa")
		authorized, err := lottery.Get(ctx, three.ID)
		require.NoError(t, err)
		assert.Equal(t, entity.CardAuthorizationStatusAuthorized, authorized.Status)
		assert.Equal(t, int64(24000), authorized.Amount)
		assert.Equal(t, entity.CardBrand("visa"), authorized.CardBrand)
		assert.Equal(t, entry(3), authorized.Purchase)
		assert.Nil(t, authorized.CapturedPayment, "nothing is charged")

		require.NoError(t, lottery.Release(ctx, two.ID))
		require.NoError(t, lottery.Release(ctx, three.ID))
	})

	t.Run("unknown id", func(t *testing.T) {
		// @spec components/entity/card-authorization/get "Unknown id"
		_, err := lottery.Get(ctx, "pi_does_not_exist")
		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})

	t.Run("capture once, then read the captured payment", func(t *testing.T) {
		opened, _ := open(t, lottery)
		confirmWith(t, key, opened.ID, "pm_card_visa")

		require.NoError(t, lottery.Capture(ctx, opened.ID))
		require.NoError(t, lottery.Capture(ctx, opened.ID), "a retried capture charges once")

		captured, err := lottery.Get(ctx, opened.ID)
		require.NoError(t, err)
		require.NotNil(t, captured.CapturedPayment)
		assert.Equal(t, amountJPY, captured.CapturedPayment.AmountJPY)
		assert.Equal(t, "JPY", captured.CapturedPayment.Currency)
		assert.Equal(t, "visa", captured.CapturedPayment.CardBrand)
		assert.Equal(t, "4242", captured.CapturedPayment.CardLast4)

		assert.ErrorIs(t, lottery.Release(ctx, opened.ID), apperr.ErrFailedPrecondition)
	})

	t.Run("release once, then capture fails", func(t *testing.T) {
		opened, _ := open(t, checkout)
		confirmWith(t, key, opened.ID, "pm_card_visa")

		require.NoError(t, checkout.Release(ctx, opened.ID))
		require.NoError(t, checkout.Release(ctx, opened.ID))
		assert.ErrorIs(t, checkout.Capture(ctx, opened.ID), apperr.ErrFailedPrecondition)
	})
}

// stripeTestKey returns the test-mode Stripe key of an opt-in run, or skips.
func stripeTestKey(t *testing.T) string {
	t.Helper()
	if os.Getenv("STRIPE_INTEGRATION_TEST") != "1" {
		t.Skip("opt-in: set STRIPE_INTEGRATION_TEST=1 and STRIPE_SECRET_KEY=sk_test_… to run")
	}
	key := os.Getenv("STRIPE_SECRET_KEY")
	// Accept both a secret (sk_test_…) and a restricted (rk_test_…) test-mode
	// key; a restricted key is the recommended shape for sandboxes.
	if !strings.HasPrefix(key, "sk_test_") && !strings.HasPrefix(key, "rk_test_") {
		t.Skip("STRIPE_SECRET_KEY must be a test-mode key (sk_test_… or rk_test_…) to run the integration test")
	}
	return key
}

// confirmWith confirms the PaymentIntent with a non-3DS test payment method,
// driving it to requires_capture, as the frontend does after card
// authentication.
func confirmWith(t *testing.T, key, id, paymentMethod string) {
	t.Helper()
	pi, err := stripe.NewClient(key).V1PaymentIntents.Confirm(context.Background(), id, &stripe.PaymentIntentConfirmParams{
		PaymentMethod: stripe.String(paymentMethod),
	})
	require.NoError(t, err)
	require.Equal(t, stripe.PaymentIntentStatusRequiresCapture, pi.Status)
}

// retrievePaymentIntent reads a PaymentIntent with a per-call client.
func retrievePaymentIntent(t *testing.T, key, id string) *stripe.PaymentIntent {
	t.Helper()
	pi, err := stripe.NewClient(key).V1PaymentIntents.Retrieve(context.Background(), id, nil)
	require.NoError(t, err)
	return pi
}
