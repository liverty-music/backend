package rdb_test

import (
	"context"
	"testing"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSeriesRepository_Create(t *testing.T) {
	repo := rdb.NewSeriesRepository(testDB)
	ctx := context.Background()

	tour := entity.NewSeries("Eras Tour 2026", entity.SeriesTypeTour, "https://example.com/eras")
	single := entity.NewSeries("Tokyo Dome 3 Days", entity.SeriesTypeSingle, "")
	festival := entity.NewSeries("FUJI ROCK 2026", entity.SeriesTypeFestival, "https://fujirock.example.com")
	presetID := &entity.Series{
		ID:    "018b2f19-e591-7d12-bf9e-f0e74f1ba001",
		Title: "Preset Tour",
		Type:  entity.SeriesTypeTour,
	}

	tests := []struct {
		name        string
		setup       func()
		series      []*entity.Series
		wantIDs     []string
		wantErr     error
		wantErrCode string
	}{
		{
			name:    "single TOUR series",
			setup:   func() { cleanDatabase(t) },
			series:  []*entity.Series{tour},
			wantIDs: []string{tour.ID},
		},
		{
			name:    "bulk insert all three SeriesTypes",
			setup:   func() { cleanDatabase(t) },
			series:  []*entity.Series{tour, single, festival},
			wantIDs: []string{tour.ID, single.ID, festival.ID},
		},
		{
			name:    "empty slice returns nil without error",
			setup:   func() { cleanDatabase(t) },
			series:  nil,
			wantIDs: nil,
		},
		{
			name:    "nil entries are skipped silently",
			setup:   func() { cleanDatabase(t) },
			series:  []*entity.Series{nil, tour, nil},
			wantIDs: []string{tour.ID},
		},
		{
			name:    "preserves pre-set ID",
			setup:   func() { cleanDatabase(t) },
			series:  []*entity.Series{presetID},
			wantIDs: []string{presetID.ID},
		},
		{
			name: "duplicate ID is filtered by ON CONFLICT DO NOTHING",
			setup: func() {
				cleanDatabase(t)
				_, err := repo.Create(ctx, &entity.Series{
					ID:    "018b2f19-e591-7d12-bf9e-f0e74f1ba002",
					Title: "Original",
					Type:  entity.SeriesTypeTour,
				})
				require.NoError(t, err)
			},
			series: []*entity.Series{{
				ID:    "018b2f19-e591-7d12-bf9e-f0e74f1ba002",
				Title: "Different Title",
				Type:  entity.SeriesTypeSingle,
			}},
			// Re-insert with same ID returns no inserted IDs — the pre-existing
			// row is preserved untouched.
			wantIDs: nil,
		},
		{
			name:    "empty title rejected",
			setup:   func() { cleanDatabase(t) },
			series:  []*entity.Series{{ID: "018b2f19-e591-7d12-bf9e-f0e74f1ba003", Title: "", Type: entity.SeriesTypeTour}},
			wantErr: apperr.ErrInvalidArgument,
		},
		{
			name:    "empty type rejected",
			setup:   func() { cleanDatabase(t) },
			series:  []*entity.Series{{ID: "018b2f19-e591-7d12-bf9e-f0e74f1ba004", Title: "Untyped", Type: ""}},
			wantErr: apperr.ErrInvalidArgument,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.setup != nil {
				tt.setup()
			}

			got, err := repo.Create(ctx, tt.series...)

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				return
			}

			require.NoError(t, err)
			assert.ElementsMatch(t, tt.wantIDs, got)
		})
	}
}

func TestSeriesRepository_Get(t *testing.T) {
	repo := rdb.NewSeriesRepository(testDB)
	ctx := context.Background()

	cleanDatabase(t)
	seeded := entity.NewSeries("Get Test Tour", entity.SeriesTypeTour, "https://example.com/get-test")
	_, err := repo.Create(ctx, seeded)
	require.NoError(t, err)

	t.Run("returns existing series with all fields", func(t *testing.T) {
		got, err := repo.Get(ctx, seeded.ID)
		require.NoError(t, err)
		assert.Equal(t, seeded.ID, got.ID)
		assert.Equal(t, "Get Test Tour", got.Title)
		assert.Equal(t, entity.SeriesTypeTour, got.Type)
		assert.Equal(t, "https://example.com/get-test", got.SourceURL)
	})

	t.Run("returns NotFound for unknown ID", func(t *testing.T) {
		_, err := repo.Get(ctx, "018b2f19-e591-7d12-bf9e-f0e74f1bdead")
		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})

	t.Run("returns InvalidArgument for empty ID", func(t *testing.T) {
		_, err := repo.Get(ctx, "")
		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
	})

	t.Run("source URL absence is preserved as empty string", func(t *testing.T) {
		noURL := entity.NewSeries("No URL Series", entity.SeriesTypeSingle, "")
		_, err := repo.Create(ctx, noURL)
		require.NoError(t, err)

		got, err := repo.Get(ctx, noURL.ID)
		require.NoError(t, err)
		assert.Empty(t, got.SourceURL)
	})
}

func TestSeriesRepository_ListByIDs(t *testing.T) {
	repo := rdb.NewSeriesRepository(testDB)
	ctx := context.Background()

	cleanDatabase(t)
	a := entity.NewSeries("Series A", entity.SeriesTypeTour, "https://a.example.com")
	b := entity.NewSeries("Series B", entity.SeriesTypeSingle, "")
	c := entity.NewSeries("Series C", entity.SeriesTypeFestival, "https://c.example.com")
	_, err := repo.Create(ctx, a, b, c)
	require.NoError(t, err)

	t.Run("returns all matching series", func(t *testing.T) {
		got, err := repo.ListByIDs(ctx, []string{a.ID, b.ID, c.ID})
		require.NoError(t, err)
		require.Len(t, got, 3)
		titles := []string{got[0].Title, got[1].Title, got[2].Title}
		assert.ElementsMatch(t, []string{"Series A", "Series B", "Series C"}, titles)
	})

	t.Run("unknown IDs are silently omitted", func(t *testing.T) {
		got, err := repo.ListByIDs(ctx, []string{a.ID, "018b2f19-e591-7d12-bf9e-f0e74f1bbeef"})
		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.Equal(t, a.ID, got[0].ID)
	})

	t.Run("empty slice returns InvalidArgument", func(t *testing.T) {
		_, err := repo.ListByIDs(ctx, nil)
		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
	})

	t.Run("preserves SeriesType across the round-trip", func(t *testing.T) {
		got, err := repo.ListByIDs(ctx, []string{a.ID, b.ID, c.ID})
		require.NoError(t, err)
		typesByID := map[string]entity.SeriesType{}
		for _, s := range got {
			typesByID[s.ID] = s.Type
		}
		assert.Equal(t, entity.SeriesTypeTour, typesByID[a.ID])
		assert.Equal(t, entity.SeriesTypeSingle, typesByID[b.ID])
		assert.Equal(t, entity.SeriesTypeFestival, typesByID[c.ID])
	})
}
