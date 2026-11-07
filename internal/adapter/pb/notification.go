package pb

import (
	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/liverty-music/backend/internal/entity"
)

var notificationTypeToProto = map[entity.NotificationType]entityv1.NotificationType{
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
}

// FromNotification maps a Notification with its rendered message to Protobuf:
// its id, fan, type, message and create time, and its read time when read.
func FromNotification(n *entity.Notification, message *entity.NotificationPayload) *entityv1.Notification {
	if n == nil {
		return nil
	}
	out := &entityv1.Notification{
		Id:         &entityv1.NotificationId{Value: n.ID},
		UserId:     &entityv1.UserId{Value: n.UserID},
		Type:       notificationTypeToProto[n.Type],
		CreateTime: timestamppb.New(n.CreateTime),
	}
	if message != nil {
		out.Message = &entityv1.NotificationMessage{
			Title: message.Title,
			Body:  message.Body,
			Tag:   message.Tag,
			Data:  message.Data,
		}
	}
	if n.ReadTime != nil {
		out.ReadTime = timestamppb.New(*n.ReadTime)
	}
	return out
}
