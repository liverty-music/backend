package payment

import (
	"context"
	"log/slog"

	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/pannpers/go-logging/logging"

	"github.com/liverty-music/backend/internal/entity"
)

// Compile-time interface compliance check.
var _ entity.CardAuthorizationRepository = (*NoopCardAuthorizationRepository)(nil)

// NoopCardAuthorizationRepository is the CardAuthorizationRepository of an
// environment without STRIPE_SECRET_KEY: every call fails with Unavailable,
// so no purchase can proceed without the payment provider.
type NoopCardAuthorizationRepository struct {
	logger *logging.Logger
}

// NewNoopCardAuthorizationRepository creates a NoopCardAuthorizationRepository.
func NewNoopCardAuthorizationRepository(logger *logging.Logger) *NoopCardAuthorizationRepository {
	return &NoopCardAuthorizationRepository{logger: logger}
}

// errPaymentNotConfigured is returned by every no-op call.
func (p *NoopCardAuthorizationRepository) errPaymentNotConfigured(ctx context.Context, operation string, attrs ...slog.Attr) error {
	p.logger.Warn(ctx, "card authorization skipped: STRIPE_SECRET_KEY is not configured",
		append([]slog.Attr{slog.String("operation", operation)}, attrs...)...)
	return apperr.New(codes.Unavailable, "payment provider is not configured")
}

// Create implements [entity.CardAuthorizationRepository]; it always fails with
// Unavailable.
func (p *NoopCardAuthorizationRepository) Create(ctx context.Context, purchase entity.CardAuthorizationPurchase, amountJPY int64) (*entity.CardAuthorization, error) {
	return nil, p.errPaymentNotConfigured(ctx, "create",
		slog.String("ticket_stock_id", string(purchase.TicketStockID)), slog.Int64("amount_jpy", amountJPY))
}

// Get implements [entity.CardAuthorizationRepository]; it always fails with
// Unavailable.
func (p *NoopCardAuthorizationRepository) Get(ctx context.Context, id string) (*entity.CardAuthorization, error) {
	return nil, p.errPaymentNotConfigured(ctx, "get", slog.String("card_authorization_id", id))
}

// Capture implements [entity.CardAuthorizationRepository]; it always fails
// with Unavailable.
func (p *NoopCardAuthorizationRepository) Capture(ctx context.Context, id string) error {
	return p.errPaymentNotConfigured(ctx, "capture", slog.String("card_authorization_id", id))
}

// Release implements [entity.CardAuthorizationRepository]; it always fails
// with Unavailable.
func (p *NoopCardAuthorizationRepository) Release(ctx context.Context, id string) error {
	return p.errPaymentNotConfigured(ctx, "release", slog.String("card_authorization_id", id))
}
