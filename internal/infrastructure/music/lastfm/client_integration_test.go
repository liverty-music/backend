//go:build integration

package lastfm_test

import (
	"context"
	"testing"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/music/lastfm"
	"github.com/liverty-music/backend/internal/testutil"
	"github.com/liverty-music/backend/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClient_Integration(t *testing.T) {
	testutil.LoadTestEnv(t, "testdata/.env.test")

	// Set required environment variables for config.Load()
	t.Setenv("DATABASE_NAME", "test-db")
	t.Setenv("DATABASE_USER", "test-user")
	t.Setenv("OIDC_ISSUER_URL", "https://test-issuer.example.com")

	cfg, err := config.Load()
	require.NoError(t, err)

	if cfg.LastFMAPIKey == "" {
		t.Skip("LASTFM_API_KEY not set, skipping integration test")
	}

	client := lastfm.NewClient(cfg.LastFMAPIKey, nil)
	ctx := context.Background()

	t.Run("Search", func(t *testing.T) {
		artists, err := client.Search(ctx, "The Beatles")
		require.NoError(t, err)
		assert.NotEmpty(t, artists)
		t.Logf("Found %d artists for 'The Beatles'", len(artists))
		for _, a := range artists {
			t.Logf("  - %s (MBID: %s)", a.Name, a.MBID)
		}
	})

	t.Run("ListSimilar", func(t *testing.T) {
		// Using the MusicBrainz MBID for Radiohead, which works on Last.fm
		artist := &entity.Artist{MBID: "a74b1b7f-71a5-4011-9441-d0b5e4122711", Name: "Radiohead"}
		artists, err := client.ListSimilar(ctx, artist)
		require.NoError(t, err)
		assert.NotEmpty(t, artists)
		t.Logf("Found %d similar artists for 'Radiohead'", len(artists))
		for i, a := range artists {
			if i >= 5 {
				break
			}
			t.Logf("  - %s (MBID: %s)", a.Name, a.MBID)
		}
	})

	t.Run("ListTop", func(t *testing.T) {
		t.Run("Japan", func(t *testing.T) {
			artists, err := client.ListTop(ctx, "Japan", "")
			require.NoError(t, err)
			assert.NotEmpty(t, artists)
			t.Logf("Found %d top artists in Japan", len(artists))
			for i, a := range artists {
				if i >= 5 {
					break
				}
				t.Logf("  - %s (MBID: %s)", a.Name, a.MBID)
			}
		})

		t.Run("Global", func(t *testing.T) {
			artists, err := client.ListTop(ctx, "", "")
			require.NoError(t, err)
			assert.NotEmpty(t, artists)
			t.Logf("Found %d global top artists", len(artists))
			for i, a := range artists {
				if i >= 5 {
					break
				}
				t.Logf("  - %s (MBID: %s)", a.Name, a.MBID)
			}
		})
	})
}
