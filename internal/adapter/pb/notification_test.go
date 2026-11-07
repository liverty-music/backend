package pb_test

import (
	"testing"
	"time"

	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	"github.com/stretchr/testify/assert"

	"github.com/liverty-music/backend/internal/adapter/pb"
	"github.com/liverty-music/backend/internal/entity"
)

// TestFromNotification_ResaleEntryTypes checks the types of the resale entry
// lapse and loss pushes map to their own proto values, never UNSPECIFIED,
// which the required, defined-only type field rejects.
func TestFromNotification_ResaleEntryTypes(t *testing.T) {
	t.Parallel()
	for typ, want := range map[entity.NotificationType]entityv1.NotificationType{
		entity.NotificationTypeResaleEntryLapsed: entityv1.NotificationType_NOTIFICATION_TYPE_RESALE_ENTRY_LAPSED,
		entity.NotificationTypeResaleEntryLost:   entityv1.NotificationType_NOTIFICATION_TYPE_RESALE_ENTRY_LOST,
	} {
		got := pb.FromNotification(&entity.Notification{ID: "n-1", UserID: "u-1", Type: typ}, nil)
		assert.Equal(t, want, got.GetType(), string(typ))
	}
}

// TestFromNotification_CancellationAndRefundTypes checks the types of the
// cancellation and refund pushes map to their own proto values and map back.
func TestFromNotification_CancellationAndRefundTypes(t *testing.T) {
	t.Parallel()
	for typ, want := range map[entity.NotificationType]entityv1.NotificationType{
		entity.NotificationTypeConcertCancelled: entityv1.NotificationType_NOTIFICATION_TYPE_CONCERT_CANCELLED,
		entity.NotificationTypeOrderRefunded:    entityv1.NotificationType_NOTIFICATION_TYPE_ORDER_REFUNDED,
	} {
		got := pb.FromNotification(&entity.Notification{ID: "n-1", UserID: "u-1", Type: typ}, nil)
		assert.Equal(t, want, got.GetType(), string(typ))
		assert.Equal(t, typ, pb.NotificationTypeFromProto(got.GetType()), string(typ))
	}
}

// TestFromNotification_EveryType checks every type maps to a proto value of its
// own, never UNSPECIFIED, which the required, defined-only type field rejects.
func TestFromNotification_EveryType(t *testing.T) {
	t.Parallel()
	for typ, want := range map[entity.NotificationType]entityv1.NotificationType{
		entity.NotificationTypeNewConcerts:            entityv1.NotificationType_NOTIFICATION_TYPE_NEW_CONCERTS,
		entity.NotificationTypeSalesReminder:          entityv1.NotificationType_NOTIFICATION_TYPE_SALES_REMINDER,
		entity.NotificationTypeTicketSaleAnnouncement: entityv1.NotificationType_NOTIFICATION_TYPE_TICKET_SALE_ANNOUNCEMENT,
		entity.NotificationTypeLotteryWin:             entityv1.NotificationType_NOTIFICATION_TYPE_LOTTERY_WIN,
		entity.NotificationTypeResaleWin:              entityv1.NotificationType_NOTIFICATION_TYPE_RESALE_WIN,
		entity.NotificationTypeLotteryLoss:            entityv1.NotificationType_NOTIFICATION_TYPE_LOTTERY_LOSS,
		entity.NotificationTypeResaleEntryLapsed:      entityv1.NotificationType_NOTIFICATION_TYPE_RESALE_ENTRY_LAPSED,
		entity.NotificationTypeResaleEntryLost:        entityv1.NotificationType_NOTIFICATION_TYPE_RESALE_ENTRY_LOST,
		entity.NotificationTypeConcertCancelled:       entityv1.NotificationType_NOTIFICATION_TYPE_CONCERT_CANCELLED,
		entity.NotificationTypeOrderRefunded:          entityv1.NotificationType_NOTIFICATION_TYPE_ORDER_REFUNDED,
	} {
		got := pb.FromNotification(&entity.Notification{ID: "n-1", UserID: "u-1", Type: typ}, nil)
		assert.Equal(t, want, got.GetType(), string(typ))
	}
}

// TestFromNotification_MessageAndTimes checks the rendered message, the create
// time and the read time reach the fan, and an unread one has no read time.
func TestFromNotification_MessageAndTimes(t *testing.T) {
	t.Parallel()
	created := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	read := created.Add(time.Hour)
	message := entity.NewNotificationPayload("[A] title", "body", "/tickets", "tag-1")

	unread := pb.FromNotification(&entity.Notification{ID: "n-1", UserID: "u-1", Type: entity.NotificationTypeLotteryWin, CreateTime: created}, message)
	assert.Equal(t, "[A] title", unread.GetMessage().GetTitle())
	assert.Equal(t, "body", unread.GetMessage().GetBody())
	assert.Equal(t, "tag-1", unread.GetMessage().GetTag())
	assert.Equal(t, "/tickets", unread.GetMessage().GetData()[entity.NotificationDataKeyURL])
	assert.True(t, created.Equal(unread.GetCreateTime().AsTime()))
	assert.Nil(t, unread.GetReadTime())

	marked := pb.FromNotification(&entity.Notification{ID: "n-1", UserID: "u-1", Type: entity.NotificationTypeLotteryWin, CreateTime: created, ReadTime: &read}, message)
	assert.True(t, read.Equal(marked.GetReadTime().AsTime()))
}
