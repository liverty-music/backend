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
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/liverty-music/backend/pkg/eventmsg"
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
// method that decodes the announcement (the entity it carries), derives the
// catalogue AnalyticsEventName, sanitises properties per the PII policy
// documented in specification/docs/analytics/event-catalog.md, and
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
		})
	})
}

// HandleUserSignedIn forwards the USER.signed_in NATS subject as the
// catalogue event usecase.EventUserSignedIn at the User's last login time.
// The subject is announced once per user-initiated sign-in (Zitadel
// session.user.checked), never on a token refresh, so this event is a
// returning/active-user signal. No properties are attached: the distinct_id
// (platform UserID) is the whole payload, and no PII (email, sub) is ever
// forwarded.
func (c *AnalyticsConsumer) HandleUserSignedIn(msg *message.Message) error {
	return c.forward(msg, entity.SubjectUserSignedIn, usecase.EventUserSignedIn, func() (fact, error) {
		return decodeFact(msg, func(u *entityv1.User) (fact, error) {
			return fact{distinctID: u.GetId().GetValue(), at: u.GetLastLoginTime().AsTime(), props: usecase.AnalyticsProperties{}}, nil
		})
	})
}

// HandleFollowCreated forwards the FOLLOW.created NATS subject as the
// catalogue event usecase.EventFollowCreated. Properties:
// artist_id (the optional `source` property is FE-only).
func (c *AnalyticsConsumer) HandleFollowCreated(msg *message.Message) error {
	return c.forward(msg, entity.SubjectFollowCreated, usecase.EventFollowCreated, func() (fact, error) {
		return decodeFact(msg, func(f *entityv1.Follow) (fact, error) {
			return followFact(f.GetUserId().GetValue(), f.GetArtistId().GetValue(), announcedAt(msg)), nil
		})
	})
}

// HandleFollowRemoved forwards the FOLLOW.removed NATS subject as the
// catalogue event usecase.EventFollowRemoved. Properties: artist_id.
func (c *AnalyticsConsumer) HandleFollowRemoved(msg *message.Message) error {
	return c.forward(msg, entity.SubjectFollowRemoved, usecase.EventFollowRemoved, func() (fact, error) {
		return decodeFact(msg, func(f *entityv1.Follow) (fact, error) {
			return followFact(f.GetUserId().GetValue(), f.GetArtistId().GetValue(), announcedAt(msg)), nil
		})
	})
}

func followFact(userID, artistID string, at time.Time) fact {
	return fact{distinctID: userID, at: at, props: usecase.AnalyticsProperties{"artist_id": artistID}}
}

// HandlePushSubscriptionSubscribed forwards the PUSH_SUBSCRIPTION.subscribed
// NATS subject as the catalogue event usecase.EventPushSubscriptionSubscribed.
// Properties: device_type (the device family of the push endpoint; the
// endpoint itself is never forwarded).
func (c *AnalyticsConsumer) HandlePushSubscriptionSubscribed(msg *message.Message) error {
	return c.forward(msg, entity.SubjectPushSubscriptionSubscribed, usecase.EventPushSubscriptionSubscribed, func() (fact, error) {
		return decodeFact(msg, func(p *entityv1.PushSubscription) (fact, error) {
			return deviceFact(p.GetUserId().GetValue(), pb.DeviceFamilyFromProto(p.GetDeviceFamily()), announcedAt(msg)), nil
		})
	})
}

// HandlePushSubscriptionUnsubscribed forwards the
// PUSH_SUBSCRIPTION.unsubscribed NATS subject as the catalogue event
// usecase.EventPushSubscriptionUnsubscribed.
// Properties: device_type. Only user-initiated unsubscriptions emit this
// event; the auto-cleanup path on 410 Gone does not.
func (c *AnalyticsConsumer) HandlePushSubscriptionUnsubscribed(msg *message.Message) error {
	return c.forward(msg, entity.SubjectPushSubscriptionUnsubscribed, usecase.EventPushSubscriptionUnsubscribed, func() (fact, error) {
		return decodeFact(msg, func(p *entityv1.PushSubscription) (fact, error) {
			return deviceFact(p.GetUserId().GetValue(), pb.DeviceFamilyFromProto(p.GetDeviceFamily()), announcedAt(msg)), nil
		})
	})
}

func deviceFact(userID, deviceType string, at time.Time) fact {
	return fact{distinctID: userID, at: at, props: usecase.AnalyticsProperties{"device_type": deviceType}}
}

// HandleTicketJourneyStatusChanged forwards the TICKET_JOURNEY.status_changed
// NATS subject as the catalogue event usecase.EventTicketJourneyStatusChanged.
// Properties: event_id, to_status, set_by (fan, platform or inference). The
// status before the change is the to_status of the fan's previous signal for
// the event.
func (c *AnalyticsConsumer) HandleTicketJourneyStatusChanged(msg *message.Message) error {
	return c.forward(msg, entity.SubjectTicketJourneyStatusChanged, usecase.EventTicketJourneyStatusChanged, func() (fact, error) {
		return decodeFact(msg, func(j *entityv1.TicketJourney) (fact, error) {
			props := usecase.AnalyticsProperties{
				"event_id":  j.GetEventId().GetValue(),
				"to_status": pb.TicketJourneyStatusFromProto[j.GetStatus()].String(),
			}
			withSetBy(props, string(pb.TicketJourneySetByFromProto[j.GetStatusSetBy()]))
			return fact{distinctID: j.GetUserId().GetValue(), at: announcedAt(msg), props: props}, nil
		})
	})
}

// withSetBy adds set_by to props. An announcement recorded before who set the
// value was stored carries none, and gets no set_by.
func withSetBy(props usecase.AnalyticsProperties, setBy string) {
	if setBy != "" {
		props["set_by"] = setBy
	}
}

// HandleOrganizerActivated forwards the ORGANIZER.activated NATS subject as
// the catalogue event usecase.EventOrganizerActivated. It is an admin-actor / group
// event keyed on organizer_id in PostHog; the organizer_id UUID is used as
// the PostHog distinct id. Properties: organizer_id.
func (c *AnalyticsConsumer) HandleOrganizerActivated(msg *message.Message) error {
	return c.forward(msg, entity.SubjectOrganizerActivated, usecase.EventOrganizerActivated, func() (fact, error) {
		created := func(organizerID string, at time.Time) fact {
			return fact{distinctID: organizerID, at: at, props: usecase.AnalyticsProperties{"organizer_id": organizerID}}
		}
		return decodeFact(msg, func(o *entityv1.Organizer) (fact, error) {
			return created(o.GetId().GetValue(), announcedAt(msg)), nil
		})
	})
}

// HandleArtistRepresented forwards the ARTIST.represented NATS subject as
// the catalogue event usecase.EventArtistRepresented,
// keyed on organizer_id as the distinct id. Properties: organizer_id,
// artist_id.
func (c *AnalyticsConsumer) HandleArtistRepresented(msg *message.Message) error {
	return c.forward(msg, entity.SubjectArtistRepresented, usecase.EventArtistRepresented, func() (fact, error) {
		associated := func(organizerID, artistID string, at time.Time) fact {
			return fact{distinctID: organizerID, at: at, props: usecase.AnalyticsProperties{"organizer_id": organizerID, "artist_id": artistID}}
		}
		return decodeFact(msg, func(a *entityv1.Artist) (fact, error) {
			return associated(a.GetOrganizerId().GetValue(), a.GetId().GetValue(), announcedAt(msg)), nil
		})
	})
}

// HandleFollowHypeChanged forwards the FOLLOW.hype_changed NATS subject as
// the catalogue event usecase.EventFollowHypeChanged. Properties: artist_id,
// hype (the level set), set_by (fan or inference).
func (c *AnalyticsConsumer) HandleFollowHypeChanged(msg *message.Message) error {
	return c.forward(msg, entity.SubjectFollowHypeChanged, usecase.EventFollowHypeChanged, func() (fact, error) {
		return decodeFact(msg, func(f *entityv1.Follow) (fact, error) {
			props := usecase.AnalyticsProperties{"artist_id": f.GetArtistId().GetValue(), "hype": string(pb.HypeFromProto[f.GetHype()])}
			withSetBy(props, string(pb.HypeSetByFromProto[f.GetHypeSetBy()]))
			return fact{distinctID: f.GetUserId().GetValue(), at: announcedAt(msg), props: props}, nil
		})
	})
}

// HandleOrderPaid forwards the ORDER.paid NATS subject as the catalogue event
// usecase.EventOrderPaid at the Order's pay time. Properties: order_id,
// event_id (of the Order's TicketStock), ticket_count, amount, currency,
// source ("lottery_entry", "checkout" or "resale_entry"), and exactly the
// source's id as lottery_entry_id, checkout_id or resale_entry_id.
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
				ResaleEntryID: o.GetResaleEntryId().GetValue(),
				PayTime:       o.GetPayTime().AsTime(),
			}), nil
		})
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
	source := data.Source()
	switch source {
	case entity.OrderSourceLotteryEntry:
		props["lottery_entry_id"] = data.LotteryEntryID
	case entity.OrderSourceCheckout:
		props["checkout_id"] = data.CheckoutID
	case entity.OrderSourceResaleEntry:
		props["resale_entry_id"] = data.ResaleEntryID
	default:
		return fact{distinctID: data.UserID, at: data.PayTime, props: props}
	}
	props["source"] = source.String()
	return fact{distinctID: data.UserID, at: data.PayTime, props: props}
}

// HandleLotteryEntryResult forwards the LOTTERY_ENTRY.won and
// LOTTERY_ENTRY.lost NATS subjects as the catalogue events
// usecase.EventLotteryEntryWon and usecase.EventLotteryEntryLost, told apart
// by the subject, at the time the result was announced. Properties:
// lottery_entry_id, ticket_sale_id, ticket_stock_id, result, capture_failed.
// The fan and the TicketStock are the entry's Order's. A message under
// another subject is not a result and fails as unparsable.
func (c *AnalyticsConsumer) HandleLotteryEntryResult(msg *message.Message) error {
	subject := eventmsg.CloudEventType(msg)
	event, result := usecase.EventLotteryEntryWon, "won"
	switch subject {
	case entity.SubjectLotteryEntryWon:
	case entity.SubjectLotteryEntryLost:
		event, result = usecase.EventLotteryEntryLost, "lost"
	default:
		return c.forward(msg, subject, event, func() (fact, error) {
			return fact{}, fmt.Errorf("not a lottery entry result: %q", subject)
		})
	}
	return c.forward(msg, subject, event, func() (fact, error) {
		return decodeFact(msg, func(e *entityv1.LotteryEntry) (fact, error) {
			order, err := c.orders.Get(msg.Context(), entity.OrderID(e.GetOrderId().GetValue()))
			if err != nil {
				return fact{}, fmt.Errorf("read the Order of lottery entry %s: %w", e.GetId().GetValue(), err)
			}
			return drawnFact(lotteryEntryDrawn{
				LotteryEntryID: e.GetId().GetValue(), UserID: string(order.UserID), TicketSaleID: e.GetTicketSaleId().GetValue(),
				TicketStockID: string(order.TicketStockID), Result: result,
				CaptureFailed: e.GetLostReason() == entityv1.LotteryEntryLostReason_LOTTERY_ENTRY_LOST_REASON_CARD_NOT_CHARGED,
				DrawTime:      announcedAt(msg),
			}), nil
		})
	})
}

func drawnFact(d lotteryEntryDrawn) fact {
	return fact{distinctID: d.UserID, at: d.DrawTime, props: usecase.AnalyticsProperties{
		"lottery_entry_id": d.LotteryEntryID,
		"ticket_sale_id":   d.TicketSaleID,
		"ticket_stock_id":  d.TicketStockID,
		"result":           d.Result,
		"capture_failed":   d.CaptureFailed,
	}}
}

// HandleCheckoutExpired forwards the CHECKOUT.expired NATS subject as
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
			return expiredFact(checkoutExpired{
				CheckoutID: co.GetId().GetValue(), UserID: string(order.UserID), TicketStockID: string(order.TicketStockID),
				TicketCount: order.TicketCount, HoldExpireTime: co.GetHoldExpireTime().AsTime(),
			}), nil
		})
	})
}

func expiredFact(d checkoutExpired) fact {
	return fact{distinctID: d.UserID, at: d.HoldExpireTime, props: usecase.AnalyticsProperties{
		"checkout_id":     d.CheckoutID,
		"ticket_stock_id": d.TicketStockID,
		"ticket_count":    d.TicketCount,
	}}
}

// HandleTicketAdmitted forwards the TICKET.admitted NATS subject as the
// catalogue event usecase.EventTicketAdmitted at the admit time. Properties:
// ticket_id, event_id.
func (c *AnalyticsConsumer) HandleTicketAdmitted(msg *message.Message) error {
	return c.forward(msg, entity.SubjectTicketAdmitted, usecase.EventTicketAdmitted, func() (fact, error) {
		admitted := func(userID, ticketID, eventID string, at time.Time) fact {
			return fact{distinctID: userID, at: at, props: usecase.AnalyticsProperties{"ticket_id": ticketID, "event_id": eventID}}
		}
		return decodeFact(msg, func(tk *entityv1.Ticket) (fact, error) {
			return admitted(tk.GetUserId().GetValue(), tk.GetId().GetValue(), tk.GetEventId().GetValue(), tk.GetAdmitTime().AsTime()), nil
		})
	})
}

// fact is what a handler reads from its event data: who it is about, when it
// happened, and the event properties.
type fact struct {
	distinctID string
	at         time.Time
	props      usecase.AnalyticsProperties
}

// decodeFact reads the fact of msg from its protobuf entity P.
func decodeFact[P any, PP interface {
	*P
	proto.Message
}](msg *message.Message, fromProto func(PP) (fact, error)) (fact, error) {
	payload := PP(new(P))
	if err := eventmsg.ParseProtobufData(msg, payload); err != nil {
		return fact{}, err
	}
	return fromProto(payload)
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
// message that carries no fact time (an entity without its own time, or an
// announcement without ce_time) is skipped and acknowledged
// (statusSkippedParseError): redelivering it would never add the time, and
// the delivery time is not when the fact happened.
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

// lotteryEntryDrawn is what the analytics consumer reads from a
// LOTTERY_ENTRY.won or LOTTERY_ENTRY.lost announcement and the entry's
// Order: ids only, never personal data. It is forwarded as the catalogue
// event lottery_entry.won or lottery_entry.lost.
type lotteryEntryDrawn struct {
	// LotteryEntryID is the drawn entry.
	LotteryEntryID string
	// UserID is the platform-internal id of the entry's fan.
	UserID string
	// TicketSaleID is the drawn Lottery sale.
	TicketSaleID string
	// TicketStockID is the TicketStock the entry's Order was for.
	TicketStockID string
	// Result is the entry's result, "won" or "lost" ([entity.LotteryEntryState]
	// String).
	Result string
	// CaptureFailed is true for a loss that came from a failed capture of a
	// drawn winner's card authorization.
	CaptureFailed bool
	// DrawTime is when the draw ran, the time the fact happened.
	DrawTime time.Time
}

// checkoutExpired is what the analytics consumer reads from an
// CHECKOUT.expired announcement and the Checkout's Order: ids and
// counts only. It is forwarded as the catalogue event checkout.expired.
type checkoutExpired struct {
	// CheckoutID is the expired Checkout.
	CheckoutID string
	// UserID is the platform-internal id of the Checkout's fan.
	UserID string
	// TicketStockID is the TicketStock the Checkout held.
	TicketStockID string
	// TicketCount is the number of tickets the Checkout held.
	TicketCount int
	// HoldExpireTime is when the Checkout's hold lapsed, the time the fact
	// happened.
	HoldExpireTime time.Time
}
