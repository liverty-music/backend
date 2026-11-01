package usecase_test

// A trigger delivered twice (an at-least-once redelivery of
// SALES_PHASE.discovered or CONCERT.created) starts the deliver-notification
// workflow twice under the same id, so the recipient is notified once. These
// tests run the producers twice over a recording Workflows and read the ids.

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/pannpers/go-apperr/apperr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/liverty-music/backend/internal/entity"
	entitymocks "github.com/liverty-music/backend/internal/entity/mocks"
	"github.com/liverty-music/backend/internal/usecase"
)

// recordingWorkflows records every Start; a start under an id already
// started starts nothing new, as the workflow runtime does.
type recordingWorkflows struct {
	mu      sync.Mutex
	starts  []string
	started map[string]any
}

func (w *recordingWorkflows) Start(_ context.Context, _, id string, input any, _ ...usecase.StartOption) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.starts = append(w.starts, id)
	if w.started == nil {
		w.started = map[string]any{}
	}
	if _, ok := w.started[id]; !ok {
		w.started[id] = input
	}
	return nil
}

func (w *recordingWorkflows) Send(context.Context, string, string, any) error { return nil }

func (w *recordingWorkflows) Await(context.Context, string, time.Duration, any) error { return nil }

func (w *recordingWorkflows) Cancel(context.Context, string) (bool, error) { return false, nil }

// runs returns the number of distinct workflows started.
func (w *recordingWorkflows) runs() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.started)
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
	seriesRepo := entitymocks.NewMockSeriesRepository(t)
	w := &recordingWorkflows{}
	uc := usecase.NewTicketSaleAnnouncementUseCase(userRepo, journeyRepo, seriesRepo, w, newTestLogger(t))

	journeyRepo.EXPECT().ListUserIDsTrackingSeries(ctx, "series-1").
		Return([]*entity.SeriesTracker{{UserID: "user-1", EventID: "event-1"}}, nil).Twice()
	seriesRepo.EXPECT().Get(ctx, "series-1").Return(&entity.Series{ID: "series-1", Title: "Tour"}, nil).Twice()
	userRepo.EXPECT().Get(ctx, "user-1").Return(&entity.User{ID: "user-1"}, nil).Twice()

	sale := entity.TicketSaleDiscoveredData{SeriesID: "series-1", TicketSaleID: "sale-1"}
	require.NoError(t, uc.AnnounceDiscoveredSale(ctx, sale))
	require.NoError(t, uc.AnnounceDiscoveredSale(ctx, sale))

	assert.Equal(t, []string{"deliver:ticket_sale_announcement:user-1:sale-1", "deliver:ticket_sale_announcement:user-1:sale-1"}, w.starts)
	assert.Equal(t, 1, w.runs(), "the recipient is notified once")
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
	return usecase.NewNotificationUseCase(nil, entitymocks.NewMockPushSubscriptionRepository(t), nil, w, noopMetrics{}, newTestLogger(t), usecase.OrderConfirmationDeps{},
		usecase.NewConcertsDeps{ArtistRepo: artistRepo, ConcertRepo: concertRepo, FollowRepo: followRepo})
}

// @spec components/usecase/notification/notify-new-concerts "Same concerts notified twice"
func TestNotifyNewConcerts_SameConcertsTwice(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := &recordingWorkflows{}
	uc := newConcertsUC(t, ctx, w, 2)

	data := usecase.ConcertCreatedData{ArtistID: "artist-1", ConcertIDs: []string{"c1"}, RequestID: "announcement-1"}
	require.NoError(t, uc.NotifyNewConcerts(ctx, data))
	require.NoError(t, uc.NotifyNewConcerts(ctx, data))

	assert.Equal(t, []string{"deliver:new_concerts:user-1:announcement-1", "deliver:new_concerts:user-1:announcement-1"}, w.starts)
	assert.Equal(t, 1, w.runs(), "the follower is notified once")
}

// @spec components/usecase/notification/notify-new-concerts "Admin sends again"
func TestNotifyNewConcerts_AdminSendsAgain(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := &recordingWorkflows{}
	uc := newConcertsUC(t, ctx, w, 2)

	data := usecase.ConcertCreatedData{ArtistID: "artist-1", ConcertIDs: []string{"c1"}}
	require.NoError(t, uc.NotifyNewConcerts(ctx, data))
	require.NoError(t, uc.NotifyNewConcerts(ctx, data))

	assert.Equal(t, 2, w.runs(), "each direct call notifies again")
	for _, input := range w.started {
		assert.Equal(t, "concert-artist-1", input.(usecase.DeliverRequest).Payload.Tag,
			"the new message replaces the earlier one on the follower's devices")
	}
}

// @spec components/usecase/notification/notify-new-concerts "Two concerts added for an artist with ten upcoming"
func TestNotifyNewConcerts_CountsOnlyTheGivenConcerts(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	artistRepo := entitymocks.NewMockArtistRepository(t)
	concertRepo := entitymocks.NewMockConcertRepository(t)
	followRepo := entitymocks.NewMockFollowRepository(t)
	w := &recordingWorkflows{}
	uc := usecase.NewNotificationUseCase(nil, entitymocks.NewMockPushSubscriptionRepository(t), nil, w, noopMetrics{}, newTestLogger(t), usecase.OrderConfirmationDeps{},
		usecase.NewConcertsDeps{ArtistRepo: artistRepo, ConcertRepo: concertRepo, FollowRepo: followRepo})
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

	require.NoError(t, uc.NotifyNewConcerts(ctx, usecase.ConcertCreatedData{ArtistID: "artist-1", ConcertIDs: []string{"c1", "c2"}, RequestID: "a-1"}))

	require.Len(t, w.started, 1)
	for _, input := range w.started {
		assert.Equal(t, "2 new concerts found", input.(usecase.DeliverRequest).Payload.Body)
	}
}

// @spec components/usecase/notification/notify-new-concerts "Unknown artist"
func TestNotifyNewConcerts_UnknownArtist(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	artistRepo := entitymocks.NewMockArtistRepository(t)
	w := &recordingWorkflows{}
	uc := usecase.NewNotificationUseCase(nil, nil, nil, w, noopMetrics{}, newTestLogger(t), usecase.OrderConfirmationDeps{},
		usecase.NewConcertsDeps{ArtistRepo: artistRepo, ConcertRepo: entitymocks.NewMockConcertRepository(t), FollowRepo: entitymocks.NewMockFollowRepository(t)})
	artistRepo.EXPECT().Get(ctx, "missing").Return(nil, apperr.ErrNotFound).Once()

	err := uc.NotifyNewConcerts(ctx, usecase.ConcertCreatedData{ArtistID: "missing", ConcertIDs: []string{"c1"}})

	assert.ErrorIs(t, err, apperr.ErrNotFound)
	assert.Zero(t, w.runs(), "nothing is sent")
}
