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

// pushNotificationTestDeps holds all dependencies for PushNotificationUseCase tests.
type pushNotificationTestDeps struct {
	artistRepo     *mocks.MockArtistRepository
	concertRepo    *mocks.MockConcertRepository
	followRepo     *mocks.MockFollowRepository
	pushSubRepo    *mocks.MockPushSubscriptionRepository
	publisher      *ucmocks.MockEventPublisher
	notificationUC *ucmocks.MockNotificationUseCase
	uc             usecase.PushNotificationUseCase
}

func newPushNotificationTestDeps(t *testing.T) *pushNotificationTestDeps {
	t.Helper()
	d := &pushNotificationTestDeps{
		artistRepo:     mocks.NewMockArtistRepository(t),
		concertRepo:    mocks.NewMockConcertRepository(t),
		followRepo:     mocks.NewMockFollowRepository(t),
		pushSubRepo:    mocks.NewMockPushSubscriptionRepository(t),
		publisher:      ucmocks.NewMockEventPublisher(t),
		notificationUC: ucmocks.NewMockNotificationUseCase(t),
	}
	d.uc = usecase.NewPushNotificationUseCase(
		d.artistRepo,
		d.concertRepo,
		d.followRepo,
		d.pushSubRepo,
		d.publisher,
		d.notificationUC,
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
			name: "persist subscription successfully and publish analytics event",
			args: args{
				userID:   "user-1",
				endpoint: "https://fcm.googleapis.com/sub/abc",
				p256dh:   "key123",
				auth:     "auth456",
			},
			setup: func(t *testing.T, d *pushNotificationTestDeps) {
				t.Helper()
				d.pushSubRepo.EXPECT().
					Create(ctx, &entity.PushSubscription{
						UserID:   "user-1",
						Endpoint: "https://fcm.googleapis.com/sub/abc",
						P256dh:   "key123",
						Auth:     "auth456",
					}).
					Return(nil).
					Once()
				d.publisher.EXPECT().
					PublishEvent(ctx, entity.SubjectNotificationSubscribed, entity.NotificationSubscribedData{
						UserID:     "user-1",
						DeviceType: "android",
					}).
					Return(nil).Once()
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
			name: "delete subscription successfully and publish analytics event",
			args: args{userID: "user-1", endpoint: "https://fcm.googleapis.com/sub/abc"},
			setup: func(t *testing.T, d *pushNotificationTestDeps) {
				t.Helper()
				d.pushSubRepo.EXPECT().
					Delete(ctx, "user-1", "https://fcm.googleapis.com/sub/abc").
					Return(nil).
					Once()
				d.publisher.EXPECT().
					PublishEvent(ctx, entity.SubjectNotificationUnsubscribed, entity.NotificationUnsubscribedData{
						UserID:     "user-1",
						DeviceType: "android",
					}).
					Return(nil).Once()
			},
			wantErr: nil,
		},
		{
			name: "publish error is non-fatal when repository delete succeeds",
			args: args{userID: "user-1", endpoint: "https://web.push.apple.com/sub/abc"},
			setup: func(t *testing.T, d *pushNotificationTestDeps) {
				t.Helper()
				d.pushSubRepo.EXPECT().
					Delete(ctx, "user-1", "https://web.push.apple.com/sub/abc").
					Return(nil).
					Once()
				d.publisher.EXPECT().
					PublishEvent(ctx, entity.SubjectNotificationUnsubscribed, entity.NotificationUnsubscribedData{
						UserID:     "user-1",
						DeviceType: "apple",
					}).
					Return(apperr.ErrInternal).Once()
			},
			// Publish error must not propagate — Delete returns nil.
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
				// No PublishEvent expected — repo failure prevents reaching the publish call.
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
				Event:      entity.Event{ID: "c1", Venue: &entity.Venue{AdminArea: adminArea}},
				Performers: []*entity.Artist{{ID: "artist-1"}},
			},
		}
	}

	// deliveredNotification returns a Notification with DeliveryStatus=Delivered,
	// representing a successful Notify call.
	deliveredNotification := func() *entity.Notification {
		return &entity.Notification{ID: "notif-1", DeliveryStatus: entity.NotificationDeliveryStatusDelivered}
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
				d.notificationUC.EXPECT().
					Notify(anyCtx, "user-1", entity.NotificationTypeNewConcerts, mock.AnythingOfType("*entity.NotificationPayload")).
					Return(deliveredNotification(), nil).
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
				// No Notify call expected — WATCH follower is filtered out.
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
				d.notificationUC.EXPECT().
					Notify(anyCtx, "user-home", entity.NotificationTypeNewConcerts, mock.AnythingOfType("*entity.NotificationPayload")).
					Return(deliveredNotification(), nil).
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
				// No Notify call expected — HOME follower filtered out.
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
						Event:      entity.Event{ID: "c-new", Venue: &entity.Venue{AdminArea: &kanazawaArea}},
						Performers: []*entity.Artist{{ID: "artist-1"}},
					},
				}
				d.concertRepo.EXPECT().ListByIDs(ctx, []string{"c-new"}).Return(newConcerts, nil).Once()
				// Follower whose home is JP-13 (Tokyo) should NOT be notified because the
				// new concert is in JP-17 (Ishikawa/Kanazawa), not Tokyo.
				followers := []*entity.Follower{
					{ArtistID: "artist-1", User: &entity.User{ID: "user-tokyo-home", Home: &entity.Home{Level1: "JP-13"}}, Hype: entity.HypeHome},
				}
				d.followRepo.EXPECT().ListFollowers(ctx, "artist-1").Return(followers, nil).Once()
				// No Notify call — HOME follower filtered out because JP-17 ≠ JP-13.
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
				// No Notify call expected — no home area set.
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
							ID: "c1",
							Venue: &entity.Venue{
								AdminArea:   &saitamaArea,
								Coordinates: &entity.Coordinates{Latitude: 35.8569, Longitude: 139.6489},
							},
						},
						Performers: []*entity.Artist{{ID: "artist-1"}},
					},
				}
				d.concertRepo.EXPECT().ListByIDs(ctx, []string{"c1"}).Return(nearbyConcerts, nil).Once()
				followers := []*entity.Follower{
					{ArtistID: "artist-1", User: &entity.User{ID: "user-nearby", Home: &entity.Home{Level1: "JP-13", Centroid: &entity.Coordinates{Latitude: 35.6762, Longitude: 139.6503}}}, Hype: entity.HypeNearby},
				}
				d.followRepo.EXPECT().ListFollowers(ctx, "artist-1").Return(followers, nil).Once()
				d.notificationUC.EXPECT().
					Notify(anyCtx, "user-nearby", entity.NotificationTypeNewConcerts, mock.AnythingOfType("*entity.NotificationPayload")).
					Return(deliveredNotification(), nil).
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
							ID: "c1",
							Venue: &entity.Venue{
								AdminArea:   &osakaArea,
								Coordinates: &entity.Coordinates{Latitude: 34.6863, Longitude: 135.5200},
							},
						},
						Performers: []*entity.Artist{{ID: "artist-1"}},
					},
				}
				d.concertRepo.EXPECT().ListByIDs(ctx, []string{"c1"}).Return(farConcerts, nil).Once()
				followers := []*entity.Follower{
					{ArtistID: "artist-1", User: &entity.User{ID: "user-nearby", Home: &entity.Home{Level1: "JP-13", Centroid: &entity.Coordinates{Latitude: 35.6762, Longitude: 139.6503}}}, Hype: entity.HypeNearby},
				}
				d.followRepo.EXPECT().ListFollowers(ctx, "artist-1").Return(followers, nil).Once()
				// No Notify — NEARBY follower filtered out.
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
				// No Notify — no home area set.
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
				// Only user-home-match and user-away are eligible.
				d.notificationUC.EXPECT().
					Notify(anyCtx, "user-home-match", entity.NotificationTypeNewConcerts, mock.AnythingOfType("*entity.NotificationPayload")).
					Return(deliveredNotification(), nil).
					Once()
				d.notificationUC.EXPECT().
					Notify(anyCtx, "user-away", entity.NotificationTypeNewConcerts, mock.AnythingOfType("*entity.NotificationPayload")).
					Return(deliveredNotification(), nil).
					Once()
			},
			wantErr: nil,
		},
		{
			name: "error - InvalidArgument when concert_id not found in repo",
			args: args{data: usecase.ConcertCreatedData{ArtistID: "artist-1", ConcertIDs: []string{"c1", "c2"}}},
			setup: func(t *testing.T, d *pushNotificationTestDeps) {
				t.Helper()
				d.artistRepo.EXPECT().Get(ctx, "artist-1").Return(artist, nil).Once()
				// ListByIDs returns only c1 — c2 is missing from the result.
				d.concertRepo.EXPECT().ListByIDs(ctx, []string{"c1", "c2"}).Return([]*entity.Concert{
					{Event: entity.Event{ID: "c1"}, Performers: []*entity.Artist{{ID: "artist-1"}}},
				}, nil).Once()
			},
			wantErr: apperr.ErrInvalidArgument,
		},
		{
			name: "error - InvalidArgument when concert belongs to different artist",
			args: args{data: usecase.ConcertCreatedData{ArtistID: "artist-1", ConcertIDs: []string{"c1"}}},
			setup: func(t *testing.T, d *pushNotificationTestDeps) {
				t.Helper()
				d.artistRepo.EXPECT().Get(ctx, "artist-1").Return(artist, nil).Once()
				// c1 exists but is performed by a different artist.
				d.concertRepo.EXPECT().ListByIDs(ctx, []string{"c1"}).Return([]*entity.Concert{
					{Event: entity.Event{ID: "c1"}, Performers: []*entity.Artist{{ID: "artist-999"}}},
				}, nil).Once()
			},
			wantErr: apperr.ErrInvalidArgument,
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
			name: "return error when Notify fails",
			args: args{data: usecase.ConcertCreatedData{ArtistID: "artist-1", ConcertIDs: []string{"c1"}}},
			setup: func(t *testing.T, d *pushNotificationTestDeps) {
				t.Helper()
				d.artistRepo.EXPECT().Get(ctx, "artist-1").Return(artist, nil).Once()
				d.concertRepo.EXPECT().ListByIDs(ctx, []string{"c1"}).Return(concertsInArea(&tokyoArea), nil).Once()
				followers := []*entity.Follower{
					{ArtistID: "artist-1", User: &entity.User{ID: "user-1"}, Hype: entity.HypeAway},
				}
				d.followRepo.EXPECT().ListFollowers(ctx, "artist-1").Return(followers, nil).Once()
				d.notificationUC.EXPECT().
					Notify(anyCtx, "user-1", entity.NotificationTypeNewConcerts, mock.AnythingOfType("*entity.NotificationPayload")).
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

// TestNotifyNewConcerts_LocalizesBodyPerRecipient verifies that each recipient
// receives a Notify call whose payload Body is localized to their preferred language.
func TestNotifyNewConcerts_LocalizesBodyPerRecipient(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	d := newPushNotificationTestDeps(t)

	tokyoArea := "JP-13"
	concerts := []*entity.Concert{
		{Event: entity.Event{ID: "c1", Venue: &entity.Venue{AdminArea: &tokyoArea}}, Performers: []*entity.Artist{{ID: "artist-1"}}},
	}
	artist := &entity.Artist{ID: "artist-1", Name: "Test Artist"}
	followers := []*entity.Follower{
		{ArtistID: "artist-1", User: &entity.User{ID: "user-ja", PreferredLanguage: "ja"}, Hype: entity.HypeAway},
		{ArtistID: "artist-1", User: &entity.User{ID: "user-en", PreferredLanguage: "en"}, Hype: entity.HypeAway},
		{ArtistID: "artist-1", User: &entity.User{ID: "user-unset"}, Hype: entity.HypeAway},
	}

	d.artistRepo.EXPECT().Get(ctx, "artist-1").Return(artist, nil).Once()
	d.concertRepo.EXPECT().ListByIDs(ctx, []string{"c1"}).Return(concerts, nil).Once()
	d.followRepo.EXPECT().ListFollowers(ctx, "artist-1").Return(followers, nil).Once()

	deliveredNotif := &entity.Notification{ID: "notif-1", DeliveryStatus: entity.NotificationDeliveryStatusDelivered}

	// Assert each recipient receives a payload with the correct localized body.
	d.notificationUC.EXPECT().
		Notify(anyCtx, "user-ja", entity.NotificationTypeNewConcerts,
			mock.MatchedBy(func(p *entity.NotificationPayload) bool {
				return p.Body == "新しいライブが1件見つかりました"
			})).
		Return(deliveredNotif, nil).
		Once()
	d.notificationUC.EXPECT().
		Notify(anyCtx, "user-en", entity.NotificationTypeNewConcerts,
			mock.MatchedBy(func(p *entity.NotificationPayload) bool {
				return p.Body == "1 new concert found"
			})).
		Return(deliveredNotif, nil).
		Once()
	d.notificationUC.EXPECT().
		Notify(anyCtx, "user-unset", entity.NotificationTypeNewConcerts,
			mock.MatchedBy(func(p *entity.NotificationPayload) bool {
				return p.Body == "1 new concert found" // unset language falls back to en
			})).
		Return(deliveredNotif, nil).
		Once()

	err := d.uc.NotifyNewConcerts(ctx, usecase.ConcertCreatedData{ArtistID: "artist-1", ConcertIDs: []string{"c1"}})
	assert.NoError(t, err)
}

// TestNotifyNewConcerts_PluralBodyPerLanguage verifies that the plural form is
// used in each language's body when there are multiple new concerts.
func TestNotifyNewConcerts_PluralBodyPerLanguage(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	d := newPushNotificationTestDeps(t)

	tokyoArea := "JP-13"
	concerts := []*entity.Concert{
		{Event: entity.Event{ID: "c1", Venue: &entity.Venue{AdminArea: &tokyoArea}}, Performers: []*entity.Artist{{ID: "artist-1"}}},
		{Event: entity.Event{ID: "c2", Venue: &entity.Venue{AdminArea: &tokyoArea}}, Performers: []*entity.Artist{{ID: "artist-1"}}},
	}
	artist := &entity.Artist{ID: "artist-1", Name: "Test Artist"}
	followers := []*entity.Follower{
		{ArtistID: "artist-1", User: &entity.User{ID: "user-ja", PreferredLanguage: "ja"}, Hype: entity.HypeAway},
		{ArtistID: "artist-1", User: &entity.User{ID: "user-en", PreferredLanguage: "en"}, Hype: entity.HypeAway},
	}

	d.artistRepo.EXPECT().Get(ctx, "artist-1").Return(artist, nil).Once()
	d.concertRepo.EXPECT().ListByIDs(ctx, []string{"c1", "c2"}).Return(concerts, nil).Once()
	d.followRepo.EXPECT().ListFollowers(ctx, "artist-1").Return(followers, nil).Once()

	deliveredNotif := &entity.Notification{ID: "notif-1", DeliveryStatus: entity.NotificationDeliveryStatusDelivered}

	d.notificationUC.EXPECT().
		Notify(anyCtx, "user-ja", entity.NotificationTypeNewConcerts,
			mock.MatchedBy(func(p *entity.NotificationPayload) bool {
				return p.Body == "新しいライブが2件見つかりました"
			})).
		Return(deliveredNotif, nil).
		Once()
	d.notificationUC.EXPECT().
		Notify(anyCtx, "user-en", entity.NotificationTypeNewConcerts,
			mock.MatchedBy(func(p *entity.NotificationPayload) bool {
				return p.Body == "2 new concerts found" // plural form for N>1
			})).
		Return(deliveredNotif, nil).
		Once()

	err := d.uc.NotifyNewConcerts(ctx, usecase.ConcertCreatedData{ArtistID: "artist-1", ConcertIDs: []string{"c1", "c2"}})
	assert.NoError(t, err)
}
