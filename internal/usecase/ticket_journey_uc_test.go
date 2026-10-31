package usecase_test

import (
	"context"
	"testing"

	"github.com/pannpers/go-apperr/apperr"
	"github.com/stretchr/testify/assert"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/entity/mocks"
	"github.com/liverty-music/backend/internal/usecase"
	ucmocks "github.com/liverty-music/backend/internal/usecase/mocks"
)

type ticketJourneyTestDeps struct {
	repo      *mocks.MockTicketJourneyRepository
	publisher *ucmocks.MockEventPublisher
	uc        usecase.TicketJourneyUseCase
}

func newTicketJourneyTestDeps(t *testing.T) *ticketJourneyTestDeps {
	t.Helper()
	d := &ticketJourneyTestDeps{
		repo:      mocks.NewMockTicketJourneyRepository(t),
		publisher: ucmocks.NewMockEventPublisher(t),
	}
	d.uc = usecase.NewTicketJourneyUseCase(d.repo, newTestLogger(t))
	return d
}

func TestTicketJourneyUseCase_SetStatus(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	journey := func(s entity.TicketJourneyStatus) *entity.TicketJourney {
		return &entity.TicketJourney{UserID: "user-1", EventID: "event-1", Status: s}
	}

	tests := []struct {
		name    string
		status  entity.TicketJourneyStatus
		upsert  error
		wantErr error
	}{
		{
			// @spec components/usecase/ticket-journey/set-status "First status for an event"
			name:   "first status for an event",
			status: entity.TicketJourneyStatusTracking,
		},
		{
			// @spec components/usecase/ticket-journey/set-status "Changing an existing status"
			name:   "changing an existing status",
			status: entity.TicketJourneyStatusUnpaid,
		},
		{
			// @spec components/usecase/ticket-journey/set-status "Recording the status fails"
			name:    "recording the status fails",
			status:  entity.TicketJourneyStatusLost,
			upsert:  apperr.ErrUnavailable,
			wantErr: apperr.ErrUnavailable,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			d := newTicketJourneyTestDeps(t)
			// No prior read: the strict mock fails on any Get.
			d.repo.EXPECT().Upsert(ctx, journey(tt.status)).Return(tt.upsert).Once()

			err := d.uc.SetStatus(ctx, "user-1", "event-1", tt.status)

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				return
			}
			assert.NoError(t, err)
		})
	}
}

func TestTicketJourneyUseCase_Delete(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	tests := []struct {
		name    string
		userID  string
		eventID string
		setup   func(t *testing.T, d *ticketJourneyTestDeps)
		wantErr error
	}{
		{
			name:    "successfully deletes journey",
			userID:  "user-1",
			eventID: "event-1",
			setup: func(t *testing.T, d *ticketJourneyTestDeps) {
				t.Helper()
				d.repo.EXPECT().
					Delete(ctx, "user-1", "event-1").
					Return(nil).
					Once()
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			d := newTicketJourneyTestDeps(t)
			tt.setup(t, d)

			err := d.uc.Delete(ctx, tt.userID, tt.eventID)

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				return
			}
			assert.NoError(t, err)
		})
	}
}

func TestTicketJourneyUseCase_ListByUser(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	t.Run("returns journeys for user", func(t *testing.T) {
		t.Parallel()
		d := newTicketJourneyTestDeps(t)

		expected := []*entity.TicketJourney{
			{UserID: "user-1", EventID: "event-1", Status: entity.TicketJourneyStatusTracking},
			{UserID: "user-1", EventID: "event-2", Status: entity.TicketJourneyStatusPaid},
		}
		d.repo.EXPECT().ListByUser(ctx, "user-1").Return(expected, nil).Once()

		result, err := d.uc.List(ctx, "user-1")

		assert.NoError(t, err)
		assert.Equal(t, expected, result)
	})
}
