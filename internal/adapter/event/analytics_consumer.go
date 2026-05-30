package event

import (
	"log/slog"
	"time"

	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/pannpers/go-logging/logging"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/messaging"
	"github.com/liverty-music/backend/internal/usecase"
)

// AnalyticsConsumer forwards backend-published domain events to the
// product-analytics destination via usecase.AnalyticsClient.
//
// Each NATS subject the consumer subscribes to has a dedicated Handle*
// method that decodes the CloudEvent payload, derives the catalogue
// AnalyticsEventName, sanitises properties per the PII policy
// documented in specification/docs/analytics/event-catalog.md, and
// hands the event to AnalyticsClient.Enqueue. The underlying client
// is non-blocking and absorbs transient PostHog failures internally.
//
// If client is nil — the local-development convention used when the
// PostHog project API key is not configured — every Handle method
// logs a warning and acknowledges without forwarding. This matches
// the optional-dependency pattern UserConsumer uses for the email
// verifier.
type AnalyticsConsumer struct {
	client usecase.AnalyticsClient
	logger *logging.Logger
}

// NewAnalyticsConsumer constructs an AnalyticsConsumer. Passing a nil
// client puts the consumer into log-and-skip mode.
func NewAnalyticsConsumer(client usecase.AnalyticsClient, logger *logging.Logger) *AnalyticsConsumer {
	return &AnalyticsConsumer{client: client, logger: logger}
}

// HandleUserCreated forwards the USER.created NATS subject as the
// catalogue event usecase.EventUserCreated. Properties currently set:
// signup_month (current UTC month, YYYY-MM) — locale and home_region
// are absent at user-creation time and added by later catalogue events
// (account.preferred_language.updated et al.).
func (c *AnalyticsConsumer) HandleUserCreated(msg *message.Message) error {
	ctx := msg.Context()

	var data entity.UserCreatedData
	if err := messaging.ParseCloudEventData(msg, &data); err != nil {
		c.logger.Error(ctx, "failed to parse USER.created event", err)
		return apperr.Wrap(err, codes.Internal, "parse USER.created event")
	}

	if c.client == nil {
		c.logger.Warn(ctx, "analytics client not configured, skipping forward",
			slog.String("event", string(usecase.EventUserCreated)),
			slog.String("user_id", data.UserID),
		)
		return nil
	}

	if data.UserID == "" {
		c.logger.Warn(ctx, "USER.created event missing user_id, skipping forward",
			slog.String("external_id", data.ExternalID),
		)
		return nil
	}

	properties := usecase.AnalyticsProperties{
		"signup_month": time.Now().UTC().Format("2006-01"),
	}

	if err := c.client.Enqueue(ctx, data.UserID, usecase.EventUserCreated, properties); err != nil {
		c.logger.Error(ctx, "failed to enqueue analytics event", err,
			slog.String("event", string(usecase.EventUserCreated)),
			slog.String("user_id", data.UserID),
		)
		return apperr.Wrap(err, codes.Internal, "enqueue analytics event")
	}

	return nil
}
