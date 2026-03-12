package google_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/liverty-music/backend/internal/infrastructure/maps/google"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPlaceSearcher_SearchPlace_Coordinates(t *testing.T) {
	tests := []struct {
		name       string
		result     placeResult
		wantCoords bool
		wantLat    float64
		wantLng    float64
	}{
		{
			name: "both lat and lng present returns non-nil Coordinates",
			result: placeResult{
				PlaceID:  "ChIJ001",
				Name:     "Zepp Tokyo",
				Geometry: placeGeometry{Location: placeLocation{Lat: 35.6250, Lng: 139.7756}},
			},
			wantCoords: true,
			wantLat:    35.6250,
			wantLng:    139.7756,
		},
		{
			name: "both lat and lng zero returns nil Coordinates",
			result: placeResult{
				PlaceID:  "ChIJ002",
				Name:     "Unknown Place",
				Geometry: placeGeometry{Location: placeLocation{Lat: 0, Lng: 0}},
			},
			wantCoords: false,
		},
		{
			name: "only lat non-zero returns non-nil Coordinates",
			result: placeResult{
				PlaceID:  "ChIJ003",
				Name:     "Equator Venue",
				Geometry: placeGeometry{Location: placeLocation{Lat: 35.0, Lng: 0}},
			},
			wantCoords: true,
			wantLat:    35.0,
			wantLng:    0,
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

			client := google.NewClient(staticTokenSource(), "test-project", server.Client(), testLogger(t))
			client.SetBaseURL(server.URL)
			searcher := google.NewPlaceSearcher(client)

			vp, err := searcher.SearchPlace(context.Background(), "test", "")
			require.NoError(t, err)
			require.NotNil(t, vp)

			assert.Equal(t, tt.result.PlaceID, vp.ExternalID)
			assert.Equal(t, tt.result.Name, vp.Name)

			if tt.wantCoords {
				require.NotNil(t, vp.Coordinates, "expected non-nil Coordinates")
				assert.InDelta(t, tt.wantLat, vp.Coordinates.Latitude, 0.0001)
				assert.InDelta(t, tt.wantLng, vp.Coordinates.Longitude, 0.0001)
			} else {
				assert.Nil(t, vp.Coordinates, "expected nil Coordinates")
			}
		})
	}
}
