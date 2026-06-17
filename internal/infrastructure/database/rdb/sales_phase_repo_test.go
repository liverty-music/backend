package rdb_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSalesPhaseRepository_Upsert(t *testing.T) {
	// Subtests are NOT parallel: each calls cleanDatabase, which TRUNCATEs the
	// shared tables; running them concurrently deadlocks (matches the sequential
	// convention of the other repository tests in this package).
	if testDB == nil {
		t.Skip("no local database available")
	}

	repo := rdb.NewSalesPhaseRepository(testDB)
	ctx := context.Background()

	// t0 is a reference time used across test cases.
	t0 := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)

	t.Run("same (series, apply_start) converges and updates descriptive fields last-write-wins", func(t *testing.T) {
		cleanDatabase(t)

		seriesID := seedSeriesOnly(t, "TestTour")

		first := &entity.SalesPhaseCandidate{
			SeriesID:       seriesID,
			Method:         entity.SalesMethodLottery,
			Channel:        entity.SalesChannelFanClub,
			ApplyStartTime: t0,
		}
		upsertPhase(t, repo, ctx, first)

		phases, err := repo.GetBySeries(ctx, seriesID)
		require.NoError(t, err)
		require.Len(t, phases, 1)
		firstID := phases[0].ID

		// Re-discover the same window (same apply_start) with reclassified
		// descriptive fields — must converge onto the existing row.
		second := &entity.SalesPhaseCandidate{
			SeriesID:       seriesID,
			Method:         entity.SalesMethodFirstCome,
			Channel:        entity.SalesChannelGeneral,
			ProviderName:   "ローチケ",
			ApplyStartTime: t0,
			URL:            "https://example.com/ticket",
		}
		upsertPhase(t, repo, ctx, second)

		phases, err = repo.GetBySeries(ctx, seriesID)
		require.NoError(t, err)
		assert.Len(t, phases, 1)
		assert.Equal(t, firstID, phases[0].ID, "phase ID must be stable after convergence")
		// apply_start is identity (unchanged); descriptive fields are LWW-updated.
		assert.True(t, t0.Equal(phases[0].ApplyStartTime), "apply_start_time is identity and unchanged")
		assert.Equal(t, entity.SalesMethodFirstCome, phases[0].Method)
		assert.Equal(t, entity.SalesChannelGeneral, phases[0].Channel)
		assert.Equal(t, "ローチケ", phases[0].ProviderName)
		assert.Equal(t, "https://example.com/ticket", phases[0].URL)
	})

	t.Run("different apply_start produces separate rows", func(t *testing.T) {
		cleanDatabase(t)

		seriesID := seedSeriesOnly(t, "TestTour2")

		early := &entity.SalesPhaseCandidate{
			SeriesID:       seriesID,
			Method:         entity.SalesMethodLottery,
			Channel:        entity.SalesChannelFanClub,
			ApplyStartTime: t0,
		}
		later := &entity.SalesPhaseCandidate{
			SeriesID:       seriesID,
			Method:         entity.SalesMethodFirstCome,
			Channel:        entity.SalesChannelGeneral,
			ApplyStartTime: t0.Add(24 * time.Hour),
		}
		upsertPhase(t, repo, ctx, early)
		upsertPhase(t, repo, ctx, later)

		phases, err := repo.GetBySeries(ctx, seriesID)
		require.NoError(t, err)
		assert.Len(t, phases, 2, "phases with distinct apply_start must be stored as separate rows")
	})

	t.Run("reclassification with same apply_start does not duplicate", func(t *testing.T) {
		cleanDatabase(t)

		seriesID := seedSeriesOnly(t, "TestTour3")

		unspec := &entity.SalesPhaseCandidate{
			SeriesID:       seriesID,
			Method:         entity.SalesMethodUnspecified,
			Channel:        entity.SalesChannelUnspecified,
			ApplyStartTime: t0,
		}
		_, unspecOutcome := upsertPhase(t, repo, ctx, unspec)
		assert.Equal(t, entity.UpsertOutcomeInserted, unspecOutcome)

		// Same apply_start, reclassified channel/sequence — must converge.
		fc := &entity.SalesPhaseCandidate{
			SeriesID:       seriesID,
			Method:         entity.SalesMethodLottery,
			Channel:        entity.SalesChannelFanClub,
			Sequence:       1,
			ApplyStartTime: t0,
		}
		_, fcOutcome := upsertPhase(t, repo, ctx, fc)
		assert.Equal(t, entity.UpsertOutcomeUpdated, fcOutcome,
			"reclassification (UNSPECIFIED→FAN_CLUB) at the same apply_start must UPDATE, not INSERT")

		phases, err := repo.GetBySeries(ctx, seriesID)
		require.NoError(t, err)
		assert.Len(t, phases, 1, "reclassification must converge to one row")
		assert.Equal(t, entity.SalesChannelFanClub, phases[0].Channel)
		assert.Equal(t, 1, phases[0].Sequence)
	})

	t.Run("upsert returns inserted then updated outcome", func(t *testing.T) {
		cleanDatabase(t)

		seriesID := seedSeriesOnly(t, "TestTour7")

		candidate := &entity.SalesPhaseCandidate{
			SeriesID:       seriesID,
			Method:         entity.SalesMethodLottery,
			Channel:        entity.SalesChannelFanClub,
			ApplyStartTime: t0,
		}
		phaseID, outcome, err := repo.Upsert(ctx, candidate)
		require.NoError(t, err)
		assert.Equal(t, entity.UpsertOutcomeInserted, outcome)
		assert.NotEmpty(t, phaseID)

		// Second call on same candidate (same apply_start) must return updated.
		phaseID2, outcome2, err := repo.Upsert(ctx, candidate)
		require.NoError(t, err)
		assert.Equal(t, entity.UpsertOutcomeUpdated, outcome2)
		assert.Equal(t, phaseID, phaseID2, "updated row ID must match inserted row ID")
	})

	t.Run("persistence guard drops candidate with zero apply_start_time", func(t *testing.T) {
		cleanDatabase(t)

		seriesID := seedSeriesOnly(t, "TestTour5")

		candidate := &entity.SalesPhaseCandidate{
			SeriesID:       seriesID,
			Method:         entity.SalesMethodLottery,
			Channel:        entity.SalesChannelFanClub,
			ApplyStartTime: time.Time{}, // zero — must be dropped
		}
		phaseID, outcome, err := repo.Upsert(ctx, candidate)
		require.NoError(t, err)
		assert.Equal(t, entity.UpsertOutcomeSkipped, outcome)
		assert.Empty(t, phaseID)

		phases, err := repo.GetBySeries(ctx, seriesID)
		require.NoError(t, err)
		assert.Empty(t, phases, "guard must prevent insertion when apply_start_time is zero")
	})
}

// ----- test seed helpers specific to sales_phase tests -----

// upsertPhase calls repo.Upsert and fails the test on error.
func upsertPhase(t *testing.T, repo *rdb.SalesPhaseRepository, ctx context.Context, c *entity.SalesPhaseCandidate) (string, entity.UpsertOutcome) {
	t.Helper()
	id, outcome, err := repo.Upsert(ctx, c)
	require.NoError(t, err)
	return id, outcome
}

// seedSeriesOnly inserts a bare series row and returns its ID.
func seedSeriesOnly(t *testing.T, title string) string {
	t.Helper()
	ctx := context.Background()
	seriesID := mustNewV7()
	_, err := testDB.Pool.Exec(ctx,
		`INSERT INTO series (id, title, type) VALUES ($1, $2, 'SINGLE')`,
		seriesID, title,
	)
	require.NoError(t, err)
	return seriesID
}

// seedEventForSeries inserts an event belonging to the given series and links
// the artist via event_performers. Returns the event ID.
func seedEventForSeries(t *testing.T, seriesID, venueID, artistID, date string) string {
	t.Helper()
	ctx := context.Background()
	eventID := mustNewV7()
	_, err := testDB.Pool.Exec(ctx,
		`INSERT INTO events (id, series_id, venue_id, local_event_date) VALUES ($1, $2, $3, $4)`,
		eventID, seriesID, venueID, date,
	)
	require.NoError(t, err)
	_, err = testDB.Pool.Exec(ctx,
		`INSERT INTO event_performers (event_id, artist_id) VALUES ($1, $2)`,
		eventID, artistID,
	)
	require.NoError(t, err)
	return eventID
}

// mustNewV7 generates a new UUIDv7 string, panicking on entropy failure.
func mustNewV7() string {
	return uuid.Must(uuid.NewV7()).String()
}

// TestSalesPhaseRepository_DiscoveredTime proves DiscoveredTime is populated
// on read and never overwritten on update.
func TestSalesPhaseRepository_DiscoveredTime(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}

	repo := rdb.NewSalesPhaseRepository(testDB)
	ctx := context.Background()
	t0 := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)

	cleanDatabase(t)

	seriesID := seedSeriesOnly(t, "TourCA")

	candidate := &entity.SalesPhaseCandidate{
		SeriesID:       seriesID,
		Method:         entity.SalesMethodLottery,
		Channel:        entity.SalesChannelFanClub,
		ApplyStartTime: t0,
	}
	before := time.Now().UTC().Add(-time.Second)
	upsertPhase(t, repo, ctx, candidate)
	after := time.Now().UTC().Add(time.Second)

	phases, err := repo.GetBySeries(ctx, seriesID)
	require.NoError(t, err)
	require.Len(t, phases, 1)
	assert.False(t, phases[0].DiscoveredTime.IsZero(), "DiscoveredTime must be non-zero after insert")
	assert.True(t, phases[0].DiscoveredTime.After(before) && phases[0].DiscoveredTime.Before(after),
		"DiscoveredTime must be populated by the DB DEFAULT during insert")

	createdAt := phases[0].DiscoveredTime

	// Update via a second upsert (same apply_start → converges) and verify
	// DiscoveredTime is unchanged.
	updated := &entity.SalesPhaseCandidate{
		SeriesID:       seriesID,
		Method:         entity.SalesMethodFirstCome,
		Channel:        entity.SalesChannelFanClub,
		ApplyStartTime: t0,
	}
	upsertPhase(t, repo, ctx, updated)

	phases2, err := repo.GetBySeries(ctx, seriesID)
	require.NoError(t, err)
	require.Len(t, phases2, 1)
	assert.Equal(t, createdAt.UTC(), phases2[0].DiscoveredTime.UTC(), "DiscoveredTime must not change on update")
}

// TestSalesPhaseReminderRepository_ListSentStages proves the batch query
// returns only the stages already sent for the given phase and user set.
func TestSalesPhaseReminderRepository_ListSentStages(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}

	ctx := context.Background()
	cleanDatabase(t)

	seriesID := seedSeriesOnly(t, "TourLS")
	userID := seedUser(t, "UserLS", "userls@example.com", "ext-ls")

	// Insert a sales phase so we can attach reminders to it.
	phaseRepo := rdb.NewSalesPhaseRepository(testDB)
	t0 := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)
	phaseID, _, err := phaseRepo.Upsert(ctx, &entity.SalesPhaseCandidate{
		SeriesID:       seriesID,
		Method:         entity.SalesMethodLottery,
		Channel:        entity.SalesChannelFanClub,
		ApplyStartTime: t0,
	})
	require.NoError(t, err)
	require.NotEmpty(t, phaseID)

	reminderRepo := rdb.NewSalesPhaseReminderRepository(testDB)

	// Record two stages as sent.
	require.NoError(t, reminderRepo.RecordSent(ctx, userID, phaseID, entity.ReminderStageApplyOpen))
	require.NoError(t, reminderRepo.RecordSent(ctx, userID, phaseID, entity.ReminderStageResultDay))

	// Batch query must return both stages for userID.
	sent, err := reminderRepo.ListSentStages(ctx, phaseID, []string{userID})
	require.NoError(t, err)
	assert.True(t, sent[userID][entity.ReminderStageApplyOpen], "APPLY_OPEN must be in sent set")
	assert.True(t, sent[userID][entity.ReminderStageResultDay], "RESULT_DAY must be in sent set")
	assert.False(t, sent[userID][entity.ReminderStageApplyClose24H], "APPLY_CLOSE_24H must not be in sent set")

	// A user not in the query must not appear.
	otherUserID := mustNewV7()
	_, inMap := sent[otherUserID]
	assert.False(t, inMap, "user not in query must not appear in result")

	// Empty userIDs returns empty map without error.
	empty, err := reminderRepo.ListSentStages(ctx, phaseID, nil)
	require.NoError(t, err)
	assert.Empty(t, empty)
}
