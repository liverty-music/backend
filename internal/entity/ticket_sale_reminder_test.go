package entity_test

import (
	"testing"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/stretchr/testify/assert"
)

func TestReminderStage_IsValid(t *testing.T) {
	t.Parallel()

	for _, s := range []entity.ReminderStage{
		entity.ReminderStageApplyOpen, entity.ReminderStageApplyClose24H,
	} {
		assert.True(t, s.IsValid(), s.String())
	}
	// @spec components/entity/ticket-sale-reminder "Result day stage"
	assert.True(t, entity.ReminderStageResultDay.IsValid())
	assert.Equal(t, "RESULT_DAY", entity.ReminderStageResultDay.String())
	// @spec components/entity/ticket-sale-reminder "Undefined stage"
	for _, s := range []entity.ReminderStage{0, 3, 5} {
		assert.False(t, s.IsValid(), "stage %d", s)
	}
}

func TestReminderStage_Anchor(t *testing.T) {
	t.Parallel()

	anchors := func(sale *entity.TicketSale) map[entity.ReminderStage]time.Time {
		got := map[entity.ReminderStage]time.Time{}
		for _, s := range []entity.ReminderStage{
			entity.ReminderStageApplyOpen, entity.ReminderStageApplyClose24H, entity.ReminderStageResultDay,
		} {
			if at, ok := s.Anchor(sale, jst); ok {
				got[s] = at
			}
		}
		return got
	}

	t.Run("lottery stages", func(t *testing.T) {
		// @spec components/entity/ticket-sale-reminder "Lottery stages"
		t.Parallel()
		sale := discoveredSale(entity.TicketSaleMethodLottery, oct(5, 18, 0), oct(22, 23, 59))

		assert.Equal(t, map[entity.ReminderStage]time.Time{
			entity.ReminderStageApplyOpen:     oct(5, 18, 0),
			entity.ReminderStageApplyClose24H: oct(21, 23, 59),
		}, anchors(sale))
	})

	t.Run("first-come stage", func(t *testing.T) {
		// @spec components/entity/ticket-sale-reminder "First-come stage"
		t.Parallel()
		sale := discoveredSale(entity.TicketSaleMethodFirstCome, oct(6, 19, 0), oct(10, 23, 59))

		assert.Equal(t, map[entity.ReminderStage]time.Time{
			entity.ReminderStageApplyOpen: oct(6, 18, 30),
		}, anchors(sale))
	})

	t.Run("organizer's lottery", func(t *testing.T) {
		t.Parallel()
		sale := saleWindow(entity.TicketSaleMethodLottery, oct(5, 18, 0), oct(12, 23, 59))

		assert.Equal(t, map[entity.ReminderStage]time.Time{
			entity.ReminderStageApplyOpen:     oct(5, 18, 0),
			entity.ReminderStageApplyClose24H: oct(11, 23, 59),
		}, anchors(sale))
	})

	t.Run("result day stage", func(t *testing.T) {
		// @spec components/entity/ticket-sale-reminder "Result day stage"
		t.Parallel()
		tokyo, err := time.LoadLocation("Asia/Tokyo")
		if err != nil {
			t.Skip("tzdata unavailable")
		}
		sale := discoveredSale(entity.TicketSaleMethodLottery, oct(5, 18, 0), oct(22, 23, 59))
		sale.ResultDate = day(time.November, 3)

		at, ok := entity.ReminderStageResultDay.Anchor(sale, tokyo)

		assert.True(t, ok)
		assert.True(t, time.Date(2026, 11, 3, 9, 0, 0, 0, tokyo).Equal(at), at)
	})

	t.Run("result day in the fan's time zone", func(t *testing.T) {
		t.Parallel()
		hawaii := time.FixedZone("HST", -10*60*60)
		sale := discoveredSale(entity.TicketSaleMethodLottery, oct(5, 18, 0), oct(22, 23, 59))
		sale.ResultDate = day(time.November, 3)

		at, ok := entity.ReminderStageResultDay.Anchor(sale, hawaii)

		assert.True(t, ok)
		assert.True(t, time.Date(2026, 11, 3, 9, 0, 0, 0, hawaii).Equal(at), at)
	})

	t.Run("lottery without a result date", func(t *testing.T) {
		// @spec components/entity/ticket-sale-reminder "Lottery without a result date"
		t.Parallel()
		discovered := discoveredSale(entity.TicketSaleMethodLottery, oct(5, 18, 0), oct(22, 23, 59))
		organizers := saleWindow(entity.TicketSaleMethodLottery, oct(5, 18, 0), oct(12, 23, 59))
		// Validate rejects a result date on an Organizer's sale; Anchor still
		// never applies the stage to one.
		organizers.ResultDate = day(time.November, 3)

		for _, sale := range []*entity.TicketSale{discovered, organizers} {
			_, ok := entity.ReminderStageResultDay.Anchor(sale, jst)
			assert.False(t, ok)
		}
	})

	t.Run("undefined stage", func(t *testing.T) {
		t.Parallel()
		sale := saleWindow(entity.TicketSaleMethodLottery, oct(5, 18, 0), oct(12, 23, 59))

		_, ok := entity.ReminderStage(3).Anchor(sale, jst)

		assert.False(t, ok)
	})
}

func TestTicketSaleSearch_Validate(t *testing.T) {
	t.Parallel()

	now := oct(13, 21, 0)

	t.Run("future searched time", func(t *testing.T) {
		// @spec components/entity/ticket-sale-search "Future searched time"
		t.Parallel()
		search := &entity.TicketSaleSearch{SeriesID: "series-1", SearchedTime: now.Add(time.Hour)}

		assert.Error(t, search.Validate(now))
	})

	t.Run("searched now", func(t *testing.T) {
		t.Parallel()
		search := &entity.TicketSaleSearch{SeriesID: "series-1", SearchedTime: now}

		assert.NoError(t, search.Validate(now))
	})
}
