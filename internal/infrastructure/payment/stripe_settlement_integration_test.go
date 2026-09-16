package payment_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/payment"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	stripe "github.com/stripe/stripe-go/v86"
)

// TestStripeSettlementPort_CreateConnectedAccount_Integration provisions a real
// Accounts v2 recipient against a Stripe sandbox and asserts the account comes
// back shaped the way the settlement design requires.
//
// This is the leg the unit tests cannot cover: they assert our input validation
// against a stub, and the Connect PoC drives Stripe directly without going
// through the port. Only this test proves that the parameters the *production*
// adapter sends are actually accepted by Stripe and produce a payout-capable
// recipient — the class of bug (wrong field, missing MCC, rejected shape) that
// a mocked port can never surface.
//
// It deliberately asserts no personal data is involved: the account is created
// unverified, and the Organizer supplies identity to Stripe via the hosted
// onboarding link.
//
// Opt-in so `make check` and CI stay network-free. Run it with:
//
//	STRIPE_INTEGRATION_TEST=1 STRIPE_SECRET_KEY=sk_test_… \
//	  go test ./internal/infrastructure/payment/ -run CreateConnectedAccount_Integration -v
func TestStripeSettlementPort_CreateConnectedAccount_Integration(t *testing.T) {
	if os.Getenv("STRIPE_INTEGRATION_TEST") != "1" {
		t.Skip("opt-in: set STRIPE_INTEGRATION_TEST=1 and STRIPE_SECRET_KEY=sk_test_… to run")
	}
	key := os.Getenv("STRIPE_SECRET_KEY")
	if !strings.HasPrefix(key, "sk_test_") && !strings.HasPrefix(key, "rk_test_") {
		t.Skip("STRIPE_SECRET_KEY must be a test-mode key (sk_test_… or rk_test_…) to run the integration test")
	}

	logger := mustLogger(t)
	port := payment.NewStripeSettlementPort(key, logger)
	ctx := context.Background()

	// A distinct organizer id per run: the idempotency key is derived from it,
	// so reusing one would replay the previous account instead of creating one.
	organizerID := "it-organizer-" + time.Now().UTC().Format("20060102T150405.000")

	contactEmail := "it-organizer@pannpers.dev"

	accountRef, err := port.CreateConnectedAccount(ctx, organizerID, contactEmail)
	require.NoError(t, err, "create Accounts v2 recipient via the production adapter")
	require.True(t, strings.HasPrefix(accountRef, "acct_"), "expected an acct_ reference, got %q", accountRef)
	t.Logf("provisioned recipient account: %s (organizer %s)", accountRef, organizerID)

	// Read the account back and assert the shape the design requires.
	sc := stripe.NewClient(key)
	acct, err := sc.V1Accounts.GetByID(ctx, accountRef, nil)
	require.NoError(t, err, "retrieve the account we just created")

	require.NotNil(t, acct.Capabilities)
	assert.NotEqual(t, stripe.AccountCapabilityStatusActive, acct.Capabilities.CardPayments,
		"a recipient must not hold card_payments: it is not the merchant of record")
	assert.Equal(t, "JP", string(acct.Country))
	assert.Equal(t, stripe.CurrencyJPY, stripe.Currency(acct.DefaultCurrency))

	require.NotNil(t, acct.Controller, "controller properties drive loss liability")
	require.NotNil(t, acct.Controller.Losses)
	assert.Equal(t, stripe.AccountControllerLossesPaymentsApplication, acct.Controller.Losses.Payments,
		"the platform must absorb negative balances (losses_collector = application)")

	assert.Equal(t, organizerID, acct.Metadata["organizer_id"],
		"metadata must tie the Stripe account back to our Organizer")
	assert.Equal(t, contactEmail, acct.Email,
		"Stripe requires a contact email on a recipient account")

	// Created unverified: we sent no identity, so Stripe still wants it. This is
	// the hosted-onboarding contract, not a failure.
	assert.False(t, acct.PayoutsEnabled,
		"a freshly created recipient must not be payout-ready before onboarding")

	// The port's own status mapping must agree that the account is not yet
	// eligible — this is what gates the payout sweeper.
	status, err := port.GetAccountStatus(ctx, accountRef)
	require.NoError(t, err)
	assert.NotEqual(t, entity.PayoutOnboardingStatusActive, status,
		"status must not report Active before verification completes")

	// The onboarding link is how the Organizer supplies identity to Stripe.
	url, err := port.CreateOnboardingLink(ctx, accountRef, "https://liverty-music.app/organizer/payouts")
	require.NoError(t, err, "create the hosted onboarding link")
	assert.True(t, strings.HasPrefix(url, "https://"), "expected an https onboarding URL, got %q", url)
}
