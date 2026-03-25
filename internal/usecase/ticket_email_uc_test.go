package usecase_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/entity/mocks"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// ticketEmailTestDeps holds all dependencies for TicketEmailUseCase tests.
type ticketEmailTestDeps struct {
	emailRepo   *mocks.MockTicketEmailRepository
	journeyRepo *mocks.MockTicketJourneyRepository
	parser      *mocks.MockTicketEmailParser
	uc          usecase.TicketEmailUseCase
}

func newTicketEmailTestDeps(t *testing.T) *ticketEmailTestDeps {
	t.Helper()
	d := &ticketEmailTestDeps{
		emailRepo:   mocks.NewMockTicketEmailRepository(t),
		journeyRepo: mocks.NewMockTicketJourneyRepository(t),
		parser:      mocks.NewMockTicketEmailParser(t),
	}
	d.uc = usecase.NewTicketEmailUseCase(d.emailRepo, d.journeyRepo, d.parser, newTestLogger(t))
	return d
}

// sampleParsedData returns a ParsedEmailData fixture with lottery dates.
func sampleParsedData() *entity.ParsedEmailData {
	appURL := "https://example.com/apply"
	start := "2026-04-01T10:00:00Z"
	end := "2026-04-10T23:59:00Z"
	deadline := "2026-04-15T23:59:00Z"
	return &entity.ParsedEmailData{
		ApplicationURL:  &appURL,
		LotteryStart:    &start,
		LotteryEnd:      &end,
		PaymentDeadline: &deadline,
	}
}

// sampleTicketEmail returns a TicketEmail fixture for a given userID.
func sampleTicketEmail(id, userID, eventID string, emailType entity.TicketEmailType) *entity.TicketEmail {
	return &entity.TicketEmail{
		ID:        id,
		UserID:    userID,
		EventID:   eventID,
		EmailType: emailType,
		RawBody:   "raw email body",
	}
}

func TestTicketEmailUseCase_Create(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	const (
		userID   = "user-1"
		eventID1 = "event-1"
		eventID2 = "event-2"
		rawBody  = "raw email body text"
	)

	tests := []struct {
		name      string
		userID    string
		eventIDs  []string
		emailType entity.TicketEmailType
		rawBody   string
		setup     func(t *testing.T, d *ticketEmailTestDeps)
		wantCount int
		wantErr   error
	}{
		{
			name:      "creates record for each event ID with lottery info email",
			userID:    userID,
			eventIDs:  []string{eventID1, eventID2},
			emailType: entity.TicketEmailTypeLotteryInfo,
			rawBody:   rawBody,
			setup: func(t *testing.T, d *ticketEmailTestDeps) {
				t.Helper()
				parsed := sampleParsedData()
				d.parser.EXPECT().
					Parse(ctx, rawBody, entity.TicketEmailTypeLotteryInfo).
					Return(parsed, nil).
					Once()
				d.emailRepo.EXPECT().
					Create(ctx, mock.AnythingOfType("*entity.NewTicketEmail")).
					Return(sampleTicketEmail("te-1", userID, eventID1, entity.TicketEmailTypeLotteryInfo), nil).
					Once()
				d.emailRepo.EXPECT().
					Create(ctx, mock.AnythingOfType("*entity.NewTicketEmail")).
					Return(sampleTicketEmail("te-2", userID, eventID2, entity.TicketEmailTypeLotteryInfo), nil).
					Once()
			},
			wantCount: 2,
		},
		{
			name:      "creates record for lottery result email",
			userID:    userID,
			eventIDs:  []string{eventID1},
			emailType: entity.TicketEmailTypeLotteryResult,
			rawBody:   rawBody,
			setup: func(t *testing.T, d *ticketEmailTestDeps) {
				t.Helper()
				d.parser.EXPECT().
					Parse(ctx, rawBody, entity.TicketEmailTypeLotteryResult).
					Return(sampleParsedData(), nil).
					Once()
				d.emailRepo.EXPECT().
					Create(ctx, mock.AnythingOfType("*entity.NewTicketEmail")).
					Return(sampleTicketEmail("te-3", userID, eventID1, entity.TicketEmailTypeLotteryResult), nil).
					Once()
			},
			wantCount: 1,
		},
		{
			name:      "rejects invalid email type without calling parser",
			userID:    userID,
			eventIDs:  []string{eventID1},
			emailType: entity.TicketEmailType(99),
			rawBody:   rawBody,
			setup:     func(_ *testing.T, _ *ticketEmailTestDeps) {},
			wantErr:   apperr.ErrInvalidArgument,
		},
		{
			name:      "rejects empty raw body without calling parser",
			userID:    userID,
			eventIDs:  []string{eventID1},
			emailType: entity.TicketEmailTypeLotteryInfo,
			rawBody:   "",
			setup:     func(_ *testing.T, _ *ticketEmailTestDeps) {},
			wantErr:   apperr.ErrInvalidArgument,
		},
		{
			name:      "propagates parser error without persisting",
			userID:    userID,
			eventIDs:  []string{eventID1},
			emailType: entity.TicketEmailTypeLotteryInfo,
			rawBody:   rawBody,
			setup: func(t *testing.T, d *ticketEmailTestDeps) {
				t.Helper()
				d.parser.EXPECT().
					Parse(ctx, rawBody, entity.TicketEmailTypeLotteryInfo).
					Return(nil, apperr.New(codes.Internal, "gemini error")).
					Once()
			},
			wantErr: apperr.ErrInternal,
		},
		{
			name:      "propagates repository error",
			userID:    userID,
			eventIDs:  []string{eventID1},
			emailType: entity.TicketEmailTypeLotteryInfo,
			rawBody:   rawBody,
			setup: func(t *testing.T, d *ticketEmailTestDeps) {
				t.Helper()
				d.parser.EXPECT().
					Parse(ctx, rawBody, entity.TicketEmailTypeLotteryInfo).
					Return(sampleParsedData(), nil).
					Once()
				d.emailRepo.EXPECT().
					Create(ctx, mock.AnythingOfType("*entity.NewTicketEmail")).
					Return(nil, apperr.New(codes.Internal, "db error")).
					Once()
			},
			wantErr: apperr.ErrInternal,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			d := newTicketEmailTestDeps(t)
			tt.setup(t, d)

			got, err := d.uc.Create(ctx, tt.userID, tt.eventIDs, tt.emailType, tt.rawBody)

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Len(t, got, tt.wantCount)
		})
	}
}

func TestTicketEmailUseCase_Update(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	const (
		userID        = "user-1"
		otherUserID   = "user-2"
		ticketEmailID = "te-1"
		eventID       = "event-1"
	)

	tests := []struct {
		name          string
		userID        string
		ticketEmailID string
		params        *entity.UpdateTicketEmail
		setup         func(t *testing.T, d *ticketEmailTestDeps)
		wantErr       error
	}{
		{
			name:          "updates record and triggers journey upsert",
			userID:        userID,
			ticketEmailID: ticketEmailID,
			params:        &entity.UpdateTicketEmail{},
			setup: func(t *testing.T, d *ticketEmailTestDeps) {
				t.Helper()
				existing := sampleTicketEmail(ticketEmailID, userID, eventID, entity.TicketEmailTypeLotteryInfo)
				updated := sampleTicketEmail(ticketEmailID, userID, eventID, entity.TicketEmailTypeLotteryInfo)
				s := entity.TicketJourneyStatusTracking
				updated.JourneyStatus = &s

				d.emailRepo.EXPECT().GetByID(ctx, ticketEmailID).Return(existing, nil).Once()
				d.emailRepo.EXPECT().
					Update(ctx, ticketEmailID, mock.AnythingOfType("*entity.UpdateTicketEmail")).
					Return(updated, nil).
					Once()
				d.journeyRepo.EXPECT().
					Upsert(ctx, mock.MatchedBy(func(j *entity.TicketJourney) bool {
						return j.UserID == userID && j.EventID == eventID
					})).
					Return(nil).
					Once()
			},
		},
		{
			name:          "rejects empty ticket_email_id",
			userID:        userID,
			ticketEmailID: "",
			params:        &entity.UpdateTicketEmail{},
			setup:         func(_ *testing.T, _ *ticketEmailTestDeps) {},
			wantErr:       apperr.ErrInvalidArgument,
		},
		{
			name:          "returns NotFound when record does not exist",
			userID:        userID,
			ticketEmailID: ticketEmailID,
			params:        &entity.UpdateTicketEmail{},
			setup: func(t *testing.T, d *ticketEmailTestDeps) {
				t.Helper()
				d.emailRepo.EXPECT().
					GetByID(ctx, ticketEmailID).
					Return(nil, apperr.New(codes.NotFound, "not found")).
					Once()
			},
			wantErr: apperr.ErrNotFound,
		},
		{
			name:          "returns NotFound when record belongs to different user",
			userID:        userID,
			ticketEmailID: ticketEmailID,
			params:        &entity.UpdateTicketEmail{},
			setup: func(t *testing.T, d *ticketEmailTestDeps) {
				t.Helper()
				existing := sampleTicketEmail(ticketEmailID, otherUserID, eventID, entity.TicketEmailTypeLotteryInfo)
				d.emailRepo.EXPECT().GetByID(ctx, ticketEmailID).Return(existing, nil).Once()
			},
			wantErr: apperr.ErrNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			d := newTicketEmailTestDeps(t)
			tt.setup(t, d)

			got, err := d.uc.Update(ctx, tt.userID, tt.ticketEmailID, tt.params)

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.NotNil(t, got)
		})
	}
}

func TestTicketEmailUseCase_BuildNewTicketEmail(t *testing.T) {
	t.Parallel()

	const (
		userID  = "user-1"
		rawBody = "raw email body"
	)

	t.Run("maps parsed dates and application URL to entity fields", func(t *testing.T) {
		t.Parallel()
		d := newTicketEmailTestDeps(t)

		appURL := "https://example.com/apply"
		lotteryStart := "2026-04-01T10:00:00Z"
		lotteryEnd := "2026-04-10T23:59:00Z"
		paymentDeadline := "2026-04-15T23:59:00Z"

		parsed := &entity.ParsedEmailData{
			ApplicationURL:  &appURL,
			LotteryStart:    &lotteryStart,
			LotteryEnd:      &lotteryEnd,
			PaymentDeadline: &paymentDeadline,
		}
		parsedJSON, err := json.Marshal(parsed)
		require.NoError(t, err)

		got := usecase.ExportedBuildNewTicketEmail(d.uc, userID, entity.TicketEmailTypeLotteryInfo, rawBody, parsedJSON, parsed)

		assert.Equal(t, userID, got.UserID)
		assert.Equal(t, entity.TicketEmailTypeLotteryInfo, got.EmailType)
		assert.Equal(t, rawBody, got.RawBody)
		assert.Equal(t, appURL, got.ApplicationURL)

		wantStart, err := time.Parse(time.RFC3339, lotteryStart)
		require.NoError(t, err)
		require.NotNil(t, got.LotteryStartTime)
		assert.Equal(t, wantStart, *got.LotteryStartTime)

		wantEnd, err := time.Parse(time.RFC3339, lotteryEnd)
		require.NoError(t, err)
		require.NotNil(t, got.LotteryEndTime)
		assert.Equal(t, wantEnd, *got.LotteryEndTime)

		wantDeadline, err := time.Parse(time.RFC3339, paymentDeadline)
		require.NoError(t, err)
		require.NotNil(t, got.PaymentDeadlineTime)
		assert.Equal(t, wantDeadline, *got.PaymentDeadlineTime)
	})

	t.Run("omits time fields when parsed data has none", func(t *testing.T) {
		t.Parallel()
		d := newTicketEmailTestDeps(t)

		parsed := &entity.ParsedEmailData{}
		parsedJSON, err := json.Marshal(parsed)
		require.NoError(t, err)

		got := usecase.ExportedBuildNewTicketEmail(d.uc, userID, entity.TicketEmailTypeLotteryInfo, rawBody, parsedJSON, parsed)

		assert.Nil(t, got.LotteryStartTime)
		assert.Nil(t, got.LotteryEndTime)
		assert.Nil(t, got.PaymentDeadlineTime)
		assert.Empty(t, got.ApplicationURL)
	})
}

func TestTicketEmailUseCase_DetermineJourneyStatus(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		te         *entity.TicketEmail
		wantStatus entity.TicketJourneyStatus
	}{
		{
			name: "returns stored status when JourneyStatus is set",
			te: func() *entity.TicketEmail {
				s := entity.TicketJourneyStatusPaid
				return &entity.TicketEmail{JourneyStatus: &s}
			}(),
			wantStatus: entity.TicketJourneyStatusPaid,
		},
		{
			name: "returns Tracking as default when JourneyStatus is nil",
			te: &entity.TicketEmail{
				EmailType: entity.TicketEmailTypeLotteryInfo,
			},
			wantStatus: entity.TicketJourneyStatusTracking,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			d := newTicketEmailTestDeps(t)

			got := usecase.ExportedDetermineJourneyStatus(d.uc, tt.te)

			assert.Equal(t, tt.wantStatus, got)
		})
	}
}
