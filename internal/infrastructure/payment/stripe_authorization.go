// Package payment provides infrastructure adapters for payment processing.
// The Stripe adapter implements the manual-capture authorization-hold model
// used by the lottery: Create (hold) → Verify (post-3DS) → Capture (on win)
// or Cancel (on loss/withdrawal).
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
	"time"

	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/pannpers/go-logging/logging"
	stripe "github.com/stripe/stripe-go/v81"
	"github.com/stripe/stripe-go/v81/paymentintent"
)

// stripeHTTPTimeout bounds every Stripe API call. The default http.Client has
// no timeout, so a hung connection would block the caller — and, for the draw
// job, stall the entire winner-capture batch — indefinitely. Context deadlines
// are propagated in addition to this ceiling (see the per-method params.Context
// assignments).
const stripeHTTPTimeout = 30 * time.Second

// Compile-time interface compliance check.
var _ usecase.PaymentAuthorizationPort = (*StripeAuthorizationPort)(nil)

// StripeAuthorizationPort implements [usecase.PaymentAuthorizationPort] via the
// Stripe PaymentIntents API using the manual-capture authorization-hold model.
type StripeAuthorizationPort struct {
	client paymentintent.Client
	logger *logging.Logger
}

// NewStripeAuthorizationPort creates a StripeAuthorizationPort with the given
// Stripe secret key. The key is used for every API call; it is never logged.
//
// Callers should use [NewNoopAuthorizationPort] when the key is empty (local
// development without a Stripe account) so that the binary starts cleanly.
func NewStripeAuthorizationPort(secretKey string, logger *logging.Logger) *StripeAuthorizationPort {
	backend := stripe.GetBackendWithConfig(stripe.APIBackend, &stripe.BackendConfig{
		HTTPClient: &http.Client{Timeout: stripeHTTPTimeout},
	})
	return &StripeAuthorizationPort{
		client: paymentintent.Client{
			B:   backend,
			Key: secretKey,
		},
		logger: logger,
	}
}

// CreateAuthorization implements [usecase.PaymentAuthorizationPort].
//
// It creates a Stripe PaymentIntent with capture_method=manual and returns the
// intent ID and client secret. The frontend passes the client secret to
// Stripe.js to complete 3DS; the intent ID is passed back to [Apply] as the
// paymentIntentRef.
func (p *StripeAuthorizationPort) CreateAuthorization(ctx context.Context, amountJPY int64) (paymentIntentRef string, clientSecret string, err error) {
	if amountJPY <= 0 {
		return "", "", apperr.New(codes.InvalidArgument, "amountJPY must be positive")
	}

	params := &stripe.PaymentIntentParams{
		Amount:        new(amountJPY),
		Currency:      stripe.String(string(stripe.CurrencyJPY)),
		CaptureMethod: stripe.String(string(stripe.PaymentIntentCaptureMethodManual)),
		AutomaticPaymentMethods: &stripe.PaymentIntentAutomaticPaymentMethodsParams{
			Enabled:        new(true),
			AllowRedirects: stripe.String(string(stripe.PaymentIntentAutomaticPaymentMethodsAllowRedirectsNever)),
		},
	}
	params.Context = ctx

	pi, err := p.client.New(params)
	if err != nil {
		return "", "", toPaymentAppErr(ctx, err, "failed to create PaymentIntent", p.logger)
	}

	p.logger.Info(ctx, "Stripe PaymentIntent created",
		slog.String("payment_intent_id", pi.ID),
		slog.Int64("amount_jpy", amountJPY),
	)
	return pi.ID, pi.ClientSecret, nil
}

// VerifyAuthorization implements [usecase.PaymentAuthorizationPort].
//
// It retrieves the PaymentIntent (with latest_charge expanded) and asserts:
//   - status == requires_capture (3DS completed, hold placed)
//   - amount == expectedAmountJPY
//   - currency == jpy
//   - card brand != amex (American Express is not accepted)
//
// Returns FailedPrecondition when status != requires_capture; InvalidArgument
// for amount/currency mismatch or unaccepted card brand.
func (p *StripeAuthorizationPort) VerifyAuthorization(ctx context.Context, paymentIntentRef string, expectedAmountJPY int64) error {
	params := &stripe.PaymentIntentParams{}
	params.Context = ctx
	params.AddExpand("latest_charge")

	pi, err := p.client.Get(paymentIntentRef, params)
	if err != nil {
		return toPaymentAppErr(ctx, err, "failed to retrieve PaymentIntent", p.logger)
	}

	// Status check: the frontend must have confirmed the intent (3DS complete)
	// before Apply is called; that transitions the intent to requires_capture.
	if pi.Status != stripe.PaymentIntentStatusRequiresCapture {
		return apperr.New(codes.FailedPrecondition,
			"payment intent is not in requires_capture state; frontend must confirm the intent first")
	}

	// Amount check: must exactly match the phase price × ticket count.
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
	if err := verifyCardBrand(pi); err != nil {
		return err
	}

	return nil
}

// CancelAuthorization implements [usecase.PaymentAuthorizationPort].
//
// It cancels the PaymentIntent, releasing the authorization hold on the fan's
// card. Used when the fan withdraws or the draw determines a loss.
//
// The idempotency key is deterministic in the PaymentIntent ref, so a retried
// cancel (e.g. after a network blip mid-draw) is a safe no-op that returns the
// original result rather than erroring on an already-cancelled intent.
func (p *StripeAuthorizationPort) CancelAuthorization(ctx context.Context, paymentIntentRef string) error {
	params := &stripe.PaymentIntentCancelParams{}
	params.Context = ctx
	params.SetIdempotencyKey("lottery-cancel:" + paymentIntentRef)

	_, err := p.client.Cancel(paymentIntentRef, params)
	if err != nil {
		return toPaymentAppErr(ctx, err, "failed to cancel PaymentIntent", p.logger)
	}

	p.logger.Info(ctx, "Stripe PaymentIntent cancelled",
		slog.String("payment_intent_id", paymentIntentRef),
	)
	return nil
}

// CaptureAuthorization implements [usecase.PaymentAuthorizationPort].
//
// It captures the held authorization, charging the fan's card. Used by the
// draw job when an application wins.
//
// The idempotency key is deterministic in the PaymentIntent ref. The draw job
// captures winners in a batch; if it retries after a transient failure, a
// re-capture of an already-captured intent returns the original success instead
// of a duplicate-charge attempt or a spurious error.
func (p *StripeAuthorizationPort) CaptureAuthorization(ctx context.Context, paymentIntentRef string) error {
	params := &stripe.PaymentIntentCaptureParams{}
	params.Context = ctx
	params.SetIdempotencyKey("lottery-capture:" + paymentIntentRef)

	_, err := p.client.Capture(paymentIntentRef, params)
	if err != nil {
		return toPaymentAppErr(ctx, err, "failed to capture PaymentIntent", p.logger)
	}

	p.logger.Info(ctx, "Stripe PaymentIntent captured",
		slog.String("payment_intent_id", paymentIntentRef),
	)
	return nil
}

// verifyCardBrand inspects the latest charge on the PaymentIntent and rejects
// American Express cards. Returns FailedPrecondition when the brand is amex;
// returns nil for all other brands (including unknown, which Stripe uses for
// some card networks).
func verifyCardBrand(pi *stripe.PaymentIntent) error {
	if pi.LatestCharge == nil {
		// No charge yet — this should not happen when status=requires_capture,
		// but guard defensively.
		return nil
	}
	details := pi.LatestCharge.PaymentMethodDetails
	if details == nil || details.Card == nil {
		return nil
	}
	if details.Card.Brand == stripe.PaymentMethodCardBrandAmex {
		return apperr.New(codes.FailedPrecondition,
			"American Express cards are not accepted for lottery applications")
	}
	return nil
}

// toPaymentAppErr maps a Stripe API error to an apperr-coded error.
// HTTP 404 → NotFound; 5xx or network failures → Unavailable;
// 400-family (invalid request) → InvalidArgument; everything else → Internal.
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

		switch {
		case stripeErr.HTTPStatusCode == 404:
			return apperr.Wrap(err, codes.NotFound, msg)
		case stripeErr.HTTPStatusCode >= 500:
			return apperr.Wrap(err, codes.Unavailable, msg)
		case stripeErr.HTTPStatusCode >= 400:
			return apperr.Wrap(err, codes.InvalidArgument, msg)
		default:
			return apperr.Wrap(err, codes.Internal, msg)
		}
	}

	// Non-Stripe error (network failure, context cancellation, etc.)
	logger.Warn(ctx, msg, slog.Any("error", err))
	return apperr.Wrap(err, codes.Unavailable, msg)
}
