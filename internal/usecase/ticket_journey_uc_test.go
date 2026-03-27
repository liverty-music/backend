package usecase_test

import (
	"context"
	"testing"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/entity/mocks"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

type ticketJourneyTestDeps struct {
	repo *mocks.MockTicketJourneyRepository
	uc   usecase.TicketJourneyUseCase
}

func newTicketJourneyTestDeps(t *testing.T) *ticketJourneyTestDeps {
	t.Helper()
	d := &ticketJourneyTestDeps{
		repo: mocks.NewMockTicketJourneyRepository(t),
	}
	d.uc = usecase.NewTicketJourneyUseCase(d.repo, newTestLogger(t))
	return d
}

func TestTicketJourneyUseCase_SetStatus(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	tests := []struct {
		name    string
		userID  string
		eventID string
		status  entity.TicketJourneyStatus
		setup   func(t *testing.T, d *ticketJourneyTestDeps)
		wantErr error
	}{
		{
			name:    "successfully sets status",
			userID:  "user-1",
			eventID: "event-1",
			status:  entity.TicketJourneyStatusTracking,
			setup: func(t *testing.T, d *ticketJourneyTestDeps) {
				t.Helper()
				d.repo.EXPECT().
					Upsert(ctx, mock.MatchedBy(func(j *entity.TicketJourney) bool {
						return j.UserID == "user-1" && j.EventID == "event-1" && j.Status == entity.TicketJourneyStatusTracking
					})).
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

			err := d.uc.SetStatus(ctx, tt.userID, tt.eventID, tt.status)

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

		result, err := d.uc.ListByUser(ctx, "user-1")

		assert.NoError(t, err)
		assert.Equal(t, expected, result)
	})

}
