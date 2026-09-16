package payment

import (
	"context"
	"log/slog"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/pannpers/go-logging/logging"
)

// Compile-time interface compliance check.
var _ usecase.PaymentSettlementPort = (*NoopSettlementPort)(nil)

// NoopSettlementPort is used when no Stripe secret key is configured (local
// development). Every method returns Unavailable so callers receive a clear
// signal that settlement is disabled, rather than a panic or silent wrong
// behavior.
type NoopSettlementPort struct {
	logger *logging.Logger
}

// NewNoopSettlementPort creates a NoopSettlementPort.
func NewNoopSettlementPort(logger *logging.Logger) *NoopSettlementPort {
	return &NoopSettlementPort{logger: logger}
}

// ResolveChargeRef returns Unavailable because no Stripe key is configured.
func (p *NoopSettlementPort) ResolveChargeRef(ctx context.Context, paymentIntentRef string) (string, error) {
	p.logger.Warn(ctx, "settlement skipped: STRIPE_SECRET_KEY is not configured",
		slog.String("payment_intent_ref", paymentIntentRef))
	return "", apperr.New(codes.Unavailable, "payment provider is not configured")
}

// CreateTransfer returns Unavailable because no Stripe key is configured.
func (p *NoopSettlementPort) CreateTransfer(ctx context.Context, params usecase.TransferParams) (string, error) {
	p.logger.Warn(ctx, "settlement transfer skipped: STRIPE_SECRET_KEY is not configured",
		slog.String("settlement_id", string(params.SettlementID)))
	return "", apperr.New(codes.Unavailable, "payment provider is not configured")
}

// CreateConnectedAccount returns Unavailable because no Stripe key is configured.
func (p *NoopSettlementPort) CreateConnectedAccount(ctx context.Context, organizerID string, _ string) (string, error) {
	p.logger.Warn(ctx, "connected account creation skipped: STRIPE_SECRET_KEY is not configured",
		slog.String("organizer_id", organizerID))
	return "", apperr.New(codes.Unavailable, "payment provider is not configured")
}

// GetAccountStatus returns Unavailable because no Stripe key is configured.
func (p *NoopSettlementPort) GetAccountStatus(ctx context.Context, accountRef string) (entity.PayoutOnboardingStatus, error) {
	p.logger.Warn(ctx, "account status check skipped: STRIPE_SECRET_KEY is not configured",
		slog.String("account_ref", accountRef))
	return entity.PayoutOnboardingStatusUnspecified, apperr.New(codes.Unavailable, "payment provider is not configured")
}

// CreateOnboardingLink returns Unavailable because no Stripe key is configured.
func (p *NoopSettlementPort) CreateOnboardingLink(ctx context.Context, accountRef string, returnURL string) (string, error) {
	p.logger.Warn(ctx, "onboarding link creation skipped: STRIPE_SECRET_KEY is not configured",
		slog.String("account_ref", accountRef))
	return "", apperr.New(codes.Unavailable, "payment provider is not configured")
}

// CreateRefund returns Unavailable because no Stripe key is configured.
func (p *NoopSettlementPort) CreateRefund(ctx context.Context, params usecase.RefundParams) (string, error) {
	p.logger.Warn(ctx, "refund skipped: STRIPE_SECRET_KEY is not configured",
		slog.String("order_id", string(params.OrderID)),
		slog.String("charge_ref", params.ChargeRef))
	return "", apperr.New(codes.Unavailable, "payment provider is not configured")
}

// ReverseTransfer returns Unavailable because no Stripe key is configured.
func (p *NoopSettlementPort) ReverseTransfer(ctx context.Context, params usecase.ReverseTransferParams) (string, error) {
	p.logger.Warn(ctx, "transfer reversal skipped: STRIPE_SECRET_KEY is not configured",
		slog.String("settlement_id", string(params.SettlementID)),
		slog.String("transfer_ref", params.TransferRef))
	return "", apperr.New(codes.Unavailable, "payment provider is not configured")
}
