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
	entity.NotificationTypeOrderConfirmation:      entityv1.NotificationType_NOTIFICATION_TYPE_ORDER_CONFIRMATION,
}

var notificationDeliveryStatusToProto = map[entity.NotificationDeliveryStatus]entityv1.NotificationDeliveryStatus{
	entity.NotificationDeliveryStatusQueued:    entityv1.NotificationDeliveryStatus_NOTIFICATION_DELIVERY_STATUS_QUEUED,
	entity.NotificationDeliveryStatusDelivered: entityv1.NotificationDeliveryStatus_NOTIFICATION_DELIVERY_STATUS_DELIVERED,
	entity.NotificationDeliveryStatusFailed:    entityv1.NotificationDeliveryStatus_NOTIFICATION_DELIVERY_STATUS_FAILED,
}

// FromNotification maps a Notification to Protobuf: its id, fan, type,
// pushed message, delivery status and failure reason, and its queue,
// deliver, read and dismiss times when set.
func FromNotification(n *entity.Notification) *entityv1.Notification {
	if n == nil {
		return nil
	}
	out := &entityv1.Notification{
		Id:             &entityv1.NotificationId{Value: n.ID},
		UserId:         &entityv1.UserId{Value: n.UserID},
		Type:           notificationTypeToProto[n.Type],
		DeliveryStatus: notificationDeliveryStatusToProto[n.DeliveryStatus],
		QueueTime:      timestamppb.New(n.QueueTime),
	}
	if n.Payload != nil {
		out.Message = &entityv1.NotificationMessage{
			Title: n.Payload.Title,
			Body:  n.Payload.Body,
			Tag:   n.Payload.Tag,
			Data:  n.Payload.Data,
		}
	}
	if n.FailureReason != "" {
		reason := n.FailureReason
		out.FailureReason = &reason
	}
	if n.DeliverTime != nil {
		out.DeliverTime = timestamppb.New(*n.DeliverTime)
	}
	if n.ReadTime != nil {
		out.ReadTime = timestamppb.New(*n.ReadTime)
	}
	if n.DismissTime != nil {
		out.DismissTime = timestamppb.New(*n.DismissTime)
	}
	return out
}
