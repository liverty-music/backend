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

// makeDrawnMsg builds the ORDER.lottery_entry_drawn announcement id of
// entry-1, whose Order is order-1, in the given state and lost reason.
func makeDrawnMsg(t *testing.T, id string, state entityv1.LotteryEntryState, reason entityv1.LotteryEntryLostReason) *message.Message {
	t.Helper()
	position := int64(3)
	return makeAnnouncementMsg(t, id, "", &entityv1.LotteryEntry{
		Id:           &entityv1.LotteryEntryId{Value: "entry-1"},
		OrderId:      &entityv1.OrderId{Value: "order-1"},
		TicketSaleId: &entityv1.TicketSaleId{Value: "sale-1"},
		State:        state,
		DrawPosition: &position,
		LostReason:   reason,
	})
}

// startedWorkflows returns a drawn-entry consumer over a real OrderUseCase
// whose workflow starts are recorded by id, as many times as each is asked.
func startedWorkflows(t *testing.T) (*event.LotteryEntryDrawnConsumer, map[string]int) {
	t.Helper()
	started := map[string]int{}
	workflows := ucmocks.NewMockWorkflows(t)
	workflows.EXPECT().Start(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, _, id string, _ any, _ ...usecase.StartOption) error {
			started[id]++
			return nil
		}).Maybe()
	orders := usecase.NewOrderUseCase(usecase.OrderDeps{Workflows: workflows, Clock: time.Now, Logger: newTestLogger(t)})
	return event.NewLotteryEntryDrawnConsumer(orders, ucmocks.NewMockNotificationUseCase(t), newTestLogger(t)), started
}

func TestLotteryEntryDrawnConsumer_HandleStartWorkflow(t *testing.T) {
	t.Parallel()

	t.Run("a repeated delivery starts each workflow under one name", func(t *testing.T) {
		t.Parallel()
		// The runtime runs one workflow per name, so the second start of the
		// same name starts nothing.
		consumer, started := startedWorkflows(t)
		won := makeDrawnMsg(t, "announcement-1", entityv1.LotteryEntryState_LOTTERY_ENTRY_STATE_WON, entityv1.LotteryEntryLostReason_LOTTERY_ENTRY_LOST_REASON_UNSPECIFIED)
		lost := makeDrawnMsg(t, "announcement-2", entityv1.LotteryEntryState_LOTTERY_ENTRY_STATE_LOST, entityv1.LotteryEntryLostReason_LOTTERY_ENTRY_LOST_REASON_NOT_DRAWN)

		for range 2 {
			require.NoError(t, consumer.HandleStartWorkflow(won))
			require.NoError(t, consumer.HandleStartWorkflow(lost))
		}

		assert.Equal(t, map[string]int{"issue:order-1": 2, "release:order-1": 2}, started, "two names, each asked twice")
	})

	t.Run("a reverted win starts its release", func(t *testing.T) {
		t.Parallel()
		consumer, started := startedWorkflows(t)

		require.NoError(t, consumer.HandleStartWorkflow(makeDrawnMsg(t, "announcement-3",
			entityv1.LotteryEntryState_LOTTERY_ENTRY_STATE_LOST, entityv1.LotteryEntryLostReason_LOTTERY_ENTRY_LOST_REASON_CARD_NOT_CHARGED)))

		assert.Equal(t, map[string]int{"release:order-1": 1}, started)
	})

	t.Run("a failed start is returned for redelivery", func(t *testing.T) {
		t.Parallel()
		orders := ucmocks.NewMockOrderUseCase(t)
		orders.EXPECT().StartDrawnEntryWorkflow(anyCtx, mock.Anything).Return(apperr.New(codes.Unavailable, "db down"))
		consumer := event.NewLotteryEntryDrawnConsumer(orders, ucmocks.NewMockNotificationUseCase(t), newTestLogger(t))

		assert.Error(t, consumer.HandleStartWorkflow(makeDrawnMsg(t, "announcement-4",
			entityv1.LotteryEntryState_LOTTERY_ENTRY_STATE_WON, entityv1.LotteryEntryLostReason_LOTTERY_ENTRY_LOST_REASON_UNSPECIFIED)))
	})

	t.Run("a malformed payload is rejected", func(t *testing.T) {
		t.Parallel()
		consumer := event.NewLotteryEntryDrawnConsumer(ucmocks.NewMockOrderUseCase(t), ucmocks.NewMockNotificationUseCase(t), newTestLogger(t))

		assert.Error(t, consumer.HandleStartWorkflow(malformedMsg()))
	})
}

func TestLotteryEntryDrawnConsumer_HandleSendLotteryLoss(t *testing.T) {
	t.Parallel()

	t.Run("a lost entry is told", func(t *testing.T) {
		t.Parallel()
		notifications := ucmocks.NewMockNotificationUseCase(t)
		notifications.EXPECT().SendLotteryLoss(anyCtx, entity.LotteryEntryID("entry-1")).Return(nil).Once()
		consumer := event.NewLotteryEntryDrawnConsumer(ucmocks.NewMockOrderUseCase(t), notifications, newTestLogger(t))

		require.NoError(t, consumer.HandleSendLotteryLoss(makeDrawnMsg(t, "announcement-1",
			entityv1.LotteryEntryState_LOTTERY_ENTRY_STATE_LOST, entityv1.LotteryEntryLostReason_LOTTERY_ENTRY_LOST_REASON_NOT_DRAWN)))
	})

	t.Run("a won entry is not told here", func(t *testing.T) {
		t.Parallel()
		consumer := event.NewLotteryEntryDrawnConsumer(ucmocks.NewMockOrderUseCase(t), ucmocks.NewMockNotificationUseCase(t), newTestLogger(t))

		require.NoError(t, consumer.HandleSendLotteryLoss(makeDrawnMsg(t, "announcement-1",
			entityv1.LotteryEntryState_LOTTERY_ENTRY_STATE_WON, entityv1.LotteryEntryLostReason_LOTTERY_ENTRY_LOST_REASON_UNSPECIFIED)))
	})

	t.Run("an unavailable mail service is delivered again later", func(t *testing.T) {
		t.Parallel()
		notifications := ucmocks.NewMockNotificationUseCase(t)
		notifications.EXPECT().SendLotteryLoss(anyCtx, entity.LotteryEntryID("entry-1")).Return(apperr.New(codes.Unavailable, "postmark down")).Once()
		consumer := event.NewLotteryEntryDrawnConsumer(ucmocks.NewMockOrderUseCase(t), notifications, newTestLogger(t))

		err := consumer.HandleSendLotteryLoss(makeDrawnMsg(t, "announcement-1",
			entityv1.LotteryEntryState_LOTTERY_ENTRY_STATE_LOST, entityv1.LotteryEntryLostReason_LOTTERY_ENTRY_LOST_REASON_NOT_DRAWN))

		assert.True(t, messaging.IsRedeliverLater(err), "the message waits for a later delivery instead of the poison queue")
	})
}
