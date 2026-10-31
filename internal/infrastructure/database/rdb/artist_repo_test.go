package rdb_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestArtistRepository_Create(t *testing.T) {
	repo := rdb.NewArtistRepository(testDB, testOutbox)
	ctx := context.Background()

	// Pre-build reusable inputs so we can reference their generated IDs in want.
	beatles := entity.NewArtist("The Beatles", "b10bbbfc-cf9e-42e0-be17-e2c3e1d2600d")
	bulkA := entity.NewArtist("Artist A", "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaa111")
	bulkB := entity.NewArtist("Artist B", "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbb222")
	bulkC := entity.NewArtist("Artist C", "cccccccc-cccc-cccc-cccc-ccccccccc333")
	presetID := &entity.Artist{ID: "018b2f19-e591-7d12-bf9e-f0e74f1b4900", Name: "Pre-set ID Artist", MBID: "11111111-2222-3333-4444-55555preset1"}

	type args struct {
		artists []*entity.Artist
	}

	tests := []struct {
		name    string
		setup   func()
		args    args
		want    []*entity.Artist
		wantErr error
	}{
		{
			name:  "single artist with MBID",
			setup: func() { cleanDatabase(t) },
			args:  args{artists: []*entity.Artist{beatles}},
			want:  []*entity.Artist{beatles},
		},
		{
			name:  "bulk insert multiple artists",
			setup: func() { cleanDatabase(t) },
			args:  args{artists: []*entity.Artist{bulkA, bulkB, bulkC}},
			want:  []*entity.Artist{bulkA, bulkB, bulkC},
		},
		{
			name:  "empty slice returns empty",
			setup: func() { cleanDatabase(t) },
			args:  args{artists: nil},
			want:  []*entity.Artist{},
		},
		{
			name:  "preserves pre-set ID",
			setup: func() { cleanDatabase(t) },
			args:  args{artists: []*entity.Artist{presetID}},
			want:  []*entity.Artist{presetID},
		},
		{
			name: "duplicate MBID returns original artist",
			setup: func() {
				cleanDatabase(t)
				_, err := repo.Create(ctx, entity.NewArtist("Original Name", "eeeeeeee-eeee-eeee-eeee-eeeeeeeed001"))
				require.NoError(t, err)
			},
			args: args{artists: []*entity.Artist{entity.NewArtist("Different Name", "eeeeeeee-eeee-eeee-eeee-eeeeeeeed001")}},
			want: []*entity.Artist{{Name: "Original Name", MBID: "eeeeeeee-eeee-eeee-eeee-eeeeeeeed001"}},
		},
		{
			name:    "empty MBID returns error",
			setup:   func() { cleanDatabase(t) },
			args:    args{artists: []*entity.Artist{entity.NewArtist("Test", "")}},
			wantErr: apperr.ErrInvalidArgument,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.setup != nil {
				tt.setup()
			}

			got, err := repo.Create(ctx, tt.args.artists...)

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				return
			}

			require.NoError(t, err)
			require.Len(t, got, len(tt.want))
			for i, w := range tt.want {
				if w.ID != "" {
					assert.Equal(t, w.ID, got[i].ID)
				} else {
					assert.NotEmpty(t, got[i].ID)
				}
				assert.Equal(t, w.Name, got[i].Name)
				assert.Equal(t, w.MBID, got[i].MBID)
			}
		})
	}
}

func TestArtistRepository_ListByMBIDs(t *testing.T) {
	repo := rdb.NewArtistRepository(testDB, testOutbox)
	ctx := context.Background()

	tests := []struct {
		name    string
		setup   func()
		mbids   []string
		want    []*entity.Artist
		wantErr error
	}{
		{
			name: "returns matching artists in input order",
			setup: func() {
				cleanDatabase(t)
				_, err := repo.Create(ctx,
					entity.NewArtist("Artist A", "11111111-1111-1111-1111-11111111a001"),
					entity.NewArtist("Artist B", "22222222-2222-2222-2222-22222222b002"),
					entity.NewArtist("Artist C", "33333333-3333-3333-3333-33333333c003"),
				)
				require.NoError(t, err)
			},
			mbids: []string{"33333333-3333-3333-3333-33333333c003", "11111111-1111-1111-1111-11111111a001"},
			want: []*entity.Artist{
				{Name: "Artist C", MBID: "33333333-3333-3333-3333-33333333c003"},
				{Name: "Artist A", MBID: "11111111-1111-1111-1111-11111111a001"},
			},
		},
		{
			name: "unknown MBIDs are silently skipped",
			setup: func() {
				cleanDatabase(t)
				_, err := repo.Create(ctx, entity.NewArtist("Known", "44444444-4444-4444-4444-444444known1"))
				require.NoError(t, err)
			},
			mbids: []string{"44444444-4444-4444-4444-444444known1", "55555555-5555-5555-5555-5555unknown1"},
			want: []*entity.Artist{
				{Name: "Known", MBID: "44444444-4444-4444-4444-444444known1"},
			},
		},
		{
			name:  "empty input returns empty slice",
			setup: func() { cleanDatabase(t) },
			mbids: nil,
			want:  []*entity.Artist{},
		},
		{
			name: "all MBIDs unknown returns empty slice",
			setup: func() {
				cleanDatabase(t)
			},
			mbids: []string{"66666666-6666-6666-6666-666nonexist1"},
			want:  []*entity.Artist{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.setup != nil {
				tt.setup()
			}

			got, err := repo.ListByMBIDs(ctx, tt.mbids)

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				return
			}

			require.NoError(t, err)
			require.Len(t, got, len(tt.want))
			for i, w := range tt.want {
				assert.NotEmpty(t, got[i].ID)
				assert.Equal(t, w.Name, got[i].Name)
				assert.Equal(t, w.MBID, got[i].MBID)
			}
		})
	}
}

func TestArtistRepository_UpdateFanart(t *testing.T) {
	repo := rdb.NewArtistRepository(testDB, testOutbox)
	ctx := context.Background()

	t.Run("stores and retrieves fanart data", func(t *testing.T) {
		cleanDatabase(t)
		created, err := repo.Create(ctx, entity.NewArtist("Fanart Artist", "fa000000-0000-0000-0000-00000000dd01"))
		require.NoError(t, err)
		artistID := created[0].ID

		fanart := &entity.Fanart{
			ArtistThumb: []entity.FanartImage{
				{ID: "100", URL: "https://assets.fanart.tv/thumb.jpg", Likes: 5, Lang: "en"},
			},
			HDMusicLogo: []entity.FanartImage{
				{ID: "200", URL: "https://assets.fanart.tv/logo.png", Likes: 10, Lang: "ja"},
			},
		}
		syncTime := time.Date(2026, 3, 16, 10, 0, 0, 0, time.UTC)

		err = repo.UpdateFanart(ctx, artistID, fanart, syncTime)
		require.NoError(t, err)

		got, err := repo.Get(ctx, artistID)
		require.NoError(t, err)
		require.NotNil(t, got.Fanart)
		assert.Len(t, got.Fanart.ArtistThumb, 1)
		assert.Equal(t, "https://assets.fanart.tv/thumb.jpg", got.Fanart.ArtistThumb[0].URL)
		assert.Equal(t, 5, got.Fanart.ArtistThumb[0].Likes)
		assert.Len(t, got.Fanart.HDMusicLogo, 1)
		assert.Equal(t, "https://assets.fanart.tv/logo.png", got.Fanart.HDMusicLogo[0].URL)
		require.NotNil(t, got.FanartSyncTime)
		assert.True(t, syncTime.Equal(*got.FanartSyncTime))
	})

	t.Run("stores nil fanart with sync time", func(t *testing.T) {
		cleanDatabase(t)
		created, err := repo.Create(ctx, entity.NewArtist("No Fanart", "fa000000-0000-0000-0000-00000000dd02"))
		require.NoError(t, err)
		artistID := created[0].ID

		syncTime := time.Date(2026, 3, 16, 12, 0, 0, 0, time.UTC)
		err = repo.UpdateFanart(ctx, artistID, nil, syncTime)
		require.NoError(t, err)

		got, err := repo.Get(ctx, artistID)
		require.NoError(t, err)
		assert.Nil(t, got.Fanart)
		require.NotNil(t, got.FanartSyncTime)
		assert.True(t, syncTime.Equal(*got.FanartSyncTime))
	})

	t.Run("returns NotFound for unknown ID", func(t *testing.T) {
		cleanDatabase(t)
		err := repo.UpdateFanart(ctx, "00000000-0000-0000-0000-000000000000", nil, time.Now())
		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})
}

func TestArtistRepository_ListStaleOrMissingFanart(t *testing.T) {
	repo := rdb.NewArtistRepository(testDB, testOutbox)
	ctx := context.Background()
	const age = 7 * 24 * time.Hour
	daysAgo := func(n int) time.Time { return time.Now().Add(-time.Duration(n) * 24 * time.Hour) }
	fanart := &entity.Fanart{
		ArtistThumb: []entity.FanartImage{
			{ID: "1", URL: "https://example.com/thumb.jpg", Likes: 1, Lang: "en"},
		},
	}

	// @spec components/entity/artist/list-stale-or-missing-fanart "Never-checked and stale artists"
	t.Run("Never-checked and stale artists", func(t *testing.T) {
		cleanDatabase(t)
		userID := seedUser(t, "Fan", "fan-fanart-stale@example.com", "ext-fan-fanart-stale")
		staleID := seedArtist(t, "Stale", "50000000-0000-0000-0000-00000000aa01")
		neverID := seedArtist(t, "Never", "50000000-0000-0000-0000-00000000aa02")
		followArtist(t, userID, staleID)
		require.NoError(t, repo.UpdateFanart(ctx, staleID, fanart, daysAgo(8)))

		got, err := repo.ListStaleOrMissingFanart(ctx, age, 10)

		require.NoError(t, err)
		assert.Equal(t, []string{"Never", "Stale"}, artistNames(got))
		assert.Equal(t, neverID, got[0].ID)
		assert.Nil(t, got[0].FanartSyncTime)
		require.NotNil(t, got[1].FanartSyncTime)
	})

	// @spec components/entity/artist/list-stale-or-missing-fanart "Never-checked artist nobody follows"
	t.Run("Never-checked artist nobody follows", func(t *testing.T) {
		cleanDatabase(t)
		artistID := seedArtist(t, "Unfollowed Never", "50000000-0000-0000-0000-00000000bb01")

		got, err := repo.ListStaleOrMissingFanart(ctx, age, 10)

		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.Equal(t, artistID, got[0].ID)
	})

	// @spec components/entity/artist/list-stale-or-missing-fanart "Stale artist nobody follows"
	t.Run("Stale artist nobody follows", func(t *testing.T) {
		cleanDatabase(t)
		artistID := seedArtist(t, "Unfollowed Stale", "50000000-0000-0000-0000-00000000cc01")
		require.NoError(t, repo.UpdateFanart(ctx, artistID, fanart, daysAgo(30)))

		got, err := repo.ListStaleOrMissingFanart(ctx, age, 10)

		require.NoError(t, err)
		assert.Empty(t, got)
	})

	// @spec components/entity/artist/list-stale-or-missing-fanart "Artist followed by several fans"
	t.Run("Artist followed by several fans", func(t *testing.T) {
		cleanDatabase(t)
		artistID := seedArtist(t, "Popular", "50000000-0000-0000-0000-00000000dd01")
		for i := range 3 {
			userID := seedUser(t, fmt.Sprintf("Fan %d", i), fmt.Sprintf("fan-fanart-%d@example.com", i), fmt.Sprintf("ext-fan-fanart-%d", i))
			followArtist(t, userID, artistID)
		}
		require.NoError(t, repo.UpdateFanart(ctx, artistID, fanart, daysAgo(8)))

		got, err := repo.ListStaleOrMissingFanart(ctx, age, 10)

		require.NoError(t, err)
		assert.Equal(t, []string{"Popular"}, artistNames(got))
	})

	// @spec components/entity/artist/list-stale-or-missing-fanart "Recently checked artist without images"
	t.Run("Recently checked artist without images", func(t *testing.T) {
		cleanDatabase(t)
		userID := seedUser(t, "Fan", "fan-fanart-recent@example.com", "ext-fan-fanart-recent")
		artistID := seedArtist(t, "No Images", "50000000-0000-0000-0000-00000000ee01")
		followArtist(t, userID, artistID)
		require.NoError(t, repo.UpdateFanart(ctx, artistID, nil, daysAgo(2)))

		got, err := repo.ListStaleOrMissingFanart(ctx, age, 10)

		require.NoError(t, err)
		assert.Empty(t, got)
	})

	// @spec components/entity/artist/list-stale-or-missing-fanart "More due artists than the limit"
	t.Run("More due artists than the limit", func(t *testing.T) {
		cleanDatabase(t)
		artists := make([]*entity.Artist, 600)
		for i := range artists {
			artists[i] = entity.NewArtist(fmt.Sprintf("Due %03d", i), fmt.Sprintf("50000000-0000-0000-0000-000000000%03d", i))
		}
		_, err := repo.Create(ctx, artists...)
		require.NoError(t, err)

		got, err := repo.ListStaleOrMissingFanart(ctx, age, 500)

		require.NoError(t, err)
		assert.Len(t, got, 500)
	})

	// @spec components/entity/artist/list-stale-or-missing-fanart "Nothing due"
	t.Run("Nothing due", func(t *testing.T) {
		cleanDatabase(t)
		userID := seedUser(t, "Fan", "fan-fanart-none@example.com", "ext-fan-fanart-none")
		followedID := seedArtist(t, "Fresh Followed", "50000000-0000-0000-0000-00000000ff01")
		unfollowedID := seedArtist(t, "Old Unfollowed", "50000000-0000-0000-0000-00000000ff02")
		followArtist(t, userID, followedID)
		require.NoError(t, repo.UpdateFanart(ctx, followedID, fanart, time.Now()))
		require.NoError(t, repo.UpdateFanart(ctx, unfollowedID, fanart, daysAgo(30)))

		got, err := repo.ListStaleOrMissingFanart(ctx, age, 10)

		require.NoError(t, err)
		assert.Empty(t, got)
	})
}

func TestArtistRepository_UpdateName(t *testing.T) {
	repo := rdb.NewArtistRepository(testDB, testOutbox)
	ctx := context.Background()

	tests := []struct {
		name    string
		setup   func() string // returns artist ID
		newName string
		wantErr error
	}{
		{
			name: "updates name successfully",
			setup: func() string {
				cleanDatabase(t)
				created, err := repo.Create(ctx, entity.NewArtist("Old Name", "77777777-7777-7777-7777-00000000ee01"))
				require.NoError(t, err)
				return created[0].ID
			},
			newName: "New Name",
		},
		{
			name: "returns NotFound for unknown ID",
			setup: func() string {
				cleanDatabase(t)
				return "00000000-0000-0000-0000-000000000000"
			},
			newName: "Anything",
			wantErr: apperr.ErrNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			artistID := tt.setup()

			err := repo.UpdateName(ctx, artistID, tt.newName)

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				return
			}

			require.NoError(t, err)

			// Verify the name was actually updated.
			got, err := repo.Get(ctx, artistID)
			require.NoError(t, err)
			assert.Equal(t, tt.newName, got.Name)
		})
	}
}

func TestArtistRepository_Get(t *testing.T) {
	repo := rdb.NewArtistRepository(testDB, testOutbox)
	ctx := context.Background()

	type args struct {
		id string
	}

	tests := []struct {
		name    string
		setup   func() string // returns artistID
		args    args
		wantErr error
	}{
		{
			name: "returns existing artist",
			setup: func() string {
				cleanDatabase(t)
				created, err := repo.Create(ctx, entity.NewArtist("Get Test Artist", "aa000000-0000-0000-0000-000000get001"))
				require.NoError(t, err)
				return created[0].ID
			},
			wantErr: nil,
		},
		{
			name: "returns NotFound for non-existent ID",
			setup: func() string {
				cleanDatabase(t)
				return "00000000-0000-0000-0000-000000000000"
			},
			wantErr: apperr.ErrNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			artistID := tt.setup()
			tt.args = args{id: artistID}

			got, err := repo.Get(ctx, tt.args.id)

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, artistID, got.ID)
			assert.Equal(t, "Get Test Artist", got.Name)
		})
	}
}

func TestArtistRepository_GetByMBID(t *testing.T) {
	repo := rdb.NewArtistRepository(testDB, testOutbox)
	ctx := context.Background()

	const knownMBID = "bb000000-0000-0000-0000-0000getmbid1"

	type args struct {
		mbid string
	}

	tests := []struct {
		name    string
		setup   func()
		args    args
		wantErr error
	}{
		{
			name: "returns artist by known MBID",
			setup: func() {
				cleanDatabase(t)
				_, err := repo.Create(ctx, entity.NewArtist("MBID Test Artist", knownMBID))
				require.NoError(t, err)
			},
			args:    args{mbid: knownMBID},
			wantErr: nil,
		},
		{
			name: "returns NotFound for unknown MBID",
			setup: func() {
				cleanDatabase(t)
			},
			args:    args{mbid: "00000000-0000-0000-0000-000000000000"},
			wantErr: apperr.ErrNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.setup()

			got, err := repo.GetByMBID(ctx, tt.args.mbid)

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, knownMBID, got.MBID)
			assert.Equal(t, "MBID Test Artist", got.Name)
		})
	}
}

func TestArtistRepository_List(t *testing.T) {
	repo := rdb.NewArtistRepository(testDB, testOutbox)
	ctx := context.Background()

	tests := []struct {
		name      string
		setup     func()
		wantLen   int
		wantEmpty bool
		wantErr   error
	}{
		{
			name: "empty database returns empty slice",
			setup: func() {
				cleanDatabase(t)
			},
			wantEmpty: true,
		},
		{
			name: "returns all artists",
			setup: func() {
				cleanDatabase(t)
				_, err := repo.Create(ctx,
					entity.NewArtist("List Artist A", "cc000000-0000-0000-0000-00000list001"),
					entity.NewArtist("List Artist B", "cc000000-0000-0000-0000-00000list002"),
					entity.NewArtist("List Artist C", "cc000000-0000-0000-0000-00000list003"),
				)
				require.NoError(t, err)
			},
			wantLen: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.setup()

			got, err := repo.List(ctx)

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				return
			}

			require.NoError(t, err)
			if tt.wantEmpty {
				assert.Empty(t, got)
			} else {
				assert.Len(t, got, tt.wantLen)
			}
		})
	}
}

func TestArtistRepository_CreateOfficialSite(t *testing.T) {
	repo := rdb.NewArtistRepository(testDB, testOutbox)
	ctx := context.Background()

	type args struct {
		site *entity.OfficialSite
	}

	tests := []struct {
		name    string
		setup   func() string // returns artistID
		args    args
		wantErr error
	}{
		{
			name: "creates site for existing artist",
			setup: func() string {
				cleanDatabase(t)
				created, err := repo.Create(ctx, entity.NewArtist("Site Artist", "dd000000-0000-0000-0000-00000site001"))
				require.NoError(t, err)
				return created[0].ID
			},
			wantErr: nil,
		},
		{
			name: "returns AlreadyExists when creating second site for same artist",
			setup: func() string {
				cleanDatabase(t)
				created, err := repo.Create(ctx, entity.NewArtist("Site Artist Dup", "dd000000-0000-0000-0000-00000site002"))
				require.NoError(t, err)
				artistID := created[0].ID
				err = repo.CreateOfficialSite(ctx, entity.NewOfficialSite(artistID, "https://first.example.com"))
				require.NoError(t, err)
				return artistID
			},
			wantErr: apperr.ErrAlreadyExists,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			artistID := tt.setup()
			tt.args = args{site: entity.NewOfficialSite(artistID, "https://example.com")}

			err := repo.CreateOfficialSite(ctx, tt.args.site)

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				return
			}

			require.NoError(t, err)
		})
	}
}

func TestArtistRepository_GetOfficialSite(t *testing.T) {
	repo := rdb.NewArtistRepository(testDB, testOutbox)
	ctx := context.Background()

	type args struct {
		artistID string
	}

	tests := []struct {
		name    string
		setup   func() string // returns artistID
		args    args
		wantURL string
		wantErr error
	}{
		{
			name: "returns site after creating one",
			setup: func() string {
				cleanDatabase(t)
				created, err := repo.Create(ctx, entity.NewArtist("Get Site Artist", "ee000000-0000-0000-0000-0000getsite1"))
				require.NoError(t, err)
				artistID := created[0].ID
				err = repo.CreateOfficialSite(ctx, entity.NewOfficialSite(artistID, "https://getsite.example.com"))
				require.NoError(t, err)
				return artistID
			},
			wantURL: "https://getsite.example.com",
			wantErr: nil,
		},
		{
			name: "returns NotFound when artist has no official site",
			setup: func() string {
				cleanDatabase(t)
				created, err := repo.Create(ctx, entity.NewArtist("No Site Artist", "ee000000-0000-0000-0000-0000getsite2"))
				require.NoError(t, err)
				return created[0].ID
			},
			wantErr: apperr.ErrNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			artistID := tt.setup()
			tt.args = args{artistID: artistID}

			got, err := repo.GetOfficialSite(ctx, tt.args.artistID)

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, artistID, got.ArtistID)
			assert.Equal(t, tt.wantURL, got.URL)
			assert.NotEmpty(t, got.ID)
		})
	}
}
