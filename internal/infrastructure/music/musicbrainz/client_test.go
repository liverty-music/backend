package musicbrainz_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/liverty-music/backend/internal/infrastructure/music/musicbrainz"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Local types mirroring the unexported response types in the package under test.
type artistResponse struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func TestClient_GetArtist(t *testing.T) {
	type args struct {
		mbid string
	}
	type want struct {
		name string
		mbid string
	}
	tests := []struct {
		name         string
		args         args
		statusCode   int
		responseBody interface{}
		wantErr      error
		want         want
		invalidJSON  bool
	}{
		{
			name:       "success - returns artist",
			args:       args{mbid: "a74b1b7f-71a5-4011-9441-d0b5e4122711"},
			statusCode: http.StatusOK,
			responseBody: artistResponse{
				ID:   "a74b1b7f-71a5-4011-9441-d0b5e4122711",
				Name: "Radiohead",
			},
			wantErr: nil,
			want: want{
				name: "Radiohead",
				mbid: "a74b1b7f-71a5-4011-9441-d0b5e4122711",
			},
		},
		{
			name:       "error - not found",
			args:       args{mbid: "non-existent"},
			statusCode: http.StatusNotFound,
			wantErr:    apperr.New(codes.NotFound, "musicbrainz api returned non-ok status: 404"),
		},
		{
			name:       "error - service unavailable (rate limit 503)",
			args:       args{mbid: "test-mbid"},
			statusCode: http.StatusServiceUnavailable,
			wantErr:    apperr.New(codes.ResourceExhausted, "musicbrainz api returned non-ok status: 503"),
		},
		{
			name:       "error - too many requests (rate limit 429)",
			args:       args{mbid: "test-mbid"},
			statusCode: http.StatusTooManyRequests,
			wantErr:    apperr.New(codes.ResourceExhausted, "musicbrainz api returned non-ok status: 429"),
		},
		{
			name:       "error - internal server error",
			args:       args{mbid: "test-mbid"},
			statusCode: http.StatusInternalServerError,
			wantErr:    apperr.New(codes.Unavailable, "musicbrainz api returned non-ok status: 500"),
		},
		{
			name:        "error - invalid JSON response",
			args:        args{mbid: "test-mbid"},
			statusCode:  http.StatusOK,
			invalidJSON: true,
			wantErr:     apperr.New(codes.DataLoss, "failed to decode musicbrainz response"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, fmt.Sprintf("/%s", tt.args.mbid), r.URL.Path)
				assert.Equal(t, "json", r.URL.Query().Get("fmt"))
				assert.Contains(t, r.Header.Get("User-Agent"), "LivertyMusic")

				w.WriteHeader(tt.statusCode)
				w.Header().Set("Content-Type", "application/json")

				if tt.invalidJSON {
					_, _ = w.Write([]byte("invalid json{"))
				} else if tt.responseBody != nil {
					_ = json.NewEncoder(w).Encode(tt.responseBody)
				}
			}))
			defer server.Close()

			client := musicbrainz.NewClient(server.Client())
			client.SetBaseURL(server.URL + "/")

			artist, err := client.GetArtist(context.Background(), tt.args.mbid)

			if tt.wantErr != nil {
				assert.Error(t, err)
				assert.ErrorIs(t, err, tt.wantErr)
				assert.Nil(t, artist)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.want.name, artist.Name)
				assert.Equal(t, tt.want.mbid, artist.MBID)
			}
		})
	}
}

func TestClient_GetArtist_ContextTimeout(t *testing.T) {
	t.Run("context cancelled - returns deadline exceeded error", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Wait for context cancellation
			<-r.Context().Done()
		}))
		defer server.Close()

		client := musicbrainz.NewClient(server.Client())
		client.SetBaseURL(server.URL + "/")

		ctx, cancel := context.WithCancel(context.Background())
		cancel() // Cancel immediately

		artist, err := client.GetArtist(ctx, "test-mbid")

		assert.Error(t, err)
		assert.Nil(t, artist)
	})
}
