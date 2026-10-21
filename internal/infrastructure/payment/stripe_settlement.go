package payment

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/pannpers/go-logging/logging"
	stripe "github.com/stripe/stripe-go/v87"
)

// Accounts v2 recipient provisioning constants. See StripePayoutAccounts.Create for
// why each value is what it is.
const (
	// recipientDashboardNone gives the recipient no Stripe Dashboard access;
	// the platform manages the account.
	recipientDashboardNone = "none"
	// recipientLocaleJA is the onboarding/communication locale for JP Organizers.
	recipientLocaleJA = "ja-JP"
	// recipientCountryJP is the only Organizer country the MVP settles to.
	recipientCountryJP = "JP"
	// recipientMCCTicketing is "theatrical producers / ticket agencies". A JP
	// recipient's stripe_transfers capability does not activate without an MCC.
	recipientMCCTicketing = "7922"
	// collectorApplication makes the platform responsible for fees and losses.
	collectorApplication = "application"
	// metadataKeyOrganizerID ties the Stripe account back to our Organizer.
	metadataKeyOrganizerID = "organizer_id"
)

// Compile-time interface compliance check.
// stripeConnect is the Stripe client of the money-movement adapters: the
// Order's refund and charge, the Settlement's payouts and the PayoutAccount's
// connected account.
type stripeConnect struct {
	client *stripe.Client
	logger *logging.Logger
}

// newStripeConnect returns a stripeConnect whose API calls are bounded by
// stripeHTTPTimeout. The key is never logged.
func newStripeConnect(secretKey string, logger *logging.Logger) stripeConnect {
	return newStripeConnectAt(secretKey, "", logger)
}

// newStripeConnectAt is newStripeConnect against the API at apiURL (empty for
// Stripe's own).
func newStripeConnectAt(secretKey, apiURL string, logger *logging.Logger) stripeConnect {
	return stripeConnect{client: newStripeClient(secretKey, apiURL), logger: logger}
}

// StripeOrderPayments is the Stripe side of the Order's operations:
// GetProviderChargeID and CreateRefund.
type StripeOrderPayments struct{ stripeConnect }

// NewStripeOrderPayments creates the Stripe side of the Order's operations.
func NewStripeOrderPayments(secretKey string, logger *logging.Logger) *StripeOrderPayments {
	return &StripeOrderPayments{newStripeConnect(secretKey, logger)}
}

// StripeSettlementPayouts is the Stripe side of the Settlement's operations:
// CreatePayout and ReversePayout.
type StripeSettlementPayouts struct{ stripeConnect }

// NewStripeSettlementPayouts creates the Stripe side of the Settlement's
// operations.
func NewStripeSettlementPayouts(secretKey string, logger *logging.Logger) *StripeSettlementPayouts {
	return &StripeSettlementPayouts{newStripeConnect(secretKey, logger)}
}

// StripePayoutAccounts is the Stripe side of the PayoutAccount's operations:
// Create, GetStatus and CreateOnboardingLink on Accounts v2 payout-recipient
// connected accounts.
type StripePayoutAccounts struct{ stripeConnect }

// NewStripePayoutAccounts creates the Stripe side of the PayoutAccount's
// operations.
func NewStripePayoutAccounts(secretKey string, logger *logging.Logger) *StripePayoutAccounts {
	return &StripePayoutAccounts{newStripeConnect(secretKey, logger)}
}

// GetProviderChargeID implements [entity.OrderRepository].
//
// It retrieves the PaymentIntent with latest_charge expanded and returns the
// charge id ("ch_..."). The charge id is used as source_transaction on the
// Transfer so Stripe can tie the transfer to the originating platform charge.
func (p *StripeOrderPayments) GetProviderChargeID(ctx context.Context, providerPaymentID string) (string, error) {
	params := &stripe.PaymentIntentRetrieveParams{}
	params.AddExpand("latest_charge")

	pi, err := p.client.V1PaymentIntents.Retrieve(ctx, providerPaymentID, params)
	if err != nil {
		return "", toPaymentAppErr(ctx, err, "failed to retrieve PaymentIntent for charge resolution", p.logger)
	}

	if pi.Status != stripe.PaymentIntentStatusSucceeded {
		return "", apperr.New(codes.FailedPrecondition,
			"payment intent is not in succeeded state; cannot resolve charge ref")
	}

	if pi.LatestCharge == nil || pi.LatestCharge.ID == "" {
		return "", apperr.New(codes.FailedPrecondition,
			"payment intent has no captured charge; cannot resolve charge ref")
	}

	p.logger.Info(ctx, "resolved charge ref from PaymentIntent",
		slog.String("provider_payment_id", providerPaymentID),
		slog.String("provider_charge_id", pi.LatestCharge.ID),
	)
	return pi.LatestCharge.ID, nil
}

// CreatePayout implements [entity.SettlementRepository].
//
// It creates a Stripe Transfer from the platform balance to the Organizer's
// connected account, with source_transaction set to the captured charge.
//
// IMPORTANT: This Transfer deliberately sets NO on_behalf_of and NO
// statement descriptor. The platform is the Stripe settlement merchant (MVP);
// the statement descriptor is platform account configuration owned by
// cloud-provisioning task 5.1. Setting on_behalf_of on a Transfer for a
// separate-charges flow requires the connected account to hold the
// card_payments capability active before the original charge was created —
// which would gate ticket sale on merchant KYB, contradicting the design
// requirement that onboarding never blocks sale.
//
// The idempotency key is derived from the SettlementID and the split's payee
// so a retried sweep never double-transfers the same split.
func (p *StripeSettlementPayouts) CreatePayout(ctx context.Context, params entity.PayoutParams) (string, error) {
	if params.Amount <= 0 {
		return "", apperr.New(codes.InvalidArgument, "transfer amount must be positive")
	}

	// Idempotency key: settlement-id + payee account ensures one transfer per
	// (settlement, payee) pair even if the sweeper retries after a crash.
	idempotencyKey := "settlement-transfer:" + string(params.SettlementID) + ":" + params.PayeeProviderAccountID

	// Take local copies so we can safely take their addresses.
	amount := params.Amount
	transferParams := &stripe.TransferCreateParams{
		Amount:            &amount,
		Currency:          stripe.String(params.Currency),
		Destination:       stripe.String(params.PayeeProviderAccountID),
		SourceTransaction: stripe.String(params.ProviderChargeID),
		// No on_behalf_of — see function-level comment above.
		// No statement descriptor — platform account config (task 5.1).
	}
	transferParams.SetIdempotencyKey(idempotencyKey)

	tr, err := p.client.V1Transfers.Create(ctx, transferParams)
	if err != nil {
		return "", toPaymentAppErr(ctx, err, "failed to create Transfer", p.logger)
	}

	p.logger.Info(ctx, "Stripe Transfer created",
		slog.String("provider_payout_id", tr.ID),
		slog.String("destination", tr.Destination.ID),
		slog.Int64("amount", tr.Amount),
		slog.String("settlement_id", string(params.SettlementID)),
	)
	return tr.ID, nil
}

// Create implements [entity.PayoutAccountRepository].
//
// It creates an Accounts v2 payout-recipient account (POST /v2/core/accounts)
// for the Organizer. Fresh sandboxes reject Accounts v1, and v2 is the shape
// Stripe recommends for new Connect integrations.
//
// The request carries the account *shape* — country, currency/locale, dashboard
// access, the requested capability, and who collects fees/losses — plus the
// Organizer's business contact email, which Stripe requires whenever a recipient
// configuration is supplied. Beyond that it sends **no personal data**: the
// Organizer supplies their name, date of birth, address and documents directly
// to Stripe through the hosted onboarding link (see CreateOnboardingLink), so no
// identity documents or verification data transit or rest here.
// The account is therefore created unverified and reaches payout eligibility
// only once Stripe reports the transfers capability active.
//
// Shape rationale:
//   - dashboard = "none": a platform-managed recipient with no Stripe Dashboard
//     access. A recipient holding stripe_transfers must declare a dashboard.
//   - responsibilities: the platform (application) collects fees and absorbs
//     losses — required for separate charges & transfers, for transfer
//     reversals, and for the platform's negative-balance responsibility.
//   - stripe_balance.stripe_transfers requested, and card_payments NOT
//     requested: a recipient is not the merchant of record, and asking for
//     card_payments would slow onboarding for no benefit.
//   - configuration.merchant.mcc: a JP recipient's stripe_transfers capability
//     will not activate without an MCC, which Stripe surfaces under the
//     merchant configuration. Declaring the MCC alone does NOT make the account
//     merchant-of-record — no merchant capabilities are requested. This was
//     established empirically by the Connect PoC against a real sandbox.
//
// The idempotency key is derived from the organizer id so a retried
// provisioning call cannot create a second account for the same Organizer.
func (p *StripePayoutAccounts) Create(ctx context.Context, organizerID string, contactEmail string) (string, error) {
	if organizerID == "" {
		return "", apperr.New(codes.InvalidArgument, "organizer id must not be empty")
	}
	if contactEmail == "" {
		return "", apperr.New(codes.InvalidArgument, "contact email must not be empty")
	}

	params := &stripe.V2CoreAccountCreateParams{
		ContactEmail: stripe.String(contactEmail),
		Dashboard:    stripe.String(recipientDashboardNone),
		Defaults: &stripe.V2CoreAccountCreateDefaultsParams{
			Currency: stripe.String(string(stripe.CurrencyJPY)),
			Locales:  []*string{stripe.String(recipientLocaleJA)},
			Responsibilities: &stripe.V2CoreAccountCreateDefaultsResponsibilitiesParams{
				FeesCollector:   stripe.String(collectorApplication),
				LossesCollector: stripe.String(collectorApplication),
			},
		},
		Identity: &stripe.V2CoreAccountCreateIdentityParams{
			Country: stripe.String(recipientCountryJP),
		},
		Configuration: &stripe.V2CoreAccountCreateConfigurationParams{
			Recipient: &stripe.V2CoreAccountCreateConfigurationRecipientParams{
				Capabilities: &stripe.V2CoreAccountCreateConfigurationRecipientCapabilitiesParams{
					StripeBalance: &stripe.V2CoreAccountCreateConfigurationRecipientCapabilitiesStripeBalanceParams{
						StripeTransfers: &stripe.V2CoreAccountCreateConfigurationRecipientCapabilitiesStripeBalanceStripeTransfersParams{
							Requested: new(true),
						},
					},
				},
			},
			Merchant: &stripe.V2CoreAccountCreateConfigurationMerchantParams{
				MCC: stripe.String(recipientMCCTicketing),
			},
		},
		Metadata: map[string]string{metadataKeyOrganizerID: organizerID},
	}
	params.SetIdempotencyKey("connected-account:" + organizerID)

	acct, err := p.client.V2CoreAccounts.Create(ctx, params)
	if err != nil {
		return "", toPaymentAppErr(ctx, err, "failed to create Accounts v2 recipient account", p.logger)
	}

	p.logger.Info(ctx, "provisioned Accounts v2 recipient account",
		slog.String("organizer_id", organizerID),
		slog.String("provider_account_id", acct.ID),
	)

	return acct.ID, nil
}

// GetStatus implements [entity.PayoutAccountRepository].
//
// It retrieves the connected account via the v1 accounts API and maps its
// transfers capability state to the platform's own
// entity.PayoutOnboardingStatus enum without exposing raw Stripe status.
//
// Mapping (v1 transfers capability → platform status):
//   - transfers_enabled == true  → Active   (eligible to receive payouts)
//   - account exists but false   → Pending  (KYC/KYB in progress)
//   - account restricted/frozen  → Restricted
//
// TODO: When Accounts v2 is wired, read
// configuration.recipient.capabilities.stripe_balance.stripe_transfers.status
// ("active" / "pending" / "restricted") directly instead of the deprecated
// transfers_enabled bool.
func (p *StripePayoutAccounts) GetStatus(ctx context.Context, providerAccountID string) (entity.PayoutOnboardingStatus, error) {
	acct, err := p.client.V1Accounts.GetByID(ctx, providerAccountID, nil)
	if err != nil {
		return entity.PayoutOnboardingStatusUnspecified, toPaymentAppErr(ctx, err, "failed to retrieve connected account", p.logger)
	}

	return mapAccountStatus(acct), nil
}

// mapAccountStatus maps a Stripe v1 Account to the platform's own
// PayoutOnboardingStatus. Uses the Capabilities.Transfers field which reflects
// the platform-requested transfers capability state.
//
// Mapping:
//   - Capabilities.Transfers == "active"   → Active   (eligible for payouts)
//   - Capabilities.Transfers == "pending"  → Pending  (KYC/KYB in progress)
//   - Capabilities.Transfers == "inactive" → Restricted (capability disabled)
//   - nil capabilities / empty             → Pending  (not yet requested)
//
// Accounts are now provisioned via Accounts v2 (see CreatePayoutAccount), but
// v2 accounts are still readable through the v1 Accounts endpoint and expose the
// same transfers capability state, so this mapping stays correct. Reading
// configuration.recipient.capabilities.stripe_balance.stripe_transfers.status via
// V2CoreAccounts.Retrieve would be more direct and is the natural follow-up; it
// is deliberately not bundled with the provisioning change.
func mapAccountStatus(acct *stripe.Account) entity.PayoutOnboardingStatus {
	if acct == nil {
		return entity.PayoutOnboardingStatusUnspecified
	}
	if acct.Capabilities == nil {
		return entity.PayoutOnboardingStatusPending
	}
	switch acct.Capabilities.Transfers {
	case stripe.AccountCapabilityStatusActive:
		return entity.PayoutOnboardingStatusActive
	case stripe.AccountCapabilityStatusInactive:
		return entity.PayoutOnboardingStatusRestricted
	default:
		// "pending" or empty — capability requested but not yet active.
		return entity.PayoutOnboardingStatusPending
	}
}

// CreateRefund implements [entity.OrderRepository].
//
// It creates a Stripe Refund of params.Amount against the captured Charge
// (ch_). The idempotency key names the purpose, so all retries for the same
// purpose share it (crash-safe replay) and two purposes of one Order are two
// refunds:
//   - the Order's own refund: "order-refund:<orderID>";
//   - the refund of one resale listing: "resale-listing-refund:<listingID>".
//
// Stripe keeps idempotency keys for 24 hours; the resale refund job retries
// within a minute, well inside that window.
func (p *StripeOrderPayments) CreateRefund(ctx context.Context, params entity.RefundParams) (string, error) {
	if params.Amount <= 0 {
		return "", apperr.New(codes.InvalidArgument, "refund amount must be positive")
	}

	idempotencyKey := "order-refund:" + string(params.OrderID)
	if params.ResaleListingID != "" {
		idempotencyKey = "resale-listing-refund:" + string(params.ResaleListingID)
	}

	amount := params.Amount
	refundParams := &stripe.RefundCreateParams{
		Charge: stripe.String(params.ProviderChargeID),
		Amount: &amount,
		Reason: stripe.String("requested_by_customer"),
	}
	refundParams.SetIdempotencyKey(idempotencyKey)

	refund, err := p.client.V1Refunds.Create(ctx, refundParams)
	if err != nil {
		if stripeErr, ok := errors.AsType[*stripe.Error](err); ok &&
			(stripeErr.Code == stripe.ErrorCodeChargeAlreadyRefunded || stripeErr.Code == stripe.ErrorCodeAmountTooLarge) {
			return "", apperr.Wrap(err, codes.FailedPrecondition, "the charge is already refunded or has less left than the amount",
				slog.String("order_id", string(params.OrderID)), slog.String("resale_listing_id", string(params.ResaleListingID)))
		}
		return "", toPaymentAppErr(ctx, err, "failed to create Refund", p.logger)
	}

	p.logger.Info(ctx, "Stripe Refund created",
		slog.String("provider_refund_id", refund.ID),
		slog.String("provider_charge_id", params.ProviderChargeID),
		slog.Int64("amount", refund.Amount),
		slog.String("order_id", string(params.OrderID)),
		slog.String("resale_listing_id", string(params.ResaleListingID)),
	)
	return refund.ID, nil
}

// ReversePayout implements [entity.SettlementRepository].
//
// It creates a Stripe TransferReversal for the given Transfer. On a refund or
// dispute clawback the Organizer's share is clawed back to the platform balance
// via transfer_reversal. The reversal amount equals the split's original amount
// (full reversal per split).
//
// The idempotency key is derived from SettlementID + ProviderPayoutID so a retried
// clawback never double-reverses a split.
func (p *StripeSettlementPayouts) ReversePayout(ctx context.Context, params entity.ReversePayoutParams) (string, error) {
	if params.Amount <= 0 {
		return "", apperr.New(codes.InvalidArgument, "reversal amount must be positive")
	}

	// Idempotency key: settlement-id + transfer-ref ensures one reversal per
	// (settlement, split) pair even if the sweeper or webhook handler retries.
	idempotencyKey := "settlement-reversal:" + string(params.SettlementID) + ":" + params.ProviderPayoutID

	amount := params.Amount
	reversalParams := &stripe.TransferReversalCreateParams{
		ID:     stripe.String(params.ProviderPayoutID),
		Amount: &amount,
	}
	reversalParams.SetIdempotencyKey(idempotencyKey)

	reversal, err := p.client.V1TransferReversals.Create(ctx, reversalParams)
	if err != nil {
		return "", toPaymentAppErr(ctx, err, "failed to create TransferReversal", p.logger)
	}

	p.logger.Info(ctx, "Stripe TransferReversal created",
		slog.String("provider_payout_reversal_id", reversal.ID),
		slog.String("provider_payout_id", params.ProviderPayoutID),
		slog.Int64("amount", reversal.Amount),
		slog.String("settlement_id", string(params.SettlementID)),
	)
	return reversal.ID, nil
}

// CreateOnboardingLink implements [entity.PayoutAccountRepository].
//
// It creates a Stripe AccountLink for the connected account so the Organizer
// can complete KYC/KYB via the Stripe-hosted onboarding flow. The link is
// single-use and expires after a short period; callers must not cache it.
func (p *StripePayoutAccounts) CreateOnboardingLink(ctx context.Context, providerAccountID string, returnURL string) (string, error) {
	params := &stripe.AccountLinkCreateParams{
		Account:    stripe.String(providerAccountID),
		RefreshURL: stripe.String(returnURL),
		ReturnURL:  stripe.String(returnURL),
		Type:       stripe.String("account_onboarding"),
	}

	link, err := p.client.V1AccountLinks.Create(ctx, params)
	if err != nil {
		return "", toPaymentAppErr(ctx, err, "failed to create account onboarding link", p.logger)
	}

	p.logger.Info(ctx, "Stripe account onboarding link created",
		slog.String("provider_account_id", providerAccountID),
		slog.Time("expires_at", time.Unix(link.ExpiresAt, 0)),
	)
	return link.URL, nil
}
