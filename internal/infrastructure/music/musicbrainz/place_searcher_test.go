package musicbrainz_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/liverty-music/backend/internal/infrastructure/music/musicbrainz"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPlaceSearcher_SearchPlace_Coordinates(t *testing.T) {
	tests := []struct {
		name       string
		response   placeSearchResponse
		wantCoords bool
		wantLat    float64
		wantLng    float64
	}{
		{
			name: "both lat and lng present returns non-nil Coordinates",
			response: placeSearchResponse{
				Places: []placeEntry{
					{ID: "p1", Name: "Zepp Tokyo", Coordinates: placeCoordinates{Latitude: "35.6250", Longitude: "139.7756"}},
				},
			},
			wantCoords: true,
			wantLat:    35.6250,
			wantLng:    139.7756,
		},
		{
			name: "both empty strings returns nil Coordinates",
			response: placeSearchResponse{
				Places: []placeEntry{
					{ID: "p2", Name: "Unknown", Coordinates: placeCoordinates{Latitude: "", Longitude: ""}},
				},
			},
			wantCoords: false,
		},
		{
			name: "only latitude present returns nil Coordinates",
			response: placeSearchResponse{
				Places: []placeEntry{
					{ID: "p3", Name: "Partial", Coordinates: placeCoordinates{Latitude: "35.0", Longitude: ""}},
				},
			},
			wantCoords: false,
		},
		{
			name: "zero-value coordinates struct returns nil Coordinates",
			response: placeSearchResponse{
				Places: []placeEntry{
					{ID: "p4", Name: "NoCoords"},
				},
			},
			wantCoords: false,
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
			searcher := musicbrainz.NewPlaceSearcher(c)

			vp, err := searcher.SearchPlace(context.Background(), "test", "")
			require.NoError(t, err)
			require.NotNil(t, vp)

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
