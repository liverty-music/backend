package rdb_test

import (
	"context"
	"testing"

	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"uuid"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
)

// newJourneyFan seeds a user and an event of their own and returns their ids.
func newJourneyFan(t *testing.T) (userID, eventID string) {
	t.Helper()
	tag := uuid.NewV7().String()
	userID = seedUser(t, "journey "+tag, tag+"@example.test", "ext-"+tag)
	eventID = seedEvent(t, seedVenue(t, "venue "+tag), seedArtist(t, "artist "+tag, uuid.NewV7().String()), "event "+tag, "2026-06-01")
	return userID, eventID
}

func TestTicketJourneyRepository_UpsertAnnounces(t *testing.T) {
	ctx := context.Background()
	repo := rdb.NewTicketJourneyRepository(testDB, testOutbox)

	journeysOf := func(t *testing.T, mark int64, userID string) []*entityv1.TicketJourney {
		t.Helper()
		return announcedSince(t, mark, entity.SubjectTicketJourneyStatusChanged, func() *entityv1.TicketJourney { return &entityv1.TicketJourney{} },
			func(j *entityv1.TicketJourney) bool { return j.GetUserId().GetValue() == userID })
	}
	statusOf := func(t *testing.T, userID, eventID string) entity.TicketJourneyStatus {
		t.Helper()
		var status int16
		require.NoError(t, testDB.Pool.QueryRow(ctx,
			`SELECT COALESCE((SELECT status FROM ticket_journeys WHERE user_id = $1 AND event_id = $2), 0)`,
			userID, eventID).Scan(&status))
		return entity.TicketJourneyStatus(status)
	}
	upsert := func(t *testing.T, r *rdb.TicketJourneyRepository, userID, eventID string, s entity.TicketJourneyStatus) error {
		t.Helper()
		return r.Upsert(ctx, &entity.TicketJourney{UserID: userID, EventID: eventID, Status: s})
	}

	t.Run("first status announced", func(t *testing.T) {
		// @spec components/entity/ticket-journey/upsert "First status announced"
		userID, eventID := newJourneyFan(t)
		mark := outboxMark(t)

		require.NoError(t, upsert(t, repo, userID, eventID, entity.TicketJourneyStatusTracking))

		got := journeysOf(t, mark, userID)
		require.Len(t, got, 1)
		assert.Equal(t, entityv1.TicketJourneyStatus_TICKET_JOURNEY_STATUS_TRACKING, got[0].GetStatus())
	})

	t.Run("status changed", func(t *testing.T) {
		// @spec components/entity/ticket-journey/upsert "Status changed"
		userID, eventID := newJourneyFan(t)
		require.NoError(t, upsert(t, repo, userID, eventID, entity.TicketJourneyStatusApplied))
		mark := outboxMark(t)

		require.NoError(t, upsert(t, repo, userID, eventID, entity.TicketJourneyStatusLost))

		got := journeysOf(t, mark, userID)
		require.Len(t, got, 1)
		assert.Equal(t, entityv1.TicketJourneyStatus_TICKET_JOURNEY_STATUS_LOST, got[0].GetStatus())
	})

	t.Run("same status again", func(t *testing.T) {
		// @spec components/entity/ticket-journey/upsert "Same status again"
		userID, eventID := newJourneyFan(t)
		require.NoError(t, upsert(t, repo, userID, eventID, entity.TicketJourneyStatusPaid))
		mark := outboxMark(t)

		require.NoError(t, upsert(t, repo, userID, eventID, entity.TicketJourneyStatusPaid))

		assert.Empty(t, journeysOf(t, mark, userID))
		assert.Equal(t, entity.TicketJourneyStatusPaid, statusOf(t, userID, eventID))
	})

	t.Run("announcement cannot be recorded", func(t *testing.T) {
		// @spec components/entity/ticket-journey/upsert "Announcement cannot be recorded"
		userID, eventID := newJourneyFan(t)
		require.NoError(t, upsert(t, repo, userID, eventID, entity.TicketJourneyStatusApplied))

		err := upsert(t, rdb.NewTicketJourneyRepository(testDB, failingOutbox{}), userID, eventID, entity.TicketJourneyStatusLost)

		assert.ErrorIs(t, err, apperr.ErrUnavailable)
		assert.Equal(t, entity.TicketJourneyStatusApplied, statusOf(t, userID, eventID))
	})
}
