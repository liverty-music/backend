package event_test

import (
	"fmt"
	"testing"
	"time"

	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	"github.com/liverty-music/backend/internal/adapter/event"
	"github.com/liverty-music/backend/internal/entity"
	ucmocks "github.com/liverty-music/backend/internal/usecase/mocks"
	"github.com/stretchr/testify/assert"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestTicketSaleAnnouncementConsumer_Handle(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, 10, 5, 9, 0, 0, 0, time.UTC)
	sale := &entityv1.TicketSale{
		Id:            &entityv1.TicketSaleId{Value: "sale-001"},
		SeriesId:      &entityv1.SeriesId{Value: "series-001"},
		Method:        entityv1.TicketSaleMethod_TICKET_SALE_METHOD_LOTTERY,
		SaleStartTime: timestamppb.New(start),
	}

	t.Run("returns error when use case fails", func(t *testing.T) {
		t.Parallel()

		uc := ucmocks.NewMockTicketSaleAnnouncementUseCase(t)
		uc.EXPECT().AnnounceDiscoveredSale(anyCtx, entity.TicketSaleDiscoveredData{
			TicketSaleID:  "sale-001",
			SeriesID:      "series-001",
			Method:        int16(entity.TicketSaleMethodLottery),
			SaleStartTime: start,
		}).Return(fmt.Errorf("push failed")).Once()

		handler := event.NewTicketSaleAnnouncementConsumer(uc, newTestLogger(t))

		err := handler.Handle(makeAnnouncementMsg(t, "announcement-1", "", sale))
		assert.Error(t, err)
	})

	t.Run("returns error on invalid payload", func(t *testing.T) {
		t.Parallel()

		uc := ucmocks.NewMockTicketSaleAnnouncementUseCase(t)
		handler := event.NewTicketSaleAnnouncementConsumer(uc, newTestLogger(t))

		err := handler.Handle(malformedMsg())
		assert.Error(t, err)
	})
}
