package entity_test

import (
	"strings"
	"testing"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/stretchr/testify/assert"
)

// nov returns 2026-11-day hh:mm in JST.
func nov(day, hh, mm int) time.Time {
	return time.Date(2026, 11, day, hh, mm, 0, 0, jst)
}

// saleWindow returns an Organizer's sale of the given method and window with
// a valid name and one ticket type.
func saleWindow(method entity.TicketSaleMethod, start, end time.Time) *entity.TicketSale {
	return namedSale("ファンクラブ先行", method, start, end)
}

// namedSale returns an Organizer's sale with the given name, method and
// window, offering one ticket type.
func namedSale(name string, method entity.TicketSaleMethod, start, end time.Time) *entity.TicketSale {
	sale := entity.NewTicketSale("series-1", name, method, start, end)
	entity.NewTicketType(sale, "event-1", 8000, 100, 4)
	return sale
}

// discoveredSale returns a discovered sale of the given method and window,
// discovered on 1 October.
func discoveredSale(method entity.TicketSaleMethod, start, end time.Time) *entity.TicketSale {
	discovered := time.Date(2026, 10, 1, 21, 0, 0, 0, jst)
	return &entity.TicketSale{
		ID:             "sale-1",
		SeriesID:       "series-1",
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
			assert.Error(t, err)
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
			assert.Error(t, err)
		})
	}
}

func TestTicketSale_Validate_TicketTypes(t *testing.T) {
	t.Parallel()

	t.Run("one ticket type per event", func(t *testing.T) {
		t.Parallel()
		sale := saleWindow(entity.TicketSaleMethodFirstCome, nov(1, 10, 0), nov(20, 18, 0))
		entity.NewTicketType(sale, "event-2", 8000, 100, 4)

		assert.NoError(t, sale.Validate())
	})

	t.Run("same event twice", func(t *testing.T) {
		t.Parallel()
		sale := saleWindow(entity.TicketSaleMethodFirstCome, nov(1, 10, 0), nov(20, 18, 0))
		entity.NewTicketType(sale, "event-1", 9000, 50, 4)

		assert.Error(t, sale.Validate())
	})

	t.Run("invalid ticket type", func(t *testing.T) {
		t.Parallel()
		sale := saleWindow(entity.TicketSaleMethodFirstCome, nov(1, 10, 0), nov(20, 18, 0))
		sale.TicketTypes[0].Price = 0

		assert.Error(t, sale.Validate())
	})

	t.Run("unknown method", func(t *testing.T) {
		t.Parallel()
		sale := saleWindow(entity.TicketSaleMethodUnspecified, nov(1, 10, 0), nov(20, 18, 0))

		assert.Error(t, sale.Validate())
	})
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

func TestTicketSale_IsDrawn(t *testing.T) {
	t.Parallel()

	t.Run("draw time set", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/ticket-sale "Draw time set"
		drawTime := nov(10, 10, 1)
		sale := saleWindow(entity.TicketSaleMethodLottery, nov(1, 10, 0), nov(10, 10, 0))
		sale.DrawTime = &drawTime

		assert.True(t, sale.IsDrawn())
	})

	t.Run("draw time absent", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/ticket-sale "Draw time absent"
		sale := saleWindow(entity.TicketSaleMethodLottery, nov(1, 10, 0), nov(10, 10, 0))

		assert.False(t, sale.IsDrawn())
	})
}

func TestTicketSale_TicketType(t *testing.T) {
	t.Parallel()

	sale := saleWindow(entity.TicketSaleMethodFirstCome, nov(1, 10, 0), nov(20, 18, 0))
	tt := entity.NewTicketType(sale, "event-1", 8000, 100, 4)

	assert.Same(t, tt, sale.TicketType(tt.ID))
	assert.Equal(t, sale.ID, tt.TicketSaleID)
	assert.Nil(t, sale.TicketType("unknown"))
}

// oct returns 2026-10-day hh:mm in JST.
func oct(day, hh, mm int) time.Time {
	return time.Date(2026, 10, day, hh, mm, 0, 0, jst)
}

func TestTicketSale_Validate_Discovered(t *testing.T) {
	t.Parallel()

	result := func(at time.Time) *entity.TicketSale {
		sale := discoveredSale(entity.TicketSaleMethodLottery, oct(5, 18, 0), oct(22, 23, 59))
		sale.LotteryResultTime = &at
		return sale
	}
	tests := []struct {
		name  string
		sale  func() *entity.TicketSale
		valid bool
	}{
		{
			// @spec components/entity/ticket-sale "Discovered lottery longer than 14 days"
			name: "discovered lottery longer than 14 days",
			sale: func() *entity.TicketSale {
				return discoveredSale(entity.TicketSaleMethodLottery, oct(1, 10, 0), oct(31, 23, 59))
			},
			valid: true,
		},
		{
			// @spec components/entity/ticket-sale "Discovered sale without a name"
			name: "discovered sale without a name",
			sale: func() *entity.TicketSale {
				return discoveredSale(entity.TicketSaleMethodFirstCome, oct(6, 19, 0), oct(8, 23, 59))
			},
			valid: true,
		},
		{
			name: "discovered sale with a name",
			sale: func() *entity.TicketSale {
				sale := discoveredSale(entity.TicketSaleMethodFirstCome, oct(6, 19, 0), time.Time{})
				sale.Name = "一般発売"
				return sale
			},
		},
		{
			// @spec components/entity/ticket-sale "Sale found by discovery"
			name: "sale found by discovery offers no ticket type",
			sale: func() *entity.TicketSale {
				return discoveredSale(entity.TicketSaleMethodLottery, oct(5, 18, 0), oct(22, 23, 59))
			},
			valid: true,
		},
		{
			// @spec components/entity/ticket-sale "Discovered sale with a TicketType"
			name: "discovered sale with a ticket type",
			sale: func() *entity.TicketSale {
				sale := discoveredSale(entity.TicketSaleMethodLottery, oct(5, 18, 0), oct(22, 23, 59))
				entity.NewTicketType(sale, "event-1", 8000, 100, 4)
				return sale
			},
		},
		{
			name: "discovered sale requiring verification",
			sale: func() *entity.TicketSale {
				sale := discoveredSale(entity.TicketSaleMethodLottery, oct(5, 18, 0), oct(22, 23, 59))
				sale.VerificationRequirement = entity.VerificationRequirementJPKIOnly
				return sale
			},
		},
		{
			name: "discovered sale with a draw time",
			sale: func() *entity.TicketSale {
				sale := discoveredSale(entity.TicketSaleMethodLottery, oct(5, 18, 0), oct(22, 23, 59))
				drawn := oct(23, 0, 1)
				sale.DrawTime = &drawn
				return sale
			},
		},
		{
			// @spec components/entity/ticket-sale "First-come sale until sold out"
			name: "first-come sale until sold out",
			sale: func() *entity.TicketSale {
				return discoveredSale(entity.TicketSaleMethodFirstCome, oct(6, 19, 0), time.Time{})
			},
			valid: true,
		},
		{
			// @spec components/entity/ticket-sale "Lottery without a close"
			name: "lottery without a close",
			sale: func() *entity.TicketSale {
				return discoveredSale(entity.TicketSaleMethodLottery, oct(5, 18, 0), time.Time{})
			},
		},
		{
			name:  "discovered lottery with a result after the close",
			sale:  func() *entity.TicketSale { return result(time.Date(2026, 11, 3, 15, 0, 0, 0, jst)) },
			valid: true,
		},
		{
			name:  "result at the close",
			sale:  func() *entity.TicketSale { return result(oct(22, 23, 59)) },
			valid: true,
		},
		{
			// @spec components/entity/ticket-sale "Result before close"
			name: "result before close",
			sale: func() *entity.TicketSale { return result(oct(22, 12, 0)) },
		},
		{
			// @spec components/entity/ticket-sale "Result on a first-come sale"
			name: "result on a first-come sale",
			sale: func() *entity.TicketSale {
				sale := discoveredSale(entity.TicketSaleMethodFirstCome, oct(6, 19, 0), time.Time{})
				at := oct(10, 12, 0)
				sale.LotteryResultTime = &at
				return sale
			},
		},
		{
			name: "no start time",
			sale: func() *entity.TicketSale {
				return discoveredSale(entity.TicketSaleMethodFirstCome, time.Time{}, time.Time{})
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := tt.sale().Validate()

			if tt.valid {
				assert.NoError(t, err)
				return
			}
			assert.Error(t, err)
		})
	}
}

func TestTicketSale_Validate_OrganizerKind(t *testing.T) {
	t.Parallel()

	t.Run("organizer's presale", func(t *testing.T) {
		// @spec components/entity/ticket-sale "Organizer's presale"
		t.Parallel()
		sale := saleWindow(entity.TicketSaleMethodLottery, nov(1, 10, 0), nov(10, 10, 0))
		entity.NewTicketType(sale, "event-2", 8000, 100, 4)

		assert.False(t, sale.IsDiscovered())
		assert.NoError(t, sale.Validate())
	})

	t.Run("organizer's sale without a ticket type", func(t *testing.T) {
		// @spec components/entity/ticket-sale "Organizer's sale without a TicketType"
		t.Parallel()
		sale := entity.NewTicketSale("series-1", "一般発売", entity.TicketSaleMethodFirstCome, nov(1, 10, 0), nov(20, 18, 0))

		assert.Error(t, sale.Validate())
	})

	t.Run("organizer's first-come sale without a close", func(t *testing.T) {
		// @spec components/entity/ticket-sale "Organizer's first-come sale without a close"
		t.Parallel()
		sale := saleWindow(entity.TicketSaleMethodFirstCome, nov(1, 10, 0), time.Time{})

		assert.Error(t, sale.Validate())
	})

	t.Run("organizer's lottery without a close", func(t *testing.T) {
		// @spec components/entity/ticket-sale "Lottery without a close"
		t.Parallel()
		sale := saleWindow(entity.TicketSaleMethodLottery, nov(1, 10, 0), time.Time{})

		assert.Error(t, sale.Validate())
	})

	t.Run("result time on an organizer's lottery", func(t *testing.T) {
		// @spec components/entity/ticket-sale "Result time on an Organizer's lottery"
		t.Parallel()
		sale := saleWindow(entity.TicketSaleMethodLottery, nov(1, 10, 0), nov(10, 10, 0))
		at := nov(12, 15, 0)
		sale.LotteryResultTime = &at

		assert.Error(t, sale.Validate())
	})
}

func TestTicketSale_ResultTime(t *testing.T) {
	t.Parallel()

	t.Run("discovered lottery with an announced result", func(t *testing.T) {
		// @spec components/entity/ticket-sale "Discovered lottery with an announced result"
		t.Parallel()
		sale := discoveredSale(entity.TicketSaleMethodLottery, oct(5, 18, 0), oct(22, 23, 59))
		announced := time.Date(2026, 11, 3, 15, 0, 0, 0, jst)
		sale.LotteryResultTime = &announced

		got, ok := sale.ResultTime()

		assert.True(t, ok)
		assert.True(t, announced.Equal(got))
	})

	t.Run("organizer's lottery", func(t *testing.T) {
		// @spec components/entity/ticket-sale "Organizer's lottery"
		t.Parallel()
		sale := saleWindow(entity.TicketSaleMethodLottery, oct(12, 18, 0), oct(22, 23, 59))

		got, ok := sale.ResultTime()

		assert.True(t, ok)
		assert.True(t, oct(22, 23, 59).Equal(got))
	})

	t.Run("discovered lottery without an announced result", func(t *testing.T) {
		// @spec components/entity/ticket-sale "Discovered lottery without an announced result"
		t.Parallel()
		sale := discoveredSale(entity.TicketSaleMethodLottery, oct(5, 18, 0), oct(22, 23, 59))

		_, ok := sale.ResultTime()

		assert.False(t, ok)
	})

	t.Run("first-come sale", func(t *testing.T) {
		t.Parallel()
		sale := saleWindow(entity.TicketSaleMethodFirstCome, oct(5, 18, 0), oct(22, 23, 59))

		_, ok := sale.ResultTime()

		assert.False(t, ok)
	})
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
		Method:        entity.TicketSaleMethodFirstCome,
		SaleStartTime: oct(6, 19, 0),
	}

	sale := found.TicketSale(first)

	assert.True(t, sale.IsDiscovered())
	assert.True(t, first.Equal(*sale.DiscoveredTime))
	assert.Nil(t, sale.LotteryResultTime)
	assert.NoError(t, found.Validate())
}

func TestTicketSale_IsNotYetOverAt(t *testing.T) {
	t.Parallel()

	t.Run("lottery before its close", func(t *testing.T) {
		// @spec components/entity/ticket-sale "Lottery before its close"
		t.Parallel()
		sale := discoveredSale(entity.TicketSaleMethodLottery, oct(5, 18, 0), oct(22, 23, 59))

		assert.True(t, sale.IsNotYetOverAt(oct(20, 12, 0)))
		assert.False(t, sale.IsNotYetOverAt(oct(22, 23, 59)))
	})

	t.Run("first-come sale until sold out has opened", func(t *testing.T) {
		// @spec components/entity/ticket-sale "First-come sale until sold out has opened"
		t.Parallel()
		sale := discoveredSale(entity.TicketSaleMethodFirstCome, oct(6, 18, 30), time.Time{})

		assert.False(t, sale.IsNotYetOverAt(oct(6, 18, 31)))
		assert.True(t, sale.IsNotYetOverAt(oct(6, 18, 29)))
	})
}
