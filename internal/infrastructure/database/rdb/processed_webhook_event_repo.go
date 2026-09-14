package rdb

import (
	"context"
	"log/slog"

	"github.com/liverty-music/backend/internal/usecase"
)

// ProcessedWebhookEventRepository implements [usecase.ProcessedWebhookEventRepository]
// for PostgreSQL. It records provider event ids that have already been applied
// to prevent double-processing of duplicate Stripe webhook deliveries.
type ProcessedWebhookEventRepository struct {
	db *Database
}

// Compile-time interface compliance check.
var _ usecase.ProcessedWebhookEventRepository = (*ProcessedWebhookEventRepository)(nil)

// NewProcessedWebhookEventRepository creates a new repository instance.
func NewProcessedWebhookEventRepository(db *Database) *ProcessedWebhookEventRepository {
	return &ProcessedWebhookEventRepository{db: db}
}

const (
	isProcessedWebhookEventQuery = `
		SELECT EXISTS(
			SELECT 1 FROM processed_webhook_events
			WHERE provider_event_id = $1
		)
	`

	markProcessedWebhookEventQuery = `
		INSERT INTO processed_webhook_events (provider_event_id)
		VALUES ($1)
		ON CONFLICT (provider_event_id) DO NOTHING
	`
)

// IsProcessed implements [usecase.ProcessedWebhookEventRepository].
func (r *ProcessedWebhookEventRepository) IsProcessed(ctx context.Context, providerEventID string) (bool, error) {
	var exists bool
	if err := r.db.Pool.QueryRow(ctx, isProcessedWebhookEventQuery, providerEventID).Scan(&exists); err != nil {
		return false, toAppErr(err, "failed to check processed webhook event",
			slog.String("provider_event_id", providerEventID))
	}
	return exists, nil
}

// MarkProcessed implements [usecase.ProcessedWebhookEventRepository].
func (r *ProcessedWebhookEventRepository) MarkProcessed(ctx context.Context, providerEventID string) error {
	_, err := r.db.Pool.Exec(ctx, markProcessedWebhookEventQuery, providerEventID)
	if err != nil {
		return toAppErr(err, "failed to mark webhook event as processed",
			slog.String("provider_event_id", providerEventID))
	}
	return nil
}
