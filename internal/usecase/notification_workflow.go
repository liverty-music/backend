package usecase

import (
	"context"
	"strings"

	"github.com/liverty-music/backend/internal/entity"
)

// WorkflowDeliverNotification is the registered name of
// NotificationUseCase.RunDeliver.
const WorkflowDeliverNotification = "deliver-notification"

// DeliverRequest is the input of the deliver-notification workflow: one
// notification for one fan.
type DeliverRequest struct {
	// UserID is the fan the notification is for.
	UserID string `json:"user_id"`
	// Type is what produced the notification.
	Type entity.NotificationType `json:"type"`
	// Payload is the finished message.
	Payload *entity.NotificationPayload `json:"payload"`
}

// deliverWorkflowID names a deliver-notification workflow after its business
// key, so a repeated request with the same key starts nothing new.
func deliverWorkflowID(typ entity.NotificationType, parts ...string) string {
	return "deliver:" + string(typ) + ":" + strings.Join(parts, ":")
}

// RunDeliver is the deliver-notification workflow: it records and delivers
// the notification through Deliver in one step. A step that fails with
// Unavailable (the Notification cannot be recorded) is tried again.
func (uc *notificationUseCase) RunDeliver(ctx context.Context, f Flow, in DeliverRequest) (string, error) {
	return Step(ctx, f, "deliver", func(ctx context.Context) (string, error) {
		n, err := uc.Deliver(ctx, in.UserID, in.Type, in.Payload)
		if err != nil {
			return "", err
		}
		return n.ID, nil
	})
}

// startDeliver starts the deliver-notification workflow id for the request.
func (uc *notificationUseCase) startDeliver(ctx context.Context, id string, req DeliverRequest) error {
	return uc.workflows.Start(ctx, WorkflowDeliverNotification, id, req)
}
