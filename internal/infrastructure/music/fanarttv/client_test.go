package fanarttv_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/music/fanarttv"
	"github.com/pannpers/go-apperr/apperr"
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
	t.Parallel()

	tests := []struct {
		name         string
		mbid         string
		statusCode   int
		responseBody string
		contentType  string
		cancelCtx    bool
		wantErr      error
		want         *entity.Fanart
	}{
		{
			// @spec components/entity/artist/resolve-images "All kinds available"
			name:        "returns images in every kind when the catalog has all of them",
			mbid:        "mbid-123",
			statusCode:  http.StatusOK,
			contentType: "application/json",
			responseBody: `{
				"artistthumb": [
					{"id": "100", "url": "https://assets.fanart.tv/thumb1.jpg", "likes": "5", "lang": "en"},
					{"id": "101", "url": "https://assets.fanart.tv/thumb2.jpg", "likes": "12", "lang": "ja"}
				],
				"artistbackground": [
					{"id": "300", "url": "https://assets.fanart.tv/background.jpg", "likes": "3", "lang": ""}
				],
				"musicbanner": [
					{"id": "400", "url": "https://assets.fanart.tv/banner.jpg", "likes": "1", "lang": "en"}
				]
			}`,
			want: &entity.Fanart{
				ArtistThumb: []entity.FanartImage{
					{ID: "100", URL: "https://assets.fanart.tv/thumb1.jpg", Likes: 5, Lang: "en"},
					{ID: "101", URL: "https://assets.fanart.tv/thumb2.jpg", Likes: 12, Lang: "ja"},
				},
				ArtistBackground: []entity.FanartImage{
					{ID: "300", URL: "https://assets.fanart.tv/background.jpg", Likes: 3, Lang: ""},
				},
				MusicBanner: []entity.FanartImage{
					{ID: "400", URL: "https://assets.fanart.tv/banner.jpg", Likes: 1, Lang: "en"},
				},
			},
		},
		{
			// @spec components/entity/artist/resolve-images "Some kinds available"
			name:        "returns only artist_thumb and artist_background when the catalog has only those",
			mbid:        "mbid-456",
			statusCode:  http.StatusOK,
			contentType: "application/json",
			responseBody: `{
				"artistthumb": [
					{"id": "100", "url": "https://assets.fanart.tv/thumb.jpg", "likes": "5", "lang": "en"}
				],
				"artistbackground": [
					{"id": "300", "url": "https://assets.fanart.tv/background.jpg", "likes": "3", "lang": ""}
				],
				"musicbanner": []
			}`,
			want: &entity.Fanart{
				ArtistThumb: []entity.FanartImage{
					{ID: "100", URL: "https://assets.fanart.tv/thumb.jpg", Likes: 5, Lang: "en"},
				},
				ArtistBackground: []entity.FanartImage{
					{ID: "300", URL: "https://assets.fanart.tv/background.jpg", Likes: 3, Lang: ""},
				},
				MusicBanner: []entity.FanartImage{},
			},
		},
		{
			// @spec components/entity/artist/resolve-images "No entry"
			name:         "returns no fanart and no error when the catalog has no entry",
			mbid:         "mbid-unknown",
			statusCode:   http.StatusNotFound,
			responseBody: "",
			want:         nil,
		},
		{
			name:         "returns error for server error",
			mbid:         "mbid-error",
			statusCode:   http.StatusInternalServerError,
			responseBody: "",
			wantErr:      assert.AnError,
		},
		{
			name:         "returns error for invalid JSON",
			mbid:         "mbid-bad-json",
			statusCode:   http.StatusOK,
			contentType:  "application/json",
			responseBody: `{not valid json`,
			wantErr:      assert.AnError,
		},
		{
			name:         "returns error when context is cancelled",
			mbid:         "mbid-cancelled",
			statusCode:   http.StatusOK,
			responseBody: `{}`,
			cancelCtx:    true,
			wantErr:      assert.AnError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, testAPIKey, r.Header.Get("api-key"))
				if tt.contentType != "" {
					w.Header().Set("Content-Type", tt.contentType)
				}
				w.WriteHeader(tt.statusCode)
				if tt.responseBody != "" {
					_, _ = w.Write([]byte(tt.responseBody))
				}
			}))
			defer srv.Close()

			c := fanarttv.NewClient(testAPIKey, srv.Client(), newTestLogger(t))
			c.SetBaseURL(srv.URL + "/")

			ctx := context.Background()
			if tt.cancelCtx {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}

			result, err := c.ResolveImages(ctx, tt.mbid)

			if tt.wantErr != nil {
				assert.Error(t, err)
				assert.Nil(t, result)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.want, result)
		})
	}
}

func TestClient_ResolveImages_ContextEnd(t *testing.T) {
	t.Parallel()

	// Both cases end the caller's context, once before the first attempt and
	// once while backoff.Retry waits out a Retry-After. backoff v6+ wraps the
	// context error in a *backoff.RetryError; the client must still report the
	// context's own code instead of Unavailable.
	tests := []struct {
		name    string
		newCtx  func(t *testing.T) context.Context
		wantErr error
	}{
		{
			name: "cancelled before the request maps to Canceled",
			newCtx: func(t *testing.T) context.Context {
				t.Helper()
				ctx, cancel := context.WithCancel(t.Context())
				cancel()
				return ctx
			},
			wantErr: apperr.ErrCanceled,
		},
		{
			name: "deadline during the Retry-After wait maps to DeadlineExceeded",
			newCtx: func(t *testing.T) context.Context {
				t.Helper()
				ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
				t.Cleanup(cancel)
				return ctx
			},
			wantErr: apperr.ErrDeadlineExceeded,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				// A long Retry-After keeps the retry loop waiting until the
				// context ends.
				w.Header().Set("Retry-After", "3600")
				w.WriteHeader(http.StatusTooManyRequests)
			}))
			defer server.Close()

			client := fanarttv.NewClient(testAPIKey, server.Client(), newTestLogger(t))
			client.SetBaseURL(server.URL + "/")

			got, err := client.ResolveImages(tt.newCtx(t), "test-mbid")

			assert.Nil(t, got)
			assert.ErrorIs(t, err, tt.wantErr)
		})
	}
}
