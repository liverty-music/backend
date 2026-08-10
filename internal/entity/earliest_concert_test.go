package entity_test

import (
	"testing"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/stretchr/testify/assert"
)

func TestEarliestConcert(t *testing.T) {
	t.Parallel()

	date := func(y int, m time.Month, d int) time.Time {
		return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
	}
	at := func(h, min int) *time.Time {
		v := time.Date(2000, 1, 1, h, min, 0, 0, time.UTC)
		return &v
	}

	sep3 := &entity.Concert{Event: entity.Event{ID: "sep3", LocalDate: date(2026, 9, 3)}}
	sep10 := &entity.Concert{Event: entity.Event{ID: "sep10", LocalDate: date(2026, 9, 10)}}

	sameDay1800 := &entity.Concert{Event: entity.Event{ID: "d-1800", LocalDate: date(2026, 9, 3), StartTime: at(18, 0)}}
	sameDay1930 := &entity.Concert{Event: entity.Event{ID: "d-1930", LocalDate: date(2026, 9, 3), StartTime: at(19, 30)}}
	sameDayNoStart := &entity.Concert{Event: entity.Event{ID: "d-none", LocalDate: date(2026, 9, 3)}}

	tieA := &entity.Concert{Event: entity.Event{ID: "aaa", LocalDate: date(2026, 9, 3)}}
	tieB := &entity.Concert{Event: entity.Event{ID: "bbb", LocalDate: date(2026, 9, 3)}}

	tests := []struct {
		name     string
		concerts []*entity.Concert
		want     *entity.Concert
	}{
		{
			name:     "empty slice returns nil",
			concerts: nil,
			want:     nil,
		},
		{
			name:     "slice of only nil elements returns nil",
			concerts: []*entity.Concert{nil, nil},
			want:     nil,
		},
		{
			name:     "earlier local date wins regardless of input order",
			concerts: []*entity.Concert{sep10, sep3},
			want:     sep3,
		},
		{
			name:     "same-day concerts tie-broken by earliest start time",
			concerts: []*entity.Concert{sameDay1930, sameDay1800},
			want:     sameDay1800,
		},
		{
			name:     "a known start time precedes an unknown one on the same date",
			concerts: []*entity.Concert{sameDayNoStart, sameDay1800},
			want:     sameDay1800,
		},
		{
			name:     "fully-tied concerts fall back to ascending ID",
			concerts: []*entity.Concert{tieB, tieA},
			want:     tieA,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Same(t, tt.want, entity.EarliestConcert(tt.concerts))
		})
	}
}

// TestEarliestConcert_HomeRecipientSubset covers the deep-link contract for a
// home-hype recipient: the deep-link target is the earliest concert of the
// recipient's *matched subset*, not the globally earliest new concert. Here the
// globally earliest concert is out-of-area (Osaka, 2026-09-01) and must NOT be
// chosen; the in-area Tokyo concert (2026-09-05) is the correct target.
func TestEarliestConcert_HomeRecipientSubset(t *testing.T) {
	t.Parallel()

	tokyoLevel1 := "JP-13"
	tokyoHome := &entity.Home{CountryCode: "JP", Level1: tokyoLevel1}
	osakaLevel1 := "JP-27"

	osakaEarlier := &entity.Concert{Event: entity.Event{
		ID:        "osaka-0901",
		LocalDate: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
		Venue:     &entity.Venue{AdminArea: &osakaLevel1},
	}}
	tokyoLater := &entity.Concert{Event: entity.Event{
		ID:        "tokyo-0905",
		LocalDate: time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC),
		Venue:     &entity.Venue{AdminArea: &tokyoLevel1},
	}}

	all := []*entity.Concert{osakaEarlier, tokyoLater}

	subset := entity.HypeHome.MatchingConcerts(tokyoHome, all)
	got := entity.EarliestConcert(subset)

	assert.Same(t, tokyoLater, got, "home recipient must deep-link to their in-area concert, not the globally earliest")
}
