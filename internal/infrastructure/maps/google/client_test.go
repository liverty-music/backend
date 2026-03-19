package google_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"golang.org/x/oauth2"

	"github.com/liverty-music/backend/internal/infrastructure/maps/google"
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

// staticTokenSource returns a fixed token for testing.
func staticTokenSource() oauth2.TokenSource {
	return oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "test-token"})
}

type placeLocation struct {
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
}

type placeDisplayName struct {
	Text string `json:"text"`
}

type placeResult struct {
	ID          string           `json:"id"`
	DisplayName placeDisplayName `json:"displayName"`
	Location    placeLocation    `json:"location"`
}

type textSearchResponse struct {
	Places []placeResult `json:"places"`
}

func TestClient_SearchPlace(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		venueName    string
		adminArea    string
		statusCode   int
		responseBody interface{}
		wantErr      error
		wantPlaceID  string
		wantName     string
		invalidJSON  bool
	}{
		{
			name:       "success - returns top place match",
			venueName:  "Zepp Nagoya",
			adminArea:  "Aichi",
			statusCode: http.StatusOK,
			responseBody: textSearchResponse{
				Places: []placeResult{
					{ID: "ChIJexamplePlaceID001", DisplayName: placeDisplayName{Text: "Zepp Nagoya"}},
				},
			},
			wantPlaceID: "ChIJexamplePlaceID001",
			wantName:    "Zepp Nagoya",
		},
		{
			name:       "success - no admin_area",
			venueName:  "Nippon Budokan",
			adminArea:  "",
			statusCode: http.StatusOK,
			responseBody: textSearchResponse{
				Places: []placeResult{
					{ID: "ChIJexamplePlaceID002", DisplayName: placeDisplayName{Text: "Nippon Budokan"}},
				},
			},
			wantPlaceID: "ChIJexamplePlaceID002",
			wantName:    "Nippon Budokan",
		},
		{
			name:         "not found - empty places array",
			venueName:    "Unknown Venue",
			adminArea:    "",
			statusCode:   http.StatusOK,
			responseBody: textSearchResponse{},
			wantErr:      apperr.New(codes.NotFound, "no matching place found in google maps"),
		},
		{
			name:       "error - HTTP 500",
			venueName:  "Test Venue",
			adminArea:  "",
			statusCode: http.StatusInternalServerError,
			wantErr:    apperr.New(codes.Unavailable, "google maps places search failed"),
		},
		{
			name:        "error - invalid JSON",
			venueName:   "Test Venue",
			adminArea:   "",
			statusCode:  http.StatusOK,
			invalidJSON: true,
			wantErr:     apperr.New(codes.Internal, "failed to decode google maps response"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, "Bearer test-token", r.Header.Get("Authorization"))
				assert.Equal(t, "test-project", r.Header.Get("X-Goog-User-Project"))

				w.WriteHeader(tt.statusCode)
				w.Header().Set("Content-Type", "application/json")

				if tt.invalidJSON {
					_, _ = w.Write([]byte("invalid json{"))
				} else if tt.responseBody != nil {
					_ = json.NewEncoder(w).Encode(tt.responseBody)
				}
			}))
			defer server.Close()

			client := google.NewClient(staticTokenSource(), "test-project", server.Client(), testLogger(t))
			client.SetBaseURL(server.URL)

			place, err := client.SearchPlace(context.Background(), tt.venueName, tt.adminArea)

			if tt.wantErr != nil {
				assert.Error(t, err)
				assert.ErrorIs(t, err, tt.wantErr)
				assert.Nil(t, place)
			} else {
				require.NoError(t, err)
				require.NotNil(t, place)
				assert.Equal(t, tt.wantPlaceID, place.PlaceID)
				assert.Equal(t, tt.wantName, place.Name)
			}
		})
	}
}

func TestClient_SearchPlace_Coordinates(t *testing.T) {
	t.Parallel()

	ptrFloat := func(v float64) *float64 { return &v }

	tests := []struct {
		name    string
		result  placeResult
		wantLat *float64
		wantLng *float64
	}{
		{
			name: "extracts valid coordinates",
			result: placeResult{
				ID:          "p1",
				DisplayName: placeDisplayName{Text: "Zepp Tokyo"},
				Location:    placeLocation{Latitude: 35.6250, Longitude: 139.7756},
			},
			wantLat: ptrFloat(35.6250),
			wantLng: ptrFloat(139.7756),
		},
		{
			name: "nil coordinates when both lat and lng are zero",
			result: placeResult{
				ID:          "p2",
				DisplayName: placeDisplayName{Text: "Unknown"},
				Location:    placeLocation{Latitude: 0, Longitude: 0},
			},
			wantLat: nil,
			wantLng: nil,
		},
		{
			name: "sets coordinates when only latitude is non-zero",
			result: placeResult{
				ID:          "p3",
				DisplayName: placeDisplayName{Text: "Equator Venue"},
				Location:    placeLocation{Latitude: 35.0, Longitude: 0},
			},
			wantLat: ptrFloat(35.0),
			wantLng: ptrFloat(0),
		},
		{
			name: "sets coordinates when only longitude is non-zero",
			result: placeResult{
				ID:          "p4",
				DisplayName: placeDisplayName{Text: "Meridian Venue"},
				Location:    placeLocation{Latitude: 0, Longitude: 139.0},
			},
			wantLat: ptrFloat(0),
			wantLng: ptrFloat(139.0),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(textSearchResponse{
					Places: []placeResult{tt.result},
				})
			}))
			defer server.Close()

			c := google.NewClient(staticTokenSource(), "test-project", server.Client(), testLogger(t))
			c.SetBaseURL(server.URL)

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
