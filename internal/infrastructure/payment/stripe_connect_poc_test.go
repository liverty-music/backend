package payment_test

import (
	"context"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	stripe "github.com/stripe/stripe-go/v86"
)

// TestStripeConnect_Settlement_PoC is a PROOF-OF-CONCEPT for the money-out layer
// that the `ticket-settlement-and-payout` capability will own (NOT ⑤). It
// exercises the Stripe Connect **separate charges & transfers** loop against a
// Stripe Sandbox (test mode), validating that the latest stripe-go (v86, unified
// stripe.Client) does what the settlement design (#940) needs:
//
//	platform charge (funds land on the platform balance)
//	  → Transfer of the Organizer's NET share to the connected account
//	    (source_transaction = the charge; platform fee kept as the retained remainder)
//	  → Payout of the connected account's balance to its bank (optional; test mode).
//
// This is the interim "works in prod with a single Liverty Music-owned Connect
// account" model: create ONE recipient connected account (its acct_… id passed
// via env), route all transfers there, switch to per-Organizer accounts once
// real Organizer onboarding ships. Connect account CREATION / onboarding
// (Accounts v2, `/v2/core/accounts`) is the Organizer-onboarding concern and is
// out of scope for this money-out PoC — create the sandbox connected account
// once in the Dashboard (or a one-off script) and pass its id here.
//
// Opt-in only, so `make check` / CI (no Stripe key, no network) skip it. Run with:
//
//	STRIPE_CONNECT_POC=1 \
//	STRIPE_SECRET_KEY=sk_test_… \
//	STRIPE_CONNECT_ACCOUNT=acct_… \
//	  go test ./internal/infrastructure/payment/ -run TestStripeConnect_Settlement_PoC -v
//
// Notes for a faithful settlement implementation (beyond this PoC):
//   - NEVER use application_fee_amount with separate charges & transfers — the fee
//     is the amount NOT transferred (demonstrated below).
//   - on_behalf_of = Organizer belongs on the CHARGE (④'s PaymentIntent), making
//     the Organizer the settlement merchant; it is not a Transfer parameter.
//   - Gate transfers on the recipient capability
//     (configuration.recipient.capabilities.stripe_balance.stripe_transfers == active).
//   - losses_collector = application (platform absorbs negative balances / dispute
//     transfer_reversal).
func TestStripeConnect_Settlement_PoC(t *testing.T) {
	if os.Getenv("STRIPE_CONNECT_POC") != "1" {
		t.Skip("opt-in: set STRIPE_CONNECT_POC=1, STRIPE_SECRET_KEY=sk_test_…, STRIPE_CONNECT_ACCOUNT=acct_… to run")
	}
	secretKey := os.Getenv("STRIPE_SECRET_KEY")
	require.NotEmpty(t, secretKey, "STRIPE_SECRET_KEY (sandbox sk_test_…) is required")
	connectedAccount := os.Getenv("STRIPE_CONNECT_ACCOUNT")
	require.NotEmpty(t, connectedAccount, "STRIPE_CONNECT_ACCOUNT (sandbox acct_…) is required")

	ctx := context.Background()
	backends := &stripe.Backends{
		API: stripe.GetBackendWithConfig(stripe.APIBackend, &stripe.BackendConfig{
			HTTPClient: &http.Client{Timeout: 30 * time.Second},
		}),
	}
	sc := stripe.NewClient(secretKey, stripe.WithBackends(backends))

	const (
		gross = int64(10000) // ¥10,000 ticket
		fee   = int64(1000)  // 10% platform fee (kept on the platform balance)
		net   = gross - fee  // ¥9,000 transferred to the Organizer
	)

	// 1. Platform charge — a plain PaymentIntent (no transfer_data), so the funds
	//    land on the PLATFORM balance. This mirrors ④'s shipped charge topology.
	piParams := &stripe.PaymentIntentCreateParams{
		Amount:        stripe.Int64(gross),
		Currency:      stripe.String(string(stripe.CurrencyJPY)),
		PaymentMethod: stripe.String("pm_card_visa"), // test PaymentMethod
		Confirm:       stripe.Bool(true),
		AutomaticPaymentMethods: &stripe.PaymentIntentCreateAutomaticPaymentMethodsParams{
			Enabled:        stripe.Bool(true),
			AllowRedirects: stripe.String(string(stripe.PaymentIntentAutomaticPaymentMethodsAllowRedirectsNever)),
		},
	}
	piParams.AddExpand("latest_charge")

	pi, err := sc.V1PaymentIntents.Create(ctx, piParams)
	require.NoError(t, err, "create+confirm platform PaymentIntent")
	require.Equal(t, stripe.PaymentIntentStatusSucceeded, pi.Status, "test charge should succeed")
	require.NotNil(t, pi.LatestCharge, "latest_charge should be expanded")
	chargeID := pi.LatestCharge.ID
	t.Logf("platform charge succeeded: pi=%s charge=%s amount=%d", pi.ID, chargeID, pi.Amount)

	// 2. Transfer the Organizer's NET share to the connected account, tying it to
	//    the originating charge via source_transaction. The platform fee is simply
	//    the portion NOT transferred (no application_fee_amount).
	tr, err := sc.V1Transfers.Create(ctx, &stripe.TransferCreateParams{
		Amount:            stripe.Int64(net),
		Currency:          stripe.String(string(stripe.CurrencyJPY)),
		Destination:       stripe.String(connectedAccount),
		SourceTransaction: stripe.String(chargeID),
	})
	require.NoError(t, err, "transfer net share to the connected account")
	require.Equal(t, net, tr.Amount)
	t.Logf("transfer to organizer succeeded: transfer=%s destination=%s net=%d (fee kept=%d)",
		tr.ID, connectedAccount, tr.Amount, fee)

	// 3. (Optional) Payout the connected account's balance to its bank. In test
	//    mode the just-transferred funds may not be immediately available, so this
	//    is best-effort and only attempted when explicitly requested.
	if os.Getenv("STRIPE_CONNECT_POC_PAYOUT") == "1" {
		payoutParams := &stripe.PayoutCreateParams{
			Amount:   stripe.Int64(net),
			Currency: stripe.String(string(stripe.CurrencyJPY)),
		}
		payoutParams.SetStripeAccount(connectedAccount) // payout ON the connected account
		po, err := sc.V1Payouts.Create(ctx, payoutParams)
		if err != nil {
			// Balance-availability timing in test mode is expected; log, don't fail.
			t.Logf("payout skipped (expected in test mode until balance settles): %v", err)
		} else {
			t.Logf("payout to organizer bank succeeded: payout=%s amount=%d status=%s", po.ID, po.Amount, po.Status)
		}
	}
}
