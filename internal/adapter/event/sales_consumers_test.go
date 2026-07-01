package event_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/liverty-music/backend/internal/adapter/event"
	"github.com/liverty-music/backend/internal/entity"
	ucmocks "github.com/liverty-music/backend/internal/usecase/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ----- helpers -----

func makeSalesPhaseDiscoveredMsg(t *testing.T, data entity.SalesPhaseDiscoveredData) *message.Message {
	t.Helper()
	payload, err := json.Marshal(data)
	require.NoError(t, err)
	return message.NewMessage("test-id", payload)
}

func makeSalesReminderDueMsg(t *testing.T, data entity.SalesPhaseReminderDueData) *message.Message {
	t.Helper()
	payload, err := json.Marshal(data)
	require.NoError(t, err)
	return message.NewMessage("test-id", payload)
}

// ----- SalesPhaseAnnouncementConsumer tests -----

func TestSalesPhaseAnnouncementConsumer_Handle(t *testing.T) {
	t.Parallel()

	validData := entity.SalesPhaseDiscoveredData{
		PhaseID:  "phase-001",
		SeriesID: "series-001",
	}

	t.Run("delegates to use case on success", func(t *testing.T) {
		t.Parallel()

		uc := ucmocks.NewMockSalesPhaseAnnouncementUseCase(t)
		uc.On("AnnounceDiscoveredPhase", context.Background(), validData).Return(nil)

		handler := event.NewSalesPhaseAnnouncementConsumer(uc, newTestLogger(t))
		msg := makeSalesPhaseDiscoveredMsg(t, validData)
		msg.SetContext(context.Background())

		err := handler.Handle(msg)
		require.NoError(t, err)
	})

	t.Run("returns error when use case fails", func(t *testing.T) {
		t.Parallel()

		uc := ucmocks.NewMockSalesPhaseAnnouncementUseCase(t)
		uc.On("AnnounceDiscoveredPhase", context.Background(), validData).
			Return(fmt.Errorf("push failed"))

		handler := event.NewSalesPhaseAnnouncementConsumer(uc, newTestLogger(t))
		msg := makeSalesPhaseDiscoveredMsg(t, validData)
		msg.SetContext(context.Background())

		err := handler.Handle(msg)
		assert.Error(t, err)
	})

	t.Run("returns error on invalid payload", func(t *testing.T) {
		t.Parallel()

		uc := ucmocks.NewMockSalesPhaseAnnouncementUseCase(t)
		handler := event.NewSalesPhaseAnnouncementConsumer(uc, newTestLogger(t))

		msg := message.NewMessage("bad-id", []byte("not json"))
		msg.SetContext(context.Background())

		err := handler.Handle(msg)
		assert.Error(t, err)
	})
}

// ----- SalesReminderConsumer tests -----

func TestSalesReminderConsumer_Handle(t *testing.T) {
	t.Parallel()

	validData := entity.SalesPhaseReminderDueData{
		UserID:  "user-001",
		PhaseID: "phase-001",
		Stage:   int16(entity.ReminderStageApplyOpen),
		Payload: entity.NewNotificationPayload("Ticket Sales Open", "Sales open now", "/series/s1", "tag-1"),
	}

	t.Run("delegates to use case on success", func(t *testing.T) {
		t.Parallel()

		uc := ucmocks.NewMockSalesReminderDeliveryUseCase(t)
		uc.On("DeliverReminder", context.Background(), validData).Return(nil)

		handler := event.NewSalesReminderConsumer(uc, newTestLogger(t))
		msg := makeSalesReminderDueMsg(t, validData)
		msg.SetContext(context.Background())

		err := handler.Handle(msg)
		require.NoError(t, err)
	})

	t.Run("returns error when use case fails", func(t *testing.T) {
		t.Parallel()

		uc := ucmocks.NewMockSalesReminderDeliveryUseCase(t)
		uc.On("DeliverReminder", context.Background(), validData).
			Return(fmt.Errorf("db unavailable"))

		handler := event.NewSalesReminderConsumer(uc, newTestLogger(t))
		msg := makeSalesReminderDueMsg(t, validData)
		msg.SetContext(context.Background())

		err := handler.Handle(msg)
		assert.Error(t, err)
	})

	t.Run("returns error on invalid payload", func(t *testing.T) {
		t.Parallel()

		uc := ucmocks.NewMockSalesReminderDeliveryUseCase(t)
		handler := event.NewSalesReminderConsumer(uc, newTestLogger(t))

		msg := message.NewMessage("bad-id", []byte("not json"))
		msg.SetContext(context.Background())

		err := handler.Handle(msg)
		assert.Error(t, err)
	})
}
