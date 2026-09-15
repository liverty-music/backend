package payment_test

import (
	"context"
	"encoding/json"
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
// account" model: route ALL transfers to ONE recipient connected account, then
// switch to per-Organizer accounts once real Organizer onboarding ships. The
// recipient account is either supplied via STRIPE_CONNECT_ACCOUNT (a
// pre-onboarded sandbox acct_…) or self-provisioned by ensureRecipientAccount so
// the PoC needs only a secret key. (Real Organizer onboarding — Accounts v2,
// `/v2/core/accounts`, AccountLinks — remains a separate concern; the self-
// provisioning here is a test convenience, not the production onboarding path.)
//
// Opt-in only, so `make check` / CI (no Stripe key, no network) skip it. Run with:
//
//	STRIPE_CONNECT_POC=1 \
//	STRIPE_SECRET_KEY=rk_test_…            # or sk_test_… (needs Connect+PaymentIntents+Transfers write) \
//	[STRIPE_CONNECT_ACCOUNT=acct_…]        # optional; self-provisioned when unset \
//	[STRIPE_CONNECT_POC_PAYOUT=1]          # optional; best-effort payout leg \
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
	require.NotEmpty(t, secretKey, "STRIPE_SECRET_KEY (sandbox sk_test_…/rk_test_…) is required")

	ctx := context.Background()
	backends := &stripe.Backends{
		API: stripe.GetBackendWithConfig(stripe.APIBackend, &stripe.BackendConfig{
			HTTPClient: &http.Client{Timeout: 30 * time.Second},
		}),
	}
	sc := stripe.NewClient(secretKey, stripe.WithBackends(backends))

	// The recipient connected account may be supplied via env (a pre-onboarded
	// sandbox acct_…), or self-provisioned here so the PoC needs only a key.
	connectedAccount := os.Getenv("STRIPE_CONNECT_ACCOUNT")
	if connectedAccount == "" {
		connectedAccount = ensureRecipientAccount(ctx, t, sc)
	}
	require.NotEmpty(t, connectedAccount)

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

// ensureRecipientAccount self-provisions a JP Custom connected account (the
// transfer recipient) filled with Stripe's test-mode magic values, so the PoC
// needs only a secret key (no manual Dashboard onboarding). JP→JP platforms must
// use the default `full` service agreement (the lighter `recipient` agreement is
// rejected for same-country JP accounts), so full individual KYC data is
// supplied. It mirrors the "single Liverty Music-owned interim payee" model:
// one recipient account that all transfers route to until per-Organizer
// onboarding ships. It fills Stripe's test-mode magic values, requests the
// `transfers` capability, then polls until it is `active`.
func ensureRecipientAccount(ctx context.Context, t *testing.T, sc *stripe.Client) string {
	t.Helper()

	now := time.Now().Unix()
	params := &stripe.AccountCreateParams{
		Type:         stripe.String(string(stripe.AccountTypeCustom)),
		Country:      stripe.String("JP"),
		Email:        stripe.String("poc-organizer@pannpers.dev"),
		BusinessType: stripe.String(string(stripe.AccountBusinessTypeIndividual)),
		Capabilities: &stripe.AccountCreateCapabilitiesParams{
			Transfers: &stripe.AccountCreateCapabilitiesTransfersParams{Requested: stripe.Bool(true)},
		},
		BusinessProfile: &stripe.AccountCreateBusinessProfileParams{
			MCC:                stripe.String("7922"), // Theatrical producers / ticket agencies
			ProductDescription: stripe.String("Live concert ticket sales"),
			URL:                stripe.String("https://liverty-music.app"),
		},
		// JP→JP platforms must use the default `full` service agreement — the
		// lighter `recipient` agreement is rejected for same-country JP accounts.
		TOSAcceptance: &stripe.AccountCreateTOSAcceptanceParams{
			Date: stripe.Int64(now),
			IP:   stripe.String("127.0.0.1"),
		},
		Individual: &stripe.PersonParams{
			FirstNameKanji: stripe.String("太郎"),
			LastNameKanji:  stripe.String("山田"),
			FirstNameKana:  stripe.String("ﾀﾛｳ"),
			LastNameKana:   stripe.String("ﾔﾏﾀﾞ"),
			Gender:         stripe.String("male"),
			Email:          stripe.String("poc-organizer@pannpers.dev"),
			Phone:          stripe.String("+815012345678"),
			DOB:            &stripe.PersonDOBParams{Day: stripe.Int64(1), Month: stripe.Int64(1), Year: stripe.Int64(1990)},
			AddressKanji: &stripe.PersonAddressKanjiParams{
				PostalCode: stripe.String("1500001"),
				State:      stripe.String("東京都"),
				City:       stripe.String("渋谷区"),
				Town:       stripe.String("神宮前１丁目"),
				Line1:      stripe.String("１−１"),
			},
			AddressKana: &stripe.PersonAddressKanaParams{
				PostalCode: stripe.String("1500001"),
				State:      stripe.String("ﾄｳｷｮｳﾄ"),
				City:       stripe.String("ｼﾌﾞﾔｸ"),
				Town:       stripe.String("ｼﾞﾝｸﾞｳﾏｴ1ﾁｮｳﾒ"),
				Line1:      stripe.String("1-1"),
			},
		},
		ExternalAccount: &stripe.AccountExternalAccountParams{
			Country:           stripe.String("JP"),
			Currency:          stripe.String(string(stripe.CurrencyJPY)),
			AccountHolderName: stripe.String("Yamada Taro"),
			AccountHolderType: stripe.String("individual"),
			RoutingNumber:     stripe.String("1100000"), // test bank(4)+branch(3)
			AccountNumber:     stripe.String("0001234"), // test account number
		},
		Metadata: map[string]string{"purpose": "ticket-purchase-and-issuance PoC interim payee"},
	}

	acct, err := sc.V1Accounts.Create(ctx, params)
	require.NoError(t, err, "create JP recipient connected account")
	t.Logf("created connected account: %s", acct.ID)

	// Poll until the transfers capability activates (test-mode verification is
	// near-instant, but not synchronous with the create call).
	deadline := time.Now().Add(30 * time.Second)
	for {
		got, err := sc.V1Accounts.GetByID(ctx, acct.ID, &stripe.AccountRetrieveParams{})
		require.NoError(t, err, "retrieve connected account")
		status := ""
		if got.Capabilities != nil {
			status = string(got.Capabilities.Transfers)
		}
		if status == "active" {
			t.Logf("transfers capability active on %s", got.ID)
			return got.ID
		}
		if time.Now().After(deadline) {
			due, _ := json.Marshal(got.Requirements)
			t.Fatalf("transfers capability not active (status=%q) before deadline; requirements=%s", status, string(due))
		}
		time.Sleep(2 * time.Second)
	}
}
