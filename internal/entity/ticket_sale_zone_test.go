package entity_test

import (
	"testing"
	"time"

	"github.com/pannpers/go-apperr/apperr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/liverty-music/backend/internal/entity"
)

// civil returns the wall-clock date-time of the given fields, to be read in a
// sale's time zone.
func civil(y int, m time.Month, d, hh, mm int) time.Time {
	return time.Date(y, m, d, hh, mm, 0, 0, time.UTC)
}

// saleIn returns a first-come Organizer's sale in zone whose window is the
// given wall-clock start and end, read in that zone. It fails the test when
// a bound does not exist in the zone.
func saleIn(t *testing.T, zone string, start, end time.Time) *entity.TicketSale {
	t.Helper()
	loc := wallZone(t, zone)
	s, err := entity.LocalDateTimeInstant(start, loc)
	require.NoError(t, err)
	e, err := entity.LocalDateTimeInstant(end, loc)
	require.NoError(t, err)
	return entity.NewTicketSale("series-1", "一般発売", entity.TicketSaleMethodFirstCome, zone, s, e, "event-1")
}

func TestTicketSale_TimeZone(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		zone  string
		valid bool
	}{
		// @spec components/entity/ticket-sale "Sale in Taipei"
		{name: "sale in Taipei", zone: "Asia/Taipei", valid: true},
		// @spec components/entity/ticket-sale "Abbreviation instead of a zone"
		{name: "abbreviation JST", zone: "JST"},
		{name: "UTC offset", zone: "+09:00"},
		// @spec components/entity/ticket-sale "No time zone"
		{name: "no time zone", zone: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			sale := entity.NewTicketSale("series-1", "一般発売", entity.TicketSaleMethodFirstCome, tt.zone, nov(1, 10, 0), nov(5, 10, 0), "event-1")
			if tt.valid {
				assert.NoError(t, sale.Validate())
				assert.NotNil(t, sale.Location())
				return
			}
			assert.ErrorIs(t, sale.Validate(), apperr.ErrInvalidArgument)
			assert.Nil(t, sale.Location())
		})
	}
}

func TestTicketSale_WallClockWindow(t *testing.T) {
	t.Parallel()

	t.Run("sale abroad", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/ticket-sale "Sale abroad"
		sale := saleIn(t, "Asia/Taipei", civil(2026, 11, 1, 12, 0), civil(2026, 11, 5, 12, 0))
		assert.True(t, sale.SaleStartTime.Equal(time.Date(2026, 11, 1, 4, 0, 0, 0, time.UTC)))
		assert.True(t, sale.SaleStartTime.Equal(time.Date(2026, 11, 1, 13, 0, 0, 0, jst)), "13:00 in Japan time")
		assert.NoError(t, sale.Validate())
	})

	t.Run("start in the spring-forward gap", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/ticket-sale "Start in the spring-forward gap"
		_, err := entity.LocalDateTimeInstant(civil(2027, 3, 14, 2, 30), wallZone(t, "America/New_York"))
		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
	})

	t.Run("end in the repeated hour", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/ticket-sale "End in the repeated hour"
		sale := saleIn(t, "America/New_York", civil(2026, 10, 30, 12, 0), civil(2026, 11, 1, 1, 30))
		assert.True(t, sale.SaleEndTime.Equal(time.Date(2026, 11, 1, 5, 30, 0, 0, time.UTC)), "the first 01:30, in daylight time")
	})

	t.Run("windows in two time zones", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/ticket-sale "Windows in two time zones"
		tokyo := saleIn(t, "Asia/Tokyo", civil(2026, 11, 5, 10, 0), civil(2026, 11, 10, 20, 0))
		taipei := saleIn(t, "Asia/Taipei", civil(2026, 11, 10, 19, 30), civil(2026, 11, 15, 19, 30))
		assert.True(t, taipei.SaleStartTime.Equal(time.Date(2026, 11, 10, 20, 30, 0, 0, jst)))
		assert.False(t, tokyo.Overlaps(taipei))
		assert.False(t, taipei.Overlaps(tokyo))
	})

	t.Run("sale times in whole minutes", func(t *testing.T) {
		t.Parallel()
		sale := saleIn(t, "Asia/Tokyo", civil(2026, 11, 1, 10, 0), civil(2026, 11, 5, 10, 0))
		sale.SaleEndTime = sale.SaleEndTime.Add(30 * time.Second)
		assert.ErrorIs(t, sale.Validate(), apperr.ErrInvalidArgument)
	})
}

func TestTicketSale_LotteryWindowOnLocalDateTimes(t *testing.T) {
	t.Parallel()

	t.Run("one day across the clock change", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/ticket-sale "One day across the clock change"
		sale := saleIn(t, "Europe/London", civil(2027, 3, 27, 12, 0), civil(2027, 3, 28, 12, 0))
		sale.Method = entity.TicketSaleMethodLottery
		assert.Equal(t, 23*time.Hour, sale.SaleEndTime.Sub(sale.SaleStartTime), "only 23 hours pass")
		assert.NoError(t, sale.Validate())
	})

	t.Run("fourteen days across the clock change", func(t *testing.T) {
		t.Parallel()
		sale := saleIn(t, "Europe/London", civil(2027, 3, 20, 12, 0), civil(2027, 4, 3, 12, 0))
		sale.Method = entity.TicketSaleMethodLottery
		assert.NoError(t, sale.Validate(), "14 calendar days although 14 days less an hour pass")
	})
}

func TestTicketSale_ResultDateAbroad(t *testing.T) {
	t.Parallel()

	// @spec components/entity/ticket-sale "Result date on the closing day abroad"
	london := wallZone(t, "Europe/London")
	end := time.Date(2026, 10, 22, 23, 30, 0, 0, london)
	assert.Equal(t, 23, end.In(jst).Day(), "23 October in Japan time")

	sale := discoveredSale(entity.TicketSaleMethodLottery, time.Date(2026, 10, 5, 18, 0, 0, 0, london), end)
	sale.TimeZone = "Europe/London"
	sale.ResultDate = day(time.October, 22)
	assert.NoError(t, sale.Validate())

	assert.True(t, entity.IsResultDateOnOrAfterEnd(*day(time.October, 22), end, london))
	assert.False(t, entity.IsResultDateOnOrAfterEnd(*day(time.October, 22), end, jst), "in Japan time the sale closes on the 23rd")
}

func TestTicketSale_CheckTimeZoneOfEvents(t *testing.T) {
	t.Parallel()

	tokyo, seoul := "Asia/Tokyo", "Asia/Seoul"
	events := []*entity.Event{
		{ID: "event-1", Venue: &entity.Venue{TimeZone: &tokyo}},
		{ID: "event-2", Venue: &entity.Venue{TimeZone: &seoul}},
		{ID: "event-3"},
	}
	sale := func(zone string, ids ...string) *entity.TicketSale {
		return entity.NewTicketSale("series-1", "n", entity.TicketSaleMethodFirstCome, zone, nov(1, 10, 0), nov(5, 10, 0), ids...)
	}

	assert.NoError(t, sale("Asia/Seoul", "event-1", "event-2").CheckTimeZoneOfEvents(events), "one of its events' zones")
	assert.ErrorIs(t, sale("Asia/Taipei", "event-1", "event-2").CheckTimeZoneOfEvents(events), apperr.ErrInvalidArgument)
	assert.ErrorIs(t, sale("Asia/Seoul", "event-1").CheckTimeZoneOfEvents(events), apperr.ErrInvalidArgument, "an event the sale does not name does not count")
	assert.ErrorIs(t, sale("Asia/Tokyo", "event-3").CheckTimeZoneOfEvents(events), apperr.ErrInvalidArgument, "a venue without a zone matches nothing")
}

func TestTicketSale_IsSameDiscoveredSaleAbroad(t *testing.T) {
	t.Parallel()

	// @spec components/entity/ticket-sale/upsert-discovered "Late-evening sale abroad keeps its date"
	taipei := wallZone(t, "Asia/Taipei")
	stored := discoveredSale(entity.TicketSaleMethodLottery, time.Date(2026, 11, 1, 23, 30, 0, 0, taipei), time.Date(2026, 11, 8, 23, 30, 0, 0, taipei))
	stored.TimeZone = "Asia/Taipei"
	assert.Equal(t, 2, stored.SaleStartTime.In(jst).Day(), "2 November in Japan time")

	again := &entity.DiscoveredTicketSale{
		SeriesID: "series-1", Method: entity.TicketSaleMethodLottery, TimeZone: "Asia/Taipei",
		SaleStartTime: time.Date(2026, 11, 1, 22, 30, 0, 0, taipei), SaleEndTime: time.Date(2026, 11, 8, 23, 30, 0, 0, taipei),
	}
	assert.True(t, stored.IsSameDiscoveredSale(again), "1 November in Taipei both times, though 2 and 1 November in Japan time")

	other := *again
	other.SaleStartTime = time.Date(2026, 11, 2, 0, 30, 0, 0, taipei)
	assert.False(t, stored.IsSameDiscoveredSale(&other), "another start date")

	otherMethod := *again
	otherMethod.Method = entity.TicketSaleMethodFirstCome
	assert.False(t, stored.IsSameDiscoveredSale(&otherMethod))

	merged := stored.MergeDiscovered(again)
	assert.True(t, merged.SaleStartTime.Equal(again.SaleStartTime), "the sale takes the corrected start")
	assert.Equal(t, "Asia/Taipei", merged.TimeZone)
}

func TestTicketSale_MergeDiscoveredResultDate(t *testing.T) {
	t.Parallel()

	// @spec components/entity/ticket-sale/upsert-discovered "A later end time clears an impossible result date"
	stored := discoveredSale(entity.TicketSaleMethodLottery, oct(5, 18, 0), oct(20, 23, 59))
	stored.ResultDate = day(time.October, 22)
	again := &entity.DiscoveredTicketSale{
		SeriesID: "series-1", Name: "オフィシャル先行", Method: entity.TicketSaleMethodLottery, TimeZone: "Asia/Tokyo",
		SaleStartTime: oct(5, 18, 0), SaleEndTime: oct(25, 23, 59),
	}

	merged := stored.MergeDiscovered(again)
	assert.Nil(t, merged.ResultDate, "the stored result date is before the new end date")
	assert.Equal(t, "オフィシャル先行", merged.Name)

	// @spec components/entity/ticket-sale/upsert-discovered "Re-discovery without a result date keeps the stored one"
	again.SaleEndTime = oct(21, 23, 59)
	merged = stored.MergeDiscovered(again)
	require.NotNil(t, merged.ResultDate)
	assert.True(t, merged.ResultDate.Equal(*day(time.October, 22)))

	// @spec components/entity/ticket-sale/upsert-discovered "Re-discovery adds a result date"
	again.ResultDate = day(time.November, 3)
	merged = stored.MergeDiscovered(again)
	assert.True(t, merged.ResultDate.Equal(*day(time.November, 3)))
}
