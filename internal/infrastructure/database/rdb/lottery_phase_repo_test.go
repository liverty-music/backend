package rdb_test

import (
	"context"
	"testing"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// seedLotteryPhase inserts a lottery_sales_phases row and returns the created entity.
// It seeds the required artist, venue, and event rows as FK prerequisites.
// Returns the created phase plus the underlying eventID.
func seedLotteryPhase(t *testing.T, phaseRepo *rdb.LotteryPhaseRepository) (*entity.LotterySalesPhase, string) {
	t.Helper()

	artistID := seedArtist(t, "lottery-artist", "aa110000-0000-7000-0000-00000000lp01")
	venueID := seedVenue(t, "lottery-venue")
	eventID := seedEvent(t, venueID, artistID, "lottery-concert", "2026-11-01")

	open := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	phase := &entity.LotterySalesPhase{
		ID:                       entity.LotteryPhaseID(entity.NewID()),
		EventID:                  eventID,
		OpenTime:                 open,
		CloseTime:                open.Add(7 * 24 * time.Hour),
		TicketCapacity:           100,
		MaxTicketsPerApplication: 4,
		TicketPrice:              5000,
	}

	created, err := phaseRepo.Create(context.Background(), phase)
	require.NoError(t, err)
	return created, eventID
}

func TestLotteryPhaseRepository_Create(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}

	repo := rdb.NewLotteryPhaseRepository(testDB)
	ctx := context.Background()

	tests := []struct {
		name    string
		setup   func() *entity.LotterySalesPhase
		wantErr error
	}{
		{
			name: "persist valid phase and return created row",
			setup: func() *entity.LotterySalesPhase {
				cleanDatabase(t)
				artistID := seedArtist(t, "create-artist", "aa110000-0000-7000-0000-00000000cp01")
				venueID := seedVenue(t, "create-venue")
				eventID := seedEvent(t, venueID, artistID, "create-concert", "2026-11-01")
				open := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
				return &entity.LotterySalesPhase{
					ID:                       entity.LotteryPhaseID(entity.NewID()),
					EventID:                  eventID,
					OpenTime:                 open,
					CloseTime:                open.Add(7 * 24 * time.Hour),
					TicketCapacity:           200,
					MaxTicketsPerApplication: 2,
					TicketPrice:              8000,
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			phase := tt.setup()

			got, err := repo.Create(ctx, phase)

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				return
			}

			require.NoError(t, err)
			require.NotNil(t, got)
			assert.Equal(t, phase.ID, got.ID)
			assert.Equal(t, phase.EventID, got.EventID)
			assert.True(t, phase.OpenTime.Equal(got.OpenTime), "OpenTime must round-trip")
			assert.True(t, phase.CloseTime.Equal(got.CloseTime), "CloseTime must round-trip")
			assert.Equal(t, phase.TicketCapacity, got.TicketCapacity)
			assert.Equal(t, phase.MaxTicketsPerApplication, got.MaxTicketsPerApplication)
			assert.Equal(t, phase.TicketPrice, got.TicketPrice)
		})
	}
}

func TestLotteryPhaseRepository_Get(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}

	repo := rdb.NewLotteryPhaseRepository(testDB)
	ctx := context.Background()

	t.Run("return phase by id", func(t *testing.T) {
		cleanDatabase(t)

		created, _ := seedLotteryPhase(t, repo)

		got, err := repo.Get(ctx, created.ID)

		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, created.ID, got.ID)
		assert.Equal(t, created.EventID, got.EventID)
		assert.Equal(t, created.TicketCapacity, got.TicketCapacity)
		assert.Equal(t, created.MaxTicketsPerApplication, got.MaxTicketsPerApplication)
		assert.Equal(t, created.TicketPrice, got.TicketPrice)
		assert.True(t, created.OpenTime.Equal(got.OpenTime), "OpenTime must round-trip via DB")
		assert.True(t, created.CloseTime.Equal(got.CloseTime), "CloseTime must round-trip via DB")
	})

	t.Run("return NotFound for unknown id", func(t *testing.T) {
		cleanDatabase(t)

		_, err := repo.Get(ctx, entity.LotteryPhaseID(entity.NewID()))

		require.Error(t, err)
		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})
}

func TestLotteryPhaseRepository_ListPhasesDueForDraw(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}

	repo := rdb.NewLotteryPhaseRepository(testDB)
	ctx := context.Background()

	t.Run("return phase whose window closed and drawn_at is null", func(t *testing.T) {
		cleanDatabase(t)

		artistID := seedArtist(t, "due-artist", "aa110000-0000-7000-0000-00000000du01")
		venueID := seedVenue(t, "due-venue")
		eventID := seedEvent(t, venueID, artistID, "due-concert", "2026-11-01")

		// Phase with a close_at in the past and drawn_at NULL (not yet drawn).
		past := time.Now().UTC().Add(-2 * time.Hour)
		phase := &entity.LotterySalesPhase{
			ID:                       entity.LotteryPhaseID(entity.NewID()),
			EventID:                  eventID,
			OpenTime:                 past.Add(-7 * 24 * time.Hour),
			CloseTime:                past,
			TicketCapacity:           50,
			MaxTicketsPerApplication: 2,
			TicketPrice:              3000,
		}
		created, err := repo.Create(ctx, phase)
		require.NoError(t, err)

		now := time.Now().UTC()
		due, err := repo.ListPhasesDueForDraw(ctx, now)

		require.NoError(t, err)
		require.Len(t, due, 1, "exactly one phase should be due")
		assert.Equal(t, created.ID, due[0].ID)
		assert.True(t, due[0].DrawnTime.IsZero(), "DrawnTime must be zero for undrawn phase")
	})

	t.Run("exclude phase whose window has not yet closed", func(t *testing.T) {
		cleanDatabase(t)

		artistID := seedArtist(t, "future-artist", "aa110000-0000-7000-0000-00000000fu01")
		venueID := seedVenue(t, "future-venue")
		eventID := seedEvent(t, venueID, artistID, "future-concert", "2026-12-01")

		// Phase with close_at in the future: window still open.
		future := time.Now().UTC().Add(24 * time.Hour)
		phase := &entity.LotterySalesPhase{
			ID:                       entity.LotteryPhaseID(entity.NewID()),
			EventID:                  eventID,
			OpenTime:                 future.Add(-7 * 24 * time.Hour),
			CloseTime:                future,
			TicketCapacity:           50,
			MaxTicketsPerApplication: 2,
			TicketPrice:              3000,
		}
		_, err := repo.Create(ctx, phase)
		require.NoError(t, err)

		now := time.Now().UTC()
		due, err := repo.ListPhasesDueForDraw(ctx, now)

		require.NoError(t, err)
		assert.Empty(t, due, "future-close phase must not be returned as due")
	})

	t.Run("exclude phase that was already drawn (drawn_at is not null)", func(t *testing.T) {
		cleanDatabase(t)

		artistID := seedArtist(t, "drawn-artist", "aa110000-0000-7000-0000-00000000dr01")
		venueID := seedVenue(t, "drawn-venue")
		eventID := seedEvent(t, venueID, artistID, "drawn-concert", "2026-11-02")

		past := time.Now().UTC().Add(-2 * time.Hour)
		phase := &entity.LotterySalesPhase{
			ID:                       entity.LotteryPhaseID(entity.NewID()),
			EventID:                  eventID,
			OpenTime:                 past.Add(-7 * 24 * time.Hour),
			CloseTime:                past,
			TicketCapacity:           50,
			MaxTicketsPerApplication: 2,
			TicketPrice:              3000,
		}
		created, err := repo.Create(ctx, phase)
		require.NoError(t, err)

		// Stamp drawn_at directly to simulate a completed draw.
		_, err = testDB.Pool.Exec(ctx,
			"UPDATE lottery_sales_phases SET drawn_at = $2 WHERE id = $1",
			string(created.ID), time.Now().UTC(),
		)
		require.NoError(t, err)

		now := time.Now().UTC()
		due, err := repo.ListPhasesDueForDraw(ctx, now)

		require.NoError(t, err)
		assert.Empty(t, due, "already-drawn phase must be excluded")
	})
}
