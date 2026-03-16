package rdb_test

import (
	"context"
	"testing"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFollowRepository_ListByUser(t *testing.T) {
	followRepo := rdb.NewFollowRepository(testDB)
	artistRepo := rdb.NewArtistRepository(testDB)
	ctx := context.Background()

	type args struct {
		userID string
	}

	tests := []struct {
		name    string
		setup   func() string // returns userID
		args    args
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

				userID := "019cf800-0000-7000-8000-0000000fa001"
				_, err = testDB.Pool.Exec(ctx,
					"INSERT INTO users (id, name, email, external_id) VALUES ($1, $2, $3, $4)",
					userID, "Test User 1", "fanart-test-1@example.com", "ext-fanart-01")
				require.NoError(t, err)

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
					UserID: "019cf800-0000-7000-8000-0000000fa001",
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
				userID := "019cf800-0000-7000-8000-0000000fa003"
				_, err := testDB.Pool.Exec(ctx,
					"INSERT INTO users (id, name, email, external_id) VALUES ($1, $2, $3, $4)",
					userID, "Lonely User", "lonely@example.com", "ext-lonely-01")
				require.NoError(t, err)
				return userID
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

				userID := "019cf800-0000-7000-8000-0000000fa004"
				_, err = testDB.Pool.Exec(ctx,
					"INSERT INTO users (id, name, email, external_id) VALUES ($1, $2, $3, $4)",
					userID, "Mixed User", "mixed@example.com", "ext-mixed-01")
				require.NoError(t, err)

				err = followRepo.Follow(ctx, userID, created[0].ID)
				require.NoError(t, err)
				err = followRepo.Follow(ctx, userID, created[1].ID)
				require.NoError(t, err)

				return userID
			},
			want: []*entity.FollowedArtist{
				{
					UserID: "019cf800-0000-7000-8000-0000000fa004",
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
					UserID: "019cf800-0000-7000-8000-0000000fa004",
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

				userID := "019cf800-0000-7000-8000-0000000fa002"
				_, err = testDB.Pool.Exec(ctx,
					"INSERT INTO users (id, name, email, external_id) VALUES ($1, $2, $3, $4)",
					userID, "Test User 2", "fanart-test-2@example.com", "ext-fanart-02")
				require.NoError(t, err)

				err = followRepo.Follow(ctx, userID, artistID)
				require.NoError(t, err)

				return userID
			},
			want: []*entity.FollowedArtist{
				{
					UserID: "019cf800-0000-7000-8000-0000000fa002",
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
			tt.args = args{userID: userID}

			got, err := followRepo.ListByUser(ctx, tt.args.userID)

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
				w.Artist.ID = g.Artist.ID
				assert.Equal(t, w, g)
			}
		})
	}
}
