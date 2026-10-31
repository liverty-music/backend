package event_test

import (
	"context"
	"testing"
	"time"

	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	"github.com/ThreeDotsLabs/watermill/message"
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

// stubOrders returns every Order as one of the fan for stock-1 with 2
// tickets.
type stubOrders struct{}

func (stubOrders) Get(_ context.Context, id entity.OrderID) (*entity.Order, error) {
	return &entity.Order{ID: id, UserID: fanID, TicketStockID: "stock-1", TicketCount: 2}, nil
}

// announcedMsg builds an outbox announcement of payload recorded at
// recordTime.
func announcedMsg(t *testing.T, payload proto.Message, recordTime time.Time) *message.Message {
	t.Helper()
	raw, err := proto.Marshal(payload)
	require.NoError(t, err)
	msg := message.NewMessage("announcement-1", raw)
	msg.Metadata.Set("ce_id", "announcement-1")
	msg.Metadata.Set("ce_time", recordTime.Format(time.RFC3339Nano))
	msg.Metadata.Set("ce_datacontenttype", messaging.ContentTypeProtobuf)
	return msg
}

// TestAnalyticsConsumer_ForwardsAnnouncements verifies, for every forwarded
// subject, that an outbox announcement is captured under its announcement id
// with the time of the fact: the entity's own time when it has one, else the
// time the announcement was recorded.
func TestAnalyticsConsumer_ForwardsAnnouncements(t *testing.T) {
	t.Parallel()
	later := factTime.Add(time.Hour) // recorded after a fact that has its own time
	fan := &entityv1.UserId{Value: fanID}
	follow := &entityv1.Follow{UserId: fan, ArtistId: &entityv1.ArtistId{Value: "artist-1"}, Hype: entityv1.HypeType_HYPE_TYPE_AWAY}
	push := &entityv1.PushSubscription{UserId: fan, DeviceFamily: entityv1.PushDeviceFamily_PUSH_DEVICE_FAMILY_APPLE}

	cases := []struct {
		name      string
		handle    handler
		payload   proto.Message
		recorded  time.Time
		wantName  usecase.AnalyticsEventName
		wantID    string
		wantProps usecase.AnalyticsProperties
	}{
		{"USER.created", (*event.AnalyticsConsumer).HandleUserCreated, &entityv1.User{Id: fan}, factTime,
			usecase.EventUserCreated, fanID, usecase.AnalyticsProperties{"signup_month": "2026-11"}},
		{"USER.logged_in", (*event.AnalyticsConsumer).HandleUserLoggedIn, &entityv1.User{Id: fan, LastLoginTime: timestamppb.New(factTime)}, later,
			usecase.EventAccountSignin, fanID, usecase.AnalyticsProperties{}},
		{"ARTIST.followed", (*event.AnalyticsConsumer).HandleArtistFollowed, follow, factTime,
			usecase.EventArtistFollowCompleted, fanID, usecase.AnalyticsProperties{"artist_id": "artist-1"}},
		{"ARTIST.unfollowed", (*event.AnalyticsConsumer).HandleArtistUnfollowed, follow, factTime,
			usecase.EventArtistUnfollowCompleted, fanID, usecase.AnalyticsProperties{"artist_id": "artist-1"}},
		{"ARTIST.hype_changed", (*event.AnalyticsConsumer).HandleArtistHypeChanged, follow, factTime,
			usecase.EventArtistHypeChanged, fanID, usecase.AnalyticsProperties{"artist_id": "artist-1", "hype": "away"}},
		{"NOTIFICATION.subscribed", (*event.AnalyticsConsumer).HandleNotificationSubscribed, push, factTime,
			usecase.EventNotificationSubscribed, fanID, usecase.AnalyticsProperties{"device_type": "apple"}},
		{"NOTIFICATION.unsubscribed", (*event.AnalyticsConsumer).HandleNotificationUnsubscribed, push, factTime,
			usecase.EventNotificationUnsubscribed, fanID, usecase.AnalyticsProperties{"device_type": "apple"}},
		{"NOTIFICATION.delivered", (*event.AnalyticsConsumer).HandleNotificationDelivered, &entityv1.Notification{
			Id: &entityv1.NotificationId{Value: "notification-1"}, UserId: fan,
			Type: entityv1.NotificationType_NOTIFICATION_TYPE_NEW_CONCERTS, DeliverTime: timestamppb.New(factTime),
		}, later, usecase.EventNotificationDelivered, fanID, usecase.AnalyticsProperties{"notification_id": "notification-1", "type": "new_concerts"}},
		// No from_status: it is the to_status of the fan's previous signal.
		{"TICKET_JOURNEY.status_changed", (*event.AnalyticsConsumer).HandleTicketJourneyStatusChanged, &entityv1.TicketJourney{
			UserId: fan, EventId: &entityv1.EventId{Value: "event-1"}, Status: entityv1.TicketJourneyStatus_TICKET_JOURNEY_STATUS_PAID,
		}, factTime, usecase.EventTicketJourneyStatusChanged, fanID, usecase.AnalyticsProperties{"event_id": "event-1", "to_status": "PAID"}},
		{"ORGANIZER.created", (*event.AnalyticsConsumer).HandleOrganizerCreated, &entityv1.Organizer{Id: &entityv1.OrganizerId{Value: organizerID}}, factTime,
			usecase.EventOrganizerCreated, organizerID, usecase.AnalyticsProperties{"organizer_id": organizerID}},
		{"ORGANIZER.artist_associated", (*event.AnalyticsConsumer).HandleOrganizerArtistAssociated, &entityv1.Artist{
			Id: &entityv1.ArtistId{Value: "artist-1"}, OrganizerId: &entityv1.OrganizerId{Value: organizerID},
		}, factTime, usecase.EventOrganizerArtistAssociated, organizerID, usecase.AnalyticsProperties{"organizer_id": organizerID, "artist_id": "artist-1"}},
		{"ORDER.paid", (*event.AnalyticsConsumer).HandleOrderPaid, &entityv1.Order{
			Id: &entityv1.OrderId{Value: "order-1"}, UserId: fan, TicketStockId: &entityv1.TicketStockId{Value: "stock-1"},
			TicketCount: 2, Amount: 6000, Currency: "JPY", Source: &entityv1.Order_CheckoutId{CheckoutId: &entityv1.CheckoutId{Value: "checkout-1"}}, PayTime: timestamppb.New(factTime),
		}, later, usecase.EventOrderPaid, fanID, usecase.AnalyticsProperties{
			"order_id": "order-1", "event_id": "event-1", "ticket_count": 2, "amount": int64(6000), "currency": "JPY",
			"source": "checkout", "checkout_id": "checkout-1",
		}},
		{"ORDER.lottery_entry_drawn", (*event.AnalyticsConsumer).HandleLotteryEntryDrawn, &entityv1.LotteryEntry{
			Id: &entityv1.LotteryEntryId{Value: "entry-1"}, OrderId: &entityv1.OrderId{Value: "order-1"},
			TicketSaleId: &entityv1.TicketSaleId{Value: "sale-1"}, State: entityv1.LotteryEntryState_LOTTERY_ENTRY_STATE_LOST,
			LostReason: entityv1.LotteryEntryLostReason_LOTTERY_ENTRY_LOST_REASON_CARD_NOT_CHARGED,
		}, factTime, usecase.EventTicketLotteryResult, fanID, usecase.AnalyticsProperties{
			"lottery_entry_id": "entry-1", "ticket_sale_id": "sale-1", "ticket_stock_id": "stock-1", "result": "lost", "capture_failed": true,
		}},
		{"ORDER.checkout_expired", (*event.AnalyticsConsumer).HandleCheckoutExpired, &entityv1.Checkout{
			Id: &entityv1.CheckoutId{Value: "checkout-1"}, OrderId: &entityv1.OrderId{Value: "order-1"}, HoldExpireTime: timestamppb.New(factTime),
		}, later, usecase.EventCheckoutExpired, fanID, usecase.AnalyticsProperties{"checkout_id": "checkout-1", "ticket_stock_id": "stock-1", "ticket_count": 2}},
		{"ORDER.ticket_admitted", (*event.AnalyticsConsumer).HandleTicketAdmitted, &entityv1.Ticket{
			Id: &entityv1.TicketId{Value: "ticket-1"}, UserId: fan, EventId: &entityv1.EventId{Value: "event-1"}, AdmitTime: timestamppb.New(factTime),
		}, later, usecase.EventTicketAdmitted, fanID, usecase.AnalyticsProperties{"ticket_id": "ticket-1", "event_id": "event-1"}},
	}

	for _, tt := range cases {
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

			require.NoError(t, tt.handle(c, announcedMsg(t, tt.payload, tt.recorded)))
			require.NoError(t, tt.handle(c, announcedMsg(t, tt.payload, tt.recorded)))
		})
	}
}
