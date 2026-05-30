package usecase_test

import (
	"context"
	"testing"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/entity/mocks"
	"github.com/liverty-music/backend/internal/usecase"
	ucmocks "github.com/liverty-music/backend/internal/usecase/mocks"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// followTestDeps holds all dependencies for FollowUseCase tests.
type followTestDeps struct {
	followRepo    *mocks.MockFollowRepository
	artistRepo    *mocks.MockArtistRepository
	siteResolver  *mocks.MockOfficialSiteResolver
	concertUC     *ucmocks.MockConcertUseCase
	searchLogRepo *mocks.MockSearchLogRepository
	publisher     *ucmocks.MockEventPublisher
	uc            usecase.FollowUseCase
}

func newFollowTestDeps(t *testing.T) *followTestDeps {
	t.Helper()
	d := &followTestDeps{
		followRepo:    mocks.NewMockFollowRepository(t),
		artistRepo:    mocks.NewMockArtistRepository(t),
		siteResolver:  mocks.NewMockOfficialSiteResolver(t),
		concertUC:     ucmocks.NewMockConcertUseCase(t),
		searchLogRepo: mocks.NewMockSearchLogRepository(t),
		publisher:     ucmocks.NewMockEventPublisher(t),
	}
	d.uc = usecase.NewFollowUseCase(
		d.followRepo,
		d.artistRepo,
		d.siteResolver,
		d.concertUC,
		d.searchLogRepo,
		d.publisher,
		noopMetrics{},
		newTestLogger(t),
	)
	return d
}

func TestFollowUseCase_SetHype(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	type args struct {
		userID   string
		artistID string
		hype     entity.Hype
	}

	tests := []struct {
		name    string
		args    args
		setup   func(t *testing.T, d *followTestDeps)
		wantErr error
	}{
		{
			name: "success",
			args: args{
				userID:   "internal-uuid-1",
				artistID: "artist-1",
				hype:     entity.HypeAway,
			},
			setup: func(t *testing.T, d *followTestDeps) {
				t.Helper()
				d.followRepo.EXPECT().
					SetHype(ctx, "internal-uuid-1", "artist-1", entity.HypeAway).
					Return(nil).
					Once()
			},
			wantErr: nil,
		},
		{
			name: "return error when repository SetHype fails",
			args: args{
				userID:   "internal-uuid-1",
				artistID: "artist-1",
				hype:     entity.HypeAway,
			},
			setup: func(t *testing.T, d *followTestDeps) {
				t.Helper()
				d.followRepo.EXPECT().
					SetHype(ctx, "internal-uuid-1", "artist-1", entity.HypeAway).
					Return(apperr.ErrInternal).
					Once()
			},
			wantErr: apperr.ErrInternal,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			d := newFollowTestDeps(t)
			if tt.setup != nil {
				tt.setup(t, d)
			}

			err := d.uc.SetHype(ctx, tt.args.userID, tt.args.artistID, tt.args.hype)

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				return
			}
			assert.NoError(t, err)
		})
	}
}

// TestFollowUseCase_Follow_PublishesAnalyticsEvent verifies that a
// successful Follow publishes ARTIST.followed via the injected
// EventPublisher. The background goroutines (resolveAndPersistOfficialSite,
// triggerFirstFollowSearch) run with context.WithoutCancel and touch
// the artist + searchLog + concert mocks; their EXPECT()s are declared
// .Maybe() so the test exits deterministically without waiting on
// background work that this test isn't asserting.
func TestFollowUseCase_Follow_PublishesAnalyticsEvent(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	t.Run("publishes ARTIST.followed on first follow", func(t *testing.T) {
		t.Parallel()
		d := newFollowTestDeps(t)

		d.followRepo.EXPECT().
			Follow(ctx, "user-1", "artist-1").
			Return(nil).Once()
		d.publisher.EXPECT().
			PublishEvent(ctx, entity.SubjectArtistFollowed, entity.ArtistFollowedData{
				UserID:   "user-1",
				ArtistID: "artist-1",
			}).
			Return(nil).Once()

		// Background goroutine deps — .Maybe() because the goroutines run
		// asynchronously and may or may not have entered their first mock
		// call by the time the test returns.
		d.artistRepo.EXPECT().GetOfficialSite(mock.Anything, "artist-1").
			Return(nil, apperr.ErrNotFound).Maybe()
		d.artistRepo.EXPECT().Get(mock.Anything, "artist-1").
			Return(&entity.Artist{ID: "artist-1"}, nil).Maybe()
		d.searchLogRepo.EXPECT().GetByArtistID(mock.Anything, "artist-1").
			Return(nil, apperr.ErrNotFound).Maybe()
		d.concertUC.EXPECT().SearchNewConcerts(mock.Anything, "artist-1").
			Return(nil, nil).Maybe()

		err := d.uc.Follow(ctx, "user-1", "artist-1")
		assert.NoError(t, err)
	})

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
				UserID:   "user-1",
				ArtistID: "artist-1",
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
