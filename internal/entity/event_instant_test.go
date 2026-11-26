package entity_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/liverty-music/backend/internal/entity"
)

func TestEvent_StartAndOpenInstant(t *testing.T) {
	t.Parallel()

	eventAtZone := func(zone string, date time.Time, start, open *entity.TimeOfDay) *entity.Event {
		return &entity.Event{LocalDate: date, StartTime: start, OpenTime: open, Venue: &entity.Venue{TimeZone: &zone}}
	}

	t.Run("show in Seoul", func(t *testing.T) {
		t.Parallel()
		e := eventAtZone("Asia/Seoul", localDay(2027, 3, 14), &entity.TimeOfDay{Hour: 18, Minute: 30}, &entity.TimeOfDay{Hour: 17, Minute: 30})
		start, ok := e.StartInstant()
		require.True(t, ok)
		assert.True(t, start.Equal(time.Date(2027, 3, 14, 9, 30, 0, 0, time.UTC)))
		open, ok := e.OpenInstant()
		require.True(t, ok)
		assert.True(t, open.Equal(time.Date(2027, 3, 14, 8, 30, 0, 0, time.UTC)))
	})

	t.Run("show in London", func(t *testing.T) {
		t.Parallel()
		e := eventAtZone("Europe/London", localDay(2026, 11, 10), &entity.TimeOfDay{Hour: 19, Minute: 30}, nil)
		start, ok := e.StartInstant()
		require.True(t, ok)
		assert.True(t, start.Equal(time.Date(2026, 11, 10, 19, 30, 0, 0, time.UTC)))
		assert.Equal(t, 4, start.In(jst).Hour(), "04:30 in Japan time")
		_, ok = e.OpenInstant()
		assert.False(t, ok, "no open time")
	})

	t.Run("start in the spring-forward gap has no instant", func(t *testing.T) {
		t.Parallel()
		e := eventAtZone("America/New_York", localDay(2027, 3, 14), &entity.TimeOfDay{Hour: 2, Minute: 30}, nil)
		_, ok := e.StartInstant()
		assert.False(t, ok)
	})

	t.Run("start in the repeated hour is the earlier instant", func(t *testing.T) {
		t.Parallel()
		e := eventAtZone("America/New_York", localDay(2026, 11, 1), &entity.TimeOfDay{Hour: 1, Minute: 30}, nil)
		start, ok := e.StartInstant()
		require.True(t, ok)
		assert.True(t, start.Equal(time.Date(2026, 11, 1, 5, 30, 0, 0, time.UTC)))
	})

	t.Run("no instant without a start time, a Venue or a zone", func(t *testing.T) {
		t.Parallel()
		_, ok := eventAtZone("Asia/Tokyo", localDay(2026, 11, 3), nil, nil).StartInstant()
		assert.False(t, ok)
		_, ok = (&entity.Event{LocalDate: localDay(2026, 11, 3), StartTime: &entity.TimeOfDay{Hour: 18}}).StartInstant()
		assert.False(t, ok)
		_, ok = (&entity.Event{LocalDate: localDay(2026, 11, 3), StartTime: &entity.TimeOfDay{Hour: 18}, Venue: &entity.Venue{}}).StartInstant()
		assert.False(t, ok)
	})
}

func TestEventStart_Instant(t *testing.T) {
	t.Parallel()

	got, ok := entity.EventStart{LocalDate: localDay(2026, 11, 20), StartTime: &entity.TimeOfDay{Hour: 18, Minute: 30}, TimeZone: "Asia/Seoul"}.Instant()
	require.True(t, ok)
	assert.True(t, got.Equal(time.Date(2026, 11, 20, 9, 30, 0, 0, time.UTC)))

	_, ok = entity.EventStart{LocalDate: localDay(2026, 11, 20), TimeZone: "Asia/Seoul"}.Instant()
	assert.False(t, ok, "start not announced")
	_, ok = entity.EventStart{LocalDate: localDay(2026, 11, 20), StartTime: &entity.TimeOfDay{Hour: 18}, TimeZone: "KST"}.Instant()
	assert.False(t, ok, "an abbreviation is no zone")
}
