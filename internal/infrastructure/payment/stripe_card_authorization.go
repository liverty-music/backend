// Package payment provides infrastructure adapters for payment processing.
// The Stripe card authorization repository implements the manual-capture
// model shared by a lottery sale's entries and a first-come checkout: a
// CardAuthorization is a Stripe PaymentIntent with capture_method=manual,
// opened at AuthorizeCard, read at Confirm, and captured (a win, a committed
// checkout) or released (a loss, a rejected entry, a lapsed hold). The
// PaymentIntent's metadata carries the purchase it pays for.
//
// Secret provisioning: the Stripe secret key value is sourced from GCP Secret
// Manager via ESO (ExternalSecretOperator) and injected into the pod as an
// environment variable (STRIPE_SECRET_KEY). Stripe KYC and live-mode key
// activation are external prerequisites; see the cloud-provisioning repo for
// the ESO/Secret Manager configuration.
package payment

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/pannpers/go-logging/logging"
	stripe "github.com/stripe/stripe-go/v87"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/pkg/api"
)

// stripeHTTPTimeout bounds every Stripe API call. The default http.Client has
// no timeout, so a hung connection would block the caller — and, for the draw
// job, stall the entire winner-capture batch — indefinitely. Context deadlines
// are propagated in addition to this ceiling (the per-call ctx argument).
const stripeHTTPTimeout = 30 * time.Second

// Compile-time interface compliance check.
var _ entity.CardAuthorizationRepository = (*StripeCardAuthorizationRepository)(nil)

// CardAuthorizationPolicy is what differs between the purchases a card
// authorization pays for: the prefix of every idempotency key it sends, so the
// keys of a lottery entry never collide with a checkout's. Only a Checkout's
// Create sends a key; a lottery entry's Create opens a new card authorization
// each time. The accepted card brands are the entity rule
// [entity.CardAuthorizationPurchase.AcceptsCardBrand].
type CardAuthorizationPolicy struct {
	// KeyPrefix prefixes every idempotency key, e.g. "lottery-capture:pi_...".
	KeyPrefix string
}

var (
	// LotteryCardAuthorizationPolicy is the policy of a Lottery sale's
	// entries.
	LotteryCardAuthorizationPolicy = CardAuthorizationPolicy{KeyPrefix: "lottery"}
	// CheckoutCardAuthorizationPolicy is the policy of a first-come
	// checkout. Its prefix predates the Checkout name and is kept so retried
	// requests keep their keys.
	CheckoutCardAuthorizationPolicy = CardAuthorizationPolicy{KeyPrefix: "ticket-sale"}
)

// idempotencyKey returns the Stripe idempotency key of one operation on ref,
// e.g. "ticket-sale-capture:pi_...".
func (c CardAuthorizationPolicy) idempotencyKey(operation, ref string) string {
	return c.KeyPrefix + "-" + operation + ":" + ref
}

// newStripeClient returns a Stripe client whose API calls are bounded by
// stripeHTTPTimeout. A non-empty apiURL replaces Stripe's API base URL (tests).
func newStripeClient(secretKey, apiURL string) *stripe.Client {
	cfg := &stripe.BackendConfig{HTTPClient: &http.Client{Timeout: stripeHTTPTimeout}}
	if apiURL != "" {
		cfg.URL = stripe.String(apiURL)
	}
	backends := &stripe.Backends{API: stripe.GetBackendWithConfig(stripe.APIBackend, cfg)}
	return stripe.NewClient(secretKey, stripe.WithBackends(backends))
}

// StripeCardAuthorizationRepository implements
// [entity.CardAuthorizationRepository] with Stripe PaymentIntents in
// manual-capture mode, under one [CardAuthorizationPolicy].
type StripeCardAuthorizationRepository struct {
	client *stripe.Client
	logger *logging.Logger
	policy CardAuthorizationPolicy
}

// NewStripeCardAuthorizationRepository creates a Stripe-backed
// CardAuthorizationRepository for the purchases of the policy.
func NewStripeCardAuthorizationRepository(secretKey string, policy CardAuthorizationPolicy, logger *logging.Logger) *StripeCardAuthorizationRepository {
	return newStripeCardAuthorizationRepository(secretKey, "", policy, logger)
}

func newStripeCardAuthorizationRepository(secretKey, apiURL string, policy CardAuthorizationPolicy, logger *logging.Logger) *StripeCardAuthorizationRepository {
	return &StripeCardAuthorizationRepository{
		client: newStripeClient(secretKey, apiURL),
		logger: logger,
		policy: policy,
	}
}

// PaymentIntent metadata keys that carry the purchase a card authorization
// pays for.
const (
	metadataCheckoutID    = "checkout_id"
	metadataUserID        = "user_id"
	metadataTicketSaleID  = "ticket_sale_id"
	metadataTicketStockID = "ticket_stock_id"
	metadataTicketCount   = "ticket_count"
	metadataEventID       = "event_id"
	metadataReferralCode  = "referral_code"
)

// Create implements [entity.CardAuthorizationRepository]. The PaymentIntent
// carries the purchase as metadata. A Checkout's idempotency key is its id, so
// a retried Create returns the same PaymentIntent; a lottery entry's Create
// sends no key and opens a new PaymentIntent each time.
func (p *StripeCardAuthorizationRepository) Create(ctx context.Context, purchase entity.CardAuthorizationPurchase, amountJPY int64) (*entity.CardAuthorization, error) {
	if amountJPY <= 0 {
		return nil, apperr.New(codes.InvalidArgument, "amountJPY must be positive")
	}

	params := &stripe.PaymentIntentCreateParams{
		Amount:        new(amountJPY),
		Currency:      stripe.String(string(stripe.CurrencyJPY)),
		CaptureMethod: stripe.String(string(stripe.PaymentIntentCaptureMethodManual)),
		// Card-only, no redirects: the fan authenticates with 3D Secure in the
		// browser and the frontend confirms the PaymentIntent.
		AutomaticPaymentMethods: &stripe.PaymentIntentCreateAutomaticPaymentMethodsParams{
			Enabled:        new(true),
			AllowRedirects: stripe.String(string(stripe.PaymentIntentAutomaticPaymentMethodsAllowRedirectsNever)),
		},
	}
	addMetadata := func(key, value string) {
		if value != "" {
			params.AddMetadata(key, value)
		}
	}
	if purchase.Purpose == entity.CardAuthorizationPurposeCheckout {
		addMetadata(metadataCheckoutID, string(purchase.CheckoutID))
	}
	addMetadata(metadataUserID, string(purchase.UserID))
	addMetadata(metadataTicketSaleID, string(purchase.TicketSaleID))
	addMetadata(metadataTicketStockID, string(purchase.TicketStockID))
	if purchase.TicketCount > 0 {
		addMetadata(metadataTicketCount, strconv.Itoa(purchase.TicketCount))
	}
	addMetadata(metadataEventID, purchase.EventID)
	if purchase.ReferralCode != nil {
		addMetadata(metadataReferralCode, string(*purchase.ReferralCode))
	}
	if purchase.Purpose == entity.CardAuthorizationPurposeCheckout && purchase.CheckoutID != "" {
		params.SetIdempotencyKey(p.policy.idempotencyKey("authorize", string(purchase.CheckoutID)))
	}

	pi, err := p.client.V1PaymentIntents.Create(ctx, params)
	if err != nil {
		return nil, toPaymentAppErr(ctx, err, "failed to create PaymentIntent", p.logger)
	}

	p.logger.Info(ctx, "Stripe PaymentIntent created",
		slog.String("payment_intent_id", pi.ID),
		slog.String("checkout_id", string(purchase.CheckoutID)),
		slog.String("ticket_stock_id", string(purchase.TicketStockID)),
		slog.Int64("amount_jpy", amountJPY),
	)
	return cardAuthorization(pi), nil
}

// Get implements [entity.CardAuthorizationRepository].
func (p *StripeCardAuthorizationRepository) Get(ctx context.Context, id string) (*entity.CardAuthorization, error) {
	pi, err := p.retrieve(ctx, id)
	if err != nil {
		return nil, err
	}
	if pi.Currency != stripe.CurrencyJPY {
		return nil, apperr.New(codes.InvalidArgument, "the card authorization's currency must be jpy")
	}
	return cardAuthorization(pi), nil
}

// Capture implements [entity.CardAuthorizationRepository]. The outcome
// follows the PaymentIntent's status rather than the capture call's error, so
// a capture repeated days later (past Stripe's 24-hour idempotency-key
// retention) still succeeds without charging twice.
func (p *StripeCardAuthorizationRepository) Capture(ctx context.Context, id string) error {
	params := &stripe.PaymentIntentCaptureParams{}
	params.SetIdempotencyKey(p.policy.idempotencyKey("capture", id))

	_, captureErr := p.client.V1PaymentIntents.Capture(ctx, id, params)
	if captureErr == nil {
		p.logger.Info(ctx, "Stripe PaymentIntent captured", slog.String("payment_intent_id", id))
		return nil
	}
	if isStripeNotFound(captureErr) {
		return toPaymentAppErr(ctx, captureErr, "PaymentIntent not found", p.logger)
	}

	current, err := p.retrieve(ctx, id)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			return err
		}
		return apperr.Wrap(captureErr, codes.Unavailable, "capture outcome is not known yet")
	}
	switch current.Status {
	case stripe.PaymentIntentStatusSucceeded:
		return nil
	case stripe.PaymentIntentStatusCanceled:
		return apperr.Wrap(captureErr, codes.FailedPrecondition, "the card authorization was released or expired")
	case stripe.PaymentIntentStatusRequiresCapture:
		if isCardRefusal(captureErr) {
			return apperr.Wrap(captureErr, codes.FailedPrecondition, "the card can no longer be charged")
		}
		return apperr.Wrap(captureErr, codes.Unavailable, "capture outcome is not known yet")
	default:
		return apperr.Wrap(captureErr, codes.Unavailable, "capture outcome is not known yet")
	}
}

// Release implements [entity.CardAuthorizationRepository]. A PaymentIntent
// that is already cancelled (released, or lapsed on the card) succeeds.
func (p *StripeCardAuthorizationRepository) Release(ctx context.Context, id string) error {
	params := &stripe.PaymentIntentCancelParams{}
	params.SetIdempotencyKey(p.policy.idempotencyKey("cancel", id))

	_, cancelErr := p.client.V1PaymentIntents.Cancel(ctx, id, params)
	if cancelErr == nil {
		p.logger.Info(ctx, "Stripe PaymentIntent cancelled", slog.String("payment_intent_id", id))
		return nil
	}
	if isStripeNotFound(cancelErr) {
		return toPaymentAppErr(ctx, cancelErr, "PaymentIntent not found", p.logger)
	}

	current, err := p.retrieve(ctx, id)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			return err
		}
		return apperr.Wrap(cancelErr, codes.Unavailable, "release outcome is not known yet")
	}
	switch current.Status {
	case stripe.PaymentIntentStatusCanceled:
		return nil
	case stripe.PaymentIntentStatusSucceeded:
		return apperr.Wrap(cancelErr, codes.FailedPrecondition, "the card authorization has already been captured")
	default:
		return toPaymentAppErr(ctx, cancelErr, "failed to cancel PaymentIntent", p.logger)
	}
}

// retrieve reads the PaymentIntent with its latest charge expanded, so the
// card brand and last four digits are present.
func (p *StripeCardAuthorizationRepository) retrieve(ctx context.Context, id string) (*stripe.PaymentIntent, error) {
	params := &stripe.PaymentIntentRetrieveParams{}
	params.AddExpand("latest_charge")
	pi, err := p.client.V1PaymentIntents.Retrieve(ctx, id, params)
	if err != nil {
		return nil, toPaymentAppErr(ctx, err, "failed to retrieve PaymentIntent", p.logger)
	}
	return pi, nil
}

// cardAuthorization maps a PaymentIntent to the CardAuthorization it holds.
func cardAuthorization(pi *stripe.PaymentIntent) *entity.CardAuthorization {
	a := &entity.CardAuthorization{
		ID:        pi.ID,
		Amount:    pi.Amount,
		CardBrand: extractCardBrand(pi),
		Status:    cardAuthorizationStatus(pi.Status),
		Secret:    pi.ClientSecret,
		Purchase:  purchaseFromMetadata(pi.Metadata),
	}
	if a.Status == entity.CardAuthorizationStatusCaptured {
		a.CapturedPayment = capturedPayment(pi)
	}
	return a
}

// purchaseFromMetadata maps a PaymentIntent's metadata back to the purchase it
// pays for: a Checkout when checkout_id is present, a lottery entry when the
// fan and the TicketSale are, and an unspecified purchase otherwise (a
// PaymentIntent the platform did not open for a purchase).
func purchaseFromMetadata(metadata map[string]string) entity.CardAuthorizationPurchase {
	purchase := entity.CardAuthorizationPurchase{
		CheckoutID:    entity.CheckoutID(metadata[metadataCheckoutID]),
		UserID:        entity.UserID(metadata[metadataUserID]),
		TicketSaleID:  entity.TicketSaleID(metadata[metadataTicketSaleID]),
		TicketStockID: entity.TicketStockID(metadata[metadataTicketStockID]),
		EventID:       metadata[metadataEventID],
	}
	// The count is written by Create as 1 to 10; parsing it as 32 bits keeps it
	// in range for the proto's int32.
	if count, err := strconv.ParseInt(metadata[metadataTicketCount], 10, 32); err == nil {
		purchase.TicketCount = int(count)
	}
	if code, ok := metadata[metadataReferralCode]; ok && code != "" {
		c := entity.ReferralCode(code)
		purchase.ReferralCode = &c
	}
	switch {
	case purchase.CheckoutID != "":
		purchase.Purpose = entity.CardAuthorizationPurposeCheckout
	case purchase.UserID != "" && purchase.TicketSaleID != "":
		purchase.Purpose = entity.CardAuthorizationPurposeLotteryEntry
	}
	return purchase
}

// cardAuthorizationStatus maps a PaymentIntent status: requires_capture is
// Authorized, succeeded Captured, canceled Released, and every status before
// card authentication completes Pending.
func cardAuthorizationStatus(s stripe.PaymentIntentStatus) entity.CardAuthorizationStatus {
	switch s {
	case stripe.PaymentIntentStatusRequiresCapture:
		return entity.CardAuthorizationStatusAuthorized
	case stripe.PaymentIntentStatusSucceeded:
		return entity.CardAuthorizationStatusCaptured
	case stripe.PaymentIntentStatusCanceled:
		return entity.CardAuthorizationStatusReleased
	default:
		return entity.CardAuthorizationStatusPending
	}
}

// capturedPayment maps a succeeded PaymentIntent to the charged payment.
func capturedPayment(pi *stripe.PaymentIntent) *entity.CapturedPayment {
	brand, last4 := extractCardFacets(pi)
	return &entity.CapturedPayment{
		Provider:          entity.PaymentProviderStripe,
		ProviderPaymentID: pi.ID,
		AmountJPY:         pi.AmountReceived,
		Currency:          strings.ToUpper(string(pi.Currency)),
		CardBrand:         brand,
		CardLast4:         last4,
	}
}

// extractCardFacets returns the display-only brand and last4 of the
// PaymentIntent's latest charge, or empty strings when unavailable.
func extractCardFacets(pi *stripe.PaymentIntent) (brand string, last4 string) {
	if pi.LatestCharge == nil {
		return "", ""
	}
	details := pi.LatestCharge.PaymentMethodDetails
	if details == nil || details.Card == nil {
		return "", ""
	}
	return string(details.Card.Brand), details.Card.Last4
}

// extractCardBrand returns the brand of the PaymentIntent's latest charge, or
// an empty brand when the charge or its card details are not present.
func extractCardBrand(pi *stripe.PaymentIntent) entity.CardBrand {
	brand, _ := extractCardFacets(pi)
	return entity.CardBrand(brand)
}

// isStripeNotFound reports whether err is a Stripe 404.
func isStripeNotFound(err error) bool {
	stripeErr, ok := errors.AsType[*stripe.Error](err)
	return ok && stripeErr.HTTPStatusCode == http.StatusNotFound
}

// isCardRefusal reports whether err is the card network refusing the charge.
func isCardRefusal(err error) bool {
	stripeErr, ok := errors.AsType[*stripe.Error](err)
	return ok && (stripeErr.Type == stripe.ErrorTypeCard || stripeErr.HTTPStatusCode == http.StatusPaymentRequired)
}

// toPaymentAppErr maps a Stripe API error to an apperr-coded error. It delegates
// the HTTP-status → apperr-code decision to the shared [api.FromStatus] policy so
// Stripe classifies identically to the raw-HTTP clients (via api.FromHTTP) and
// other SDK adapters — in particular 401 → Unauthenticated, 403 → PermissionDenied,
// and 429 → ResourceExhausted, which a bespoke mapping tends to collapse into a
// single InvalidArgument. A non-Stripe error (network failure, context
// cancellation) maps to Unavailable.
func toPaymentAppErr(ctx context.Context, err error, msg string, logger *logging.Logger) error {
	if err == nil {
		return nil
	}

	if stripeErr, ok := errors.AsType[*stripe.Error](err); ok {
		logger.Warn(ctx, msg,
			slog.String("stripe_error_type", string(stripeErr.Type)),
			slog.String("stripe_error_code", string(stripeErr.Code)),
			slog.Int("stripe_http_status", stripeErr.HTTPStatusCode),
			slog.String("stripe_message", stripeErr.Msg),
		)

		if appErr := api.FromStatus(stripeErr.HTTPStatusCode, err, msg); appErr != nil {
			return appErr
		}
		return apperr.Wrap(err, codes.Internal, msg)
	}

	logger.Warn(ctx, msg, slog.Any("error", err))
	return apperr.Wrap(err, codes.Unavailable, msg)
}
