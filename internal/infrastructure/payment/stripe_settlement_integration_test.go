package payment_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/payment"
	"github.com/liverty-music/backend/internal/usecase"
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

// TestStripeSettlementPort_MoneyOut_Integration walks the whole money-out path
// through the production adapter against a Stripe sandbox: a captured platform
// charge, the pi_ → ch_ resolution, a Transfer pinned to that charge, a buyer
// refund, and a transfer reversal.
//
// The usecase tests already cover the *decisions* — release gate, splits, refund
// policy, idempotent replay — but they run against stubs, so they only prove we
// called the port. The Connect PoC exercises Stripe for real but drives the SDK
// directly, bypassing the port entirely. Neither proves that the parameters our
// adapter actually sends are accepted, which is the gap that let
// CreateConnectedAccount ship a request Stripe rejected with a 400.
//
// Opt-in so `make check` and CI stay network-free. Run it with:
//
//	STRIPE_INTEGRATION_TEST=1 STRIPE_SECRET_KEY=sk_test_… \
//	  go test ./internal/infrastructure/payment/ -run MoneyOut_Integration -v
func TestStripeSettlementPort_MoneyOut_Integration(t *testing.T) {
	if os.Getenv("STRIPE_INTEGRATION_TEST") != "1" {
		t.Skip("opt-in: set STRIPE_INTEGRATION_TEST=1 and STRIPE_SECRET_KEY=sk_test_… to run")
	}
	key := os.Getenv("STRIPE_SECRET_KEY")
	if !strings.HasPrefix(key, "sk_test_") && !strings.HasPrefix(key, "rk_test_") {
		t.Skip("STRIPE_SECRET_KEY must be a test-mode key (sk_test_… or rk_test_…) to run the integration test")
	}

	logger := mustLogger(t)
	settlement := payment.NewStripeSettlementPort(key, logger)
	authorization := payment.NewStripeAuthorizationPort(key, logger)
	ctx := context.Background()

	// The payee must already be able to receive transfers. Provisioning a fresh
	// recipient would leave it unverified, so reuse one the Connect PoC has
	// already driven to active; skip rather than fail if the sandbox has none,
	// since that is a fixture gap and not a defect in the code under test.
	payeeAccountRef := findActiveRecipient(ctx, t, key)

	const (
		grossJPY    int64 = 5000 // what the buyer pays
		platformFee int64 = 500  // retained as the un-transferred remainder
		netJPY            = grossJPY - platformFee
	)

	// ── Capture: buyer funds land on the PLATFORM balance ──────────────────
	// A plain platform charge, not a destination charge: settlement to the
	// Organizer happens later, via Transfer.
	paymentIntentRef, _, err := authorization.CreateAuthorization(ctx, grossJPY)
	require.NoError(t, err, "authorize the buyer's hold")
	confirmWithTestCard(t, key, paymentIntentRef)
	require.NoError(t, authorization.CaptureAuthorization(ctx, paymentIntentRef), "capture the hold")

	// ── Resolve pi_ → ch_ ──────────────────────────────────────────────────
	// Transfer.source_transaction needs the charge, not the PaymentIntent.
	// Passing pi_ here is the mistake a stubbed test can never catch.
	chargeRef, err := settlement.ResolveChargeRef(ctx, paymentIntentRef)
	require.NoError(t, err, "resolve the captured charge from the PaymentIntent")
	require.True(t, strings.HasPrefix(chargeRef, "ch_"),
		"source_transaction must be a charge reference, got %q", chargeRef)

	settlementID := entity.SettlementID("it-settlement-" + time.Now().UTC().Format("20060102T150405.000"))

	// ── Transfer the Organizer's net share ─────────────────────────────────
	transferRef, err := settlement.CreateTransfer(ctx, usecase.TransferParams{
		SettlementID:         settlementID,
		PayeeAccountRef:      payeeAccountRef,
		SourceTransactionRef: chargeRef,
		Amount:               netJPY,
		Currency:             string(stripe.CurrencyJPY),
	})
	require.NoError(t, err, "transfer the Organizer's net share")
	require.True(t, strings.HasPrefix(transferRef, "tr_"), "expected a tr_ reference, got %q", transferRef)
	t.Logf("charge=%s transfer=%s net=%d fee=%d", chargeRef, transferRef, netJPY, platformFee)

	// The platform fee is the remainder that was never transferred — there is
	// no application_fee and no self-transfer.
	sc := stripe.NewClient(key)
	tr, err := sc.V1Transfers.Retrieve(ctx, transferRef, nil)
	require.NoError(t, err)
	assert.Equal(t, netJPY, tr.Amount, "transfer must carry the net share only")
	require.NotNil(t, tr.SourceTransaction, "transfer must be pinned to the originating charge")
	assert.Equal(t, chargeRef, tr.SourceTransaction.ID)

	// A retried sweep must not double-transfer: the idempotency key is derived
	// from the settlement id, so Stripe replays the original transfer.
	replayRef, err := settlement.CreateTransfer(ctx, usecase.TransferParams{
		SettlementID:         settlementID,
		PayeeAccountRef:      payeeAccountRef,
		SourceTransactionRef: chargeRef,
		Amount:               netJPY,
		Currency:             string(stripe.CurrencyJPY),
	})
	require.NoError(t, err, "a retried transfer must be a safe no-op")
	assert.Equal(t, transferRef, replayRef, "retry must replay the original transfer, not create a second")

	// ── Cancellation: refund the buyer and claw the transfer back ──────────
	orderID := entity.OrderID("it-order-" + time.Now().UTC().Format("20060102T150405.000"))

	refundRef, err := settlement.CreateRefund(ctx, usecase.RefundParams{
		OrderID:   orderID,
		ChargeRef: chargeRef,
		Amount:    grossJPY,
	})
	require.NoError(t, err, "refund the buyer from the platform balance")
	require.True(t, strings.HasPrefix(refundRef, "re_"), "expected an re_ reference, got %q", refundRef)

	reversalRef, err := settlement.ReverseTransfer(ctx, usecase.ReverseTransferParams{
		SettlementID: settlementID,
		TransferRef:  transferRef,
		Amount:       netJPY,
	})
	require.NoError(t, err, "reverse the Organizer's transfer")
	require.True(t, strings.HasPrefix(reversalRef, "trr_"), "expected a trr_ reference, got %q", reversalRef)

	// The reversal must actually return the funds, not merely be recorded.
	tr, err = sc.V1Transfers.Retrieve(ctx, transferRef, nil)
	require.NoError(t, err)
	assert.Equal(t, netJPY, tr.AmountReversed, "the full net share must be clawed back")
	assert.True(t, tr.Reversed, "transfer must be marked reversed")

	// Replaying the clawback must not reverse twice.
	replayReversal, err := settlement.ReverseTransfer(ctx, usecase.ReverseTransferParams{
		SettlementID: settlementID,
		TransferRef:  transferRef,
		Amount:       netJPY,
	})
	require.NoError(t, err, "a retried reversal must be a safe no-op")
	assert.Equal(t, reversalRef, replayReversal, "retry must replay the original reversal")
}

// findActiveRecipient returns a connected account whose transfers capability is
// already active, skipping the test when the sandbox has none. A freshly created
// recipient is unverified and cannot receive transfers, so the money-out legs
// need one the Connect PoC has already driven to active.
func findActiveRecipient(ctx context.Context, t *testing.T, key string) string {
	t.Helper()

	sc := stripe.NewClient(key)
	accounts := sc.V1Accounts.List(ctx, &stripe.AccountListParams{
		ListParams: stripe.ListParams{Limit: stripe.Int64(100)},
	})
	for account, err := range accounts.All(ctx) {
		require.NoError(t, err, "list connected accounts")
		if account.Capabilities != nil && account.Capabilities.Transfers == stripe.AccountCapabilityStatusActive {
			t.Logf("using payee account %s", account.ID)
			return account.ID
		}
	}

	t.Skip("no connected account with an active transfers capability in this sandbox; " +
		"run the Connect PoC (STRIPE_CONNECT_POC=1) once to provision one")
	return ""
}
