package entity_test

import (
	"testing"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/stretchr/testify/assert"
)

func TestCurrentDateAtVenue(t *testing.T) {
	t.Parallel()

	date := func(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.UTC) }

	tests := []struct {
		name string
		// instant is the given time.
		instant time.Time
		// eventDate is the local date of an Event at the Venue.
		eventDate time.Time
		want      time.Time
		// wantPast is whether the Event is in the past at the instant.
		wantPast bool
	}{
		{
			// @spec components/entity/venue "Early morning counts as the new day"
			name:      "early morning at the venue counts as the new day while UTC is on the day before",
			instant:   time.Date(2026, 11, 10, 23, 30, 0, 0, time.UTC),
			eventDate: date(2026, 11, 10),
			want:      date(2026, 11, 11),
			wantPast:  true,
		},
		{
			// @spec components/entity/venue "Event day still today"
			name:      "the last minute of the event day at the venue is still that day",
			instant:   time.Date(2026, 11, 11, 14, 59, 0, 0, time.UTC),
			eventDate: date(2026, 11, 11),
			want:      date(2026, 11, 11),
			wantPast:  false,
		},
		{
			name:      "midnight at the venue starts the next day",
			instant:   time.Date(2026, 11, 11, 15, 0, 0, 0, time.UTC),
			eventDate: date(2026, 11, 11),
			want:      date(2026, 11, 12),
			wantPast:  true,
		},
		{
			name:      "an instant given in another zone falls on the same venue day",
			instant:   time.Date(2026, 11, 10, 18, 30, 0, 0, time.FixedZone("EST", -5*60*60)),
			eventDate: date(2026, 11, 10),
			want:      date(2026, 11, 11),
			wantPast:  true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := entity.CurrentDateAtVenue(tt.instant)

			assert.True(t, tt.want.Equal(got), "current date at the venue: want %s, got %s", tt.want, got)
			assert.Equal(t, time.UTC, got.Location(), "the date is at midnight UTC, like an Event's local date")
			event := &entity.Event{LocalDate: tt.eventDate}
			assert.Equal(t, tt.wantPast, event.HasPassedAt(tt.instant))
		})
	}
}

func TestVenueTimeZone(t *testing.T) {
	t.Parallel()

	// Every Venue is taken to be in Asia/Tokyo: 2026-11-11 00:00 there is
	// 2026-11-10 15:00 UTC.
	midnight := time.Date(2026, 11, 11, 0, 0, 0, 0, entity.VenueTimeZone())

	assert.Equal(t, "Asia/Tokyo", entity.VenueTimeZone().String())
	assert.True(t, time.Date(2026, 11, 10, 15, 0, 0, 0, time.UTC).Equal(midnight))
}

func TestTimeZoneOf(t *testing.T) {
	t.Parallel()

	// 2026-11-10 23:30 UTC, read in each zone.
	instant := time.Date(2026, 11, 10, 23, 30, 0, 0, time.UTC)

	tests := []struct {
		name     string
		zone     string
		wantName string
		wantHour int
	}{
		{name: "use a known IANA name", zone: "America/New_York", wantName: "America/New_York", wantHour: 18},
		{name: "fall back to Asia/Tokyo for an empty name", zone: "", wantName: "Asia/Tokyo", wantHour: 8},
		{name: "fall back to Asia/Tokyo for a blank name", zone: "  ", wantName: "Asia/Tokyo", wantHour: 8},
		{name: "fall back to Asia/Tokyo for an unknown name", zone: "Mars/Olympus_Mons", wantName: "Asia/Tokyo", wantHour: 8},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := entity.TimeZoneOf(tt.zone)

			assert.Equal(t, tt.wantName, got.String())
			assert.Equal(t, tt.wantHour, instant.In(got).Hour())
		})
	}
}
