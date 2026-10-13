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
		entity.ReminderStageApplyOpen, entity.ReminderStageApplyClose24H, entity.ReminderStageResultDay,
	} {
		assert.True(t, s.IsValid(), s.String())
	}
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
			if at, ok := s.Anchor(sale); ok {
				got[s] = at
			}
		}
		return got
	}

	t.Run("lottery stages", func(t *testing.T) {
		// @spec components/entity/ticket-sale-reminder "Lottery stages"
		t.Parallel()
		sale := discoveredSale(entity.TicketSaleMethodLottery, oct(5, 18, 0), oct(22, 23, 59))
		result := time.Date(2026, 11, 3, 15, 0, 0, 0, jst)
		sale.LotteryResultTime = &result

		assert.Equal(t, map[entity.ReminderStage]time.Time{
			entity.ReminderStageApplyOpen:     oct(5, 18, 0),
			entity.ReminderStageApplyClose24H: oct(21, 23, 59),
			entity.ReminderStageResultDay:     result,
		}, anchors(sale))
	})

	t.Run("first-come stage", func(t *testing.T) {
		// @spec components/entity/ticket-sale-reminder "First-come stage"
		t.Parallel()
		sale := discoveredSale(entity.TicketSaleMethodFirstCome, oct(6, 19, 0), time.Time{})

		assert.Equal(t, map[entity.ReminderStage]time.Time{
			entity.ReminderStageApplyOpen: oct(6, 18, 30),
		}, anchors(sale))
	})

	t.Run("lottery without a result time", func(t *testing.T) {
		// @spec components/entity/ticket-sale-reminder "Lottery without a result time"
		t.Parallel()
		sale := discoveredSale(entity.TicketSaleMethodLottery, oct(5, 18, 0), oct(22, 23, 59))

		_, ok := entity.ReminderStageResultDay.Anchor(sale)

		assert.False(t, ok)
	})

	t.Run("organizer's lottery", func(t *testing.T) {
		// @spec components/entity/ticket-sale-reminder "Organizer's lottery"
		t.Parallel()
		sale := saleWindow(entity.TicketSaleMethodLottery, oct(5, 18, 0), oct(12, 23, 59))

		assert.Equal(t, map[entity.ReminderStage]time.Time{
			entity.ReminderStageApplyOpen:     oct(5, 18, 0),
			entity.ReminderStageApplyClose24H: oct(11, 23, 59),
			entity.ReminderStageResultDay:     oct(12, 23, 59),
		}, anchors(sale))
	})

	t.Run("undefined stage", func(t *testing.T) {
		t.Parallel()
		sale := saleWindow(entity.TicketSaleMethodLottery, oct(5, 18, 0), oct(12, 23, 59))

		_, ok := entity.ReminderStage(3).Anchor(sale)

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
