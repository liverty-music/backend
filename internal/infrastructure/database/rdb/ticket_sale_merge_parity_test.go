package rdb_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
)

// TestDiscoveredTicketSaleMergeParity checks that the upsert of a discovered
// sale stores what TicketSale.MergeDiscovered says, and finds the stored sale
// by what TicketSale.IsSameDiscoveredSale says, for every combination of the
// stored result date (none, before, on and after the new end time's Japan-time
// day) and the new one (none or given).
func TestDiscoveredTicketSaleMergeParity(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	repo := rdb.NewTicketSaleRepository(db, testOutbox)
	ctx := context.Background()

	jst := time.FixedZone("JST", 9*60*60)
	start := time.Date(2026, 10, 5, 16, 0, 0, 0, jst)
	oldEnd := time.Date(2026, 10, 22, 23, 59, 0, 0, jst)
	newEnd := time.Date(2026, 10, 24, 23, 59, 0, 0, jst) // the new end time's day: 10-24
	day := func(d int) *time.Time {
		v := time.Date(2026, 10, d, 0, 0, 0, 0, time.UTC)
		return &v
	}
	storedDates := map[string]*time.Time{"none": nil, "before the new end day": day(23), "on the new end day": day(24), "after the new end day": day(25)}
	incomingDates := map[string]*time.Time{"none": nil, "given": day(27)}

	for storedName, stored := range storedDates {
		for incomingName, incoming := range incomingDates {
			name := fmt.Sprintf("stored %s, new %s", storedName, incomingName)
			seriesID := seedSeriesOnly(t, db, "Tour "+name)
			first := &entity.DiscoveredTicketSale{
				SeriesID: seriesID, Name: "先行抽選", Method: entity.TicketSaleMethodLottery,
				SaleStartTime: start, SaleEndTime: oldEnd, ResultDate: stored,
			}
			id, outcome := upsertDiscovered(t, repo, ctx, first)
			require.Equal(t, entity.UpsertOutcomeInserted, outcome, name)
			existing := first.TicketSale(time.Now())
			existing.ID = id

			// The same sale found again, its page corrected: another start time
			// on the same Japan-time day, a new end time and name.
			corrected := &entity.DiscoveredTicketSale{
				SeriesID: seriesID, Name: "先行抽選(受付延長)", Method: entity.TicketSaleMethodLottery,
				SaleStartTime: start.Add(2 * time.Hour), SaleEndTime: newEnd, ResultDate: incoming,
			}
			require.True(t, existing.IsSameDiscoveredSale(corrected), name)
			gotID, outcome := upsertDiscovered(t, repo, ctx, corrected)
			require.Equal(t, entity.UpsertOutcomeUpdated, outcome, name)
			assert.Equal(t, id, gotID, "the stored sale keeps its id: "+name)

			sales, err := repo.ListBySeries(ctx, seriesID)
			require.NoError(t, err)
			require.Len(t, sales, 1, name)
			want := existing.MergeDiscovered(corrected)
			assert.Equal(t, want.Name, sales[0].Name, name)
			assert.True(t, want.SaleStartTime.Equal(sales[0].SaleStartTime), "start: "+name)
			assert.True(t, want.SaleEndTime.Equal(sales[0].SaleEndTime), "end: "+name)
			if want.ResultDate == nil {
				assert.Nil(t, sales[0].ResultDate, "result date: "+name)
			} else if assert.NotNil(t, sales[0].ResultDate, "result date: "+name) {
				assert.True(t, want.ResultDate.Equal(*sales[0].ResultDate), "result date: "+name)
			}
		}
	}

	t.Run("another day, series or method is another sale", func(t *testing.T) {
		seriesID := seedSeriesOnly(t, db, "Identity")
		first := &entity.DiscoveredTicketSale{SeriesID: seriesID, Name: "n", Method: entity.TicketSaleMethodLottery, SaleStartTime: start, SaleEndTime: oldEnd}
		existing := first.TicketSale(time.Now())
		for name, other := range map[string]*entity.DiscoveredTicketSale{
			"another day":    {SeriesID: seriesID, Method: entity.TicketSaleMethodLottery, SaleStartTime: start.AddDate(0, 0, 1)},
			"another series": {SeriesID: "series-2", Method: entity.TicketSaleMethodLottery, SaleStartTime: start},
			"another method": {SeriesID: seriesID, Method: entity.TicketSaleMethodFirstCome, SaleStartTime: start},
		} {
			assert.False(t, existing.IsSameDiscoveredSale(other), name)
		}
		// 23:30 UTC is the next day in Japan.
		lateUTC := time.Date(2026, 10, 4, 15, 30, 0, 0, time.UTC) // 10-05 00:30 JST
		assert.True(t, existing.IsSameDiscoveredSale(&entity.DiscoveredTicketSale{SeriesID: seriesID, Method: entity.TicketSaleMethodLottery, SaleStartTime: lateUTC}), "the same Japan-time day")
		assert.False(t, (&entity.TicketSale{SeriesID: seriesID, Method: entity.TicketSaleMethodLottery, SaleStartTime: start}).IsSameDiscoveredSale(first), "a sale of an Organizer is never one found again")
	})
}
