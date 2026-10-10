package rdb_test

import (
	"context"
	"testing"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"uuid"
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

	jst := time.FixedZone("JST", 9*60*60)
	oct5At16 := time.Date(2026, 10, 5, 16, 0, 0, 0, jst)
	oct5At18 := time.Date(2026, 10, 5, 18, 0, 0, 0, jst)
	oct22Close := time.Date(2026, 10, 22, 23, 59, 0, 0, jst)
	nov3Result := time.Date(2026, 11, 3, 15, 0, 0, 0, jst)

	lottery := func(seriesID string, start time.Time) *entity.SalesPhaseCandidate {
		return &entity.SalesPhaseCandidate{
			SeriesID:       seriesID,
			Method:         entity.SalesMethodLottery,
			ApplyStartTime: start,
			ApplyEndTime:   oct22Close,
		}
	}

	// @spec components/entity/sales-phase/upsert "Re-discovery with a corrected time"
	t.Run("Re-discovery with a corrected time", func(t *testing.T) {
		cleanDatabase(t)
		seriesID := seedSeriesOnly(t, "Tour")

		firstID, outcome := upsertPhase(t, repo, ctx, lottery(seriesID, oct5At16))
		assert.Equal(t, entity.UpsertOutcomeInserted, outcome)

		id, outcome := upsertPhase(t, repo, ctx, lottery(seriesID, oct5At18))
		assert.Equal(t, entity.UpsertOutcomeUpdated, outcome)
		assert.Equal(t, firstID, id)

		phases, err := repo.GetBySeries(ctx, seriesID)
		require.NoError(t, err)
		require.Len(t, phases, 1)
		assert.True(t, oct5At18.Equal(phases[0].ApplyStartTime))
	})

	// @spec components/entity/sales-phase/upsert "Re-discovery with more detail"
	t.Run("Re-discovery with more detail", func(t *testing.T) {
		cleanDatabase(t)
		seriesID := seedSeriesOnly(t, "Tour")

		firstID, _ := upsertPhase(t, repo, ctx, lottery(seriesID, oct5At18))

		detailed := lottery(seriesID, oct5At18)
		detailed.LotteryResultTime = nov3Result
		id, outcome := upsertPhase(t, repo, ctx, detailed)
		assert.Equal(t, entity.UpsertOutcomeUpdated, outcome)
		assert.Equal(t, firstID, id)

		phases, err := repo.GetBySeries(ctx, seriesID)
		require.NoError(t, err)
		require.Len(t, phases, 1)
		assert.True(t, nov3Result.Equal(phases[0].LotteryResultTime))
	})

	// @spec components/entity/sales-phase/upsert "Different start dates stay separate"
	t.Run("Different start dates stay separate", func(t *testing.T) {
		cleanDatabase(t)
		seriesID := seedSeriesOnly(t, "Tour")

		firstID, _ := upsertPhase(t, repo, ctx, lottery(seriesID, oct5At18))

		nov10 := &entity.SalesPhaseCandidate{
			SeriesID:       seriesID,
			Method:         entity.SalesMethodLottery,
			ApplyStartTime: time.Date(2026, 11, 10, 12, 0, 0, 0, jst),
			ApplyEndTime:   time.Date(2026, 11, 20, 23, 59, 0, 0, jst),
		}
		id, outcome := upsertPhase(t, repo, ctx, nov10)
		assert.Equal(t, entity.UpsertOutcomeInserted, outcome)
		assert.NotEqual(t, firstID, id)

		phases, err := repo.GetBySeries(ctx, seriesID)
		require.NoError(t, err)
		assert.Len(t, phases, 2)
	})

	// @spec components/entity/sales-phase/upsert "Different methods on one day stay separate"
	t.Run("Different methods on one day stay separate", func(t *testing.T) {
		cleanDatabase(t)
		seriesID := seedSeriesOnly(t, "Tour")

		upsertPhase(t, repo, ctx, lottery(seriesID, oct5At18))

		firstCome := &entity.SalesPhaseCandidate{
			SeriesID:       seriesID,
			Method:         entity.SalesMethodFirstCome,
			ApplyStartTime: oct5At18,
		}
		_, outcome := upsertPhase(t, repo, ctx, firstCome)
		assert.Equal(t, entity.UpsertOutcomeInserted, outcome)

		phases, err := repo.GetBySeries(ctx, seriesID)
		require.NoError(t, err)
		assert.Len(t, phases, 2)
	})

	t.Run("same instant on different Japan dates stays separate", func(t *testing.T) {
		cleanDatabase(t)
		seriesID := seedSeriesOnly(t, "Tour")

		// 23:30 and 00:30 JST are an hour apart but on different Japan dates.
		late := lottery(seriesID, time.Date(2026, 10, 5, 23, 30, 0, 0, jst))
		early := lottery(seriesID, time.Date(2026, 10, 6, 0, 30, 0, 0, jst))
		upsertPhase(t, repo, ctx, late)
		_, outcome := upsertPhase(t, repo, ctx, early)
		assert.Equal(t, entity.UpsertOutcomeInserted, outcome)
	})

	// @spec components/entity/sales-phase/upsert "Omitted value clears the stored one"
	t.Run("Omitted value clears the stored one", func(t *testing.T) {
		cleanDatabase(t)
		seriesID := seedSeriesOnly(t, "Tour")

		withResult := lottery(seriesID, oct5At18)
		withResult.LotteryResultTime = nov3Result
		upsertPhase(t, repo, ctx, withResult)

		_, outcome := upsertPhase(t, repo, ctx, lottery(seriesID, oct5At18))
		assert.Equal(t, entity.UpsertOutcomeUpdated, outcome)

		phases, err := repo.GetBySeries(ctx, seriesID)
		require.NoError(t, err)
		require.Len(t, phases, 1)
		assert.True(t, phases[0].LotteryResultTime.IsZero())
	})

	// @spec components/entity/sales-phase/upsert "Unknown start"
	t.Run("Unknown start", func(t *testing.T) {
		cleanDatabase(t)
		seriesID := seedSeriesOnly(t, "Tour")

		phaseID, outcome, err := repo.Upsert(ctx, &entity.SalesPhaseCandidate{
			SeriesID: seriesID,
			Method:   entity.SalesMethodLottery,
		})
		require.NoError(t, err)
		assert.Equal(t, entity.UpsertOutcomeSkipped, outcome)
		assert.Empty(t, phaseID)

		phases, err := repo.GetBySeries(ctx, seriesID)
		require.NoError(t, err)
		assert.Empty(t, phases)
	})

	// @spec components/entity/sales-phase/upsert "Phase no longer discovered"
	t.Run("Phase no longer discovered", func(t *testing.T) {
		cleanDatabase(t)
		seriesID := seedSeriesOnly(t, "Tour")

		upsertPhase(t, repo, ctx, lottery(seriesID, oct5At18))
		// A later discovery that returns no phases makes no Upsert call; the
		// stored phase stays.
		phases, err := repo.GetBySeries(ctx, seriesID)
		require.NoError(t, err)
		assert.Len(t, phases, 1)
	})

	// @spec components/entity/sales-phase/upsert "No series"
	t.Run("No series", func(t *testing.T) {
		cleanDatabase(t)

		_, _, err := repo.Upsert(ctx, lottery("", oct5At18))
		assertAppErrCode(t, err, codes.InvalidArgument)
	})

	// @spec components/entity/sales-phase/upsert "Unknown series"
	t.Run("Unknown series", func(t *testing.T) {
		cleanDatabase(t)

		_, _, err := repo.Upsert(ctx, lottery(mustNewV7(), oct5At18))
		assertAppErrCode(t, err, codes.FailedPrecondition)
	})

	t.Run("lottery without a close is rejected", func(t *testing.T) {
		cleanDatabase(t)
		seriesID := seedSeriesOnly(t, "Tour")

		_, _, err := repo.Upsert(ctx, &entity.SalesPhaseCandidate{
			SeriesID:       seriesID,
			Method:         entity.SalesMethodLottery,
			ApplyStartTime: oct5At18,
		})
		assertAppErrCode(t, err, codes.InvalidArgument)
	})
}

func TestSalesPhaseRepository_GetBySeries(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}

	repo := rdb.NewSalesPhaseRepository(testDB)
	ctx := context.Background()
	jst := time.FixedZone("JST", 9*60*60)

	// @spec components/entity/sales-phase/get-by-series "Series with two phases"
	t.Run("Series with two phases", func(t *testing.T) {
		cleanDatabase(t)
		seriesID := seedSeriesOnly(t, "Tour")

		nov10 := time.Date(2026, 11, 10, 12, 0, 0, 0, jst)
		oct5 := time.Date(2026, 10, 5, 18, 0, 0, 0, jst)
		// Insert the later one first so the order comes from the query.
		upsertPhase(t, repo, ctx, &entity.SalesPhaseCandidate{
			SeriesID: seriesID, Method: entity.SalesMethodLottery,
			ApplyStartTime: nov10, ApplyEndTime: nov10.Add(72 * time.Hour),
		})
		upsertPhase(t, repo, ctx, &entity.SalesPhaseCandidate{
			SeriesID: seriesID, Method: entity.SalesMethodLottery,
			ApplyStartTime: oct5, ApplyEndTime: oct5.Add(72 * time.Hour),
		})

		phases, err := repo.GetBySeries(ctx, seriesID)
		require.NoError(t, err)
		require.Len(t, phases, 2)
		assert.True(t, oct5.Equal(phases[0].ApplyStartTime))
		assert.True(t, nov10.Equal(phases[1].ApplyStartTime))
	})

	// @spec components/entity/sales-phase/get-by-series "Series with no phase"
	t.Run("Series with no phase", func(t *testing.T) {
		cleanDatabase(t)
		seriesID := seedSeriesOnly(t, "Tour")

		phases, err := repo.GetBySeries(ctx, seriesID)
		require.NoError(t, err)
		assert.Empty(t, phases)
	})

	// @spec components/entity/sales-phase/get-by-series "No series given"
	t.Run("No series given", func(t *testing.T) {
		_, err := repo.GetBySeries(ctx, "")
		assertAppErrCode(t, err, codes.InvalidArgument)
	})
}

func TestSalesPhaseRepository_ListPhasesWithPendingMilestones(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}

	repo := rdb.NewSalesPhaseRepository(testDB)
	ctx := context.Background()
	now := time.Now()
	lookahead := 7 * 24 * time.Hour
	lookback := 2 * time.Hour

	ids := func(phases []*entity.SalesPhase) []string {
		out := make([]string, 0, len(phases))
		for _, p := range phases {
			out = append(out, p.ID)
		}
		return out
	}

	// @spec components/entity/sales-phase/list-phases-with-pending-milestones "Opened weeks ago, result tomorrow"
	t.Run("Opened weeks ago, result tomorrow", func(t *testing.T) {
		cleanDatabase(t)
		seriesID := seedSeriesOnly(t, "Tour")
		id, _ := upsertPhase(t, repo, ctx, &entity.SalesPhaseCandidate{
			SeriesID: seriesID, Method: entity.SalesMethodLottery,
			ApplyStartTime:    now.Add(-21 * 24 * time.Hour),
			ApplyEndTime:      now.Add(-14 * 24 * time.Hour),
			LotteryResultTime: now.Add(24 * time.Hour),
		})

		phases, err := repo.ListPhasesWithPendingMilestones(ctx, lookahead, lookback)
		require.NoError(t, err)
		assert.Equal(t, []string{id}, ids(phases))
	})

	// @spec components/entity/sales-phase/list-phases-with-pending-milestones "Opens beyond the lookahead"
	t.Run("Opens beyond the lookahead", func(t *testing.T) {
		cleanDatabase(t)
		seriesID := seedSeriesOnly(t, "Tour")
		upsertPhase(t, repo, ctx, &entity.SalesPhaseCandidate{
			SeriesID: seriesID, Method: entity.SalesMethodFirstCome,
			ApplyStartTime: now.Add(10 * 24 * time.Hour),
		})

		phases, err := repo.ListPhasesWithPendingMilestones(ctx, lookahead, lookback)
		require.NoError(t, err)
		assert.Empty(t, phases)
	})

	// @spec components/entity/sales-phase/list-phases-with-pending-milestones "All milestones long past"
	t.Run("All milestones long past", func(t *testing.T) {
		cleanDatabase(t)
		seriesID := seedSeriesOnly(t, "Tour")
		upsertPhase(t, repo, ctx, &entity.SalesPhaseCandidate{
			SeriesID: seriesID, Method: entity.SalesMethodFirstCome,
			ApplyStartTime: now.Add(-3 * time.Hour),
		})

		phases, err := repo.ListPhasesWithPendingMilestones(ctx, lookahead, lookback)
		require.NoError(t, err)
		assert.Empty(t, phases)
	})

	// @spec components/entity/sales-phase/list-phases-with-pending-milestones "Latest milestone just past"
	t.Run("Latest milestone just past", func(t *testing.T) {
		cleanDatabase(t)
		seriesID := seedSeriesOnly(t, "Tour")
		id, _ := upsertPhase(t, repo, ctx, &entity.SalesPhaseCandidate{
			SeriesID: seriesID, Method: entity.SalesMethodFirstCome,
			ApplyStartTime: now.Add(-1 * time.Hour),
		})

		phases, err := repo.ListPhasesWithPendingMilestones(ctx, lookahead, lookback)
		require.NoError(t, err)
		assert.Equal(t, []string{id}, ids(phases))
	})

	// @spec components/entity/sales-phase/list-phases-with-pending-milestones "Nothing pending"
	t.Run("Nothing pending", func(t *testing.T) {
		cleanDatabase(t)

		phases, err := repo.ListPhasesWithPendingMilestones(ctx, lookahead, lookback)
		require.NoError(t, err)
		assert.Empty(t, phases)
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
// the artist via concert_artists. Returns the event ID.
func seedEventForSeries(t *testing.T, seriesID, venueID, artistID, date string) string {
	t.Helper()
	ctx := context.Background()
	eventID := mustNewV7()
	_, err := testDB.Pool.Exec(ctx,
		`INSERT INTO events (id, series_id, venue_id, local_event_date) VALUES ($1, $2, $3, $4)`,
		eventID, seriesID, venueID, date,
	)
	require.NoError(t, err)
	_, err = testDB.Pool.Exec(ctx, `INSERT INTO concerts (event_id) VALUES ($1)`, eventID)
	require.NoError(t, err)
	_, err = testDB.Pool.Exec(ctx,
		`INSERT INTO concert_artists (event_id, artist_id) VALUES ($1, $2)`,
		eventID, artistID,
	)
	require.NoError(t, err)
	return eventID
}

// mustNewV7 generates a new UUIDv7 string, panicking on entropy failure.
func mustNewV7() string {
	return uuid.NewV7().String()
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
		ApplyStartTime: t0,
		ApplyEndTime:   t0.Add(72 * time.Hour),
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
		Method:         entity.SalesMethodLottery,
		ApplyStartTime: t0,
		ApplyEndTime:   t0.Add(96 * time.Hour),
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
		ApplyStartTime: t0,
		ApplyEndTime:   t0.Add(72 * time.Hour),
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

// assertAppErrCode asserts err is an apperr carrying the given code.
func assertAppErrCode(t *testing.T, err error, want codes.Code) {
	t.Helper()
	require.Error(t, err)
	var ae *apperr.AppErr
	require.ErrorAs(t, err, &ae)
	assert.Equal(t, want, ae.Code, "got error: %v", err)
}
