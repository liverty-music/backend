package usecase_test

// A trigger delivered twice (an at-least-once redelivery of
// TICKET_SALE.discovered or CONCERT.created) starts the deliver-notification
// workflow twice under the same id, so the recipient is notified once. These
// tests run the producers twice over a Workflows mock and read the ids.

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/pannpers/go-apperr/apperr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/liverty-music/backend/internal/entity"
	entitymocks "github.com/liverty-music/backend/internal/entity/mocks"
	"github.com/liverty-music/backend/internal/usecase"
	ucmocks "github.com/liverty-music/backend/internal/usecase/mocks"
)

// workflowStart is one Workflows.Start call a test captured.
type workflowStart struct {
	workflow string
	id       string
	input    any
}

// captureStarts makes w accept any number of Start calls and returns a
// function listing them in the order they were made.
func captureStarts(w *ucmocks.MockWorkflows) func() []workflowStart {
	var (
		mu     sync.Mutex
		starts []workflowStart
	)
	w.EXPECT().Start(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, workflow, id string, input any, _ ...usecase.StartOption) error {
			mu.Lock()
			defer mu.Unlock()
			starts = append(starts, workflowStart{workflow: workflow, id: id, input: input})
			return nil
		}).Maybe()
	return func() []workflowStart {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(starts)
	}
}

// TestAnnounceDiscoveredSale_SameSaleTwice verifies that announcing the same
// sale twice for the same recipient starts one deliver-notification workflow.
//
// @spec components/usecase/ticket-sale/announce-discovered-sale "Same sale announced twice"
func TestAnnounceDiscoveredSale_SameSaleTwice(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	userRepo := entitymocks.NewMockUserRepository(t)
	journeyRepo := entitymocks.NewMockTicketJourneyRepository(t)
	w := ucmocks.NewMockWorkflows(t)
	uc := usecase.NewTicketSaleAnnouncementUseCase(userRepo, journeyRepo, w, newTestLogger(t))

	journeyRepo.EXPECT().ListUserIDsTrackingSeries(ctx, "series-1").
		Return([]*entity.SeriesTracker{{UserID: "user-1", EventID: "event-1"}}, nil).Twice()
	userRepo.EXPECT().Get(ctx, "user-1").Return(&entity.User{ID: "user-1"}, nil).Twice()

	// Both starts carry one id, so the workflow runtime runs one workflow:
	// the recipient is notified once.
	w.EXPECT().Start(ctx, usecase.WorkflowDeliverNotification, "deliver:ticket_sale_announcement:user-1:sale-1", mock.Anything).
		Return(nil).Twice()

	sale := entity.TicketSaleDiscoveredData{SeriesID: "series-1", TicketSaleID: "sale-1"}
	require.NoError(t, uc.AnnounceDiscoveredSale(ctx, sale))
	require.NoError(t, uc.AnnounceDiscoveredSale(ctx, sale))
}

// newConcertsUC builds a NotificationUseCase for NotifyNewConcerts over w,
// with one follower of artist-1 at Away and one concert c1.
func newConcertsUC(t *testing.T, ctx context.Context, w usecase.Workflows, calls int) usecase.NotificationUseCase {
	t.Helper()
	artistRepo := entitymocks.NewMockArtistRepository(t)
	concertRepo := entitymocks.NewMockConcertRepository(t)
	followRepo := entitymocks.NewMockFollowRepository(t)
	area := "JP-13"
	artistRepo.EXPECT().Get(ctx, "artist-1").Return(&entity.Artist{ID: "artist-1", Name: "Test Artist"}, nil).Times(calls)
	concertRepo.EXPECT().ListByIDs(ctx, []string{"c1"}).Return([]*entity.Concert{
		{ID: "c1", Venue: &entity.Venue{AdminArea: &area}, Artists: []*entity.Artist{{ID: "artist-1"}}},
	}, nil).Times(calls)
	followRepo.EXPECT().ListFollowers(ctx, "artist-1").Return([]*entity.Follower{
		{ArtistID: "artist-1", User: &entity.User{ID: "user-1"}, Hype: entity.HypeAway},
	}, nil).Times(calls)
	return usecase.NewNotificationUseCase(nil, entitymocks.NewMockPushSubscriptionRepository(t), nil, w, anyPushMetrics(t), newTestLogger(t), usecase.OrderConfirmationDeps{Now: time.Now},
		usecase.NewConcertsDeps{ArtistRepo: artistRepo, ConcertRepo: concertRepo, FollowRepo: followRepo}, usecase.SubjectDeps{Now: time.Now})
}

// @spec components/usecase/notification/notify-new-concerts "Same concerts notified twice"
func TestNotifyNewConcerts_SameConcertsTwice(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := ucmocks.NewMockWorkflows(t)
	uc := newConcertsUC(t, ctx, w, 2)
	// Both starts carry one id, so the workflow runtime runs one workflow:
	// the follower is notified once.
	w.EXPECT().Start(ctx, usecase.WorkflowDeliverNotification, "deliver:new_concerts:user-1:announcement-1", mock.Anything).
		Return(nil).Twice()

	data := usecase.ConcertCreatedData{ArtistID: "artist-1", ConcertIDs: []string{"c1"}, RequestID: "announcement-1"}
	require.NoError(t, uc.NotifyNewConcerts(ctx, data))
	require.NoError(t, uc.NotifyNewConcerts(ctx, data))
}

// @spec components/usecase/notification/notify-new-concerts "Admin sends again"
func TestNotifyNewConcerts_AdminSendsAgain(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := ucmocks.NewMockWorkflows(t)
	starts := captureStarts(w)
	uc := newConcertsUC(t, ctx, w, 2)

	data := usecase.ConcertCreatedData{ArtistID: "artist-1", ConcertIDs: []string{"c1"}}
	require.NoError(t, uc.NotifyNewConcerts(ctx, data))
	require.NoError(t, uc.NotifyNewConcerts(ctx, data))

	started := starts()
	require.Len(t, started, 2)
	assert.NotEqual(t, started[0].id, started[1].id, "each direct call notifies again")

	// The two messages carry one tag, so the new one replaces the earlier one
	// on the follower's devices.
	f := newSubjectFixture(t)
	f.fanReadable(renderedFan("user-1", "ja"))
	f.artists.EXPECT().Get(context.Background(), "artist-1").Return(&entity.Artist{ID: "artist-1", Name: "Test Artist"}, nil).Twice()
	concert := renderedConcert("c1")
	f.concertsReadable(concert)
	for _, start := range started {
		req := start.input.(usecase.DeliverRequest)
		assert.Equal(t, entity.NotificationSubject{ArtistID: "artist-1", EventIDs: []string{"c1"}}, req.Subject)
		assert.Equal(t, "concert-artist-1", f.push(t, req).Tag)
	}
}

// @spec components/usecase/notification/notify-new-concerts "Two concerts added for an artist with ten upcoming"
func TestNotifyNewConcerts_CountsOnlyTheGivenConcerts(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	artistRepo := entitymocks.NewMockArtistRepository(t)
	concertRepo := entitymocks.NewMockConcertRepository(t)
	followRepo := entitymocks.NewMockFollowRepository(t)
	w := ucmocks.NewMockWorkflows(t)
	uc := usecase.NewNotificationUseCase(nil, entitymocks.NewMockPushSubscriptionRepository(t), nil, w, anyPushMetrics(t), newTestLogger(t), usecase.OrderConfirmationDeps{Now: time.Now},
		usecase.NewConcertsDeps{ArtistRepo: artistRepo, ConcertRepo: concertRepo, FollowRepo: followRepo}, usecase.SubjectDeps{Now: time.Now})
	area := "JP-13"
	artist := &entity.Artist{ID: "artist-1", Name: "Test Artist"}
	artistRepo.EXPECT().Get(ctx, "artist-1").Return(artist, nil).Once()
	// Only the two added concerts are read; the artist's ten other upcoming
	// concerts are never listed.
	concertRepo.EXPECT().ListByIDs(ctx, []string{"c1", "c2"}).Return([]*entity.Concert{
		{ID: "c1", Venue: &entity.Venue{AdminArea: &area}, Artists: []*entity.Artist{artist}},
		{ID: "c2", Venue: &entity.Venue{AdminArea: &area}, Artists: []*entity.Artist{artist}},
	}, nil).Once()
	followRepo.EXPECT().ListFollowers(ctx, "artist-1").Return([]*entity.Follower{
		{ArtistID: "artist-1", User: &entity.User{ID: "user-1", PreferredLanguage: "en"}, Hype: entity.HypeAway},
	}, nil).Once()

	w.EXPECT().Start(ctx, usecase.WorkflowDeliverNotification, "deliver:new_concerts:user-1:a-1", usecase.DeliverRequest{
		UserID:  "user-1",
		Type:    entity.NotificationTypeNewConcerts,
		Subject: entity.NotificationSubject{ArtistID: "artist-1", EventIDs: []string{"c1", "c2"}},
	}).Return(nil).Once()

	require.NoError(t, uc.NotifyNewConcerts(ctx, usecase.ConcertCreatedData{ArtistID: "artist-1", ConcertIDs: []string{"c1", "c2"}, RequestID: "a-1"}))
}

// @spec components/usecase/notification/notify-new-concerts "Unknown artist"
func TestNotifyNewConcerts_UnknownArtist(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	artistRepo := entitymocks.NewMockArtistRepository(t)
	// w expects no Start: nothing is sent.
	w := ucmocks.NewMockWorkflows(t)
	uc := usecase.NewNotificationUseCase(nil, nil, nil, w, anyPushMetrics(t), newTestLogger(t), usecase.OrderConfirmationDeps{Now: time.Now},
		usecase.NewConcertsDeps{ArtistRepo: artistRepo, ConcertRepo: entitymocks.NewMockConcertRepository(t), FollowRepo: entitymocks.NewMockFollowRepository(t)}, usecase.SubjectDeps{Now: time.Now})
	artistRepo.EXPECT().Get(ctx, "missing").Return(nil, apperr.ErrNotFound).Once()

	err := uc.NotifyNewConcerts(ctx, usecase.ConcertCreatedData{ArtistID: "missing", ConcertIDs: []string{"c1"}})

	assert.ErrorIs(t, err, apperr.ErrNotFound)
}
