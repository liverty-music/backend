package rdb_test

import (
	"context"
	"testing"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFollowRepository_ListByUser(t *testing.T) {
	followRepo := rdb.NewFollowRepository(testDB)
	artistRepo := rdb.NewArtistRepository(testDB)
	ctx := context.Background()

	tests := []struct {
		name    string
		setup   func() string // returns userID
		want    []*entity.FollowedArtist
		wantErr error
	}{
		{
			name: "includes fanart when artist has fanart data",
			setup: func() string {
				cleanDatabase()
				created, err := artistRepo.Create(ctx, entity.NewArtist("Logo Artist", "f1000000-0000-0000-0000-00000000fa01"))
				require.NoError(t, err)
				artistID := created[0].ID

				userID := seedUser(t, "Test User 1", "fanart-test-1@example.com", "ext-fanart-01")

				fanart := &entity.Fanart{
					ArtistThumb: []entity.FanartImage{
						{ID: "100", URL: "https://assets.fanart.tv/thumb.jpg", Likes: 5, Lang: "en"},
					},
					HDMusicLogo: []entity.FanartImage{
						{ID: "200", URL: "https://assets.fanart.tv/logo.png", Likes: 10, Lang: "ja"},
					},
				}
				err = artistRepo.UpdateFanart(ctx, artistID, fanart, time.Date(2026, 3, 16, 10, 0, 0, 0, time.UTC))
				require.NoError(t, err)

				err = followRepo.Follow(ctx, userID, artistID)
				require.NoError(t, err)
				err = followRepo.SetHype(ctx, userID, artistID, entity.HypeAway)
				require.NoError(t, err)

				return userID
			},
			want: []*entity.FollowedArtist{
				{
					Artist: &entity.Artist{
						Name: "Logo Artist",
						MBID: "f1000000-0000-0000-0000-00000000fa01",
						Fanart: &entity.Fanart{
							ArtistThumb: []entity.FanartImage{
								{ID: "100", URL: "https://assets.fanart.tv/thumb.jpg", Likes: 5, Lang: "en"},
							},
							HDMusicLogo: []entity.FanartImage{
								{ID: "200", URL: "https://assets.fanart.tv/logo.png", Likes: 10, Lang: "ja"},
							},
						},
					},
					Hype: entity.HypeAway,
				},
			},
		},
		{
			name: "no followed artists returns empty slice",
			setup: func() string {
				cleanDatabase()
				return seedUser(t, "Lonely User", "lonely@example.com", "ext-lonely-01")
			},
			want: nil,
		},
		{
			name: "multiple artists with mixed fanart presence",
			setup: func() string {
				cleanDatabase()

				created, err := artistRepo.Create(ctx,
					entity.NewArtist("With Fanart", "f3000000-0000-0000-0000-00000000fa03"),
					entity.NewArtist("Without Fanart", "f4000000-0000-0000-0000-00000000fa04"),
				)
				require.NoError(t, err)

				fanart := &entity.Fanart{
					ArtistThumb: []entity.FanartImage{
						{ID: "300", URL: "https://assets.fanart.tv/mixed-thumb.jpg", Likes: 3, Lang: "en"},
					},
				}
				err = artistRepo.UpdateFanart(ctx, created[0].ID, fanart, time.Date(2026, 3, 16, 10, 0, 0, 0, time.UTC))
				require.NoError(t, err)

				userID := seedUser(t, "Mixed User", "mixed@example.com", "ext-mixed-01")

				err = followRepo.Follow(ctx, userID, created[0].ID)
				require.NoError(t, err)
				err = followRepo.Follow(ctx, userID, created[1].ID)
				require.NoError(t, err)

				return userID
			},
			want: []*entity.FollowedArtist{
				{
					Artist: &entity.Artist{
						Name: "With Fanart",
						MBID: "f3000000-0000-0000-0000-00000000fa03",
						Fanart: &entity.Fanart{
							ArtistThumb: []entity.FanartImage{
								{ID: "300", URL: "https://assets.fanart.tv/mixed-thumb.jpg", Likes: 3, Lang: "en"},
							},
						},
					},
					Hype: entity.HypeWatch,
				},
				{
					Artist: &entity.Artist{
						Name: "Without Fanart",
						MBID: "f4000000-0000-0000-0000-00000000fa04",
					},
					Hype: entity.HypeWatch,
				},
			},
		},
		{
			name: "returns nil fanart when artist has no fanart data",
			setup: func() string {
				cleanDatabase()
				created, err := artistRepo.Create(ctx, entity.NewArtist("Plain Artist", "f2000000-0000-0000-0000-00000000fa02"))
				require.NoError(t, err)
				artistID := created[0].ID

				userID := seedUser(t, "Test User 2", "fanart-test-2@example.com", "ext-fanart-02")

				err = followRepo.Follow(ctx, userID, artistID)
				require.NoError(t, err)

				return userID
			},
			want: []*entity.FollowedArtist{
				{
					Artist: &entity.Artist{
						Name: "Plain Artist",
						MBID: "f2000000-0000-0000-0000-00000000fa02",
					},
					Hype: entity.HypeWatch,
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			userID := tt.setup()

			got, err := followRepo.ListByUser(ctx, userID)

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				return
			}

			require.NoError(t, err)
			require.Len(t, got, len(tt.want))

			// Build lookup by MBID for order-independent comparison.
			gotByMBID := make(map[string]*entity.FollowedArtist, len(got))
			for _, g := range got {
				assert.NotEmpty(t, g.Artist.ID)
				gotByMBID[g.Artist.MBID] = g
			}
			for _, w := range tt.want {
				g, ok := gotByMBID[w.Artist.MBID]
				require.True(t, ok, "expected artist MBID %s not found", w.Artist.MBID)
				w.UserID = userID
				w.Artist.ID = g.Artist.ID
				assert.Equal(t, w, g)
			}
		})
	}
}

func TestFollowRepository_Follow(t *testing.T) {
	followRepo := rdb.NewFollowRepository(testDB)
	ctx := context.Background()

	tests := []struct {
		name    string
		setup   func() (userID, artistID string)
		wantErr error
	}{
		{
			name: "follow succeeds",
			setup: func() (string, string) {
				cleanDatabase()
				userID := seedUser(t, "Follow User", "follow@test.com", "ext-follow-01")
				artistID := seedArtist(t, "Follow Artist", "a1000000-0000-0000-0000-000000000001")
				return userID, artistID
			},
		},
		{
			name: "duplicate follow is idempotent",
			setup: func() (string, string) {
				cleanDatabase()
				userID := seedUser(t, "Dup Follow User", "dupfollow@test.com", "ext-dupfollow-01")
				artistID := seedArtist(t, "Dup Follow Artist", "a1000000-0000-0000-0000-000000000002")
				err := followRepo.Follow(ctx, userID, artistID)
				require.NoError(t, err)
				return userID, artistID
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			userID, artistID := tt.setup()

			err := followRepo.Follow(ctx, userID, artistID)

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				return
			}

			assert.NoError(t, err)
		})
	}
}

func TestFollowRepository_Unfollow(t *testing.T) {
	followRepo := rdb.NewFollowRepository(testDB)
	ctx := context.Background()

	tests := []struct {
		name    string
		setup   func() (userID, artistID string)
		wantErr error
	}{
		{
			name: "unfollow existing relationship",
			setup: func() (string, string) {
				cleanDatabase()
				userID := seedUser(t, "Unfollow User", "unfollow@test.com", "ext-unfollow-01")
				artistID := seedArtist(t, "Unfollow Artist", "a2000000-0000-0000-0000-000000000001")
				err := followRepo.Follow(ctx, userID, artistID)
				require.NoError(t, err)
				return userID, artistID
			},
		},
		{
			name: "unfollow non-existent relationship",
			setup: func() (string, string) {
				cleanDatabase()
				userID := seedUser(t, "Ghost Unfollow User", "ghost-unfollow@test.com", "ext-ghost-unfollow-01")
				artistID := seedArtist(t, "Ghost Artist", "a2000000-0000-0000-0000-000000000002")
				return userID, artistID
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			userID, artistID := tt.setup()

			err := followRepo.Unfollow(ctx, userID, artistID)

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				return
			}

			assert.NoError(t, err)
		})
	}
}

func TestFollowRepository_SetHype(t *testing.T) {
	followRepo := rdb.NewFollowRepository(testDB)
	ctx := context.Background()

	tests := []struct {
		name    string
		setup   func() (userID, artistID string)
		hype    entity.Hype
		wantErr error
	}{
		{
			name: "set hype on existing follow",
			setup: func() (string, string) {
				cleanDatabase()
				userID := seedUser(t, "Hype User", "hype@test.com", "ext-hype-01")
				artistID := seedArtist(t, "Hype Artist", "a3000000-0000-0000-0000-000000000001")
				err := followRepo.Follow(ctx, userID, artistID)
				require.NoError(t, err)
				return userID, artistID
			},
			hype: entity.HypeAway,
		},
		{
			name: "set hype on non-existent follow returns NotFound",
			setup: func() (string, string) {
				cleanDatabase()
				userID := seedUser(t, "No Follow Hype User", "nofollow-hype@test.com", "ext-nofollow-hype-01")
				artistID := seedArtist(t, "No Follow Hype Artist", "a3000000-0000-0000-0000-000000000002")
				return userID, artistID
			},
			hype:    entity.HypeAway,
			wantErr: apperr.ErrNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			userID, artistID := tt.setup()

			err := followRepo.SetHype(ctx, userID, artistID, tt.hype)

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				return
			}

			require.NoError(t, err)

			// Verify hype was updated via ListByUser.
			followed, err := followRepo.ListByUser(ctx, userID)
			require.NoError(t, err)
			require.Len(t, followed, 1)
			assert.Equal(t, tt.hype, followed[0].Hype)
			assert.Equal(t, artistID, followed[0].Artist.ID)
		})
	}
}

func TestFollowRepository_ListAll(t *testing.T) {
	followRepo := rdb.NewFollowRepository(testDB)
	ctx := context.Background()

	tests := []struct {
		name      string
		setup     func()
		wantCount int
		wantErr   error
	}{
		{
			name: "empty when no follows",
			setup: func() {
				cleanDatabase()
			},
			wantCount: 0,
		},
		{
			name: "returns distinct artists",
			setup: func() {
				cleanDatabase()
				artistID1 := seedArtist(t, "ListAll Artist 1", "a4000000-0000-0000-0000-000000000001")
				artistID2 := seedArtist(t, "ListAll Artist 2", "a4000000-0000-0000-0000-000000000002")
				user1ID := seedUser(t, "ListAll User 1", "listall-user1@test.com", "ext-listall-01")
				user2ID := seedUser(t, "ListAll User 2", "listall-user2@test.com", "ext-listall-02")

				// User1 follows both artists.
				err := followRepo.Follow(ctx, user1ID, artistID1)
				require.NoError(t, err)
				err = followRepo.Follow(ctx, user1ID, artistID2)
				require.NoError(t, err)

				// User2 follows artist1 only — should not duplicate artist1 in results.
				err = followRepo.Follow(ctx, user2ID, artistID1)
				require.NoError(t, err)
			},
			wantCount: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.setup()

			got, err := followRepo.ListAll(ctx)

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				return
			}

			require.NoError(t, err)
			assert.Len(t, got, tt.wantCount)
		})
	}
}

func TestFollowRepository_ListFollowers(t *testing.T) {
	followRepo := rdb.NewFollowRepository(testDB)
	ctx := context.Background()

	tests := []struct {
		name    string
		setup   func() string // returns artistID
		check   func(t *testing.T, got []*entity.Follower)
		wantErr error
	}{
		{
			name: "empty when no followers",
			setup: func() string {
				cleanDatabase()
				return seedArtist(t, "No Followers Artist", "a5000000-0000-0000-0000-000000000001")
			},
			check: func(t *testing.T, got []*entity.Follower) {
				t.Helper()
				assert.Empty(t, got)
			},
		},
		{
			name: "returns followers with hype and home",
			setup: func() string {
				cleanDatabase()
				artistID := seedArtist(t, "Followers Artist", "a5000000-0000-0000-0000-000000000002")

				// User without home area.
				noHomeUserID := seedUser(t, "No Home User", "nohome@test.com", "ext-nohome-01")
				err := followRepo.Follow(ctx, noHomeUserID, artistID)
				require.NoError(t, err)
				err = followRepo.SetHype(ctx, noHomeUserID, artistID, entity.HypeAway)
				require.NoError(t, err)

				// User with home area.
				homeUserID := seedUser(t, "Home User", "home@test.com", "ext-home-01")
				homeID := seedHome(t, "JP", "JP-13")
				_, err = testDB.Pool.Exec(ctx,
					`UPDATE users SET home_id = $1 WHERE id = $2`,
					homeID, homeUserID,
				)
				require.NoError(t, err)
				err = followRepo.Follow(ctx, homeUserID, artistID)
				require.NoError(t, err)
				err = followRepo.SetHype(ctx, homeUserID, artistID, entity.HypeHome)
				require.NoError(t, err)

				return artistID
			},
			check: func(t *testing.T, got []*entity.Follower) {
				t.Helper()
				require.Len(t, got, 2)

				// Build lookup by user ID for order-independent assertions.
				byUserID := make(map[string]*entity.Follower, len(got))
				for _, f := range got {
					assert.NotEmpty(t, f.User.ID)
					byUserID[f.User.ID] = f
				}

				// Find the home user by checking which follower has a non-nil Home.
				var homeFollower, noHomeFollower *entity.Follower
				for _, f := range byUserID {
					if f.User.Home != nil {
						homeFollower = f
					} else {
						noHomeFollower = f
					}
				}

				require.NotNil(t, homeFollower, "expected a follower with home area")
				assert.Equal(t, entity.HypeHome, homeFollower.Hype)
				assert.Equal(t, "JP-13", homeFollower.User.Home.Level1)

				require.NotNil(t, noHomeFollower, "expected a follower without home area")
				assert.Equal(t, entity.HypeAway, noHomeFollower.Hype)
				assert.Nil(t, noHomeFollower.User.Home)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			artistID := tt.setup()

			got, err := followRepo.ListFollowers(ctx, artistID)

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				return
			}

			require.NoError(t, err)
			tt.check(t, got)
		})
	}
}
