package usecase

import (
	"context"
	"fmt"
	"time"

	"github.com/liverty-music/backend/internal/entity"
)

// WorkflowDeliverReminder is the registered name of
// TicketSaleReminderDeliveryUseCase.RunDeliverReminder.
const WorkflowDeliverReminder = "deliver-reminder"

// reminderScanPeriod is the length of the scan periods a reminder workflow is
// named after: a run repeated in the same period starts no reminder twice,
// and a later period starts it again while it is not recorded as sent.
const reminderScanPeriod = 15 * time.Minute

// reminderWorkflowID names the reminder of the fan, sale and stage in the
// scan period of now.
func reminderWorkflowID(data entity.TicketSaleReminderDueData, now time.Time) string {
	return fmt.Sprintf("reminder:%s:%s:%d:%d", data.UserID, data.TicketSaleID, data.Stage, now.Truncate(reminderScanPeriod).Unix())
}

// RunDeliverReminder is the deliver-reminder workflow: it delivers the
// reminder through DeliverReminder in one step.
func (uc *ticketSaleReminderDeliveryUseCase) RunDeliverReminder(ctx context.Context, f Flow, in entity.TicketSaleReminderDueData) (struct{}, error) {
	return Step(ctx, f, "deliver", func(ctx context.Context) (struct{}, error) {
		return struct{}{}, uc.DeliverReminder(ctx, in)
	})
}
