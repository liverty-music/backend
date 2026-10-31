package event

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/liverty-music/backend/internal/adapter/pb"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/messaging"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/pannpers/go-logging/logging"
	"google.golang.org/protobuf/proto"
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
// method that decodes the announcement (the entity of an outbox
// announcement, or the JSON of a message published before the outbox),
// derives the catalogue AnalyticsEventName, sanitises properties per the PII
// policy documented in specification/docs/analytics/event-catalog.md, and
// hands the event to AnalyticsClient.Capture with the announcement id as the
// event UUID and the time the fact happened, truncated to milliseconds, as
// the event time. That time is the entity's own (an Order's pay time, a
// Ticket's admit time, a User's last login time, a Notification's deliver
// time) or, for an entity that has none, the time the announcement was
// recorded, in the transaction of the change. A redelivered message is
// therefore handed off with the same UUID and time, which PostHog stores
// once. Values not on the entity are read by id: the event of an Order's
// TicketStock, and the Order of a LotteryEntry or a Checkout. A previous
// hype level or journey status is not sent: it is the fan's previous signal.
// The underlying client is non-blocking and absorbs transient PostHog
// failures internally.
//
// If client is nil — the local-development convention used when the
// PostHog project API key is not configured — every Handle method
// logs a warning and acknowledges without forwarding.
//
// metrics is required and MUST NOT be nil; the DI graph constructs a
// no-op-meter implementation when telemetry is disabled.
type AnalyticsConsumer struct {
	client  usecase.AnalyticsClient
	metrics usecase.AnalyticsConsumerMetrics
	orders  orderReader
	stocks  ticketStockReader
	logger  *logging.Logger
}

// orderReader reads the Order of a LotteryEntry or a Checkout (Order.Get).
type orderReader interface {
	Get(ctx context.Context, id entity.OrderID) (*entity.Order, error)
}

// NewAnalyticsConsumer constructs an AnalyticsConsumer. Passing a nil
// client puts the consumer into log-and-skip mode.
func NewAnalyticsConsumer(
	client usecase.AnalyticsClient,
	metrics usecase.AnalyticsConsumerMetrics,
	orders orderReader,
	stocks ticketStockReader,
	logger *logging.Logger,
) *AnalyticsConsumer {
	return &AnalyticsConsumer{client: client, metrics: metrics, orders: orders, stocks: stocks, logger: logger}
}

// HandleUserCreated forwards the USER.created NATS subject as the
// catalogue event usecase.EventUserCreated. Properties: signup_month (the
// UTC month of the creation, YYYY-MM) — locale and home_region are absent at
// user-creation time and added by later catalogue events.
func (c *AnalyticsConsumer) HandleUserCreated(msg *message.Message) error {
	return c.forward(msg, entity.SubjectUserCreated, usecase.EventUserCreated, func() (fact, error) {
		signup := func(userID string, at time.Time) fact {
			return fact{distinctID: userID, at: at, props: usecase.AnalyticsProperties{"signup_month": at.UTC().Format("2006-01")}}
		}
		return decodeFact(msg, func(u *entityv1.User) (fact, error) {
			return signup(u.GetId().GetValue(), announcedAt(msg)), nil
		}, func(d *entity.UserCreatedData) fact { return signup(d.UserID, d.CreateTime) })
	})
}

// HandleUserLoggedIn forwards the USER.logged_in NATS subject as the
// catalogue event usecase.EventAccountSignin at the User's last login time.
// The subject is announced once per user-initiated sign-in (Zitadel
// session.user.checked), never on a token refresh, so this event is a
// returning/active-user signal. No properties are attached: the distinct_id
// (platform UserID) is the whole payload, and no PII (email, sub) is ever
// forwarded.
func (c *AnalyticsConsumer) HandleUserLoggedIn(msg *message.Message) error {
	return c.forward(msg, entity.SubjectUserLoggedIn, usecase.EventAccountSignin, func() (fact, error) {
		return decodeFact(msg, func(u *entityv1.User) (fact, error) {
			return fact{distinctID: u.GetId().GetValue(), at: u.GetLastLoginTime().AsTime(), props: usecase.AnalyticsProperties{}}, nil
		}, func(d *entity.UserLoggedInData) fact {
			return fact{distinctID: d.UserID, at: d.LoginTime, props: usecase.AnalyticsProperties{}}
		})
	})
}

// HandleArtistFollowed forwards the ARTIST.followed NATS subject as the
// catalogue event usecase.EventArtistFollowCompleted. Properties:
// artist_id (the optional `source` property is FE-only).
func (c *AnalyticsConsumer) HandleArtistFollowed(msg *message.Message) error {
	return c.forward(msg, entity.SubjectArtistFollowed, usecase.EventArtistFollowCompleted, func() (fact, error) {
		return decodeFact(msg, func(f *entityv1.Follow) (fact, error) {
			return followFact(f.GetUserId().GetValue(), f.GetArtistId().GetValue(), announcedAt(msg)), nil
		}, func(d *entity.ArtistFollowedData) fact { return followFact(d.UserID, d.ArtistID, d.FollowTime) })
	})
}

// HandleArtistUnfollowed forwards the ARTIST.unfollowed NATS subject as
// the catalogue event usecase.EventArtistUnfollowCompleted. Properties:
// artist_id.
func (c *AnalyticsConsumer) HandleArtistUnfollowed(msg *message.Message) error {
	return c.forward(msg, entity.SubjectArtistUnfollowed, usecase.EventArtistUnfollowCompleted, func() (fact, error) {
		return decodeFact(msg, func(f *entityv1.Follow) (fact, error) {
			return followFact(f.GetUserId().GetValue(), f.GetArtistId().GetValue(), announcedAt(msg)), nil
		}, func(d *entity.ArtistUnfollowedData) fact { return followFact(d.UserID, d.ArtistID, d.UnfollowTime) })
	})
}

func followFact(userID, artistID string, at time.Time) fact {
	return fact{distinctID: userID, at: at, props: usecase.AnalyticsProperties{"artist_id": artistID}}
}

// HandleNotificationSubscribed forwards the NOTIFICATION.subscribed NATS
// subject as the catalogue event usecase.EventNotificationSubscribed.
// Properties: device_type (the device family of the push endpoint; the
// endpoint itself is never forwarded).
func (c *AnalyticsConsumer) HandleNotificationSubscribed(msg *message.Message) error {
	return c.forward(msg, entity.SubjectNotificationSubscribed, usecase.EventNotificationSubscribed, func() (fact, error) {
		return decodeFact(msg, func(p *entityv1.PushSubscription) (fact, error) {
			return deviceFact(p.GetUserId().GetValue(), pb.DeviceFamilyFromProto(p.GetDeviceFamily()), announcedAt(msg)), nil
		}, func(d *entity.NotificationSubscribedData) fact {
			return deviceFact(d.UserID, d.DeviceType, d.SubscribeTime)
		})
	})
}

// HandleNotificationUnsubscribed forwards the NOTIFICATION.unsubscribed NATS
// subject as the catalogue event usecase.EventNotificationUnsubscribed.
// Properties: device_type. Only user-initiated unsubscriptions emit this
// event; the auto-cleanup path on 410 Gone does not.
func (c *AnalyticsConsumer) HandleNotificationUnsubscribed(msg *message.Message) error {
	return c.forward(msg, entity.SubjectNotificationUnsubscribed, usecase.EventNotificationUnsubscribed, func() (fact, error) {
		return decodeFact(msg, func(p *entityv1.PushSubscription) (fact, error) {
			return deviceFact(p.GetUserId().GetValue(), pb.DeviceFamilyFromProto(p.GetDeviceFamily()), announcedAt(msg)), nil
		}, func(d *entity.NotificationUnsubscribedData) fact {
			return deviceFact(d.UserID, d.DeviceType, d.UnsubscribeTime)
		})
	})
}

func deviceFact(userID, deviceType string, at time.Time) fact {
	return fact{distinctID: userID, at: at, props: usecase.AnalyticsProperties{"device_type": deviceType}}
}

// HandleNotificationDelivered forwards the NOTIFICATION.delivered NATS
// subject as the catalogue event usecase.EventNotificationDelivered at the
// Notification's deliver time. Properties: notification_id, type. The
// distinct_id is the recipient's platform UserID. A message without
// notification_id is skipped like one without user_id: "missing correlation
// key" keeps the metric label set fixed.
func (c *AnalyticsConsumer) HandleNotificationDelivered(msg *message.Message) error {
	return c.forward(msg, entity.SubjectNotificationDelivered, usecase.EventNotificationDelivered, func() (fact, error) {
		delivered := func(userID, notificationID, typ string, at time.Time) fact {
			f := fact{distinctID: userID, at: at, props: usecase.AnalyticsProperties{"notification_id": notificationID, "type": typ}}
			if notificationID == "" {
				f.distinctID = ""
			}
			return f
		}
		return decodeFact(msg, func(n *entityv1.Notification) (fact, error) {
			return delivered(n.GetUserId().GetValue(), n.GetId().GetValue(), string(pb.NotificationTypeFromProto(n.GetType())), n.GetDeliverTime().AsTime()), nil
		}, func(d *entity.NotificationDeliveredData) fact {
			return delivered(d.UserID, d.NotificationID, d.Type, d.DeliverTime)
		})
	})
}

// HandleTicketJourneyStatusChanged forwards the TICKET_JOURNEY.status_changed
// NATS subject as the catalogue event usecase.EventTicketJourneyStatusChanged.
// Properties: event_id, to_status. The status before the change is the
// to_status of the fan's previous signal for the event; a message published
// before the outbox still carries it as from_status.
func (c *AnalyticsConsumer) HandleTicketJourneyStatusChanged(msg *message.Message) error {
	return c.forward(msg, entity.SubjectTicketJourneyStatusChanged, usecase.EventTicketJourneyStatusChanged, func() (fact, error) {
		return decodeFact(msg, func(j *entityv1.TicketJourney) (fact, error) {
			return fact{distinctID: j.GetUserId().GetValue(), at: announcedAt(msg), props: usecase.AnalyticsProperties{
				"event_id":  j.GetEventId().GetValue(),
				"to_status": pb.TicketJourneyStatusFromProto[j.GetStatus()].String(),
			}}, nil
		}, func(d *entity.TicketJourneyStatusChangedData) fact {
			return fact{distinctID: d.UserID, at: d.ChangeTime, props: usecase.AnalyticsProperties{
				"event_id":    d.EventID,
				"from_status": d.FromStatus,
				"to_status":   d.ToStatus,
			}}
		})
	})
}

// HandleOrganizerCreated forwards the ORGANIZER.created NATS subject as the
// catalogue event usecase.EventOrganizerCreated. It is an admin-actor / group
// event keyed on organizer_id in PostHog; the organizer_id UUID is used as
// the PostHog distinct id. Properties: organizer_id.
func (c *AnalyticsConsumer) HandleOrganizerCreated(msg *message.Message) error {
	return c.forward(msg, entity.SubjectOrganizerCreated, usecase.EventOrganizerCreated, func() (fact, error) {
		created := func(organizerID string, at time.Time) fact {
			return fact{distinctID: organizerID, at: at, props: usecase.AnalyticsProperties{"organizer_id": organizerID}}
		}
		return decodeFact(msg, func(o *entityv1.Organizer) (fact, error) {
			return created(o.GetId().GetValue(), announcedAt(msg)), nil
		}, func(d *entity.OrganizerCreatedData) fact { return created(d.OrganizerID, d.ActivateTime) })
	})
}

// HandleOrganizerArtistAssociated forwards the ORGANIZER.artist_associated
// NATS subject as the catalogue event usecase.EventOrganizerArtistAssociated,
// keyed on organizer_id as the distinct id. Properties: organizer_id,
// artist_id.
func (c *AnalyticsConsumer) HandleOrganizerArtistAssociated(msg *message.Message) error {
	return c.forward(msg, entity.SubjectOrganizerArtistAssociated, usecase.EventOrganizerArtistAssociated, func() (fact, error) {
		associated := func(organizerID, artistID string, at time.Time) fact {
			return fact{distinctID: organizerID, at: at, props: usecase.AnalyticsProperties{"organizer_id": organizerID, "artist_id": artistID}}
		}
		return decodeFact(msg, func(a *entityv1.Artist) (fact, error) {
			return associated(a.GetOrganizerId().GetValue(), a.GetId().GetValue(), announcedAt(msg)), nil
		}, func(d *entity.OrganizerArtistAssociatedData) fact {
			return associated(d.OrganizerID, d.ArtistID, d.AssociateTime)
		})
	})
}

// HandleArtistHypeChanged forwards the ARTIST.hype_changed NATS subject as the
// catalogue event usecase.EventArtistHypeChanged. Properties: artist_id,
// hype (the level set).
func (c *AnalyticsConsumer) HandleArtistHypeChanged(msg *message.Message) error {
	return c.forward(msg, entity.SubjectArtistHypeChanged, usecase.EventArtistHypeChanged, func() (fact, error) {
		hype := func(userID, artistID, level string, at time.Time) fact {
			return fact{distinctID: userID, at: at, props: usecase.AnalyticsProperties{"artist_id": artistID, "hype": level}}
		}
		return decodeFact(msg, func(f *entityv1.Follow) (fact, error) {
			return hype(f.GetUserId().GetValue(), f.GetArtistId().GetValue(), string(pb.HypeFromProto[f.GetHype()]), announcedAt(msg)), nil
		}, func(d *entity.ArtistHypeChangedData) fact { return hype(d.UserID, d.ArtistID, d.Hype, d.SetTime) })
	})
}

// HandleOrderPaid forwards the ORDER.paid NATS subject as the catalogue event
// usecase.EventOrderPaid at the Order's pay time. Properties: order_id,
// event_id (of the Order's TicketStock), ticket_count, amount, currency,
// source ("lottery_entry" or "checkout"), and the source's id as
// lottery_entry_id or checkout_id.
func (c *AnalyticsConsumer) HandleOrderPaid(msg *message.Message) error {
	return c.forward(msg, entity.SubjectOrderPaid, usecase.EventOrderPaid, func() (fact, error) {
		return decodeFact(msg, func(o *entityv1.Order) (fact, error) {
			stock, err := c.stocks.Get(msg.Context(), entity.TicketStockID(o.GetTicketStockId().GetValue()), time.Now())
			if err != nil {
				return fact{}, fmt.Errorf("read the TicketStock of order %s: %w", o.GetId().GetValue(), err)
			}
			return orderPaidFact(entity.OrderPaidData{
				OrderID: o.GetId().GetValue(), UserID: o.GetUserId().GetValue(), EventID: stock.EventID,
				TicketCount: int(o.GetTicketCount()), Amount: o.GetAmount(), Currency: o.GetCurrency(),
				LotteryEntryID: o.GetLotteryEntryId().GetValue(), CheckoutID: o.GetCheckoutId().GetValue(),
				PayTime: o.GetPayTime().AsTime(),
			}), nil
		}, func(d *entity.OrderPaidData) fact { return orderPaidFact(*d) })
	})
}

func orderPaidFact(data entity.OrderPaidData) fact {
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
}

// HandleLotteryEntryDrawn forwards the ORDER.lottery_entry_drawn NATS subject
// as the catalogue event usecase.EventTicketLotteryResult at the draw time.
// Properties: lottery_entry_id, ticket_sale_id, ticket_stock_id, result,
// capture_failed. The fan and the TicketStock are the entry's Order's.
func (c *AnalyticsConsumer) HandleLotteryEntryDrawn(msg *message.Message) error {
	return c.forward(msg, entity.SubjectLotteryEntryDrawn, usecase.EventTicketLotteryResult, func() (fact, error) {
		return decodeFact(msg, func(e *entityv1.LotteryEntry) (fact, error) {
			order, err := c.orders.Get(msg.Context(), entity.OrderID(e.GetOrderId().GetValue()))
			if err != nil {
				return fact{}, fmt.Errorf("read the Order of lottery entry %s: %w", e.GetId().GetValue(), err)
			}
			return drawnFact(entity.LotteryEntryDrawnData{
				LotteryEntryID: e.GetId().GetValue(), UserID: string(order.UserID), TicketSaleID: e.GetTicketSaleId().GetValue(),
				TicketStockID: string(order.TicketStockID), Result: pb.LotteryEntryStateFromProto(e.GetState()).String(),
				CaptureFailed: e.GetLostReason() == entityv1.LotteryEntryLostReason_LOTTERY_ENTRY_LOST_REASON_CARD_NOT_CHARGED,
				DrawTime:      announcedAt(msg),
			}), nil
		}, func(d *entity.LotteryEntryDrawnData) fact { return drawnFact(*d) })
	})
}

func drawnFact(d entity.LotteryEntryDrawnData) fact {
	return fact{distinctID: d.UserID, at: d.DrawTime, props: usecase.AnalyticsProperties{
		"lottery_entry_id": d.LotteryEntryID,
		"ticket_sale_id":   d.TicketSaleID,
		"ticket_stock_id":  d.TicketStockID,
		"result":           d.Result,
		"capture_failed":   d.CaptureFailed,
	}}
}

// HandleCheckoutExpired forwards the ORDER.checkout_expired NATS subject as
// the catalogue event usecase.EventCheckoutExpired at the hold's expiry.
// Properties: checkout_id, ticket_stock_id, ticket_count. The fan, the
// TicketStock and the count are the Checkout's Order's.
func (c *AnalyticsConsumer) HandleCheckoutExpired(msg *message.Message) error {
	return c.forward(msg, entity.SubjectCheckoutExpired, usecase.EventCheckoutExpired, func() (fact, error) {
		return decodeFact(msg, func(co *entityv1.Checkout) (fact, error) {
			order, err := c.orders.Get(msg.Context(), entity.OrderID(co.GetOrderId().GetValue()))
			if err != nil {
				return fact{}, fmt.Errorf("read the Order of checkout %s: %w", co.GetId().GetValue(), err)
			}
			return expiredFact(entity.CheckoutExpiredData{
				CheckoutID: co.GetId().GetValue(), UserID: string(order.UserID), TicketStockID: string(order.TicketStockID),
				TicketCount: order.TicketCount, HoldExpireTime: co.GetHoldExpireTime().AsTime(),
			}), nil
		}, func(d *entity.CheckoutExpiredData) fact { return expiredFact(*d) })
	})
}

func expiredFact(d entity.CheckoutExpiredData) fact {
	return fact{distinctID: d.UserID, at: d.HoldExpireTime, props: usecase.AnalyticsProperties{
		"checkout_id":     d.CheckoutID,
		"ticket_stock_id": d.TicketStockID,
		"ticket_count":    d.TicketCount,
	}}
}

// HandleTicketAdmitted forwards the ORDER.ticket_admitted NATS subject as the
// catalogue event usecase.EventTicketAdmitted at the admit time. Properties:
// ticket_id, event_id.
func (c *AnalyticsConsumer) HandleTicketAdmitted(msg *message.Message) error {
	return c.forward(msg, entity.SubjectTicketAdmitted, usecase.EventTicketAdmitted, func() (fact, error) {
		admitted := func(userID, ticketID, eventID string, at time.Time) fact {
			return fact{distinctID: userID, at: at, props: usecase.AnalyticsProperties{"ticket_id": ticketID, "event_id": eventID}}
		}
		return decodeFact(msg, func(tk *entityv1.Ticket) (fact, error) {
			return admitted(tk.GetUserId().GetValue(), tk.GetId().GetValue(), tk.GetEventId().GetValue(), tk.GetAdmitTime().AsTime()), nil
		}, func(d *entity.TicketAdmittedData) fact { return admitted(d.UserID, d.TicketID, d.EventID, d.AdmitTime) })
	})
}

// fact is what a handler reads from its event data: who it is about, when it
// happened, and the event properties.
type fact struct {
	distinctID string
	at         time.Time
	props      usecase.AnalyticsProperties
}

// decodeFact reads the fact of msg: from its protobuf entity P when it is an
// outbox announcement, or from the JSON J of a message published before the
// outbox.
func decodeFact[P any, PP interface {
	*P
	proto.Message
}, J any](msg *message.Message, fromProto func(PP) (fact, error), fromJSON func(*J) fact) (fact, error) {
	if messaging.IsProtobuf(msg) {
		payload := PP(new(P))
		if err := messaging.ParseProtobufData(msg, payload); err != nil {
			return fact{}, err
		}
		return fromProto(payload)
	}
	var data J
	if err := messaging.ParseCloudEventData(msg, &data); err != nil {
		return fact{}, err
	}
	return fromJSON(&data), nil
}

// announcedAt is the time the announcement was recorded, in the transaction
// of the change it announces (ce_time); zero when it is missing.
func announcedAt(msg *message.Message) time.Time {
	at, err := time.Parse(time.RFC3339Nano, msg.Metadata.Get("ce_time"))
	if err != nil {
		return time.Time{}
	}
	return at
}

// forward is the shared body of every handler: it reads the fact of the
// message with read, and captures the event with the announcement id as its
// UUID and the fact's time, truncated to milliseconds, as its time.
//
// A message without a distinct id is skipped (statusSkippedEmptyUserID). A
// message that carries no fact time (published before the time was added)
// is skipped and acknowledged (statusSkippedParseError): redelivering it
// would never add the time, and its publish time is not when the fact
// happened.
func (c *AnalyticsConsumer) forward(
	msg *message.Message,
	subject string,
	event usecase.AnalyticsEventName,
	read func() (fact, error),
) error {
	ctx := msg.Context()
	defer c.recordLag(ctx, msg)

	f, err := read()
	if err != nil {
		c.logger.Error(ctx, "failed to parse "+subject+" event", err)
		c.metrics.RecordMessage(ctx, statusSkippedParseError)
		return apperr.Wrap(err, codes.Internal, "parse "+subject+" event")
	}

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

	if f.at.IsZero() || f.at.Unix() == 0 {
		c.logger.Warn(ctx, subject+" event carries no fact time, skipping forward",
			slog.String("distinct_id", f.distinctID),
		)
		c.metrics.RecordMessage(ctx, statusSkippedParseError)
		return nil
	}

	if err := c.client.Capture(ctx, usecase.AnalyticsEvent{
		Name:       event,
		UUID:       announcementID(msg),
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
