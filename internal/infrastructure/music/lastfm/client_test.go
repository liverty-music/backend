package lastfm_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/music/lastfm"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testLogger(t *testing.T) *logging.Logger {
	t.Helper()
	l, _ := logging.New()
	return l
}

// Local types mirroring the unexported response types in the package under test.
// These are used so black-box tests can construct expected JSON responses.
type artist struct {
	Name string `json:"name"`
	MBID string `json:"mbid"`
}

type artistSearchResponse struct {
	Results struct {
		ArtistMatches struct {
			Artist []artist `json:"artist"`
		} `json:"artistmatches"`
	} `json:"results"`
}

type similarArtistsResponse struct {
	SimilarArtists struct {
		Artist []artist `json:"artist"`
	} `json:"similarartists"`
}

type topArtistsResponse struct {
	TopArtists struct {
		Artist []artist `json:"artist"`
	} `json:"topartists"`
}

func newArtistSearchResponse(artists []artist) artistSearchResponse {
	resp := artistSearchResponse{}
	resp.Results.ArtistMatches.Artist = artists
	return resp
}

func newSimilarArtistsResponse(artists []artist) similarArtistsResponse {
	return similarArtistsResponse{
		SimilarArtists: struct {
			Artist []artist `json:"artist"`
		}{
			Artist: artists,
		},
	}
}

func newTopArtistsResponse(artists []artist) topArtistsResponse {
	return topArtistsResponse{
		TopArtists: struct {
			Artist []artist `json:"artist"`
		}{
			Artist: artists,
		},
	}
}

func TestClient_Search(t *testing.T) {
	t.Parallel()

	type args struct {
		query string
	}
	type want struct {
		len          int
		expectedName string
		expectedMBID string
	}
	tests := []struct {
		name         string
		args         args
		statusCode   int
		responseBody any
		wantErr      error
		want         want
		invalidJSON  bool
	}{
		{
			name:       "success - returns artists",
			args:       args{query: "The Beatles"},
			statusCode: http.StatusOK,
			responseBody: newArtistSearchResponse([]artist{
				{Name: "The Beatles", MBID: "074612c4-8380-4e4e-81c2-f0b61dfc0d56"},
				{Name: "Beatles", MBID: "5f4e8f1e-1234-5678-abcd-ef1234567890"},
			}),
			wantErr: nil,
			want: want{
				len:          2,
				expectedName: "The Beatles",
				expectedMBID: "074612c4-8380-4e4e-81c2-f0b61dfc0d56",
			},
		},
		{
			name:         "success - no results",
			args:         args{query: "NonexistentArtist"},
			statusCode:   http.StatusOK,
			responseBody: newArtistSearchResponse(nil),
			wantErr:      nil,
			want:         want{len: 0},
		},
		{
			name:       "error - server returns 500",
			args:       args{query: "Test"},
			statusCode: http.StatusInternalServerError,
			wantErr:    apperr.New(codes.Unavailable, "lastfm api returned non-ok status: 500"),
		},
		{
			name:        "error - invalid JSON response",
			args:        args{query: "Test"},
			statusCode:  http.StatusOK,
			invalidJSON: true,
			wantErr:     apperr.New(codes.Internal, "failed to decode lastfm response"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, "artist.search", r.URL.Query().Get("method"))
				assert.Equal(t, tt.args.query, r.URL.Query().Get("artist"))

				w.WriteHeader(tt.statusCode)
				w.Header().Set("Content-Type", "application/json")

				if tt.invalidJSON {
					_, _ = w.Write([]byte("invalid json{"))
				} else {
					_ = json.NewEncoder(w).Encode(tt.responseBody)
				}
			}))
			defer server.Close()

			c := lastfm.NewClient("test-key", server.Client(), testLogger(t))
			c.SetBaseURL(server.URL + "/")

			artists, err := c.Search(context.Background(), tt.args.query)

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				assert.Nil(t, artists)
			} else {
				require.NoError(t, err)
				require.Len(t, artists, tt.want.len)
				if tt.want.len > 0 {
					assert.Equal(t, tt.want.expectedName, artists[0].Name)
					assert.Equal(t, tt.want.expectedMBID, artists[0].MBID)
				}
			}
		})
	}
}

func TestClient_ListSimilar(t *testing.T) {
	t.Parallel()

	type args struct {
		artist *entity.Artist
	}
	type want struct {
		len          int
		expectedName string
	}
	tests := []struct {
		name         string
		args         args
		statusCode   int
		responseBody any
		wantErr      error
		want         want
	}{
		{
			name: "success - returns similar artists by listening mbid",
			args: args{
				artist: &entity.Artist{MBID: "test-mbid-123", ListeningMBID: "test-mbid-123", Name: "Test Artist"},
			},
			statusCode: http.StatusOK,
			responseBody: newSimilarArtistsResponse([]artist{
				{Name: "Artist 1", MBID: "mbid-1"},
				{Name: "Artist 2", MBID: "mbid-2"},
			}),
			wantErr: nil,
			want: want{
				len:          2,
				expectedName: "Artist 1",
			},
		},
		{
			// @spec components/entity/artist/list-similar "Corrected artist"
			name: "success - a corrected artist is looked up by its listening mbid",
			args: args{
				artist: &entity.Artist{MBID: "corrected-mbid-b", ListeningMBID: "listening-mbid-a", Name: "sumika"},
			},
			statusCode: http.StatusOK,
			responseBody: newSimilarArtistsResponse([]artist{
				{Name: "Similar Artist", MBID: "similar-mbid"},
			}),
			want: want{
				len:          1,
				expectedName: "Similar Artist",
			},
		},
		{
			name: "success - uses artist name as fallback",
			args: args{
				artist: &entity.Artist{Name: "Test Band"},
			},
			statusCode: http.StatusOK,
			responseBody: newSimilarArtistsResponse([]artist{
				{Name: "Similar Artist", MBID: "similar-mbid"},
			}),
			wantErr: nil,
			want: want{
				len:          1,
				expectedName: "Similar Artist",
			},
		},
		{
			name: "error - server returns 404",
			args: args{
				artist: &entity.Artist{Name: "unknown"},
			},
			statusCode: http.StatusNotFound,
			wantErr:    apperr.New(codes.NotFound, "lastfm api returned non-ok status: 404"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, "artist.getsimilar", r.URL.Query().Get("method"))
				assert.NotEmpty(t, r.Header.Get("User-Agent"))
				if tt.args.artist.ListeningMBID != "" {
					assert.Equal(t, tt.args.artist.ListeningMBID, r.URL.Query().Get("mbid"))
				} else {
					assert.Equal(t, tt.args.artist.Name, r.URL.Query().Get("artist"))
					assert.Empty(t, r.URL.Query().Get("mbid"))
				}

				w.WriteHeader(tt.statusCode)
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(tt.responseBody)
			}))
			defer server.Close()

			client := lastfm.NewClient("test-key", server.Client(), testLogger(t))
			client.SetBaseURL(server.URL + "/")

			artists, err := client.ListSimilar(context.Background(), tt.args.artist, int32(0))

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				assert.Nil(t, artists)
			} else {
				require.NoError(t, err)
				require.Len(t, artists, tt.want.len)
				if tt.want.len > 0 {
					assert.Equal(t, tt.want.expectedName, artists[0].Name)
				}
			}
		})
	}
}

func TestClient_ListTop(t *testing.T) {
	t.Parallel()

	type args struct {
		country string
		tag     string
	}
	type want struct {
		len          int
		expectedName string
	}
	tests := []struct {
		name           string
		args           args
		statusCode     int
		responseBody   any
		wantErr        error
		want           want
		expectedMethod string
	}{
		{
			name:       "success - returns top artists by country",
			args:       args{country: "JP"},
			statusCode: http.StatusOK,
			responseBody: newTopArtistsResponse([]artist{
				{Name: "Top Artist JP 1", MBID: "jp-1"},
				{Name: "Top Artist JP 2", MBID: "jp-2"},
			}),
			wantErr: nil,
			want: want{
				len:          2,
				expectedName: "Top Artist JP 1",
			},
			expectedMethod: "geo.gettopartists",
		},
		{
			name:       "success - returns top artists by tag",
			args:       args{country: "JP", tag: "rock"},
			statusCode: http.StatusOK,
			responseBody: newTopArtistsResponse([]artist{
				{Name: "Rock Artist 1", MBID: "rock-1"},
			}),
			wantErr: nil,
			want: want{
				len:          1,
				expectedName: "Rock Artist 1",
			},
			expectedMethod: "tag.gettopartists",
		},
		{
			name:       "success - returns global top artists when no country",
			args:       args{country: ""},
			statusCode: http.StatusOK,
			responseBody: newTopArtistsResponse([]artist{
				{Name: "Global Top Artist", MBID: "global-1"},
			}),
			wantErr: nil,
			want: want{
				len:          1,
				expectedName: "Global Top Artist",
			},
			expectedMethod: "chart.gettopartists",
		},
		{
			name:           "error - server returns 500",
			args:           args{country: "US"},
			statusCode:     http.StatusInternalServerError,
			wantErr:        apperr.New(codes.Unavailable, "lastfm api returned non-ok status: 500"),
			expectedMethod: "geo.gettopartists",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, tt.expectedMethod, r.URL.Query().Get("method"))
				if tt.args.tag != "" {
					assert.Equal(t, tt.args.tag, r.URL.Query().Get("tag"))
					assert.Empty(t, r.URL.Query().Get("country"), "country must be absent when tag is provided")
				} else if tt.args.country != "" {
					assert.Equal(t, tt.args.country, r.URL.Query().Get("country"))
				} else {
					assert.Empty(t, r.URL.Query().Get("country"))
				}

				w.WriteHeader(tt.statusCode)
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(tt.responseBody)
			}))
			defer server.Close()

			client := lastfm.NewClient("test-key", server.Client(), testLogger(t))
			client.SetBaseURL(server.URL + "/")

			artists, err := client.ListTop(context.Background(), tt.args.country, tt.args.tag, int32(0))

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				assert.Nil(t, artists)
			} else {
				require.NoError(t, err)
				require.Len(t, artists, tt.want.len)
				if tt.want.len > 0 {
					assert.Equal(t, tt.want.expectedName, artists[0].Name)
				}
			}
		})
	}
}

func TestClient_Search_ContextEnd(t *testing.T) {
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

			client := lastfm.NewClient("test-key", server.Client(), testLogger(t))
			client.SetBaseURL(server.URL + "/")

			got, err := client.Search(tt.newCtx(t), "Test")

			assert.Nil(t, got)
			assert.ErrorIs(t, err, tt.wantErr)
		})
	}
}

func TestClient_RetryOnRateLimit(t *testing.T) {
	t.Parallel()

	t.Run("retries on 429 and succeeds", func(t *testing.T) {
		var calls atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if calls.Add(1) == 1 {
				w.WriteHeader(http.StatusTooManyRequests)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(newArtistSearchResponse([]artist{
				{Name: "The Beatles", MBID: "074612c4"},
			}))
		}))
		defer server.Close()

		client := lastfm.NewClient("test-key", server.Client(), testLogger(t))
		client.SetBaseURL(server.URL + "/")

		artists, err := client.Search(context.Background(), "The Beatles")

		require.NoError(t, err)
		require.Len(t, artists, 1)
		assert.Equal(t, "The Beatles", artists[0].Name)
		assert.Equal(t, int32(2), calls.Load())
	})
}

// TestClient_ListSimilar_Limit checks how ListSimilar passes its limit to the
// listening service and what it returns when the service lists nobody.
func TestClient_ListSimilar_Limit(t *testing.T) {
	t.Parallel()

	many := make([]artist, 10)
	for i := range many {
		many[i] = artist{Name: fmt.Sprintf("Artist %d", i), MBID: fmt.Sprintf("mbid-%d", i)}
	}
	tests := []struct {
		name      string
		limit     int32
		wantParam string
		listed    []artist
		wantLen   int
	}{
		// @spec components/entity/artist/list-similar "Limit given"
		{name: "limit given", limit: 10, wantParam: "10", listed: many, wantLen: 10},
		// @spec components/entity/artist/list-similar "No limit"
		{name: "no limit", limit: 0, wantParam: "", listed: many, wantLen: 10},
		// @spec components/entity/artist/list-similar "No similar artists"
		{name: "no similar artists", limit: 10, wantParam: "10", listed: []artist{}, wantLen: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, tt.wantParam, r.URL.Query().Get("limit"), "the limit is forwarded only when greater than 0")
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(newSimilarArtistsResponse(tt.listed))
			}))
			t.Cleanup(server.Close)
			client := lastfm.NewClient("test-key", server.Client(), testLogger(t))
			client.SetBaseURL(server.URL + "/")

			got, err := client.ListSimilar(context.Background(),
				&entity.Artist{Name: "Ado", MBID: "e134b52f-1e3b-4a0e-8bd2-0b2a2c4d3e7f", ListeningMBID: "ca195d97-30d9-4870-8a52-7fd3c2e175c3"}, tt.limit)

			require.NoError(t, err)
			assert.Len(t, got, tt.wantLen)
		})
	}
}
