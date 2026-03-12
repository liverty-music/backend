package musicbrainz_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/liverty-music/backend/internal/infrastructure/music/musicbrainz"
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
type artistResponse struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type placeCoordinates struct {
	Latitude  string `json:"latitude"`
	Longitude string `json:"longitude"`
}

type placeEntry struct {
	ID          string           `json:"id"`
	Name        string           `json:"name"`
	Coordinates placeCoordinates `json:"coordinates"`
}

type placeSearchResponse struct {
	Places []placeEntry `json:"places"`
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
			name:       "error - service unavailable (rate limit 503, retries exhausted)",
			args:       args{mbid: "test-mbid"},
			statusCode: http.StatusServiceUnavailable,
			wantErr:    apperr.New(codes.Unavailable, "musicbrainz api request failed"),
		},
		{
			name:       "error - too many requests (rate limit 429, retries exhausted)",
			args:       args{mbid: "test-mbid"},
			statusCode: http.StatusTooManyRequests,
			wantErr:    apperr.New(codes.Unavailable, "musicbrainz api request failed"),
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

			client := musicbrainz.NewClient(server.Client(), testLogger(t))
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

func TestClient_SearchPlace(t *testing.T) {
	tests := []struct {
		name         string
		venueName    string
		adminArea    string
		statusCode   int
		responseBody interface{}
		wantErr      error
		wantID       string
		wantName     string
		invalidJSON  bool
	}{
		{
			name:       "success - returns top place match",
			venueName:  "Zepp Nagoya",
			adminArea:  "Aichi",
			statusCode: http.StatusOK,
			responseBody: placeSearchResponse{
				Places: []placeEntry{
					{ID: "a2e6e2c0-dead-beef-abcd-000000000001", Name: "Zepp Nagoya"},
				},
			},
			wantID:   "a2e6e2c0-dead-beef-abcd-000000000001",
			wantName: "Zepp Nagoya",
		},
		{
			name:       "success - no admin_area",
			venueName:  "Nippon Budokan",
			adminArea:  "",
			statusCode: http.StatusOK,
			responseBody: placeSearchResponse{
				Places: []placeEntry{
					{ID: "bbbbbbbb-0000-0000-0000-000000000001", Name: "Nippon Budokan"},
				},
			},
			wantID:   "bbbbbbbb-0000-0000-0000-000000000001",
			wantName: "Nippon Budokan",
		},
		{
			name:         "not found - empty places list",
			venueName:    "Unknown Venue",
			adminArea:    "",
			statusCode:   http.StatusOK,
			responseBody: placeSearchResponse{},
			wantErr:      apperr.New(codes.NotFound, "no matching place found in musicbrainz"),
		},
		{
			name:       "error - service unavailable (retries exhausted)",
			venueName:  "Test Venue",
			adminArea:  "",
			statusCode: http.StatusServiceUnavailable,
			wantErr:    apperr.New(codes.Unavailable, "musicbrainz place search failed"),
		},
		{
			name:        "error - invalid JSON",
			venueName:   "Test Venue",
			adminArea:   "",
			statusCode:  http.StatusOK,
			invalidJSON: true,
			wantErr:     apperr.New(codes.DataLoss, "failed to decode musicbrainz place response"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Contains(t, r.Header.Get("User-Agent"), "LivertyMusic")
				assert.Equal(t, "json", r.URL.Query().Get("fmt"))

				w.WriteHeader(tt.statusCode)
				w.Header().Set("Content-Type", "application/json")

				if tt.invalidJSON {
					_, _ = w.Write([]byte("invalid json{"))
				} else if tt.responseBody != nil {
					_ = json.NewEncoder(w).Encode(tt.responseBody)
				}
			}))
			defer server.Close()

			client := musicbrainz.NewClient(server.Client(), testLogger(t))
			client.SetPlaceBaseURL(server.URL + "/")

			place, err := client.SearchPlace(context.Background(), tt.venueName, tt.adminArea)

			if tt.wantErr != nil {
				assert.Error(t, err)
				assert.ErrorIs(t, err, tt.wantErr)
				assert.Nil(t, place)
			} else {
				require.NoError(t, err)
				require.NotNil(t, place)
				assert.Equal(t, tt.wantID, place.ID)
				assert.Equal(t, tt.wantName, place.Name)
			}
		})
	}
}

func TestClient_SearchPlace_Coordinates(t *testing.T) {
	ptrFloat := func(v float64) *float64 { return &v }

	tests := []struct {
		name     string
		response placeSearchResponse
		wantLat  *float64
		wantLng  *float64
	}{
		{
			name: "extracts valid coordinates",
			response: placeSearchResponse{
				Places: []placeEntry{
					{ID: "p1", Name: "Zepp Tokyo", Coordinates: placeCoordinates{Latitude: "35.6250", Longitude: "139.7756"}},
				},
			},
			wantLat: ptrFloat(35.6250),
			wantLng: ptrFloat(139.7756),
		},
		{
			name: "nil coordinates when both strings are empty",
			response: placeSearchResponse{
				Places: []placeEntry{
					{ID: "p2", Name: "Unknown", Coordinates: placeCoordinates{Latitude: "", Longitude: ""}},
				},
			},
			wantLat: nil,
			wantLng: nil,
		},
		{
			name: "nil coordinates when only latitude is present",
			response: placeSearchResponse{
				Places: []placeEntry{
					{ID: "p3", Name: "Partial", Coordinates: placeCoordinates{Latitude: "35.0", Longitude: ""}},
				},
			},
			wantLat: nil,
			wantLng: nil,
		},
		{
			name: "nil coordinates when zero-value struct",
			response: placeSearchResponse{
				Places: []placeEntry{
					{ID: "p4", Name: "NoCoords"},
				},
			},
			wantLat: nil,
			wantLng: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(tt.response)
			}))
			defer server.Close()

			c := musicbrainz.NewClient(server.Client(), testLogger(t))
			c.SetPlaceBaseURL(server.URL + "/")

			place, err := c.SearchPlace(context.Background(), "test", "")

			require.NoError(t, err)
			require.NotNil(t, place)

			if tt.wantLat != nil {
				require.NotNil(t, place.Latitude)
				assert.InDelta(t, *tt.wantLat, *place.Latitude, 0.0001)
			} else {
				assert.Nil(t, place.Latitude)
			}

			if tt.wantLng != nil {
				require.NotNil(t, place.Longitude)
				assert.InDelta(t, *tt.wantLng, *place.Longitude, 0.0001)
			} else {
				assert.Nil(t, place.Longitude)
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

		client := musicbrainz.NewClient(server.Client(), testLogger(t))
		client.SetBaseURL(server.URL + "/")

		ctx, cancel := context.WithCancel(context.Background())
		cancel() // Cancel immediately

		artist, err := client.GetArtist(ctx, "test-mbid")

		assert.Error(t, err)
		assert.Nil(t, artist)
	})
}

func TestClient_GetArtist_RetryOnServiceUnavailable(t *testing.T) {
	t.Run("retries on 503 and succeeds", func(t *testing.T) {
		var calls atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if calls.Add(1) == 1 {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(artistResponse{
				ID:   "a74b1b7f",
				Name: "Radiohead",
			})
		}))
		defer server.Close()

		client := musicbrainz.NewClient(server.Client(), testLogger(t))
		client.SetBaseURL(server.URL + "/")

		artist, err := client.GetArtist(context.Background(), "a74b1b7f")

		require.NoError(t, err)
		assert.Equal(t, "Radiohead", artist.Name)
		assert.Equal(t, int32(2), calls.Load())
	})
}
