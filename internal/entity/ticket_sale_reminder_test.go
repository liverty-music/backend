package entity_test

import (
	"testing"
	"time"

	"github.com/pannpers/go-apperr/apperr"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReminderStage_IsValid(t *testing.T) {
	t.Parallel()

	for _, s := range []entity.ReminderStage{
		entity.ReminderStageApplyOpen, entity.ReminderStageApplyClose24H,
	} {
		assert.True(t, s.IsValid(), s.String())
	}
	// @spec components/entity/ticket-sale-reminder "Undefined stage"
	for _, s := range []entity.ReminderStage{0, 3, 5} {
		assert.False(t, s.IsValid(), "stage %d", s)
	}
	// @spec components/entity/ticket-sale-reminder "Result day stage"
	// 4 was RESULT_DAY.
	assert.False(t, entity.ReminderStage(4).IsValid())
}

func TestReminderStage_Anchor(t *testing.T) {
	t.Parallel()

	anchors := func(sale *entity.TicketSale) map[entity.ReminderStage]time.Time {
		got := map[entity.ReminderStage]time.Time{}
		for _, s := range []entity.ReminderStage{
			entity.ReminderStageApplyOpen, entity.ReminderStageApplyClose24H, entity.ReminderStage(4),
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
		// @spec components/entity/ticket-sale-reminder "Organizer's lottery"
		t.Parallel()
		sale := saleWindow(entity.TicketSaleMethodLottery, oct(5, 18, 0), oct(12, 23, 59))

		assert.Equal(t, map[entity.ReminderStage]time.Time{
			entity.ReminderStageApplyOpen:     oct(5, 18, 0),
			entity.ReminderStageApplyClose24H: oct(11, 23, 59),
		}, anchors(sale))
	})

	t.Run("result date anchors nothing", func(t *testing.T) {
		// @spec components/entity/ticket-sale-reminder "Result date anchors nothing"
		t.Parallel()
		sale := discoveredSale(entity.TicketSaleMethodLottery, oct(5, 18, 0), oct(22, 23, 59))
		sale.ResultDate = day(time.November, 3)

		assert.Equal(t, map[entity.ReminderStage]time.Time{
			entity.ReminderStageApplyOpen:     oct(5, 18, 0),
			entity.ReminderStageApplyClose24H: oct(21, 23, 59),
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

		assert.ErrorIs(t, search.Validate(now), apperr.ErrInvalidArgument)
	})

	t.Run("searched now", func(t *testing.T) {
		t.Parallel()
		search := &entity.TicketSaleSearch{SeriesID: "series-1", SearchedTime: now}

		assert.NoError(t, search.Validate(now))
	})
}

func TestDiscoveredTicketSale_FoundReason(t *testing.T) {
	t.Parallel()

	jst := time.FixedZone("JST", 9*60*60)
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, jst)
	ref := &entity.SalesSeriesRef{
		SeriesID: "s-1", Title: "Tour",
		EventDates: []time.Time{time.Date(2026, 12, 20, 0, 0, 0, 0, time.UTC), time.Date(2026, 12, 5, 0, 0, 0, 0, time.UTC), {}},
	}
	good := func() *entity.DiscoveredTicketSale {
		return &entity.DiscoveredTicketSale{
			SeriesID: "s-1", Name: "先行抽選", Method: entity.TicketSaleMethodLottery,
			SaleStartTime: time.Date(2026, 10, 5, 16, 0, 0, 0, jst), SaleEndTime: time.Date(2026, 10, 22, 23, 59, 0, 0, jst),
		}
	}

	tests := []struct {
		name   string
		mutate func(*entity.DiscoveredTicketSale)
		want   string
	}{
		{"valid", func(*entity.DiscoveredTicketSale) {}, ""},
		{"already started", func(d *entity.DiscoveredTicketSale) { d.SaleStartTime = now }, "already started"},
		{"no name", func(d *entity.DiscoveredTicketSale) { d.Name = "" }, "no name"},
		{"no end time", func(d *entity.DiscoveredTicketSale) { d.SaleEndTime = time.Time{} }, "no apply_end_time"},
		{"ends before it starts", func(d *entity.DiscoveredTicketSale) { d.SaleEndTime = d.SaleStartTime.Add(-time.Hour) }, "sale end must be after the sale start"},
		{"starts at the start of the day after the last event", func(d *entity.DiscoveredTicketSale) {
			d.SaleStartTime = time.Date(2026, 12, 21, 0, 0, 0, 0, jst)
			d.SaleEndTime = d.SaleStartTime.Add(24 * time.Hour)
		}, ""},
		{"starts after the day following the last event begins", func(d *entity.DiscoveredTicketSale) {
			d.SaleStartTime = time.Date(2026, 12, 21, 0, 1, 0, 0, jst)
			d.SaleEndTime = d.SaleStartTime.Add(24 * time.Hour)
		}, "apply_start_time after the series' last event"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			d := good()
			tt.mutate(d)
			got := d.FoundReason(now, ref)
			if tt.want == "" {
				assert.Empty(t, got)
				return
			}
			assert.Contains(t, got, tt.want)
		})
	}

	t.Run("a series without event dates has no last event", func(t *testing.T) {
		t.Parallel()
		d := good()
		d.SaleStartTime = time.Date(2027, 6, 1, 9, 0, 0, 0, jst)
		d.SaleEndTime = d.SaleStartTime.Add(24 * time.Hour)
		assert.Empty(t, d.FoundReason(now, &entity.SalesSeriesRef{SeriesID: "s-1"}))
	})

	t.Run("event period", func(t *testing.T) {
		t.Parallel()
		first, last, ok := ref.EventPeriod()
		require.True(t, ok)
		assert.Equal(t, 5, first.Day())
		assert.Equal(t, 20, last.Day())
		_, _, ok = (&entity.SalesSeriesRef{}).EventPeriod()
		assert.False(t, ok)
	})
}

func TestTicketSale_ReminderAudience(t *testing.T) {
	t.Parallel()

	discovered := entity.NewTicketSale("series-1", "n", entity.TicketSaleMethodLottery, nov(1, 10, 0), nov(5, 10, 0), "e-1")
	found := nov(1, 0, 0)
	discovered.DiscoveredTime = &found
	organizer := entity.NewTicketSale("series-1", "n", entity.TicketSaleMethodLottery, nov(1, 10, 0), nov(5, 10, 0), "e-1", "e-2")

	assert.Equal(t, entity.ReminderAudience{SeriesID: "series-1"}, discovered.ReminderAudience(), "every fan tracking the series")
	assert.Equal(t, entity.ReminderAudience{EventIDs: []string{"e-1", "e-2"}}, organizer.ReminderAudience(), "only the fans tracking an event the sale names")
}

func TestReminderOutcome(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		delivered, noSub bool
		want             entity.ReminderOutcome
		wantSent         bool
		wantName         string
	}{
		{"delivered", true, false, entity.ReminderOutcomeDelivered, true, "delivered"},
		{"no subscription counts as sent", false, true, entity.ReminderOutcomeNoSubscription, true, "no_subscription"},
		{"failed is left unsent", false, false, entity.ReminderOutcomeFailed, false, "failed"},
	}
	for _, tt := range tests {
		got := entity.NewReminderOutcome(tt.delivered, tt.noSub)
		assert.Equal(t, tt.want, got, tt.name)
		assert.Equal(t, tt.wantSent, got.IsSent(), tt.name)
		assert.Equal(t, tt.wantName, got.String(), tt.name)
	}

	assert.True(t, entity.ReminderCanBeSent("event-1"))
	assert.False(t, entity.ReminderCanBeSent(""), "a request without a linked event is skipped")
}

func TestSeries_IsSearchable(t *testing.T) {
	t.Parallel()
	org := "org-1"
	assert.True(t, (&entity.Series{}).IsSearchable(), "a discovered series is searched")
	assert.False(t, (&entity.Series{OrganizerID: &org}).IsSearchable(), "a first-party series is never searched")
}
