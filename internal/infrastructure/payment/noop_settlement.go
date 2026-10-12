package payment

import (
	"context"
	"log/slog"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/pannpers/go-logging/logging"
)

// Compile-time interface compliance check.
// noopConnect is the money-movement adapter of an environment without
// STRIPE_SECRET_KEY: every call fails with Unavailable.
type noopConnect struct {
	logger *logging.Logger
}

// NoopOrderPayments is the Order's no-op provider side.
type NoopOrderPayments struct{ noopConnect }

// NewNoopOrderPayments creates the Order's no-op provider side.
func NewNoopOrderPayments(logger *logging.Logger) *NoopOrderPayments {
	return &NoopOrderPayments{noopConnect{logger: logger}}
}

// NoopSettlementPayouts is the Settlement's no-op provider side.
type NoopSettlementPayouts struct{ noopConnect }

// NewNoopSettlementPayouts creates the Settlement's no-op provider side.
func NewNoopSettlementPayouts(logger *logging.Logger) *NoopSettlementPayouts {
	return &NoopSettlementPayouts{noopConnect{logger: logger}}
}

// NoopPayoutAccounts is the PayoutAccount's no-op provider side.
type NoopPayoutAccounts struct{ noopConnect }

// NewNoopPayoutAccounts creates the PayoutAccount's no-op provider side.
func NewNoopPayoutAccounts(logger *logging.Logger) *NoopPayoutAccounts {
	return &NoopPayoutAccounts{noopConnect{logger: logger}}
}

// GetProviderChargeID returns Unavailable because no Stripe key is configured.
func (p *NoopOrderPayments) GetProviderChargeID(ctx context.Context, providerPaymentID string) (string, error) {
	p.logger.Warn(ctx, "settlement skipped: STRIPE_SECRET_KEY is not configured",
		slog.String("provider_payment_id", providerPaymentID))
	return "", apperr.New(codes.Unavailable, "payment provider is not configured")
}

// CreatePayout returns Unavailable because no Stripe key is configured.
func (p *NoopSettlementPayouts) CreatePayout(ctx context.Context, params entity.PayoutParams) (string, error) {
	p.logger.Warn(ctx, "settlement transfer skipped: STRIPE_SECRET_KEY is not configured",
		slog.String("settlement_id", string(params.SettlementID)))
	return "", apperr.New(codes.Unavailable, "payment provider is not configured")
}

// Create returns Unavailable because no Stripe key is configured.
func (p *NoopPayoutAccounts) Create(ctx context.Context, organizerID string, _ string) (string, error) {
	p.logger.Warn(ctx, "connected account creation skipped: STRIPE_SECRET_KEY is not configured",
		slog.String("organizer_id", organizerID))
	return "", apperr.New(codes.Unavailable, "payment provider is not configured")
}

// GetStatus returns Unavailable because no Stripe key is configured.
func (p *NoopPayoutAccounts) GetStatus(ctx context.Context, providerAccountID string) (entity.PayoutOnboardingStatus, error) {
	p.logger.Warn(ctx, "account status check skipped: STRIPE_SECRET_KEY is not configured",
		slog.String("account_ref", providerAccountID))
	return entity.PayoutOnboardingStatusUnspecified, apperr.New(codes.Unavailable, "payment provider is not configured")
}

// CreateOnboardingLink returns Unavailable because no Stripe key is configured.
func (p *NoopPayoutAccounts) CreateOnboardingLink(ctx context.Context, providerAccountID string, returnURL string) (string, error) {
	p.logger.Warn(ctx, "onboarding link creation skipped: STRIPE_SECRET_KEY is not configured",
		slog.String("account_ref", providerAccountID))
	return "", apperr.New(codes.Unavailable, "payment provider is not configured")
}

// CreateRefund returns Unavailable because no Stripe key is configured.
func (p *NoopOrderPayments) CreateRefund(ctx context.Context, params entity.RefundParams) (string, error) {
	p.logger.Warn(ctx, "refund skipped: STRIPE_SECRET_KEY is not configured",
		slog.String("order_id", string(params.OrderID)),
		slog.String("charge_ref", params.ProviderChargeID))
	return "", apperr.New(codes.Unavailable, "payment provider is not configured")
}

// ReversePayout returns Unavailable because no Stripe key is configured.
func (p *NoopSettlementPayouts) ReversePayout(ctx context.Context, params entity.ReversePayoutParams) (string, error) {
	p.logger.Warn(ctx, "transfer reversal skipped: STRIPE_SECRET_KEY is not configured",
		slog.String("settlement_id", string(params.SettlementID)),
		slog.String("transfer_ref", params.ProviderPayoutID))
	return "", apperr.New(codes.Unavailable, "payment provider is not configured")
}
