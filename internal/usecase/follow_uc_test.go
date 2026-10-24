package usecase_test

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/ThreeDotsLabs/watermill"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/entity/mocks"
	"github.com/liverty-music/backend/internal/infrastructure/messaging"
	"github.com/liverty-music/backend/internal/usecase"
	ucmocks "github.com/liverty-music/backend/internal/usecase/mocks"
	"github.com/liverty-music/backend/pkg/config"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// followTestDeps holds all dependencies for FollowUseCase tests.
type followTestDeps struct {
	followRepo   *mocks.MockFollowRepository
	artistRepo   *mocks.MockArtistRepository
	siteResolver *mocks.MockOfficialSiteResolver
	publisher    *ucmocks.MockEventPublisher
	uc           usecase.FollowUseCase
}

func newFollowTestDeps(t *testing.T) *followTestDeps {
	t.Helper()
	d := &followTestDeps{
		followRepo:   mocks.NewMockFollowRepository(t),
		artistRepo:   mocks.NewMockArtistRepository(t),
		siteResolver: mocks.NewMockOfficialSiteResolver(t),
		publisher:    ucmocks.NewMockEventPublisher(t),
	}
	d.uc = usecase.NewFollowUseCase(
		d.followRepo,
		d.artistRepo,
		d.siteResolver,
		d.publisher,
		noopMetrics{},
		fixedClock(followTestNow),
		newTestLogger(t),
	)
	return d
}

// followTestNow is the clock of the FollowUseCase under test.
var followTestNow = time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)

func TestFollowUseCase_SetHype(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	t.Run("not followed", func(t *testing.T) {
		t.Parallel()
		d := newFollowTestDeps(t)
		d.followRepo.EXPECT().SetHype(ctx, "internal-uuid-1", "artist-1", entity.HypeAway).Return(apperr.ErrNotFound).Once()

		// Nothing is set, so nothing is announced (the strict publisher mock
		// fails on any PublishEvent call).
		assert.ErrorIs(t, d.uc.SetHype(ctx, "internal-uuid-1", "artist-1", entity.HypeAway), apperr.ErrNotFound)
	})

	t.Run("hype raised", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/follow/set-hype "Hype raised"
		d := newFollowTestDeps(t)
		d.followRepo.EXPECT().SetHype(ctx, "internal-uuid-1", "artist-1", entity.HypeAway).Return(nil).Once()
		d.publisher.EXPECT().
			PublishEvent(ctx, entity.SubjectArtistHypeChanged, entity.ArtistHypeChangedData{
				UserID:   "internal-uuid-1",
				ArtistID: "artist-1",
				Hype:     "away",
				SetTime:  followTestNow,
			}).
			Return(nil).
			Once()

		require.NoError(t, d.uc.SetHype(ctx, "internal-uuid-1", "artist-1", entity.HypeAway))
	})

	t.Run("signal failure", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/follow/set-hype "Signal failure"
		d := newFollowTestDeps(t)
		d.followRepo.EXPECT().SetHype(ctx, "internal-uuid-1", "artist-1", entity.HypeAway).Return(nil).Once()
		d.publisher.EXPECT().PublishEvent(ctx, entity.SubjectArtistHypeChanged, mock.Anything).Return(apperr.ErrUnavailable).Once()

		// SetHype succeeds; the level stays as set (nothing is undone: the
		// repository is not called again).
		require.NoError(t, d.uc.SetHype(ctx, "internal-uuid-1", "artist-1", entity.HypeAway))
	})
}

// TestFollowUseCase_Follow_PublishesAnalyticsEvent verifies that a
// successful Follow publishes ARTIST.followed via the injected
// EventPublisher. The background goroutine
// (resolveAndPersistOfficialSite) runs with context.WithoutCancel and
// touches the artist mock; its EXPECT()s are declared .Maybe() so the test
// exits deterministically without waiting on background work that this test
// isn't asserting. Concert discovery no longer runs from Follow itself: it is
// triggered by the search-first-followed-artist consumer reacting to the
// published ARTIST.followed event (see ConcertUseCase.SearchNewConcertsOnFirstFollow).
func TestFollowUseCase_Follow_PublishesAnalyticsEvent(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	// @spec components/usecase/follow/follow "New follow announced"
	t.Run("publishes ARTIST.followed on first follow", func(t *testing.T) {
		t.Parallel()
		d := newFollowTestDeps(t)

		d.followRepo.EXPECT().
			Follow(ctx, "user-1", "artist-1").
			Return(nil).Once()
		d.publisher.EXPECT().
			PublishEvent(ctx, entity.SubjectArtistFollowed, entity.ArtistFollowedData{
				UserID:     "user-1",
				ArtistID:   "artist-1",
				FollowTime: followTestNow,
			}).
			Return(nil).Once()

		// Background goroutine deps — .Maybe() because the goroutine runs
		// asynchronously and may or may not have entered its first mock call
		// by the time the test returns.
		d.artistRepo.EXPECT().GetOfficialSite(mock.Anything, "artist-1").
			Return(nil, apperr.ErrNotFound).Maybe()
		d.artistRepo.EXPECT().Get(mock.Anything, "artist-1").
			Return(&entity.Artist{ID: "artist-1"}, nil).Maybe()

		err := d.uc.Follow(ctx, "user-1", "artist-1")
		assert.NoError(t, err)
	})

	// @spec components/usecase/follow/follow "Announcement fails"
	t.Run("tolerates publisher failure (non-fatal)", func(t *testing.T) {
		t.Parallel()
		d := newFollowTestDeps(t)

		d.followRepo.EXPECT().
			Follow(ctx, "user-1", "artist-1").
			Return(nil).Once()
		d.publisher.EXPECT().
			PublishEvent(ctx, entity.SubjectArtistFollowed, entity.ArtistFollowedData{
				UserID:     "user-1",
				ArtistID:   "artist-1",
				FollowTime: followTestNow,
			}).
			Return(apperr.ErrInternal).Once()

		d.artistRepo.EXPECT().GetOfficialSite(mock.Anything, "artist-1").
			Return(nil, apperr.ErrNotFound).Maybe()
		d.artistRepo.EXPECT().Get(mock.Anything, "artist-1").
			Return(&entity.Artist{ID: "artist-1"}, nil).Maybe()

		// Follow contract: succeeds despite publish failure because the
		// relationship is already persisted.
		err := d.uc.Follow(ctx, "user-1", "artist-1")
		assert.NoError(t, err)
	})

	// @spec components/usecase/follow/follow "Fan follows the same artist twice"
	t.Run("does not publish on already-following idempotent path", func(t *testing.T) {
		t.Parallel()
		d := newFollowTestDeps(t)

		d.followRepo.EXPECT().
			Follow(ctx, "user-1", "artist-1").
			Return(apperr.ErrAlreadyExists).Once()
		// publisher MUST NOT be called — no EXPECT() registered.

		err := d.uc.Follow(ctx, "user-1", "artist-1")
		assert.NoError(t, err)
	})

	t.Run("returns Internal on repository failure without publishing", func(t *testing.T) {
		t.Parallel()
		d := newFollowTestDeps(t)

		d.followRepo.EXPECT().
			Follow(ctx, "user-1", "artist-1").
			Return(apperr.ErrInternal).Once()
		// publisher MUST NOT be called.

		err := d.uc.Follow(ctx, "user-1", "artist-1")
		assert.ErrorIs(t, err, apperr.ErrInternal)
	})
}

// TestFollowUseCase_Follow_BrokerDown runs Follow against the real NATS
// publisher while nothing listens on the broker address: the publisher is
// created without waiting for the broker, and the publish gives up within the
// JetStream ack timeout instead of blocking the call.
//
// @spec components/infrastructure/backend/process/startup-dependencies "Follow while the broker is down"
func TestFollowUseCase_Follow_BrokerDown(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	brokerURL := "nats://" + ln.Addr().String()
	require.NoError(t, ln.Close())

	logger := newTestLogger(t)
	pub, err := messaging.NewPublisher(config.NATSConfig{URL: brokerURL}, watermill.NopLogger{}, nil, logger)
	require.NoError(t, err, "the publisher must not need the broker to start")
	t.Cleanup(func() { _ = pub.Close() })

	followRepo := mocks.NewMockFollowRepository(t)
	artistRepo := mocks.NewMockArtistRepository(t)
	followRepo.EXPECT().Follow(ctx, "user-1", "artist-1").Return(nil).Once()
	// Background official-site lookup: the site is already recorded.
	artistRepo.EXPECT().GetOfficialSite(mock.Anything, "artist-1").
		Return(&entity.OfficialSite{}, nil).Maybe()
	uc := usecase.NewFollowUseCase(followRepo, artistRepo, mocks.NewMockOfficialSiteResolver(t),
		messaging.NewEventPublisher(pub), noopMetrics{}, time.Now, logger)

	start := time.Now()
	err = uc.Follow(ctx, "user-1", "artist-1")

	assert.NoError(t, err, "the follow is stored even though the event is not published")
	assert.Less(t, time.Since(start), 10*time.Second)
}

// TestFollowUseCase_Unfollow_PublishesAnalyticsEvent verifies that a
// successful Unfollow publishes ARTIST.unfollowed. No goroutines in the
// Unfollow path, so the test is straightforward.
func TestFollowUseCase_Unfollow_PublishesAnalyticsEvent(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	t.Run("publishes ARTIST.unfollowed on success", func(t *testing.T) {
		t.Parallel()
		d := newFollowTestDeps(t)

		d.followRepo.EXPECT().
			Unfollow(ctx, "user-1", "artist-1").
			Return(nil).Once()
		d.publisher.EXPECT().
			PublishEvent(ctx, entity.SubjectArtistUnfollowed, entity.ArtistUnfollowedData{
				UserID:       "user-1",
				ArtistID:     "artist-1",
				UnfollowTime: followTestNow,
			}).
			Return(nil).Once()

		err := d.uc.Unfollow(ctx, "user-1", "artist-1")
		assert.NoError(t, err)
	})

	t.Run("does not publish when repository fails", func(t *testing.T) {
		t.Parallel()
		d := newFollowTestDeps(t)

		d.followRepo.EXPECT().
			Unfollow(ctx, "user-1", "artist-1").
			Return(apperr.ErrInternal).Once()
		// publisher MUST NOT be called.

		err := d.uc.Unfollow(ctx, "user-1", "artist-1")
		assert.ErrorIs(t, err, apperr.ErrInternal)
	})

	t.Run("tolerates publisher failure (non-fatal)", func(t *testing.T) {
		t.Parallel()
		d := newFollowTestDeps(t)

		d.followRepo.EXPECT().
			Unfollow(ctx, "user-1", "artist-1").
			Return(nil).Once()
		d.publisher.EXPECT().
			PublishEvent(ctx, entity.SubjectArtistUnfollowed, mock.Anything).
			Return(apperr.ErrInternal).Once()

		err := d.uc.Unfollow(ctx, "user-1", "artist-1")
		// Unfollow contract: succeeds despite publish failure because
		// the relationship is already persisted.
		assert.NoError(t, err)
	})
}
