package usecase_test

import (
	"context"
	"slices"
	"strings"
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

// pushNotificationTestDeps holds all dependencies for PushSubscriptionUseCase tests.
type pushNotificationTestDeps struct {
	artistRepo  *mocks.MockArtistRepository
	concertRepo *mocks.MockConcertRepository
	followRepo  *mocks.MockFollowRepository
	pushSubRepo *mocks.MockPushSubscriptionRepository
	publisher   *ucmocks.MockWorkflows
	uc          usecase.PushSubscriptionUseCase
	notify      usecase.NotificationUseCase
}

func newPushNotificationTestDeps(t *testing.T) *pushNotificationTestDeps {
	t.Helper()
	d := &pushNotificationTestDeps{
		artistRepo:  mocks.NewMockArtistRepository(t),
		concertRepo: mocks.NewMockConcertRepository(t),
		followRepo:  mocks.NewMockFollowRepository(t),
		pushSubRepo: mocks.NewMockPushSubscriptionRepository(t),
		publisher:   ucmocks.NewMockWorkflows(t),
	}
	d.uc = usecase.NewPushSubscriptionUseCase(d.pushSubRepo, newTestLogger(t))
	d.notify = newNotifyNewConcertsUC(t, d, newTestLogger(t))
	return d
}

// expectNotificationRequested sets up a Start expectation matching a
// deliver-notification workflow for the given recipient and
// notification type — the request NotifyNewConcerts / AnnounceDiscoveredSale
// now issue instead of calling NotificationUseCase.Deliver directly.
func expectNotificationRequested(t *testing.T, publisher *ucmocks.MockWorkflows, userID string, typ entity.NotificationType) *mock.Call {
	t.Helper()
	return publisher.EXPECT().
		Start(anyCtx, usecase.WorkflowDeliverNotification, mock.Anything, mock.MatchedBy(func(data usecase.DeliverRequest) bool {
			return data.UserID == userID && data.Type == typ &&
				data.Subject.ArtistID == "artist-1" && slices.Equal(data.Subject.EventIDs, []string{"c1"})
		})).
		Return(nil).
		Once()
}

func TestPushSubscriptionUseCase_Create(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("fan enables push on a new browser", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/push-subscription/create "Fan enables push on a new browser"
		d := newPushNotificationTestDeps(t)
		d.pushSubRepo.EXPECT().Create(ctx, mock.MatchedBy(func(s *entity.PushSubscription) bool {
			return s.UserID == "user-1" && s.Endpoint == "https://push/1" && s.P256dh == "k" && s.Auth == "a"
		})).RunAndReturn(func(_ context.Context, s *entity.PushSubscription) error {
			s.ID = "sub-new"
			return nil
		}).Once()

		got, err := d.uc.Create(ctx, "user-1", "https://push/1", "k", "a")

		require.NoError(t, err)
		assert.Equal(t, "sub-new", got.ID)
	})

	t.Run("fan re-registers the same browser", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/push-subscription/create "Fan re-registers the same browser"
		d := newPushNotificationTestDeps(t)
		d.pushSubRepo.EXPECT().Create(ctx, mock.Anything).RunAndReturn(func(_ context.Context, s *entity.PushSubscription) error {
			s.ID = "sub-existing"
			return nil
		}).Once()

		got, err := d.uc.Create(ctx, "user-1", "https://push/1", "k2", "a2")

		require.NoError(t, err)
		assert.Equal(t, "sub-existing", got.ID)
	})

	t.Run("storing fails", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/push-subscription/create "Storing fails"
		d := newPushNotificationTestDeps(t)
		d.pushSubRepo.EXPECT().Create(ctx, mock.Anything).Return(apperr.ErrUnavailable).Once()

		_, err := d.uc.Create(ctx, "user-1", "https://push/1", "k", "a")

		assert.ErrorIs(t, err, apperr.ErrUnavailable)
	})
}

func TestPushSubscriptionUseCase_Get(t *testing.T) {
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

func TestPushSubscriptionUseCase_Delete(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("fan disables push on one browser", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/push-subscription/delete "Fan disables push on one browser"
		d := newPushNotificationTestDeps(t)
		// Only the named browser is removed; the repository keeps the other.
		d.pushSubRepo.EXPECT().Delete(ctx, "user-1", "https://push/1").Return(nil).Once()

		assert.NoError(t, d.uc.Delete(ctx, "user-1", "https://push/1"))
	})

	t.Run("browser not registered", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/push-subscription/delete "Browser not registered"
		d := newPushNotificationTestDeps(t)
		d.pushSubRepo.EXPECT().Delete(ctx, "user-1", "https://push/unknown").Return(nil).Once()

		assert.NoError(t, d.uc.Delete(ctx, "user-1", "https://push/unknown"))
	})

	t.Run("removal fails", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/push-subscription/delete "Removal fails"
		d := newPushNotificationTestDeps(t)
		d.pushSubRepo.EXPECT().Delete(ctx, "user-1", "https://push/1").Return(apperr.ErrUnavailable).Once()

		assert.ErrorIs(t, d.uc.Delete(ctx, "user-1", "https://push/1"), apperr.ErrUnavailable)
	})
}

func TestPushSubscriptionUseCase_NotifyNewConcerts(t *testing.T) {
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
				Artists: []*entity.Artist{{ID: "artist-1"}},
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
			// @spec components/usecase/notification/notify-new-concerts "Nobody follows the artist"
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
			// @spec components/usecase/notification/notify-new-concerts "Watch follower"
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
			// @spec components/usecase/notification/notify-new-concerts "Home follower and a new concert elsewhere"
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
						Artists: []*entity.Artist{{ID: "artist-1"}},
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
						Artists: []*entity.Artist{{ID: "artist-1"}},
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
			// @spec components/usecase/notification/notify-new-concerts "Nearby follower and a new concert in range"
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
						Artists: []*entity.Artist{{ID: "artist-1"}},
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
			// @spec components/usecase/notification/notify-new-concerts "Nearby follower and a distant new concert"
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
						Artists: []*entity.Artist{{ID: "artist-1"}},
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
			// @spec components/usecase/notification/notify-new-concerts "Unknown concert"
			name: "error - InvalidArgument when concert_id not found in repo",
			args: args{data: usecase.ConcertCreatedData{ArtistID: "artist-1", ConcertIDs: []string{"c1", "c2"}}},
			setup: func(t *testing.T, d *pushNotificationTestDeps) {
				t.Helper()
				d.artistRepo.EXPECT().Get(ctx, "artist-1").Return(artist, nil).Once()
				// ListByIDs returns only c1 — c2 is missing from the result.
				d.concertRepo.EXPECT().ListByIDs(ctx, []string{"c1", "c2"}).Return([]*entity.Concert{
					{ID: "c1", Artists: []*entity.Artist{{ID: "artist-1"}}},
				}, nil).Once()
			},
			wantErr: apperr.ErrInvalidArgument,
		},
		{
			// @spec components/usecase/notification/notify-new-concerts "Concert of another artist"
			name: "error - InvalidArgument when concert belongs to different artist",
			args: args{data: usecase.ConcertCreatedData{ArtistID: "artist-1", ConcertIDs: []string{"c1"}}},
			setup: func(t *testing.T, d *pushNotificationTestDeps) {
				t.Helper()
				d.artistRepo.EXPECT().Get(ctx, "artist-1").Return(artist, nil).Once()
				// c1 exists but is performed by a different artist.
				d.concertRepo.EXPECT().ListByIDs(ctx, []string{"c1"}).Return([]*entity.Concert{
					{ID: "c1", Artists: []*entity.Artist{{ID: "artist-999"}}},
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
					Start(anyCtx, usecase.WorkflowDeliverNotification, mock.Anything, mock.MatchedBy(func(data usecase.DeliverRequest) bool { return data.UserID == "user-2" })).
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

			err := d.notify.NotifyNewConcerts(ctx, tt.args.data)

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				return
			}
			assert.NoError(t, err)
		})
	}
}

// newConcertsArtist is the artist of the NotifyNewConcerts message tests.
var newConcertsArtist = &entity.Artist{ID: "artist-1", Name: "Vaundy"}

// newConcertFor is a concert of newConcertsArtist in the area on the date.
func newConcertFor(id, area string, date time.Time) *entity.Concert {
	return &entity.Concert{
		ID: id, LocalDate: date,
		Venue:   &entity.Venue{Name: "Venue " + id, AdminArea: &area},
		Series:  &entity.Series{ID: "series-" + id, Title: "Tour"},
		Artists: []*entity.Artist{newConcertsArtist},
	}
}

// awayFollower is a follower with the language who matches every concert.
func awayFollower(id, language string) *entity.Follower {
	return &entity.Follower{ArtistID: "artist-1", User: renderedFan(id, language), Hype: entity.HypeAway}
}

// TestNotifyNewConcerts_LocalizesBodyPerRecipient runs the real rendering of
// one matched concert: each follower's message follows their language, and a
// language without copy is English.
//
// @spec components/usecase/notification/notify-new-concerts "One concert in English"
// @spec components/usecase/notification/notify-new-concerts "Unsupported or missing language"
func TestNotifyNewConcerts_LocalizesBodyPerRecipient(t *testing.T) {
	t.Parallel()

	concerts := []*entity.Concert{newConcertFor("c1", "JP-13", time.Date(2026, 9, 3, 0, 0, 0, 0, time.UTC))}
	messages := newConcertMessages(t, newConcertsArtist, concerts,
		[]*entity.Follower{awayFollower("user-ja", "ja"), awayFollower("user-en", "en"), awayFollower("user-fr", "fr")},
		usecase.ConcertCreatedData{ArtistID: "artist-1", ConcertIDs: []string{"c1"}})

	require.Len(t, messages, 3)
	assert.Equal(t, "新しいライブが1件見つかりました\nTour", messages["user-ja"].Body)
	assert.Equal(t, "1 new concert found\nTour", messages["user-en"].Body)
	assert.Equal(t, "1 new concert found\nTour", messages["user-fr"].Body)
	assert.Equal(t, "[Vaundy] New Concerts", messages["user-fr"].Title)
}

// TestNotifyNewConcerts_Japanese verifies the Japanese title and the count of
// three matched concerts.
//
// @spec components/usecase/notification/notify-new-concerts "Japanese"
func TestNotifyNewConcerts_Japanese(t *testing.T) {
	t.Parallel()

	date := time.Date(2026, 9, 3, 0, 0, 0, 0, time.UTC)
	concerts := []*entity.Concert{newConcertFor("c1", "JP-13", date), newConcertFor("c2", "JP-13", date), newConcertFor("c3", "JP-13", date)}
	messages := newConcertMessages(t, newConcertsArtist, concerts, []*entity.Follower{awayFollower("user-ja", "ja")},
		usecase.ConcertCreatedData{ArtistID: "artist-1", ConcertIDs: []string{"c1", "c2", "c3"}})

	require.Contains(t, messages, "user-ja")
	assert.Equal(t, "[Vaundy] 新着ライブ発表!!", messages["user-ja"].Title)
	assert.True(t, strings.HasPrefix(messages["user-ja"].Body, "新しいライブが3件見つかりました"), messages["user-ja"].Body)
}

// newPushNotificationUCWithLogger builds the use case with a capture logger so a
// test can assert on the zero-recipient early-return logs.
func newPushNotificationUCWithLogger(t *testing.T, d *pushNotificationTestDeps, logger *logging.Logger) usecase.NotificationUseCase {
	t.Helper()
	return newNotifyNewConcertsUC(t, d, logger)
}

// newNotifyNewConcertsUC builds the NotificationUseCase that NotifyNewConcerts
// runs on, over the fixture's repositories and publisher.
func newNotifyNewConcertsUC(t *testing.T, d *pushNotificationTestDeps, logger *logging.Logger) usecase.NotificationUseCase {
	t.Helper()
	return usecase.NewNotificationUseCase(nil, d.pushSubRepo, nil, d.publisher, noopMetrics{}, logger,
		usecase.OrderConfirmationDeps{Now: time.Now},
		usecase.NewConcertsDeps{ArtistRepo: d.artistRepo, ConcertRepo: d.concertRepo, FollowRepo: d.followRepo}, usecase.SubjectDeps{Now: time.Now})
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
		{ID: "c1", Venue: &entity.Venue{AdminArea: &tokyoArea}, Artists: []*entity.Artist{{ID: "artist-1"}}},
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
// recipient's message links to the earliest concert of the batch, regardless of
// the order the concerts arrive in.
//
// @spec components/usecase/notification/notify-new-concerts "Link to the earliest matched concert"
func TestNotifyNewConcerts_DeepLinksToEarliestMatched(t *testing.T) {
	t.Parallel()

	date := func(day int) time.Time { return time.Date(2026, 9, day, 0, 0, 0, 0, time.UTC) }
	// The later concert is listed first to prove ordering is by date, not slice order.
	concerts := []*entity.Concert{newConcertFor("c-late", "JP-13", date(10)), newConcertFor("c-early", "JP-13", date(3))}
	messages := newConcertMessages(t, newConcertsArtist, concerts, []*entity.Follower{awayFollower("user-away", "en")},
		usecase.ConcertCreatedData{ArtistID: "artist-1", ConcertIDs: []string{"c-late", "c-early"}})

	require.Contains(t, messages, "user-away")
	assert.Equal(t, "/concerts/c-early", payloadURL(messages["user-away"]))
	assert.Equal(t, "2 new concerts found\nTour", messages["user-away"].Body)
}

// TestNotifyNewConcerts_FirstPartyDeepLinksToEventPage verifies that when the
// earliest matched concert belongs to an Organizer's Series, the message links
// to its public event page instead of the dashboard detail sheet.
//
// @spec components/usecase/notification/notify-new-concerts "First-party concert links to its event page"
func TestNotifyNewConcerts_FirstPartyDeepLinksToEventPage(t *testing.T) {
	t.Parallel()

	date := func(day int) time.Time { return time.Date(2026, 9, day, 0, 0, 0, 0, time.UTC) }
	firstParty := newConcertFor("first-party-early", "JP-13", date(3))
	firstParty.Series.OrganizerID = new("organizer-1")
	concerts := []*entity.Concert{newConcertFor("discovered-late", "JP-13", date(10)), firstParty}
	messages := newConcertMessages(t, newConcertsArtist, concerts, []*entity.Follower{awayFollower("user-away", "en")},
		usecase.ConcertCreatedData{ArtistID: "artist-1", ConcertIDs: []string{"discovered-late", "first-party-early"}})

	require.Contains(t, messages, "user-away")
	assert.Equal(t, "/events/first-party-early", payloadURL(messages["user-away"]))
}

// TestNotifyNewConcerts_HomeRecipientSubsetCountAndDeepLink verifies the spec's
// home-hype scenario: with concerts in JP-40 and JP-13, a home recipient in JP-13
// sees a count of 1 and links to the in-area concert, never the earlier,
// out-of-area JP-40 concerts. The subject names only the matched concert.
//
// @spec components/usecase/notification/notify-new-concerts "Home follower links to their in-area concert"
// @spec components/usecase/notification/notify-new-concerts "Home follower counts only in-area concerts"
func TestNotifyNewConcerts_HomeRecipientSubsetCountAndDeepLink(t *testing.T) {
	t.Parallel()

	date := func(day int) time.Time { return time.Date(2026, 9, day, 0, 0, 0, 0, time.UTC) }
	concerts := []*entity.Concert{
		newConcertFor("aichi-early", "JP-40", date(1)),
		newConcertFor("tokyo-later", "JP-13", date(5)),
		newConcertFor("aichi-early2", "JP-40", date(2)),
	}
	home := renderedFan("user-home-tokyo", "en")
	home.Home = &entity.Home{Level1: "JP-13"}
	messages := newConcertMessages(t, newConcertsArtist, concerts,
		[]*entity.Follower{{ArtistID: "artist-1", User: home, Hype: entity.HypeHome}},
		usecase.ConcertCreatedData{ArtistID: "artist-1", ConcertIDs: []string{"aichi-early", "tokyo-later", "aichi-early2"}})

	require.Contains(t, messages, "user-home-tokyo")
	assert.Equal(t, "/concerts/tokyo-later", payloadURL(messages["user-home-tokyo"]))
	assert.Equal(t, "1 new concert found\nTour", messages["user-home-tokyo"].Body)
}

// TestNotifyNewConcerts_SeveralConcertsInEnglish verifies the English plural
// body counts every matched concert.
//
// @spec components/usecase/notification/notify-new-concerts "Several concerts in English"
func TestNotifyNewConcerts_SeveralConcertsInEnglish(t *testing.T) {
	t.Parallel()

	date := time.Date(2026, 9, 3, 0, 0, 0, 0, time.UTC)
	concerts := []*entity.Concert{newConcertFor("c1", "JP-13", date), newConcertFor("c2", "JP-13", date), newConcertFor("c3", "JP-13", date)}
	messages := newConcertMessages(t, newConcertsArtist, concerts, []*entity.Follower{awayFollower("user-en", "en")},
		usecase.ConcertCreatedData{ArtistID: "artist-1", ConcertIDs: []string{"c1", "c2", "c3"}})

	require.Contains(t, messages, "user-en")
	assert.True(t, strings.HasPrefix(messages["user-en"].Body, "3 new concerts found"), messages["user-en"].Body)
}

// TestNotifyNewConcerts_SameDayEarlierStart verifies two concerts on the same
// date link to the one that starts earlier.
//
// @spec components/usecase/notification/notify-new-concerts "Same day, earlier start"
func TestNotifyNewConcerts_SameDayEarlierStart(t *testing.T) {
	t.Parallel()

	day := time.Date(2026, 9, 3, 0, 0, 0, 0, time.UTC)
	at := func(h, m int) *time.Time {
		start := time.Date(2026, 9, 3, h-9, m, 0, 0, time.UTC) // JST wall time
		return &start
	}
	// The IDs sort opposite to the start times, so the ID tie-break in
	// concertEarlier cannot pick the earlier concert by itself.
	late, early := newConcertFor("a-late", "JP-13", day), newConcertFor("z-early", "JP-13", day)
	late.StartTime, early.StartTime = at(19, 30), at(18, 0)
	messages := newConcertMessages(t, newConcertsArtist, []*entity.Concert{late, early}, []*entity.Follower{awayFollower("user-away", "en")},
		usecase.ConcertCreatedData{ArtistID: "artist-1", ConcertIDs: []string{"a-late", "z-early"}})

	require.Contains(t, messages, "user-away")
	assert.Equal(t, "/concerts/z-early", payloadURL(messages["user-away"]))
}

// TestNotifyNewConcerts_RequestFailsForOneFollower runs the announcement again
// after the second of three starts fails: the third was not started, and the
// first keeps one workflow because its name repeats.
//
// @spec components/usecase/notification/notify-new-concerts "Request fails for one follower"
func TestNotifyNewConcerts_RequestFailsForOneFollower(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	d := newPushNotificationTestDeps(t)
	d.artistRepo.EXPECT().Get(ctx, "artist-1").Return(newConcertsArtist, nil).Twice()
	concerts := []*entity.Concert{newConcertFor("c1", "JP-13", time.Date(2026, 9, 3, 0, 0, 0, 0, time.UTC))}
	d.concertRepo.EXPECT().ListByIDs(ctx, []string{"c1"}).Return(concerts, nil).Twice()
	d.followRepo.EXPECT().ListFollowers(ctx, "artist-1").Return([]*entity.Follower{
		awayFollower("user-1", "en"), awayFollower("user-2", "en"), awayFollower("user-3", "en"),
	}, nil).Twice()
	started := map[string]bool{}
	failed := false
	publishErr := apperr.ErrInternal
	d.publisher.EXPECT().Start(anyCtx, usecase.WorkflowDeliverNotification, mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, _, id string, _ any, _ ...usecase.StartOption) error {
			if id == "deliver:new_concerts:user-2:announcement-1" && !failed {
				failed = true
				return publishErr
			}
			started[id] = true
			return nil
		})
	data := usecase.ConcertCreatedData{ArtistID: "artist-1", ConcertIDs: []string{"c1"}, RequestID: "announcement-1"}

	require.ErrorIs(t, d.notify.NotifyNewConcerts(ctx, data), publishErr)
	assert.Equal(t, map[string]bool{"deliver:new_concerts:user-1:announcement-1": true}, started, "the third is not started")

	require.NoError(t, d.notify.NotifyNewConcerts(ctx, data))
	assert.Len(t, started, 3, "one name per follower, so the first is not notified again")
}

// TestNotifyNewConcerts_ConcertWithoutPerformers verifies that a concert without
// performers is left out, of the subject and so of the message's count.
//
// @spec components/usecase/notification/notify-new-concerts "Concert without performers"
func TestNotifyNewConcerts_ConcertWithoutPerformers(t *testing.T) {
	t.Parallel()

	date := time.Date(2026, 9, 3, 0, 0, 0, 0, time.UTC)
	orphan := newConcertFor("c3", "JP-13", date)
	orphan.Artists = nil
	concerts := []*entity.Concert{newConcertFor("c1", "JP-13", date), newConcertFor("c2", "JP-13", date), orphan}
	messages := newConcertMessages(t, newConcertsArtist, concerts, []*entity.Follower{awayFollower("user-en", "en")},
		usecase.ConcertCreatedData{ArtistID: "artist-1", ConcertIDs: []string{"c1", "c2", "c3"}})

	require.Contains(t, messages, "user-en")
	assert.Equal(t, "2 new concerts found\nTour", messages["user-en"].Body)
}
