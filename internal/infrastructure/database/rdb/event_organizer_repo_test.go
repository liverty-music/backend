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

// TestEventOrganizerRepository_GetOrganizerID exercises the event -> series ->
// organizer_id resolution (backend#468) against a real local Postgres.
func TestEventOrganizerRepository_GetOrganizerID(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	ctx := context.Background()
	repo := rdb.NewEventOrganizerRepository(testDB)

	// @spec components/entity/event/get-organizer-id "Organizer-authored event"
	t.Run("returns the Organizer id for an event whose series is organizer-authored", func(t *testing.T) {
		organizerID := seedOrganizer(t)
		venueID := seedVenue(t, "eorg-venue-owned")
		seriesID := entity.NewID()
		_, err := testDB.Pool.Exec(ctx, `
			INSERT INTO series (id, title, type, organizer_id, visibility, publish_state)
			VALUES ($1, 'Organizer Series', 'SINGLE', $2, 'PUBLIC', 'PUBLISHED')
		`, seriesID, organizerID)
		require.NoError(t, err)
		eventID := entity.NewID()
		_, err = testDB.Pool.Exec(ctx,
			`INSERT INTO events (id, series_id, venue_id, local_event_date) VALUES ($1, $2, $3, '2027-06-01')`,
			eventID, seriesID, venueID,
		)
		require.NoError(t, err)

		got, err := repo.GetOrganizerID(ctx, eventID)
		require.NoError(t, err)
		assert.Equal(t, organizerID, got)
	})

	// @spec components/entity/event/get-organizer-id "Unknown event"
	t.Run("fails with NotFound when no event has the id", func(t *testing.T) {
		_, err := repo.GetOrganizerID(ctx, entity.NewID())
		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})

	// @spec components/entity/event/get-organizer-id "Discovery-pipeline event"
	t.Run("fails with NotFound when the event's series has no Organizer", func(t *testing.T) {
		venueID := seedVenue(t, "eorg-venue-discovered")
		seriesID := entity.NewID()
		_, err := testDB.Pool.Exec(ctx,
			`INSERT INTO series (id, title, type) VALUES ($1, 'Discovered Series', 'SINGLE')`,
			seriesID,
		)
		require.NoError(t, err)
		eventID := entity.NewID()
		_, err = testDB.Pool.Exec(ctx,
			`INSERT INTO events (id, series_id, venue_id, local_event_date) VALUES ($1, $2, $3, '2027-06-02')`,
			eventID, seriesID, venueID,
		)
		require.NoError(t, err)

		_, err = repo.GetOrganizerID(ctx, eventID)
		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})
}
