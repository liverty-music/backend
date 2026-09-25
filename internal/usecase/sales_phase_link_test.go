package usecase_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	entitymocks "github.com/liverty-music/backend/internal/entity/mocks"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/stretchr/testify/assert"
)

// timePtr returns a pointer to t, for building *time.Time fields inline.
func timePtr(t time.Time) *time.Time {
	return &t
}

func TestResolveSeriesLinkURL(t *testing.T) {
	t.Parallel()

	today := time.Now().UTC().Truncate(24 * time.Hour)
	yesterday := today.AddDate(0, 0, -1)
	tomorrow := today.AddDate(0, 0, 1)
	nextWeek := today.AddDate(0, 0, 7)

	type args struct {
		seriesID string
	}
	type dep struct {
		events []*entity.Event
		err    error
	}
	tests := []struct {
		name string
		args args
		dep  dep
		want string
	}{
		{
			name: "earliest upcoming event is chosen over a later upcoming event",
			args: args{seriesID: "series-1"},
			dep: dep{
				events: []*entity.Event{
					{ID: "event-later", LocalDate: nextWeek},
					{ID: "event-soon", LocalDate: tomorrow},
				},
			},
			want: "/concerts/event-soon",
		},
		{
			name: "past events are skipped in favor of the upcoming one",
			args: args{seriesID: "series-1"},
			dep: dep{
				events: []*entity.Event{
					{ID: "event-past", LocalDate: yesterday},
					{ID: "event-upcoming", LocalDate: tomorrow},
				},
			},
			want: "/concerts/event-upcoming",
		},
		{
			name: "no upcoming event falls back to the earliest event overall",
			args: args{seriesID: "series-1"},
			dep: dep{
				events: []*entity.Event{
					{ID: "event-recent", LocalDate: yesterday},
					{ID: "event-oldest", LocalDate: yesterday.AddDate(0, 0, -10)},
				},
			},
			want: "/concerts/event-oldest",
		},
		{
			name: "same date is broken by start time, unknown start time last",
			args: args{seriesID: "series-1"},
			dep: dep{
				events: []*entity.Event{
					{ID: "event-no-time", LocalDate: tomorrow},
					{ID: "event-earlier-time", LocalDate: tomorrow, StartTime: timePtr(tomorrow.Add(10 * time.Hour))},
					{ID: "event-later-time", LocalDate: tomorrow, StartTime: timePtr(tomorrow.Add(20 * time.Hour))},
				},
			},
			want: "/concerts/event-earlier-time",
		},
		{
			name: "no events falls back to the dashboard",
			args: args{seriesID: "series-1"},
			dep:  dep{events: nil},
			want: "/dashboard",
		},
		{
			name: "repository error falls back to the dashboard",
			args: args{seriesID: "series-1"},
			dep:  dep{err: errors.New("boom")},
			want: "/dashboard",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctx := context.Background()
			concertRepo := entitymocks.NewMockConcertRepository(t)
			concertRepo.EXPECT().
				ListEventsBySeries(ctx, tt.args.seriesID).
				Return(tt.dep.events, tt.dep.err).
				Once()

			got := usecase.ResolveSeriesLinkURL(ctx, tt.args.seriesID, concertRepo, newTestLogger(t))

			assert.Equal(t, tt.want, got)
		})
	}
}
