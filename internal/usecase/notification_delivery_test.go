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
//
// @spec components/usecase/sales-phase/announce-discovered-phase "Same phase announced twice"
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

// TestNotifyNewConcerts_RequestIDIsDeterministic verifies that requesting
// new_concerts notifications twice for the same artist, concerts and follower
// (simulating an at-least-once redelivery of CONCERT.created) derives the
// identical id both times, and that a different follower derives a different
// id.
//
// @spec components/usecase/notification/notify-new-concerts "Same concerts notified twice"
func TestNotifyNewConcerts_RequestIDIsDeterministic(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	runOnce := func(t *testing.T, userID string) []string {
		t.Helper()
		artistRepo := entitymocks.NewMockArtistRepository(t)
		concertRepo := entitymocks.NewMockConcertRepository(t)
		followRepo := entitymocks.NewMockFollowRepository(t)
		pushSubRepo := entitymocks.NewMockPushSubscriptionRepository(t)
		publisher := ucmocks.NewMockEventPublisher(t)
		uc := usecase.NewPushNotificationUseCase(artistRepo, concertRepo, followRepo, pushSubRepo, publisher, newTestLogger(t))

		area := "JP-13"
		artistRepo.EXPECT().Get(ctx, "artist-1").Return(&entity.Artist{ID: "artist-1", Name: "Test Artist"}, nil).Once()
		concertRepo.EXPECT().ListByIDs(ctx, []string{"c1"}).Return([]*entity.Concert{
			{ID: "c1", Venue: &entity.Venue{AdminArea: &area}, Performers: []*entity.Artist{{ID: "artist-1"}}},
		}, nil).Once()
		followRepo.EXPECT().ListFollowers(ctx, "artist-1").Return([]*entity.Follower{
			{ArtistID: "artist-1", User: &entity.User{ID: userID}, Hype: entity.HypeAway},
		}, nil).Once()

		var ids []string
		publisher.EXPECT().
			PublishEventWithID(mock.Anything, entity.SubjectNotificationRequested, mock.AnythingOfType("string"), mock.AnythingOfType("entity.NotificationRequestedData")).
			RunAndReturn(func(_ context.Context, _ string, id string, _ any) error {
				ids = append(ids, id)
				return nil
			}).
			Once()

		require.NoError(t, uc.NotifyNewConcerts(ctx, usecase.ConcertCreatedData{ArtistID: "artist-1", ConcertIDs: []string{"c1"}}))
		return ids
	}

	firstRun := runOnce(t, "user-1")
	secondRun := runOnce(t, "user-1")
	require.Len(t, firstRun, 1)
	require.Equal(t, firstRun, secondRun, "same artist + concerts + follower must derive the same id across redeliveries")

	differentFollower := runOnce(t, "user-2")
	require.NotEqual(t, firstRun, differentFollower, "different follower must derive a different id")
}
