package usecase_test

import (
	"context"
	"testing"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/entity/mocks"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/stretchr/testify/assert"
)

// fakeSender is a configurable PushNotificationSender for tests.
// By default it succeeds. Set sendFn to control per-subscription behavior.
type fakeSender struct {
	sendFn func(ctx context.Context, payload []byte, sub *entity.PushSubscription) error
}

func (s *fakeSender) Send(ctx context.Context, payload []byte, sub *entity.PushSubscription) error {
	if s.sendFn != nil {
		return s.sendFn(ctx, payload, sub)
	}
	return nil
}

// pushNotificationTestDeps holds all dependencies for PushNotificationUseCase tests.
type pushNotificationTestDeps struct {
	artistRepo  *mocks.MockArtistRepository
	concertRepo *mocks.MockConcertRepository
	followRepo  *mocks.MockFollowRepository
	pushSubRepo *mocks.MockPushSubscriptionRepository
	sender      *fakeSender
	uc          usecase.PushNotificationUseCase
}

func newPushNotificationTestDeps(t *testing.T) *pushNotificationTestDeps {
	t.Helper()
	d := &pushNotificationTestDeps{
		artistRepo:  mocks.NewMockArtistRepository(t),
		concertRepo: mocks.NewMockConcertRepository(t),
		followRepo:  mocks.NewMockFollowRepository(t),
		pushSubRepo: mocks.NewMockPushSubscriptionRepository(t),
		sender:      &fakeSender{},
	}
	d.uc = usecase.NewPushNotificationUseCase(
		d.artistRepo,
		d.concertRepo,
		d.followRepo,
		d.pushSubRepo,
		d.sender,
		noopMetrics{},
		newTestLogger(t),
	)
	return d
}

func TestPushNotificationUseCase_Create(t *testing.T) {
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
					Return(apperr.ErrInternal).
					Once()
			},
			wantErr: apperr.ErrInternal,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			d := newPushNotificationTestDeps(t)
			if tt.setup != nil {
				tt.setup(t, d)
			}

			sub, err := d.uc.Create(ctx, tt.args.userID, tt.args.endpoint, tt.args.p256dh, tt.args.auth)

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				return
			}
			assert.NoError(t, err)
			assert.Equal(t, tt.args.userID, sub.UserID)
			assert.Equal(t, tt.args.endpoint, sub.Endpoint)
		})
	}
}

func TestPushNotificationUseCase_Get(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	type args struct {
		userID   string
		endpoint string
	}

	tests := []struct {
		name    string
		args    args
		setup   func(t *testing.T, d *pushNotificationTestDeps)
		want    *entity.PushSubscription
		wantErr error
	}{
		{
			name: "returns matching subscription",
			args: args{userID: "user-1", endpoint: "https://push.example.com/sub"},
			setup: func(t *testing.T, d *pushNotificationTestDeps) {
				t.Helper()
				d.pushSubRepo.EXPECT().
					Get(ctx, "user-1", "https://push.example.com/sub").
					Return(&entity.PushSubscription{
						ID:       "sub-1",
						UserID:   "user-1",
						Endpoint: "https://push.example.com/sub",
						P256dh:   "k",
						Auth:     "a",
					}, nil).
					Once()
			},
			want: &entity.PushSubscription{
				ID:       "sub-1",
				UserID:   "user-1",
				Endpoint: "https://push.example.com/sub",
				P256dh:   "k",
				Auth:     "a",
			},
			wantErr: nil,
		},
		{
			name: "propagates NotFound from repository",
			args: args{userID: "user-1", endpoint: "https://push.example.com/missing"},
			setup: func(t *testing.T, d *pushNotificationTestDeps) {
				t.Helper()
				d.pushSubRepo.EXPECT().
					Get(ctx, "user-1", "https://push.example.com/missing").
					Return(nil, apperr.ErrNotFound).
					Once()
			},
			wantErr: apperr.ErrNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			d := newPushNotificationTestDeps(t)
			if tt.setup != nil {
				tt.setup(t, d)
			}

			got, err := d.uc.Get(ctx, tt.args.userID, tt.args.endpoint)

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				return
			}
			assert.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestPushNotificationUseCase_Delete(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	type args struct {
		userID   string
		endpoint string
	}

	tests := []struct {
		name    string
		args    args
		setup   func(t *testing.T, d *pushNotificationTestDeps)
		wantErr error
	}{
		{
			name: "delete subscription successfully",
			args: args{userID: "user-1", endpoint: "https://push.example.com/sub"},
			setup: func(t *testing.T, d *pushNotificationTestDeps) {
				t.Helper()
				d.pushSubRepo.EXPECT().
					Delete(ctx, "user-1", "https://push.example.com/sub").
					Return(nil).
					Once()
			},
			wantErr: nil,
		},
		{
			name: "return error when repository fails",
			args: args{userID: "user-1", endpoint: "https://push.example.com/sub"},
			setup: func(t *testing.T, d *pushNotificationTestDeps) {
				t.Helper()
				d.pushSubRepo.EXPECT().
					Delete(ctx, "user-1", "https://push.example.com/sub").
					Return(apperr.ErrInternal).
					Once()
			},
			wantErr: apperr.ErrInternal,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			d := newPushNotificationTestDeps(t)
			if tt.setup != nil {
				tt.setup(t, d)
			}

			err := d.uc.Delete(ctx, tt.args.userID, tt.args.endpoint)

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

	tokyoArea := "JP-13"
	osakaArea := "JP-27"
	saitamaArea := "JP-11"
	kanazawaArea := "JP-17"

	artist := &entity.Artist{ID: "artist-1", Name: "Test Artist"}

	concertsInArea := func(adminArea *string) []*entity.Concert {
		return []*entity.Concert{
			{
				Event:    entity.Event{ID: "c1", Title: "Concert 1", Venue: &entity.Venue{AdminArea: adminArea}},
				ArtistID: "artist-1",
			},
		}
	}

	type args struct {
		data usecase.ConcertCreatedData
	}

	tests := []struct {
		name    string
		args    args
		setup   func(t *testing.T, d *pushNotificationTestDeps)
		wantErr error
	}{
		{
			name: "return nil when no followers",
			args: args{data: usecase.ConcertCreatedData{ArtistID: "artist-1", ConcertIDs: []string{"c1"}}},
			setup: func(t *testing.T, d *pushNotificationTestDeps) {
				t.Helper()
				d.artistRepo.EXPECT().Get(ctx, "artist-1").Return(artist, nil).Once()
				d.concertRepo.EXPECT().ListByIDs(ctx, []string{"c1"}).Return(concertsInArea(&tokyoArea), nil).Once()
				d.followRepo.EXPECT().ListFollowers(ctx, "artist-1").Return([]*entity.Follower{}, nil).Once()
			},
			wantErr: nil,
		},
		{
			name: "AWAY follower receives notification",
			args: args{data: usecase.ConcertCreatedData{ArtistID: "artist-1", ConcertIDs: []string{"c1"}}},
			setup: func(t *testing.T, d *pushNotificationTestDeps) {
				t.Helper()
				d.artistRepo.EXPECT().Get(ctx, "artist-1").Return(artist, nil).Once()
				d.concertRepo.EXPECT().ListByIDs(ctx, []string{"c1"}).Return(concertsInArea(&tokyoArea), nil).Once()
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
			args: args{data: usecase.ConcertCreatedData{ArtistID: "artist-1", ConcertIDs: []string{"c1"}}},
			setup: func(t *testing.T, d *pushNotificationTestDeps) {
				t.Helper()
				d.artistRepo.EXPECT().Get(ctx, "artist-1").Return(artist, nil).Once()
				d.concertRepo.EXPECT().ListByIDs(ctx, []string{"c1"}).Return(concertsInArea(&tokyoArea), nil).Once()
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
			args: args{data: usecase.ConcertCreatedData{ArtistID: "artist-1", ConcertIDs: []string{"c1"}}},
			setup: func(t *testing.T, d *pushNotificationTestDeps) {
				t.Helper()
				d.artistRepo.EXPECT().Get(ctx, "artist-1").Return(artist, nil).Once()
				d.concertRepo.EXPECT().ListByIDs(ctx, []string{"c1"}).Return(concertsInArea(&tokyoArea), nil).Once()
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
			args: args{data: usecase.ConcertCreatedData{ArtistID: "artist-1", ConcertIDs: []string{"c1"}}},
			setup: func(t *testing.T, d *pushNotificationTestDeps) {
				t.Helper()
				d.artistRepo.EXPECT().Get(ctx, "artist-1").Return(artist, nil).Once()
				d.concertRepo.EXPECT().ListByIDs(ctx, []string{"c1"}).Return(concertsInArea(&osakaArea), nil).Once()
				followers := []*entity.Follower{
					{ArtistID: "artist-1", User: &entity.User{ID: "user-home", Home: &entity.Home{Level1: "JP-13"}}, Hype: entity.HypeHome},
				}
				d.followRepo.EXPECT().ListFollowers(ctx, "artist-1").Return(followers, nil).Once()
				// No ListByUserIDs call expected — HOME follower filtered out.
			},
			wantErr: nil,
		},
		{
			name: "HOME filter uses only new concerts' venues, not artist's full history",
			args: args{data: usecase.ConcertCreatedData{ArtistID: "artist-1", ConcertIDs: []string{"c-new"}}},
			setup: func(t *testing.T, d *pushNotificationTestDeps) {
				t.Helper()
				d.artistRepo.EXPECT().Get(ctx, "artist-1").Return(artist, nil).Once()
				// The newly created concert is in JP-40 (Ishikawa); the artist's historical
				// concerts include JP-13 (Tokyo), but those are NOT in this batch.
				newConcerts := []*entity.Concert{
					{
						Event:    entity.Event{ID: "c-new", Title: "New Concert", Venue: &entity.Venue{AdminArea: &kanazawaArea}},
						ArtistID: "artist-1",
					},
				}
				d.concertRepo.EXPECT().ListByIDs(ctx, []string{"c-new"}).Return(newConcerts, nil).Once()
				// Follower whose home is JP-13 (Tokyo) should NOT be notified because the
				// new concert is in JP-17 (Ishikawa/Kanazawa), not Tokyo.
				followers := []*entity.Follower{
					{ArtistID: "artist-1", User: &entity.User{ID: "user-tokyo-home", Home: &entity.Home{Level1: "JP-13"}}, Hype: entity.HypeHome},
				}
				d.followRepo.EXPECT().ListFollowers(ctx, "artist-1").Return(followers, nil).Once()
				// No ListByUserIDs call — HOME follower filtered out because JP-17 ≠ JP-13.
			},
			wantErr: nil,
		},
		{
			name: "HOME follower is skipped when no home area set",
			args: args{data: usecase.ConcertCreatedData{ArtistID: "artist-1", ConcertIDs: []string{"c1"}}},
			setup: func(t *testing.T, d *pushNotificationTestDeps) {
				t.Helper()
				d.artistRepo.EXPECT().Get(ctx, "artist-1").Return(artist, nil).Once()
				d.concertRepo.EXPECT().ListByIDs(ctx, []string{"c1"}).Return(concertsInArea(&tokyoArea), nil).Once()
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
			args: args{data: usecase.ConcertCreatedData{ArtistID: "artist-1", ConcertIDs: []string{"c1"}}},
			setup: func(t *testing.T, d *pushNotificationTestDeps) {
				t.Helper()
				d.artistRepo.EXPECT().Get(ctx, "artist-1").Return(artist, nil).Once()
				nearbyConcerts := []*entity.Concert{
					{
						Event: entity.Event{
							ID:    "c1",
							Title: "Concert 1",
							Venue: &entity.Venue{
								AdminArea:   &saitamaArea,
								Coordinates: &entity.Coordinates{Latitude: 35.8569, Longitude: 139.6489},
							},
						},
						ArtistID: "artist-1",
					},
				}
				d.concertRepo.EXPECT().ListByIDs(ctx, []string{"c1"}).Return(nearbyConcerts, nil).Once()
				followers := []*entity.Follower{
					{ArtistID: "artist-1", User: &entity.User{ID: "user-nearby", Home: &entity.Home{Level1: "JP-13", Centroid: &entity.Coordinates{Latitude: 35.6762, Longitude: 139.6503}}}, Hype: entity.HypeNearby},
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
			args: args{data: usecase.ConcertCreatedData{ArtistID: "artist-1", ConcertIDs: []string{"c1"}}},
			setup: func(t *testing.T, d *pushNotificationTestDeps) {
				t.Helper()
				d.artistRepo.EXPECT().Get(ctx, "artist-1").Return(artist, nil).Once()
				farConcerts := []*entity.Concert{
					{
						Event: entity.Event{
							ID:    "c1",
							Title: "Concert 1",
							Venue: &entity.Venue{
								AdminArea:   &osakaArea,
								Coordinates: &entity.Coordinates{Latitude: 34.6863, Longitude: 135.5200},
							},
						},
						ArtistID: "artist-1",
					},
				}
				d.concertRepo.EXPECT().ListByIDs(ctx, []string{"c1"}).Return(farConcerts, nil).Once()
				followers := []*entity.Follower{
					{ArtistID: "artist-1", User: &entity.User{ID: "user-nearby", Home: &entity.Home{Level1: "JP-13", Centroid: &entity.Coordinates{Latitude: 35.6762, Longitude: 139.6503}}}, Hype: entity.HypeNearby},
				}
				d.followRepo.EXPECT().ListFollowers(ctx, "artist-1").Return(followers, nil).Once()
				// No ListByUserIDs — NEARBY follower filtered out.
			},
			wantErr: nil,
		},
		{
			name: "NEARBY follower skipped when no home area set",
			args: args{data: usecase.ConcertCreatedData{ArtistID: "artist-1", ConcertIDs: []string{"c1"}}},
			setup: func(t *testing.T, d *pushNotificationTestDeps) {
				t.Helper()
				d.artistRepo.EXPECT().Get(ctx, "artist-1").Return(artist, nil).Once()
				d.concertRepo.EXPECT().ListByIDs(ctx, []string{"c1"}).Return(concertsInArea(&tokyoArea), nil).Once()
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
			args: args{data: usecase.ConcertCreatedData{ArtistID: "artist-1", ConcertIDs: []string{"c1"}}},
			setup: func(t *testing.T, d *pushNotificationTestDeps) {
				t.Helper()
				d.artistRepo.EXPECT().Get(ctx, "artist-1").Return(artist, nil).Once()
				d.concertRepo.EXPECT().ListByIDs(ctx, []string{"c1"}).Return(concertsInArea(&tokyoArea), nil).Once()
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
			name: "return error when artist lookup fails",
			args: args{data: usecase.ConcertCreatedData{ArtistID: "artist-1", ConcertIDs: []string{"c1"}}},
			setup: func(t *testing.T, d *pushNotificationTestDeps) {
				t.Helper()
				d.artistRepo.EXPECT().Get(ctx, "artist-1").Return(nil, apperr.ErrInternal).Once()
			},
			wantErr: apperr.ErrInternal,
		},
		{
			name: "return error when concert lookup fails",
			args: args{data: usecase.ConcertCreatedData{ArtistID: "artist-1", ConcertIDs: []string{"c1"}}},
			setup: func(t *testing.T, d *pushNotificationTestDeps) {
				t.Helper()
				d.artistRepo.EXPECT().Get(ctx, "artist-1").Return(artist, nil).Once()
				d.concertRepo.EXPECT().ListByIDs(ctx, []string{"c1"}).Return(nil, apperr.ErrInternal).Once()
			},
			wantErr: apperr.ErrInternal,
		},
		{
			name: "return error when ListFollowers fails",
			args: args{data: usecase.ConcertCreatedData{ArtistID: "artist-1", ConcertIDs: []string{"c1"}}},
			setup: func(t *testing.T, d *pushNotificationTestDeps) {
				t.Helper()
				d.artistRepo.EXPECT().Get(ctx, "artist-1").Return(artist, nil).Once()
				d.concertRepo.EXPECT().ListByIDs(ctx, []string{"c1"}).Return(concertsInArea(&tokyoArea), nil).Once()
				d.followRepo.EXPECT().ListFollowers(ctx, "artist-1").Return(nil, apperr.ErrInternal).Once()
			},
			wantErr: apperr.ErrInternal,
		},
		{
			name: "return error when ListByUserIDs fails",
			args: args{data: usecase.ConcertCreatedData{ArtistID: "artist-1", ConcertIDs: []string{"c1"}}},
			setup: func(t *testing.T, d *pushNotificationTestDeps) {
				t.Helper()
				d.artistRepo.EXPECT().Get(ctx, "artist-1").Return(artist, nil).Once()
				d.concertRepo.EXPECT().ListByIDs(ctx, []string{"c1"}).Return(concertsInArea(&tokyoArea), nil).Once()
				followers := []*entity.Follower{
					{ArtistID: "artist-1", User: &entity.User{ID: "user-1"}, Hype: entity.HypeAway},
				}
				d.followRepo.EXPECT().ListFollowers(ctx, "artist-1").Return(followers, nil).Once()
				d.pushSubRepo.EXPECT().
					ListByUserIDs(ctx, []string{"user-1"}).
					Return(nil, apperr.ErrInternal).
					Once()
			},
			wantErr: apperr.ErrInternal,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			d := newPushNotificationTestDeps(t)
			if tt.setup != nil {
				tt.setup(t, d)
			}

			err := d.uc.NotifyNewConcerts(ctx, tt.args.data)

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				return
			}
			assert.NoError(t, err)
		})
	}
}

// notifySenderTestDeps creates deps with an AWAY follower and the given subscriptions,
// reducing boilerplate for sender error-path tests.
func notifySenderTestDeps(t *testing.T, subs []*entity.PushSubscription) *pushNotificationTestDeps {
	t.Helper()
	d := newPushNotificationTestDeps(t)
	ctx := context.Background()

	tokyoArea := "JP-13"
	concerts := []*entity.Concert{
		{Event: entity.Event{ID: "c1", Venue: &entity.Venue{AdminArea: &tokyoArea}}, ArtistID: "artist-1"},
	}
	artist := &entity.Artist{ID: "artist-1", Name: "Test Artist"}
	followers := []*entity.Follower{
		{ArtistID: "artist-1", User: &entity.User{ID: "user-1"}, Hype: entity.HypeAway},
	}

	d.artistRepo.EXPECT().Get(ctx, "artist-1").Return(artist, nil).Once()
	d.concertRepo.EXPECT().ListByIDs(ctx, []string{"c1"}).Return(concerts, nil).Once()
	d.followRepo.EXPECT().ListFollowers(ctx, "artist-1").Return(followers, nil).Once()
	d.pushSubRepo.EXPECT().ListByUserIDs(ctx, []string{"user-1"}).Return(subs, nil).Once()
	return d
}

func TestNotifyNewConcerts_SenderGone_DeletesSubscription(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	sub := &entity.PushSubscription{UserID: "user-1", Endpoint: "https://push.example.com/gone"}
	d := notifySenderTestDeps(t, []*entity.PushSubscription{sub})
	d.sender.sendFn = func(_ context.Context, _ []byte, _ *entity.PushSubscription) error {
		return apperr.ErrNotFound
	}
	d.pushSubRepo.EXPECT().Delete(ctx, "user-1", "https://push.example.com/gone").Return(nil).Once()

	err := d.uc.NotifyNewConcerts(ctx, usecase.ConcertCreatedData{ArtistID: "artist-1", ConcertIDs: []string{"c1"}})
	assert.NoError(t, err)
}

func TestNotifyNewConcerts_SenderGone_DeleteFails_ContinuesProcessing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	sub := &entity.PushSubscription{UserID: "user-1", Endpoint: "https://push.example.com/gone"}
	d := notifySenderTestDeps(t, []*entity.PushSubscription{sub})
	d.sender.sendFn = func(_ context.Context, _ []byte, _ *entity.PushSubscription) error {
		return apperr.ErrNotFound
	}
	d.pushSubRepo.EXPECT().Delete(ctx, "user-1", "https://push.example.com/gone").Return(apperr.ErrInternal).Once()

	err := d.uc.NotifyNewConcerts(ctx, usecase.ConcertCreatedData{ArtistID: "artist-1", ConcertIDs: []string{"c1"}})
	assert.NoError(t, err, "delete failure should be logged but not returned")
}

func TestNotifyNewConcerts_SenderTransientError_LogsAndContinues(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	sub := &entity.PushSubscription{UserID: "user-1", Endpoint: "https://push.example.com/flaky"}
	d := notifySenderTestDeps(t, []*entity.PushSubscription{sub})
	d.sender.sendFn = func(_ context.Context, _ []byte, _ *entity.PushSubscription) error {
		return apperr.ErrInternal
	}
	// No Delete expected — transient errors don't trigger cleanup.

	err := d.uc.NotifyNewConcerts(ctx, usecase.ConcertCreatedData{ArtistID: "artist-1", ConcertIDs: []string{"c1"}})
	assert.NoError(t, err, "transient sender error should be logged but not returned")
}

func TestNotifyNewConcerts_MixedSenderResults(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	subs := []*entity.PushSubscription{
		{UserID: "user-1", Endpoint: "https://push.example.com/ok"},
		{UserID: "user-1", Endpoint: "https://push.example.com/gone"},
		{UserID: "user-1", Endpoint: "https://push.example.com/fail"},
	}
	d := notifySenderTestDeps(t, subs)

	d.sender.sendFn = func(_ context.Context, _ []byte, sub *entity.PushSubscription) error {
		switch sub.Endpoint {
		case "https://push.example.com/ok":
			return nil
		case "https://push.example.com/gone":
			return apperr.ErrNotFound
		case "https://push.example.com/fail":
			return apperr.ErrInternal
		}
		return nil
	}
	d.pushSubRepo.EXPECT().
		Delete(ctx, "user-1", "https://push.example.com/gone").
		Return(nil).
		Once()

	err := d.uc.NotifyNewConcerts(ctx, usecase.ConcertCreatedData{ArtistID: "artist-1", ConcertIDs: []string{"c1"}})
	assert.NoError(t, err)
}
