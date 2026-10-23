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
			slog.String("provider_user_id", data.ProviderUserID),
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

// HandleUserLoggedIn forwards the USER.logged_in NATS subject as the
// catalogue event usecase.EventAccountSignin. The subject is published once
// per user-initiated sign-in (Zitadel session.user.checked), never on a token
// refresh, so this event is a returning/active-user signal. No properties
// are attached: the distinct_id (platform UserID) is the whole payload, and
// no PII (email, sub) is ever forwarded.
func (c *AnalyticsConsumer) HandleUserLoggedIn(msg *message.Message) error {
	ctx := msg.Context()
	defer c.recordLag(ctx, msg)

	var data entity.UserLoggedInData
	if err := messaging.ParseCloudEventData(msg, &data); err != nil {
		c.logger.Error(ctx, "failed to parse USER.logged_in event", err)
		c.metrics.RecordMessage(ctx, statusSkippedParseError)
		return apperr.Wrap(err, codes.Internal, "parse USER.logged_in event")
	}

	if c.client == nil {
		c.logger.Warn(ctx, "analytics client not configured, skipping forward",
			slog.String("event", string(usecase.EventAccountSignin)),
			slog.String("user_id", data.UserID),
		)
		c.metrics.RecordMessage(ctx, statusSkippedNilClient)
		return nil
	}

	if data.UserID == "" {
		c.logger.Warn(ctx, "USER.logged_in event missing user_id, skipping forward")
		c.metrics.RecordMessage(ctx, statusSkippedEmptyUserID)
		return nil
	}

	if err := c.client.Enqueue(ctx, data.UserID, usecase.EventAccountSignin, usecase.AnalyticsProperties{}); err != nil {
		c.logger.Error(ctx, "failed to enqueue analytics event", err,
			slog.String("event", string(usecase.EventAccountSignin)),
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

// HandleNotificationUnsubscribed forwards the NOTIFICATION.unsubscribed
// NATS subject as the catalogue event
// usecase.EventNotificationUnsubscribed. Properties: device_type
// (classifier output from the endpoint host; the endpoint itself is
// never forwarded). Only user-initiated unsubscriptions emit this event;
// the auto-cleanup path on 410 Gone does not.
func (c *AnalyticsConsumer) HandleNotificationUnsubscribed(msg *message.Message) error {
	ctx := msg.Context()
	defer c.recordLag(ctx, msg)

	var data entity.NotificationUnsubscribedData
	if err := messaging.ParseCloudEventData(msg, &data); err != nil {
		c.logger.Error(ctx, "failed to parse NOTIFICATION.unsubscribed event", err)
		c.metrics.RecordMessage(ctx, statusSkippedParseError)
		return apperr.Wrap(err, codes.Internal, "parse NOTIFICATION.unsubscribed event")
	}

	if c.client == nil {
		c.logger.Warn(ctx, "analytics client not configured, skipping forward",
			slog.String("event", string(usecase.EventNotificationUnsubscribed)),
			slog.String("user_id", data.UserID),
		)
		c.metrics.RecordMessage(ctx, statusSkippedNilClient)
		return nil
	}

	if data.UserID == "" {
		c.logger.Warn(ctx, "NOTIFICATION.unsubscribed event missing user_id, skipping forward")
		c.metrics.RecordMessage(ctx, statusSkippedEmptyUserID)
		return nil
	}

	properties := usecase.AnalyticsProperties{
		"device_type": data.DeviceType,
	}

	if err := c.client.Enqueue(ctx, data.UserID, usecase.EventNotificationUnsubscribed, properties); err != nil {
		c.logger.Error(ctx, "failed to enqueue analytics event", err,
			slog.String("event", string(usecase.EventNotificationUnsubscribed)),
			slog.String("user_id", data.UserID),
		)
		c.metrics.RecordMessage(ctx, statusEnqueueError)
		return apperr.Wrap(err, codes.Internal, "enqueue analytics event")
	}

	c.metrics.RecordMessage(ctx, statusForwarded)
	return nil
}

// HandleNotificationDelivered forwards the NOTIFICATION.delivered NATS
// subject as the catalogue event usecase.EventNotificationDelivered.
// Properties: notification_id, type. The distinct_id is the recipient's
// platform UserID. Fired once per notification whose channel send reached
// the delivered state, so push-delivery reach is measurable in PostHog.
func (c *AnalyticsConsumer) HandleNotificationDelivered(msg *message.Message) error {
	ctx := msg.Context()
	defer c.recordLag(ctx, msg)

	var data entity.NotificationDeliveredData
	if err := messaging.ParseCloudEventData(msg, &data); err != nil {
		c.logger.Error(ctx, "failed to parse NOTIFICATION.delivered event", err)
		c.metrics.RecordMessage(ctx, statusSkippedParseError)
		return apperr.Wrap(err, codes.Internal, "parse NOTIFICATION.delivered event")
	}

	if c.client == nil {
		c.logger.Warn(ctx, "analytics client not configured, skipping forward",
			slog.String("event", string(usecase.EventNotificationDelivered)),
			slog.String("user_id", data.UserID),
			slog.String("notification_id", data.NotificationID),
		)
		c.metrics.RecordMessage(ctx, statusSkippedNilClient)
		return nil
	}

	if data.UserID == "" {
		c.logger.Warn(ctx, "NOTIFICATION.delivered event missing user_id, skipping forward",
			slog.String("notification_id", data.NotificationID),
		)
		c.metrics.RecordMessage(ctx, statusSkippedEmptyUserID)
		return nil
	}

	if data.NotificationID == "" {
		c.logger.Warn(ctx, "NOTIFICATION.delivered event missing notification_id, skipping forward",
			slog.String("user_id", data.UserID),
		)
		// Reuse the empty-user_id status label to keep the metric label set
		// (and Prometheus cardinality) fixed: "missing correlation key" is the
		// semantic match regardless of which key is absent.
		c.metrics.RecordMessage(ctx, statusSkippedEmptyUserID)
		return nil
	}

	properties := usecase.AnalyticsProperties{
		"notification_id": data.NotificationID,
		"type":            data.Type,
	}

	if err := c.client.Enqueue(ctx, data.UserID, usecase.EventNotificationDelivered, properties); err != nil {
		c.logger.Error(ctx, "failed to enqueue analytics event", err,
			slog.String("event", string(usecase.EventNotificationDelivered)),
			slog.String("user_id", data.UserID),
			slog.String("notification_id", data.NotificationID),
		)
		c.metrics.RecordMessage(ctx, statusEnqueueError)
		return apperr.Wrap(err, codes.Internal, "enqueue analytics event")
	}

	c.metrics.RecordMessage(ctx, statusForwarded)
	return nil
}

// HandleTicketJourneyStatusChanged forwards the TICKET_JOURNEY.status_changed
// NATS subject as the catalogue event
// usecase.EventTicketJourneyStatusChanged. Properties: event_id,
// from_status, to_status. from_status is "UNSPECIFIED" when no prior
// journey existed (first-time SetStatus call for the (user, event) pair).
func (c *AnalyticsConsumer) HandleTicketJourneyStatusChanged(msg *message.Message) error {
	ctx := msg.Context()
	defer c.recordLag(ctx, msg)

	var data entity.TicketJourneyStatusChangedData
	if err := messaging.ParseCloudEventData(msg, &data); err != nil {
		c.logger.Error(ctx, "failed to parse TICKET_JOURNEY.status_changed event", err)
		c.metrics.RecordMessage(ctx, statusSkippedParseError)
		return apperr.Wrap(err, codes.Internal, "parse TICKET_JOURNEY.status_changed event")
	}

	if c.client == nil {
		c.logger.Warn(ctx, "analytics client not configured, skipping forward",
			slog.String("event", string(usecase.EventTicketJourneyStatusChanged)),
			slog.String("user_id", data.UserID),
			slog.String("event_id", data.EventID),
		)
		c.metrics.RecordMessage(ctx, statusSkippedNilClient)
		return nil
	}

	if data.UserID == "" {
		c.logger.Warn(ctx, "TICKET_JOURNEY.status_changed event missing user_id, skipping forward",
			slog.String("event_id", data.EventID),
		)
		c.metrics.RecordMessage(ctx, statusSkippedEmptyUserID)
		return nil
	}

	properties := usecase.AnalyticsProperties{
		"event_id":    data.EventID,
		"from_status": data.FromStatus,
		"to_status":   data.ToStatus,
	}

	if err := c.client.Enqueue(ctx, data.UserID, usecase.EventTicketJourneyStatusChanged, properties); err != nil {
		c.logger.Error(ctx, "failed to enqueue analytics event", err,
			slog.String("event", string(usecase.EventTicketJourneyStatusChanged)),
			slog.String("user_id", data.UserID),
			slog.String("event_id", data.EventID),
		)
		c.metrics.RecordMessage(ctx, statusEnqueueError)
		return apperr.Wrap(err, codes.Internal, "enqueue analytics event")
	}

	c.metrics.RecordMessage(ctx, statusForwarded)
	return nil
}

// HandleOrganizerCreated forwards the ORGANIZER.created NATS subject as the
// catalogue event usecase.EventOrganizerCreated. It is an admin-actor / group
// event keyed on organizer_id in PostHog; the organizer_id UUID is used as
// the PostHog distinctID (no fan distinct_id is present for this event).
// Properties: organizer_id.
func (c *AnalyticsConsumer) HandleOrganizerCreated(msg *message.Message) error {
	ctx := msg.Context()
	defer c.recordLag(ctx, msg)

	var data entity.OrganizerCreatedData
	if err := messaging.ParseCloudEventData(msg, &data); err != nil {
		c.logger.Error(ctx, "failed to parse ORGANIZER.created event", err)
		c.metrics.RecordMessage(ctx, statusSkippedParseError)
		return apperr.Wrap(err, codes.Internal, "parse ORGANIZER.created event")
	}

	if c.client == nil {
		c.logger.Warn(ctx, "analytics client not configured, skipping forward",
			slog.String("event", string(usecase.EventOrganizerCreated)),
			slog.String("organizer_id", data.OrganizerID),
		)
		c.metrics.RecordMessage(ctx, statusSkippedNilClient)
		return nil
	}

	if data.OrganizerID == "" {
		c.logger.Warn(ctx, "ORGANIZER.created event missing organizer_id, skipping forward")
		c.metrics.RecordMessage(ctx, statusSkippedEmptyUserID)
		return nil
	}

	properties := usecase.AnalyticsProperties{
		"organizer_id": data.OrganizerID,
	}

	if err := c.client.Enqueue(ctx, data.OrganizerID, usecase.EventOrganizerCreated, properties); err != nil {
		c.logger.Error(ctx, "failed to enqueue analytics event", err,
			slog.String("event", string(usecase.EventOrganizerCreated)),
			slog.String("organizer_id", data.OrganizerID),
		)
		c.metrics.RecordMessage(ctx, statusEnqueueError)
		return apperr.Wrap(err, codes.Internal, "enqueue analytics event")
	}

	c.metrics.RecordMessage(ctx, statusForwarded)
	return nil
}

// HandleOrganizerArtistAssociated forwards the ORGANIZER.artist_associated NATS
// subject as the catalogue event usecase.EventOrganizerArtistAssociated. It is
// an admin-actor / group event keyed on organizer_id in PostHog; the
// organizer_id UUID is used as the PostHog distinctID (no fan distinct_id is
// present for this event). Properties: organizer_id, artist_id.
func (c *AnalyticsConsumer) HandleOrganizerArtistAssociated(msg *message.Message) error {
	ctx := msg.Context()
	defer c.recordLag(ctx, msg)

	var data entity.OrganizerArtistAssociatedData
	if err := messaging.ParseCloudEventData(msg, &data); err != nil {
		c.logger.Error(ctx, "failed to parse ORGANIZER.artist_associated event", err)
		c.metrics.RecordMessage(ctx, statusSkippedParseError)
		return apperr.Wrap(err, codes.Internal, "parse ORGANIZER.artist_associated event")
	}

	if c.client == nil {
		c.logger.Warn(ctx, "analytics client not configured, skipping forward",
			slog.String("event", string(usecase.EventOrganizerArtistAssociated)),
			slog.String("organizer_id", data.OrganizerID),
			slog.String("artist_id", data.ArtistID),
		)
		c.metrics.RecordMessage(ctx, statusSkippedNilClient)
		return nil
	}

	if data.OrganizerID == "" {
		c.logger.Warn(ctx, "ORGANIZER.artist_associated event missing organizer_id, skipping forward",
			slog.String("artist_id", data.ArtistID),
		)
		c.metrics.RecordMessage(ctx, statusSkippedEmptyUserID)
		return nil
	}

	properties := usecase.AnalyticsProperties{
		"organizer_id": data.OrganizerID,
		"artist_id":    data.ArtistID,
	}

	if err := c.client.Enqueue(ctx, data.OrganizerID, usecase.EventOrganizerArtistAssociated, properties); err != nil {
		c.logger.Error(ctx, "failed to enqueue analytics event", err,
			slog.String("event", string(usecase.EventOrganizerArtistAssociated)),
			slog.String("organizer_id", data.OrganizerID),
			slog.String("artist_id", data.ArtistID),
		)
		c.metrics.RecordMessage(ctx, statusEnqueueError)
		return apperr.Wrap(err, codes.Internal, "enqueue analytics event")
	}

	c.metrics.RecordMessage(ctx, statusForwarded)
	return nil
}

// HandleArtistHypeChanged forwards the ARTIST.hype_changed NATS subject as the
// catalogue event usecase.EventArtistHypeChanged. Properties: artist_id,
// from_hype, to_hype.
func (c *AnalyticsConsumer) HandleArtistHypeChanged(msg *message.Message) error {
	var data entity.ArtistHypeChangedData
	return c.forward(msg, entity.SubjectArtistHypeChanged, &data, usecase.EventArtistHypeChanged, func() (string, usecase.AnalyticsProperties) {
		return data.UserID, usecase.AnalyticsProperties{
			"artist_id": data.ArtistID,
			"from_hype": data.FromHype,
			"to_hype":   data.ToHype,
		}
	}, nil)
}

// stableEvent is the PostHog event id and time of an announcement that may be
// handled more than once (see usecase.AnalyticsClient.EnqueueOnce).
type stableEvent struct {
	id string
	at time.Time
}

// HandleOrderPaid forwards the ORDER.paid NATS subject as the catalogue event
// usecase.EventOrderPaid. The Order id is the PostHog event id and the
// Order's pay time its timestamp, both the same on every announcement of the
// Order, so an Order announced or handled more than once is stored once. Properties: order_id, event_id, ticket_count,
// amount, currency, source ("lottery_entry" or "checkout"), and the source's
// id as lottery_entry_id or checkout_id.
func (c *AnalyticsConsumer) HandleOrderPaid(msg *message.Message) error {
	var data entity.OrderPaidData
	return c.forward(msg, entity.SubjectOrderPaid, &data, usecase.EventOrderPaid, func() (string, usecase.AnalyticsProperties) {
		props := usecase.AnalyticsProperties{
			"order_id":     data.OrderID,
			"event_id":     data.EventID,
			"ticket_count": data.TicketCount,
			"amount":       data.Amount,
			"currency":     data.Currency,
		}
		switch {
		case data.LotteryEntryID != "":
			props["source"] = "lottery_entry"
			props["lottery_entry_id"] = data.LotteryEntryID
		case data.CheckoutID != "":
			props["source"] = "checkout"
			props["checkout_id"] = data.CheckoutID
		}
		return data.UserID, props
	}, func() stableEvent { return stableEvent{id: data.OrderID, at: data.PayTime} })
}

// HandleLotteryEntryDrawn forwards the ORDER.lottery_entry_drawn NATS subject
// as the catalogue event usecase.EventTicketLotteryResult, once per entry id
// and draw time. Properties:
// lottery_entry_id, ticket_sale_id, ticket_type_id, result, capture_failed.
func (c *AnalyticsConsumer) HandleLotteryEntryDrawn(msg *message.Message) error {
	var data entity.LotteryEntryDrawnData
	return c.forward(msg, entity.SubjectLotteryEntryDrawn, &data, usecase.EventTicketLotteryResult, func() (string, usecase.AnalyticsProperties) {
		return data.UserID, usecase.AnalyticsProperties{
			"lottery_entry_id": data.LotteryEntryID,
			"ticket_sale_id":   data.TicketSaleID,
			"ticket_type_id":   data.TicketTypeID,
			"result":           data.Result,
			"capture_failed":   data.CaptureFailed,
		}
	}, func() stableEvent { return stableEvent{id: data.LotteryEntryID, at: data.DrawTime} })
}

// HandleCheckoutExpired forwards the ORDER.checkout_expired NATS subject as
// the catalogue event usecase.EventCheckoutExpired, once per Checkout id and
// hold expiry time. Properties: checkout_id,
// ticket_type_id, ticket_count.
func (c *AnalyticsConsumer) HandleCheckoutExpired(msg *message.Message) error {
	var data entity.CheckoutExpiredData
	return c.forward(msg, entity.SubjectCheckoutExpired, &data, usecase.EventCheckoutExpired, func() (string, usecase.AnalyticsProperties) {
		return data.UserID, usecase.AnalyticsProperties{
			"checkout_id":    data.CheckoutID,
			"ticket_type_id": data.TicketTypeID,
			"ticket_count":   data.TicketCount,
		}
	}, func() stableEvent { return stableEvent{id: data.CheckoutID, at: data.HoldExpireTime} })
}

// HandleTicketAdmitted forwards the ORDER.ticket_admitted NATS subject as the
// catalogue event usecase.EventTicketAdmitted, once per Ticket id and admit
// time. Properties: ticket_id,
// event_id.
func (c *AnalyticsConsumer) HandleTicketAdmitted(msg *message.Message) error {
	var data entity.TicketAdmittedData
	return c.forward(msg, entity.SubjectTicketAdmitted, &data, usecase.EventTicketAdmitted, func() (string, usecase.AnalyticsProperties) {
		return data.UserID, usecase.AnalyticsProperties{
			"ticket_id": data.TicketID,
			"event_id":  data.EventID,
		}
	}, func() stableEvent { return stableEvent{id: data.TicketID, at: data.AdmitTime} })
}

// forward is the shared body of the fan-level handlers: it parses the
// CloudEvent into data, reads the distinct id and properties from it with
// build, and enqueues the event. With stable, the event is enqueued once per
// id and time read from data (see usecase.AnalyticsClient.EnqueueOnce); the
// time is truncated to the millisecond so it does not depend on the precision
// it was stored or serialised with, and falls back to the CloudEvent's publish
// time when the data has none (an announcement published before the field
// existed). The skip and failure paths match the hand-written handlers above.
func (c *AnalyticsConsumer) forward(
	msg *message.Message,
	subject string,
	data any,
	event usecase.AnalyticsEventName,
	build func() (userID string, props usecase.AnalyticsProperties),
	stable func() stableEvent,
) error {
	ctx := msg.Context()
	defer c.recordLag(ctx, msg)

	if err := messaging.ParseCloudEventData(msg, data); err != nil {
		c.logger.Error(ctx, "failed to parse "+subject+" event", err)
		c.metrics.RecordMessage(ctx, statusSkippedParseError)
		return apperr.Wrap(err, codes.Internal, "parse "+subject+" event")
	}
	userID, props := build()

	if c.client == nil {
		c.logger.Warn(ctx, "analytics client not configured, skipping forward",
			slog.String("event", string(event)),
			slog.String("user_id", userID),
		)
		c.metrics.RecordMessage(ctx, statusSkippedNilClient)
		return nil
	}

	if userID == "" {
		c.logger.Warn(ctx, subject+" event missing user_id, skipping forward")
		c.metrics.RecordMessage(ctx, statusSkippedEmptyUserID)
		return nil
	}

	var err error
	if stable != nil {
		e := stable()
		at := e.at.UTC().Truncate(time.Millisecond)
		if e.at.IsZero() {
			at, _ = time.Parse(time.RFC3339, msg.Metadata.Get("ce_time"))
		}
		err = c.client.EnqueueOnce(ctx, userID, e.id, at, event, props)
	} else {
		err = c.client.Enqueue(ctx, userID, event, props)
	}
	if err != nil {
		c.logger.Error(ctx, "failed to enqueue analytics event", err,
			slog.String("event", string(event)),
			slog.String("user_id", userID),
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
