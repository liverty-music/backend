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
	Lat float64 `json:"lat"`
	Lng float64 `json:"lng"`
}

type placeGeometry struct {
	Location placeLocation `json:"location"`
}

type placeResult struct {
	PlaceID  string        `json:"place_id"`
	Name     string        `json:"name"`
	Geometry placeGeometry `json:"geometry"`
}

type textSearchResponse struct {
	Results []placeResult `json:"results"`
	Status  string        `json:"status"`
}

func TestClient_SearchPlace(t *testing.T) {
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
				Status: "OK",
				Results: []placeResult{
					{PlaceID: "ChIJexamplePlaceID001", Name: "Zepp Nagoya"},
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
				Status: "OK",
				Results: []placeResult{
					{PlaceID: "ChIJexamplePlaceID002", Name: "Nippon Budokan"},
				},
			},
			wantPlaceID: "ChIJexamplePlaceID002",
			wantName:    "Nippon Budokan",
		},
		{
			name:       "not found - ZERO_RESULTS status",
			venueName:  "Unknown Venue",
			adminArea:  "",
			statusCode: http.StatusOK,
			responseBody: textSearchResponse{
				Status: "ZERO_RESULTS",
			},
			wantErr: apperr.New(codes.NotFound, "no matching place found in google maps"),
		},
		{
			name:       "error - application-level error status",
			venueName:  "Test Venue",
			adminArea:  "",
			statusCode: http.StatusOK,
			responseBody: textSearchResponse{
				Status: "REQUEST_DENIED",
			},
			wantErr: apperr.New(codes.Unavailable, "google maps api returned status: REQUEST_DENIED"),
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
			wantErr:     apperr.New(codes.DataLoss, "failed to decode google maps response"),
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
				PlaceID:  "p1",
				Name:     "Zepp Tokyo",
				Geometry: placeGeometry{Location: placeLocation{Lat: 35.6250, Lng: 139.7756}},
			},
			wantLat: ptrFloat(35.6250),
			wantLng: ptrFloat(139.7756),
		},
		{
			name: "nil coordinates when both lat and lng are zero",
			result: placeResult{
				PlaceID:  "p2",
				Name:     "Unknown",
				Geometry: placeGeometry{Location: placeLocation{Lat: 0, Lng: 0}},
			},
			wantLat: nil,
			wantLng: nil,
		},
		{
			name: "sets coordinates when only latitude is non-zero",
			result: placeResult{
				PlaceID:  "p3",
				Name:     "Equator Venue",
				Geometry: placeGeometry{Location: placeLocation{Lat: 35.0, Lng: 0}},
			},
			wantLat: ptrFloat(35.0),
			wantLng: ptrFloat(0),
		},
		{
			name: "sets coordinates when only longitude is non-zero",
			result: placeResult{
				PlaceID:  "p4",
				Name:     "Meridian Venue",
				Geometry: placeGeometry{Location: placeLocation{Lat: 0, Lng: 139.0}},
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
					Status:  "OK",
					Results: []placeResult{tt.result},
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
