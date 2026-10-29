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
// hands the event to AnalyticsClient.Capture with the CloudEvent id as
// the event UUID and the time the fact happened, read from the event
// data and truncated to milliseconds, as the event time. A redelivered
// message is therefore handed off with the same UUID and time, which
// PostHog stores once. The underlying client is non-blocking and absorbs
// transient PostHog failures internally.
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
// catalogue event usecase.EventUserCreated. Properties: signup_month (the
// UTC month of the creation, YYYY-MM) — locale and home_region are absent at
// user-creation time and added by later catalogue events.
func (c *AnalyticsConsumer) HandleUserCreated(msg *message.Message) error {
	var data entity.UserCreatedData
	return c.forward(msg, entity.SubjectUserCreated, &data, usecase.EventUserCreated, func() fact {
		return fact{distinctID: data.UserID, at: data.CreateTime, props: usecase.AnalyticsProperties{
			"signup_month": data.CreateTime.UTC().Format("2006-01"),
		}}
	})
}

// HandleUserLoggedIn forwards the USER.logged_in NATS subject as the
// catalogue event usecase.EventAccountSignin. The subject is published once
// per user-initiated sign-in (Zitadel session.user.checked), never on a token
// refresh, so this event is a returning/active-user signal. No properties
// are attached: the distinct_id (platform UserID) is the whole payload, and
// no PII (email, sub) is ever forwarded.
func (c *AnalyticsConsumer) HandleUserLoggedIn(msg *message.Message) error {
	var data entity.UserLoggedInData
	return c.forward(msg, entity.SubjectUserLoggedIn, &data, usecase.EventAccountSignin, func() fact {
		return fact{distinctID: data.UserID, at: data.LoginTime, props: usecase.AnalyticsProperties{}}
	})
}

// HandleArtistFollowed forwards the ARTIST.followed NATS subject as the
// catalogue event usecase.EventArtistFollowCompleted. Properties:
// artist_id (the optional `source` property is FE-only).
func (c *AnalyticsConsumer) HandleArtistFollowed(msg *message.Message) error {
	var data entity.ArtistFollowedData
	return c.forward(msg, entity.SubjectArtistFollowed, &data, usecase.EventArtistFollowCompleted, func() fact {
		return fact{distinctID: data.UserID, at: data.FollowTime, props: usecase.AnalyticsProperties{"artist_id": data.ArtistID}}
	})
}

// HandleArtistUnfollowed forwards the ARTIST.unfollowed NATS subject as
// the catalogue event usecase.EventArtistUnfollowCompleted. Properties:
// artist_id.
func (c *AnalyticsConsumer) HandleArtistUnfollowed(msg *message.Message) error {
	var data entity.ArtistUnfollowedData
	return c.forward(msg, entity.SubjectArtistUnfollowed, &data, usecase.EventArtistUnfollowCompleted, func() fact {
		return fact{distinctID: data.UserID, at: data.UnfollowTime, props: usecase.AnalyticsProperties{"artist_id": data.ArtistID}}
	})
}

// HandleNotificationSubscribed forwards the NOTIFICATION.subscribed NATS
// subject as the catalogue event usecase.EventNotificationSubscribed.
// Properties: device_type (classifier output from the endpoint host; the
// endpoint itself is never forwarded).
func (c *AnalyticsConsumer) HandleNotificationSubscribed(msg *message.Message) error {
	var data entity.NotificationSubscribedData
	return c.forward(msg, entity.SubjectNotificationSubscribed, &data, usecase.EventNotificationSubscribed, func() fact {
		return fact{distinctID: data.UserID, at: data.SubscribeTime, props: usecase.AnalyticsProperties{"device_type": data.DeviceType}}
	})
}

// HandleNotificationUnsubscribed forwards the NOTIFICATION.unsubscribed NATS
// subject as the catalogue event usecase.EventNotificationUnsubscribed.
// Properties: device_type. Only user-initiated unsubscriptions emit this
// event; the auto-cleanup path on 410 Gone does not.
func (c *AnalyticsConsumer) HandleNotificationUnsubscribed(msg *message.Message) error {
	var data entity.NotificationUnsubscribedData
	return c.forward(msg, entity.SubjectNotificationUnsubscribed, &data, usecase.EventNotificationUnsubscribed, func() fact {
		return fact{distinctID: data.UserID, at: data.UnsubscribeTime, props: usecase.AnalyticsProperties{"device_type": data.DeviceType}}
	})
}

// HandleNotificationDelivered forwards the NOTIFICATION.delivered NATS
// subject as the catalogue event usecase.EventNotificationDelivered.
// Properties: notification_id, type. The distinct_id is the recipient's
// platform UserID. A message without notification_id is skipped like one
// without user_id: "missing correlation key" keeps the metric label set
// fixed.
func (c *AnalyticsConsumer) HandleNotificationDelivered(msg *message.Message) error {
	var data entity.NotificationDeliveredData
	return c.forward(msg, entity.SubjectNotificationDelivered, &data, usecase.EventNotificationDelivered, func() fact {
		f := fact{distinctID: data.UserID, at: data.DeliverTime, props: usecase.AnalyticsProperties{
			"notification_id": data.NotificationID,
			"type":            data.Type,
		}}
		if data.NotificationID == "" {
			f.distinctID = ""
		}
		return f
	})
}

// HandleTicketJourneyStatusChanged forwards the TICKET_JOURNEY.status_changed
// NATS subject as the catalogue event usecase.EventTicketJourneyStatusChanged.
// Properties: event_id, from_status, to_status. from_status is "UNSPECIFIED"
// when no prior journey existed.
func (c *AnalyticsConsumer) HandleTicketJourneyStatusChanged(msg *message.Message) error {
	var data entity.TicketJourneyStatusChangedData
	return c.forward(msg, entity.SubjectTicketJourneyStatusChanged, &data, usecase.EventTicketJourneyStatusChanged, func() fact {
		return fact{distinctID: data.UserID, at: data.ChangeTime, props: usecase.AnalyticsProperties{
			"event_id":    data.EventID,
			"from_status": data.FromStatus,
			"to_status":   data.ToStatus,
		}}
	})
}

// HandleOrganizerCreated forwards the ORGANIZER.created NATS subject as the
// catalogue event usecase.EventOrganizerCreated. It is an admin-actor / group
// event keyed on organizer_id in PostHog; the organizer_id UUID is used as
// the PostHog distinct id. Properties: organizer_id.
func (c *AnalyticsConsumer) HandleOrganizerCreated(msg *message.Message) error {
	var data entity.OrganizerCreatedData
	return c.forward(msg, entity.SubjectOrganizerCreated, &data, usecase.EventOrganizerCreated, func() fact {
		return fact{distinctID: data.OrganizerID, at: data.ActivateTime, props: usecase.AnalyticsProperties{"organizer_id": data.OrganizerID}}
	})
}

// HandleOrganizerArtistAssociated forwards the ORGANIZER.artist_associated
// NATS subject as the catalogue event usecase.EventOrganizerArtistAssociated,
// keyed on organizer_id as the distinct id. Properties: organizer_id,
// artist_id.
func (c *AnalyticsConsumer) HandleOrganizerArtistAssociated(msg *message.Message) error {
	var data entity.OrganizerArtistAssociatedData
	return c.forward(msg, entity.SubjectOrganizerArtistAssociated, &data, usecase.EventOrganizerArtistAssociated, func() fact {
		return fact{distinctID: data.OrganizerID, at: data.AssociateTime, props: usecase.AnalyticsProperties{
			"organizer_id": data.OrganizerID,
			"artist_id":    data.ArtistID,
		}}
	})
}

// HandleArtistHypeChanged forwards the ARTIST.hype_changed NATS subject as the
// catalogue event usecase.EventArtistHypeChanged. Properties: artist_id,
// hype (the level set).
func (c *AnalyticsConsumer) HandleArtistHypeChanged(msg *message.Message) error {
	var data entity.ArtistHypeChangedData
	return c.forward(msg, entity.SubjectArtistHypeChanged, &data, usecase.EventArtistHypeChanged, func() fact {
		return fact{distinctID: data.UserID, at: data.SetTime, props: usecase.AnalyticsProperties{
			"artist_id": data.ArtistID,
			"hype":      data.Hype,
		}}
	})
}

// HandleOrderPaid forwards the ORDER.paid NATS subject as the catalogue event
// usecase.EventOrderPaid at the Order's pay time. Properties: order_id,
// event_id, ticket_count, amount, currency, source ("lottery_entry" or
// "checkout"), and the source's id as lottery_entry_id or checkout_id.
func (c *AnalyticsConsumer) HandleOrderPaid(msg *message.Message) error {
	var data entity.OrderPaidData
	return c.forward(msg, entity.SubjectOrderPaid, &data, usecase.EventOrderPaid, func() fact {
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
		return fact{distinctID: data.UserID, at: data.PayTime, props: props}
	})
}

// HandleLotteryEntryDrawn forwards the ORDER.lottery_entry_drawn NATS subject
// as the catalogue event usecase.EventTicketLotteryResult at the draw time.
// Properties: lottery_entry_id, ticket_sale_id, ticket_stock_id, result,
// capture_failed.
func (c *AnalyticsConsumer) HandleLotteryEntryDrawn(msg *message.Message) error {
	var data entity.LotteryEntryDrawnData
	return c.forward(msg, entity.SubjectLotteryEntryDrawn, &data, usecase.EventTicketLotteryResult, func() fact {
		return fact{distinctID: data.UserID, at: data.DrawTime, props: usecase.AnalyticsProperties{
			"lottery_entry_id": data.LotteryEntryID,
			"ticket_sale_id":   data.TicketSaleID,
			"ticket_stock_id":  data.TicketStockID,
			"result":           data.Result,
			"capture_failed":   data.CaptureFailed,
		}}
	})
}

// HandleCheckoutExpired forwards the ORDER.checkout_expired NATS subject as
// the catalogue event usecase.EventCheckoutExpired at the hold's expiry.
// Properties: checkout_id, ticket_stock_id, ticket_count.
func (c *AnalyticsConsumer) HandleCheckoutExpired(msg *message.Message) error {
	var data entity.CheckoutExpiredData
	return c.forward(msg, entity.SubjectCheckoutExpired, &data, usecase.EventCheckoutExpired, func() fact {
		return fact{distinctID: data.UserID, at: data.HoldExpireTime, props: usecase.AnalyticsProperties{
			"checkout_id":     data.CheckoutID,
			"ticket_stock_id": data.TicketStockID,
			"ticket_count":    data.TicketCount,
		}}
	})
}

// HandleTicketAdmitted forwards the ORDER.ticket_admitted NATS subject as the
// catalogue event usecase.EventTicketAdmitted at the admit time. Properties:
// ticket_id, event_id.
func (c *AnalyticsConsumer) HandleTicketAdmitted(msg *message.Message) error {
	var data entity.TicketAdmittedData
	return c.forward(msg, entity.SubjectTicketAdmitted, &data, usecase.EventTicketAdmitted, func() fact {
		return fact{distinctID: data.UserID, at: data.AdmitTime, props: usecase.AnalyticsProperties{
			"ticket_id": data.TicketID,
			"event_id":  data.EventID,
		}}
	})
}

// fact is what a handler reads from its event data: who it is about, when it
// happened, and the event properties.
type fact struct {
	distinctID string
	at         time.Time
	props      usecase.AnalyticsProperties
}

// forward is the shared body of every handler: it parses the CloudEvent into
// data, reads the fact from it with read, and captures the event with the
// CloudEvent id as its UUID and the fact's time, truncated to milliseconds,
// as its time.
//
// A message without a distinct id is skipped (statusSkippedEmptyUserID). A
// message whose data carries no fact time (published before the time was
// added) is skipped and acknowledged (statusSkippedParseError): redelivering
// it would never add the time, and its publish time is not when the fact
// happened.
func (c *AnalyticsConsumer) forward(
	msg *message.Message,
	subject string,
	data any,
	event usecase.AnalyticsEventName,
	read func() fact,
) error {
	ctx := msg.Context()
	defer c.recordLag(ctx, msg)

	if err := messaging.ParseCloudEventData(msg, data); err != nil {
		c.logger.Error(ctx, "failed to parse "+subject+" event", err)
		c.metrics.RecordMessage(ctx, statusSkippedParseError)
		return apperr.Wrap(err, codes.Internal, "parse "+subject+" event")
	}
	f := read()

	if c.client == nil {
		c.logger.Warn(ctx, "analytics client not configured, skipping forward",
			slog.String("event", string(event)),
			slog.String("distinct_id", f.distinctID),
		)
		c.metrics.RecordMessage(ctx, statusSkippedNilClient)
		return nil
	}

	if f.distinctID == "" {
		c.logger.Warn(ctx, subject+" event missing its correlation key, skipping forward")
		c.metrics.RecordMessage(ctx, statusSkippedEmptyUserID)
		return nil
	}

	if f.at.IsZero() {
		c.logger.Warn(ctx, subject+" event carries no fact time, skipping forward",
			slog.String("distinct_id", f.distinctID),
		)
		c.metrics.RecordMessage(ctx, statusSkippedParseError)
		return nil
	}

	if err := c.client.Capture(ctx, usecase.AnalyticsEvent{
		Name:       event,
		UUID:       cloudEventID(msg),
		Time:       f.at.UTC().Truncate(time.Millisecond),
		DistinctID: f.distinctID,
		Properties: f.props,
	}); err != nil {
		c.logger.Error(ctx, "failed to enqueue analytics event", err,
			slog.String("event", string(event)),
			slog.String("distinct_id", f.distinctID),
		)
		c.metrics.RecordMessage(ctx, statusEnqueueError)
		return apperr.Wrap(err, codes.Internal, "enqueue analytics event")
	}

	c.metrics.RecordMessage(ctx, statusForwarded)
	return nil
}

// cloudEventID returns the message's CloudEvent id (ce_id), which NewEvent
// sets equal to the message UUID; the UUID stands in when ce_id is absent.
func cloudEventID(msg *message.Message) string {
	if id := msg.Metadata.Get("ce_id"); id != "" {
		return id
	}
	return msg.UUID
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
