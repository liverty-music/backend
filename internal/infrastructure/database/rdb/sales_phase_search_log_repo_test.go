package rdb_test

import (
	"context"
	"testing"
	"time"

	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSalesPhaseSearchLogRepository_ListBySeries(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}

	repo := rdb.NewSalesPhaseSearchLogRepository(testDB)
	ctx := context.Background()
	oct1 := time.Date(2026, 10, 1, 21, 0, 0, 0, time.UTC)

	// @spec components/entity/sales-phase-search-log/list-by-series "One searched, one never searched"
	t.Run("One searched, one never searched", func(t *testing.T) {
		cleanDatabase(t)
		seriesA := seedSeriesOnly(t, "A")
		seriesB := seedSeriesOnly(t, "B")
		require.NoError(t, repo.Record(ctx, []string{seriesA}, oct1))

		logs, err := repo.ListBySeries(ctx, []string{seriesA, seriesB})
		require.NoError(t, err)
		require.Len(t, logs, 1)
		assert.Equal(t, seriesA, logs[0].SeriesID)
		assert.True(t, oct1.Equal(logs[0].SearchedTime))
	})

	// @spec components/entity/sales-phase-search-log/list-by-series "No series given"
	t.Run("No series given", func(t *testing.T) {
		logs, err := repo.ListBySeries(ctx, nil)
		require.NoError(t, err)
		assert.Empty(t, logs)
	})
}

func TestSalesPhaseSearchLogRepository_Record(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}

	repo := rdb.NewSalesPhaseSearchLogRepository(testDB)
	ctx := context.Background()
	sep1 := time.Date(2026, 9, 1, 21, 0, 0, 0, time.UTC)
	oct1 := time.Date(2026, 10, 1, 21, 0, 0, 0, time.UTC)

	// @spec components/entity/sales-phase-search-log/record "Series searched again"
	t.Run("Series searched again", func(t *testing.T) {
		cleanDatabase(t)
		seriesA := seedSeriesOnly(t, "A")
		require.NoError(t, repo.Record(ctx, []string{seriesA}, sep1))

		require.NoError(t, repo.Record(ctx, []string{seriesA}, oct1))

		logs, err := repo.ListBySeries(ctx, []string{seriesA})
		require.NoError(t, err)
		require.Len(t, logs, 1)
		assert.True(t, oct1.Equal(logs[0].SearchedTime))
	})

	// @spec components/entity/sales-phase-search-log/record "Series searched for the first time"
	t.Run("Series searched for the first time", func(t *testing.T) {
		cleanDatabase(t)
		seriesA := seedSeriesOnly(t, "A")

		require.NoError(t, repo.Record(ctx, []string{seriesA}, oct1))

		logs, err := repo.ListBySeries(ctx, []string{seriesA})
		require.NoError(t, err)
		require.Len(t, logs, 1)
		assert.True(t, oct1.Equal(logs[0].SearchedTime))
	})

	// @spec components/entity/sales-phase-search-log/record "Unknown series"
	t.Run("Unknown series", func(t *testing.T) {
		cleanDatabase(t)
		seriesA := seedSeriesOnly(t, "A")

		err := repo.Record(ctx, []string{seriesA, mustNewV7()}, oct1)
		assertAppErrCode(t, err, codes.FailedPrecondition)

		logs, err := repo.ListBySeries(ctx, []string{seriesA})
		require.NoError(t, err)
		assert.Empty(t, logs, "no series is recorded when one is unknown")
	})

	t.Run("empty input records nothing", func(t *testing.T) {
		require.NoError(t, repo.Record(ctx, nil, oct1))
	})

	t.Run("future searched time is rejected", func(t *testing.T) {
		cleanDatabase(t)
		seriesA := seedSeriesOnly(t, "A")

		err := repo.Record(ctx, []string{seriesA}, time.Now().Add(time.Hour))
		assertAppErrCode(t, err, codes.InvalidArgument)
	})
}
