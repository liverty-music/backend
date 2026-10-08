package usecase_test

import (
	"context"
	"testing"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/entity/mocks"
	"github.com/liverty-music/backend/internal/usecase"
	ucmocks "github.com/liverty-music/backend/internal/usecase/mocks"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// pushNotificationTestDeps holds all dependencies for PushNotificationUseCase tests.
type pushNotificationTestDeps struct {
	artistRepo  *mocks.MockArtistRepository
	concertRepo *mocks.MockConcertRepository
	followRepo  *mocks.MockFollowRepository
	pushSubRepo *mocks.MockPushSubscriptionRepository
	publisher   *ucmocks.MockEventPublisher
	uc          usecase.PushNotificationUseCase
}

func newPushNotificationTestDeps(t *testing.T) *pushNotificationTestDeps {
	t.Helper()
	d := &pushNotificationTestDeps{
		artistRepo:  mocks.NewMockArtistRepository(t),
		concertRepo: mocks.NewMockConcertRepository(t),
		followRepo:  mocks.NewMockFollowRepository(t),
		pushSubRepo: mocks.NewMockPushSubscriptionRepository(t),
		publisher:   ucmocks.NewMockEventPublisher(t),
	}
	d.uc = usecase.NewPushNotificationUseCase(
		d.artistRepo,
		d.concertRepo,
		d.followRepo,
		d.pushSubRepo,
		d.publisher,
		newTestLogger(t),
	)
	return d
}

// expectNotificationRequested sets up a PublishEventWithID expectation
// matching a NOTIFICATION.requested publish for the given recipient and
// notification type — the request NotifyNewConcerts / AnnounceDiscoveredPhase
// now issue instead of calling NotificationUseCase.Deliver directly. The id
// argument is deliberately not asserted here (it is a deterministic hash of
// business keys, covered by its own test); mock.AnythingOfType("string")
// matches whatever notificationRequestMsgID derives.
func expectNotificationRequested(t *testing.T, publisher *ucmocks.MockEventPublisher, userID string, typ entity.NotificationType) *mock.Call {
	t.Helper()
	return publisher.EXPECT().
		PublishEventWithID(anyCtx, entity.SubjectNotificationRequested, mock.AnythingOfType("string"),
			mock.MatchedBy(func(data entity.NotificationRequestedData) bool {
				return data.UserID == userID && data.Type == typ
			})).
		Return(nil).
		Once()
}

// expectNotificationRequestedMatching is [expectNotificationRequested] with an
// additional predicate over the requested payload, for tests that assert on
// rendered copy (localized body, deep-link, etc.).
func expectNotificationRequestedMatching(
	t *testing.T,
	publisher *ucmocks.MockEventPublisher,
	userID string,
	typ entity.NotificationType,
	payloadMatches func(p *entity.NotificationPayload) bool,
) *mock.Call {
	t.Helper()
	return publisher.EXPECT().
		PublishEventWithID(anyCtx, entity.SubjectNotificationRequested, mock.AnythingOfType("string"),
			mock.MatchedBy(func(data entity.NotificationRequestedData) bool {
				return data.UserID == userID && data.Type == typ && payloadMatches(data.Payload)
			})).
		Return(nil).
		Once()
}

func TestPushNotificationUseCase_Create(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	type args struct {
		userID     string
		endpoint   string
		p256dh     string
		auth       string
		deviceType string
	}

	tests := []struct {
		name    string
		args    args
		setup   func(t *testing.T, d *pushNotificationTestDeps)
		wantID  string
		wantErr error
	}{
		{
			name: "persist subscription successfully and publish analytics event",
			args: args{
				userID:     "user-1",
				endpoint:   "https://fcm.googleapis.com/sub/abc",
				p256dh:     "key123",
				auth:       "auth456",
				deviceType: "android",
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
			// The usecase must surface the id the repository actually stored
			// the subscription under, not the (possibly newly minted, unstored)
			// in-memory id — the repository mutates sub.ID via the pointer on
			// both a fresh insert and a re-registration. See
			// liverty-music/backend#474.
			name: "return the id the repository stored the subscription under",
			args: args{
				userID:     "user-1",
				endpoint:   "https://fcm.googleapis.com/sub/abc",
				p256dh:     "key123",
				auth:       "auth456",
				deviceType: "android",
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
					Run(func(_ context.Context, sub *entity.PushSubscription) {
						// Simulate the repository's UPSERT ... RETURNING id: the
						// endpoint was already registered, so the row's existing
						// stored id is written back, not a freshly minted one.
						sub.ID = "existing-stored-id"
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
			wantID:  "existing-stored-id",
			wantErr: nil,
		},
		{
			name: "return error when repository fails",
			args: args{
				userID:     "user-1",
				endpoint:   "https://push.example.com/sub/abc",
				p256dh:     "key123",
				auth:       "auth456",
				deviceType: "other",
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

			sub, err := d.uc.Create(ctx, tt.args.userID, tt.args.endpoint, tt.args.p256dh, tt.args.auth, tt.args.deviceType)

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				return
			}
			assert.NoError(t, err)
			assert.Equal(t, tt.args.userID, sub.UserID)
			assert.Equal(t, tt.args.endpoint, sub.Endpoint)
			if tt.wantID != "" {
				assert.Equal(t, tt.wantID, sub.ID)
			}
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
		userID     string
		endpoint   string
		deviceType string
	}

	tests := []struct {
		name    string
		args    args
		setup   func(t *testing.T, d *pushNotificationTestDeps)
		wantErr error
	}{
		{
			name: "delete subscription successfully and publish analytics event",
			args: args{userID: "user-1", endpoint: "https://fcm.googleapis.com/sub/abc", deviceType: "android"},
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
			args: args{userID: "user-1", endpoint: "https://web.push.apple.com/sub/abc", deviceType: "apple"},
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
			args: args{userID: "user-1", endpoint: "https://push.example.com/sub", deviceType: "other"},
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

			err := d.uc.Delete(ctx, tt.args.userID, tt.args.endpoint, tt.args.deviceType)

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
	yamanashiArea := "JP-19"

	artist := &entity.Artist{ID: "artist-1", Name: "Test Artist"}

	concertsInArea := func(adminArea *string) []*entity.Concert {
		return []*entity.Concert{
			{
				ID: "c1", Venue: &entity.Venue{AdminArea: adminArea},
				Performers: []*entity.Artist{{ID: "artist-1"}},
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
				expectNotificationRequested(t, d.publisher, "user-1", entity.NotificationTypeNewConcerts)
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
				expectNotificationRequested(t, d.publisher, "user-home", entity.NotificationTypeNewConcerts)
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
						ID: "c-new", Venue: &entity.Venue{AdminArea: &kanazawaArea},
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
						ID: "c1",
						Venue: &entity.Venue{
							AdminArea:   &saitamaArea,
							Coordinates: &entity.Coordinates{Latitude: 35.8569, Longitude: 139.6489},
						},
						Performers: []*entity.Artist{{ID: "artist-1"}},
					},
				}
				d.concertRepo.EXPECT().ListByIDs(ctx, []string{"c1"}).Return(nearbyConcerts, nil).Once()
				followers := []*entity.Follower{
					{ArtistID: "artist-1", User: &entity.User{ID: "user-nearby", Home: &entity.Home{Level1: "JP-13", Centroid: &entity.Coordinates{Latitude: 35.6762, Longitude: 139.6503}}}, Hype: entity.HypeNearby},
				}
				d.followRepo.EXPECT().ListFollowers(ctx, "artist-1").Return(followers, nil).Once()
				expectNotificationRequested(t, d.publisher, "user-nearby", entity.NotificationTypeNewConcerts)
			},
			wantErr: nil,
		},
		{
			// Regression test for backend#469: followListFollowersQuery previously
			// dropped the home centroid, so ProximityTo always fell back to AWAY for
			// NEARBY followers outside their home area. Mirrors the store scenario
			// "Nearby follower and a new concert in range" (usecase/notification/
			// notify-new-concerts): a concert 150km from the follower's home centre,
			// outside the follower's home area (JP-13 Tokyo vs. JP-19 Yamanashi).
			name: "NEARBY follower notified when concert is 150km away in another prefecture",
			args: args{data: usecase.ConcertCreatedData{ArtistID: "artist-1", ConcertIDs: []string{"c1"}}},
			setup: func(t *testing.T, d *pushNotificationTestDeps) {
				t.Helper()
				d.artistRepo.EXPECT().Get(ctx, "artist-1").Return(artist, nil).Once()
				nearbyConcerts := []*entity.Concert{
					{
						ID: "c1",
						Venue: &entity.Venue{
							AdminArea: &yamanashiArea,
							// ~150km west of the Tokyo (JP-13) centroid below.
							Coordinates: &entity.Coordinates{Latitude: 35.6648, Longitude: 137.9898},
						},
						Performers: []*entity.Artist{{ID: "artist-1"}},
					},
				}
				d.concertRepo.EXPECT().ListByIDs(ctx, []string{"c1"}).Return(nearbyConcerts, nil).Once()
				followers := []*entity.Follower{
					{ArtistID: "artist-1", User: &entity.User{ID: "user-nearby", Home: &entity.Home{Level1: "JP-13", Centroid: &entity.Coordinates{Latitude: 35.6762, Longitude: 139.6503}}}, Hype: entity.HypeNearby},
				}
				d.followRepo.EXPECT().ListFollowers(ctx, "artist-1").Return(followers, nil).Once()
				expectNotificationRequested(t, d.publisher, "user-nearby", entity.NotificationTypeNewConcerts)
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
						ID: "c1",
						Venue: &entity.Venue{
							AdminArea:   &osakaArea,
							Coordinates: &entity.Coordinates{Latitude: 34.6863, Longitude: 135.5200},
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
				expectNotificationRequested(t, d.publisher, "user-home-match", entity.NotificationTypeNewConcerts)
				expectNotificationRequested(t, d.publisher, "user-away", entity.NotificationTypeNewConcerts)
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
					{ID: "c1", Performers: []*entity.Artist{{ID: "artist-1"}}},
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
					{ID: "c1", Performers: []*entity.Artist{{ID: "artist-999"}}},
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
			// @spec components/usecase/notification/notify-new-concerts "Every matched follower is requested"
			name: "request a notification for each of three matched AWAY followers",
			args: args{data: usecase.ConcertCreatedData{ArtistID: "artist-1", ConcertIDs: []string{"c1"}}},
			setup: func(t *testing.T, d *pushNotificationTestDeps) {
				t.Helper()
				d.artistRepo.EXPECT().Get(ctx, "artist-1").Return(artist, nil).Once()
				d.concertRepo.EXPECT().ListByIDs(ctx, []string{"c1"}).Return(concertsInArea(&tokyoArea), nil).Once()
				followers := []*entity.Follower{
					{ArtistID: "artist-1", User: &entity.User{ID: "user-1"}, Hype: entity.HypeAway},
					{ArtistID: "artist-1", User: &entity.User{ID: "user-2"}, Hype: entity.HypeAway},
					{ArtistID: "artist-1", User: &entity.User{ID: "user-3"}, Hype: entity.HypeAway},
				}
				d.followRepo.EXPECT().ListFollowers(ctx, "artist-1").Return(followers, nil).Once()
				// One request per follower, each Once(): an extra, missing or
				// mismatched request fails the mock.
				expectNotificationRequested(t, d.publisher, "user-1", entity.NotificationTypeNewConcerts)
				expectNotificationRequested(t, d.publisher, "user-2", entity.NotificationTypeNewConcerts)
				expectNotificationRequested(t, d.publisher, "user-3", entity.NotificationTypeNewConcerts)
			},
			wantErr: nil,
		},
		{
			// @spec components/usecase/notification/notify-new-concerts "Request fails for one follower"
			name: "return error when publishing the notification request fails",
			args: args{data: usecase.ConcertCreatedData{ArtistID: "artist-1", ConcertIDs: []string{"c1"}}},
			setup: func(t *testing.T, d *pushNotificationTestDeps) {
				t.Helper()
				d.artistRepo.EXPECT().Get(ctx, "artist-1").Return(artist, nil).Once()
				d.concertRepo.EXPECT().ListByIDs(ctx, []string{"c1"}).Return(concertsInArea(&tokyoArea), nil).Once()
				followers := []*entity.Follower{
					{ArtistID: "artist-1", User: &entity.User{ID: "user-1"}, Hype: entity.HypeAway},
					{ArtistID: "artist-1", User: &entity.User{ID: "user-2"}, Hype: entity.HypeAway},
					{ArtistID: "artist-1", User: &entity.User{ID: "user-3"}, Hype: entity.HypeAway},
				}
				d.followRepo.EXPECT().ListFollowers(ctx, "artist-1").Return(followers, nil).Once()
				expectNotificationRequested(t, d.publisher, "user-1", entity.NotificationTypeNewConcerts)
				// The second publish fails; no request is expected for user-3,
				// so a third publish would fail the mock.
				d.publisher.EXPECT().
					PublishEventWithID(anyCtx, entity.SubjectNotificationRequested, mock.AnythingOfType("string"),
						mock.MatchedBy(func(data entity.NotificationRequestedData) bool { return data.UserID == "user-2" })).
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
// @spec components/usecase/notification/notify-new-concerts "One concert in English"
// @spec components/usecase/notification/notify-new-concerts "Unsupported or missing language"
func TestNotifyNewConcerts_LocalizesBodyPerRecipient(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	d := newPushNotificationTestDeps(t)

	tokyoArea := "JP-13"
	concerts := []*entity.Concert{
		{ID: "c1", Venue: &entity.Venue{AdminArea: &tokyoArea}, Performers: []*entity.Artist{{ID: "artist-1"}}},
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

	// Assert each recipient's requested payload carries the correct localized body.
	expectNotificationRequestedMatching(t, d.publisher, "user-ja", entity.NotificationTypeNewConcerts, func(p *entity.NotificationPayload) bool {
		return p.Body == "新しいライブが1件見つかりました"
	})
	expectNotificationRequestedMatching(t, d.publisher, "user-en", entity.NotificationTypeNewConcerts, func(p *entity.NotificationPayload) bool {
		return p.Body == "1 new concert found"
	})
	expectNotificationRequestedMatching(t, d.publisher, "user-unset", entity.NotificationTypeNewConcerts, func(p *entity.NotificationPayload) bool {
		return p.Body == "1 new concert found" // unset language falls back to en
	})

	err := d.uc.NotifyNewConcerts(ctx, usecase.ConcertCreatedData{ArtistID: "artist-1", ConcertIDs: []string{"c1"}})
	assert.NoError(t, err)
}

// TestNotifyNewConcerts_PluralBodyPerLanguage verifies that the plural form is
// used in each language's body when there are multiple new concerts.
// @spec components/usecase/notification/notify-new-concerts "Japanese"
func TestNotifyNewConcerts_PluralBodyPerLanguage(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	d := newPushNotificationTestDeps(t)

	tokyoArea := "JP-13"
	concerts := []*entity.Concert{
		{ID: "c1", Venue: &entity.Venue{AdminArea: &tokyoArea}, Performers: []*entity.Artist{{ID: "artist-1"}}},
		{ID: "c2", Venue: &entity.Venue{AdminArea: &tokyoArea}, Performers: []*entity.Artist{{ID: "artist-1"}}},
	}
	artist := &entity.Artist{ID: "artist-1", Name: "Test Artist"}
	followers := []*entity.Follower{
		{ArtistID: "artist-1", User: &entity.User{ID: "user-ja", PreferredLanguage: "ja"}, Hype: entity.HypeAway},
		{ArtistID: "artist-1", User: &entity.User{ID: "user-en", PreferredLanguage: "en"}, Hype: entity.HypeAway},
	}

	d.artistRepo.EXPECT().Get(ctx, "artist-1").Return(artist, nil).Once()
	d.concertRepo.EXPECT().ListByIDs(ctx, []string{"c1", "c2"}).Return(concerts, nil).Once()
	d.followRepo.EXPECT().ListFollowers(ctx, "artist-1").Return(followers, nil).Once()

	expectNotificationRequestedMatching(t, d.publisher, "user-ja", entity.NotificationTypeNewConcerts, func(p *entity.NotificationPayload) bool {
		return p.Body == "新しいライブが2件見つかりました"
	})
	expectNotificationRequestedMatching(t, d.publisher, "user-en", entity.NotificationTypeNewConcerts, func(p *entity.NotificationPayload) bool {
		return p.Body == "2 new concerts found" // plural form for N>1
	})

	err := d.uc.NotifyNewConcerts(ctx, usecase.ConcertCreatedData{ArtistID: "artist-1", ConcertIDs: []string{"c1", "c2"}})
	assert.NoError(t, err)
}

// newPushNotificationUCWithLogger builds the use case with a capture logger so a
// test can assert on the zero-recipient early-return logs.
func newPushNotificationUCWithLogger(t *testing.T, d *pushNotificationTestDeps, logger *logging.Logger) usecase.PushNotificationUseCase {
	t.Helper()
	return usecase.NewPushNotificationUseCase(
		d.artistRepo,
		d.concertRepo,
		d.followRepo,
		d.pushSubRepo,
		d.publisher,
		logger,
	)
}

// TestNotifyNewConcerts_ZeroRecipientPathsAreLogged verifies each zero-dispatch
// early-return emits a diagnostic log with its reason and the artist id, instead
// of returning silently.
func TestNotifyNewConcerts_ZeroRecipientPathsAreLogged(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	tokyoArea := "JP-13"
	artist := &entity.Artist{ID: "artist-1", Name: "Test Artist"}
	concert := []*entity.Concert{
		{ID: "c1", Venue: &entity.Venue{AdminArea: &tokyoArea}, Performers: []*entity.Artist{{ID: "artist-1"}}},
	}

	tests := []struct {
		name       string
		setup      func(t *testing.T, d *pushNotificationTestDeps)
		wantReason string
	}{
		{
			name: "no followers",
			setup: func(t *testing.T, d *pushNotificationTestDeps) {
				t.Helper()
				d.artistRepo.EXPECT().Get(ctx, "artist-1").Return(artist, nil).Once()
				d.concertRepo.EXPECT().ListByIDs(ctx, []string{"c1"}).Return(concert, nil).Once()
				d.followRepo.EXPECT().ListFollowers(ctx, "artist-1").Return([]*entity.Follower{}, nil).Once()
			},
			wantReason: `"reason":"no_followers"`,
		},
		{
			name: "no eligible recipients after hype filtering",
			setup: func(t *testing.T, d *pushNotificationTestDeps) {
				t.Helper()
				d.artistRepo.EXPECT().Get(ctx, "artist-1").Return(artist, nil).Once()
				d.concertRepo.EXPECT().ListByIDs(ctx, []string{"c1"}).Return(concert, nil).Once()
				// A WATCH follower is never eligible ⇒ zero dispatches.
				d.followRepo.EXPECT().ListFollowers(ctx, "artist-1").Return([]*entity.Follower{
					{ArtistID: "artist-1", User: &entity.User{ID: "user-watch"}, Hype: entity.HypeWatch},
				}, nil).Once()
			},
			wantReason: `"reason":"no_eligible_recipients"`,
		},
		{
			name: "no deliverable concerts (all orphans)",
			setup: func(t *testing.T, d *pushNotificationTestDeps) {
				t.Helper()
				d.artistRepo.EXPECT().Get(ctx, "artist-1").Return(artist, nil).Once()
				// The only concert has no performers ⇒ dropped as an orphan ⇒ empty set.
				d.concertRepo.EXPECT().ListByIDs(ctx, []string{"c1"}).Return([]*entity.Concert{
					{ID: "c1", Venue: &entity.Venue{AdminArea: &tokyoArea}},
				}, nil).Once()
			},
			wantReason: `"reason":"no_deliverable_concerts"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			d := newPushNotificationTestDeps(t)
			tt.setup(t, d)
			logger, buf := newCaptureLogger(t)
			uc := newPushNotificationUCWithLogger(t, d, logger)

			err := uc.NotifyNewConcerts(ctx, usecase.ConcertCreatedData{ArtistID: "artist-1", ConcertIDs: []string{"c1"}})
			require.NoError(t, err)

			logs := buf.String()
			assert.Contains(t, logs, tt.wantReason)
			assert.Contains(t, logs, `"artist_id":"artist-1"`)
		})
	}
}

// payloadURL extracts the deep-link URL from a notification payload's data map.
func payloadURL(p *entity.NotificationPayload) string {
	return p.Data[entity.NotificationDataKeyURL]
}

// TestNotifyNewConcerts_DeepLinksToEarliestMatched verifies that an AWAY
// recipient's notification deep-links to the earliest concert of the batch,
// regardless of the order the concerts arrive in.
// @spec components/usecase/notification/notify-new-concerts "Link to the earliest matched concert"
func TestNotifyNewConcerts_DeepLinksToEarliestMatched(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	d := newPushNotificationTestDeps(t)

	tokyoArea := "JP-13"
	date := func(day int) time.Time { return time.Date(2026, 9, day, 0, 0, 0, 0, time.UTC) }
	// The later concert is listed first to prove ordering is by date, not slice order.
	concerts := []*entity.Concert{
		{ID: "c-late", LocalDate: date(10), Venue: &entity.Venue{AdminArea: &tokyoArea}, Performers: []*entity.Artist{{ID: "artist-1"}}},
		{ID: "c-early", LocalDate: date(3), Venue: &entity.Venue{AdminArea: &tokyoArea}, Performers: []*entity.Artist{{ID: "artist-1"}}},
	}
	artist := &entity.Artist{ID: "artist-1", Name: "Test Artist"}
	followers := []*entity.Follower{
		{ArtistID: "artist-1", User: &entity.User{ID: "user-away"}, Hype: entity.HypeAway},
	}

	d.artistRepo.EXPECT().Get(ctx, "artist-1").Return(artist, nil).Once()
	d.concertRepo.EXPECT().ListByIDs(ctx, []string{"c-late", "c-early"}).Return(concerts, nil).Once()
	d.followRepo.EXPECT().ListFollowers(ctx, "artist-1").Return(followers, nil).Once()

	expectNotificationRequestedMatching(t, d.publisher, "user-away", entity.NotificationTypeNewConcerts, func(p *entity.NotificationPayload) bool {
		return payloadURL(p) == "/concerts/c-early" && p.Body == "2 new concerts found"
	})

	err := d.uc.NotifyNewConcerts(ctx, usecase.ConcertCreatedData{ArtistID: "artist-1", ConcertIDs: []string{"c-late", "c-early"}})
	assert.NoError(t, err)
}

// TestNotifyNewConcerts_FirstPartyDeepLinksToEventPage verifies that when the
// earliest matched concert belongs to an Organizer's Series, the notification
// deep-links to its public event page instead of the dashboard detail sheet.
func TestNotifyNewConcerts_FirstPartyDeepLinksToEventPage(t *testing.T) {
	// @spec components/usecase/notification/notify-new-concerts "First-party concert links to its event page"
	t.Parallel()
	ctx := context.Background()

	d := newPushNotificationTestDeps(t)

	tokyoArea := "JP-13"
	organizerID := "organizer-1"
	date := func(day int) time.Time { return time.Date(2026, 9, day, 0, 0, 0, 0, time.UTC) }
	concerts := []*entity.Concert{
		{ID: "discovered-late", LocalDate: date(10), Series: &entity.Series{ID: "s-discovered"}, Venue: &entity.Venue{AdminArea: &tokyoArea}, Performers: []*entity.Artist{{ID: "artist-1"}}},
		{ID: "first-party-early", LocalDate: date(3), Series: &entity.Series{ID: "s-first-party", OrganizerID: &organizerID}, Venue: &entity.Venue{AdminArea: &tokyoArea}, Performers: []*entity.Artist{{ID: "artist-1"}}},
	}
	artist := &entity.Artist{ID: "artist-1", Name: "Test Artist"}
	followers := []*entity.Follower{
		{ArtistID: "artist-1", User: &entity.User{ID: "user-away"}, Hype: entity.HypeAway},
	}

	d.artistRepo.EXPECT().Get(ctx, "artist-1").Return(artist, nil).Once()
	d.concertRepo.EXPECT().ListByIDs(ctx, []string{"discovered-late", "first-party-early"}).Return(concerts, nil).Once()
	d.followRepo.EXPECT().ListFollowers(ctx, "artist-1").Return(followers, nil).Once()

	expectNotificationRequestedMatching(t, d.publisher, "user-away", entity.NotificationTypeNewConcerts, func(p *entity.NotificationPayload) bool {
		return payloadURL(p) == "/events/first-party-early"
	})

	err := d.uc.NotifyNewConcerts(ctx, usecase.ConcertCreatedData{ArtistID: "artist-1", ConcertIDs: []string{"discovered-late", "first-party-early"}})
	assert.NoError(t, err)
}

// TestNotifyNewConcerts_HomeRecipientSubsetCountAndDeepLink verifies the spec's
// home-hype scenario: with 3 new concerts (1 in JP-13, 2 in JP-40), a home
// recipient in JP-13 sees a count of 1 and deep-links to the in-area concert —
// never the earlier, out-of-area JP-40 concert.
// @spec components/usecase/notification/notify-new-concerts "Home follower links to their in-area concert"
func TestNotifyNewConcerts_HomeRecipientSubsetCountAndDeepLink(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	d := newPushNotificationTestDeps(t)

	tokyoArea := "JP-13"
	aichiArea := "JP-40"
	date := func(day int) time.Time { return time.Date(2026, 9, day, 0, 0, 0, 0, time.UTC) }
	concerts := []*entity.Concert{
		{ID: "aichi-early", LocalDate: date(1), Venue: &entity.Venue{AdminArea: &aichiArea}, Performers: []*entity.Artist{{ID: "artist-1"}}},
		{ID: "tokyo-later", LocalDate: date(5), Venue: &entity.Venue{AdminArea: &tokyoArea}, Performers: []*entity.Artist{{ID: "artist-1"}}},
		{ID: "aichi-early2", LocalDate: date(2), Venue: &entity.Venue{AdminArea: &aichiArea}, Performers: []*entity.Artist{{ID: "artist-1"}}},
	}
	artist := &entity.Artist{ID: "artist-1", Name: "Test Artist"}
	followers := []*entity.Follower{
		{ArtistID: "artist-1", User: &entity.User{ID: "user-home-tokyo", Home: &entity.Home{Level1: "JP-13"}}, Hype: entity.HypeHome},
	}

	d.artistRepo.EXPECT().Get(ctx, "artist-1").Return(artist, nil).Once()
	d.concertRepo.EXPECT().ListByIDs(ctx, []string{"aichi-early", "tokyo-later", "aichi-early2"}).Return(concerts, nil).Once()
	d.followRepo.EXPECT().ListFollowers(ctx, "artist-1").Return(followers, nil).Once()

	expectNotificationRequestedMatching(t, d.publisher, "user-home-tokyo", entity.NotificationTypeNewConcerts, func(p *entity.NotificationPayload) bool {
		return payloadURL(p) == "/concerts/tokyo-later" && p.Body == "1 new concert found"
	})

	err := d.uc.NotifyNewConcerts(ctx, usecase.ConcertCreatedData{ArtistID: "artist-1", ConcertIDs: []string{"aichi-early", "tokyo-later", "aichi-early2"}})
	assert.NoError(t, err)
}

// TestNotifyNewConcerts_SeveralConcertsInEnglish verifies the English plural
// body counts every matched concert.
func TestNotifyNewConcerts_SeveralConcertsInEnglish(t *testing.T) {
	// @spec components/usecase/notification/notify-new-concerts "Several concerts in English"
	t.Parallel()
	ctx := context.Background()

	d := newPushNotificationTestDeps(t)

	tokyoArea := "JP-13"
	concerts := []*entity.Concert{
		{ID: "c1", Venue: &entity.Venue{AdminArea: &tokyoArea}, Performers: []*entity.Artist{{ID: "artist-1"}}},
		{ID: "c2", Venue: &entity.Venue{AdminArea: &tokyoArea}, Performers: []*entity.Artist{{ID: "artist-1"}}},
		{ID: "c3", Venue: &entity.Venue{AdminArea: &tokyoArea}, Performers: []*entity.Artist{{ID: "artist-1"}}},
	}
	artist := &entity.Artist{ID: "artist-1", Name: "Test Artist"}
	followers := []*entity.Follower{
		{ArtistID: "artist-1", User: &entity.User{ID: "user-en", PreferredLanguage: "en"}, Hype: entity.HypeAway},
	}

	d.artistRepo.EXPECT().Get(ctx, "artist-1").Return(artist, nil).Once()
	d.concertRepo.EXPECT().ListByIDs(ctx, []string{"c1", "c2", "c3"}).Return(concerts, nil).Once()
	d.followRepo.EXPECT().ListFollowers(ctx, "artist-1").Return(followers, nil).Once()

	expectNotificationRequestedMatching(t, d.publisher, "user-en", entity.NotificationTypeNewConcerts, func(p *entity.NotificationPayload) bool {
		return p.Body == "3 new concerts found"
	})

	err := d.uc.NotifyNewConcerts(ctx, usecase.ConcertCreatedData{ArtistID: "artist-1", ConcertIDs: []string{"c1", "c2", "c3"}})
	assert.NoError(t, err)
}

// TestNotifyNewConcerts_SameDayEarlierStart verifies two concerts on the same
// date link to the one that starts earlier.
func TestNotifyNewConcerts_SameDayEarlierStart(t *testing.T) {
	// @spec components/usecase/notification/notify-new-concerts "Same day, earlier start"
	t.Parallel()
	ctx := context.Background()

	d := newPushNotificationTestDeps(t)

	tokyoArea := "JP-13"
	day := time.Date(2026, 9, 3, 0, 0, 0, 0, time.UTC)
	at := func(h, m int) *time.Time {
		t := time.Date(2026, 9, 3, h-9, m, 0, 0, time.UTC) // JST wall time
		return &t
	}
	concerts := []*entity.Concert{
		{ID: "late", LocalDate: day, StartTime: at(19, 30), Venue: &entity.Venue{AdminArea: &tokyoArea}, Performers: []*entity.Artist{{ID: "artist-1"}}},
		{ID: "early", LocalDate: day, StartTime: at(18, 0), Venue: &entity.Venue{AdminArea: &tokyoArea}, Performers: []*entity.Artist{{ID: "artist-1"}}},
	}
	artist := &entity.Artist{ID: "artist-1", Name: "Test Artist"}
	followers := []*entity.Follower{
		{ArtistID: "artist-1", User: &entity.User{ID: "user-away"}, Hype: entity.HypeAway},
	}

	d.artistRepo.EXPECT().Get(ctx, "artist-1").Return(artist, nil).Once()
	d.concertRepo.EXPECT().ListByIDs(ctx, []string{"late", "early"}).Return(concerts, nil).Once()
	d.followRepo.EXPECT().ListFollowers(ctx, "artist-1").Return(followers, nil).Once()

	expectNotificationRequestedMatching(t, d.publisher, "user-away", entity.NotificationTypeNewConcerts, func(p *entity.NotificationPayload) bool {
		return payloadURL(p) == "/concerts/early"
	})

	err := d.uc.NotifyNewConcerts(ctx, usecase.ConcertCreatedData{ArtistID: "artist-1", ConcertIDs: []string{"late", "early"}})
	assert.NoError(t, err)
}
