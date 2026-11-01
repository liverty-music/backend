package event_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/liverty-music/backend/internal/adapter/event"
	"github.com/liverty-music/backend/internal/entity"
	ucmocks "github.com/liverty-music/backend/internal/usecase/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ----- helpers -----

func makeTicketSaleDiscoveredMsg(t *testing.T, data entity.TicketSaleDiscoveredData) *message.Message {
	t.Helper()
	payload, err := json.Marshal(data)
	require.NoError(t, err)
	return message.NewMessage("test-id", payload)
}

// ----- TicketSaleAnnouncementConsumer tests -----

func TestTicketSaleAnnouncementConsumer_Handle(t *testing.T) {
	t.Parallel()

	validData := entity.TicketSaleDiscoveredData{
		TicketSaleID:  "sale-001",
		SeriesID:      "series-001",
		Method:        int16(entity.TicketSaleMethodLottery),
		SaleStartTime: time.Date(2026, 10, 5, 9, 0, 0, 0, time.UTC),
	}

	t.Run("delegates to use case on success", func(t *testing.T) {
		t.Parallel()

		uc := ucmocks.NewMockTicketSaleAnnouncementUseCase(t)
		uc.On("AnnounceDiscoveredSale", context.Background(), validData).Return(nil)

		handler := event.NewTicketSaleAnnouncementConsumer(uc, newTestLogger(t))
		msg := makeTicketSaleDiscoveredMsg(t, validData)
		msg.SetContext(context.Background())

		err := handler.Handle(msg)
		require.NoError(t, err)
	})

	t.Run("returns error when use case fails", func(t *testing.T) {
		t.Parallel()

		uc := ucmocks.NewMockTicketSaleAnnouncementUseCase(t)
		uc.On("AnnounceDiscoveredSale", context.Background(), validData).
			Return(fmt.Errorf("push failed"))

		handler := event.NewTicketSaleAnnouncementConsumer(uc, newTestLogger(t))
		msg := makeTicketSaleDiscoveredMsg(t, validData)
		msg.SetContext(context.Background())

		err := handler.Handle(msg)
		assert.Error(t, err)
	})

	t.Run("returns error on invalid payload", func(t *testing.T) {
		t.Parallel()

		uc := ucmocks.NewMockTicketSaleAnnouncementUseCase(t)
		handler := event.NewTicketSaleAnnouncementConsumer(uc, newTestLogger(t))

		msg := message.NewMessage("bad-id", []byte("not json"))
		msg.SetContext(context.Background())

		err := handler.Handle(msg)
		assert.Error(t, err)
	})
}
