package payment

import (
	"context"
	"log/slog"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/pannpers/go-logging/logging"
)

// Compile-time interface compliance checks.
var (
	_ entity.PaymentAuthorizationPort = (*NoopAuthorizationPort)(nil)
	_ entity.PaymentCapturePort       = (*NoopAuthorizationPort)(nil)
)

// NoopAuthorizationPort is used when no Stripe secret key is configured (local
// development). Every method returns Unavailable so callers receive a clear
// signal that payment is disabled, rather than a panic or silent wrong
// behavior.
type NoopAuthorizationPort struct {
	logger *logging.Logger
}

// NewNoopAuthorizationPort creates a NoopAuthorizationPort.
func NewNoopAuthorizationPort(logger *logging.Logger) *NoopAuthorizationPort {
	return &NoopAuthorizationPort{logger: logger}
}

// CreateAuthorization returns Unavailable because no Stripe key is configured.
func (p *NoopAuthorizationPort) CreateAuthorization(ctx context.Context, amountJPY int64) (string, string, error) {
	p.logger.Warn(ctx, "payment skipped: STRIPE_SECRET_KEY is not configured",
		slog.Int64("amount_jpy", amountJPY))
	return "", "", apperr.New(codes.Unavailable, "payment provider is not configured")
}

// VerifyAuthorization returns Unavailable because no Stripe key is configured.
func (p *NoopAuthorizationPort) VerifyAuthorization(ctx context.Context, paymentIntentRef string, expectedAmountJPY int64) error {
	p.logger.Warn(ctx, "payment verification skipped: STRIPE_SECRET_KEY is not configured",
		slog.String("payment_intent_ref", paymentIntentRef))
	return apperr.New(codes.Unavailable, "payment provider is not configured")
}

// CancelAuthorization returns Unavailable because no Stripe key is configured.
func (p *NoopAuthorizationPort) CancelAuthorization(ctx context.Context, paymentIntentRef string) error {
	p.logger.Warn(ctx, "payment cancellation skipped: STRIPE_SECRET_KEY is not configured",
		slog.String("payment_intent_ref", paymentIntentRef))
	return apperr.New(codes.Unavailable, "payment provider is not configured")
}

// CaptureAuthorization returns Unavailable because no Stripe key is configured.
func (p *NoopAuthorizationPort) CaptureAuthorization(ctx context.Context, paymentIntentRef string) error {
	p.logger.Warn(ctx, "payment capture skipped: STRIPE_SECRET_KEY is not configured",
		slog.String("payment_intent_ref", paymentIntentRef))
	return apperr.New(codes.Unavailable, "payment provider is not configured")
}

// GetClientSecret returns Unavailable because no Stripe key is configured.
func (p *NoopAuthorizationPort) GetClientSecret(ctx context.Context, paymentIntentRef string) (string, error) {
	p.logger.Warn(ctx, "client secret read skipped: STRIPE_SECRET_KEY is not configured",
		slog.String("payment_intent_ref", paymentIntentRef))
	return "", apperr.New(codes.Unavailable, "payment provider is not configured")
}

// GetCapturedPayment returns Unavailable because no Stripe key is configured.
func (p *NoopAuthorizationPort) GetCapturedPayment(ctx context.Context, paymentIntentRef string) (*entity.CapturedPayment, error) {
	p.logger.Warn(ctx, "captured-payment read skipped: STRIPE_SECRET_KEY is not configured",
		slog.String("payment_intent_ref", paymentIntentRef))
	return nil, apperr.New(codes.Unavailable, "payment provider is not configured")
}

// Compile-time interface compliance check.
var _ entity.CheckoutAuthorizationPort = (*NoopCheckoutAuthorizationPort)(nil)

// NoopCheckoutAuthorizationPort is the checkout card-hold port used when no
// Stripe secret key is configured (local development). Every method returns
// Unavailable.
type NoopCheckoutAuthorizationPort struct {
	logger *logging.Logger
}

// NewNoopCheckoutAuthorizationPort creates a NoopCheckoutAuthorizationPort.
func NewNoopCheckoutAuthorizationPort(logger *logging.Logger) *NoopCheckoutAuthorizationPort {
	return &NoopCheckoutAuthorizationPort{logger: logger}
}

// CreateAuthorization returns Unavailable because no Stripe key is configured.
func (p *NoopCheckoutAuthorizationPort) CreateAuthorization(ctx context.Context, amountJPY int64, meta entity.AuthorizationMetadata) (string, string, error) {
	p.logger.Warn(ctx, "checkout payment skipped: STRIPE_SECRET_KEY is not configured",
		slog.String("checkout_id", string(meta.CheckoutID)))
	return "", "", apperr.New(codes.Unavailable, "payment provider is not configured")
}

// VerifyAuthorization returns Unavailable because no Stripe key is configured.
func (p *NoopCheckoutAuthorizationPort) VerifyAuthorization(ctx context.Context, authorizationRef string, expectedAmountJPY int64) error {
	return apperr.New(codes.Unavailable, "payment provider is not configured")
}

// CaptureAuthorization returns Unavailable because no Stripe key is configured.
func (p *NoopCheckoutAuthorizationPort) CaptureAuthorization(ctx context.Context, authorizationRef string) (*entity.CapturedPayment, error) {
	return nil, apperr.New(codes.Unavailable, "payment provider is not configured")
}

// CancelAuthorization returns Unavailable because no Stripe key is configured.
func (p *NoopCheckoutAuthorizationPort) CancelAuthorization(ctx context.Context, authorizationRef string) error {
	return apperr.New(codes.Unavailable, "payment provider is not configured")
}
