package usecase_test

import (
	"context"
	"errors"
	"testing"

	"github.com/pannpers/go-apperr/apperr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

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
	d.uc = usecase.NewTicketJourneyUseCase(d.repo, d.publisher, newTestLogger(t))
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
			name:    "publishes status_changed when no prior journey exists (new tracking)",
			userID:  "user-1",
			eventID: "event-1",
			status:  entity.TicketJourneyStatusTracking,
			setup: func(t *testing.T, d *ticketJourneyTestDeps) {
				t.Helper()
				d.repo.EXPECT().
					Get(ctx, "user-1", "event-1").
					Return(nil, apperr.ErrNotFound).
					Once()
				d.repo.EXPECT().
					Upsert(ctx, mock.MatchedBy(func(j *entity.TicketJourney) bool {
						return j.UserID == "user-1" && j.EventID == "event-1" && j.Status == entity.TicketJourneyStatusTracking
					})).
					Return(nil).
					Once()
				d.publisher.EXPECT().
					PublishEvent(ctx, entity.SubjectTicketJourneyStatusChanged, mock.MatchedBy(func(data entity.TicketJourneyStatusChangedData) bool {
						return data.UserID == "user-1" &&
							data.EventID == "event-1" &&
							data.FromStatus == "UNSPECIFIED" &&
							data.ToStatus == "TRACKING"
					})).
					Return(nil).
					Once()
			},
		},
		{
			name:    "publishes status_changed when status transitions from tracking to applied",
			userID:  "user-1",
			eventID: "event-1",
			status:  entity.TicketJourneyStatusApplied,
			setup: func(t *testing.T, d *ticketJourneyTestDeps) {
				t.Helper()
				d.repo.EXPECT().
					Get(ctx, "user-1", "event-1").
					Return(&entity.TicketJourney{
						UserID:  "user-1",
						EventID: "event-1",
						Status:  entity.TicketJourneyStatusTracking,
					}, nil).
					Once()
				d.repo.EXPECT().
					Upsert(ctx, mock.Anything).
					Return(nil).
					Once()
				d.publisher.EXPECT().
					PublishEvent(ctx, entity.SubjectTicketJourneyStatusChanged, mock.MatchedBy(func(data entity.TicketJourneyStatusChangedData) bool {
						return data.FromStatus == "TRACKING" && data.ToStatus == "APPLIED"
					})).
					Return(nil).
					Once()
			},
		},
		{
			name:    "does not publish when status is unchanged (no-op upsert)",
			userID:  "user-1",
			eventID: "event-1",
			status:  entity.TicketJourneyStatusTracking,
			setup: func(t *testing.T, d *ticketJourneyTestDeps) {
				t.Helper()
				d.repo.EXPECT().
					Get(ctx, "user-1", "event-1").
					Return(&entity.TicketJourney{
						UserID:  "user-1",
						EventID: "event-1",
						Status:  entity.TicketJourneyStatusTracking,
					}, nil).
					Once()
				d.repo.EXPECT().
					Upsert(ctx, mock.Anything).
					Return(nil).
					Once()
				// publisher MUST NOT be called — no EXPECT() registered
			},
		},
		{
			name:    "publish error is non-fatal — SetStatus returns nil",
			userID:  "user-2",
			eventID: "event-2",
			status:  entity.TicketJourneyStatusPaid,
			setup: func(t *testing.T, d *ticketJourneyTestDeps) {
				t.Helper()
				d.repo.EXPECT().
					Get(ctx, "user-2", "event-2").
					Return(&entity.TicketJourney{
						UserID:  "user-2",
						EventID: "event-2",
						Status:  entity.TicketJourneyStatusUnpaid,
					}, nil).
					Once()
				d.repo.EXPECT().
					Upsert(ctx, mock.Anything).
					Return(nil).
					Once()
				d.publisher.EXPECT().
					PublishEvent(ctx, entity.SubjectTicketJourneyStatusChanged, mock.Anything).
					Return(errors.New("nats unavailable")).
					Once()
			},
		},
		{
			name:    "returns error when Get fails with non-NotFound error",
			userID:  "user-3",
			eventID: "event-3",
			status:  entity.TicketJourneyStatusTracking,
			setup: func(t *testing.T, d *ticketJourneyTestDeps) {
				t.Helper()
				d.repo.EXPECT().
					Get(ctx, "user-3", "event-3").
					Return(nil, apperr.ErrInternal).
					Once()
			},
			wantErr: apperr.ErrInternal,
		},
		{
			name:    "returns error when Upsert fails",
			userID:  "user-4",
			eventID: "event-4",
			status:  entity.TicketJourneyStatusTracking,
			setup: func(t *testing.T, d *ticketJourneyTestDeps) {
				t.Helper()
				d.repo.EXPECT().
					Get(ctx, "user-4", "event-4").
					Return(nil, apperr.ErrNotFound).
					Once()
				d.repo.EXPECT().
					Upsert(ctx, mock.Anything).
					Return(apperr.ErrInternal).
					Once()
			},
			wantErr: apperr.ErrInternal,
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
