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

// seedPublishedEvent inserts a first-party series in PUBLISHED state with an
// associated event and returns the event ID. The series has an organizer_id,
// visibility, and publish_state set to satisfy the schema constraints
// (chk_series_first_party_state).
func seedPublishedEvent(t *testing.T) string {
	t.Helper()
	ctx := context.Background()

	organizerID := seedOrganizer(t)
	venueID := seedVenue(t, "eps-venue")
	seriesID := uuid.NewV7().String()
	_, err := testDB.Pool.Exec(ctx, `
		INSERT INTO series (id, title, type, organizer_id, visibility, publish_state, published_at)
		VALUES ($1, 'Published Series', 'SINGLE', $2, 'PUBLIC', 'PUBLISHED', now())
	`, seriesID, organizerID)
	require.NoError(t, err)

	eventID := uuid.NewV7().String()
	_, err = testDB.Pool.Exec(ctx,
		`INSERT INTO events (id, series_id, venue_id, local_event_date) VALUES ($1, $2, $3, '2027-03-01')`,
		eventID, seriesID, venueID,
	)
	require.NoError(t, err)

	return eventID
}

// seedDraftEvent inserts a first-party series in DRAFT state with an
// associated event and returns the event ID.
func seedDraftEvent(t *testing.T) string {
	t.Helper()
	ctx := context.Background()

	organizerID := seedOrganizer(t)
	venueID := seedVenue(t, "eps-draft-venue")
	seriesID := uuid.NewV7().String()
	_, err := testDB.Pool.Exec(ctx, `
		INSERT INTO series (id, title, type, organizer_id, visibility, publish_state)
		VALUES ($1, 'Draft Series', 'SINGLE', $2, 'PUBLIC', 'DRAFT')
	`, seriesID, organizerID)
	require.NoError(t, err)

	eventID := uuid.NewV7().String()
	_, err = testDB.Pool.Exec(ctx,
		`INSERT INTO events (id, series_id, venue_id, local_event_date) VALUES ($1, $2, $3, '2027-04-01')`,
		eventID, seriesID, venueID,
	)
	require.NoError(t, err)

	return eventID
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
		`INSERT INTO organizers (id, name, operator_email, status, zitadel_org_id) VALUES ($1, 'Test Organizer', 'test@example.com', 2, $2)`,
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
			name: "return true for published event",
			setup: func() string {
				cleanDatabase(t)
				return seedPublishedEvent(t)
			},
			wantResult: true,
		},
		{
			name: "return false (not error) for draft event",
			setup: func() string {
				cleanDatabase(t)
				return seedDraftEvent(t)
			},
			wantResult: false,
		},
		{
			name: "return false (not error) for discovered event with null publish_state",
			setup: func() string {
				cleanDatabase(t)
				return seedDiscoveredEvent(t)
			},
			wantResult: false,
		},
		{
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
