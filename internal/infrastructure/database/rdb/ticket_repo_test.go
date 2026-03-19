package rdb_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// seedTicketTestData inserts a user, artist, venue, and event needed by the tickets table FK constraints.
// Returns (eventID, userID).
func seedTicketTestData(t *testing.T) (string, string) {
	t.Helper()
	userID := seedUser(t, "ticket-test-user", "ticket-test@example.com", "018b2f19-e591-7d12-bf9e-f0e74f1b4900")
	artistID := seedArtist(t, "ticket-test-artist", uuid.Must(uuid.NewV7()).String())
	venueID := seedVenue(t, "ticket-test-venue")
	eventID := seedEvent(t, venueID, artistID, "ticket-test-event", "2026-03-01")
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

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ticket, err := repo.Create(ctx, tt.args)
			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				return
			}

			require.NoError(t, err)
			assert.NotEmpty(t, ticket.ID)
			assert.Equal(t, tt.args.EventID, ticket.EventID)
			assert.Equal(t, tt.args.UserID, ticket.UserID)
			assert.Equal(t, tt.args.TokenID, ticket.TokenID)
			assert.Equal(t, tt.args.TxHash, ticket.TxHash)
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

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ticket, err := repo.Get(ctx, tt.args)
			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
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

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ticket, err := repo.GetByEventAndUser(ctx, tt.eventID, tt.userID)
			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
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

	// Create a second event using shared seed helpers.
	artistID2 := seedArtist(t, "list-user-artist-2", uuid.Must(uuid.NewV7()).String())
	venueID2 := seedVenue(t, "list-user-venue-2")
	eventID2 := seedEvent(t, venueID2, artistID2, "second event", "2026-04-01")

	_, err := repo.Create(ctx, &entity.NewTicket{EventID: eventID, UserID: userID, TokenID: 1, TxHash: "0x1"})
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

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tickets, err := repo.ListByUser(ctx, tt.userID)
			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				return
			}

			require.NoError(t, err)
			assert.Len(t, tickets, tt.wantCount)
		})
	}
}

func TestTicketRepository_ListByEvent(t *testing.T) {
	cleanDatabase()
	repo := rdb.NewTicketRepository(testDB)
	ctx := context.Background()
	eventID, userID := seedTicketTestData(t)

	// Create a second user.
	userID2 := seedUser(t, "list-by-event-user2", "list-by-event2@example.com", "ext-list-by-event-02")

	_, err := repo.Create(ctx, &entity.NewTicket{EventID: eventID, UserID: userID, TokenID: 10, TxHash: "0xa"})
	require.NoError(t, err)
	_, err = repo.Create(ctx, &entity.NewTicket{EventID: eventID, UserID: userID2, TokenID: 20, TxHash: "0xb"})
	require.NoError(t, err)

	t.Run("returns tickets ordered by minted_at ASC", func(t *testing.T) {
		tickets, err := repo.ListByEvent(ctx, eventID)
		require.NoError(t, err)
		require.Len(t, tickets, 2)

		// First minted should be first in the list (ASC order).
		assert.Equal(t, userID, tickets[0].UserID)
		assert.Equal(t, userID2, tickets[1].UserID)
	})

	t.Run("empty event returns empty list", func(t *testing.T) {
		tickets, err := repo.ListByEvent(ctx, "018b2f19-e591-7d12-bf9e-000000000000")
		require.NoError(t, err)
		assert.Empty(t, tickets)
	})

	t.Run("empty event ID returns error", func(t *testing.T) {
		_, err := repo.ListByEvent(ctx, "")
		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
	})
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

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exists, err := repo.EventExists(ctx, tt.eventID)
			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.want, exists)
		})
	}
}
