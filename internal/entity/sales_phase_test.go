package entity_test

import (
	"testing"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/stretchr/testify/assert"
)

func TestSalesPhaseCandidate_Validate(t *testing.T) {
	t.Parallel()

	jst := time.FixedZone("JST", 9*60*60)
	open := time.Date(2026, 10, 5, 18, 0, 0, 0, jst)
	closeAt := time.Date(2026, 10, 22, 23, 59, 0, 0, jst)
	result := time.Date(2026, 11, 3, 15, 0, 0, 0, jst)

	tests := []struct {
		name    string
		c       *entity.SalesPhaseCandidate
		wantErr error
	}{
		{
			// @spec components/entity/sales-phase "Fan-club lottery"
			name: "Fan-club lottery",
			c:    &entity.SalesPhaseCandidate{SeriesID: "s", Method: entity.SalesMethodLottery, ApplyStartTime: open, ApplyEndTime: closeAt, LotteryResultTime: result},
		},
		{
			// @spec components/entity/sales-phase "No method"
			name:    "No method",
			c:       &entity.SalesPhaseCandidate{SeriesID: "s", ApplyStartTime: open, ApplyEndTime: closeAt},
			wantErr: assert.AnError,
		},
		{
			name:    "undefined method value",
			c:       &entity.SalesPhaseCandidate{SeriesID: "s", Method: 3, ApplyStartTime: open, ApplyEndTime: closeAt},
			wantErr: assert.AnError,
		},
		{
			// @spec components/entity/sales-phase "First-come sale until sold out"
			name: "First-come sale until sold out",
			c:    &entity.SalesPhaseCandidate{SeriesID: "s", Method: entity.SalesMethodFirstCome, ApplyStartTime: open},
		},
		{
			// @spec components/entity/sales-phase "Lottery without a close"
			name:    "Lottery without a close",
			c:       &entity.SalesPhaseCandidate{SeriesID: "s", Method: entity.SalesMethodLottery, ApplyStartTime: open},
			wantErr: assert.AnError,
		},
		{
			// @spec components/entity/sales-phase "No start time"
			name:    "No start time",
			c:       &entity.SalesPhaseCandidate{SeriesID: "s", Method: entity.SalesMethodFirstCome, ApplyEndTime: closeAt},
			wantErr: assert.AnError,
		},
		{
			// @spec components/entity/sales-phase "Close before open"
			name:    "Close before open",
			c:       &entity.SalesPhaseCandidate{SeriesID: "s", Method: entity.SalesMethodLottery, ApplyStartTime: closeAt, ApplyEndTime: open},
			wantErr: assert.AnError,
		},
		{
			// @spec components/entity/sales-phase "Result before close"
			name:    "Result before close",
			c:       &entity.SalesPhaseCandidate{SeriesID: "s", Method: entity.SalesMethodLottery, ApplyStartTime: open, ApplyEndTime: closeAt, LotteryResultTime: closeAt.Add(-time.Hour)},
			wantErr: assert.AnError,
		},
		{
			// @spec components/entity/sales-phase "Result on a first-come sale"
			name:    "Result on a first-come sale",
			c:       &entity.SalesPhaseCandidate{SeriesID: "s", Method: entity.SalesMethodFirstCome, ApplyStartTime: open, LotteryResultTime: result},
			wantErr: assert.AnError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := tt.c.Validate()
			if tt.wantErr != nil {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
		})
	}
}

func TestSalesPhase_HasApplicationEnded(t *testing.T) {
	t.Parallel()

	jst := time.FixedZone("JST", 9*60*60)

	tests := []struct {
		name  string
		phase *entity.SalesPhase
		now   time.Time
		want  bool
	}{
		{
			// @spec components/entity/sales-phase "Lottery before its close"
			name: "Lottery before its close",
			phase: &entity.SalesPhase{
				Method:         entity.SalesMethodLottery,
				ApplyStartTime: time.Date(2026, 10, 5, 18, 0, 0, 0, jst),
				ApplyEndTime:   time.Date(2026, 10, 22, 23, 59, 0, 0, jst),
			},
			now:  time.Date(2026, 10, 20, 12, 0, 0, 0, jst),
			want: false,
		},
		{
			name: "lottery at its close",
			phase: &entity.SalesPhase{
				Method:         entity.SalesMethodLottery,
				ApplyStartTime: time.Date(2026, 10, 5, 18, 0, 0, 0, jst),
				ApplyEndTime:   time.Date(2026, 10, 22, 23, 59, 0, 0, jst),
			},
			now:  time.Date(2026, 10, 22, 23, 59, 0, 0, jst),
			want: true,
		},
		{
			// @spec components/entity/sales-phase "First-come sale until sold out"
			name: "First-come sale until sold out has opened",
			phase: &entity.SalesPhase{
				Method:         entity.SalesMethodFirstCome,
				ApplyStartTime: time.Date(2026, 10, 6, 18, 30, 0, 0, jst),
			},
			now:  time.Date(2026, 10, 6, 18, 31, 0, 0, jst),
			want: true,
		},
		{
			name: "first-come sale until sold out before it opens",
			phase: &entity.SalesPhase{
				Method:         entity.SalesMethodFirstCome,
				ApplyStartTime: time.Date(2026, 10, 6, 18, 30, 0, 0, jst),
			},
			now:  time.Date(2026, 10, 6, 18, 0, 0, 0, jst),
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, tt.phase.HasApplicationEnded(tt.now))
		})
	}
}

func TestReminderStage_IsValid(t *testing.T) {
	t.Parallel()

	assert.True(t, entity.ReminderStageApplyOpen.IsValid())
	assert.True(t, entity.ReminderStageApplyClose24H.IsValid())
	assert.True(t, entity.ReminderStageResultDay.IsValid())

	// @spec components/entity/sales-phase-reminder "Undefined stage"
	t.Run("Undefined stage", func(t *testing.T) {
		t.Parallel()

		assert.False(t, entity.ReminderStage(3).IsValid())
		assert.False(t, entity.ReminderStage(0).IsValid())
	})
}

func TestSalesPhaseSearchLog_Validate(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)

	// @spec components/entity/sales-phase-search-log "Future searched time"
	t.Run("Future searched time", func(t *testing.T) {
		t.Parallel()

		l := &entity.SalesPhaseSearchLog{SeriesID: "s", SearchedTime: now.Add(time.Hour)}
		assert.Error(t, l.Validate(now))
	})

	t.Run("searched now is valid", func(t *testing.T) {
		t.Parallel()

		l := &entity.SalesPhaseSearchLog{SeriesID: "s", SearchedTime: now}
		assert.NoError(t, l.Validate(now))
	})
}
