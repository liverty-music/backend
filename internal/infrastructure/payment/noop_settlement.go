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
var _ entity.SettlementPayoutRepository = (*NoopSettlementPayoutRepository)(nil)

// NoopSettlementPayoutRepository is used when no Stripe secret key is configured (local
// development). Every method returns Unavailable so callers receive a clear
// signal that settlement is disabled, rather than a panic or silent wrong
// behavior.
type NoopSettlementPayoutRepository struct {
	logger *logging.Logger
}

// NewNoopSettlementPayoutRepository creates a NoopSettlementPort.
func NewNoopSettlementPayoutRepository(logger *logging.Logger) *NoopSettlementPayoutRepository {
	return &NoopSettlementPayoutRepository{logger: logger}
}

// GetProviderChargeID returns Unavailable because no Stripe key is configured.
func (p *NoopSettlementPayoutRepository) GetProviderChargeID(ctx context.Context, providerPaymentID string) (string, error) {
	p.logger.Warn(ctx, "settlement skipped: STRIPE_SECRET_KEY is not configured",
		slog.String("provider_payment_id", providerPaymentID))
	return "", apperr.New(codes.Unavailable, "payment provider is not configured")
}

// CreatePayout returns Unavailable because no Stripe key is configured.
func (p *NoopSettlementPayoutRepository) CreatePayout(ctx context.Context, params entity.TransferParams) (string, error) {
	p.logger.Warn(ctx, "settlement transfer skipped: STRIPE_SECRET_KEY is not configured",
		slog.String("settlement_id", string(params.SettlementID)))
	return "", apperr.New(codes.Unavailable, "payment provider is not configured")
}

// CreatePayoutAccount returns Unavailable because no Stripe key is configured.
func (p *NoopSettlementPayoutRepository) CreatePayoutAccount(ctx context.Context, organizerID string, _ string) (string, error) {
	p.logger.Warn(ctx, "connected account creation skipped: STRIPE_SECRET_KEY is not configured",
		slog.String("organizer_id", organizerID))
	return "", apperr.New(codes.Unavailable, "payment provider is not configured")
}

// GetPayoutAccountStatus returns Unavailable because no Stripe key is configured.
func (p *NoopSettlementPayoutRepository) GetPayoutAccountStatus(ctx context.Context, providerAccountID string) (entity.PayoutOnboardingStatus, error) {
	p.logger.Warn(ctx, "account status check skipped: STRIPE_SECRET_KEY is not configured",
		slog.String("account_ref", providerAccountID))
	return entity.PayoutOnboardingStatusUnspecified, apperr.New(codes.Unavailable, "payment provider is not configured")
}

// CreateOnboardingLink returns Unavailable because no Stripe key is configured.
func (p *NoopSettlementPayoutRepository) CreateOnboardingLink(ctx context.Context, providerAccountID string, returnURL string) (string, error) {
	p.logger.Warn(ctx, "onboarding link creation skipped: STRIPE_SECRET_KEY is not configured",
		slog.String("account_ref", providerAccountID))
	return "", apperr.New(codes.Unavailable, "payment provider is not configured")
}

// CreateRefund returns Unavailable because no Stripe key is configured.
func (p *NoopSettlementPayoutRepository) CreateRefund(ctx context.Context, params entity.RefundParams) (string, error) {
	p.logger.Warn(ctx, "refund skipped: STRIPE_SECRET_KEY is not configured",
		slog.String("order_id", string(params.OrderID)),
		slog.String("charge_ref", params.ProviderChargeID))
	return "", apperr.New(codes.Unavailable, "payment provider is not configured")
}

// ReversePayout returns Unavailable because no Stripe key is configured.
func (p *NoopSettlementPayoutRepository) ReversePayout(ctx context.Context, params entity.ReverseTransferParams) (string, error) {
	p.logger.Warn(ctx, "transfer reversal skipped: STRIPE_SECRET_KEY is not configured",
		slog.String("settlement_id", string(params.SettlementID)),
		slog.String("transfer_ref", params.ProviderPayoutID))
	return "", apperr.New(codes.Unavailable, "payment provider is not configured")
}
