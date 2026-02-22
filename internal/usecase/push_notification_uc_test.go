package usecase_test

import (
	"context"
	"testing"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/entity/mocks"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
)

// pushNotificationTestDeps holds all dependencies for PushNotificationUseCase tests.
type pushNotificationTestDeps struct {
	artistRepo  *mocks.MockArtistRepository
	pushSubRepo *mocks.MockPushSubscriptionRepository
	uc          usecase.PushNotificationUseCase
}

func newPushNotificationTestDeps(t *testing.T) *pushNotificationTestDeps {
	t.Helper()
	logger, _ := logging.New()
	d := &pushNotificationTestDeps{
		artistRepo:  mocks.NewMockArtistRepository(t),
		pushSubRepo: mocks.NewMockPushSubscriptionRepository(t),
	}
	d.uc = usecase.NewPushNotificationUseCase(
		d.artistRepo,
		d.pushSubRepo,
		logger,
		"vapid-pub", "vapid-priv", "mailto:test@example.com",
	)
	return d
}

func TestPushNotificationUseCase_Subscribe(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	type args struct {
		userID   string
		endpoint string
		p256dh   string
		auth     string
	}

	tests := []struct {
		name    string
		args    args
		setup   func(t *testing.T, d *pushNotificationTestDeps)
		wantErr error
	}{
		{
			name: "persist subscription successfully",
			args: args{
				userID:   "user-1",
				endpoint: "https://push.example.com/sub/abc",
				p256dh:   "key123",
				auth:     "auth456",
			},
			setup: func(t *testing.T, d *pushNotificationTestDeps) {
				t.Helper()
				d.pushSubRepo.EXPECT().
					Create(ctx, &entity.PushSubscription{
						UserID:   "user-1",
						Endpoint: "https://push.example.com/sub/abc",
						P256dh:   "key123",
						Auth:     "auth456",
					}).
					Return(nil).
					Once()
			},
			wantErr: nil,
		},
		{
			name: "return error when repository fails",
			args: args{
				userID:   "user-1",
				endpoint: "https://push.example.com/sub/abc",
				p256dh:   "key123",
				auth:     "auth456",
			},
			setup: func(t *testing.T, d *pushNotificationTestDeps) {
				t.Helper()
				d.pushSubRepo.EXPECT().
					Create(ctx, &entity.PushSubscription{
						UserID:   "user-1",
						Endpoint: "https://push.example.com/sub/abc",
						P256dh:   "key123",
						Auth:     "auth456",
					}).
					Return(assert.AnError).
					Once()
			},
			wantErr: assert.AnError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			d := newPushNotificationTestDeps(t)
			if tt.setup != nil {
				tt.setup(t, d)
			}

			err := d.uc.Subscribe(ctx, tt.args.userID, tt.args.endpoint, tt.args.p256dh, tt.args.auth)

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				return
			}
			assert.NoError(t, err)
		})
	}
}

func TestPushNotificationUseCase_Unsubscribe(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	type args struct {
		userID string
	}

	tests := []struct {
		name    string
		args    args
		setup   func(t *testing.T, d *pushNotificationTestDeps)
		wantErr error
	}{
		{
			name: "delete subscriptions successfully",
			args: args{userID: "user-1"},
			setup: func(t *testing.T, d *pushNotificationTestDeps) {
				t.Helper()
				d.pushSubRepo.EXPECT().DeleteByUserID(ctx, "user-1").Return(nil).Once()
			},
			wantErr: nil,
		},
		{
			name: "return error when repository fails",
			args: args{userID: "user-1"},
			setup: func(t *testing.T, d *pushNotificationTestDeps) {
				t.Helper()
				d.pushSubRepo.EXPECT().DeleteByUserID(ctx, "user-1").Return(assert.AnError).Once()
			},
			wantErr: assert.AnError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			d := newPushNotificationTestDeps(t)
			if tt.setup != nil {
				tt.setup(t, d)
			}

			err := d.uc.Unsubscribe(ctx, tt.args.userID)

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				return
			}
			assert.NoError(t, err)
		})
	}
}

func TestPushNotificationUseCase_NotifyNewConcerts(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	artist := &entity.Artist{ID: "artist-1", Name: "Test Artist"}
	concerts := []*entity.Concert{
		{Event: entity.Event{ID: "c1", Title: "Concert 1"}, ArtistID: "artist-1"},
	}

	type args struct {
		artist   *entity.Artist
		concerts []*entity.Concert
	}

	tests := []struct {
		name    string
		args    args
		setup   func(t *testing.T, d *pushNotificationTestDeps)
		wantErr error
	}{
		{
			name: "return nil when no followers",
			args: args{artist: artist, concerts: concerts},
			setup: func(t *testing.T, d *pushNotificationTestDeps) {
				t.Helper()
				d.artistRepo.EXPECT().ListFollowers(ctx, "artist-1").Return([]*entity.User{}, nil).Once()
			},
			wantErr: nil,
		},
		{
			name: "return nil when followers have no subscriptions",
			args: args{artist: artist, concerts: concerts},
			setup: func(t *testing.T, d *pushNotificationTestDeps) {
				t.Helper()
				followers := []*entity.User{{ID: "user-1"}}
				d.artistRepo.EXPECT().ListFollowers(ctx, "artist-1").Return(followers, nil).Once()
				d.pushSubRepo.EXPECT().
					ListByUserIDs(ctx, []string{"user-1"}).
					Return([]*entity.PushSubscription{}, nil).
					Once()
			},
			wantErr: nil,
		},
		{
			name: "return error when ListFollowers fails",
			args: args{artist: artist, concerts: concerts},
			setup: func(t *testing.T, d *pushNotificationTestDeps) {
				t.Helper()
				d.artistRepo.EXPECT().ListFollowers(ctx, "artist-1").Return(nil, assert.AnError).Once()
			},
			wantErr: assert.AnError,
		},
		{
			name: "return error when ListByUserIDs fails",
			args: args{artist: artist, concerts: concerts},
			setup: func(t *testing.T, d *pushNotificationTestDeps) {
				t.Helper()
				followers := []*entity.User{{ID: "user-1"}}
				d.artistRepo.EXPECT().ListFollowers(ctx, "artist-1").Return(followers, nil).Once()
				d.pushSubRepo.EXPECT().
					ListByUserIDs(ctx, []string{"user-1"}).
					Return(nil, assert.AnError).
					Once()
			},
			wantErr: assert.AnError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			d := newPushNotificationTestDeps(t)
			if tt.setup != nil {
				tt.setup(t, d)
			}

			err := d.uc.NotifyNewConcerts(ctx, tt.args.artist, tt.args.concerts)

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				return
			}
			assert.NoError(t, err)
		})
	}
}
