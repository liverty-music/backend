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
	t.Parallel()

	type wantImages struct {
		thumbCount  int
		thumb0URL   string
		thumb0Likes int
		thumb1URL   string
		thumb1Likes int
		logoCount   int
		logo0URL    string
	}

	tests := []struct {
		name         string
		mbid         string
		statusCode   int
		responseBody string
		contentType  string
		cancelCtx    bool
		wantErr      error
		want         *wantImages
	}{
		{
			name:        "parses successful response",
			mbid:        "mbid-123",
			statusCode:  http.StatusOK,
			contentType: "application/json",
			responseBody: `{
				"artistthumb": [
					{"id": "100", "url": "https://assets.fanart.tv/thumb1.jpg", "likes": "5", "lang": "en"},
					{"id": "101", "url": "https://assets.fanart.tv/thumb2.jpg", "likes": "12", "lang": "ja"}
				],
				"hdmusiclogo": [
					{"id": "200", "url": "https://assets.fanart.tv/logo.png", "likes": "8", "lang": "en"}
				],
				"musicbanner": []
			}`,
			want: &wantImages{
				thumbCount:  2,
				thumb0URL:   "https://assets.fanart.tv/thumb1.jpg",
				thumb0Likes: 5,
				thumb1URL:   "https://assets.fanart.tv/thumb2.jpg",
				thumb1Likes: 12,
				logoCount:   1,
				logo0URL:    "https://assets.fanart.tv/logo.png",
			},
		},
		{
			name:         "returns nil for 404 not found",
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

			if tt.want == nil {
				assert.Nil(t, result)
				return
			}

			require.NotNil(t, result)
			assert.Len(t, result.ArtistThumb, tt.want.thumbCount)
			if tt.want.thumbCount >= 1 {
				assert.Equal(t, tt.want.thumb0URL, result.ArtistThumb[0].URL)
				assert.Equal(t, tt.want.thumb0Likes, result.ArtistThumb[0].Likes)
			}
			if tt.want.thumbCount >= 2 {
				assert.Equal(t, tt.want.thumb1URL, result.ArtistThumb[1].URL)
				assert.Equal(t, tt.want.thumb1Likes, result.ArtistThumb[1].Likes)
			}
			assert.Len(t, result.HDMusicLogo, tt.want.logoCount)
			if tt.want.logoCount >= 1 {
				assert.Equal(t, tt.want.logo0URL, result.HDMusicLogo[0].URL)
			}
			assert.Empty(t, result.MusicBanner)
		})
	}
}
