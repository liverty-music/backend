package rdb_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// seedTicketEmailTestData inserts a user, artist, venue, and event required by the
// ticket_emails FK constraints. It returns (userID, eventID).
func seedTicketEmailTestData(t *testing.T) (userID, eventID string) {
	t.Helper()
	userID = seedUser(t, "ticket-email-user", "ticket-email@example.com", uuid.Must(uuid.NewV7()).String())
	artistID := seedArtist(t, "ticket-email-artist", uuid.Must(uuid.NewV7()).String())
	venueID := seedVenue(t, "ticket-email-venue")
	eventID = seedEvent(t, venueID, artistID, "ticket-email-event", "2026-06-01")
	return userID, eventID
}

func TestTicketEmailRepository_Create(t *testing.T) {
	repo := rdb.NewTicketEmailRepository(testDB)
	ctx := context.Background()

	type args struct {
		params *entity.NewTicketEmail
	}

	tests := []struct {
		name    string
		setup   func() (userID, eventID string)
		args    func(userID, eventID string) args
		wantErr error
	}{
		{
			name: "creates ticket email with all fields populated",
			setup: func() (string, string) {
				cleanDatabase(t)
				return seedTicketEmailTestData(t)
			},
			args: func(userID, eventID string) args {
				deadline := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
				start := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
				end := time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC)
				status := entity.TicketJourneyStatusApplied
				return args{
					params: &entity.NewTicketEmail{
						UserID:              userID,
						EventID:             eventID,
						EmailType:           entity.TicketEmailTypeLotteryInfo,
						RawBody:             "lottery info email body",
						ParsedData:          json.RawMessage(`{"seats":2}`),
						PaymentDeadlineTime: &deadline,
						LotteryStartTime:    &start,
						LotteryEndTime:      &end,
						ApplicationURL:      "https://example.com/apply",
						JourneyStatus:       &status,
					},
				}
			},
			wantErr: nil,
		},
		{
			name: "creates ticket email with minimal fields",
			setup: func() (string, string) {
				cleanDatabase(t)
				return seedTicketEmailTestData(t)
			},
			args: func(userID, eventID string) args {
				return args{
					params: &entity.NewTicketEmail{
						UserID:     userID,
						EventID:    eventID,
						EmailType:  entity.TicketEmailTypeLotteryResult,
						RawBody:    "lottery result email body",
						ParsedData: json.RawMessage(`{}`),
					},
				}
			},
			wantErr: nil,
		},
		{
			name: "nil params returns invalid argument",
			setup: func() (string, string) {
				return "", ""
			},
			args: func(_, _ string) args {
				return args{params: nil}
			},
			wantErr: apperr.ErrInvalidArgument,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			userID, eventID := tt.setup()
			a := tt.args(userID, eventID)

			got, err := repo.Create(ctx, a.params)

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				return
			}

			require.NoError(t, err)
			assert.NotEmpty(t, got.ID)
			assert.Equal(t, a.params.UserID, got.UserID)
			assert.Equal(t, a.params.EventID, got.EventID)
			assert.Equal(t, a.params.EmailType, got.EmailType)
			assert.Equal(t, a.params.RawBody, got.RawBody)
			assert.Equal(t, a.params.ApplicationURL, got.ApplicationURL)
		})
	}
}

func TestTicketEmailRepository_Create_UUIDGenerated(t *testing.T) {
	cleanDatabase(t)
	repo := rdb.NewTicketEmailRepository(testDB)
	ctx := context.Background()

	userID, eventID := seedTicketEmailTestData(t)

	got, err := repo.Create(ctx, &entity.NewTicketEmail{
		UserID:     userID,
		EventID:    eventID,
		EmailType:  entity.TicketEmailTypeLotteryInfo,
		RawBody:    "body",
		ParsedData: json.RawMessage(`{}`),
	})

	require.NoError(t, err)
	assert.NotEmpty(t, got.ID)

	// Verify the ID is a valid UUID string.
	_, err = uuid.Parse(got.ID)
	assert.NoError(t, err, "created ID should be a valid UUID")
}

func TestTicketEmailRepository_Update(t *testing.T) {
	repo := rdb.NewTicketEmailRepository(testDB)
	ctx := context.Background()

	appURL := "https://example.com/apply"
	status := entity.TicketJourneyStatusApplied

	tests := []struct {
		name    string
		setup   func() string // returns ticket email ID
		params  *entity.UpdateTicketEmail
		wantErr error
	}{
		{
			name: "updates application_url and journey_status",
			setup: func() string {
				cleanDatabase(t)
				userID, eventID := seedTicketEmailTestData(t)
				created, err := repo.Create(ctx, &entity.NewTicketEmail{
					UserID:     userID,
					EventID:    eventID,
					EmailType:  entity.TicketEmailTypeLotteryInfo,
					RawBody:    "body",
					ParsedData: json.RawMessage(`{}`),
				})
				require.NoError(t, err)
				return created.ID
			},
			params: &entity.UpdateTicketEmail{
				ApplicationURL: &appURL,
				JourneyStatus:  &status,
			},
			wantErr: nil,
		},
		{
			name: "partial update — only deadline, other fields remain unchanged",
			setup: func() string {
				cleanDatabase(t)
				userID, eventID := seedTicketEmailTestData(t)
				existingURL := "https://example.com/existing"
				existingStatus := entity.TicketJourneyStatusTracking
				created, err := repo.Create(ctx, &entity.NewTicketEmail{
					UserID:         userID,
					EventID:        eventID,
					EmailType:      entity.TicketEmailTypeLotteryResult,
					RawBody:        "body",
					ParsedData:     json.RawMessage(`{}`),
					ApplicationURL: existingURL,
					JourneyStatus:  &existingStatus,
				})
				require.NoError(t, err)
				return created.ID
			},
			params: &entity.UpdateTicketEmail{
				PaymentDeadlineTime: new(time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)),
			},
			wantErr: nil,
		},
		{
			name: "nil params returns invalid argument",
			setup: func() string {
				return uuid.Must(uuid.NewV7()).String()
			},
			params:  nil,
			wantErr: apperr.ErrInvalidArgument,
		},
		{
			name: "update non-existent ID returns not found",
			setup: func() string {
				cleanDatabase(t)
				return uuid.Must(uuid.NewV7()).String()
			},
			params: &entity.UpdateTicketEmail{
				ApplicationURL: &appURL,
			},
			wantErr: apperr.ErrNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id := tt.setup()

			got, err := repo.Update(ctx, id, tt.params)

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, id, got.ID)
		})
	}
}

func TestTicketEmailRepository_Update_PartialFieldsUnchanged(t *testing.T) {
	cleanDatabase(t)
	repo := rdb.NewTicketEmailRepository(testDB)
	ctx := context.Background()

	userID, eventID := seedTicketEmailTestData(t)
	originalURL := "https://example.com/original"
	originalStatus := entity.TicketJourneyStatusTracking

	created, err := repo.Create(ctx, &entity.NewTicketEmail{
		UserID:         userID,
		EventID:        eventID,
		EmailType:      entity.TicketEmailTypeLotteryInfo,
		RawBody:        "body",
		ParsedData:     json.RawMessage(`{}`),
		ApplicationURL: originalURL,
		JourneyStatus:  &originalStatus,
	})
	require.NoError(t, err)

	// Update only the payment deadline — application_url and journey_status must be preserved.
	deadline := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	got, err := repo.Update(ctx, created.ID, &entity.UpdateTicketEmail{
		PaymentDeadlineTime: &deadline,
	})

	require.NoError(t, err)
	assert.Equal(t, originalURL, got.ApplicationURL)
	require.NotNil(t, got.JourneyStatus)
	assert.Equal(t, originalStatus, *got.JourneyStatus)
	require.NotNil(t, got.PaymentDeadlineTime)
	assert.Equal(t, deadline.UTC(), got.PaymentDeadlineTime.UTC())
}

func TestTicketEmailRepository_GetByID(t *testing.T) {
	repo := rdb.NewTicketEmailRepository(testDB)
	ctx := context.Background()

	tests := []struct {
		name    string
		setup   func() string // returns ticket email ID
		wantErr error
	}{
		{
			name: "returns existing ticket email",
			setup: func() string {
				cleanDatabase(t)
				userID, eventID := seedTicketEmailTestData(t)
				created, err := repo.Create(ctx, &entity.NewTicketEmail{
					UserID:     userID,
					EventID:    eventID,
					EmailType:  entity.TicketEmailTypeLotteryInfo,
					RawBody:    "body",
					ParsedData: json.RawMessage(`{}`),
				})
				require.NoError(t, err)
				return created.ID
			},
			wantErr: nil,
		},
		{
			name: "non-existent ID returns not found",
			setup: func() string {
				cleanDatabase(t)
				return uuid.Must(uuid.NewV7()).String()
			},
			wantErr: apperr.ErrNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id := tt.setup()

			got, err := repo.GetByID(ctx, id)

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, id, got.ID)
		})
	}
}

func TestTicketEmailRepository_ListByUserAndEvent(t *testing.T) {
	repo := rdb.NewTicketEmailRepository(testDB)
	ctx := context.Background()

	tests := []struct {
		name      string
		setup     func() (userID, eventID string)
		wantCount int
		wantErr   error
	}{
		{
			name: "returns matching records for user and event",
			setup: func() (string, string) {
				cleanDatabase(t)
				userID, eventID := seedTicketEmailTestData(t)
				_, err := repo.Create(ctx, &entity.NewTicketEmail{
					UserID:     userID,
					EventID:    eventID,
					EmailType:  entity.TicketEmailTypeLotteryInfo,
					RawBody:    "lottery info body",
					ParsedData: json.RawMessage(`{}`),
				})
				require.NoError(t, err)
				return userID, eventID
			},
			wantCount: 1,
			wantErr:   nil,
		},
		{
			name: "returns empty slice when no records match",
			setup: func() (string, string) {
				cleanDatabase(t)
				userID, eventID := seedTicketEmailTestData(t)
				// Do not insert any ticket emails.
				return userID, eventID
			},
			wantCount: 0,
			wantErr:   nil,
		},
		{
			name: "returns all records when multiple emails exist for same user and event",
			setup: func() (string, string) {
				cleanDatabase(t)
				userID, eventID := seedTicketEmailTestData(t)
				_, err := repo.Create(ctx, &entity.NewTicketEmail{
					UserID:     userID,
					EventID:    eventID,
					EmailType:  entity.TicketEmailTypeLotteryInfo,
					RawBody:    "lottery info body",
					ParsedData: json.RawMessage(`{}`),
				})
				require.NoError(t, err)
				_, err = repo.Create(ctx, &entity.NewTicketEmail{
					UserID:     userID,
					EventID:    eventID,
					EmailType:  entity.TicketEmailTypeLotteryResult,
					RawBody:    "lottery result body",
					ParsedData: json.RawMessage(`{}`),
				})
				require.NoError(t, err)
				return userID, eventID
			},
			wantCount: 2,
			wantErr:   nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			userID, eventID := tt.setup()

			got, err := repo.ListByUserAndEvent(ctx, userID, eventID)

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				return
			}

			require.NoError(t, err)
			assert.Len(t, got, tt.wantCount)
		})
	}
}
