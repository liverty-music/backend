package rdb_test

import (
	"context"
	"testing"

	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"uuid"
)

// seedFirstPartyEvents inserts a first-party PUBLIC series with one event per
// given publish state, on consecutive dates, and returns the event IDs in that
// order.
func seedFirstPartyEvents(t *testing.T, states ...string) []string {
	t.Helper()
	ctx := context.Background()

	organizerID := seedOrganizer(t)
	venueID := seedVenue(t, "eps-venue-"+uuid.NewV7().String())
	seriesID := uuid.NewV7().String()
	_, err := testDB.Pool.Exec(ctx, `
		INSERT INTO series (id, title, type, organizer_id, visibility)
		VALUES ($1, 'Organizer Series', 'TOUR', $2, 'PUBLIC')
	`, seriesID, organizerID)
	require.NoError(t, err)

	ids := make([]string, len(states))
	for i, state := range states {
		ids[i] = uuid.NewV7().String()
		_, err = testDB.Pool.Exec(ctx,
			`INSERT INTO events (id, series_id, venue_id, local_event_date, publish_state)
			 VALUES ($1, $2, $3, DATE '2027-03-01' + $4::int, $5::event_publish_state)`,
			ids[i], seriesID, venueID, i, state,
		)
		require.NoError(t, err)
	}
	return ids
}

// seedPublishedEvent inserts a first-party series with one PUBLISHED event and
// returns the event ID.
func seedPublishedEvent(t *testing.T) string {
	t.Helper()
	return seedFirstPartyEvents(t, "PUBLISHED")[0]
}

// seedDiscoveredEvent inserts a discovered (NULL organizer/visibility/publish_state)
// series with an associated event and returns the event ID.
func seedDiscoveredEvent(t *testing.T) string {
	t.Helper()
	ctx := context.Background()

	venueID := seedVenue(t, "eps-disc-venue")
	seriesID := uuid.NewV7().String()
	_, err := testDB.Pool.Exec(ctx,
		`INSERT INTO series (id, title, type) VALUES ($1, 'Discovered Series', 'SINGLE')`,
		seriesID,
	)
	require.NoError(t, err)

	eventID := uuid.NewV7().String()
	_, err = testDB.Pool.Exec(ctx,
		`INSERT INTO events (id, series_id, venue_id, local_event_date) VALUES ($1, $2, $3, '2027-05-01')`,
		eventID, seriesID, venueID,
	)
	require.NoError(t, err)

	return eventID
}

// seedOrganizer inserts a minimal organizer row and returns its ID.
func seedOrganizer(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	id := uuid.NewV7().String()
	_, err := testDB.Pool.Exec(ctx,
		`INSERT INTO organizers (id, name, owner_email, status, provider_organizer_id) VALUES ($1, 'Test Organizer', 'test@example.com', 2, $2)`,
		id, "org-"+id,
	)
	require.NoError(t, err)
	return id
}

func TestEventPublishStateRepository_IsEventPublished(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}

	repo := rdb.NewEventPublishStateRepository(testDB)
	ctx := context.Background()

	tests := []struct {
		name        string
		setup       func() string // returns eventID
		wantResult  bool
		wantErrCode codes.Code
	}{
		{
			// @spec components/entity/event/is-event-published "Published organizer event"
			name: "return true for a PUBLISHED event",
			setup: func() string {
				cleanDatabase(t)
				return seedPublishedEvent(t)
			},
			wantResult: true,
		},
		{
			// @spec components/entity/event/is-event-published "Draft date of a published series"
			name: "return false (not error) for a DRAFT event whose sibling date is PUBLISHED",
			setup: func() string {
				cleanDatabase(t)
				return seedFirstPartyEvents(t, "PUBLISHED", "DRAFT")[1]
			},
			wantResult: false,
		},
		{
			// @spec components/entity/event/is-event-published "Cancelled date of a published series"
			name: "return false (not error) for a CANCELLED event whose sibling date is PUBLISHED",
			setup: func() string {
				cleanDatabase(t)
				return seedFirstPartyEvents(t, "PUBLISHED", "CANCELLED")[1]
			},
			wantResult: false,
		},
		{
			// @spec components/entity/event/is-event-published "Discovered event"
			name: "return false (not error) for a discovered event with no publish state",
			setup: func() string {
				cleanDatabase(t)
				return seedDiscoveredEvent(t)
			},
			wantResult: false,
		},
		{
			// @spec components/entity/event/is-event-published "Unknown event id"
			name: "return NotFound for non-existent event",
			setup: func() string {
				cleanDatabase(t)
				return uuid.NewV7().String()
			},
			wantResult:  false,
			wantErrCode: codes.NotFound,
		},
		{
			name: "return InvalidArgument for empty event ID",
			setup: func() string {
				cleanDatabase(t)
				return ""
			},
			wantResult:  false,
			wantErrCode: codes.InvalidArgument,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			eventID := tt.setup()

			got, err := repo.IsEventPublished(ctx, eventID)

			if tt.wantErrCode != 0 {
				require.Error(t, err)
				var ae *apperr.AppErr
				require.ErrorAs(t, err, &ae)
				assert.Equal(t, tt.wantErrCode, ae.Code, "got error: %v", err)
				assert.False(t, got)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.wantResult, got)
		})
	}
}

func TestEventPublishStateRepository_GetEventOrganizerID(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}

	repo := rdb.NewEventPublishStateRepository(testDB)
	ctx := context.Background()

	t.Run("return owning organizer id for a first-party event", func(t *testing.T) {
		cleanDatabase(t)
		organizerID := seedOrganizer(t)
		venueID := seedVenue(t, "eps-owner-venue")
		seriesID := uuid.NewV7().String()
		_, err := testDB.Pool.Exec(ctx, `
			INSERT INTO series (id, title, type, organizer_id, visibility)
			VALUES ($1, 'Owned Series', 'SINGLE', $2, 'PUBLIC')
		`, seriesID, organizerID)
		require.NoError(t, err)
		eventID := uuid.NewV7().String()
		_, err = testDB.Pool.Exec(ctx,
			`INSERT INTO events (id, series_id, venue_id, local_event_date) VALUES ($1, $2, $3, '2027-06-01')`,
			eventID, seriesID, venueID,
		)
		require.NoError(t, err)

		got, err := repo.GetEventOrganizerID(ctx, eventID)
		require.NoError(t, err)
		assert.Equal(t, organizerID, got)
	})

	t.Run("return empty string for a discovered event with no owning organizer", func(t *testing.T) {
		cleanDatabase(t)
		eventID := seedDiscoveredEvent(t)

		got, err := repo.GetEventOrganizerID(ctx, eventID)
		require.NoError(t, err)
		assert.Empty(t, got)
	})

	t.Run("return NotFound for non-existent event", func(t *testing.T) {
		cleanDatabase(t)

		got, err := repo.GetEventOrganizerID(ctx, uuid.NewV7().String())
		require.Error(t, err)
		var ae *apperr.AppErr
		require.ErrorAs(t, err, &ae)
		assert.Equal(t, codes.NotFound, ae.Code, "got error: %v", err)
		assert.Empty(t, got)
	})

	t.Run("return InvalidArgument for empty event ID", func(t *testing.T) {
		cleanDatabase(t)

		got, err := repo.GetEventOrganizerID(ctx, "")
		require.Error(t, err)
		var ae *apperr.AppErr
		require.ErrorAs(t, err, &ae)
		assert.Equal(t, codes.InvalidArgument, ae.Code, "got error: %v", err)
		assert.Empty(t, got)
	})
}
