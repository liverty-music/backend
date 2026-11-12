package entity_test

import (
	"strings"
	"testing"
	"time"

	"github.com/pannpers/go-apperr/apperr"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/stretchr/testify/assert"
)

// nov returns 2026-11-day hh:mm in JST.
func nov(day, hh, mm int) time.Time {
	return time.Date(2026, 11, day, hh, mm, 0, 0, jst)
}

// saleWindow returns an Organizer's sale of the given method and window with
// a valid name and one event.
func saleWindow(method entity.TicketSaleMethod, start, end time.Time) *entity.TicketSale {
	return namedSale("ファンクラブ先行", method, start, end)
}

// namedSale returns an Organizer's sale with the given name, method and
// window, naming one event.
func namedSale(name string, method entity.TicketSaleMethod, start, end time.Time) *entity.TicketSale {
	return entity.NewTicketSale("series-1", name, method, start, end, "event-1")
}

// discoveredSale returns a discovered sale of the given method and window,
// discovered on 1 October.
func discoveredSale(method entity.TicketSaleMethod, start, end time.Time) *entity.TicketSale {
	discovered := time.Date(2026, 10, 1, 21, 0, 0, 0, jst)
	return &entity.TicketSale{
		ID:             "sale-1",
		SeriesID:       "series-1",
		Name:           "オフィシャル先行",
		Method:         method,
		SaleStartTime:  start,
		SaleEndTime:    end,
		DiscoveredTime: &discovered,
	}
}

func TestTicketSale_Validate_Window(t *testing.T) {
	t.Parallel()

	start := nov(1, 10, 0)
	tests := []struct {
		name   string
		method entity.TicketSaleMethod
		end    time.Time
		valid  bool
	}{
		{
			// @spec components/entity/ticket-sale "Lottery window within bounds"
			name:   "lottery window within bounds",
			method: entity.TicketSaleMethodLottery,
			end:    start.Add(10 * 24 * time.Hour),
			valid:  true,
		},
		{
			name:   "lottery window of exactly 1 day",
			method: entity.TicketSaleMethodLottery,
			end:    start.Add(24 * time.Hour),
			valid:  true,
		},
		{
			name:   "lottery window of exactly 14 days",
			method: entity.TicketSaleMethodLottery,
			end:    start.Add(14 * 24 * time.Hour),
			valid:  true,
		},
		{
			// @spec components/entity/ticket-sale "Lottery window shorter than 1 day"
			name:   "lottery window shorter than 1 day",
			method: entity.TicketSaleMethodLottery,
			end:    start.Add(23 * time.Hour),
		},
		{
			// @spec components/entity/ticket-sale "Lottery window longer than 14 days"
			name:   "lottery window longer than 14 days",
			method: entity.TicketSaleMethodLottery,
			end:    start.Add(14*24*time.Hour + time.Hour),
		},
		{
			// @spec components/entity/ticket-sale "End not after start"
			name:   "end equals start",
			method: entity.TicketSaleMethodFirstCome,
			end:    start,
		},
		{
			name:   "end precedes start",
			method: entity.TicketSaleMethodLottery,
			end:    start.Add(-time.Hour),
		},
		{
			// @spec components/entity/ticket-sale "First-come window longer than 14 days"
			name:   "first-come window longer than 14 days",
			method: entity.TicketSaleMethodFirstCome,
			end:    nov(20, 18, 0),
			valid:  true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := saleWindow(tt.method, start, tt.end).Validate()

			if tt.valid {
				assert.NoError(t, err)
				return
			}
			assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
		})
	}
}

func TestTicketSale_Validate_Name(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		saleName string
		valid    bool
	}{
		{
			// @spec components/entity/ticket-sale "Named sale"
			name:     "named sale",
			saleName: "ファンクラブ先行",
			valid:    true,
		},
		{
			// @spec components/entity/ticket-sale "Empty name"
			name:     "empty name",
			saleName: "",
		},
		{
			name:     "name of 100 characters",
			saleName: strings.Repeat("名", 100),
			valid:    true,
		},
		{
			name:     "name of 101 characters",
			saleName: strings.Repeat("名", 101),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := namedSale(tt.saleName, entity.TicketSaleMethodFirstCome, nov(1, 10, 0), nov(20, 18, 0)).Validate()

			if tt.valid {
				assert.NoError(t, err)
				return
			}
			assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
		})
	}
}

func TestTicketSale_VerificationRequirement(t *testing.T) {
	t.Parallel()

	t.Run("no requirement given", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/ticket-sale "No requirement given"
		sale := saleWindow(entity.TicketSaleMethodLottery, nov(1, 10, 0), nov(10, 10, 0))

		assert.Equal(t, entity.VerificationRequirementNone, sale.VerificationRequirement)
		assert.False(t, sale.VerificationRequirement.RequiresVerification())
	})

	t.Run("verified-any or JPKI-only", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/ticket-sale "Verified-any or JPKI-only"
		for _, req := range []entity.VerificationRequirement{
			entity.VerificationRequirementVerifiedAny,
			entity.VerificationRequirementJPKIOnly,
		} {
			assert.True(t, req.RequiresVerification(), "requirement %d", req)
		}
	})
}

func TestTicketSale_IsOpenAt(t *testing.T) {
	t.Parallel()

	sale := saleWindow(entity.TicketSaleMethodLottery, nov(1, 10, 0), nov(10, 10, 0))
	tests := []struct {
		name string
		at   time.Time
		want bool
	}{
		// @spec components/entity/ticket-sale "Before start"
		{name: "before start", at: nov(1, 9, 59), want: false},
		// @spec components/entity/ticket-sale "Between start and end"
		{name: "at the start time", at: nov(1, 10, 0), want: true},
		{name: "between start and end", at: nov(5, 12, 0), want: true},
		// @spec components/entity/ticket-sale "At end"
		{name: "at the end time", at: nov(10, 10, 0), want: false},
		{name: "after the end time", at: nov(10, 10, 1), want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, sale.IsOpenAt(tt.at))
		})
	}
}

func TestTicketSale_Overlaps(t *testing.T) {
	t.Parallel()

	t.Run("back to back", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/ticket-sale "Back to back"
		first := saleWindow(entity.TicketSaleMethodLottery, nov(1, 10, 0), nov(10, 23, 59))
		second := saleWindow(entity.TicketSaleMethodFirstCome, nov(10, 23, 59), nov(20, 18, 0))

		assert.False(t, first.Overlaps(second))
		assert.False(t, second.Overlaps(first))
	})

	t.Run("overlapping", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/ticket-sale "Overlapping"
		first := saleWindow(entity.TicketSaleMethodLottery, nov(1, 0, 0), nov(10, 0, 0))
		second := saleWindow(entity.TicketSaleMethodLottery, nov(8, 0, 0), nov(20, 0, 0))

		assert.True(t, first.Overlaps(second))
		assert.True(t, second.Overlaps(first))
	})
}

// oct returns 2026-10-day hh:mm in JST.
func oct(day, hh, mm int) time.Time {
	return time.Date(2026, 10, day, hh, mm, 0, 0, jst)
}

func TestDiscoveredTicketSale_TicketSale(t *testing.T) {
	t.Parallel()

	// @spec components/entity/ticket-sale "Re-discovery keeps the discovered time"
	// The discovered time is the one given when the sale is first stored; a
	// later discovery only carries the sale's values, never a discovered time,
	// and UpsertDiscovered keeps the stored one (rdb contract test).
	first := time.Date(2026, 6, 1, 21, 0, 0, 0, jst)
	found := &entity.DiscoveredTicketSale{
		SeriesID:      "series-1",
		Name:          "一般発売",
		Method:        entity.TicketSaleMethodFirstCome,
		SaleStartTime: oct(6, 19, 0),
		SaleEndTime:   oct(10, 23, 59),
	}

	sale := found.TicketSale(first)

	assert.True(t, sale.IsDiscovered())
	assert.True(t, first.Equal(*sale.DiscoveredTime))
	assert.NoError(t, found.Validate(first))
}

func TestTicketSale_Validate_Kind(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		sale  func() *entity.TicketSale
		valid bool
	}{
		{
			// @spec components/entity/ticket-sale "Organizer's presale"
			name: "Organizer's sale naming two events",
			sale: func() *entity.TicketSale {
				return entity.NewTicketSale("series-1", "FC先行", entity.TicketSaleMethodLottery, nov(1, 10, 0), nov(10, 10, 0), "event-1", "event-2")
			},
			valid: true,
		},
		{
			// @spec components/entity/ticket-sale "Organizer's sale without an event"
			name: "Organizer's sale naming no event",
			sale: func() *entity.TicketSale {
				return entity.NewTicketSale("series-1", "FC先行", entity.TicketSaleMethodLottery, nov(1, 10, 0), nov(10, 10, 0))
			},
		},
		{
			name: "Organizer's sale naming an event twice",
			sale: func() *entity.TicketSale {
				return entity.NewTicketSale("series-1", "FC先行", entity.TicketSaleMethodLottery, nov(1, 10, 0), nov(10, 10, 0), "event-1", "event-1")
			},
		},
		{
			// @spec components/entity/ticket-sale "Sale found by discovery"
			// @spec components/entity/ticket-sale "Discovered lottery longer than 14 days"
			name: "discovered sale naming no event",
			sale: func() *entity.TicketSale {
				return discoveredSale(entity.TicketSaleMethodLottery, oct(1, 10, 0), oct(31, 23, 59))
			},
			valid: true,
		},
		{
			// @spec components/entity/ticket-sale "Discovered sale naming an event"
			name: "discovered sale naming an event",
			sale: func() *entity.TicketSale {
				s := discoveredSale(entity.TicketSaleMethodLottery, oct(1, 10, 0), oct(31, 23, 59))
				s.EventIDs = []string{"event-1"}
				return s
			},
		},
		{
			name: "discovered sale with a verification requirement",
			sale: func() *entity.TicketSale {
				s := discoveredSale(entity.TicketSaleMethodLottery, oct(1, 10, 0), oct(31, 23, 59))
				s.VerificationRequirement = entity.VerificationRequirementJPKIOnly
				return s
			},
		},
		{
			// @spec components/entity/ticket-sale "Discovered sale with a name"
			name: "discovered sale named オフィシャル先行",
			sale: func() *entity.TicketSale {
				return discoveredSale(entity.TicketSaleMethodFirstCome, oct(1, 10, 0), oct(5, 18, 0))
			},
			valid: true,
		},
		{
			name: "discovered sale without a name",
			sale: func() *entity.TicketSale {
				s := discoveredSale(entity.TicketSaleMethodFirstCome, oct(1, 10, 0), oct(5, 18, 0))
				s.Name = ""
				return s
			},
		},
		{
			// @spec components/entity/ticket-sale "Both times"
			name: "start and end time",
			sale: func() *entity.TicketSale {
				return saleWindow(entity.TicketSaleMethodFirstCome, nov(1, 10, 0), nov(20, 18, 0))
			},
			valid: true,
		},
		{
			// @spec components/entity/ticket-sale "Lottery without a close"
			name: "lottery without an end time",
			sale: func() *entity.TicketSale {
				return saleWindow(entity.TicketSaleMethodLottery, nov(1, 10, 0), time.Time{})
			},
		},
		{
			// @spec components/entity/ticket-sale "First-come sale without a close"
			name: "Organizer's first-come sale without an end time",
			sale: func() *entity.TicketSale {
				return saleWindow(entity.TicketSaleMethodFirstCome, nov(1, 10, 0), time.Time{})
			},
		},
		{
			// @spec components/entity/ticket-sale "First-come sale without a close"
			name: "discovered first-come sale without an end time",
			sale: func() *entity.TicketSale {
				return discoveredSale(entity.TicketSaleMethodFirstCome, oct(1, 10, 0), time.Time{})
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := tt.sale().Validate()
			if tt.valid {
				assert.NoError(t, err)
			} else {
				assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
			}
		})
	}
}

func TestTicketSale_CheckEventsOfSeries(t *testing.T) {
	t.Parallel()

	sale := entity.NewTicketSale("series-1", "FC先行", entity.TicketSaleMethodLottery, nov(1, 10, 0), nov(10, 10, 0), "event-1", "event-2")

	t.Run("events of its Series", func(t *testing.T) {
		t.Parallel()
		assert.NoError(t, sale.CheckEventsOfSeries([]*entity.Event{
			{ID: "event-1", SeriesID: "series-1"}, {ID: "event-2", SeriesID: "series-1"},
		}))
	})

	t.Run("event of another Series", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/ticket-sale "Event of another Series"
		assert.ErrorIs(t, sale.CheckEventsOfSeries([]*entity.Event{
			{ID: "event-1", SeriesID: "series-1"}, {ID: "event-2", SeriesID: "series-2"},
		}), apperr.ErrInvalidArgument)
	})
}

func TestTicketSale_IsDrawnAt(t *testing.T) {
	t.Parallel()

	sale := saleWindow(entity.TicketSaleMethodLottery, nov(1, 10, 0), nov(10, 10, 0))
	tests := []struct {
		name    string
		at      time.Time
		entered int
		want    bool
	}{
		// @spec components/entity/ticket-sale "Closed with entries still Entered"
		{name: "closed with an Entered entry", at: nov(10, 10, 1), entered: 1},
		// @spec components/entity/ticket-sale "Every entry decided"
		{name: "closed with every entry Won or Lost", at: nov(10, 10, 1), want: true},
		// @spec components/entity/ticket-sale "Closed without entries"
		{name: "closed without entries", at: nov(10, 10, 0), want: true},
		// @spec components/entity/ticket-sale "Still open"
		{name: "before the end time", at: nov(10, 9, 59)},
	}
	t.Run("discovered lottery is never drawn", func(t *testing.T) {
		t.Parallel()
		assert.False(t, discoveredSale(entity.TicketSaleMethodLottery, oct(1, 10, 0), oct(5, 18, 0)).IsDrawnAt(oct(6, 0, 0), 0))
	})
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, sale.IsDrawnAt(tt.at, tt.entered))
		})
	}
}

func TestTicketSale_IsNotYetOverAt(t *testing.T) {
	t.Parallel()

	t.Run("lottery before its close", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/ticket-sale "Lottery before its close"
		sale := saleWindow(entity.TicketSaleMethodLottery, nov(15, 10, 0), nov(22, 23, 59))
		assert.True(t, sale.IsNotYetOverAt(nov(20, 12, 0)))
	})

	t.Run("first-come sale at its close", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/ticket-sale "First-come sale after its close"
		sale := saleWindow(entity.TicketSaleMethodFirstCome, nov(15, 10, 0), nov(20, 20, 0))
		assert.False(t, sale.IsNotYetOverAt(nov(20, 20, 0)))
	})
}

// day returns the calendar day 2026-month-d in the form of
// TicketSale.ResultDate.
func day(month time.Month, d int) *time.Time {
	t := time.Date(2026, month, d, 0, 0, 0, 0, time.UTC)
	return &t
}

func TestTicketSale_Validate_ResultDate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		sale  func() *entity.TicketSale
		valid bool
	}{
		{
			// @spec components/entity/ticket-sale "Discovered lottery with a result date"
			name: "discovered lottery with a result date after its close",
			sale: func() *entity.TicketSale {
				s := discoveredSale(entity.TicketSaleMethodLottery, oct(5, 18, 0), oct(22, 23, 59))
				s.ResultDate = day(time.November, 3)
				return s
			},
			valid: true,
		},
		{
			name: "discovered lottery with a result date on the day of its close",
			sale: func() *entity.TicketSale {
				s := discoveredSale(entity.TicketSaleMethodLottery, oct(5, 18, 0), oct(22, 23, 59))
				s.ResultDate = day(time.October, 22)
				return s
			},
			valid: true,
		},
		{
			// @spec components/entity/ticket-sale "Result date before the close"
			name: "discovered lottery with a result date before its close",
			sale: func() *entity.TicketSale {
				s := discoveredSale(entity.TicketSaleMethodLottery, oct(5, 18, 0), oct(22, 23, 59))
				s.ResultDate = day(time.October, 20)
				return s
			},
		},
		{
			// @spec components/entity/ticket-sale "First-come sale with a result date"
			name: "discovered first-come sale with a result date",
			sale: func() *entity.TicketSale {
				s := discoveredSale(entity.TicketSaleMethodFirstCome, oct(6, 19, 0), oct(10, 23, 59))
				s.ResultDate = day(time.November, 3)
				return s
			},
		},
		{
			// @spec components/entity/ticket-sale "Organizer's lottery with a result date"
			name: "Organizer's lottery with a result date",
			sale: func() *entity.TicketSale {
				s := saleWindow(entity.TicketSaleMethodLottery, oct(5, 18, 0), oct(12, 23, 59))
				s.ResultDate = day(time.November, 3)
				return s
			},
		},
		{
			// @spec components/entity/ticket-sale "Discovered lottery without a result date"
			name: "discovered lottery without a result date",
			sale: func() *entity.TicketSale {
				return discoveredSale(entity.TicketSaleMethodLottery, oct(5, 18, 0), oct(22, 23, 59))
			},
			valid: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := tt.sale().Validate()
			if tt.valid {
				assert.NoError(t, err)
			} else {
				assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
			}
		})
	}
}

func TestTicketSale_IsLotteryClosedAt(t *testing.T) {
	t.Parallel()

	start, end := nov(1, 10, 0), nov(10, 10, 0)
	lottery := entity.NewTicketSale("series-1", "FC先行", entity.TicketSaleMethodLottery, start, end, "event-1")
	firstCome := entity.NewTicketSale("series-1", "一般", entity.TicketSaleMethodFirstCome, start, end, "event-1")

	assert.False(t, lottery.IsLotteryClosedAt(end.Add(-time.Second)), "open")
	assert.True(t, lottery.IsLotteryClosedAt(end), "closed at the end time")
	assert.True(t, lottery.IsLotteryClosedAt(end.Add(time.Hour)))
	assert.False(t, firstCome.IsLotteryClosedAt(end.Add(time.Hour)), "a first-come sale is no lottery")
}

func TestTicketSale_CheckDrawnFor(t *testing.T) {
	t.Parallel()

	sale := entity.NewTicketSale("series-1", "FC先行", entity.TicketSaleMethodLottery, nov(1, 10, 0), nov(10, 10, 0), "event-1")
	assert.NoError(t, sale.CheckDrawnFor(0), "no Entered entry is left for the stock")
	assert.ErrorIs(t, sale.CheckDrawnFor(1), apperr.ErrFailedPrecondition)
}

func TestTicketSale_CheckCreate(t *testing.T) {
	t.Parallel()

	start := nov(20, 19, 0)
	facts := func(published bool, start *time.Time, stock bool) entity.TicketSaleEventFacts {
		return entity.TicketSaleEventFacts{Published: published, StartTime: start, HasStock: stock}
	}
	good := func() entity.TicketSaleCreateSnapshot {
		return entity.TicketSaleCreateSnapshot{
			SeriesEvents:                []*entity.Event{{ID: "event-1", SeriesID: "series-1"}},
			EventFacts:                  map[string]entity.TicketSaleEventFacts{"event-1": facts(true, &start, true)},
			OrganizerHasBusinessDetails: true,
		}
	}
	lottery := func() *entity.TicketSale {
		return entity.NewTicketSale("series-1", "FC先行", entity.TicketSaleMethodLottery, nov(1, 10, 0), nov(11, 10, 0), "event-1")
	}

	tests := []struct {
		name    string
		sale    func() *entity.TicketSale
		mutate  func(*entity.TicketSaleCreateSnapshot)
		wantErr error
	}{
		// @spec components/entity/ticket-sale "Create"
		{"valid", lottery, func(*entity.TicketSaleCreateSnapshot) {}, nil},
		{"event of another series", lottery, func(s *entity.TicketSaleCreateSnapshot) { s.SeriesEvents[0].SeriesID = "series-2" }, apperr.ErrInvalidArgument},
		{"event not published", lottery, func(s *entity.TicketSaleCreateSnapshot) { s.EventFacts["event-1"] = facts(false, &start, true) }, apperr.ErrFailedPrecondition},
		{"start time not announced", lottery, func(s *entity.TicketSaleCreateSnapshot) { s.EventFacts["event-1"] = facts(true, nil, true) }, apperr.ErrFailedPrecondition},
		{"event without a stock", lottery, func(s *entity.TicketSaleCreateSnapshot) { s.EventFacts["event-1"] = facts(true, &start, false) }, apperr.ErrFailedPrecondition},
		{"lottery ending at the start", func() *entity.TicketSale {
			return entity.NewTicketSale("series-1", "FC先行", entity.TicketSaleMethodLottery, nov(1, 10, 0), start, "event-1")
		}, func(*entity.TicketSaleCreateSnapshot) {}, apperr.ErrFailedPrecondition},
		{"first-come ending at the start", func() *entity.TicketSale {
			return entity.NewTicketSale("series-1", "一般", entity.TicketSaleMethodFirstCome, nov(1, 10, 0), start, "event-1")
		}, func(*entity.TicketSaleCreateSnapshot) {}, nil},
		{"first-come ending after the start", func() *entity.TicketSale {
			return entity.NewTicketSale("series-1", "一般", entity.TicketSaleMethodFirstCome, nov(1, 10, 0), start.Add(time.Minute), "event-1")
		}, func(*entity.TicketSaleCreateSnapshot) {}, apperr.ErrFailedPrecondition},
		{"business details missing", lottery, func(s *entity.TicketSaleCreateSnapshot) { s.OrganizerHasBusinessDetails = false }, apperr.ErrFailedPrecondition},
		{"window overlaps another sale", lottery, func(s *entity.TicketSaleCreateSnapshot) {
			s.OtherSales = []*entity.TicketSale{entity.NewTicketSale("series-1", "x", entity.TicketSaleMethodFirstCome, nov(5, 10, 0), nov(15, 10, 0), "event-1")}
		}, apperr.ErrFailedPrecondition},
		{"back-to-back with another sale", lottery, func(s *entity.TicketSaleCreateSnapshot) {
			s.OtherSales = []*entity.TicketSale{entity.NewTicketSale("series-1", "x", entity.TicketSaleMethodFirstCome, nov(11, 10, 0), nov(15, 10, 0), "event-1")}
		}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			snapshot := good()
			tt.mutate(&snapshot)
			err := tt.sale().CheckCreate(snapshot)
			if tt.wantErr == nil {
				assert.NoError(t, err)
				return
			}
			assert.ErrorIs(t, err, tt.wantErr)
		})
	}

	t.Run("the sale's own stored window is not compared", func(t *testing.T) {
		t.Parallel()
		sale := lottery()
		snapshot := good()
		snapshot.OtherSales = []*entity.TicketSale{sale}
		assert.NoError(t, sale.CheckCreate(snapshot))
	})
}

func TestTicketSale_IsOpenFirstComeAt(t *testing.T) {
	t.Parallel()

	firstCome := entity.NewTicketSale("s", "n", entity.TicketSaleMethodFirstCome, nov(1, 10, 0), nov(10, 10, 0), "e")
	lottery := entity.NewTicketSale("s", "n", entity.TicketSaleMethodLottery, nov(1, 10, 0), nov(10, 10, 0), "e")
	assert.True(t, firstCome.IsOpenFirstComeAt(nov(5, 0, 0)))
	assert.False(t, firstCome.IsOpenFirstComeAt(nov(10, 10, 0)), "closed at the end time")
	assert.False(t, lottery.IsOpenFirstComeAt(nov(5, 0, 0)), "a lottery sale is not first-come")
}
