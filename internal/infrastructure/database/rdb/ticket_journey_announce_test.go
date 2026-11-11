package rdb_test

import (
	"context"
	"errors"
	"testing"

	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"uuid"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
	"github.com/liverty-music/backend/internal/usecase"
)

// newJourneyFan seeds a user and an event of their own and returns their ids.
func newJourneyFan(t *testing.T, db *rdb.Database) (userID, eventID string) {
	t.Helper()
	tag := uuid.NewV7().String()
	userID = seedUser(t, db, "journey "+tag, tag+"@example.test", "ext-"+tag)
	eventID = seedEvent(t, db, seedVenue(t, db, "venue "+tag), seedArtist(t, db, "artist "+tag, uuid.NewV7().String()), "event "+tag, "2026-06-01")
	return userID, eventID
}

func TestTicketJourneyRepository_UpsertAnnounces(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	ctx := context.Background()
	repo := rdb.NewTicketJourneyRepository(db, testOutbox)

	journeysOf := func(t *testing.T, mark int64, userID string) []*entityv1.TicketJourney {
		t.Helper()
		return announcedSince(t, db, mark, entity.SubjectTicketJourneyStatusChanged, func() *entityv1.TicketJourney { return &entityv1.TicketJourney{} },
			func(j *entityv1.TicketJourney) bool { return j.GetUserId().GetValue() == userID })
	}
	statusOf := func(t *testing.T, userID, eventID string) entity.TicketJourneyStatus {
		t.Helper()
		var status int16
		require.NoError(t, db.Pool.QueryRow(ctx,
			`SELECT COALESCE((SELECT status FROM ticket_journeys WHERE user_id = $1 AND event_id = $2), 0)`,
			userID, eventID).Scan(&status))
		return entity.TicketJourneyStatus(status)
	}
	upsert := func(t *testing.T, r *rdb.TicketJourneyRepository, userID, eventID string, s entity.TicketJourneyStatus) error {
		t.Helper()
		return r.Upsert(ctx, &entity.TicketJourney{UserID: userID, EventID: eventID, Status: s, StatusSetBy: entity.TicketJourneySetByFan})
	}

	t.Run("first status announced", func(t *testing.T) {
		// @spec components/entity/ticket-journey/upsert "First status announced"
		userID, eventID := newJourneyFan(t, db)
		mark := outboxMark(t, db)

		require.NoError(t, upsert(t, repo, userID, eventID, entity.TicketJourneyStatusTracking))

		got := journeysOf(t, mark, userID)
		require.Len(t, got, 1)
		assert.Equal(t, entityv1.TicketJourneyStatus_TICKET_JOURNEY_STATUS_TRACKING, got[0].GetStatus())
	})

	t.Run("status changed", func(t *testing.T) {
		// @spec components/entity/ticket-journey/upsert "Status changed"
		userID, eventID := newJourneyFan(t, db)
		require.NoError(t, upsert(t, repo, userID, eventID, entity.TicketJourneyStatusApplied))
		mark := outboxMark(t, db)

		require.NoError(t, upsert(t, repo, userID, eventID, entity.TicketJourneyStatusLost))

		got := journeysOf(t, mark, userID)
		require.Len(t, got, 1)
		assert.Equal(t, entityv1.TicketJourneyStatus_TICKET_JOURNEY_STATUS_LOST, got[0].GetStatus())
	})

	t.Run("same status again", func(t *testing.T) {
		// @spec components/entity/ticket-journey/upsert "Same status again"
		userID, eventID := newJourneyFan(t, db)
		require.NoError(t, upsert(t, repo, userID, eventID, entity.TicketJourneyStatusPaid))
		mark := outboxMark(t, db)

		require.NoError(t, upsert(t, repo, userID, eventID, entity.TicketJourneyStatusPaid))

		assert.Empty(t, journeysOf(t, mark, userID))
		assert.Equal(t, entity.TicketJourneyStatusPaid, statusOf(t, userID, eventID))
	})

	t.Run("announcement cannot be recorded", func(t *testing.T) {
		// @spec components/entity/ticket-journey/upsert "Announcement cannot be recorded"
		userID, eventID := newJourneyFan(t, db)
		require.NoError(t, upsert(t, repo, userID, eventID, entity.TicketJourneyStatusApplied))

		err := upsert(t, rdb.NewTicketJourneyRepository(db, failingOutbox{}), userID, eventID, entity.TicketJourneyStatusLost)

		assert.ErrorIs(t, err, apperr.ErrUnavailable)
		assert.Equal(t, entity.TicketJourneyStatusApplied, statusOf(t, userID, eventID))
	})
}

// journeyOf returns the stored journey of the user for the event, or nil.
func journeyOf(t *testing.T, db *rdb.Database, userID, eventID string) *entity.TicketJourney {
	t.Helper()
	j, err := rdb.NewTicketJourneyRepository(db, testOutbox).Get(context.Background(), userID, eventID)
	if errors.Is(err, apperr.ErrNotFound) {
		return nil
	}
	require.NoError(t, err)
	return j
}

func journeyAnnouncementsOf(t *testing.T, db *rdb.Database, mark int64, userID string) []*entityv1.TicketJourney {
	t.Helper()
	return announcedSince(t, db, mark, entity.SubjectTicketJourneyStatusChanged, func() *entityv1.TicketJourney { return &entityv1.TicketJourney{} },
		func(j *entityv1.TicketJourney) bool { return j.GetUserId().GetValue() == userID })
}

func TestTicketJourneyRepository_UpsertSetBy(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	ctx := context.Background()
	repo := rdb.NewTicketJourneyRepository(db, testOutbox)
	upsert := func(t *testing.T, userID, eventID string, s entity.TicketJourneyStatus, by entity.TicketJourneySetBy) {
		t.Helper()
		require.NoError(t, repo.Upsert(ctx, &entity.TicketJourney{UserID: userID, EventID: eventID, Status: s, StatusSetBy: by}))
	}

	t.Run("first status for an event", func(t *testing.T) {
		// @spec components/entity/ticket-journey/upsert "First status for an event"
		// @spec components/entity/ticket-journey/upsert "First status announced"
		userID, eventID := newJourneyFan(t, db)
		mark := outboxMark(t, db)

		upsert(t, userID, eventID, entity.TicketJourneyStatusTracking, entity.TicketJourneySetByFan)

		assert.Equal(t, &entity.TicketJourney{UserID: userID, EventID: eventID, Status: entity.TicketJourneyStatusTracking, StatusSetBy: entity.TicketJourneySetByFan},
			journeyOf(t, db, userID, eventID))
		got := journeyAnnouncementsOf(t, db, mark, userID)
		require.Len(t, got, 1)
		assert.Equal(t, entityv1.TicketJourneyStatusSetBy_TICKET_JOURNEY_STATUS_SET_BY_FAN, got[0].GetStatusSetBy())
	})

	t.Run("replacing the status", func(t *testing.T) {
		// @spec components/entity/ticket-journey/upsert "Replacing the status"
		userID, eventID := newJourneyFan(t, db)
		upsert(t, userID, eventID, entity.TicketJourneyStatusApplied, entity.TicketJourneySetByFan)

		upsert(t, userID, eventID, entity.TicketJourneyStatusLost, entity.TicketJourneySetByPlatform)

		assert.Equal(t, &entity.TicketJourney{UserID: userID, EventID: eventID, Status: entity.TicketJourneyStatusLost, StatusSetBy: entity.TicketJourneySetByPlatform},
			journeyOf(t, db, userID, eventID))
	})

	t.Run("same status again", func(t *testing.T) {
		// @spec components/entity/ticket-journey/upsert "Same status again"
		userID, eventID := newJourneyFan(t, db)
		upsert(t, userID, eventID, entity.TicketJourneyStatusPaid, entity.TicketJourneySetByPlatform)
		mark := outboxMark(t, db)

		upsert(t, userID, eventID, entity.TicketJourneyStatusPaid, entity.TicketJourneySetByPlatform)

		assert.Equal(t, entity.TicketJourneySetByPlatform, journeyOf(t, db, userID, eventID).StatusSetBy)
		assert.Empty(t, journeyAnnouncementsOf(t, db, mark, userID))
	})

	t.Run("the fan confirms an inferred status", func(t *testing.T) {
		// @spec components/entity/ticket-journey/upsert "The fan confirms an inferred status"
		userID, eventID := newJourneyFan(t, db)
		upsert(t, userID, eventID, entity.TicketJourneyStatusTracking, entity.TicketJourneySetByInference)
		mark := outboxMark(t, db)

		upsert(t, userID, eventID, entity.TicketJourneyStatusTracking, entity.TicketJourneySetByFan)

		got := journeyAnnouncementsOf(t, db, mark, userID)
		require.Len(t, got, 1)
		assert.Equal(t, entityv1.TicketJourneyStatus_TICKET_JOURNEY_STATUS_TRACKING, got[0].GetStatus())
		assert.Equal(t, entityv1.TicketJourneyStatusSetBy_TICKET_JOURNEY_STATUS_SET_BY_FAN, got[0].GetStatusSetBy())
	})
}

func TestTicketJourneyRepository_UpsertIfReplaceable(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	ctx := context.Background()
	repo := rdb.NewTicketJourneyRepository(db, testOutbox)
	journey := func(userID, eventID string, s entity.TicketJourneyStatus, by entity.TicketJourneySetBy) *entity.TicketJourney {
		return &entity.TicketJourney{UserID: userID, EventID: eventID, Status: s, StatusSetBy: by}
	}

	t.Run("no journey yet", func(t *testing.T) {
		// @spec components/entity/ticket-journey/upsert-if-replaceable "No journey yet"
		// @spec components/entity/ticket-journey/upsert-if-replaceable "Status stored"
		userID, eventID := newJourneyFan(t, db)
		mark := outboxMark(t, db)
		tracking := journey(userID, eventID, entity.TicketJourneyStatusTracking, entity.TicketJourneySetByInference)

		stored, err := repo.UpsertIfReplaceable(ctx, tracking)

		require.NoError(t, err)
		assert.True(t, stored)
		assert.Equal(t, tracking, journeyOf(t, db, userID, eventID))
		got := journeyAnnouncementsOf(t, db, mark, userID)
		require.Len(t, got, 1)
		assert.Equal(t, entityv1.TicketJourneyStatus_TICKET_JOURNEY_STATUS_TRACKING, got[0].GetStatus())
		assert.Equal(t, entityv1.TicketJourneyStatusSetBy_TICKET_JOURNEY_STATUS_SET_BY_INFERENCE, got[0].GetStatusSetBy())
	})

	t.Run("journey not replaceable", func(t *testing.T) {
		// @spec components/entity/ticket-journey/upsert-if-replaceable "Journey not replaceable"
		// @spec components/entity/ticket-journey/upsert-if-replaceable "Nothing stored"
		userID, eventID := newJourneyFan(t, db)
		applied := journey(userID, eventID, entity.TicketJourneyStatusApplied, entity.TicketJourneySetByFan)
		require.NoError(t, repo.Upsert(ctx, applied))
		mark := outboxMark(t, db)

		stored, err := repo.UpsertIfReplaceable(ctx, journey(userID, eventID, entity.TicketJourneyStatusTracking, entity.TicketJourneySetByInference))

		require.NoError(t, err)
		assert.False(t, stored)
		assert.Equal(t, applied, journeyOf(t, db, userID, eventID))
		assert.Empty(t, journeyAnnouncementsOf(t, db, mark, userID))
	})

	t.Run("same status again", func(t *testing.T) {
		// @spec components/entity/ticket-journey/upsert-if-replaceable "Same status again"
		// @spec components/entity/ticket-journey/upsert-if-replaceable "Nothing stored"
		userID, eventID := newJourneyFan(t, db)
		applied := journey(userID, eventID, entity.TicketJourneyStatusApplied, entity.TicketJourneySetByPlatform)
		_, err := repo.UpsertIfReplaceable(ctx, applied)
		require.NoError(t, err)
		mark := outboxMark(t, db)

		stored, err := repo.UpsertIfReplaceable(ctx, applied)

		require.NoError(t, err)
		assert.False(t, stored)
		assert.Empty(t, journeyAnnouncementsOf(t, db, mark, userID))
	})

	t.Run("entry over Tracking", func(t *testing.T) {
		// @spec components/entity/ticket-journey/upsert-if-replaceable "Entry over Tracking"
		userID, eventID := newJourneyFan(t, db)
		_, err := repo.UpsertIfReplaceable(ctx, journey(userID, eventID, entity.TicketJourneyStatusTracking, entity.TicketJourneySetByInference))
		require.NoError(t, err)
		applied := journey(userID, eventID, entity.TicketJourneyStatusApplied, entity.TicketJourneySetByPlatform)

		stored, err := repo.UpsertIfReplaceable(ctx, applied)

		require.NoError(t, err)
		assert.True(t, stored)
		assert.Equal(t, applied, journeyOf(t, db, userID, eventID))
	})

	t.Run("fan writes at the same moment", func(t *testing.T) {
		// @spec components/entity/ticket-journey/upsert-if-replaceable "Fan writes at the same moment"
		userID, eventID := newJourneyFan(t, db)
		require.NoError(t, repo.Upsert(ctx, journey(userID, eventID, entity.TicketJourneyStatusTracking, entity.TicketJourneySetByFan)))

		// The fan's transaction holds the journey's row lock when the guarded
		// write starts, so it waits and then reads the fan's Paid.
		fanTx, err := db.Pool.Begin(ctx)
		require.NoError(t, err)
		defer func() { _ = fanTx.Rollback(ctx) }()
		_, err = fanTx.Exec(ctx, `UPDATE ticket_journeys SET status = 5, status_set_by = 'fan' WHERE user_id = $1 AND event_id = $2`, userID, eventID)
		require.NoError(t, err)

		done := make(chan error, 1)
		go func() {
			_, err := repo.UpsertIfReplaceable(ctx, journey(userID, eventID, entity.TicketJourneyStatusApplied, entity.TicketJourneySetByPlatform))
			done <- err
		}()
		require.NoError(t, fanTx.Commit(ctx))
		require.NoError(t, <-done)

		assert.Equal(t, journey(userID, eventID, entity.TicketJourneyStatusPaid, entity.TicketJourneySetByFan), journeyOf(t, db, userID, eventID))
	})

	t.Run("unknown event", func(t *testing.T) {
		// @spec components/entity/ticket-journey/upsert-if-replaceable "Unknown event"
		userID, _ := newJourneyFan(t, db)
		unknown := uuid.NewV7().String()

		_, err := repo.UpsertIfReplaceable(ctx, journey(userID, unknown, entity.TicketJourneyStatusTracking, entity.TicketJourneySetByInference))

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
		assert.Nil(t, journeyOf(t, db, userID, unknown))
	})

	t.Run("announcement cannot be recorded", func(t *testing.T) {
		userID, eventID := newJourneyFan(t, db)

		_, err := rdb.NewTicketJourneyRepository(db, failingOutbox{}).UpsertIfReplaceable(ctx,
			journey(userID, eventID, entity.TicketJourneyStatusTracking, entity.TicketJourneySetByInference))

		assert.ErrorIs(t, err, apperr.ErrUnavailable)
		assert.Nil(t, journeyOf(t, db, userID, eventID))
	})
}

func TestTicketJourneyUseCase_InferTrackingAfterUndo(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	// @spec components/usecase/ticket-journey/infer-tracking "After the fan removed an inferred journey"
	ctx := context.Background()
	logger, err := logging.New()
	require.NoError(t, err)
	repo := rdb.NewTicketJourneyRepository(db, testOutbox)
	uc := usecase.NewTicketJourneyUseCase(repo, rdb.NewUserRepository(db, testOutbox), rdb.NewConcertRepository(db, testOutbox),
		rdb.NewFollowRepository(db, testOutbox), rdb.NewLotteryEntryRepository(db, testOutbox), logger)
	userID, eventID := newJourneyFan(t, db)
	started, err := uc.InferTracking(ctx, userID, eventID)
	require.NoError(t, err)
	require.True(t, started)

	// The fan undoes the inferred journey, then adds the concert to the
	// calendar again.
	require.NoError(t, uc.Delete(ctx, userID, eventID))
	started, err = uc.InferTracking(ctx, userID, eventID)

	require.NoError(t, err)
	assert.True(t, started)
	assert.Equal(t, &entity.TicketJourney{
		UserID: userID, EventID: eventID, Status: entity.TicketJourneyStatusTracking, StatusSetBy: entity.TicketJourneySetByInference,
	}, journeyOf(t, db, userID, eventID))
}
