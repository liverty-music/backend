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

	t.Run("overlap match converges re-discovered phase onto existing row", func(t *testing.T) {
		cleanDatabase(t)

		artistID := seedArtist(t, "TestArtist", "aaaabbbb-cccc-dddd-eeee-111111111111")
		venueID := seedVenue(t, "VenueA")
		seriesID := seedSeriesOnly(t, "TestTour")
		eventID := seedEventForSeries(t, seriesID, venueID, artistID, "2026-09-01")

		first := &entity.SalesPhaseCandidate{
			SeriesID:        seriesID,
			CoveredEventIDs: []string{eventID},
			AnchorEventID:   eventID,
			Method:          entity.SalesMethodLottery,
			Channel:         entity.SalesChannelFanClub,
			ApplyStartTime:  t0,
		}
		upsertPhase(t, repo, ctx, first)

		phases, err := repo.GetBySeries(ctx, seriesID)
		require.NoError(t, err)
		require.Len(t, phases, 1)
		firstID := phases[0].ID

		// Re-discover the same phase with additional data.
		second := &entity.SalesPhaseCandidate{
			SeriesID:        seriesID,
			CoveredEventIDs: []string{eventID},
			AnchorEventID:   eventID,
			Method:          entity.SalesMethodLottery,
			Channel:         entity.SalesChannelFanClub,
			ApplyStartTime:  t0.Add(time.Hour), // last-write-wins
			URL:             "https://example.com/ticket",
		}
		upsertPhase(t, repo, ctx, second)

		phases, err = repo.GetBySeries(ctx, seriesID)
		require.NoError(t, err)
		// Same number of rows — converged onto the existing row.
		assert.Len(t, phases, 1)
		assert.Equal(t, firstID, phases[0].ID, "phase ID must be stable after convergence")
		assert.True(t, t0.Add(time.Hour).Equal(phases[0].ApplyStartTime), "apply_start_time should be updated (last-write-wins)")
		assert.Equal(t, "https://example.com/ticket", phases[0].URL)
		assert.Equal(t, []string{eventID}, phases[0].CoveredEventIDs)
	})

	t.Run("incremental coverage growth updates in place without duplicating the phase row", func(t *testing.T) {
		cleanDatabase(t)

		artistID := seedArtist(t, "TestArtist2", "aaaabbbb-cccc-dddd-eeee-222222222222")
		venueA := seedVenue(t, "VenueA2")
		venueB := seedVenue(t, "VenueB2")
		seriesID := seedSeriesOnly(t, "TestTour2")
		eventA := seedEventForSeries(t, seriesID, venueA, artistID, "2026-09-01")
		eventB := seedEventForSeries(t, seriesID, venueB, artistID, "2026-09-02")

		// Initial insert with only eventA covered.
		initial := &entity.SalesPhaseCandidate{
			SeriesID:        seriesID,
			CoveredEventIDs: []string{eventA},
			AnchorEventID:   eventA,
			Method:          entity.SalesMethodLottery,
			Channel:         entity.SalesChannelGeneral,
			ApplyStartTime:  t0,
		}
		upsertPhase(t, repo, ctx, initial)

		phases, err := repo.GetBySeries(ctx, seriesID)
		require.NoError(t, err)
		require.Len(t, phases, 1)
		phaseID := phases[0].ID

		// Second discovery now includes eventB as well — same phase, more coverage.
		grown := &entity.SalesPhaseCandidate{
			SeriesID:        seriesID,
			CoveredEventIDs: []string{eventA, eventB},
			AnchorEventID:   eventA,
			Method:          entity.SalesMethodLottery,
			Channel:         entity.SalesChannelGeneral,
			ApplyStartTime:  t0,
		}
		upsertPhase(t, repo, ctx, grown)

		phases, err = repo.GetBySeries(ctx, seriesID)
		require.NoError(t, err)
		assert.Len(t, phases, 1, "must not duplicate the phase row on coverage growth")
		assert.Equal(t, phaseID, phases[0].ID)
		assert.ElementsMatch(t, []string{eventA, eventB}, phases[0].CoveredEventIDs,
			"covered events must be updated to include both events")
	})

	t.Run("per-leg disjoint coverage produces separate rows", func(t *testing.T) {
		cleanDatabase(t)

		artistID := seedArtist(t, "TestArtist3", "aaaabbbb-cccc-dddd-eeee-333333333333")
		venueA := seedVenue(t, "VenueA3")
		venueB := seedVenue(t, "VenueB3")
		seriesID := seedSeriesOnly(t, "TestTour3")
		eventA := seedEventForSeries(t, seriesID, venueA, artistID, "2026-09-01")
		eventB := seedEventForSeries(t, seriesID, venueB, artistID, "2026-09-02")

		// Two distinct sales phases, each covering a different leg of the tour.
		legA := &entity.SalesPhaseCandidate{
			SeriesID:        seriesID,
			CoveredEventIDs: []string{eventA},
			AnchorEventID:   eventA,
			Method:          entity.SalesMethodLottery,
			Channel:         entity.SalesChannelFanClub,
			ApplyStartTime:  t0,
		}
		legB := &entity.SalesPhaseCandidate{
			SeriesID:        seriesID,
			CoveredEventIDs: []string{eventB},
			AnchorEventID:   eventB,
			Method:          entity.SalesMethodLottery,
			Channel:         entity.SalesChannelFanClub,
			ApplyStartTime:  t0.Add(24 * time.Hour),
		}
		upsertPhase(t, repo, ctx, legA)
		upsertPhase(t, repo, ctx, legB)

		phases, err := repo.GetBySeries(ctx, seriesID)
		require.NoError(t, err)
		assert.Len(t, phases, 2, "disjoint-coverage phases must be stored as separate rows")
	})

	t.Run("last-write-wins on mutable fields when phase converges", func(t *testing.T) {
		cleanDatabase(t)

		artistID := seedArtist(t, "TestArtist4", "aaaabbbb-cccc-dddd-eeee-444444444444")
		venueID := seedVenue(t, "VenueA4")
		seriesID := seedSeriesOnly(t, "TestTour4")
		eventID := seedEventForSeries(t, seriesID, venueID, artistID, "2026-09-01")

		first := &entity.SalesPhaseCandidate{
			SeriesID:        seriesID,
			CoveredEventIDs: []string{eventID},
			AnchorEventID:   eventID,
			Method:          entity.SalesMethodLottery,
			Channel:         entity.SalesChannelGeneral,
			ProviderName:    "e+",
			ApplyStartTime:  t0,
			URL:             "https://old.example.com",
		}
		upsertPhase(t, repo, ctx, first)

		second := &entity.SalesPhaseCandidate{
			SeriesID:        seriesID,
			CoveredEventIDs: []string{eventID},
			AnchorEventID:   eventID,
			Method:          entity.SalesMethodFirstCome,
			Channel:         entity.SalesChannelGeneral,
			ProviderName:    "ローチケ",
			ApplyStartTime:  t0.Add(time.Hour),
			URL:             "https://new.example.com",
		}
		upsertPhase(t, repo, ctx, second)

		phases, err := repo.GetBySeries(ctx, seriesID)
		require.NoError(t, err)
		require.Len(t, phases, 1)
		p := phases[0]
		// All mutable fields must reflect the second (later) write.
		assert.Equal(t, entity.SalesMethodFirstCome, p.Method)
		assert.Equal(t, "ローチケ", p.ProviderName)
		// time.Time.Equal compares the instant, ignoring zone: pgx returns
		// timestamptz in the local zone while t0 is UTC (same instant).
		assert.True(t, t0.Add(time.Hour).Equal(p.ApplyStartTime), "apply_start_time should reflect the later write")
		assert.Equal(t, "https://new.example.com", p.URL)
	})

	t.Run("upsert returns inserted outcome for new row", func(t *testing.T) {
		cleanDatabase(t)

		artistID := seedArtist(t, "TestArtist7", "aaaabbbb-cccc-dddd-eeee-777777777777")
		venueID := seedVenue(t, "VenueA7")
		seriesID := seedSeriesOnly(t, "TestTour7")
		eventID := seedEventForSeries(t, seriesID, venueID, artistID, "2026-09-01")

		candidate := &entity.SalesPhaseCandidate{
			SeriesID:        seriesID,
			CoveredEventIDs: []string{eventID},
			AnchorEventID:   eventID,
			Method:          entity.SalesMethodLottery,
			Channel:         entity.SalesChannelFanClub,
			ApplyStartTime:  t0,
		}
		phaseID, outcome, err := repo.Upsert(ctx, candidate)
		require.NoError(t, err)
		assert.Equal(t, entity.UpsertOutcomeInserted, outcome)
		assert.NotEmpty(t, phaseID)

		// Second call on same candidate must return updated.
		phaseID2, outcome2, err := repo.Upsert(ctx, candidate)
		require.NoError(t, err)
		assert.Equal(t, entity.UpsertOutcomeUpdated, outcome2)
		assert.Equal(t, phaseID, phaseID2, "updated row ID must match inserted row ID")
	})

	t.Run("persistence guard drops candidate with zero apply_start_time", func(t *testing.T) {
		cleanDatabase(t)

		artistID := seedArtist(t, "TestArtist5", "aaaabbbb-cccc-dddd-eeee-555555555555")
		venueID := seedVenue(t, "VenueA5")
		seriesID := seedSeriesOnly(t, "TestTour5")
		eventID := seedEventForSeries(t, seriesID, venueID, artistID, "2026-09-01")

		candidate := &entity.SalesPhaseCandidate{
			SeriesID:        seriesID,
			CoveredEventIDs: []string{eventID},
			AnchorEventID:   eventID,
			Method:          entity.SalesMethodLottery,
			Channel:         entity.SalesChannelFanClub,
			ApplyStartTime:  time.Time{}, // zero — must be dropped
		}
		phaseID, outcome, err := repo.Upsert(ctx, candidate)
		require.NoError(t, err)
		assert.Equal(t, entity.UpsertOutcomeSkipped, outcome)
		assert.Empty(t, phaseID)

		phases, err := repo.GetBySeries(ctx, seriesID)
		require.NoError(t, err)
		assert.Empty(t, phases, "guard must prevent insertion when apply_start_time is zero")
	})

	t.Run("persistence guard drops candidate with no covered events", func(t *testing.T) {
		cleanDatabase(t)

		artistID := seedArtist(t, "TestArtist6", "aaaabbbb-cccc-dddd-eeee-666666666666")
		venueID := seedVenue(t, "VenueA6")
		seriesID := seedSeriesOnly(t, "TestTour6")
		_ = seedEventForSeries(t, seriesID, venueID, artistID, "2026-09-01")

		candidate := &entity.SalesPhaseCandidate{
			SeriesID:        seriesID,
			CoveredEventIDs: nil, // empty — must be dropped
			AnchorEventID:   "",
			Method:          entity.SalesMethodLottery,
			Channel:         entity.SalesChannelFanClub,
			ApplyStartTime:  t0,
		}
		phaseID, outcome, err := repo.Upsert(ctx, candidate)
		require.NoError(t, err)
		assert.Equal(t, entity.UpsertOutcomeSkipped, outcome)
		assert.Empty(t, phaseID)

		phases, err := repo.GetBySeries(ctx, seriesID)
		require.NoError(t, err)
		assert.Empty(t, phases, "guard must prevent insertion when covered event list is empty")
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

// TestSalesPhaseRepository_ChannelIsolation proves fix #4: an FC presale
// (channel=FAN_CLUB) and a general on-sale (channel=GENERAL) covering the
// same event produce TWO rows, not one. Also proves that UNSPECIFIED→FAN_CLUB
// still converges to ONE row.
func TestSalesPhaseRepository_ChannelIsolation(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}

	repo := rdb.NewSalesPhaseRepository(testDB)
	ctx := context.Background()
	t0 := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)

	t.Run("FC and GENERAL over same event → two distinct rows", func(t *testing.T) {
		cleanDatabase(t)

		artistID := seedArtist(t, "BandCh1", "bbbbcccc-dddd-eeee-ffff-111111111111")
		venueID := seedVenue(t, "VenueC1")
		seriesID := seedSeriesOnly(t, "TourCh1")
		eventID := seedEventForSeries(t, seriesID, venueID, artistID, "2026-09-01")

		fc := &entity.SalesPhaseCandidate{
			SeriesID:        seriesID,
			CoveredEventIDs: []string{eventID},
			AnchorEventID:   eventID,
			Method:          entity.SalesMethodLottery,
			Channel:         entity.SalesChannelFanClub, // channel=1
			ApplyStartTime:  t0,
		}
		general := &entity.SalesPhaseCandidate{
			SeriesID:        seriesID,
			CoveredEventIDs: []string{eventID},
			AnchorEventID:   eventID,
			Method:          entity.SalesMethodFirstCome,
			Channel:         entity.SalesChannelGeneral, // channel=6, different from FC
			ApplyStartTime:  t0.Add(24 * time.Hour),
		}

		_, fcOutcome := upsertPhase(t, repo, ctx, fc)
		assert.Equal(t, entity.UpsertOutcomeInserted, fcOutcome)

		_, genOutcome := upsertPhase(t, repo, ctx, general)
		assert.Equal(t, entity.UpsertOutcomeInserted, genOutcome, "GENERAL over same event must INSERT, not UPDATE the FC row")

		phases, err := repo.GetBySeries(ctx, seriesID)
		require.NoError(t, err)
		assert.Len(t, phases, 2, "FC and GENERAL phases must be stored as separate rows")
	})

	t.Run("UNSPECIFIED then FAN_CLUB over same event → converges to one row", func(t *testing.T) {
		cleanDatabase(t)

		artistID := seedArtist(t, "BandCh2", "bbbbcccc-dddd-eeee-ffff-222222222222")
		venueID := seedVenue(t, "VenueC2")
		seriesID := seedSeriesOnly(t, "TourCh2")
		eventID := seedEventForSeries(t, seriesID, venueID, artistID, "2026-09-01")

		unspec := &entity.SalesPhaseCandidate{
			SeriesID:        seriesID,
			CoveredEventIDs: []string{eventID},
			AnchorEventID:   eventID,
			Method:          entity.SalesMethodUnspecified,
			Channel:         entity.SalesChannelUnspecified, // 0 → matches any
			ApplyStartTime:  t0,
		}
		fc := &entity.SalesPhaseCandidate{
			SeriesID:        seriesID,
			CoveredEventIDs: []string{eventID},
			AnchorEventID:   eventID,
			Method:          entity.SalesMethodLottery,
			Channel:         entity.SalesChannelFanClub, // reclassified
			ApplyStartTime:  t0,
		}

		_, unspecOutcome := upsertPhase(t, repo, ctx, unspec)
		assert.Equal(t, entity.UpsertOutcomeInserted, unspecOutcome)

		_, fcOutcome := upsertPhase(t, repo, ctx, fc)
		assert.Equal(t, entity.UpsertOutcomeUpdated, fcOutcome, "reclassification UNSPECIFIED→FAN_CLUB must UPDATE, not INSERT")

		phases, err := repo.GetBySeries(ctx, seriesID)
		require.NoError(t, err)
		assert.Len(t, phases, 1, "reclassification must converge to one row")
		assert.Equal(t, entity.SalesChannelFanClub, phases[0].Channel)
	})
}

// TestSalesPhaseRepository_DiscoveredAt proves fix #5b: DiscoveredAt is populated
// on read and never overwritten on update.
func TestSalesPhaseRepository_DiscoveredAt(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}

	repo := rdb.NewSalesPhaseRepository(testDB)
	ctx := context.Background()
	t0 := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)

	cleanDatabase(t)

	artistID := seedArtist(t, "BandCA", "ccccdddd-eeee-ffff-0000-111111111111")
	venueID := seedVenue(t, "VenueCA")
	seriesID := seedSeriesOnly(t, "TourCA")
	eventID := seedEventForSeries(t, seriesID, venueID, artistID, "2026-09-01")

	candidate := &entity.SalesPhaseCandidate{
		SeriesID:        seriesID,
		CoveredEventIDs: []string{eventID},
		AnchorEventID:   eventID,
		Method:          entity.SalesMethodLottery,
		Channel:         entity.SalesChannelFanClub,
		ApplyStartTime:  t0,
	}
	before := time.Now().UTC().Add(-time.Second)
	upsertPhase(t, repo, ctx, candidate)
	after := time.Now().UTC().Add(time.Second)

	phases, err := repo.GetBySeries(ctx, seriesID)
	require.NoError(t, err)
	require.Len(t, phases, 1)
	assert.False(t, phases[0].DiscoveredAt.IsZero(), "DiscoveredAt must be non-zero after insert")
	assert.True(t, phases[0].DiscoveredAt.After(before) && phases[0].DiscoveredAt.Before(after),
		"DiscoveredAt must be populated by the DB DEFAULT during insert")

	createdAt := phases[0].DiscoveredAt

	// Update via a second upsert and verify DiscoveredAt is unchanged.
	updated := &entity.SalesPhaseCandidate{
		SeriesID:        seriesID,
		CoveredEventIDs: []string{eventID},
		AnchorEventID:   eventID,
		Method:          entity.SalesMethodFirstCome,
		Channel:         entity.SalesChannelFanClub,
		ApplyStartTime:  t0.Add(time.Hour),
	}
	upsertPhase(t, repo, ctx, updated)

	phases2, err := repo.GetBySeries(ctx, seriesID)
	require.NoError(t, err)
	require.Len(t, phases2, 1)
	assert.Equal(t, createdAt.UTC(), phases2[0].DiscoveredAt.UTC(), "DiscoveredAt must not change on update")
}

// TestSalesPhaseReminderRepository_ListSentStages proves fix #10: the batch
// query returns only the stages already sent for the given phase and user set.
func TestSalesPhaseReminderRepository_ListSentStages(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}

	ctx := context.Background()
	cleanDatabase(t)

	artistID := seedArtist(t, "BandLS", "ddddeeee-ffff-0000-1111-222222222222")
	venueID := seedVenue(t, "VenueLS")
	seriesID := seedSeriesOnly(t, "TourLS")
	eventID := seedEventForSeries(t, seriesID, venueID, artistID, "2026-09-01")
	userID := seedUser(t, "UserLS", "userls@example.com", "ext-ls")

	// Insert a sales phase so we can attach reminders to it.
	phaseRepo := rdb.NewSalesPhaseRepository(testDB)
	t0 := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)
	phaseID, _, err := phaseRepo.Upsert(ctx, &entity.SalesPhaseCandidate{
		SeriesID:        seriesID,
		CoveredEventIDs: []string{eventID},
		AnchorEventID:   eventID,
		Method:          entity.SalesMethodLottery,
		Channel:         entity.SalesChannelFanClub,
		ApplyStartTime:  t0,
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
