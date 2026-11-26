package rpc_test

import (
	"testing"
	"time"

	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	ticketsalev1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/ticket_sale/v1"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	handler "github.com/liverty-music/backend/internal/adapter/rpc"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/usecase"
	ucmocks "github.com/liverty-music/backend/internal/usecase/mocks"
)

func TestTicketSaleHandler_ListByEvent(t *testing.T) {
	t.Parallel()

	t.Run("return a sale window as date-times in the sale's time zone", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/fan/api/rpc/ticket-sale "Sale window in its own time zone"
		sales := ucmocks.NewMockTicketSaleUseCase(t)
		sales.EXPECT().ListByEvent(mock.Anything, testEventID).Return(&usecase.EventSales{
			Sales: []*entity.TicketSale{{
				ID: tkSaleID, SeriesID: tkSeriesID, Name: "一般発売", Method: entity.TicketSaleMethodFirstCome,
				TimeZone: "Asia/Taipei",
				// 12:00 on 1 November to 23:59 on 7 November in Taipei.
				SaleStartTime: time.Date(2026, 11, 1, 4, 0, 0, 0, time.UTC),
				SaleEndTime:   time.Date(2026, 11, 7, 15, 59, 0, 0, time.UTC),
				EventIDs:      []string{testEventID},
			}},
		}, nil).Once()
		logger, err := logging.New()
		require.NoError(t, err)
		h := handler.NewTicketSaleHandler(sales, logger)

		resp, err := h.ListByEvent(t.Context(), &ticketsalev1.ListByEventRequest{
			EventId: &entityv1.EventId{Value: testEventID},
		})

		require.NoError(t, err)
		require.Len(t, resp.GetTicketSales(), 1)
		sale := resp.GetTicketSales()[0]
		assert.Equal(t, "2026-11-01 12:00 Asia/Taipei", localString(sale.GetSaleStartTime()))
		assert.Equal(t, "2026-11-07 23:59 Asia/Taipei", localString(sale.GetSaleEndTime()))
	})
}
