package event_test

import (
	"context"
	"testing"
	"time"

	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/liverty-music/backend/internal/adapter/event"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/messaging"
	"github.com/liverty-music/backend/internal/usecase"
	ucmocks "github.com/liverty-music/backend/internal/usecase/mocks"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// makeResultMsg builds a LOTTERY_ENTRY.won or LOTTERY_ENTRY.lost
// announcement id of entry-1, whose Order is order-1, with the lost reason.
// The payload carries no state: the subject tells it.
func makeResultMsg(t *testing.T, id, subject string, reason entityv1.LotteryEntryLostReason) *message.Message {
	t.Helper()
	position := int64(3)
	msg := makeAnnouncementMsg(t, id, "", &entityv1.LotteryEntry{
		Id:           &entityv1.LotteryEntryId{Value: "entry-1"},
		OrderId:      &entityv1.OrderId{Value: "order-1"},
		TicketSaleId: &entityv1.TicketSaleId{Value: "sale-1"},
		DrawPosition: &position,
		LostReason:   reason,
	})
	msg.Metadata.Set("ce_type", subject)
	return msg
}

// startedWorkflows returns a lottery entry result consumer over a real
// OrderUseCase whose workflow starts are recorded by id, as many times as
// each is asked.
func startedWorkflows(t *testing.T) (*event.LotteryEntryResultConsumer, map[string]int) {
	t.Helper()
	started := map[string]int{}
	workflows := ucmocks.NewMockWorkflows(t)
	workflows.EXPECT().Start(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, _, id string, _ any, _ ...usecase.StartOption) error {
			started[id]++
			return nil
		}).Maybe()
	orders := usecase.NewOrderUseCase(usecase.OrderDeps{Workflows: workflows, Clock: time.Now, Logger: newTestLogger(t)})
	return event.NewLotteryEntryResultConsumer(orders, ucmocks.NewMockNotificationUseCase(t), newTestLogger(t)), started
}

func TestLotteryEntryResultConsumer_HandleStartWorkflow(t *testing.T) {
	t.Parallel()

	t.Run("a won entry starts its issue", func(t *testing.T) {
		t.Parallel()
		consumer, started := startedWorkflows(t)

		require.NoError(t, consumer.HandleStartWorkflow(makeResultMsg(t, "announcement-1",
			entity.SubjectLotteryEntryWon, entityv1.LotteryEntryLostReason_LOTTERY_ENTRY_LOST_REASON_UNSPECIFIED)))

		assert.Equal(t, map[string]int{"issue:order-1": 1}, started)
	})

	t.Run("a lost entry starts its release", func(t *testing.T) {
		t.Parallel()
		consumer, started := startedWorkflows(t)

		require.NoError(t, consumer.HandleStartWorkflow(makeResultMsg(t, "announcement-2",
			entity.SubjectLotteryEntryLost, entityv1.LotteryEntryLostReason_LOTTERY_ENTRY_LOST_REASON_NOT_DRAWN)))

		assert.Equal(t, map[string]int{"release:order-1": 1}, started)
	})

	t.Run("a repeated delivery starts each workflow under one name", func(t *testing.T) {
		t.Parallel()
		// The runtime runs one workflow per name, so the second start of the
		// same name starts nothing.
		consumer, started := startedWorkflows(t)
		won := makeResultMsg(t, "announcement-1", entity.SubjectLotteryEntryWon, entityv1.LotteryEntryLostReason_LOTTERY_ENTRY_LOST_REASON_UNSPECIFIED)
		lost := makeResultMsg(t, "announcement-2", entity.SubjectLotteryEntryLost, entityv1.LotteryEntryLostReason_LOTTERY_ENTRY_LOST_REASON_NOT_DRAWN)

		for range 2 {
			require.NoError(t, consumer.HandleStartWorkflow(won))
			require.NoError(t, consumer.HandleStartWorkflow(lost))
		}

		assert.Equal(t, map[string]int{"issue:order-1": 2, "release:order-1": 2}, started, "two names, each asked twice")
	})

	t.Run("a reverted win starts its release", func(t *testing.T) {
		t.Parallel()
		consumer, started := startedWorkflows(t)

		require.NoError(t, consumer.HandleStartWorkflow(makeResultMsg(t, "announcement-3",
			entity.SubjectLotteryEntryLost, entityv1.LotteryEntryLostReason_LOTTERY_ENTRY_LOST_REASON_CARD_NOT_CHARGED)))

		assert.Equal(t, map[string]int{"release:order-1": 1}, started)
	})

	t.Run("a failed start is returned for redelivery", func(t *testing.T) {
		t.Parallel()
		orders := ucmocks.NewMockOrderUseCase(t)
		orders.EXPECT().StartDrawnEntryWorkflow(anyCtx, mock.Anything).Return(apperr.New(codes.Unavailable, "db down"))
		consumer := event.NewLotteryEntryResultConsumer(orders, ucmocks.NewMockNotificationUseCase(t), newTestLogger(t))

		assert.Error(t, consumer.HandleStartWorkflow(makeResultMsg(t, "announcement-4",
			entity.SubjectLotteryEntryWon, entityv1.LotteryEntryLostReason_LOTTERY_ENTRY_LOST_REASON_UNSPECIFIED)))
	})

	t.Run("a message under another subject is rejected", func(t *testing.T) {
		t.Parallel()
		consumer := event.NewLotteryEntryResultConsumer(ucmocks.NewMockOrderUseCase(t), ucmocks.NewMockNotificationUseCase(t), newTestLogger(t))

		assert.Error(t, consumer.HandleStartWorkflow(makeResultMsg(t, "announcement-5",
			entity.SubjectLotteryEntryEntered, entityv1.LotteryEntryLostReason_LOTTERY_ENTRY_LOST_REASON_UNSPECIFIED)))
	})

	t.Run("a malformed payload is rejected", func(t *testing.T) {
		t.Parallel()
		consumer := event.NewLotteryEntryResultConsumer(ucmocks.NewMockOrderUseCase(t), ucmocks.NewMockNotificationUseCase(t), newTestLogger(t))
		msg := malformedMsg()
		msg.Metadata.Set("ce_type", entity.SubjectLotteryEntryWon)

		assert.Error(t, consumer.HandleStartWorkflow(msg))
	})
}

func TestLotteryEntryResultConsumer_HandleSendLotteryLoss(t *testing.T) {
	t.Parallel()

	t.Run("a lost entry is told", func(t *testing.T) {
		t.Parallel()
		notifications := ucmocks.NewMockNotificationUseCase(t)
		notifications.EXPECT().SendLotteryLoss(anyCtx, entity.LotteryEntryID("entry-1")).Return(nil).Once()
		consumer := event.NewLotteryEntryResultConsumer(ucmocks.NewMockOrderUseCase(t), notifications, newTestLogger(t))

		require.NoError(t, consumer.HandleSendLotteryLoss(makeResultMsg(t, "announcement-1",
			entity.SubjectLotteryEntryLost, entityv1.LotteryEntryLostReason_LOTTERY_ENTRY_LOST_REASON_NOT_DRAWN)))
	})

	t.Run("an unavailable mail service is delivered again later", func(t *testing.T) {
		t.Parallel()
		notifications := ucmocks.NewMockNotificationUseCase(t)
		notifications.EXPECT().SendLotteryLoss(anyCtx, entity.LotteryEntryID("entry-1")).Return(apperr.New(codes.Unavailable, "postmark down")).Once()
		consumer := event.NewLotteryEntryResultConsumer(ucmocks.NewMockOrderUseCase(t), notifications, newTestLogger(t))

		err := consumer.HandleSendLotteryLoss(makeResultMsg(t, "announcement-1",
			entity.SubjectLotteryEntryLost, entityv1.LotteryEntryLostReason_LOTTERY_ENTRY_LOST_REASON_NOT_DRAWN))

		assert.True(t, messaging.IsRedeliverLater(err), "the message waits for a later delivery instead of the poison queue")
	})
}
