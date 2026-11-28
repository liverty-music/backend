package entity_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/liverty-music/backend/internal/entity"
)

func TestRejectedConcert_SameListing(t *testing.T) {
	t.Parallel()

	const artistID = "019b0000-0000-7000-8000-000000000001"
	day := time.Date(2027, 1, 25, 0, 0, 0, 0, time.UTC)
	entry := func(venue string, start *entity.TimeOfDay) *entity.RejectedConcert {
		return &entity.RejectedConcert{ArtistID: artistID, LocalDate: day, ListedVenueName: venue, StartTime: start}
	}
	found := func(venue string, start *entity.TimeOfDay) *entity.DiscoveredEvent {
		return &entity.DiscoveredEvent{LocalDate: day, ListedVenueName: venue, StartTime: start}
	}

	tests := []struct {
		name     string
		entry    *entity.RejectedConcert
		artistID string
		event    *entity.DiscoveredEvent
		want     bool
	}{
		{
			// @spec components/entity/rejected-concert "Identical listing"
			name:     "identical listing",
			entry:    entry("【振替公演】仙台GIGS", tod(19, 0)),
			artistID: artistID,
			event:    found("【振替公演】仙台GIGS", tod(19, 0)),
			want:     true,
		},
		{
			// @spec components/entity/rejected-concert "Name differs only in spacing or case"
			name:     "name differs only in spacing or case",
			entry:    entry("Zepp Haneda", nil),
			artistID: artistID,
			event:    found("ZEPP HANEDA", nil),
			want:     true,
		},
		{
			// @spec components/entity/rejected-concert "Start time announced later"
			name:     "start time announced later",
			entry:    entry("Zepp Haneda", nil),
			artistID: artistID,
			event:    found("Zepp Haneda", tod(18, 0)),
			want:     false,
		},
		{
			// @spec components/entity/rejected-concert "Another venue"
			name:     "another venue",
			entry:    entry("Zepp Namba", nil),
			artistID: artistID,
			event:    found("Zepp Osaka Bayside", nil),
			want:     false,
		},
		{
			// @spec components/entity/rejected-concert "Another Artist"
			name:     "another artist",
			entry:    entry("Zepp Haneda", tod(19, 0)),
			artistID: "019b0000-0000-7000-8000-000000000002",
			event:    found("Zepp Haneda", tod(19, 0)),
			want:     false,
		},
		{
			name:     "another date",
			entry:    entry("Zepp Haneda", tod(19, 0)),
			artistID: artistID,
			event:    &entity.DiscoveredEvent{LocalDate: day.AddDate(0, 0, 1), ListedVenueName: "Zepp Haneda", StartTime: tod(19, 0)},
			want:     false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, tt.entry.SameListing(tt.artistID, tt.event))
		})
	}
}

func TestDropRejected(t *testing.T) {
	t.Parallel()

	const artistID = "019b0000-0000-7000-8000-000000000001"
	day := time.Date(2027, 1, 25, 0, 0, 0, 0, time.UTC)
	rejected := []*entity.RejectedConcert{{ArtistID: artistID, LocalDate: day, ListedVenueName: "【振替公演】仙台GIGS", StartTime: tod(19, 0)}}

	t.Run("drops the same listing and keeps a corrected one", func(t *testing.T) {
		t.Parallel()
		series := []*entity.DiscoveredSeries{
			{Title: "Tour", Events: []*entity.DiscoveredEvent{
				{LocalDate: day, ListedVenueName: "【振替公演】仙台GIGS", StartTime: tod(19, 0)},
				{LocalDate: day, ListedVenueName: "【振替公演】仙台GIGS", StartTime: tod(18, 0)},
			}},
		}

		got := entity.DropRejected(artistID, series, rejected)

		require.Len(t, got, 1)
		require.Len(t, got[0].Events, 1)
		assert.Equal(t, tod(18, 0), got[0].Events[0].StartTime)
	})

	t.Run("removes a series left with no events", func(t *testing.T) {
		t.Parallel()
		series := []*entity.DiscoveredSeries{
			{Title: "Gone", Events: []*entity.DiscoveredEvent{{LocalDate: day, ListedVenueName: "【振替公演】仙台GIGS", StartTime: tod(19, 0)}}},
			{Title: "Kept", Events: []*entity.DiscoveredEvent{{LocalDate: day, ListedVenueName: "仙台GIGS", StartTime: tod(19, 0)}}},
		}

		got := entity.DropRejected(artistID, series, rejected)

		require.Len(t, got, 1)
		assert.Equal(t, "Kept", got[0].Title)
	})
}
