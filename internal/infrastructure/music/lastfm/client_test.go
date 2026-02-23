package lastfm_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

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
		responseBody interface{}
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
			wantErr:     apperr.New(codes.DataLoss, "failed to decode lastfm response"),
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
				assert.Error(t, err)
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
		responseBody interface{}
		wantErr      error
		want         want
	}{
		{
			name: "success - returns similar artists by mbid",
			args: args{
				artist: &entity.Artist{MBID: "test-mbid-123", Name: "Test Artist"},
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
				if tt.args.artist.MBID != "" {
					assert.Equal(t, tt.args.artist.MBID, r.URL.Query().Get("mbid"))
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

			artists, err := client.ListSimilar(context.Background(), tt.args.artist)

			if tt.wantErr != nil {
				assert.Error(t, err)
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
		responseBody   interface{}
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

			artists, err := client.ListTop(context.Background(), tt.args.country, tt.args.tag)

			if tt.wantErr != nil {
				assert.Error(t, err)
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

func TestClient_ContextTimeout(t *testing.T) {
	t.Run("context cancelled - returns deadline exceeded error", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Simulate slow response
			<-r.Context().Done()
		}))
		defer server.Close()

		client := lastfm.NewClient("test-key", server.Client(), testLogger(t))
		client.SetBaseURL(server.URL + "/")

		ctx, cancel := context.WithCancel(context.Background())
		cancel() // Cancel immediately

		artists, err := client.Search(ctx, "Test")

		assert.Error(t, err)
		assert.Nil(t, artists)
	})
}
