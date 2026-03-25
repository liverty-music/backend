package entity_test

import (
	"testing"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/stretchr/testify/assert"
)

func TestSearchLog_IsFresh(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 3, 25, 12, 0, 0, 0, time.UTC)
	ttl := 24 * time.Hour

	tests := []struct {
		name string
		sl   *entity.SearchLog
		want bool
	}{
		{
			name: "completed within TTL is fresh",
			sl: &entity.SearchLog{
				Status:     entity.SearchLogStatusCompleted,
				SearchTime: now.Add(-1 * time.Hour),
			},
			want: true,
		},
		{
			name: "completed exactly at TTL boundary is not fresh",
			sl: &entity.SearchLog{
				Status:     entity.SearchLogStatusCompleted,
				SearchTime: now.Add(-ttl),
			},
			want: false,
		},
		{
			name: "completed beyond TTL is stale",
			sl: &entity.SearchLog{
				Status:     entity.SearchLogStatusCompleted,
				SearchTime: now.Add(-25 * time.Hour),
			},
			want: false,
		},
		{
			name: "pending status is never fresh",
			sl: &entity.SearchLog{
				Status:     entity.SearchLogStatusPending,
				SearchTime: now.Add(-1 * time.Minute),
			},
			want: false,
		},
		{
			name: "failed status is never fresh",
			sl: &entity.SearchLog{
				Status:     entity.SearchLogStatusFailed,
				SearchTime: now.Add(-1 * time.Minute),
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := tt.sl.IsFresh(now, ttl)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestSearchLog_IsPending(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 3, 25, 12, 0, 0, 0, time.UTC)
	timeout := 3 * time.Minute

	tests := []struct {
		name string
		sl   *entity.SearchLog
		want bool
	}{
		{
			name: "pending within timeout is active",
			sl: &entity.SearchLog{
				Status:     entity.SearchLogStatusPending,
				SearchTime: now.Add(-1 * time.Minute),
			},
			want: true,
		},
		{
			name: "pending exactly at timeout boundary is not active",
			sl: &entity.SearchLog{
				Status:     entity.SearchLogStatusPending,
				SearchTime: now.Add(-timeout),
			},
			want: false,
		},
		{
			name: "pending beyond timeout is stale",
			sl: &entity.SearchLog{
				Status:     entity.SearchLogStatusPending,
				SearchTime: now.Add(-10 * time.Minute),
			},
			want: false,
		},
		{
			name: "completed status is never pending",
			sl: &entity.SearchLog{
				Status:     entity.SearchLogStatusCompleted,
				SearchTime: now.Add(-1 * time.Minute),
			},
			want: false,
		},
		{
			name: "failed status is never pending",
			sl: &entity.SearchLog{
				Status:     entity.SearchLogStatusFailed,
				SearchTime: now.Add(-1 * time.Minute),
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := tt.sl.IsPending(now, timeout)
			assert.Equal(t, tt.want, got)
		})
	}
}
