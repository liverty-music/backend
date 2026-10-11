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

// saleWindow returns a sale of the given method and window with a valid name.
func saleWindow(method entity.TicketSaleMethod, start, end time.Time) *entity.TicketSale {
	return entity.NewTicketSale("series-1", "ファンクラブ先行", method, start, end)
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

			sale := entity.NewTicketSale("series-1", tt.saleName, entity.TicketSaleMethodFirstCome, nov(1, 10, 0), nov(20, 18, 0))
			err := sale.Validate()

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
		entity.NewTicketType(sale, "event-1", 8000, 100, 4)
		entity.NewTicketType(sale, "event-2", 8000, 100, 4)

		assert.NoError(t, sale.Validate())
	})

	t.Run("same event twice", func(t *testing.T) {
		t.Parallel()
		sale := saleWindow(entity.TicketSaleMethodFirstCome, nov(1, 10, 0), nov(20, 18, 0))
		entity.NewTicketType(sale, "event-1", 8000, 100, 4)
		entity.NewTicketType(sale, "event-1", 9000, 50, 4)

		assert.Error(t, sale.Validate())
	})

	t.Run("invalid ticket type", func(t *testing.T) {
		t.Parallel()
		sale := saleWindow(entity.TicketSaleMethodFirstCome, nov(1, 10, 0), nov(20, 18, 0))
		entity.NewTicketType(sale, "event-1", 0, 100, 4)

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

	t.Run("drawn time set", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/ticket-sale "Drawn time set"
		drawn := nov(10, 10, 1)
		sale := saleWindow(entity.TicketSaleMethodLottery, nov(1, 10, 0), nov(10, 10, 0))
		sale.DrawnTime = &drawn

		assert.True(t, sale.IsDrawn())
	})

	t.Run("drawn time absent", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/ticket-sale "Drawn time absent"
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
