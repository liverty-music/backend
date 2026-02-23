package google_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

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

type textSearchResponse struct {
	Results []struct {
		PlaceID string `json:"place_id"`
		Name    string `json:"name"`
	} `json:"results"`
	Status string `json:"status"`
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
				Results: []struct {
					PlaceID string `json:"place_id"`
					Name    string `json:"name"`
				}{
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
				Results: []struct {
					PlaceID string `json:"place_id"`
					Name    string `json:"name"`
				}{
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
				assert.Equal(t, "test-api-key", r.URL.Query().Get("key"))

				w.WriteHeader(tt.statusCode)
				w.Header().Set("Content-Type", "application/json")

				if tt.invalidJSON {
					_, _ = w.Write([]byte("invalid json{"))
				} else if tt.responseBody != nil {
					_ = json.NewEncoder(w).Encode(tt.responseBody)
				}
			}))
			defer server.Close()

			client := google.NewClient("test-api-key", server.Client(), testLogger(t))
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
