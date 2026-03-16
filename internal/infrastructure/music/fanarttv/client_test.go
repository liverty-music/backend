package fanarttv_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/liverty-music/backend/internal/infrastructure/music/fanarttv"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testAPIKey = "test-api-key"

func newTestLogger(t *testing.T) *logging.Logger {
	t.Helper()
	logger, err := logging.New()
	require.NoError(t, err)
	return logger
}

func TestClient_ResolveImages(t *testing.T) {
	t.Run("parses successful response", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Contains(t, r.URL.RawQuery, "api_key="+testAPIKey)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{
				"artistthumb": [
					{"id": "100", "url": "https://assets.fanart.tv/thumb1.jpg", "likes": "5", "lang": "en"},
					{"id": "101", "url": "https://assets.fanart.tv/thumb2.jpg", "likes": "12", "lang": "ja"}
				],
				"hdmusiclogo": [
					{"id": "200", "url": "https://assets.fanart.tv/logo.png", "likes": "8", "lang": "en"}
				],
				"musicbanner": []
			}`))
		}))
		defer srv.Close()

		c := fanarttv.NewClient(testAPIKey, srv.Client(), newTestLogger(t))
		c.SetBaseURL(srv.URL + "/")

		result, err := c.ResolveImages(context.Background(), "mbid-123")
		require.NoError(t, err)
		require.NotNil(t, result)

		assert.Len(t, result.ArtistThumb, 2)
		assert.Equal(t, "https://assets.fanart.tv/thumb1.jpg", result.ArtistThumb[0].URL)
		assert.Equal(t, 5, result.ArtistThumb[0].Likes)
		assert.Equal(t, "https://assets.fanart.tv/thumb2.jpg", result.ArtistThumb[1].URL)
		assert.Equal(t, 12, result.ArtistThumb[1].Likes)

		assert.Len(t, result.HDMusicLogo, 1)
		assert.Equal(t, "https://assets.fanart.tv/logo.png", result.HDMusicLogo[0].URL)

		assert.Empty(t, result.MusicBanner)
	})

	t.Run("returns nil for 404 not found", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		}))
		defer srv.Close()

		c := fanarttv.NewClient(testAPIKey, srv.Client(), newTestLogger(t))
		c.SetBaseURL(srv.URL + "/")

		result, err := c.ResolveImages(context.Background(), "mbid-unknown")
		assert.NoError(t, err)
		assert.Nil(t, result)
	})

	t.Run("returns error for server error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer srv.Close()

		c := fanarttv.NewClient(testAPIKey, srv.Client(), newTestLogger(t))
		c.SetBaseURL(srv.URL + "/")

		result, err := c.ResolveImages(context.Background(), "mbid-error")
		assert.Error(t, err)
		assert.Nil(t, result)
	})

	t.Run("returns error for invalid JSON", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{not valid json`))
		}))
		defer srv.Close()

		c := fanarttv.NewClient(testAPIKey, srv.Client(), newTestLogger(t))
		c.SetBaseURL(srv.URL + "/")

		result, err := c.ResolveImages(context.Background(), "mbid-bad-json")
		assert.Error(t, err)
		assert.Nil(t, result)
	})

	t.Run("returns error when context is cancelled", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{}`))
		}))
		defer srv.Close()

		c := fanarttv.NewClient(testAPIKey, srv.Client(), newTestLogger(t))
		c.SetBaseURL(srv.URL + "/")

		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		result, err := c.ResolveImages(ctx, "mbid-cancelled")
		assert.Error(t, err)
		assert.Nil(t, result)
	})
}
