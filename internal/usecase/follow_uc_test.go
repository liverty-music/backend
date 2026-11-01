package usecase_test

import (
	"context"
	"testing"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/entity/mocks"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// followTestDeps holds all dependencies for FollowUseCase tests. The
// repository and catalog mocks are strict: a background official-site
// lookup that a test does not expect fails it.
type followTestDeps struct {
	followRepo   *mocks.MockFollowRepository
	artistRepo   *mocks.MockArtistRepository
	siteResolver *mocks.MockOfficialSiteResolver
	uc           usecase.FollowUseCase
}

func newFollowTestDeps(t *testing.T) *followTestDeps {
	t.Helper()
	d := &followTestDeps{
		followRepo:   mocks.NewMockFollowRepository(t),
		artistRepo:   mocks.NewMockArtistRepository(t),
		siteResolver: mocks.NewMockOfficialSiteResolver(t),
	}
	d.uc = usecase.NewFollowUseCase(
		d.followRepo,
		d.artistRepo,
		d.siteResolver,
		noopMetrics{},
		newTestLogger(t),
	)
	return d
}

// expectSiteAlreadyRecorded lets the background official-site lookup find a
// recorded site, and returns a channel closed when it looked.
func (d *followTestDeps) expectSiteAlreadyRecorded(artistID string) <-chan struct{} {
	looked := make(chan struct{})
	d.artistRepo.EXPECT().GetOfficialSite(mock.Anything, artistID).
		RunAndReturn(func(context.Context, string) (*entity.OfficialSite, error) {
			close(looked)
			return &entity.OfficialSite{}, nil
		}).Once()
	return looked
}

func TestFollowUseCase_Follow(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("fan follows a new artist", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/follow/follow "Fan follows a new artist"
		d := newFollowTestDeps(t)
		d.followRepo.EXPECT().Follow(ctx, "user-1", "artist-1").Return(nil).Once()
		looked := d.expectSiteAlreadyRecorded("artist-1")

		assert.NoError(t, d.uc.Follow(ctx, "user-1", "artist-1"))
		<-looked
	})

	t.Run("announcement cannot be recorded", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/follow/follow "Announcement cannot be recorded"
		d := newFollowTestDeps(t)
		d.followRepo.EXPECT().Follow(ctx, "user-1", "artist-1").
			Return(apperr.New(codes.Unavailable, "failed to record the announcement")).Once()

		// No background work: the strict artist mock fails on any lookup.
		assert.ErrorIs(t, d.uc.Follow(ctx, "user-1", "artist-1"), apperr.ErrUnavailable)
	})

	t.Run("unknown artist", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/follow/follow "Unknown artist"
		d := newFollowTestDeps(t)
		d.followRepo.EXPECT().Follow(ctx, "user-1", "missing").
			Return(apperr.New(codes.NotFound, "artist not found")).Once()

		assert.ErrorIs(t, d.uc.Follow(ctx, "user-1", "missing"), apperr.ErrInternal)
	})

	t.Run("fan follows the same artist twice", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/follow/follow "Fan follows the same artist twice"
		d := newFollowTestDeps(t)
		d.followRepo.EXPECT().Follow(ctx, "user-1", "artist-1").Return(apperr.ErrAlreadyExists).Once()

		// The repository leaves the stored Follow and its level unchanged;
		// no background work starts.
		assert.NoError(t, d.uc.Follow(ctx, "user-1", "artist-1"))
	})
}

// Follow no longer talks to the message broker: the announcement is recorded
// with the Follow, so a follow returns as soon as it is stored.
//
// @spec components/infrastructure/backend/process/startup-dependencies "Follow while the broker is down"
func TestFollowUseCase_Follow_BrokerDown(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := newFollowTestDeps(t)
	d.followRepo.EXPECT().Follow(ctx, "user-1", "artist-1").Return(nil).Once()
	looked := d.expectSiteAlreadyRecorded("artist-1")

	start := time.Now()
	assert.NoError(t, d.uc.Follow(ctx, "user-1", "artist-1"))
	assert.Less(t, time.Since(start), 10*time.Second)
	<-looked
}

func TestFollowUseCase_Unfollow(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("fan unfollows an artist", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/follow/unfollow "Fan unfollows an artist"
		d := newFollowTestDeps(t)
		d.followRepo.EXPECT().Unfollow(ctx, "user-1", "artist-1").Return(nil).Once()

		assert.NoError(t, d.uc.Unfollow(ctx, "user-1", "artist-1"))
	})

	t.Run("artist not followed", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/follow/unfollow "Artist not followed"
		d := newFollowTestDeps(t)
		// Follow.Unfollow succeeds without removing anything.
		d.followRepo.EXPECT().Unfollow(ctx, "user-1", "artist-1").Return(nil).Once()

		assert.NoError(t, d.uc.Unfollow(ctx, "user-1", "artist-1"))
	})

	t.Run("removal fails", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/follow/unfollow "Removal fails"
		d := newFollowTestDeps(t)
		d.followRepo.EXPECT().Unfollow(ctx, "user-1", "artist-1").
			Return(apperr.New(codes.Unavailable, "failed to record the announcement")).Once()

		assert.ErrorIs(t, d.uc.Unfollow(ctx, "user-1", "artist-1"), apperr.ErrUnavailable)
	})
}

func TestFollowUseCase_SetHype(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("fan raises the hype level", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/follow/set-hype "Fan raises the hype level"
		d := newFollowTestDeps(t)
		d.followRepo.EXPECT().SetHype(ctx, "user-1", "artist-1", entity.HypeAway).Return(nil).Once()

		assert.NoError(t, d.uc.SetHype(ctx, "user-1", "artist-1", entity.HypeAway))
	})

	t.Run("artist not followed", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/follow/set-hype "Artist not followed"
		d := newFollowTestDeps(t)
		d.followRepo.EXPECT().SetHype(ctx, "user-1", "artist-1", entity.HypeAway).Return(apperr.ErrNotFound).Once()

		assert.ErrorIs(t, d.uc.SetHype(ctx, "user-1", "artist-1", entity.HypeAway), apperr.ErrNotFound)
	})
}
