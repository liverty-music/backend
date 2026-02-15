//go:build integration

package musicbrainz_test

import (
	"context"
	"testing"

	"github.com/liverty-music/backend/internal/infrastructure/music/musicbrainz"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClient_Integration_GetArtist(t *testing.T) {
	client := musicbrainz.NewClient(nil)
	ctx := context.Background()

	t.Run("Radiohead", func(t *testing.T) {
		t.Skip("Skipping flaky integration test - MusicBrainz API connection unstable (see #51)")
		artist, err := client.GetArtist(ctx, "a74b1b7f-71a5-4011-9441-d0b5e4122711")
		require.NoError(t, err)
		assert.Equal(t, "Radiohead", artist.Name)
		assert.Equal(t, "a74b1b7f-71a5-4011-9441-d0b5e4122711", artist.MBID)
	})

	t.Run("UVERworld", func(t *testing.T) {
		artist, err := client.GetArtist(ctx, "a107bff6-58da-4302-83ad-317e86a1811c")
		require.NoError(t, err)
		assert.Equal(t, "UVERworld", artist.Name)
		assert.Equal(t, "a107bff6-58da-4302-83ad-317e86a1811c", artist.MBID)
	})
}
