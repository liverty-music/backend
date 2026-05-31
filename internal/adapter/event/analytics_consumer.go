package event

import (
	"context"
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

// AnalyticsConsumer status labels emitted to
// AnalyticsConsumerMetrics.RecordMessage. Centralised so the metric
// cardinality matches the documented Prometheus contract.
const (
	statusForwarded          = "forwarded"
	statusSkippedNilClient   = "skipped_nil_client"
	statusSkippedEmptyUserID = "skipped_empty_user_id"
	statusSkippedParseError  = "skipped_parse_error"
	statusEnqueueError       = "enqueue_error"
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
//
// metrics is required and MUST NOT be nil; the DI graph constructs a
// no-op-meter implementation when telemetry is disabled.
type AnalyticsConsumer struct {
	client  usecase.AnalyticsClient
	metrics usecase.AnalyticsConsumerMetrics
	logger  *logging.Logger
}

// NewAnalyticsConsumer constructs an AnalyticsConsumer. Passing a nil
// client puts the consumer into log-and-skip mode.
func NewAnalyticsConsumer(
	client usecase.AnalyticsClient,
	metrics usecase.AnalyticsConsumerMetrics,
	logger *logging.Logger,
) *AnalyticsConsumer {
	return &AnalyticsConsumer{client: client, metrics: metrics, logger: logger}
}

// HandleUserCreated forwards the USER.created NATS subject as the
// catalogue event usecase.EventUserCreated. Properties currently set:
// signup_month (current UTC month, YYYY-MM) — locale and home_region
// are absent at user-creation time and added by later catalogue events
// (account.preferred_language.updated et al.).
func (c *AnalyticsConsumer) HandleUserCreated(msg *message.Message) error {
	ctx := msg.Context()
	defer c.recordLag(ctx, msg)

	var data entity.UserCreatedData
	if err := messaging.ParseCloudEventData(msg, &data); err != nil {
		c.logger.Error(ctx, "failed to parse USER.created event", err)
		c.metrics.RecordMessage(ctx, statusSkippedParseError)
		return apperr.Wrap(err, codes.Internal, "parse USER.created event")
	}

	if c.client == nil {
		c.logger.Warn(ctx, "analytics client not configured, skipping forward",
			slog.String("event", string(usecase.EventUserCreated)),
			slog.String("user_id", data.UserID),
		)
		c.metrics.RecordMessage(ctx, statusSkippedNilClient)
		return nil
	}

	if data.UserID == "" {
		c.logger.Warn(ctx, "USER.created event missing user_id, skipping forward",
			slog.String("external_id", data.ExternalID),
		)
		c.metrics.RecordMessage(ctx, statusSkippedEmptyUserID)
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
		c.metrics.RecordMessage(ctx, statusEnqueueError)
		return apperr.Wrap(err, codes.Internal, "enqueue analytics event")
	}

	c.metrics.RecordMessage(ctx, statusForwarded)
	return nil
}

// HandleUserPreferredLanguageUpdated forwards the
// USER.preferred_language_updated NATS subject as the catalogue event
// usecase.EventAccountPreferredLanguageUpdated. Properties: from_locale,
// to_locale (per specification/docs/analytics/event-catalog.md).
func (c *AnalyticsConsumer) HandleUserPreferredLanguageUpdated(msg *message.Message) error {
	ctx := msg.Context()
	defer c.recordLag(ctx, msg)

	var data entity.UserPreferredLanguageUpdatedData
	if err := messaging.ParseCloudEventData(msg, &data); err != nil {
		c.logger.Error(ctx, "failed to parse USER.preferred_language_updated event", err)
		c.metrics.RecordMessage(ctx, statusSkippedParseError)
		return apperr.Wrap(err, codes.Internal, "parse USER.preferred_language_updated event")
	}

	if c.client == nil {
		c.logger.Warn(ctx, "analytics client not configured, skipping forward",
			slog.String("event", string(usecase.EventAccountPreferredLanguageUpdated)),
			slog.String("user_id", data.UserID),
		)
		c.metrics.RecordMessage(ctx, statusSkippedNilClient)
		return nil
	}

	if data.UserID == "" {
		c.logger.Warn(ctx, "USER.preferred_language_updated event missing user_id, skipping forward")
		c.metrics.RecordMessage(ctx, statusSkippedEmptyUserID)
		return nil
	}

	properties := usecase.AnalyticsProperties{
		"from_locale": data.FromLocale,
		"to_locale":   data.ToLocale,
	}

	if err := c.client.Enqueue(ctx, data.UserID, usecase.EventAccountPreferredLanguageUpdated, properties); err != nil {
		c.logger.Error(ctx, "failed to enqueue analytics event", err,
			slog.String("event", string(usecase.EventAccountPreferredLanguageUpdated)),
			slog.String("user_id", data.UserID),
		)
		c.metrics.RecordMessage(ctx, statusEnqueueError)
		return apperr.Wrap(err, codes.Internal, "enqueue analytics event")
	}

	c.metrics.RecordMessage(ctx, statusForwarded)
	return nil
}

// HandleArtistFollowed forwards the ARTIST.followed NATS subject as the
// catalogue event usecase.EventArtistFollowCompleted. Properties:
// artist_id (per specification/docs/analytics/event-catalog.md; the
// optional `source` property is FE-only and is therefore omitted here).
func (c *AnalyticsConsumer) HandleArtistFollowed(msg *message.Message) error {
	ctx := msg.Context()
	defer c.recordLag(ctx, msg)

	var data entity.ArtistFollowedData
	if err := messaging.ParseCloudEventData(msg, &data); err != nil {
		c.logger.Error(ctx, "failed to parse ARTIST.followed event", err)
		c.metrics.RecordMessage(ctx, statusSkippedParseError)
		return apperr.Wrap(err, codes.Internal, "parse ARTIST.followed event")
	}

	if c.client == nil {
		c.logger.Warn(ctx, "analytics client not configured, skipping forward",
			slog.String("event", string(usecase.EventArtistFollowCompleted)),
			slog.String("user_id", data.UserID),
			slog.String("artist_id", data.ArtistID),
		)
		c.metrics.RecordMessage(ctx, statusSkippedNilClient)
		return nil
	}

	if data.UserID == "" {
		c.logger.Warn(ctx, "ARTIST.followed event missing user_id, skipping forward",
			slog.String("artist_id", data.ArtistID),
		)
		c.metrics.RecordMessage(ctx, statusSkippedEmptyUserID)
		return nil
	}

	properties := usecase.AnalyticsProperties{
		"artist_id": data.ArtistID,
	}

	if err := c.client.Enqueue(ctx, data.UserID, usecase.EventArtistFollowCompleted, properties); err != nil {
		c.logger.Error(ctx, "failed to enqueue analytics event", err,
			slog.String("event", string(usecase.EventArtistFollowCompleted)),
			slog.String("user_id", data.UserID),
		)
		c.metrics.RecordMessage(ctx, statusEnqueueError)
		return apperr.Wrap(err, codes.Internal, "enqueue analytics event")
	}

	c.metrics.RecordMessage(ctx, statusForwarded)
	return nil
}

// HandleArtistUnfollowed forwards the ARTIST.unfollowed NATS subject as
// the catalogue event usecase.EventArtistUnfollowCompleted. Properties:
// artist_id.
func (c *AnalyticsConsumer) HandleArtistUnfollowed(msg *message.Message) error {
	ctx := msg.Context()
	defer c.recordLag(ctx, msg)

	var data entity.ArtistUnfollowedData
	if err := messaging.ParseCloudEventData(msg, &data); err != nil {
		c.logger.Error(ctx, "failed to parse ARTIST.unfollowed event", err)
		c.metrics.RecordMessage(ctx, statusSkippedParseError)
		return apperr.Wrap(err, codes.Internal, "parse ARTIST.unfollowed event")
	}

	if c.client == nil {
		c.logger.Warn(ctx, "analytics client not configured, skipping forward",
			slog.String("event", string(usecase.EventArtistUnfollowCompleted)),
			slog.String("user_id", data.UserID),
			slog.String("artist_id", data.ArtistID),
		)
		c.metrics.RecordMessage(ctx, statusSkippedNilClient)
		return nil
	}

	if data.UserID == "" {
		c.logger.Warn(ctx, "ARTIST.unfollowed event missing user_id, skipping forward",
			slog.String("artist_id", data.ArtistID),
		)
		c.metrics.RecordMessage(ctx, statusSkippedEmptyUserID)
		return nil
	}

	properties := usecase.AnalyticsProperties{
		"artist_id": data.ArtistID,
	}

	if err := c.client.Enqueue(ctx, data.UserID, usecase.EventArtistUnfollowCompleted, properties); err != nil {
		c.logger.Error(ctx, "failed to enqueue analytics event", err,
			slog.String("event", string(usecase.EventArtistUnfollowCompleted)),
			slog.String("user_id", data.UserID),
		)
		c.metrics.RecordMessage(ctx, statusEnqueueError)
		return apperr.Wrap(err, codes.Internal, "enqueue analytics event")
	}

	c.metrics.RecordMessage(ctx, statusForwarded)
	return nil
}

// HandleNotificationSubscribed forwards the NOTIFICATION.subscribed
// NATS subject as the catalogue event
// usecase.EventNotificationSubscribed. Properties: device_type
// (classifier output from the endpoint host; the endpoint itself is
// never forwarded).
func (c *AnalyticsConsumer) HandleNotificationSubscribed(msg *message.Message) error {
	ctx := msg.Context()
	defer c.recordLag(ctx, msg)

	var data entity.NotificationSubscribedData
	if err := messaging.ParseCloudEventData(msg, &data); err != nil {
		c.logger.Error(ctx, "failed to parse NOTIFICATION.subscribed event", err)
		c.metrics.RecordMessage(ctx, statusSkippedParseError)
		return apperr.Wrap(err, codes.Internal, "parse NOTIFICATION.subscribed event")
	}

	if c.client == nil {
		c.logger.Warn(ctx, "analytics client not configured, skipping forward",
			slog.String("event", string(usecase.EventNotificationSubscribed)),
			slog.String("user_id", data.UserID),
		)
		c.metrics.RecordMessage(ctx, statusSkippedNilClient)
		return nil
	}

	if data.UserID == "" {
		c.logger.Warn(ctx, "NOTIFICATION.subscribed event missing user_id, skipping forward")
		c.metrics.RecordMessage(ctx, statusSkippedEmptyUserID)
		return nil
	}

	properties := usecase.AnalyticsProperties{
		"device_type": data.DeviceType,
	}

	if err := c.client.Enqueue(ctx, data.UserID, usecase.EventNotificationSubscribed, properties); err != nil {
		c.logger.Error(ctx, "failed to enqueue analytics event", err,
			slog.String("event", string(usecase.EventNotificationSubscribed)),
			slog.String("user_id", data.UserID),
		)
		c.metrics.RecordMessage(ctx, statusEnqueueError)
		return apperr.Wrap(err, codes.Internal, "enqueue analytics event")
	}

	c.metrics.RecordMessage(ctx, statusForwarded)
	return nil
}

// HandleEntryZkProofVerified forwards the ENTRY.zk_proof_verified
// NATS subject as the catalogue event usecase.EventEntryZkProofVerified.
// The distinct_id is the nullifier hash hex — anonymous-by-design per
// ZK guarantee, stable per (ticket, event) pair.
func (c *AnalyticsConsumer) HandleEntryZkProofVerified(msg *message.Message) error {
	ctx := msg.Context()
	defer c.recordLag(ctx, msg)

	var data entity.EntryZkProofVerifiedData
	if err := messaging.ParseCloudEventData(msg, &data); err != nil {
		c.logger.Error(ctx, "failed to parse ENTRY.zk_proof_verified event", err)
		c.metrics.RecordMessage(ctx, statusSkippedParseError)
		return apperr.Wrap(err, codes.Internal, "parse ENTRY.zk_proof_verified event")
	}

	if c.client == nil {
		c.logger.Warn(ctx, "analytics client not configured, skipping forward",
			slog.String("event", string(usecase.EventEntryZkProofVerified)),
			slog.String("event_id", data.EventID),
		)
		c.metrics.RecordMessage(ctx, statusSkippedNilClient)
		return nil
	}

	if data.NullifierHashHex == "" {
		c.logger.Warn(ctx, "ENTRY.zk_proof_verified event missing nullifier_hash_hex, skipping forward",
			slog.String("event_id", data.EventID),
		)
		// Reuse the empty-user_id status label: the metric label set is
		// fixed to keep Prometheus cardinality bounded, and "missing
		// distinct_id" is the semantic match regardless of the field name.
		c.metrics.RecordMessage(ctx, statusSkippedEmptyUserID)
		return nil
	}

	properties := usecase.AnalyticsProperties{
		"event_id": data.EventID,
	}

	if err := c.client.Enqueue(ctx, data.NullifierHashHex, usecase.EventEntryZkProofVerified, properties); err != nil {
		c.logger.Error(ctx, "failed to enqueue analytics event", err,
			slog.String("event", string(usecase.EventEntryZkProofVerified)),
			slog.String("event_id", data.EventID),
		)
		c.metrics.RecordMessage(ctx, statusEnqueueError)
		return apperr.Wrap(err, codes.Internal, "enqueue analytics event")
	}

	c.metrics.RecordMessage(ctx, statusForwarded)
	return nil
}

// HandleEntryZkProofRejected forwards the ENTRY.zk_proof_rejected NATS
// subject as the catalogue event usecase.EventEntryZkProofRejected.
// Properties: event_id, reason (one of the entity.EntryRejection*
// constants).
func (c *AnalyticsConsumer) HandleEntryZkProofRejected(msg *message.Message) error {
	ctx := msg.Context()
	defer c.recordLag(ctx, msg)

	var data entity.EntryZkProofRejectedData
	if err := messaging.ParseCloudEventData(msg, &data); err != nil {
		c.logger.Error(ctx, "failed to parse ENTRY.zk_proof_rejected event", err)
		c.metrics.RecordMessage(ctx, statusSkippedParseError)
		return apperr.Wrap(err, codes.Internal, "parse ENTRY.zk_proof_rejected event")
	}

	if c.client == nil {
		c.logger.Warn(ctx, "analytics client not configured, skipping forward",
			slog.String("event", string(usecase.EventEntryZkProofRejected)),
			slog.String("event_id", data.EventID),
			slog.String("reason", string(data.Reason)),
		)
		c.metrics.RecordMessage(ctx, statusSkippedNilClient)
		return nil
	}

	if data.NullifierHashHex == "" {
		c.logger.Warn(ctx, "ENTRY.zk_proof_rejected event missing nullifier_hash_hex, skipping forward",
			slog.String("event_id", data.EventID),
			slog.String("reason", string(data.Reason)),
		)
		c.metrics.RecordMessage(ctx, statusSkippedEmptyUserID)
		return nil
	}

	properties := usecase.AnalyticsProperties{
		"event_id": data.EventID,
		"reason":   string(data.Reason),
	}

	if err := c.client.Enqueue(ctx, data.NullifierHashHex, usecase.EventEntryZkProofRejected, properties); err != nil {
		c.logger.Error(ctx, "failed to enqueue analytics event", err,
			slog.String("event", string(usecase.EventEntryZkProofRejected)),
			slog.String("event_id", data.EventID),
		)
		c.metrics.RecordMessage(ctx, statusEnqueueError)
		return apperr.Wrap(err, codes.Internal, "enqueue analytics event")
	}

	c.metrics.RecordMessage(ctx, statusForwarded)
	return nil
}

// recordLag emits analytics_consumer_lag_seconds derived from the
// CloudEvent's `ce_time` metadata. Missing or unparseable timestamps
// are silently skipped — the metric is best-effort and downstream
// dashboards should distinguish "no samples" from "high lag" via
// the sample-count rather than the value.
func (c *AnalyticsConsumer) recordLag(ctx context.Context, msg *message.Message) {
	publishedAt, err := time.Parse(time.RFC3339, msg.Metadata.Get("ce_time"))
	if err != nil {
		return
	}
	c.metrics.RecordLag(ctx, time.Since(publishedAt).Seconds())
}
