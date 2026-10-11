// Package payment provides infrastructure adapters for payment processing.
// The Stripe adapter implements the manual-capture authorization-hold model
// used by a lottery sale's entries: Create (hold, at Authorize) → Verify (at
// Confirm) → Capture (on a win) or Cancel (on a loss, a withdrawal or a Draft
// left unconfirmed at the draw).
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
	"strings"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/pkg/api"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/pannpers/go-logging/logging"
	stripe "github.com/stripe/stripe-go/v86"
)

// stripeHTTPTimeout bounds every Stripe API call. The default http.Client has
// no timeout, so a hung connection would block the caller — and, for the draw
// job, stall the entire winner-capture batch — indefinitely. Context deadlines
// are propagated in addition to this ceiling (the per-call ctx argument).
const stripeHTTPTimeout = 30 * time.Second

// Compile-time interface compliance checks.
var (
	_ entity.PaymentAuthorizationPort = (*StripeAuthorizationPort)(nil)
	_ entity.PaymentCapturePort       = (*StripeAuthorizationPort)(nil)
)

// CardHoldPolicy is what differs between the callers of the card-hold port:
// which card brands a hold accepts, and the prefix of every idempotency key it
// sends, so the keys of one caller never collide with another's.
type CardHoldPolicy struct {
	// AcceptAllBrands accepts every card brand, American Express included.
	// When false, only the brands [entity.IsAcceptedCardBrand] accepts pass.
	AcceptAllBrands bool
	// KeyPrefix prefixes every idempotency key, e.g. "lottery-capture:pi_...".
	KeyPrefix string
}

var (
	// LotteryCardHoldPolicy is the policy of a Lottery sale's entries: it
	// rejects American Express, whose hold cannot last the up-to-14-day
	// window, and keys requests "lottery-*". The prefix names the sale's
	// method, so it stays the same for every lottery sale.
	LotteryCardHoldPolicy = CardHoldPolicy{AcceptAllBrands: false, KeyPrefix: "lottery"}
	// TicketSaleCardHoldPolicy accepts every brand, because a checkout's hold
	// lasts seconds, and keys requests "ticket-sale-*".
	TicketSaleCardHoldPolicy = CardHoldPolicy{AcceptAllBrands: true, KeyPrefix: "ticket-sale"}
)

// idempotencyKey returns the policy's key for an operation on ref.
func (c CardHoldPolicy) idempotencyKey(operation, ref string) string {
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

// StripeAuthorizationPort implements [entity.PaymentAuthorizationPort] via the
// Stripe PaymentIntents API using the manual-capture authorization-hold model,
// with the lottery's [CardHoldPolicy].
//
// Capture and cancel outcomes follow the PaymentIntent's status: after an
// error the PaymentIntent is read back and classified, so a repeated capture
// or cancel of the same hold succeeds and a cancel of a captured hold is
// FailedPrecondition.
type StripeAuthorizationPort struct {
	client *stripe.Client
	logger *logging.Logger
	policy CardHoldPolicy
}

// NewStripeAuthorizationPort creates a StripeAuthorizationPort with the given
// Stripe secret key. The key is used for every API call; it is never logged.
//
// Callers should use [NewNoopAuthorizationPort] when the key is empty (local
// development without a Stripe account) so that the binary starts cleanly.
func NewStripeAuthorizationPort(secretKey string, logger *logging.Logger) *StripeAuthorizationPort {
	return newStripeAuthorizationPort(secretKey, "", logger)
}

// newStripeAuthorizationPort creates the port against apiURL; an empty apiURL
// is Stripe's API (tests pass a stub server).
func newStripeAuthorizationPort(secretKey, apiURL string, logger *logging.Logger) *StripeAuthorizationPort {
	return &StripeAuthorizationPort{
		client: newStripeClient(secretKey, apiURL),
		logger: logger,
		policy: LotteryCardHoldPolicy,
	}
}

// CreateAuthorization implements [entity.PaymentAuthorizationPort].
//
// It creates a Stripe PaymentIntent with capture_method=manual and returns the
// intent ID and client secret. The frontend passes the client secret to
// Stripe.js to complete 3DS; the intent ID is recorded on the Draft entry as
// its paymentIntentRef.
func (p *StripeAuthorizationPort) CreateAuthorization(ctx context.Context, amountJPY int64) (providerPaymentID string, clientSecret string, err error) {
	if amountJPY <= 0 {
		return "", "", apperr.New(codes.InvalidArgument, "amountJPY must be positive")
	}

	params := &stripe.PaymentIntentCreateParams{
		Amount:        new(amountJPY),
		Currency:      stripe.String(string(stripe.CurrencyJPY)),
		CaptureMethod: stripe.String(string(stripe.PaymentIntentCaptureMethodManual)),
		AutomaticPaymentMethods: &stripe.PaymentIntentCreateAutomaticPaymentMethodsParams{
			Enabled:        new(true),
			AllowRedirects: stripe.String(string(stripe.PaymentIntentAutomaticPaymentMethodsAllowRedirectsNever)),
		},
	}

	pi, err := p.client.V1PaymentIntents.Create(ctx, params)
	if err != nil {
		return "", "", toPaymentAppErr(ctx, err, "failed to create PaymentIntent", p.logger)
	}

	p.logger.Info(ctx, "Stripe PaymentIntent created",
		slog.String("payment_intent_id", pi.ID),
		slog.Int64("amount_jpy", amountJPY),
	)
	return pi.ID, pi.ClientSecret, nil
}

// VerifyAuthorization implements [entity.PaymentAuthorizationPort].
//
// It retrieves the PaymentIntent (with latest_charge expanded) and asserts:
//   - status == requires_capture (3DS completed, hold placed)
//   - amount == expectedAmountJPY
//   - currency == jpy
//   - card brand != amex (American Express is not accepted)
//
// Returns FailedPrecondition when status != requires_capture; InvalidArgument
// for amount/currency mismatch or unaccepted card brand.
func (p *StripeAuthorizationPort) VerifyAuthorization(ctx context.Context, providerPaymentID string, expectedAmountJPY int64) error {
	params := &stripe.PaymentIntentRetrieveParams{}
	params.AddExpand("latest_charge")

	pi, err := p.client.V1PaymentIntents.Retrieve(ctx, providerPaymentID, params)
	if err != nil {
		return toPaymentAppErr(ctx, err, "failed to retrieve PaymentIntent", p.logger)
	}

	// Status check: the frontend must have confirmed the intent (3DS complete)
	// before Confirm is called; that transitions the intent to requires_capture.
	if pi.Status != stripe.PaymentIntentStatusRequiresCapture {
		return apperr.New(codes.FailedPrecondition,
			"payment intent is not in requires_capture state; frontend must confirm the intent first")
	}

	// Amount check: must exactly match the ticket type price × ticket count.
	if pi.Amount != expectedAmountJPY {
		return apperr.New(codes.InvalidArgument,
			"payment intent amount does not match the expected authorization amount")
	}

	// Currency check: only JPY is accepted for the lottery.
	if pi.Currency != stripe.CurrencyJPY {
		return apperr.New(codes.InvalidArgument,
			"payment intent currency must be jpy")
	}

	// Card brand check via latest_charge.payment_method_details.card.brand.
	// American Express is not accepted; other brands (visa, mastercard, jcb,
	// diners, discover) are allowed.
	if err := p.verifyCardBrand(pi); err != nil {
		return err
	}

	return nil
}

// CancelAuthorization implements [entity.PaymentAuthorizationPort].
//
// It cancels the PaymentIntent, releasing the authorization hold on the fan's
// card. Used when the fan withdraws or the draw determines a loss.
//
// The idempotency key is deterministic in the PaymentIntent ref, so a retried
// cancel (e.g. after a network blip mid-draw) is a safe no-op that returns the
// original result rather than erroring on an already-cancelled intent.
func (p *StripeAuthorizationPort) CancelAuthorization(ctx context.Context, providerPaymentID string) error {
	params := &stripe.PaymentIntentCancelParams{}
	params.SetIdempotencyKey(p.policy.idempotencyKey("cancel", providerPaymentID))

	_, cancelErr := p.client.V1PaymentIntents.Cancel(ctx, providerPaymentID, params)
	if cancelErr == nil {
		p.logger.Info(ctx, "Stripe PaymentIntent cancelled",
			slog.String("payment_intent_id", providerPaymentID),
		)
		return nil
	}
	if isStripeNotFound(cancelErr) {
		return toPaymentAppErr(ctx, cancelErr, "PaymentIntent not found", p.logger)
	}

	// Classify by the PaymentIntent's status, not by the error.
	current, err := p.client.V1PaymentIntents.Retrieve(ctx, providerPaymentID, &stripe.PaymentIntentRetrieveParams{})
	if err != nil {
		return toPaymentAppErr(ctx, cancelErr, "failed to cancel PaymentIntent", p.logger)
	}
	switch current.Status {
	case stripe.PaymentIntentStatusCanceled:
		// Released earlier, or the hold expired: nothing more to do.
		return nil
	case stripe.PaymentIntentStatusSucceeded:
		return apperr.Wrap(cancelErr, codes.FailedPrecondition, "the card hold has already been captured")
	default:
		return toPaymentAppErr(ctx, cancelErr, "failed to cancel PaymentIntent", p.logger)
	}
}

// CaptureAuthorization implements [entity.PaymentAuthorizationPort].
//
// It captures the held authorization, charging the fan's card. Used by the
// draw job when an entry wins.
//
// The idempotency key is deterministic in the PaymentIntent ref. The draw job
// captures winners in a batch; if it retries after a transient failure, a
// re-capture of an already-captured intent returns the original success instead
// of a duplicate-charge attempt or a spurious error.
func (p *StripeAuthorizationPort) CaptureAuthorization(ctx context.Context, providerPaymentID string) error {
	params := &stripe.PaymentIntentCaptureParams{}
	params.SetIdempotencyKey(p.policy.idempotencyKey("capture", providerPaymentID))

	_, captureErr := p.client.V1PaymentIntents.Capture(ctx, providerPaymentID, params)
	if captureErr == nil {
		p.logger.Info(ctx, "Stripe PaymentIntent captured",
			slog.String("payment_intent_id", providerPaymentID),
		)
		return nil
	}
	if isStripeNotFound(captureErr) {
		return toPaymentAppErr(ctx, captureErr, "PaymentIntent not found", p.logger)
	}

	// Classify by the PaymentIntent's status, not by the error.
	current, err := p.client.V1PaymentIntents.Retrieve(ctx, providerPaymentID, &stripe.PaymentIntentRetrieveParams{})
	if err != nil {
		return apperr.Wrap(captureErr, codes.Unavailable, "capture outcome is not known yet")
	}
	switch current.Status {
	case stripe.PaymentIntentStatusSucceeded:
		// Captured earlier: the card is charged once.
		return nil
	case stripe.PaymentIntentStatusCanceled:
		return apperr.Wrap(captureErr, codes.FailedPrecondition, "the card hold was released or expired")
	case stripe.PaymentIntentStatusRequiresCapture:
		if isCardRefusal(captureErr) {
			return apperr.Wrap(captureErr, codes.FailedPrecondition, "the card can no longer be charged")
		}
		return apperr.Wrap(captureErr, codes.Unavailable, "capture outcome is not known yet")
	default:
		// processing, or a capture of the same hold still in flight.
		return apperr.Wrap(captureErr, codes.Unavailable, "capture outcome is not known yet")
	}
}

// GetClientSecret implements [entity.PaymentAuthorizationPort].
//
// It reads the PaymentIntent and returns its client secret, so a fan who
// reloads the entry screen completes 3DS on the hold already opened instead
// of opening a second one.
func (p *StripeAuthorizationPort) GetClientSecret(ctx context.Context, providerPaymentID string) (string, error) {
	pi, err := p.client.V1PaymentIntents.Retrieve(ctx, providerPaymentID, &stripe.PaymentIntentRetrieveParams{})
	if err != nil {
		return "", toPaymentAppErr(ctx, err, "failed to retrieve PaymentIntent", p.logger)
	}
	return pi.ClientSecret, nil
}

// GetCapturedPayment implements [entity.PaymentCapturePort].
//
// It retrieves the PaymentIntent (with latest_charge expanded) and returns the
// authoritative captured amount, currency, and display-only card facets. ⑤ does
// not capture — ④ already captured at the draw — so this only reads. A
// PaymentIntent that has not succeeded (not yet captured) is reported as
// FailedPrecondition.
func (p *StripeAuthorizationPort) GetCapturedPayment(ctx context.Context, providerPaymentID string) (*entity.CapturedPayment, error) {
	params := &stripe.PaymentIntentRetrieveParams{}
	params.AddExpand("latest_charge")

	pi, err := p.client.V1PaymentIntents.Retrieve(ctx, providerPaymentID, params)
	if err != nil {
		return nil, toPaymentAppErr(ctx, err, "failed to retrieve captured PaymentIntent", p.logger)
	}

	// A captured manual-capture PaymentIntent transitions to succeeded. Anything
	// else means ④'s capture has not settled.
	if pi.Status != stripe.PaymentIntentStatusSucceeded {
		return nil, apperr.New(codes.FailedPrecondition,
			"payment intent is not captured (status is not succeeded)")
	}

	brand, last4 := extractCardFacets(pi)
	return &entity.CapturedPayment{
		Provider:  entity.PaymentProviderStripe,
		AmountJPY: pi.AmountReceived,
		Currency:  strings.ToUpper(string(pi.Currency)),
		CardBrand: brand,
		CardLast4: last4,
	}, nil
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

// verifyCardBrand rejects card brands the policy does not accept, delegating
// the accept/reject decision to the domain rule [entity.IsAcceptedCardBrand].
// It extracts the brand from the latest charge; an absent charge or card
// details yields an empty brand, which the domain rule accepts.
func (p *StripeAuthorizationPort) verifyCardBrand(pi *stripe.PaymentIntent) error {
	if p.policy.AcceptAllBrands || entity.IsAcceptedCardBrand(extractCardBrand(pi)) {
		return nil
	}
	return apperr.New(codes.FailedPrecondition,
		"American Express cards are not accepted for lottery entries")
}

// extractCardBrand returns the brand of the PaymentIntent's latest charge, or an
// empty brand when the charge or its card details are not present (which should
// not happen when status=requires_capture, but is guarded defensively).
func extractCardBrand(pi *stripe.PaymentIntent) entity.CardBrand {
	if pi.LatestCharge == nil {
		return ""
	}
	details := pi.LatestCharge.PaymentMethodDetails
	if details == nil || details.Card == nil {
		return ""
	}
	return entity.CardBrand(details.Card.Brand)
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
		// Defensive: a *stripe.Error carrying a 2xx/zero status is nonsensical;
		// classify as Internal rather than returning nil for a real error.
		return apperr.Wrap(err, codes.Internal, msg)
	}

	// Non-Stripe error (network failure, context cancellation, etc.)
	logger.Warn(ctx, msg, slog.Any("error", err))
	return apperr.Wrap(err, codes.Unavailable, msg)
}
