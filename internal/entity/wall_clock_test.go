package entity_test

import (
	"testing"
	"time"

	"github.com/pannpers/go-apperr/apperr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/liverty-music/backend/internal/entity"
)

func wallZone(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := entity.LoadZone(name)
	require.NoError(t, err)
	return loc
}

func localDay(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.UTC) }

func TestLocalInstant(t *testing.T) {
	t.Parallel()

	t.Run("Show in Seoul", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/event "Show in Seoul"
		got, err := entity.LocalInstant(localDay(2027, 1, 20), entity.TimeOfDay{Hour: 18, Minute: 30}, wallZone(t, "Asia/Seoul"))
		require.NoError(t, err)
		assert.Equal(t, time.Date(2027, 1, 20, 9, 30, 0, 0, time.UTC), got.UTC())
	})

	t.Run("Show in London", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/event "Show in London"
		got, err := entity.LocalInstant(localDay(2027, 7, 10), entity.TimeOfDay{Hour: 19}, wallZone(t, "Europe/London"))
		require.NoError(t, err)
		assert.Equal(t, time.Date(2027, 7, 10, 18, 0, 0, 0, time.UTC), got.UTC(), "BST is UTC+1")
		got, err = entity.LocalInstant(localDay(2027, 1, 10), entity.TimeOfDay{Hour: 19}, wallZone(t, "Europe/London"))
		require.NoError(t, err)
		assert.Equal(t, time.Date(2027, 1, 10, 19, 0, 0, 0, time.UTC), got.UTC())
	})

	t.Run("Start in the spring-forward gap", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/event "Start in the spring-forward gap"
		_, err := entity.LocalInstant(localDay(2027, 3, 14), entity.TimeOfDay{Hour: 2, Minute: 30}, wallZone(t, "America/New_York"))
		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
		got, err := entity.LocalInstant(localDay(2027, 3, 14), entity.TimeOfDay{Hour: 3}, wallZone(t, "America/New_York"))
		require.NoError(t, err)
		assert.Equal(t, time.Date(2027, 3, 14, 7, 0, 0, 0, time.UTC), got.UTC())
	})

	t.Run("Start in the repeated hour", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/event "Start in the repeated hour"
		got, err := entity.LocalInstant(localDay(2027, 11, 7), entity.TimeOfDay{Hour: 1, Minute: 30}, wallZone(t, "America/New_York"))
		require.NoError(t, err)
		assert.Equal(t, time.Date(2027, 11, 7, 5, 30, 0, 0, time.UTC), got.UTC(), "the first 01:30, in EDT")
	})

	t.Run("a zone without daylight saving", func(t *testing.T) {
		t.Parallel()
		got, err := entity.LocalInstant(localDay(2027, 3, 14), entity.TimeOfDay{Hour: 2, Minute: 30}, wallZone(t, "Asia/Tokyo"))
		require.NoError(t, err)
		assert.Equal(t, time.Date(2027, 3, 13, 17, 30, 0, 0, time.UTC), got.UTC())
	})
}

func TestLocalInstant_LordHoweHalfHourShift(t *testing.T) {
	t.Parallel()

	// Australia/Lord_Howe moves its clocks by 30 minutes: 02:00 to 02:30 in
	// October, so 02:15 does not exist on 2027-10-03.
	loc := wallZone(t, "Australia/Lord_Howe")
	_, err := entity.LocalInstant(localDay(2027, 10, 3), entity.TimeOfDay{Hour: 2, Minute: 15}, loc)
	assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
	_, err = entity.LocalInstant(localDay(2027, 10, 3), entity.TimeOfDay{Hour: 2, Minute: 45}, loc)
	assert.NoError(t, err)
}

func TestNewTimeOfDay(t *testing.T) {
	t.Parallel()

	got, err := entity.NewTimeOfDay(23, 59)
	require.NoError(t, err)
	assert.Equal(t, "23:59", got.String())
	for _, c := range [][2]int{{24, 0}, {-1, 0}, {0, 60}, {0, -1}} {
		_, err := entity.NewTimeOfDay(c[0], c[1])
		assert.ErrorIs(t, err, apperr.ErrInvalidArgument, "%v", c)
	}
}

func TestTimeOfDayUnmarshalTextStrict(t *testing.T) {
	t.Parallel()
	for _, text := range []string{"7:00", "7:00 PM", "19:00:30", "19:00junk", "24:00", "19", ""} {
		var got entity.TimeOfDay
		assert.ErrorIs(t, got.UnmarshalText([]byte(text)), apperr.ErrInvalidArgument, text)
	}
}

func TestLoadZone(t *testing.T) {
	t.Parallel()

	_, err := entity.LoadZone("")
	assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
	for _, name := range []string{"Mars/Olympus", "EST", "UTC", "Local", "JST", "+09:00"} {
		_, err = entity.LoadZone(name)
		assert.ErrorIs(t, err, apperr.ErrInvalidArgument, name)
	}
	loc, err := entity.LoadZone("Asia/Taipei")
	require.NoError(t, err)
	assert.Equal(t, "Asia/Taipei", loc.String())
}

func TestCivilTimeAndLocalDateIn(t *testing.T) {
	t.Parallel()

	taipei := wallZone(t, "Asia/Taipei")
	instant := time.Date(2026, 11, 1, 4, 0, 0, 0, time.UTC)
	assert.Equal(t, time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC), entity.CivilTime(instant, taipei))
	assert.Equal(t, localDay(2026, 11, 1), entity.LocalDateIn(time.Date(2026, 10, 31, 20, 0, 0, 0, time.UTC), taipei))
	back, err := entity.LocalDateTimeInstant(entity.CivilTime(instant, taipei), taipei)
	require.NoError(t, err)
	assert.True(t, instant.Equal(back))
}
