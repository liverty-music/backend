package rdb_test

import (
	"context"
	"testing"
	"time"

	"github.com/pannpers/go-apperr/apperr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
)

func TestEventStartTimeRepository_GetEventStartTime(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	repo := rdb.NewEventStartTimeRepository(db)

	// seed stores an event at a new venue of the area and zone on date,
	// starting at start (nil: not announced).
	seed := func(t *testing.T, area, zone, date string, start *entity.TimeOfDay) string {
		t.Helper()
		f := newVisibilityFixture(t, db)
		venueID := f.venue(area, nil, nil)
		f.exec(`UPDATE venues SET time_zone = $2 WHERE id = $1`, venueID, zone)
		day, err := time.Parse(time.DateOnly, date)
		require.NoError(t, err)
		eventID := f.eventOn(f.discoveredSeries(), venueID, day, "", f.artist())
		f.exec(`UPDATE events SET local_start_time = $2::text::time WHERE id = $1`, eventID, timeOfDayArg(start))
		return eventID
	}

	for _, tt := range []struct {
		name             string
		area, zone, date string
		start            *entity.TimeOfDay
	}{
		// @spec components/entity/event/get-event-start-time "Known start"
		{name: "known start", area: "JP-13", zone: "Asia/Tokyo", date: "2026-11-03", start: new(timeOfDayAt(18, 0))},
		// @spec components/entity/event/get-event-start-time "Show abroad"
		{name: "show abroad", area: "KR-11", zone: "Asia/Seoul", date: "2026-11-20", start: new(timeOfDayAt(18, 30))},
		// @spec components/entity/event/get-event-start-time "Start not announced"
		{name: "start not announced", area: "JP-13", zone: "Asia/Tokyo", date: "2026-11-03"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			eventID := seed(t, tt.area, tt.zone, tt.date, tt.start)

			got, err := repo.GetEventStartTime(context.Background(), eventID)

			require.NoError(t, err)
			assert.Equal(t, tt.date, got.LocalDate.Format(time.DateOnly))
			assert.Equal(t, tt.start, got.StartTime)
			assert.Equal(t, tt.zone, got.TimeZone)
		})
	}

	t.Run("unknown event", func(t *testing.T) {
		// @spec components/entity/event/get-event-start-time "Unknown event"
		_, err := repo.GetEventStartTime(context.Background(), entity.NewID())

		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})
}
