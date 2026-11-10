package event_test

import (
	"context"
	"testing"
	"time"

	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/liverty-music/backend/internal/adapter/event"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/messaging"
	"github.com/liverty-music/backend/internal/usecase"
	ucmocks "github.com/liverty-music/backend/internal/usecase/mocks"
)

// makeResaleEntryMsg builds a RESALE_ENTRY announcement id of entry-1, whose
// Order is order-1, under subject with the lost reason. The payload carries
// no state: the subject tells it.
func makeResaleEntryMsg(t *testing.T, id, subject string, reason entityv1.ResaleEntryLostReason) *message.Message {
	t.Helper()
	msg := makeAnnouncementMsg(t, id, "", &entityv1.ResaleEntry{
		Id:         &entityv1.ResaleEntryId{Value: "entry-1"},
		OrderId:    &entityv1.OrderId{Value: "order-1"},
		LostReason: reason,
	})
	msg.Metadata.Set("ce_type", subject)
	return msg
}

// startedResaleWorkflows returns a resale entry consumer over a real
// OrderUseCase whose workflow starts are recorded by id.
func startedResaleWorkflows(t *testing.T) (*event.ResaleEntryConsumer, map[string]int) {
	t.Helper()
	started := map[string]int{}
	workflows := ucmocks.NewMockWorkflows(t)
	workflows.EXPECT().Start(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, _, id string, _ any, _ ...usecase.StartOption) error {
			started[id]++
			return nil
		}).Maybe()
	orders := usecase.NewOrderUseCase(usecase.OrderDeps{Workflows: workflows, Clock: time.Now, Logger: newTestLogger(t)})
	return event.NewResaleEntryConsumer(orders, ucmocks.NewMockNotificationUseCase(t), newTestLogger(t)), started
}

func TestResaleEntryConsumer_HandleStartWorkflow(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		subject string
		reason  entityv1.ResaleEntryLostReason
		want    map[string]int
	}{
		{name: "a won entry starts its issue", subject: entity.SubjectResaleEntryWon, want: map[string]int{"issue:order-1": 1}},
		{name: "a lapsed entry starts its release", subject: entity.SubjectResaleEntryLapsed, want: map[string]int{"release:order-1": 1}},
		{
			name: "a lost entry starts its release", subject: entity.SubjectResaleEntryLost,
			reason: entityv1.ResaleEntryLostReason_RESALE_ENTRY_LOST_REASON_NO_TICKET_LEFT, want: map[string]int{"release:order-1": 1},
		},
		{name: "a withdrawn entry starts its release", subject: entity.SubjectResaleEntryWithdrawn, want: map[string]int{"release:order-1": 1}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			consumer, started := startedResaleWorkflows(t)

			require.NoError(t, consumer.HandleStartWorkflow(makeResaleEntryMsg(t, "announcement-1", tt.subject, tt.reason)))

			assert.Equal(t, tt.want, started)
		})
	}

	t.Run("a message under another subject is rejected", func(t *testing.T) {
		t.Parallel()
		consumer := event.NewResaleEntryConsumer(ucmocks.NewMockOrderUseCase(t), ucmocks.NewMockNotificationUseCase(t), newTestLogger(t))

		assert.Error(t, consumer.HandleStartWorkflow(makeResaleEntryMsg(t, "announcement-2", entity.SubjectResaleListingSold, 0)))
	})

	t.Run("a failed start is returned for redelivery", func(t *testing.T) {
		t.Parallel()
		orders := ucmocks.NewMockOrderUseCase(t)
		orders.EXPECT().StartResaleEntryWorkflow(anyCtx, mock.Anything).Return(apperr.New(codes.Unavailable, "db down"))
		consumer := event.NewResaleEntryConsumer(orders, ucmocks.NewMockNotificationUseCase(t), newTestLogger(t))

		assert.Error(t, consumer.HandleStartWorkflow(makeResaleEntryMsg(t, "announcement-3", entity.SubjectResaleEntryWon, 0)))
	})
}

func TestResaleEntryConsumer_HandleSendResaleEntryEnded(t *testing.T) {
	t.Parallel()

	for _, subject := range []string{entity.SubjectResaleEntryLapsed, entity.SubjectResaleEntryLost} {
		t.Run(subject+" is told", func(t *testing.T) {
			t.Parallel()
			notifications := ucmocks.NewMockNotificationUseCase(t)
			notifications.EXPECT().SendResaleEntryEnded(anyCtx, entity.ResaleEntryID("entry-1")).Return(nil).Once()
			consumer := event.NewResaleEntryConsumer(ucmocks.NewMockOrderUseCase(t), notifications, newTestLogger(t))

			require.NoError(t, consumer.HandleSendResaleEntryEnded(makeResaleEntryMsg(t, "announcement-1", subject, 0)))
		})
	}

	t.Run("an unavailable mail service is delivered again later", func(t *testing.T) {
		t.Parallel()
		notifications := ucmocks.NewMockNotificationUseCase(t)
		notifications.EXPECT().SendResaleEntryEnded(anyCtx, entity.ResaleEntryID("entry-1")).Return(apperr.New(codes.Unavailable, "postmark down")).Once()
		consumer := event.NewResaleEntryConsumer(ucmocks.NewMockOrderUseCase(t), notifications, newTestLogger(t))

		err := consumer.HandleSendResaleEntryEnded(makeResaleEntryMsg(t, "announcement-1", entity.SubjectResaleEntryLost, 0))

		assert.True(t, messaging.IsRedeliverLater(err), "the message waits for a later delivery instead of the poison queue")
	})
}
