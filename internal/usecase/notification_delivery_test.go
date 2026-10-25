package usecase_test

// A trigger delivered twice (an at-least-once redelivery of
// SALES_PHASE.discovered or CONCERT.created) makes two NOTIFICATION.requested
// publishes for the same recipient, each a new CloudEvent with its own id; the
// second push replaces the first on the device by its Tag. These tests run
// the producers twice over a real EventPublisher and read the message ids.

import (
	"context"
	"sync"
	"testing"

	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/liverty-music/backend/internal/entity"
	entitymocks "github.com/liverty-music/backend/internal/entity/mocks"
	"github.com/liverty-music/backend/internal/infrastructure/messaging"
	"github.com/liverty-music/backend/internal/usecase"
)

// capturingPublisher is a watermill publisher that keeps every published
// message.
type capturingPublisher struct {
	mu   sync.Mutex
	msgs []*message.Message
}

func (p *capturingPublisher) Publish(_ string, msgs ...*message.Message) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.msgs = append(p.msgs, msgs...)
	return nil
}

func (p *capturingPublisher) Close() error { return nil }

// ids returns the CloudEvent ids of the published messages.
func (p *capturingPublisher) ids() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]string, 0, len(p.msgs))
	for _, m := range p.msgs {
		out = append(out, m.Metadata.Get("ce_id"))
	}
	return out
}

// tags returns the notification Tag of each published NOTIFICATION.requested
// message, in publish order.
func (p *capturingPublisher) tags(t *testing.T) []string {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]string, 0, len(p.msgs))
	for _, m := range p.msgs {
		var data entity.NotificationRequestedData
		require.NoError(t, messaging.ParseCloudEventData(m, &data))
		require.NotNil(t, data.Payload)
		out = append(out, data.Payload.Tag)
	}
	return out
}

// TestAnnounceDiscoveredSale_SameSaleTwice verifies that announcing the same
// sale twice for the same recipient makes two requests, each with its own id
// and the same per-sale tag.
//
// @spec components/usecase/ticket-sale/announce-discovered-sale "Same sale announced twice"
func TestAnnounceDiscoveredSale_SameSaleTwice(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	userRepo := entitymocks.NewMockUserRepository(t)
	journeyRepo := entitymocks.NewMockTicketJourneyRepository(t)
	seriesRepo := entitymocks.NewMockSeriesRepository(t)
	wm := &capturingPublisher{}
	uc := usecase.NewTicketSaleAnnouncementUseCase(userRepo, journeyRepo, seriesRepo, messaging.NewEventPublisher(wm), newTestLogger(t))

	journeyRepo.EXPECT().ListUserIDsTrackingSeries(ctx, "series-1").
		Return([]*entity.SeriesTracker{{UserID: "user-1", EventID: "event-1"}}, nil).Twice()
	seriesRepo.EXPECT().Get(ctx, "series-1").Return(&entity.Series{ID: "series-1", Title: "Tour"}, nil).Twice()
	userRepo.EXPECT().Get(ctx, "user-1").Return(&entity.User{ID: "user-1"}, nil).Twice()

	sale := entity.TicketSaleDiscoveredData{SeriesID: "series-1", TicketSaleID: "sale-1"}
	require.NoError(t, uc.AnnounceDiscoveredSale(ctx, sale))
	require.NoError(t, uc.AnnounceDiscoveredSale(ctx, sale))

	ids := wm.ids()
	require.Len(t, ids, 2, "two requests are made for the recipient")
	assert.NotEmpty(t, ids[0])
	assert.NotEqual(t, ids[0], ids[1], "each request is a new event with its own id")
	assert.Equal(t, []string{"ticket-sale-sale-1", "ticket-sale-sale-1"}, wm.tags(t),
		"both requests carry the per-sale tag, so the second replaces the first")
}

// TestNotifyNewConcerts_SameConcertsTwice verifies that notifying the same
// concerts twice to the same follower makes two requests, each with its own
// id and the same per-artist tag.
//
// @spec components/usecase/notification/notify-new-concerts "Same concerts notified twice"
func TestNotifyNewConcerts_SameConcertsTwice(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	artistRepo := entitymocks.NewMockArtistRepository(t)
	concertRepo := entitymocks.NewMockConcertRepository(t)
	followRepo := entitymocks.NewMockFollowRepository(t)
	pushSubRepo := entitymocks.NewMockPushSubscriptionRepository(t)
	wm := &capturingPublisher{}
	uc := usecase.NewNotificationUseCase(nil, pushSubRepo, nil, messaging.NewEventPublisher(wm), noopMetrics{}, newTestLogger(t), usecase.OrderConfirmationDeps{},
		usecase.NewConcertsDeps{ArtistRepo: artistRepo, ConcertRepo: concertRepo, FollowRepo: followRepo})

	area := "JP-13"
	artistRepo.EXPECT().Get(ctx, "artist-1").Return(&entity.Artist{ID: "artist-1", Name: "Test Artist"}, nil).Twice()
	concertRepo.EXPECT().ListByIDs(ctx, []string{"c1"}).Return([]*entity.Concert{
		{ID: "c1", Venue: &entity.Venue{AdminArea: &area}, Artists: []*entity.Artist{{ID: "artist-1"}}},
	}, nil).Twice()
	followRepo.EXPECT().ListFollowers(ctx, "artist-1").Return([]*entity.Follower{
		{ArtistID: "artist-1", User: &entity.User{ID: "user-1"}, Hype: entity.HypeAway},
	}, nil).Twice()

	data := usecase.ConcertCreatedData{ArtistID: "artist-1", ConcertIDs: []string{"c1"}}
	require.NoError(t, uc.NotifyNewConcerts(ctx, data))
	require.NoError(t, uc.NotifyNewConcerts(ctx, data))

	ids := wm.ids()
	require.Len(t, ids, 2, "two requests are made for the follower")
	assert.NotEmpty(t, ids[0])
	assert.NotEqual(t, ids[0], ids[1], "each request is a new event with its own id")
	assert.Equal(t, []string{"concert-artist-1", "concert-artist-1"}, wm.tags(t),
		"both requests carry the per-artist tag, so the second replaces the first")
}
