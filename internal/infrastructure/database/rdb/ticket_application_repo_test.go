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

// seedApplication inserts a ticket_applications row for the given phase and returns it.
// state defaults to TicketApplicationStateApplied.
func seedApplication(
	t *testing.T,
	appRepo *rdb.TicketApplicationRepository,
	phaseID entity.LotteryPhaseID,
	applicantID string,
	state entity.TicketApplicationState,
) *entity.TicketApplication {
	t.Helper()
	app := &entity.TicketApplication{
		ID:                   entity.TicketApplicationID(entity.NewID()),
		PhaseID:              phaseID,
		ApplicantID:          entity.UserID(applicantID),
		RequestedTicketCount: 2,
		Identity: entity.ApplicantIdentity{
			FullName:    "山田太郎",
			PhoneNumber: "+819012345678",
		},
		Authorization: entity.PaymentAuthorization{
			PaymentIntentRef: "pi_seed_" + entity.NewID(),
		},
		State: state,
	}
	created, err := appRepo.Create(context.Background(), app)
	require.NoError(t, err)
	return created
}

func TestTicketApplicationRepository_Create(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}

	phaseRepo := rdb.NewLotteryPhaseRepository(testDB)
	appRepo := rdb.NewTicketApplicationRepository(testDB)
	ctx := context.Background()

	t.Run("persist application and return created row", func(t *testing.T) {
		cleanDatabase(t)

		phase, _ := seedLotteryPhase(t, phaseRepo)
		userID := seedUser(t, "app-create-user", "app-create@test.com", "ext-app-create-01")

		app := &entity.TicketApplication{
			ID:                   entity.TicketApplicationID(entity.NewID()),
			PhaseID:              phase.ID,
			ApplicantID:          entity.UserID(userID),
			RequestedTicketCount: 3,
			Identity: entity.ApplicantIdentity{
				FullName:    "田中花子",
				PhoneNumber: "+819011112222",
			},
			Authorization: entity.PaymentAuthorization{
				PaymentIntentRef: "pi_test_create",
			},
			State: entity.TicketApplicationStateApplied,
		}

		got, err := appRepo.Create(ctx, app)

		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, app.ID, got.ID)
		assert.Equal(t, app.PhaseID, got.PhaseID)
		assert.Equal(t, app.ApplicantID, got.ApplicantID)
		assert.Equal(t, 3, got.RequestedTicketCount)
		assert.Equal(t, "田中花子", got.Identity.FullName)
		assert.Equal(t, "+819011112222", got.Identity.PhoneNumber)
		assert.Equal(t, "pi_test_create", got.Authorization.PaymentIntentRef)
		assert.Equal(t, entity.TicketApplicationStateApplied, got.State)
		assert.Zero(t, got.DrawSequence, "draw_sequence must be zero (NULL) at creation time")
	})
}

func TestTicketApplicationRepository_GetByPhaseAndApplicant(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}

	phaseRepo := rdb.NewLotteryPhaseRepository(testDB)
	appRepo := rdb.NewTicketApplicationRepository(testDB)
	ctx := context.Background()

	t.Run("return active Applied application", func(t *testing.T) {
		cleanDatabase(t)

		phase, _ := seedLotteryPhase(t, phaseRepo)
		userID := seedUser(t, "get-pair-user", "get-pair@test.com", "ext-get-pair-01")

		created := seedApplication(t, appRepo, phase.ID, userID, entity.TicketApplicationStateApplied)

		got, err := appRepo.GetByPhaseAndApplicant(ctx, phase.ID, entity.UserID(userID))

		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, created.ID, got.ID)
		assert.Equal(t, entity.TicketApplicationStateApplied, got.State)
	})

	t.Run("return active Won application", func(t *testing.T) {
		cleanDatabase(t)

		phase, _ := seedLotteryPhase(t, phaseRepo)
		userID := seedUser(t, "get-pair-won-user", "get-pair-won@test.com", "ext-get-pair-won-01")

		created := seedApplication(t, appRepo, phase.ID, userID, entity.TicketApplicationStateWon)

		got, err := appRepo.GetByPhaseAndApplicant(ctx, phase.ID, entity.UserID(userID))

		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, created.ID, got.ID)
		assert.Equal(t, entity.TicketApplicationStateWon, got.State)
	})

	t.Run("return active Lost application", func(t *testing.T) {
		cleanDatabase(t)

		phase, _ := seedLotteryPhase(t, phaseRepo)
		userID := seedUser(t, "get-pair-lost-user", "get-pair-lost@test.com", "ext-get-pair-lost-01")

		created := seedApplication(t, appRepo, phase.ID, userID, entity.TicketApplicationStateLost)

		got, err := appRepo.GetByPhaseAndApplicant(ctx, phase.ID, entity.UserID(userID))

		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, created.ID, got.ID)
		assert.Equal(t, entity.TicketApplicationStateLost, got.State)
	})

	t.Run("return NotFound when only Withdrawn application exists", func(t *testing.T) {
		cleanDatabase(t)

		phase, _ := seedLotteryPhase(t, phaseRepo)
		userID := seedUser(t, "get-pair-withdrawn-user", "get-pair-withdrawn@test.com", "ext-get-pair-wdn-01")

		// Insert a Withdrawn application; it must NOT be returned as active.
		seedApplication(t, appRepo, phase.ID, userID, entity.TicketApplicationStateWithdrawn)

		_, err := appRepo.GetByPhaseAndApplicant(ctx, phase.ID, entity.UserID(userID))

		require.Error(t, err)
		assert.ErrorIs(t, err, apperr.ErrNotFound, "Withdrawn application must not be returned as active")
	})

	t.Run("return NotFound when no application exists", func(t *testing.T) {
		cleanDatabase(t)

		phase, _ := seedLotteryPhase(t, phaseRepo)

		_, err := appRepo.GetByPhaseAndApplicant(ctx, phase.ID, entity.UserID(entity.NewID()))

		require.Error(t, err)
		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})
}

func TestTicketApplicationRepository_Get(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}

	phaseRepo := rdb.NewLotteryPhaseRepository(testDB)
	appRepo := rdb.NewTicketApplicationRepository(testDB)
	ctx := context.Background()

	t.Run("return application by id", func(t *testing.T) {
		cleanDatabase(t)

		phase, _ := seedLotteryPhase(t, phaseRepo)
		userID := seedUser(t, "get-id-user", "get-id@test.com", "ext-get-id-01")

		created := seedApplication(t, appRepo, phase.ID, userID, entity.TicketApplicationStateApplied)

		got, err := appRepo.Get(ctx, created.ID)

		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, created.ID, got.ID)
		assert.Equal(t, created.PhaseID, got.PhaseID)
		assert.Equal(t, created.ApplicantID, got.ApplicantID)
	})

	t.Run("return NotFound for unknown id", func(t *testing.T) {
		cleanDatabase(t)

		_, err := appRepo.Get(ctx, entity.TicketApplicationID(entity.NewID()))

		require.Error(t, err)
		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})
}

func TestTicketApplicationRepository_UpdateState(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}

	phaseRepo := rdb.NewLotteryPhaseRepository(testDB)
	appRepo := rdb.NewTicketApplicationRepository(testDB)
	ctx := context.Background()

	t.Run("update state from Applied to Withdrawn", func(t *testing.T) {
		cleanDatabase(t)

		phase, _ := seedLotteryPhase(t, phaseRepo)
		userID := seedUser(t, "update-state-user", "update-state@test.com", "ext-update-state-01")

		created := seedApplication(t, appRepo, phase.ID, userID, entity.TicketApplicationStateApplied)

		err := appRepo.UpdateState(ctx, created.ID, entity.TicketApplicationStateWithdrawn)

		require.NoError(t, err)

		// Verify the state changed in the DB.
		got, err := appRepo.Get(ctx, created.ID)
		require.NoError(t, err)
		assert.Equal(t, entity.TicketApplicationStateWithdrawn, got.State)
	})

	t.Run("return NotFound when application does not exist", func(t *testing.T) {
		cleanDatabase(t)

		err := appRepo.UpdateState(ctx, entity.TicketApplicationID(entity.NewID()), entity.TicketApplicationStateWithdrawn)

		require.Error(t, err)
		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})
}

func TestTicketApplicationRepository_GetPhaseStats(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}

	phaseRepo := rdb.NewLotteryPhaseRepository(testDB)
	appRepo := rdb.NewTicketApplicationRepository(testDB)
	ctx := context.Background()

	t.Run("return zero stats when no applications exist", func(t *testing.T) {
		cleanDatabase(t)

		phase, _ := seedLotteryPhase(t, phaseRepo)

		stats, err := appRepo.GetPhaseStats(ctx, phase.ID)

		require.NoError(t, err)
		assert.False(t, stats.DrawCompleted)
		assert.Zero(t, stats.ApplicationCount)
		assert.Zero(t, stats.RequestedTicketCount)
		assert.Zero(t, stats.WinningApplicationCount)
		assert.Zero(t, stats.WonTicketCount)
		assert.Zero(t, stats.WaitlistedApplicationCount)
	})

	t.Run("count non-withdrawn applications and requested tickets", func(t *testing.T) {
		cleanDatabase(t)

		phase, _ := seedLotteryPhase(t, phaseRepo)
		userID1 := seedUser(t, "stats-user-1", "stats1@test.com", "ext-stats-01")
		userID2 := seedUser(t, "stats-user-2", "stats2@test.com", "ext-stats-02")
		userID3 := seedUser(t, "stats-user-3", "stats3@test.com", "ext-stats-03")

		seedApplication(t, appRepo, phase.ID, userID1, entity.TicketApplicationStateApplied) // count 2
		seedApplication(t, appRepo, phase.ID, userID2, entity.TicketApplicationStateApplied) // count 2
		// Withdrawn must not be counted.
		seedApplication(t, appRepo, phase.ID, userID3, entity.TicketApplicationStateWithdrawn)

		stats, err := appRepo.GetPhaseStats(ctx, phase.ID)

		require.NoError(t, err)
		assert.False(t, stats.DrawCompleted, "draw_sequence is NULL on Applied rows")
		assert.Equal(t, 2, stats.ApplicationCount)
		assert.Equal(t, 4, stats.RequestedTicketCount, "2 apps × 2 tickets each")
		assert.Zero(t, stats.WinningApplicationCount)
		assert.Zero(t, stats.WonTicketCount)
		assert.Zero(t, stats.WaitlistedApplicationCount)
	})

	t.Run("tally winners and losers after draw", func(t *testing.T) {
		cleanDatabase(t)

		phase, _ := seedLotteryPhase(t, phaseRepo)
		userID1 := seedUser(t, "draw-user-1", "draw1@test.com", "ext-draw-01")
		userID2 := seedUser(t, "draw-user-2", "draw2@test.com", "ext-draw-02")
		userID3 := seedUser(t, "draw-user-3", "draw3@test.com", "ext-draw-03")

		won := seedApplication(t, appRepo, phase.ID, userID1, entity.TicketApplicationStateApplied)
		lost1 := seedApplication(t, appRepo, phase.ID, userID2, entity.TicketApplicationStateApplied)
		lost2 := seedApplication(t, appRepo, phase.ID, userID3, entity.TicketApplicationStateApplied)

		// Simulate draw: set Won and Lost states.
		require.NoError(t, appRepo.UpdateState(ctx, won.ID, entity.TicketApplicationStateWon))
		require.NoError(t, appRepo.UpdateState(ctx, lost1.ID, entity.TicketApplicationStateLost))
		require.NoError(t, appRepo.UpdateState(ctx, lost2.ID, entity.TicketApplicationStateLost))

		// draw_completed relies on draw_sequence being non-null. Stamp it directly
		// to simulate what the draw job would do.
		_, err := testDB.Pool.Exec(ctx,
			"UPDATE ticket_applications SET draw_sequence = $2 WHERE id = $1",
			string(won.ID), 0,
		)
		require.NoError(t, err)
		_, err = testDB.Pool.Exec(ctx,
			"UPDATE ticket_applications SET draw_sequence = $2 WHERE id = $1",
			string(lost1.ID), 1,
		)
		require.NoError(t, err)

		stats, err := appRepo.GetPhaseStats(ctx, phase.ID)

		require.NoError(t, err)
		assert.True(t, stats.DrawCompleted, "at least one row has draw_sequence set")
		assert.Equal(t, 3, stats.ApplicationCount)
		assert.Equal(t, 6, stats.RequestedTicketCount, "3 apps × 2 tickets each")
		assert.Equal(t, 1, stats.WinningApplicationCount)
		assert.Equal(t, 2, stats.WonTicketCount, "1 winner × 2 tickets")
		assert.Equal(t, 2, stats.WaitlistedApplicationCount)
	})

	t.Run("return InvalidArgument for empty phase ID", func(t *testing.T) {
		cleanDatabase(t)

		_, err := appRepo.GetPhaseStats(ctx, "")

		require.Error(t, err)
		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
	})
}

func TestTicketApplicationRepository_UniqueIndex_ActiveConstraint(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}

	phaseRepo := rdb.NewLotteryPhaseRepository(testDB)
	appRepo := rdb.NewTicketApplicationRepository(testDB)
	ctx := context.Background()

	t.Run("duplicate active application violates unique index and maps to AlreadyExists", func(t *testing.T) {
		cleanDatabase(t)

		phase, _ := seedLotteryPhase(t, phaseRepo)
		userID := seedUser(t, "dup-active-user", "dup-active@test.com", "ext-dup-active-01")

		// First application succeeds.
		seedApplication(t, appRepo, phase.ID, userID, entity.TicketApplicationStateApplied)

		// Second application with same (phase_id, applicant_id) and active state must fail.
		dup := &entity.TicketApplication{
			ID:                   entity.TicketApplicationID(entity.NewID()),
			PhaseID:              phase.ID,
			ApplicantID:          entity.UserID(userID),
			RequestedTicketCount: 1,
			Identity: entity.ApplicantIdentity{
				FullName:    "重複太郎",
				PhoneNumber: "+819099998888",
			},
			Authorization: entity.PaymentAuthorization{
				PaymentIntentRef: "pi_dup_" + entity.NewID(),
			},
			State: entity.TicketApplicationStateApplied,
		}
		_, err := appRepo.Create(ctx, dup)

		require.Error(t, err)
		assert.ErrorIs(t, err, apperr.ErrAlreadyExists,
			"duplicate active application must map to AlreadyExists")
	})

	t.Run("application after withdrawal is allowed for same phase and applicant", func(t *testing.T) {
		cleanDatabase(t)

		phase, _ := seedLotteryPhase(t, phaseRepo)
		userID := seedUser(t, "reapply-user", "reapply@test.com", "ext-reapply-01")

		// First application: Applied.
		first := seedApplication(t, appRepo, phase.ID, userID, entity.TicketApplicationStateApplied)

		// Withdraw the first application so it no longer occupies the unique index.
		require.NoError(t, appRepo.UpdateState(ctx, first.ID, entity.TicketApplicationStateWithdrawn))

		// Re-apply: the Withdrawn row is excluded from the partial unique index so
		// this must succeed.
		second := &entity.TicketApplication{
			ID:                   entity.TicketApplicationID(entity.NewID()),
			PhaseID:              phase.ID,
			ApplicantID:          entity.UserID(userID),
			RequestedTicketCount: 1,
			Identity: entity.ApplicantIdentity{
				FullName:    "再申請太郎",
				PhoneNumber: "+819012341234",
			},
			Authorization: entity.PaymentAuthorization{
				PaymentIntentRef: "pi_reapply_" + entity.NewID(),
			},
			State: entity.TicketApplicationStateApplied,
		}
		created, err := appRepo.Create(ctx, second)

		require.NoError(t, err, "re-application after withdrawal must succeed")
		assert.Equal(t, entity.TicketApplicationStateApplied, created.State)
	})
}

func TestTicketApplicationRepository_ListAppliedForPhase(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}

	phaseRepo := rdb.NewLotteryPhaseRepository(testDB)
	appRepo := rdb.NewTicketApplicationRepository(testDB)
	ctx := context.Background()

	t.Run("return only Applied applications", func(t *testing.T) {
		cleanDatabase(t)

		phase, _ := seedLotteryPhase(t, phaseRepo)
		userApplied := seedUser(t, "list-applied-user", "lapplied@test.com", "ext-lapplied-01")
		userWon := seedUser(t, "list-applied-won", "lapplied-won@test.com", "ext-lapplied-02")
		userWithdrawn := seedUser(t, "list-applied-wdn", "lapplied-wdn@test.com", "ext-lapplied-03")

		applied := seedApplication(t, appRepo, phase.ID, userApplied, entity.TicketApplicationStateApplied)
		// Won and Withdrawn must be excluded from draw candidates.
		seedApplication(t, appRepo, phase.ID, userWon, entity.TicketApplicationStateWon)
		seedApplication(t, appRepo, phase.ID, userWithdrawn, entity.TicketApplicationStateWithdrawn)

		got, err := appRepo.ListAppliedForPhase(ctx, phase.ID)

		require.NoError(t, err)
		require.Len(t, got, 1, "only Applied application must be returned")
		assert.Equal(t, applied.ID, got[0].ID)
		assert.Equal(t, entity.TicketApplicationStateApplied, got[0].State)
	})

	t.Run("return empty slice when no Applied applications exist", func(t *testing.T) {
		cleanDatabase(t)

		phase, _ := seedLotteryPhase(t, phaseRepo)
		userID := seedUser(t, "list-none-user", "lnone@test.com", "ext-lnone-01")
		seedApplication(t, appRepo, phase.ID, userID, entity.TicketApplicationStateWithdrawn)

		got, err := appRepo.ListAppliedForPhase(ctx, phase.ID)

		require.NoError(t, err)
		assert.Empty(t, got)
	})

	t.Run("return InvalidArgument for empty phase ID", func(t *testing.T) {
		cleanDatabase(t)

		_, err := appRepo.ListAppliedForPhase(ctx, "")

		require.Error(t, err)
		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
	})
}

func TestTicketApplicationRepository_PersistDrawOutcome(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}

	phaseRepo := rdb.NewLotteryPhaseRepository(testDB)
	appRepo := rdb.NewTicketApplicationRepository(testDB)
	ctx := context.Background()

	t.Run("set winner state=Won, loser state=Lost, stamp drawn_at atomically", func(t *testing.T) {
		cleanDatabase(t)

		phase, _ := seedLotteryPhase(t, phaseRepo)
		userW := seedUser(t, "draw-win-user", "drawwin@test.com", "ext-drawwin-01")
		userL := seedUser(t, "draw-lose-user", "drawlose@test.com", "ext-drawlose-01")

		winner := seedApplication(t, appRepo, phase.ID, userW, entity.TicketApplicationStateApplied)
		loser := seedApplication(t, appRepo, phase.ID, userL, entity.TicketApplicationStateApplied)

		winners := []entity.DrawWinnerRow{{ApplicationID: winner.ID, PaymentIntentRef: "pi_win", DrawSequence: 0}}
		losers := []entity.DrawLoserRow{{ApplicationID: loser.ID, PaymentIntentRef: "pi_lose", DrawSequence: 1}}

		err := appRepo.PersistDrawOutcome(ctx, phase.ID, winners, losers)
		require.NoError(t, err)

		// Verify winner state and draw_sequence.
		gotWinner, err := appRepo.Get(ctx, winner.ID)
		require.NoError(t, err)
		assert.Equal(t, entity.TicketApplicationStateWon, gotWinner.State)
		assert.Equal(t, int64(0), gotWinner.DrawSequence)

		// Verify loser state and draw_sequence.
		gotLoser, err := appRepo.Get(ctx, loser.ID)
		require.NoError(t, err)
		assert.Equal(t, entity.TicketApplicationStateLost, gotLoser.State)
		assert.Equal(t, int64(1), gotLoser.DrawSequence)

		// Verify drawn_at is stamped on the phase.
		gotPhase, err := phaseRepo.Get(ctx, phase.ID)
		require.NoError(t, err)
		assert.False(t, gotPhase.DrawnTime.IsZero(), "drawn_at must be set after PersistDrawOutcome")
	})

	t.Run("zero-application draw stamps drawn_at on phase", func(t *testing.T) {
		cleanDatabase(t)

		phase, _ := seedLotteryPhase(t, phaseRepo)

		err := appRepo.PersistDrawOutcome(ctx, phase.ID, nil, nil)
		require.NoError(t, err)

		gotPhase, err := phaseRepo.Get(ctx, phase.ID)
		require.NoError(t, err)
		assert.False(t, gotPhase.DrawnTime.IsZero(), "drawn_at must be stamped even for a zero-application draw")
	})

	t.Run("return InvalidArgument for empty phase ID", func(t *testing.T) {
		cleanDatabase(t)

		err := appRepo.PersistDrawOutcome(ctx, "", nil, nil)

		require.Error(t, err)
		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
	})
}
