package event_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

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

// cloudEvent builds the message NewEvent publishes for data: a fresh CloudEvent
// id, set as both ce_id and the message UUID, and ce_time = now.
func cloudEvent(t *testing.T, data any) *message.Message {
	t.Helper()
	msg, err := messaging.NewEvent(t.Context(), data)
	require.NoError(t, err)
	return msg
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

// forwardCase is one subject the consumer forwards: its event data with the
// fact time set, and what reaches AnalyticsClient.Capture.
type forwardCase struct {
	name       string
	data       any
	handle     handler
	wantName   usecase.AnalyticsEventName
	wantID     string
	wantProps  usecase.AnalyticsProperties
	noTimeData any // the same data without its fact time
	noKeyData  any // the same data without its distinct id
}

func forwardCases() []forwardCase {
	return []forwardCase{
		{
			name:       "user created",
			data:       entity.UserCreatedData{UserID: fanID, ProviderUserID: "sub", Email: "a@example.com", CreateTime: factTime},
			handle:     (*event.AnalyticsConsumer).HandleUserCreated,
			wantName:   usecase.EventUserCreated,
			wantID:     fanID,
			wantProps:  usecase.AnalyticsProperties{"signup_month": "2026-11"},
			noTimeData: entity.UserCreatedData{UserID: fanID},
			noKeyData:  entity.UserCreatedData{CreateTime: factTime},
		},
		{
			name:       "user logged in",
			data:       entity.UserLoggedInData{UserID: fanID, LoginTime: factTime},
			handle:     (*event.AnalyticsConsumer).HandleUserLoggedIn,
			wantName:   usecase.EventAccountSignin,
			wantID:     fanID,
			wantProps:  usecase.AnalyticsProperties{},
			noTimeData: entity.UserLoggedInData{UserID: fanID},
			noKeyData:  entity.UserLoggedInData{LoginTime: factTime},
		},
		{
			name:       "artist followed",
			data:       entity.ArtistFollowedData{UserID: fanID, ArtistID: "artist-1", FollowTime: factTime},
			handle:     (*event.AnalyticsConsumer).HandleArtistFollowed,
			wantName:   usecase.EventArtistFollowCompleted,
			wantID:     fanID,
			wantProps:  usecase.AnalyticsProperties{"artist_id": "artist-1"},
			noTimeData: entity.ArtistFollowedData{UserID: fanID, ArtistID: "artist-1"},
			noKeyData:  entity.ArtistFollowedData{ArtistID: "artist-1", FollowTime: factTime},
		},
		{
			name:       "artist unfollowed",
			data:       entity.ArtistUnfollowedData{UserID: fanID, ArtistID: "artist-1", UnfollowTime: factTime},
			handle:     (*event.AnalyticsConsumer).HandleArtistUnfollowed,
			wantName:   usecase.EventArtistUnfollowCompleted,
			wantID:     fanID,
			wantProps:  usecase.AnalyticsProperties{"artist_id": "artist-1"},
			noTimeData: entity.ArtistUnfollowedData{UserID: fanID, ArtistID: "artist-1"},
			noKeyData:  entity.ArtistUnfollowedData{ArtistID: "artist-1", UnfollowTime: factTime},
		},
		{
			name:       "notification subscribed",
			data:       entity.NotificationSubscribedData{UserID: fanID, DeviceType: "android", SubscribeTime: factTime},
			handle:     (*event.AnalyticsConsumer).HandleNotificationSubscribed,
			wantName:   usecase.EventNotificationSubscribed,
			wantID:     fanID,
			wantProps:  usecase.AnalyticsProperties{"device_type": "android"},
			noTimeData: entity.NotificationSubscribedData{UserID: fanID, DeviceType: "android"},
			noKeyData:  entity.NotificationSubscribedData{DeviceType: "android", SubscribeTime: factTime},
		},
		{
			name:       "notification unsubscribed",
			data:       entity.NotificationUnsubscribedData{UserID: fanID, DeviceType: "apple", UnsubscribeTime: factTime},
			handle:     (*event.AnalyticsConsumer).HandleNotificationUnsubscribed,
			wantName:   usecase.EventNotificationUnsubscribed,
			wantID:     fanID,
			wantProps:  usecase.AnalyticsProperties{"device_type": "apple"},
			noTimeData: entity.NotificationUnsubscribedData{UserID: fanID, DeviceType: "apple"},
			noKeyData:  entity.NotificationUnsubscribedData{DeviceType: "apple", UnsubscribeTime: factTime},
		},
		{
			name:       "notification delivered",
			data:       entity.NotificationDeliveredData{UserID: fanID, NotificationID: "n-1", Type: "new_concerts", DeliverTime: factTime},
			handle:     (*event.AnalyticsConsumer).HandleNotificationDelivered,
			wantName:   usecase.EventNotificationDelivered,
			wantID:     fanID,
			wantProps:  usecase.AnalyticsProperties{"notification_id": "n-1", "type": "new_concerts"},
			noTimeData: entity.NotificationDeliveredData{UserID: fanID, NotificationID: "n-1"},
			// A message without notification_id is skipped like one without user_id.
			noKeyData: entity.NotificationDeliveredData{UserID: fanID, DeliverTime: factTime},
		},
		{
			name: "ticket journey status changed",
			data: entity.TicketJourneyStatusChangedData{
				UserID: fanID, EventID: "event-1", FromStatus: "TRACKING", ToStatus: "APPLIED", ChangeTime: factTime,
			},
			handle:     (*event.AnalyticsConsumer).HandleTicketJourneyStatusChanged,
			wantName:   usecase.EventTicketJourneyStatusChanged,
			wantID:     fanID,
			wantProps:  usecase.AnalyticsProperties{"event_id": "event-1", "from_status": "TRACKING", "to_status": "APPLIED"},
			noTimeData: entity.TicketJourneyStatusChangedData{UserID: fanID, EventID: "event-1"},
			noKeyData:  entity.TicketJourneyStatusChangedData{EventID: "event-1", ChangeTime: factTime},
		},
		{
			name:       "organizer created",
			data:       entity.OrganizerCreatedData{OrganizerID: organizerID, ActivateTime: factTime},
			handle:     (*event.AnalyticsConsumer).HandleOrganizerCreated,
			wantName:   usecase.EventOrganizerCreated,
			wantID:     organizerID,
			wantProps:  usecase.AnalyticsProperties{"organizer_id": organizerID},
			noTimeData: entity.OrganizerCreatedData{OrganizerID: organizerID},
			noKeyData:  entity.OrganizerCreatedData{ActivateTime: factTime},
		},
		{
			name:       "organizer artist associated",
			data:       entity.OrganizerArtistAssociatedData{OrganizerID: organizerID, ArtistID: "artist-1", AssociateTime: factTime},
			handle:     (*event.AnalyticsConsumer).HandleOrganizerArtistAssociated,
			wantName:   usecase.EventOrganizerArtistAssociated,
			wantID:     organizerID,
			wantProps:  usecase.AnalyticsProperties{"organizer_id": organizerID, "artist_id": "artist-1"},
			noTimeData: entity.OrganizerArtistAssociatedData{OrganizerID: organizerID, ArtistID: "artist-1"},
			noKeyData:  entity.OrganizerArtistAssociatedData{ArtistID: "artist-1", AssociateTime: factTime},
		},
		{
			name:       "hype set",
			data:       entity.ArtistHypeChangedData{UserID: fanID, ArtistID: "artist-1", Hype: "away", SetTime: factTime},
			handle:     (*event.AnalyticsConsumer).HandleArtistHypeChanged,
			wantName:   usecase.EventArtistHypeChanged,
			wantID:     fanID,
			wantProps:  usecase.AnalyticsProperties{"artist_id": "artist-1", "hype": "away"},
			noTimeData: entity.ArtistHypeChangedData{UserID: fanID, ArtistID: "artist-1", Hype: "away"},
			noKeyData:  entity.ArtistHypeChangedData{ArtistID: "artist-1", Hype: "away", SetTime: factTime},
		},
		{
			name: "lottery result",
			data: entity.LotteryEntryDrawnData{
				LotteryEntryID: "entry-1", UserID: fanID, TicketSaleID: "sale-1", TicketStockID: "stock-1",
				Result: "lost", CaptureFailed: true, DrawTime: factTime,
			},
			handle:   (*event.AnalyticsConsumer).HandleLotteryEntryDrawn,
			wantName: usecase.EventTicketLotteryResult,
			wantID:   fanID,
			wantProps: usecase.AnalyticsProperties{
				"lottery_entry_id": "entry-1", "ticket_sale_id": "sale-1", "ticket_stock_id": "stock-1",
				"result": "lost", "capture_failed": true,
			},
			noTimeData: entity.LotteryEntryDrawnData{LotteryEntryID: "entry-1", UserID: fanID},
			noKeyData:  entity.LotteryEntryDrawnData{LotteryEntryID: "entry-1", DrawTime: factTime},
		},
		{
			name:       "checkout expired",
			data:       entity.CheckoutExpiredData{CheckoutID: "checkout-1", UserID: fanID, TicketStockID: "stock-1", TicketCount: 2, HoldExpireTime: factTime},
			handle:     (*event.AnalyticsConsumer).HandleCheckoutExpired,
			wantName:   usecase.EventCheckoutExpired,
			wantID:     fanID,
			wantProps:  usecase.AnalyticsProperties{"checkout_id": "checkout-1", "ticket_stock_id": "stock-1", "ticket_count": 2},
			noTimeData: entity.CheckoutExpiredData{CheckoutID: "checkout-1", UserID: fanID},
			noKeyData:  entity.CheckoutExpiredData{CheckoutID: "checkout-1", HoldExpireTime: factTime},
		},
		{
			name:       "ticket admitted",
			data:       entity.TicketAdmittedData{TicketID: "ticket-1", UserID: fanID, EventID: "event-1", AdmitTime: factTime},
			handle:     (*event.AnalyticsConsumer).HandleTicketAdmitted,
			wantName:   usecase.EventTicketAdmitted,
			wantID:     fanID,
			wantProps:  usecase.AnalyticsProperties{"ticket_id": "ticket-1", "event_id": "event-1"},
			noTimeData: entity.TicketAdmittedData{TicketID: "ticket-1", UserID: fanID},
			noKeyData:  entity.TicketAdmittedData{TicketID: "ticket-1", AdmitTime: factTime},
		},
		{
			name: "order paid",
			data: paidCheckout,
			handle: func(c *event.AnalyticsConsumer, m *message.Message) error {
				return c.HandleOrderPaid(m)
			},
			wantName:   usecase.EventOrderPaid,
			wantID:     fanID,
			wantProps:  paidCheckoutProps,
			noTimeData: entity.OrderPaidData{OrderID: paidCheckout.OrderID, UserID: fanID},
			noKeyData:  entity.OrderPaidData{OrderID: paidCheckout.OrderID, PayTime: factTime},
		},
	}
}

// TestAnalyticsConsumer_Forwards verifies, for every forwarded subject, that
// the event is captured with the message's CloudEvent id as its UUID, the
// fact time from the event data (truncated to milliseconds, not the publish
// time) as its time, the distinct id and the catalogue properties.
func TestAnalyticsConsumer_Forwards(t *testing.T) {
	t.Parallel()

	for _, tt := range forwardCases() {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			msg := cloudEvent(t, tt.data)
			client := ucmocks.NewMockAnalyticsClient(t)
			client.EXPECT().Capture(mock.Anything, usecase.AnalyticsEvent{
				Name:       tt.wantName,
				UUID:       msg.Metadata.Get("ce_id"),
				Time:       factMillis,
				DistinctID: tt.wantID,
				Properties: tt.wantProps,
			}).Return(nil).Once()

			c := event.NewAnalyticsConsumer(client, consumerMetrics(t, "forwarded"), newTestLogger(t))
			require.NoError(t, tt.handle(c, msg))
			assert.Equal(t, msg.UUID, msg.Metadata.Get("ce_id"), "the CloudEvent id is the message UUID")
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
			c := event.NewAnalyticsConsumer(nil, consumerMetrics(t, "skipped_nil_client"), newTestLogger(t))
			require.NoError(t, tt.handle(c, cloudEvent(t, tt.data)))
		})
		t.Run(tt.name+": no distinct id", func(t *testing.T) {
			t.Parallel()
			c := event.NewAnalyticsConsumer(ucmocks.NewMockAnalyticsClient(t), consumerMetrics(t, "skipped_empty_user_id"), newTestLogger(t))
			require.NoError(t, tt.handle(c, cloudEvent(t, tt.noKeyData)))
		})
		t.Run(tt.name+": no fact time", func(t *testing.T) {
			t.Parallel()
			// A message published before its data carried the time: its
			// publish time is not the fact's, so it is acknowledged unsent.
			c := event.NewAnalyticsConsumer(ucmocks.NewMockAnalyticsClient(t), consumerMetrics(t, "skipped_parse_error"), newTestLogger(t))
			require.NoError(t, tt.handle(c, cloudEvent(t, tt.noTimeData)))
		})
	}
}

func TestAnalyticsConsumer_Failures(t *testing.T) {
	t.Parallel()

	t.Run("bad payload is an error", func(t *testing.T) {
		t.Parallel()
		c := event.NewAnalyticsConsumer(ucmocks.NewMockAnalyticsClient(t), consumerMetrics(t, "skipped_parse_error"), newTestLogger(t))
		msg := message.NewMessage("bad", []byte("{not json"))
		msg.Metadata.Set("ce_time", time.Now().UTC().Format(time.RFC3339))
		assert.ErrorIs(t, c.HandleUserCreated(msg), apperr.ErrInternal)
	})

	t.Run("capture failure is an error", func(t *testing.T) {
		t.Parallel()
		client := ucmocks.NewMockAnalyticsClient(t)
		client.EXPECT().Capture(mock.Anything, mock.Anything).Return(errors.New("queue full")).Once()
		c := event.NewAnalyticsConsumer(client, consumerMetrics(t, "enqueue_error"), newTestLogger(t))
		err := c.HandleArtistHypeChanged(cloudEvent(t, entity.ArtistHypeChangedData{UserID: fanID, ArtistID: "a", Hype: "away", SetTime: factTime}))
		assert.ErrorIs(t, err, apperr.ErrInternal)
	})
}

// paidCheckout is the ORDER.paid announcement of a fan's 2-ticket Checkout,
// paid at factTime.
var paidCheckout = entity.OrderPaidData{
	OrderID:     "019a0000-0000-7000-8000-00000000a001",
	UserID:      fanID,
	EventID:     "event-1",
	TicketCount: 2,
	Amount:      16000,
	Currency:    "JPY",
	CheckoutID:  "checkout-1",
	PayTime:     factTime,
}

var paidCheckoutProps = usecase.AnalyticsProperties{
	"order_id":     paidCheckout.OrderID,
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

	msg := cloudEvent(t, paidCheckout)
	client := ucmocks.NewMockAnalyticsClient(t)
	client.EXPECT().Capture(mock.Anything, usecase.AnalyticsEvent{
		Name:       usecase.EventOrderPaid,
		UUID:       msg.Metadata.Get("ce_id"),
		Time:       factMillis,
		DistinctID: fanID,
		Properties: paidCheckoutProps,
	}).Return(nil).Once()

	c := event.NewAnalyticsConsumer(client, consumerMetrics(t, "forwarded"), newTestLogger(t))
	require.NoError(t, c.HandleOrderPaid(msg))
}

// The same message delivered twice (a JetStream redelivery) is handed off
// with the same UUID (its CloudEvent id) and the same time (the Order's pay
// time), whatever the time of the delivery. PostHog treats events with the
// same uuid, event name, timestamp and distinct_id as duplicates and keeps one
// (posthog.com/docs/data/events, "Event deduplication"), so one order-paid
// signal is recorded for the Order, at its pay time.
//
// @spec components/usecase/order/fulfill-payment "Announcement handled twice"
func TestAnalyticsConsumer_HandleOrderPaid_HandledTwice(t *testing.T) {
	t.Parallel()

	msg := cloudEvent(t, paidCheckout)
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
	c := event.NewAnalyticsConsumer(client, metrics, newTestLogger(t))

	require.NoError(t, c.HandleOrderPaid(msg))
	redelivered := msg.Copy()
	redelivered.Metadata.Set("ce_time", time.Now().Add(10*time.Minute).UTC().Format(time.RFC3339))
	require.NoError(t, c.HandleOrderPaid(redelivered))

	require.Len(t, handoffs, 2)
	assert.Equal(t, handoffs[0], handoffs[1], "both hand-offs carry the same UUID, time, distinct id and properties")
	assert.Equal(t, msg.Metadata.Get("ce_id"), handoffs[0].UUID)
	assert.Equal(t, factMillis, handoffs[0].Time, "the Order's pay time, not the delivery time")
}

// TestAnalyticsConsumer_UUIDFromCloudEventID verifies the event UUID is read
// from the message's ce_id and the time from the data, not from ce_time.
func TestAnalyticsConsumer_UUIDFromCloudEventID(t *testing.T) {
	t.Parallel()

	payload, err := json.Marshal(entity.TicketAdmittedData{TicketID: "ticket-1", UserID: fanID, EventID: "event-1", AdmitTime: factTime})
	require.NoError(t, err)
	msg := message.NewMessage("019a0000-0000-7000-8000-00000000ce01", payload)
	msg.Metadata.Set("ce_id", "019a0000-0000-7000-8000-00000000ce01")
	msg.Metadata.Set("ce_time", "2030-01-01T00:00:00Z")

	client := ucmocks.NewMockAnalyticsClient(t)
	client.EXPECT().Capture(mock.Anything, mock.MatchedBy(func(e usecase.AnalyticsEvent) bool {
		return e.UUID == "019a0000-0000-7000-8000-00000000ce01" && e.Time.Equal(factMillis)
	})).Return(nil).Once()

	c := event.NewAnalyticsConsumer(client, consumerMetrics(t, "forwarded"), newTestLogger(t))
	require.NoError(t, c.HandleTicketAdmitted(msg))
}
