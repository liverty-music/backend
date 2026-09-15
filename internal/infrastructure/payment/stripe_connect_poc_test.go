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
// pre-onboarded sandbox acct_…) or self-provisioned by ensureRecipientAccount via
// the Accounts v2 API so the PoC needs only a secret key. (The self-provisioning
// here fills test-mode values inline; real Organizer onboarding — the hosted
// AccountLinks/KYC flow — remains a separate concern, but it targets the same
// Accounts v2 (`/v2/core/accounts`) surface exercised here.)
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

// ensureRecipientAccount self-provisions a JP recipient connected account via the
// current Accounts v2 API (POST /v2/core/accounts) — the shape Stripe recommends
// for new Connect integrations, and the only one fresh sandboxes accept (they
// reject Accounts v1 by default). It requests the recipient stripe_transfers
// capability, fills Stripe's test-mode identity values (JP requires kanji + kana
// script names/addresses), attests ToS, then polls until stripe_transfers is
// `active`. So the PoC needs only a secret key — no manual Dashboard onboarding.
// It mirrors the "single Liverty Music-owned interim payee" model: one recipient
// all transfers route to until per-Organizer onboarding ships.
func ensureRecipientAccount(ctx context.Context, t *testing.T, sc *stripe.Client) string {
	t.Helper()

	now := time.Now()
	include := []*string{stripe.String("configuration.recipient"), stripe.String("requirements")}
	params := &stripe.V2CoreAccountCreateParams{
		DisplayName:  stripe.String("Liverty Music PoC Organizer"),
		ContactEmail: stripe.String("poc-organizer@pannpers.dev"),
		// A recipient with the stripe_transfers capability must declare a dashboard;
		// `none` = platform-managed recipient with no Stripe Dashboard access.
		Dashboard: stripe.String("none"),
		Defaults: &stripe.V2CoreAccountCreateDefaultsParams{
			Currency: stripe.String("jpy"),
			Locales:  []*string{stripe.String("ja-JP")},
			Profile: &stripe.V2CoreAccountCreateDefaultsProfileParams{
				BusinessURL:        stripe.String("https://liverty-music.app"),
				ProductDescription: stripe.String("Live concert ticket sales"),
			},
			// Separate charges & transfers: the platform (application) collects fees
			// and absorbs losses — required for a recipient-only account (the
			// recipient is not the merchant of record).
			Responsibilities: &stripe.V2CoreAccountCreateDefaultsResponsibilitiesParams{
				FeesCollector:   stripe.String("application"),
				LossesCollector: stripe.String("application"),
			},
		},
		Identity: &stripe.V2CoreAccountCreateIdentityParams{
			Country:    stripe.String("JP"),
			EntityType: stripe.String("individual"),
			Individual: &stripe.V2CoreAccountCreateIdentityIndividualParams{
				GivenName: stripe.String("太郎"),
				Surname:   stripe.String("山田"),
				Email:     stripe.String("poc-organizer@pannpers.dev"),
				Phone:     stripe.String("+815012345678"),
				DateOfBirth: &stripe.V2CoreAccountCreateIdentityIndividualDateOfBirthParams{
					Day: stripe.Int64(1), Month: stripe.Int64(1), Year: stripe.Int64(1990),
				},
				Address: &stripe.V2CoreAccountCreateIdentityIndividualAddressParams{
					Country:    stripe.String("JP"),
					PostalCode: stripe.String("1500001"),
					State:      stripe.String("東京都"),
					City:       stripe.String("渋谷区"),
					Line1:      stripe.String("神宮前1-1-1"),
				},
				ScriptNames: &stripe.V2CoreAccountCreateIdentityIndividualScriptNamesParams{
					Kanji: &stripe.V2CoreAccountCreateIdentityIndividualScriptNamesKanjiParams{
						GivenName: stripe.String("太郎"), Surname: stripe.String("山田"),
					},
					Kana: &stripe.V2CoreAccountCreateIdentityIndividualScriptNamesKanaParams{
						GivenName: stripe.String("ﾀﾛｳ"), Surname: stripe.String("ﾔﾏﾀﾞ"),
					},
				},
				ScriptAddresses: &stripe.V2CoreAccountCreateIdentityIndividualScriptAddressesParams{
					Kanji: &stripe.V2CoreAccountCreateIdentityIndividualScriptAddressesKanjiParams{
						Country: stripe.String("JP"), PostalCode: stripe.String("1500001"),
						State: stripe.String("東京都"), City: stripe.String("渋谷区"),
						Town: stripe.String("神宮前"), Line1: stripe.String("１−１"),
					},
					Kana: &stripe.V2CoreAccountCreateIdentityIndividualScriptAddressesKanaParams{
						Country: stripe.String("JP"), PostalCode: stripe.String("1500001"),
						State: stripe.String("ﾄｳｷｮｳﾄ"), City: stripe.String("ｼﾌﾞﾔｸ"),
						Town: stripe.String("ｼﾞﾝｸﾞｳﾏｴ"), Line1: stripe.String("1-1"),
					},
				},
			},
			Attestations: &stripe.V2CoreAccountCreateIdentityAttestationsParams{
				TermsOfService: &stripe.V2CoreAccountCreateIdentityAttestationsTermsOfServiceParams{
					Account: &stripe.V2CoreAccountCreateIdentityAttestationsTermsOfServiceAccountParams{
						Date: &now, IP: stripe.String("127.0.0.1"),
					},
				},
			},
		},
		Configuration: &stripe.V2CoreAccountCreateConfigurationParams{
			Recipient: &stripe.V2CoreAccountCreateConfigurationRecipientParams{
				Capabilities: &stripe.V2CoreAccountCreateConfigurationRecipientCapabilitiesParams{
					StripeBalance: &stripe.V2CoreAccountCreateConfigurationRecipientCapabilitiesStripeBalanceParams{
						StripeTransfers: &stripe.V2CoreAccountCreateConfigurationRecipientCapabilitiesStripeBalanceStripeTransfersParams{
							Requested: stripe.Bool(true),
						},
					},
				},
			},
			// A JP recipient's stripe_transfers capability requires an MCC, which
			// Stripe surfaces under `configuration.merchant.mcc`. Declaring the MCC
			// here (no merchant capabilities requested → not merchant-of-record)
			// satisfies that onboarding requirement. This is a real finding for the
			// future ticket-settlement-and-payout onboarding: JP recipients need an MCC.
			Merchant: &stripe.V2CoreAccountCreateConfigurationMerchantParams{
				MCC: stripe.String("7922"), // Theatrical producers / ticket agencies
			},
		},
		Include:  include,
		Metadata: map[string]string{"purpose": "ticket-purchase-and-issuance PoC interim payee"},
	}

	acct, err := sc.V2CoreAccounts.Create(ctx, params)
	require.NoError(t, err, "create JP recipient account (Accounts v2)")
	t.Logf("created recipient account (v2): %s", acct.ID)

	// Poll until the recipient stripe_transfers capability activates (test-mode
	// verification is near-instant, but not synchronous with the create call).
	deadline := time.Now().Add(30 * time.Second)
	for {
		got, err := sc.V2CoreAccounts.Retrieve(ctx, acct.ID, &stripe.V2CoreAccountRetrieveParams{Include: include})
		require.NoError(t, err, "retrieve recipient account")
		status := recipientTransfersStatus(got)
		if status == string(stripe.V2CoreAccountConfigurationRecipientCapabilitiesStripeBalanceStripeTransfersStatusActive) {
			t.Logf("recipient stripe_transfers active on %s", got.ID)
			return got.ID
		}
		if time.Now().After(deadline) {
			due, _ := json.Marshal(got.Requirements)
			t.Fatalf("stripe_transfers not active (status=%q) before deadline; requirements=%s", status, string(due))
		}
		time.Sleep(2 * time.Second)
	}
}

// recipientTransfersStatus safely reads the nested recipient stripe_transfers
// capability status from a v2 account, returning "" when any hop is absent.
func recipientTransfersStatus(a *stripe.V2CoreAccount) string {
	if a.Configuration == nil || a.Configuration.Recipient == nil ||
		a.Configuration.Recipient.Capabilities == nil ||
		a.Configuration.Recipient.Capabilities.StripeBalance == nil ||
		a.Configuration.Recipient.Capabilities.StripeBalance.StripeTransfers == nil {
		return ""
	}
	return string(a.Configuration.Recipient.Capabilities.StripeBalance.StripeTransfers.Status)
}
