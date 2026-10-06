package rdb_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// followArtist records that userID follows artistID.
func followArtist(t *testing.T, userID, artistID string) {
	t.Helper()
	_, err := testDB.Pool.Exec(context.Background(),
		"INSERT INTO followed_artists (user_id, artist_id) VALUES ($1, $2)",
		userID, artistID,
	)
	require.NoError(t, err)
}

// officialSiteCheckTime reads artists.official_site_checked_at for artistID.
func officialSiteCheckTime(t *testing.T, artistID string) *time.Time {
	t.Helper()
	var checkedAt *time.Time
	err := testDB.Pool.QueryRow(context.Background(),
		"SELECT official_site_checked_at FROM artists WHERE id = $1", artistID,
	).Scan(&checkedAt)
	require.NoError(t, err)
	return checkedAt
}

func artistNames(artists []*entity.Artist) []string {
	names := make([]string, len(artists))
	for i, a := range artists {
		names[i] = a.Name
	}
	return names
}

func TestArtistRepository_ListStaleOfficialSite(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	repo := rdb.NewArtistRepository(testDB)
	ctx := context.Background()
	const age = 7 * 24 * time.Hour

	// @spec components/entity/artist/list-stale-official-site "Never-checked and stale artists"
	t.Run("Never-checked and stale artists", func(t *testing.T) {
		cleanDatabase(t)
		userID := seedUser(t, "Fan", "fan-stale@example.com", "ext-fan-stale")
		staleID := seedArtist(t, "Stale", "51000000-0000-0000-0000-00000000aa01")
		neverID := seedArtist(t, "Never", "51000000-0000-0000-0000-00000000aa02")
		followArtist(t, userID, staleID)
		followArtist(t, userID, neverID)
		require.NoError(t, repo.MarkOfficialSiteChecked(ctx, staleID, time.Now().Add(-8*24*time.Hour)))

		got, err := repo.ListStaleOfficialSite(ctx, age, 10)

		require.NoError(t, err)
		assert.Equal(t, []string{"Never", "Stale"}, artistNames(got))
		assert.Nil(t, got[0].OfficialSiteCheckTime)
		require.NotNil(t, got[1].OfficialSiteCheckTime)
		assert.Equal(t, "51000000-0000-0000-0000-00000000aa02", got[0].MBID)
		assert.Equal(t, neverID, got[0].ID)
	})

	// @spec components/entity/artist/list-stale-official-site "Recently checked artist"
	t.Run("Recently checked artist", func(t *testing.T) {
		cleanDatabase(t)
		userID := seedUser(t, "Fan", "fan-recent@example.com", "ext-fan-recent")
		artistID := seedArtist(t, "Recent", "52000000-0000-0000-0000-00000000aa01")
		followArtist(t, userID, artistID)
		require.NoError(t, repo.MarkOfficialSiteChecked(ctx, artistID, time.Now().Add(-2*24*time.Hour)))

		got, err := repo.ListStaleOfficialSite(ctx, age, 10)

		require.NoError(t, err)
		assert.Empty(t, got)
	})

	// @spec components/entity/artist/list-stale-official-site "Followed artist without an official site"
	t.Run("Followed artist without an official site", func(t *testing.T) {
		cleanDatabase(t)
		userID := seedUser(t, "Fan", "fan-nosite@example.com", "ext-fan-nosite")
		withSiteID := seedArtist(t, "With Site", "53000000-0000-0000-0000-00000000aa01")
		noSiteID := seedArtist(t, "No Site", "53000000-0000-0000-0000-00000000aa02")
		followArtist(t, userID, withSiteID)
		followArtist(t, userID, noSiteID)
		require.NoError(t, repo.CreateOfficialSite(ctx, entity.NewOfficialSite(withSiteID, "https://example.com/")))

		got, err := repo.ListStaleOfficialSite(ctx, age, 10)

		require.NoError(t, err)
		assert.ElementsMatch(t, []string{"With Site", "No Site"}, artistNames(got))
	})

	// @spec components/entity/artist/list-stale-official-site "Artist nobody follows"
	t.Run("Artist nobody follows", func(t *testing.T) {
		cleanDatabase(t)
		user1 := seedUser(t, "Fan 1", "fan-1@example.com", "ext-fan-1")
		user2 := seedUser(t, "Fan 2", "fan-2@example.com", "ext-fan-2")
		followedID := seedArtist(t, "Followed Twice", "54000000-0000-0000-0000-00000000aa01")
		seedArtist(t, "Unfollowed", "54000000-0000-0000-0000-00000000aa02")
		followArtist(t, user1, followedID)
		followArtist(t, user2, followedID)

		got, err := repo.ListStaleOfficialSite(ctx, age, 10)

		require.NoError(t, err)
		// The artist followed by two fans is returned once; the unfollowed one is not.
		assert.Equal(t, []string{"Followed Twice"}, artistNames(got))
	})

	// @spec components/entity/artist/list-stale-official-site "More due artists than the limit"
	t.Run("More due artists than the limit", func(t *testing.T) {
		cleanDatabase(t)
		userID := seedUser(t, "Fan", "fan-limit@example.com", "ext-fan-limit")
		for i := range 30 {
			artistID := seedArtist(t, fmt.Sprintf("Due %02d", i), fmt.Sprintf("55000000-0000-0000-0000-0000000000%02d", i))
			followArtist(t, userID, artistID)
		}

		got, err := repo.ListStaleOfficialSite(ctx, age, 20)

		require.NoError(t, err)
		assert.Len(t, got, 20)
	})

	// @spec components/entity/artist/list-stale-official-site "Nothing due"
	t.Run("Nothing due", func(t *testing.T) {
		cleanDatabase(t)
		userID := seedUser(t, "Fan", "fan-none@example.com", "ext-fan-none")
		artistID := seedArtist(t, "Fresh", "56000000-0000-0000-0000-00000000aa01")
		followArtist(t, userID, artistID)
		require.NoError(t, repo.MarkOfficialSiteChecked(ctx, artistID, time.Now()))

		got, err := repo.ListStaleOfficialSite(ctx, age, 10)

		require.NoError(t, err)
		assert.NotNil(t, got)
		assert.Empty(t, got)
	})
}

func TestArtistRepository_UpdateOfficialSiteURL(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	repo := rdb.NewArtistRepository(testDB)
	ctx := context.Background()

	// @spec components/entity/artist/update-official-site-url "Artist with a site"
	t.Run("Artist with a site", func(t *testing.T) {
		cleanDatabase(t)
		artistID := seedArtist(t, "羊文学", "61000000-0000-0000-0000-00000000aa01")
		site := entity.NewOfficialSite(artistID, "http://hitsujibungaku.jimdo.com/")
		require.NoError(t, repo.CreateOfficialSite(ctx, site))

		err := repo.UpdateOfficialSiteURL(ctx, artistID, "https://www.hitsujibungaku.info/")

		require.NoError(t, err)
		got, err := repo.GetOfficialSite(ctx, artistID)
		require.NoError(t, err)
		assert.Equal(t, site.ID, got.ID)
		assert.Equal(t, artistID, got.ArtistID)
		assert.Equal(t, "https://www.hitsujibungaku.info/", got.URL)
	})

	// @spec components/entity/artist/update-official-site-url "Artist without a site"
	t.Run("Artist without a site", func(t *testing.T) {
		cleanDatabase(t)
		artistID := seedArtist(t, "No Site", "61000000-0000-0000-0000-00000000aa02")

		err := repo.UpdateOfficialSiteURL(ctx, artistID, "https://example.com/")

		assert.ErrorIs(t, err, apperr.ErrNotFound)
		_, err = repo.GetOfficialSite(ctx, artistID)
		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})

	t.Run("unknown artist fails with NotFound", func(t *testing.T) {
		cleanDatabase(t)

		err := repo.UpdateOfficialSiteURL(ctx, entity.NewID(), "https://example.com/")

		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})

	// @spec components/entity/artist/update-official-site-url "Invalid URL"
	t.Run("Invalid URL", func(t *testing.T) {
		cleanDatabase(t)
		artistID := seedArtist(t, "Long URL", "61000000-0000-0000-0000-00000000aa03")
		require.NoError(t, repo.CreateOfficialSite(ctx, entity.NewOfficialSite(artistID, "https://example.com/")))
		overlong := "https://example.com/" + strings.Repeat("a", 2049)

		err := repo.UpdateOfficialSiteURL(ctx, artistID, overlong)

		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
		got, err := repo.GetOfficialSite(ctx, artistID)
		require.NoError(t, err)
		assert.Equal(t, "https://example.com/", got.URL)
	})
}

func TestArtistRepository_MarkOfficialSiteChecked(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	repo := rdb.NewArtistRepository(testDB)
	ctx := context.Background()

	// @spec components/entity/artist/mark-official-site-checked "Artist checked"
	t.Run("Artist checked", func(t *testing.T) {
		cleanDatabase(t)
		artistID := seedArtist(t, "Checked", "71000000-0000-0000-0000-00000000aa01")
		require.NoError(t, repo.CreateOfficialSite(ctx, entity.NewOfficialSite(artistID, "https://example.com/")))
		checkTime := time.Date(2026, 10, 5, 19, 0, 0, 0, time.UTC)

		err := repo.MarkOfficialSiteChecked(ctx, artistID, checkTime)

		require.NoError(t, err)
		got, err := repo.Get(ctx, artistID)
		require.NoError(t, err)
		require.NotNil(t, got.OfficialSiteCheckTime)
		assert.True(t, checkTime.Equal(*got.OfficialSiteCheckTime))
	})

	// @spec components/entity/artist/mark-official-site-checked "Artist without an official site"
	t.Run("Artist without an official site", func(t *testing.T) {
		cleanDatabase(t)
		artistID := seedArtist(t, "No Site", "71000000-0000-0000-0000-00000000aa02")

		err := repo.MarkOfficialSiteChecked(ctx, artistID, time.Now())

		require.NoError(t, err)
		assert.NotNil(t, officialSiteCheckTime(t, artistID))
		_, err = repo.GetOfficialSite(ctx, artistID)
		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})

	// @spec components/entity/artist/mark-official-site-checked "Unknown artist"
	t.Run("Unknown artist", func(t *testing.T) {
		cleanDatabase(t)

		err := repo.MarkOfficialSiteChecked(ctx, entity.NewID(), time.Now())

		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})
}
