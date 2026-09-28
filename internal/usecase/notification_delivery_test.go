package usecase_test

// notificationRequestMsgID is unexported, so its determinism is exercised
// indirectly through the producers that call it (PushNotificationUseCase.
// NotifyNewConcerts, SalesPhaseAnnouncementUseCase.AnnounceDiscoveredPhase):
// this test asserts the id EventPublisher.PublishEventWithID actually
// receives is stable across repeated calls with the same business keys, and
// differs when the recipient differs — the property NATS Msg-Id
// deduplication (see infrastructure/messaging/streams.go's Duplicates
// window) depends on.

import (
	"context"
	"testing"

	"github.com/liverty-music/backend/internal/entity"
	entitymocks "github.com/liverty-music/backend/internal/entity/mocks"
	"github.com/liverty-music/backend/internal/usecase"
	ucmocks "github.com/liverty-music/backend/internal/usecase/mocks"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// TestAnnounceDiscoveredPhase_RequestIDIsDeterministic verifies that
// publishing NOTIFICATION.requested for the same phase and the same
// recipient twice (simulating an at-least-once redelivery of
// SALES_PHASE.discovered) derives the identical id both times, and that a
// different recipient derives a different id — so an accidental collision
// wouldn't silently swallow a distinct recipient's request.
func TestAnnounceDiscoveredPhase_RequestIDIsDeterministic(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	runOnce := func(t *testing.T, userIDs []string) []string {
		t.Helper()
		userRepo := entitymocks.NewMockUserRepository(t)
		journeyRepo := entitymocks.NewMockTicketJourneyRepository(t)
		concertRepo := entitymocks.NewMockConcertRepository(t)
		publisher := ucmocks.NewMockEventPublisher(t)
		uc := usecase.NewSalesPhaseAnnouncementUseCase(userRepo, journeyRepo, concertRepo, publisher, newTestLogger(t))

		journeyRepo.EXPECT().ListUserIDsTrackingSeries(ctx, "series-1").Return(userIDs, nil).Once()
		concertRepo.EXPECT().ListEventsBySeries(ctx, "series-1").Return(nil, nil).Once()
		for _, uid := range userIDs {
			userRepo.EXPECT().Get(ctx, uid).Return(&entity.User{ID: uid}, nil).Once()
		}

		var ids []string
		publisher.EXPECT().
			PublishEventWithID(mock.Anything, entity.SubjectNotificationRequested, mock.AnythingOfType("string"), mock.AnythingOfType("entity.NotificationRequestedData")).
			RunAndReturn(func(_ context.Context, _ string, id string, _ any) error {
				ids = append(ids, id)
				return nil
			}).
			Times(len(userIDs))

		require.NoError(t, uc.AnnounceDiscoveredPhase(ctx, entity.SalesPhaseDiscoveredData{SeriesID: "series-1", PhaseID: "phase-1"}))
		return ids
	}

	firstRun := runOnce(t, []string{"user-1"})
	secondRun := runOnce(t, []string{"user-1"})
	require.Equal(t, firstRun, secondRun, "same phase + same recipient must derive the same id across redeliveries")

	differentRecipient := runOnce(t, []string{"user-2"})
	require.NotEqual(t, firstRun, differentRecipient, "different recipient must derive a different id")
}
