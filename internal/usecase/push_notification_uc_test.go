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
	followRepo  *mocks.MockFollowRepository
	pushSubRepo *mocks.MockPushSubscriptionRepository
	uc          usecase.PushNotificationUseCase
}

func newPushNotificationTestDeps(t *testing.T) *pushNotificationTestDeps {
	t.Helper()
	logger, _ := logging.New()
	d := &pushNotificationTestDeps{
		followRepo:  mocks.NewMockFollowRepository(t),
		pushSubRepo: mocks.NewMockPushSubscriptionRepository(t),
	}
	d.uc = usecase.NewPushNotificationUseCase(
		d.followRepo,
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
	tokyoArea := "JP-13"
	osakaArea := "JP-27"
	saitamaArea := "JP-11"
	float64Ptr := func(v float64) *float64 { return &v }
	concertsWithVenue := func(adminArea *string) []*entity.Concert {
		return []*entity.Concert{
			{
				Event:    entity.Event{ID: "c1", Title: "Concert 1", Venue: &entity.Venue{AdminArea: adminArea}},
				ArtistID: "artist-1",
			},
		}
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
			args: args{artist: artist, concerts: concertsWithVenue(&tokyoArea)},
			setup: func(t *testing.T, d *pushNotificationTestDeps) {
				t.Helper()
				d.followRepo.EXPECT().ListFollowers(ctx, "artist-1").Return([]*entity.Follower{}, nil).Once()
			},
			wantErr: nil,
		},
		{
			name: "AWAY follower receives notification",
			args: args{artist: artist, concerts: concertsWithVenue(&tokyoArea)},
			setup: func(t *testing.T, d *pushNotificationTestDeps) {
				t.Helper()
				followers := []*entity.Follower{
					{ArtistID: "artist-1", User: &entity.User{ID: "user-1"}, Hype: entity.HypeAway},
				}
				d.followRepo.EXPECT().ListFollowers(ctx, "artist-1").Return(followers, nil).Once()
				d.pushSubRepo.EXPECT().
					ListByUserIDs(ctx, []string{"user-1"}).
					Return([]*entity.PushSubscription{}, nil).
					Once()
			},
			wantErr: nil,
		},
		{
			name: "WATCH follower is skipped",
			args: args{artist: artist, concerts: concertsWithVenue(&tokyoArea)},
			setup: func(t *testing.T, d *pushNotificationTestDeps) {
				t.Helper()
				followers := []*entity.Follower{
					{ArtistID: "artist-1", User: &entity.User{ID: "user-watch"}, Hype: entity.HypeWatch},
				}
				d.followRepo.EXPECT().ListFollowers(ctx, "artist-1").Return(followers, nil).Once()
				// No ListByUserIDs call expected — all followers filtered out.
			},
			wantErr: nil,
		},
		{
			name: "HOME follower receives notification when venue matches home area",
			args: args{artist: artist, concerts: concertsWithVenue(&tokyoArea)},
			setup: func(t *testing.T, d *pushNotificationTestDeps) {
				t.Helper()
				followers := []*entity.Follower{
					{ArtistID: "artist-1", User: &entity.User{ID: "user-home", Home: &entity.Home{Level1: "JP-13"}}, Hype: entity.HypeHome},
				}
				d.followRepo.EXPECT().ListFollowers(ctx, "artist-1").Return(followers, nil).Once()
				d.pushSubRepo.EXPECT().
					ListByUserIDs(ctx, []string{"user-home"}).
					Return([]*entity.PushSubscription{}, nil).
					Once()
			},
			wantErr: nil,
		},
		{
			name: "HOME follower is skipped when venue does not match home area",
			args: args{artist: artist, concerts: concertsWithVenue(&osakaArea)},
			setup: func(t *testing.T, d *pushNotificationTestDeps) {
				t.Helper()
				followers := []*entity.Follower{
					{ArtistID: "artist-1", User: &entity.User{ID: "user-home", Home: &entity.Home{Level1: "JP-13"}}, Hype: entity.HypeHome},
				}
				d.followRepo.EXPECT().ListFollowers(ctx, "artist-1").Return(followers, nil).Once()
				// No ListByUserIDs call expected — HOME follower filtered out.
			},
			wantErr: nil,
		},
		{
			name: "HOME follower is skipped when no home area set",
			args: args{artist: artist, concerts: concertsWithVenue(&tokyoArea)},
			setup: func(t *testing.T, d *pushNotificationTestDeps) {
				t.Helper()
				followers := []*entity.Follower{
					{ArtistID: "artist-1", User: &entity.User{ID: "user-home"}, Hype: entity.HypeHome},
				}
				d.followRepo.EXPECT().ListFollowers(ctx, "artist-1").Return(followers, nil).Once()
				// No ListByUserIDs call expected — no home area set.
			},
			wantErr: nil,
		},
		{
			name: "NEARBY follower notified when venue is within 200km",
			args: args{
				artist: artist,
				concerts: []*entity.Concert{
					{
						Event: entity.Event{
							ID:    "c1",
							Title: "Concert 1",
							Venue: &entity.Venue{
								AdminArea: &saitamaArea,
								Latitude:  float64Ptr(35.8569),
								Longitude: float64Ptr(139.6489),
							},
						},
						ArtistID: "artist-1",
					},
				},
			},
			setup: func(t *testing.T, d *pushNotificationTestDeps) {
				t.Helper()
				followers := []*entity.Follower{
					{ArtistID: "artist-1", User: &entity.User{ID: "user-nearby", Home: &entity.Home{Level1: "JP-13"}}, Hype: entity.HypeNearby},
				}
				d.followRepo.EXPECT().ListFollowers(ctx, "artist-1").Return(followers, nil).Once()
				d.pushSubRepo.EXPECT().
					ListByUserIDs(ctx, []string{"user-nearby"}).
					Return([]*entity.PushSubscription{}, nil).
					Once()
			},
			wantErr: nil,
		},
		{
			name: "NEARBY follower skipped when venue is beyond 200km",
			args: args{
				artist: artist,
				concerts: []*entity.Concert{
					{
						Event: entity.Event{
							ID:    "c1",
							Title: "Concert 1",
							Venue: &entity.Venue{
								AdminArea: &osakaArea,
								Latitude:  float64Ptr(34.6863),
								Longitude: float64Ptr(135.5200),
							},
						},
						ArtistID: "artist-1",
					},
				},
			},
			setup: func(t *testing.T, d *pushNotificationTestDeps) {
				t.Helper()
				followers := []*entity.Follower{
					{ArtistID: "artist-1", User: &entity.User{ID: "user-nearby", Home: &entity.Home{Level1: "JP-13"}}, Hype: entity.HypeNearby},
				}
				d.followRepo.EXPECT().ListFollowers(ctx, "artist-1").Return(followers, nil).Once()
				// No ListByUserIDs — NEARBY follower filtered out.
			},
			wantErr: nil,
		},
		{
			name: "NEARBY follower skipped when no home area set",
			args: args{artist: artist, concerts: concertsWithVenue(&tokyoArea)},
			setup: func(t *testing.T, d *pushNotificationTestDeps) {
				t.Helper()
				followers := []*entity.Follower{
					{ArtistID: "artist-1", User: &entity.User{ID: "user-nearby"}, Hype: entity.HypeNearby},
				}
				d.followRepo.EXPECT().ListFollowers(ctx, "artist-1").Return(followers, nil).Once()
				// No ListByUserIDs — no home area set.
			},
			wantErr: nil,
		},
		{
			name: "mixed hype levels filter correctly",
			args: args{artist: artist, concerts: concertsWithVenue(&tokyoArea)},
			setup: func(t *testing.T, d *pushNotificationTestDeps) {
				t.Helper()
				followers := []*entity.Follower{
					{ArtistID: "artist-1", User: &entity.User{ID: "user-watch"}, Hype: entity.HypeWatch},
					{ArtistID: "artist-1", User: &entity.User{ID: "user-home-match", Home: &entity.Home{Level1: "JP-13"}}, Hype: entity.HypeHome},
					{ArtistID: "artist-1", User: &entity.User{ID: "user-home-nomatch", Home: &entity.Home{Level1: "JP-27"}}, Hype: entity.HypeHome},
					{ArtistID: "artist-1", User: &entity.User{ID: "user-away"}, Hype: entity.HypeAway},
				}
				d.followRepo.EXPECT().ListFollowers(ctx, "artist-1").Return(followers, nil).Once()
				d.pushSubRepo.EXPECT().
					ListByUserIDs(ctx, []string{"user-home-match", "user-away"}).
					Return([]*entity.PushSubscription{}, nil).
					Once()
			},
			wantErr: nil,
		},
		{
			name: "return error when ListFollowers fails",
			args: args{artist: artist, concerts: concertsWithVenue(&tokyoArea)},
			setup: func(t *testing.T, d *pushNotificationTestDeps) {
				t.Helper()
				d.followRepo.EXPECT().ListFollowers(ctx, "artist-1").Return(nil, assert.AnError).Once()
			},
			wantErr: assert.AnError,
		},
		{
			name: "return error when ListByUserIDs fails",
			args: args{artist: artist, concerts: concertsWithVenue(&tokyoArea)},
			setup: func(t *testing.T, d *pushNotificationTestDeps) {
				t.Helper()
				followers := []*entity.Follower{
					{ArtistID: "artist-1", User: &entity.User{ID: "user-1"}, Hype: entity.HypeAway},
				}
				d.followRepo.EXPECT().ListFollowers(ctx, "artist-1").Return(followers, nil).Once()
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
