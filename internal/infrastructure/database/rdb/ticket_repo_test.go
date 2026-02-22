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

// seedTicketTestData inserts a user, venue, and event needed by the tickets table FK constraints.
// Returns (eventID, userID).
func seedTicketTestData(t *testing.T) (string, string) {
	t.Helper()
	ctx := context.Background()

	var userID string
	err := testDB.Pool.QueryRow(ctx,
		`INSERT INTO users (name, email, external_id) VALUES ($1, $2, $3) RETURNING id`,
		"ticket-test-user", "ticket-test@example.com", "018b2f19-e591-7d12-bf9e-f0e74f1b4900",
	).Scan(&userID)
	require.NoError(t, err)

	var venueID string
	err = testDB.Pool.QueryRow(ctx,
		`INSERT INTO venues (name, raw_name) VALUES ($1, $2) RETURNING id`,
		"ticket-test-venue", "ticket-test-venue",
	).Scan(&venueID)
	require.NoError(t, err)

	var eventID string
	err = testDB.Pool.QueryRow(ctx,
		`INSERT INTO events (venue_id, title, local_event_date) VALUES ($1, $2, $3) RETURNING id`,
		venueID, "ticket-test-event", "2026-03-01",
	).Scan(&eventID)
	require.NoError(t, err)

	return eventID, userID
}

func TestTicketRepository_Create(t *testing.T) {
	cleanDatabase()
	repo := rdb.NewTicketRepository(testDB)
	ctx := context.Background()
	eventID, userID := seedTicketTestData(t)

	tests := []struct {
		name    string
		args    *entity.NewTicket
		wantErr error
	}{
		{
			name: "create valid ticket",
			args: &entity.NewTicket{
				EventID: eventID,
				UserID:  userID,
				TokenID: 12345,
				TxHash:  "0xdeadbeef",
			},
			wantErr: nil,
		},
		{
			name:    "nil params",
			args:    nil,
			wantErr: apperr.ErrInvalidArgument,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ticket, err := repo.Create(ctx, tc.args)
			if tc.wantErr != nil {
				require.Error(t, err)
				assert.ErrorIs(t, err, tc.wantErr)
				return
			}

			require.NoError(t, err)
			assert.NotEmpty(t, ticket.ID)
			assert.Equal(t, tc.args.EventID, ticket.EventID)
			assert.Equal(t, tc.args.UserID, ticket.UserID)
			assert.Equal(t, tc.args.TokenID, ticket.TokenID)
			assert.Equal(t, tc.args.TxHash, ticket.TxHash)
			assert.False(t, ticket.MintTime.IsZero())
		})
	}
}

func TestTicketRepository_Create_DuplicateEventUser(t *testing.T) {
	cleanDatabase()
	repo := rdb.NewTicketRepository(testDB)
	ctx := context.Background()
	eventID, userID := seedTicketTestData(t)

	_, err := repo.Create(ctx, &entity.NewTicket{
		EventID: eventID,
		UserID:  userID,
		TokenID: 100,
		TxHash:  "0xfirst",
	})
	require.NoError(t, err)

	// Second create with same event+user should fail with AlreadyExists.
	_, err = repo.Create(ctx, &entity.NewTicket{
		EventID: eventID,
		UserID:  userID,
		TokenID: 200,
		TxHash:  "0xsecond",
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, apperr.ErrAlreadyExists)
}

func TestTicketRepository_Get(t *testing.T) {
	cleanDatabase()
	repo := rdb.NewTicketRepository(testDB)
	ctx := context.Background()
	eventID, userID := seedTicketTestData(t)

	created, err := repo.Create(ctx, &entity.NewTicket{
		EventID: eventID,
		UserID:  userID,
		TokenID: 42,
		TxHash:  "0xabcdef",
	})
	require.NoError(t, err)

	tests := []struct {
		name    string
		args    string
		wantErr error
	}{
		{
			name:    "existing ticket",
			args:    created.ID,
			wantErr: nil,
		},
		{
			name:    "non-existent ticket",
			args:    "018b2f19-e591-7d12-bf9e-000000000000",
			wantErr: apperr.ErrNotFound,
		},
		{
			name:    "empty ID",
			args:    "",
			wantErr: apperr.ErrInvalidArgument,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ticket, err := repo.Get(ctx, tc.args)
			if tc.wantErr != nil {
				require.Error(t, err)
				assert.ErrorIs(t, err, tc.wantErr)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, created.ID, ticket.ID)
			assert.Equal(t, created.EventID, ticket.EventID)
			assert.Equal(t, created.UserID, ticket.UserID)
			assert.Equal(t, created.TokenID, ticket.TokenID)
		})
	}
}

func TestTicketRepository_GetByEventAndUser(t *testing.T) {
	cleanDatabase()
	repo := rdb.NewTicketRepository(testDB)
	ctx := context.Background()
	eventID, userID := seedTicketTestData(t)

	created, err := repo.Create(ctx, &entity.NewTicket{
		EventID: eventID,
		UserID:  userID,
		TokenID: 77,
		TxHash:  "0x7777",
	})
	require.NoError(t, err)

	tests := []struct {
		name    string
		eventID string
		userID  string
		wantErr error
	}{
		{
			name:    "existing combination",
			eventID: eventID,
			userID:  userID,
			wantErr: nil,
		},
		{
			name:    "non-existent combination",
			eventID: eventID,
			userID:  "018b2f19-e591-7d12-bf9e-000000000000",
			wantErr: apperr.ErrNotFound,
		},
		{
			name:    "empty event ID",
			eventID: "",
			userID:  userID,
			wantErr: apperr.ErrInvalidArgument,
		},
		{
			name:    "empty user ID",
			eventID: eventID,
			userID:  "",
			wantErr: apperr.ErrInvalidArgument,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ticket, err := repo.GetByEventAndUser(ctx, tc.eventID, tc.userID)
			if tc.wantErr != nil {
				require.Error(t, err)
				assert.ErrorIs(t, err, tc.wantErr)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, created.ID, ticket.ID)
			assert.Equal(t, created.TokenID, ticket.TokenID)
		})
	}
}

func TestTicketRepository_ListByUser(t *testing.T) {
	cleanDatabase()
	repo := rdb.NewTicketRepository(testDB)
	ctx := context.Background()
	eventID, userID := seedTicketTestData(t)

	// Create a second event for the same venue.
	var eventID2 string
	err := testDB.Pool.QueryRow(ctx,
		`INSERT INTO events (venue_id, title, local_event_date)
		 SELECT venue_id, 'second event', '2026-04-01' FROM events WHERE id = $1 RETURNING id`,
		eventID,
	).Scan(&eventID2)
	require.NoError(t, err)

	_, err = repo.Create(ctx, &entity.NewTicket{EventID: eventID, UserID: userID, TokenID: 1, TxHash: "0x1"})
	require.NoError(t, err)
	_, err = repo.Create(ctx, &entity.NewTicket{EventID: eventID2, UserID: userID, TokenID: 2, TxHash: "0x2"})
	require.NoError(t, err)

	tests := []struct {
		name      string
		userID    string
		wantCount int
		wantErr   error
	}{
		{
			name:      "user with two tickets",
			userID:    userID,
			wantCount: 2,
			wantErr:   nil,
		},
		{
			name:      "user with no tickets",
			userID:    "018b2f19-e591-7d12-bf9e-000000000000",
			wantCount: 0,
			wantErr:   nil,
		},
		{
			name:      "empty user ID",
			userID:    "",
			wantCount: 0,
			wantErr:   apperr.ErrInvalidArgument,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tickets, err := repo.ListByUser(ctx, tc.userID)
			if tc.wantErr != nil {
				require.Error(t, err)
				assert.ErrorIs(t, err, tc.wantErr)
				return
			}

			require.NoError(t, err)
			assert.Len(t, tickets, tc.wantCount)
		})
	}
}

func TestTicketRepository_EventExists(t *testing.T) {
	cleanDatabase()
	repo := rdb.NewTicketRepository(testDB)
	ctx := context.Background()
	eventID, _ := seedTicketTestData(t)

	tests := []struct {
		name    string
		eventID string
		want    bool
		wantErr error
	}{
		{
			name:    "existing event",
			eventID: eventID,
			want:    true,
			wantErr: nil,
		},
		{
			name:    "non-existent event",
			eventID: "018b2f19-e591-7d12-bf9e-000000000000",
			want:    false,
			wantErr: nil,
		},
		{
			name:    "empty event ID",
			eventID: "",
			want:    false,
			wantErr: apperr.ErrInvalidArgument,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			exists, err := repo.EventExists(ctx, tc.eventID)
			if tc.wantErr != nil {
				require.Error(t, err)
				assert.ErrorIs(t, err, tc.wantErr)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tc.want, exists)
		})
	}
}
