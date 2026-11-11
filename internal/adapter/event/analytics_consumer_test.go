package event_test

import (
	"context"
	"errors"
	"testing"
	"time"

	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/liverty-music/backend/internal/adapter/event"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/messaging"
	"github.com/liverty-music/backend/internal/usecase"
	ucmocks "github.com/liverty-music/backend/internal/usecase/mocks"
)

const (
	fanID       = "11111111-2222-3333-4444-555555555555"
	organizerID = "019a0000-0000-7000-8000-0000000000o1"
)

// factTime is when the facts in these tests happened; factMillis is it
// truncated to milliseconds, as the consumer sends it.
var (
	factTime   = time.Date(2026, 11, 20, 18, 32, 0, 123456789, time.UTC)
	factMillis = factTime.Truncate(time.Millisecond)
)

// stubOrders returns every Order as one of the fan for stock-1 with 2
// tickets.
type stubOrders struct{}

func (stubOrders) Get(_ context.Context, id entity.OrderID) (*entity.Order, error) {
	return &entity.Order{ID: id, UserID: fanID, TicketStockID: "stock-1", TicketCount: 2}, nil
}

// announcedMsg builds an outbox announcement of payload under subject,
// recorded at recordTime.
func announcedMsg(t *testing.T, subject string, payload proto.Message, recordTime time.Time) *message.Message {
	t.Helper()
	raw, err := proto.Marshal(payload)
	require.NoError(t, err)
	msg := message.NewMessage("announcement-1", raw)
	msg.Metadata.Set("ce_id", "announcement-1")
	msg.Metadata.Set("ce_type", subject)
	msg.Metadata.Set("ce_time", recordTime.Format(time.RFC3339Nano))
	msg.Metadata.Set("ce_datacontenttype", messaging.ContentTypeProtobuf)
	return msg
}

// edited returns a copy of m changed by edit.
func edited[M proto.Message](m M, edit func(M)) M {
	c, _ := proto.Clone(m).(M)
	edit(c)
	return c
}

// consumerMetrics returns a metrics mock that accepts the lag sample and the
// given status once.
func consumerMetrics(t *testing.T, status string) *ucmocks.MockAnalyticsConsumerMetrics {
	t.Helper()
	m := ucmocks.NewMockAnalyticsConsumerMetrics(t)
	m.EXPECT().RecordMessage(mock.Anything, status).Once()
	m.EXPECT().RecordLag(mock.Anything, mock.Anything).Once()
	return m
}

type handler = func(c *event.AnalyticsConsumer, msg *message.Message) error

// forwardCase is one subject the consumer forwards: the announced entity
// recorded at recorded, and what reaches AnalyticsClient.Capture.
type forwardCase struct {
	// name is the subject, recorded as the announcement's ce_type.
	name      string
	handle    handler
	payload   proto.Message
	recorded  time.Time
	wantName  usecase.AnalyticsEventName
	wantID    string
	wantProps usecase.AnalyticsProperties
	// noKey is the entity without its distinct id; nil when the distinct id
	// is read by id from another entity.
	noKey proto.Message
	// noTime is the entity without its own time, announced without a
	// record time.
	noTime proto.Message
}

func forwardCases() []forwardCase {
	later := factTime.Add(time.Hour) // recorded after a fact that has its own time
	fan := &entityv1.UserId{Value: fanID}
	follow := &entityv1.Follow{
		UserId: fan, ArtistId: &entityv1.ArtistId{Value: "artist-1"}, Hype: entityv1.HypeType_HYPE_TYPE_AWAY,
		HypeSetBy: entityv1.HypeSetBy_HYPE_SET_BY_FAN,
	}
	noFan := func(f *entityv1.Follow) { f.UserId = nil }
	push := &entityv1.PushSubscription{UserId: fan, DeviceFamily: entityv1.PushDeviceFamily_PUSH_DEVICE_FAMILY_APPLE}
	login := &entityv1.User{Id: fan, LastLoginTime: timestamppb.New(factTime)}
	journey := &entityv1.TicketJourney{
		UserId: fan, EventId: &entityv1.EventId{Value: "event-1"}, Status: entityv1.TicketJourneyStatus_TICKET_JOURNEY_STATUS_PAID,
		StatusSetBy: entityv1.TicketJourneyStatusSetBy_TICKET_JOURNEY_STATUS_SET_BY_PLATFORM,
	}
	associated := &entityv1.Artist{Id: &entityv1.ArtistId{Value: "artist-1"}, OrganizerId: &entityv1.OrganizerId{Value: organizerID}}
	drawn := &entityv1.LotteryEntry{
		Id: &entityv1.LotteryEntryId{Value: "entry-1"}, OrderId: &entityv1.OrderId{Value: "order-1"},
		TicketSaleId: &entityv1.TicketSaleId{Value: "sale-1"}, State: entityv1.LotteryEntryState_LOTTERY_ENTRY_STATE_LOST,
		LostReason: entityv1.LotteryEntryLostReason_LOTTERY_ENTRY_LOST_REASON_CARD_NOT_CHARGED,
	}
	won := &entityv1.LotteryEntry{
		Id: &entityv1.LotteryEntryId{Value: "entry-1"}, OrderId: &entityv1.OrderId{Value: "order-1"},
		TicketSaleId: &entityv1.TicketSaleId{Value: "sale-1"},
	}
	expired := &entityv1.Checkout{
		Id: &entityv1.CheckoutId{Value: "checkout-1"}, OrderId: &entityv1.OrderId{Value: "order-1"}, HoldExpireTime: timestamppb.New(factTime),
	}
	admitted := &entityv1.Ticket{
		Id: &entityv1.TicketId{Value: "ticket-1"}, UserId: fan, EventId: &entityv1.EventId{Value: "event-1"}, AdmitTime: timestamppb.New(factTime),
	}

	return []forwardCase{
		{
			name: "USER.created", handle: (*event.AnalyticsConsumer).HandleUserCreated,
			payload: &entityv1.User{Id: fan}, recorded: factTime,
			wantName: usecase.EventUserCreated, wantID: fanID, wantProps: usecase.AnalyticsProperties{"signup_month": "2026-11"},
			noKey: &entityv1.User{}, noTime: &entityv1.User{Id: fan},
		},
		{
			name: "USER.signed_in", handle: (*event.AnalyticsConsumer).HandleUserSignedIn,
			payload: login, recorded: later,
			wantName: usecase.EventUserSignedIn, wantID: fanID, wantProps: usecase.AnalyticsProperties{},
			noKey:  edited(login, func(u *entityv1.User) { u.Id = nil }),
			noTime: edited(login, func(u *entityv1.User) { u.LastLoginTime = nil }),
		},
		{
			name: "FOLLOW.created", handle: (*event.AnalyticsConsumer).HandleFollowCreated,
			payload: follow, recorded: factTime,
			wantName: usecase.EventFollowCreated, wantID: fanID, wantProps: usecase.AnalyticsProperties{"artist_id": "artist-1"},
			noKey: edited(follow, noFan), noTime: follow,
		},
		{
			name: "FOLLOW.removed", handle: (*event.AnalyticsConsumer).HandleFollowRemoved,
			payload: follow, recorded: factTime,
			wantName: usecase.EventFollowRemoved, wantID: fanID, wantProps: usecase.AnalyticsProperties{"artist_id": "artist-1"},
			noKey: edited(follow, noFan), noTime: follow,
		},
		{
			name: "FOLLOW.hype_changed", handle: (*event.AnalyticsConsumer).HandleFollowHypeChanged,
			payload: follow, recorded: factTime,
			wantName: usecase.EventFollowHypeChanged, wantID: fanID, wantProps: usecase.AnalyticsProperties{"artist_id": "artist-1", "hype": "away", "set_by": "fan"},
			noKey: edited(follow, noFan), noTime: follow,
		},
		{
			name: "PUSH_SUBSCRIPTION.subscribed", handle: (*event.AnalyticsConsumer).HandlePushSubscriptionSubscribed,
			payload: push, recorded: factTime,
			wantName: usecase.EventPushSubscriptionSubscribed, wantID: fanID, wantProps: usecase.AnalyticsProperties{"device_type": "apple"},
			noKey: edited(push, func(p *entityv1.PushSubscription) { p.UserId = nil }), noTime: push,
		},
		{
			name: "PUSH_SUBSCRIPTION.unsubscribed", handle: (*event.AnalyticsConsumer).HandlePushSubscriptionUnsubscribed,
			payload: push, recorded: factTime,
			wantName: usecase.EventPushSubscriptionUnsubscribed, wantID: fanID, wantProps: usecase.AnalyticsProperties{"device_type": "apple"},
			noKey: edited(push, func(p *entityv1.PushSubscription) { p.UserId = nil }), noTime: push,
		},
		{
			// No from_status: it is the to_status of the fan's previous signal.
			name: "TICKET_JOURNEY.status_changed", handle: (*event.AnalyticsConsumer).HandleTicketJourneyStatusChanged,
			payload: journey, recorded: factTime,
			wantName: usecase.EventTicketJourneyStatusChanged, wantID: fanID,
			wantProps: usecase.AnalyticsProperties{"event_id": "event-1", "to_status": "PAID", "set_by": "platform"},
			noKey:     edited(journey, func(j *entityv1.TicketJourney) { j.UserId = nil }), noTime: journey,
		},
		{
			name: "ORGANIZER.activated", handle: (*event.AnalyticsConsumer).HandleOrganizerActivated,
			payload: &entityv1.Organizer{Id: &entityv1.OrganizerId{Value: organizerID}}, recorded: factTime,
			wantName: usecase.EventOrganizerActivated, wantID: organizerID, wantProps: usecase.AnalyticsProperties{"organizer_id": organizerID},
			noKey: &entityv1.Organizer{}, noTime: &entityv1.Organizer{Id: &entityv1.OrganizerId{Value: organizerID}},
		},
		{
			name: "ARTIST.represented", handle: (*event.AnalyticsConsumer).HandleArtistRepresented,
			payload: associated, recorded: factTime,
			wantName: usecase.EventArtistRepresented, wantID: organizerID,
			wantProps: usecase.AnalyticsProperties{"organizer_id": organizerID, "artist_id": "artist-1"},
			noKey:     edited(associated, func(a *entityv1.Artist) { a.OrganizerId = nil }), noTime: associated,
		},
		{
			name: "ORDER.paid", handle: (*event.AnalyticsConsumer).HandleOrderPaid,
			payload: paidCheckout(), recorded: later,
			wantName: usecase.EventOrderPaid, wantID: fanID, wantProps: paidCheckoutProps,
			noKey:  edited(paidCheckout(), func(o *entityv1.Order) { o.UserId = nil }),
			noTime: edited(paidCheckout(), func(o *entityv1.Order) { o.PayTime = nil }),
		},
		{
			// The fan and the TicketStock are the entry's Order's; the
			// subject tells the result.
			name: "LOTTERY_ENTRY.won", handle: (*event.AnalyticsConsumer).HandleLotteryEntryResult,
			payload: won, recorded: factTime,
			wantName: usecase.EventLotteryEntryWon, wantID: fanID, wantProps: usecase.AnalyticsProperties{
				"lottery_entry_id": "entry-1", "ticket_sale_id": "sale-1", "ticket_stock_id": "stock-1", "result": "won", "capture_failed": false,
			},
			noTime: won,
		},
		{
			name: "LOTTERY_ENTRY.lost", handle: (*event.AnalyticsConsumer).HandleLotteryEntryResult,
			payload: drawn, recorded: factTime,
			wantName: usecase.EventLotteryEntryLost, wantID: fanID, wantProps: usecase.AnalyticsProperties{
				"lottery_entry_id": "entry-1", "ticket_sale_id": "sale-1", "ticket_stock_id": "stock-1", "result": "lost", "capture_failed": true,
			},
			noTime: drawn,
		},
		{
			// The fan, the TicketStock and the count are the Checkout's
			// Order's.
			name: "CHECKOUT.expired", handle: (*event.AnalyticsConsumer).HandleCheckoutExpired,
			payload: expired, recorded: later,
			wantName: usecase.EventCheckoutExpired, wantID: fanID,
			wantProps: usecase.AnalyticsProperties{"checkout_id": "checkout-1", "ticket_stock_id": "stock-1", "ticket_count": 2},
			noTime:    edited(expired, func(c *entityv1.Checkout) { c.HoldExpireTime = nil }),
		},
		{
			name: "TICKET.admitted", handle: (*event.AnalyticsConsumer).HandleTicketAdmitted,
			payload: admitted, recorded: later,
			wantName: usecase.EventTicketAdmitted, wantID: fanID, wantProps: usecase.AnalyticsProperties{"ticket_id": "ticket-1", "event_id": "event-1"},
			noKey:  edited(admitted, func(tk *entityv1.Ticket) { tk.UserId = nil }),
			noTime: edited(admitted, func(tk *entityv1.Ticket) { tk.AdmitTime = nil }),
		},
	}
}

// TestAnalyticsConsumer_Forwards verifies, for every forwarded subject, that
// an outbox announcement is captured under its announcement id with the time
// of the fact: the entity's own time when it has one, else the time the
// announcement was recorded.
func TestAnalyticsConsumer_Forwards(t *testing.T) {
	t.Parallel()

	for _, tt := range forwardCases() {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			client := ucmocks.NewMockAnalyticsClient(t)
			// Delivered twice, the event keeps its UUID and time, which
			// PostHog stores once.
			client.EXPECT().Capture(mock.Anything, usecase.AnalyticsEvent{
				Name: tt.wantName, UUID: "announcement-1", Time: factMillis, DistinctID: tt.wantID, Properties: tt.wantProps,
			}).Return(nil).Twice()
			metrics := ucmocks.NewMockAnalyticsConsumerMetrics(t)
			metrics.EXPECT().RecordMessage(mock.Anything, "forwarded").Twice()
			metrics.EXPECT().RecordLag(mock.Anything, mock.Anything).Twice()
			c := event.NewAnalyticsConsumer(client, metrics, stubOrders{}, stubStocks{}, newTestLogger(t))

			require.NoError(t, tt.handle(c, announcedMsg(t, tt.name, tt.payload, tt.recorded)))
			require.NoError(t, tt.handle(c, announcedMsg(t, tt.name, tt.payload, tt.recorded)))
		})
	}
}

// TestAnalyticsConsumer_SetBy verifies that follow.hype_changed and
// ticket_journey.status_changed carry who set the value as set_by, and no
// set_by for an announcement recorded before it was stored.
func TestAnalyticsConsumer_SetBy(t *testing.T) {
	t.Parallel()

	fan := &entityv1.UserId{Value: fanID}
	hype := func(by entityv1.HypeSetBy) proto.Message {
		return &entityv1.Follow{UserId: fan, ArtistId: &entityv1.ArtistId{Value: "artist-1"}, Hype: entityv1.HypeType_HYPE_TYPE_AWAY, HypeSetBy: by}
	}
	journey := func(by entityv1.TicketJourneyStatusSetBy) proto.Message {
		return &entityv1.TicketJourney{
			UserId: fan, EventId: &entityv1.EventId{Value: "event-1"},
			Status: entityv1.TicketJourneyStatus_TICKET_JOURNEY_STATUS_TRACKING, StatusSetBy: by,
		}
	}
	tests := []struct {
		name    string
		handle  func(*event.AnalyticsConsumer, *message.Message) error
		payload proto.Message
		want    string
	}{
		{"hype set by default", (*event.AnalyticsConsumer).HandleFollowHypeChanged, hype(entityv1.HypeSetBy_HYPE_SET_BY_DEFAULT), "default"},
		{"hype set by the fan", (*event.AnalyticsConsumer).HandleFollowHypeChanged, hype(entityv1.HypeSetBy_HYPE_SET_BY_FAN), "fan"},
		{"hype set by inference", (*event.AnalyticsConsumer).HandleFollowHypeChanged, hype(entityv1.HypeSetBy_HYPE_SET_BY_INFERENCE), "inference"},
		{"hype without set by", (*event.AnalyticsConsumer).HandleFollowHypeChanged, hype(entityv1.HypeSetBy_HYPE_SET_BY_UNSPECIFIED), ""},
		{"journey set by the fan", (*event.AnalyticsConsumer).HandleTicketJourneyStatusChanged, journey(entityv1.TicketJourneyStatusSetBy_TICKET_JOURNEY_STATUS_SET_BY_FAN), "fan"},
		{"journey set by the platform", (*event.AnalyticsConsumer).HandleTicketJourneyStatusChanged, journey(entityv1.TicketJourneyStatusSetBy_TICKET_JOURNEY_STATUS_SET_BY_PLATFORM), "platform"},
		{"journey set by inference", (*event.AnalyticsConsumer).HandleTicketJourneyStatusChanged, journey(entityv1.TicketJourneyStatusSetBy_TICKET_JOURNEY_STATUS_SET_BY_INFERENCE), "inference"},
		{"journey without set by", (*event.AnalyticsConsumer).HandleTicketJourneyStatusChanged, journey(entityv1.TicketJourneyStatusSetBy_TICKET_JOURNEY_STATUS_SET_BY_UNSPECIFIED), ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			client := ucmocks.NewMockAnalyticsClient(t)
			client.EXPECT().Capture(mock.Anything, mock.MatchedBy(func(e usecase.AnalyticsEvent) bool {
				got, ok := e.Properties["set_by"]
				if tt.want == "" {
					return !ok
				}
				return got == tt.want
			})).Return(nil).Once()
			metrics := ucmocks.NewMockAnalyticsConsumerMetrics(t)
			metrics.EXPECT().RecordMessage(mock.Anything, "forwarded").Once()
			metrics.EXPECT().RecordLag(mock.Anything, mock.Anything).Once()
			c := event.NewAnalyticsConsumer(client, metrics, stubOrders{}, stubStocks{}, newTestLogger(t))

			require.NoError(t, tt.handle(c, announcedMsg(t, "", tt.payload, factTime)))
		})
	}
}

// TestAnalyticsConsumer_Skips covers the paths that acknowledge without
// forwarding, for every subject: no client, no distinct id, no fact time.
func TestAnalyticsConsumer_Skips(t *testing.T) {
	t.Parallel()

	for _, tt := range forwardCases() {
		t.Run(tt.name+": no client", func(t *testing.T) {
			t.Parallel()
			c := event.NewAnalyticsConsumer(nil, consumerMetrics(t, "skipped_nil_client"), stubOrders{}, stubStocks{}, newTestLogger(t))
			require.NoError(t, tt.handle(c, announcedMsg(t, tt.name, tt.payload, tt.recorded)))
		})
		if tt.noKey != nil {
			t.Run(tt.name+": no distinct id", func(t *testing.T) {
				t.Parallel()
				c := event.NewAnalyticsConsumer(ucmocks.NewMockAnalyticsClient(t), consumerMetrics(t, "skipped_empty_user_id"), stubOrders{}, stubStocks{}, newTestLogger(t))
				require.NoError(t, tt.handle(c, announcedMsg(t, tt.name, tt.noKey, tt.recorded)))
			})
		}
		t.Run(tt.name+": no fact time", func(t *testing.T) {
			t.Parallel()
			// Redelivering an announcement without the time of its fact would
			// never add it, so it is acknowledged unsent.
			c := event.NewAnalyticsConsumer(ucmocks.NewMockAnalyticsClient(t), consumerMetrics(t, "skipped_parse_error"), stubOrders{}, stubStocks{}, newTestLogger(t))
			require.NoError(t, tt.handle(c, announcedMsg(t, tt.name, tt.noTime, time.Time{})))
		})
	}
}

func TestAnalyticsConsumer_Failures(t *testing.T) {
	t.Parallel()

	t.Run("bad payload is an error", func(t *testing.T) {
		t.Parallel()
		c := event.NewAnalyticsConsumer(ucmocks.NewMockAnalyticsClient(t), consumerMetrics(t, "skipped_parse_error"), stubOrders{}, stubStocks{}, newTestLogger(t))
		msg := malformedMsg()
		msg.Metadata.Set("ce_time", time.Now().UTC().Format(time.RFC3339))
		assert.ErrorIs(t, c.HandleUserCreated(msg), apperr.ErrInternal)
	})

	t.Run("capture failure is an error", func(t *testing.T) {
		t.Parallel()
		client := ucmocks.NewMockAnalyticsClient(t)
		client.EXPECT().Capture(mock.Anything, mock.Anything).Return(errors.New("queue full")).Once()
		c := event.NewAnalyticsConsumer(client, consumerMetrics(t, "enqueue_error"), stubOrders{}, stubStocks{}, newTestLogger(t))
		follow := &entityv1.Follow{UserId: &entityv1.UserId{Value: fanID}, ArtistId: &entityv1.ArtistId{Value: "a"}, Hype: entityv1.HypeType_HYPE_TYPE_AWAY}
		err := c.HandleFollowHypeChanged(announcedMsg(t, entity.SubjectFollowHypeChanged, follow, factTime))
		assert.ErrorIs(t, err, apperr.ErrInternal)
	})
}

// paidCheckout is the ORDER.paid announcement of a fan's 2-ticket Checkout
// for a TicketStock of event-1, paid at factTime.
func paidCheckout() *entityv1.Order {
	return &entityv1.Order{
		Id:            &entityv1.OrderId{Value: "019a0000-0000-7000-8000-00000000a001"},
		UserId:        &entityv1.UserId{Value: fanID},
		TicketStockId: &entityv1.TicketStockId{Value: "stock-1"},
		TicketCount:   2,
		Amount:        16000,
		Currency:      "JPY",
		Source:        &entityv1.Order_CheckoutId{CheckoutId: &entityv1.CheckoutId{Value: "checkout-1"}},
		PayTime:       timestamppb.New(factTime),
	}
}

var paidCheckoutProps = usecase.AnalyticsProperties{
	"order_id":     "019a0000-0000-7000-8000-00000000a001",
	"event_id":     "event-1",
	"ticket_count": 2,
	"amount":       int64(16000),
	"currency":     "JPY",
	"source":       "checkout",
	"checkout_id":  "checkout-1",
}

// @spec components/usecase/order/fulfill-payment "Paid checkout"
func TestAnalyticsConsumer_HandleOrderPaid_PaidCheckout(t *testing.T) {
	t.Parallel()

	client := ucmocks.NewMockAnalyticsClient(t)
	client.EXPECT().Capture(mock.Anything, usecase.AnalyticsEvent{
		Name:       usecase.EventOrderPaid,
		UUID:       "announcement-1",
		Time:       factMillis,
		DistinctID: fanID,
		Properties: paidCheckoutProps,
	}).Return(nil).Once()

	c := event.NewAnalyticsConsumer(client, consumerMetrics(t, "forwarded"), stubOrders{}, stubStocks{}, newTestLogger(t))
	require.NoError(t, c.HandleOrderPaid(announcedMsg(t, entity.SubjectOrderPaid, paidCheckout(), factTime)))
}

// A paid Order is recorded with how it was bought: source names the
// LotteryEntry, Checkout or ResaleEntry, and only the matching id is sent.
func TestAnalyticsConsumer_HandleOrderPaid_Source(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		source     func(o *entityv1.Order)
		wantSource string
		wantIDKey  string
		wantID     string
	}{
		{
			name: "record a Checkout Order with source checkout and its checkout_id",
			source: func(o *entityv1.Order) {
				o.Source = &entityv1.Order_CheckoutId{CheckoutId: &entityv1.CheckoutId{Value: "checkout-1"}}
			},
			wantSource: "checkout", wantIDKey: "checkout_id", wantID: "checkout-1",
		},
		{
			name: "record a lottery Order with source lottery_entry and its lottery_entry_id",
			source: func(o *entityv1.Order) {
				o.Source = &entityv1.Order_LotteryEntryId{LotteryEntryId: &entityv1.LotteryEntryId{Value: "lottery-entry-1"}}
			},
			wantSource: "lottery_entry", wantIDKey: "lottery_entry_id", wantID: "lottery-entry-1",
		},
		{
			name: "record a resale Order with source resale_entry and its resale_entry_id",
			source: func(o *entityv1.Order) {
				o.Source = &entityv1.Order_ResaleEntryId{ResaleEntryId: &entityv1.ResaleEntryId{Value: "resale-entry-1"}}
			},
			wantSource: "resale_entry", wantIDKey: "resale_entry_id", wantID: "resale-entry-1",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Capture is matched on the whole property set, so an id of
			// another source, set or empty, fails the test.
			client := ucmocks.NewMockAnalyticsClient(t)
			client.EXPECT().Capture(mock.Anything, usecase.AnalyticsEvent{
				Name:       usecase.EventOrderPaid,
				UUID:       "announcement-1",
				Time:       factMillis,
				DistinctID: fanID,
				Properties: usecase.AnalyticsProperties{
					"order_id":     "019a0000-0000-7000-8000-00000000a001",
					"event_id":     "event-1",
					"ticket_count": 2,
					"amount":       int64(16000),
					"currency":     "JPY",
					"source":       tt.wantSource,
					tt.wantIDKey:   tt.wantID,
				},
			}).Return(nil).Once()

			c := event.NewAnalyticsConsumer(client, consumerMetrics(t, "forwarded"), stubOrders{}, stubStocks{}, newTestLogger(t))
			require.NoError(t, c.HandleOrderPaid(announcedMsg(t, entity.SubjectOrderPaid, edited(paidCheckout(), tt.source), factTime)))
		})
	}
}

// The same announcement delivered twice (a JetStream redelivery) is handed
// off with the same UUID (its announcement id) and the same time (the
// Order's pay time), whatever the time of the delivery. PostHog treats events
// with the same uuid, event name, timestamp and distinct_id as duplicates and
// keeps one (posthog.com/docs/data/events, "Event deduplication"), so one
// order-paid signal is recorded for the Order, at its pay time.
//
// @spec components/usecase/order/fulfill-payment "Announcement handled twice"
func TestAnalyticsConsumer_HandleOrderPaid_HandledTwice(t *testing.T) {
	t.Parallel()

	msg := announcedMsg(t, entity.SubjectOrderPaid, paidCheckout(), factTime)
	var handoffs []usecase.AnalyticsEvent
	client := ucmocks.NewMockAnalyticsClient(t)
	client.EXPECT().Capture(mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, e usecase.AnalyticsEvent) error {
			handoffs = append(handoffs, e)
			return nil
		}).Twice()

	metrics := ucmocks.NewMockAnalyticsConsumerMetrics(t)
	metrics.EXPECT().RecordMessage(mock.Anything, "forwarded").Twice()
	metrics.EXPECT().RecordLag(mock.Anything, mock.Anything).Twice()
	c := event.NewAnalyticsConsumer(client, metrics, stubOrders{}, stubStocks{}, newTestLogger(t))

	require.NoError(t, c.HandleOrderPaid(msg))
	redelivered := msg.Copy()
	redelivered.Metadata.Set("ce_time", time.Now().Add(10*time.Minute).UTC().Format(time.RFC3339))
	require.NoError(t, c.HandleOrderPaid(redelivered))

	require.Len(t, handoffs, 2)
	assert.Equal(t, handoffs[0], handoffs[1], "both hand-offs carry the same UUID, time, distinct id and properties")
	assert.Equal(t, msg.Metadata.Get("ce_id"), handoffs[0].UUID)
	assert.Equal(t, factMillis, handoffs[0].Time, "the Order's pay time, not the delivery time")
}
