package rdb_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"uuid"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
)

// TestDiscoveredMergeParity checks that what the upsert of discovered events
// keeps on a natural-key conflict is what Event.MergeDiscovered says, for every
// combination of a known or unknown start time (the same on both, which is what
// makes the natural key conflict) and a known or unknown open time on the
// existing and the incoming event, and that a Series stored again under its
// content-addressed id keeps its first writer's row (Series.MergeDiscovered).
func TestDiscoveredMergeParity(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	ctx := context.Background()
	concerts := rdb.NewConcertRepository(db, testOutbox)
	seriesRepo := rdb.NewSeriesRepository(db, testOutbox)
	artistID := seedArtist(t, db, "merge-artist", uuid.NewV4().String())
	venueID := seedVenue(t, db, "merge-venue-"+uuid.NewV7().String())

	newSeries := func(title string) *entity.Series {
		return &entity.Series{ID: entity.NewID(), Title: title, Type: entity.SeriesTypeSingle}
	}
	start := time.Date(2027, 2, 1, 18, 0, 0, 0, time.UTC)
	openA, openB := start.Add(-time.Hour), start.Add(-90*time.Minute)

	day := 0
	for _, withStart := range []bool{false, true} {
		for _, existingOpen := range []*time.Time{nil, &openA} {
			for _, incomingOpen := range []*time.Time{nil, &openB} {
				day++
				date := time.Date(2027, 2, 1, 0, 0, 0, 0, time.UTC).AddDate(0, 0, day)
				var startTime *time.Time
				if withStart {
					startTime = &start
				}
				seriesID := newSeries("merge")
				_, err := seriesRepo.Create(ctx, seriesID)
				require.NoError(t, err)
				concertOf := func(open *time.Time) *entity.Concert {
					return &entity.Concert{
						Event: entity.Event{
							ID: entity.NewID(), SeriesID: seriesID.ID, VenueID: venueID,
							LocalDate: date, StartTime: startTime, OpenTime: open,
						},
						Artists: []*entity.Artist{{ID: artistID}},
					}
				}
				existing, incoming := concertOf(existingOpen), concertOf(incomingOpen)
				_, err = concerts.Create(ctx, existing)
				require.NoError(t, err)
				_, err = concerts.Create(ctx, incoming)
				require.NoError(t, err)

				wantStart, wantOpen := existing.MergeDiscovered(&incoming.Event)

				var gotStart, gotOpen *time.Time
				var rows int
				require.NoError(t, db.Pool.QueryRow(ctx,
					`SELECT count(*), min(start_at), min(open_at) FROM events WHERE venue_id = $1 AND local_event_date = $2`, venueID, date).Scan(&rows, &gotStart, &gotOpen))
				name := fmt.Sprintf("start known %v, existing open %v, incoming open %v", withStart, existingOpen != nil, incomingOpen != nil)
				assert.Equal(t, 1, rows, "one row stays: "+name)
				assertSameInstant(t, wantStart, gotStart, "start time: "+name)
				assertSameInstant(t, wantOpen, gotOpen, "open time: "+name)
			}
		}
	}

	t.Run("a series stored again under its id keeps its first writer's row", func(t *testing.T) {
		first := newSeries("first title")
		first.SourceURL = "https://example.test/first"
		_, err := seriesRepo.Create(ctx, first)
		require.NoError(t, err)
		later := &entity.Series{ID: first.ID, Title: "later title", Type: entity.SeriesTypeTour, SourceURL: "https://example.test/later"}

		inserted, err := seriesRepo.Create(ctx, later)

		require.NoError(t, err)
		assert.Empty(t, inserted, "nothing is inserted again")
		stored, err := seriesRepo.Get(ctx, first.ID)
		require.NoError(t, err)
		want := first.MergeDiscovered(later)
		assert.Equal(t, want.Title, stored.Title)
		assert.Equal(t, want.SourceURL, stored.SourceURL)
		assert.Equal(t, want.Type, stored.Type)
	})
}

func assertSameInstant(t *testing.T, want, got *time.Time, msg string) {
	t.Helper()
	if want == nil {
		assert.Nil(t, got, msg)
		return
	}
	if assert.NotNil(t, got, msg) {
		assert.True(t, want.Equal(*got), msg)
	}
}
