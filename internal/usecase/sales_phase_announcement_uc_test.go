package usecase_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/liverty-music/backend/internal/entity"
	entitymocks "github.com/liverty-music/backend/internal/entity/mocks"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// announcementTestDeps holds the mocks and use case for announcement tests.
type announcementTestDeps struct {
	userRepo    *entitymocks.MockUserRepository
	journeyRepo *entitymocks.MockTicketJourneyRepository
	pushSubRepo *entitymocks.MockPushSubscriptionRepository
	sender      *fakeSender
	uc          usecase.SalesPhaseAnnouncementUseCase
}

func newAnnouncementTestDeps(t *testing.T) *announcementTestDeps {
	t.Helper()
	d := &announcementTestDeps{
		userRepo:    entitymocks.NewMockUserRepository(t),
		journeyRepo: entitymocks.NewMockTicketJourneyRepository(t),
		pushSubRepo: entitymocks.NewMockPushSubscriptionRepository(t),
		sender:      &fakeSender{},
	}
	d.uc = usecase.NewSalesPhaseAnnouncementUseCase(
		d.userRepo,
		d.journeyRepo,
		d.pushSubRepo,
		d.sender,
		noopMetrics{},
		newTestLogger(t),
	)
	return d
}

func TestAnnounceDiscoveredPhase_LocalizesCopyPerRecipient(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	d := newAnnouncementTestDeps(t)
	data := entity.SalesPhaseDiscoveredData{SeriesID: "series-1", PhaseID: "phase-1"}

	d.journeyRepo.EXPECT().
		ListUserIDsTrackingSeries(ctx, "series-1").
		Return([]string{"user-ja", "user-en", "user-unset"}, nil).
		Once()
	d.userRepo.EXPECT().Get(ctx, "user-ja").Return(&entity.User{ID: "user-ja", PreferredLanguage: "ja"}, nil).Once()
	d.userRepo.EXPECT().Get(ctx, "user-en").Return(&entity.User{ID: "user-en", PreferredLanguage: "en"}, nil).Once()
	d.userRepo.EXPECT().Get(ctx, "user-unset").Return(&entity.User{ID: "user-unset"}, nil).Once()

	subs := []*entity.PushSubscription{
		{UserID: "user-ja", Endpoint: "https://push.example.com/ja"},
		{UserID: "user-en", Endpoint: "https://push.example.com/en"},
		{UserID: "user-unset", Endpoint: "https://push.example.com/unset"},
	}
	d.pushSubRepo.EXPECT().
		ListByUserIDs(ctx, []string{"user-ja", "user-en", "user-unset"}).
		Return(subs, nil).
		Once()

	var mu sync.Mutex
	captured := make(map[string]entity.NotificationPayload)
	d.sender.sendFn = func(_ context.Context, payload []byte, sub *entity.PushSubscription) error {
		var p entity.NotificationPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return err
		}
		mu.Lock()
		captured[sub.Endpoint] = p
		mu.Unlock()
		return nil
	}

	err := d.uc.AnnounceDiscoveredPhase(ctx, data)
	require.NoError(t, err)

	assert.Equal(t, "チケット販売情報の新着", captured["https://push.example.com/ja"].Title)
	assert.Equal(t, "New Ticket Sales Phase", captured["https://push.example.com/en"].Title)
	assert.Equal(t, "New Ticket Sales Phase", captured["https://push.example.com/unset"].Title, "unset language falls back to en")

	// URL and Tag are language-independent.
	assert.Equal(t, "/series/series-1", captured["https://push.example.com/ja"].URL)
	assert.Equal(t, "sales-phase-phase-1", captured["https://push.example.com/ja"].Tag)
}

func TestAnnounceDiscoveredPhase_HydrationError_SkipsButContinues(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	d := newAnnouncementTestDeps(t)
	data := entity.SalesPhaseDiscoveredData{SeriesID: "series-1", PhaseID: "phase-1"}

	d.journeyRepo.EXPECT().
		ListUserIDsTrackingSeries(ctx, "series-1").
		Return([]string{"user-ja", "user-broken"}, nil).
		Once()
	d.userRepo.EXPECT().Get(ctx, "user-ja").Return(&entity.User{ID: "user-ja", PreferredLanguage: "ja"}, nil).Once()
	d.userRepo.EXPECT().Get(ctx, "user-broken").Return(nil, apperr.ErrInternal).Once()

	// Subscriptions are still listed for the full audience; the broken user's
	// subscription falls back to en since it never made it into the language map.
	subs := []*entity.PushSubscription{
		{UserID: "user-ja", Endpoint: "https://push.example.com/ja"},
		{UserID: "user-broken", Endpoint: "https://push.example.com/broken"},
	}
	d.pushSubRepo.EXPECT().
		ListByUserIDs(ctx, []string{"user-ja", "user-broken"}).
		Return(subs, nil).
		Once()

	var mu sync.Mutex
	captured := make(map[string]entity.NotificationPayload)
	d.sender.sendFn = func(_ context.Context, payload []byte, sub *entity.PushSubscription) error {
		var p entity.NotificationPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return err
		}
		mu.Lock()
		captured[sub.Endpoint] = p
		mu.Unlock()
		return nil
	}

	err := d.uc.AnnounceDiscoveredPhase(ctx, data)
	require.NoError(t, err)

	assert.Equal(t, "チケット販売情報の新着", captured["https://push.example.com/ja"].Title)
	assert.Equal(t, "New Ticket Sales Phase", captured["https://push.example.com/broken"].Title, "hydration failure falls back to en")
}

func TestAnnounceDiscoveredPhase_EmptyAudience_NoOp(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	d := newAnnouncementTestDeps(t)
	data := entity.SalesPhaseDiscoveredData{SeriesID: "series-1", PhaseID: "phase-1"}

	d.journeyRepo.EXPECT().
		ListUserIDsTrackingSeries(ctx, "series-1").
		Return([]string{}, nil).
		Once()
	// No user hydration, no subscription listing, no sends expected.

	err := d.uc.AnnounceDiscoveredPhase(ctx, data)
	require.NoError(t, err)
}
