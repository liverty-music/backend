package payment

import (
	"context"
	"log/slog"

	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/pannpers/go-logging/logging"

	"github.com/liverty-music/backend/internal/entity"
)

// NoopCardAuthorizationProvider is the CardAuthorizationRepository of an
// environment without STRIPE_SECRET_KEY: every call fails with Unavailable,
// so no purchase can proceed without the payment provider.
type NoopCardAuthorizationProvider struct {
	logger *logging.Logger
}

// NewNoopCardAuthorizationProvider creates a NoopCardAuthorizationProvider.
func NewNoopCardAuthorizationProvider(logger *logging.Logger) *NoopCardAuthorizationProvider {
	return &NoopCardAuthorizationProvider{logger: logger}
}

// errPaymentNotConfigured is returned by every no-op call.
func (p *NoopCardAuthorizationProvider) errPaymentNotConfigured(ctx context.Context, operation string, attrs ...slog.Attr) error {
	p.logger.Warn(ctx, "card authorization skipped: STRIPE_SECRET_KEY is not configured",
		append([]slog.Attr{slog.String("operation", operation)}, attrs...)...)
	return apperr.New(codes.Unavailable, "payment provider is not configured")
}

// Create always fails with
// Unavailable.
func (p *NoopCardAuthorizationProvider) Create(ctx context.Context, purchase entity.CardAuthorizationPurchase, amountJPY int64) (*entity.CardAuthorization, error) {
	return nil, p.errPaymentNotConfigured(ctx, "create",
		slog.String("ticket_stock_id", string(purchase.TicketStockID)), slog.Int64("amount_jpy", amountJPY))
}

// Get always fails with
// Unavailable.
func (p *NoopCardAuthorizationProvider) Get(ctx context.Context, id string) (*entity.CardAuthorization, error) {
	return nil, p.errPaymentNotConfigured(ctx, "get", slog.String("card_authorization_id", id))
}

// Capture always fails
// with Unavailable.
func (p *NoopCardAuthorizationProvider) Capture(ctx context.Context, id string) error {
	return p.errPaymentNotConfigured(ctx, "capture", slog.String("card_authorization_id", id))
}

// Release always fails
// with Unavailable.
func (p *NoopCardAuthorizationProvider) Release(ctx context.Context, id string) error {
	return p.errPaymentNotConfigured(ctx, "release", slog.String("card_authorization_id", id))
}
