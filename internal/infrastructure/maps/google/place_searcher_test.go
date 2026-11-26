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
	t.Parallel()

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
				ID:          "ChIJ001",
				DisplayName: placeDisplayName{Text: "Zepp Tokyo"},
				Location:    placeLocation{Latitude: 35.6250, Longitude: 139.7756},
			},
			wantCoords: true,
			wantLat:    35.6250,
			wantLng:    139.7756,
		},
		{
			name: "both lat and lng zero returns nil Coordinates",
			result: placeResult{
				ID:          "ChIJ002",
				DisplayName: placeDisplayName{Text: "Unknown Place"},
				Location:    placeLocation{Latitude: 0, Longitude: 0},
			},
			wantCoords: false,
		},
		{
			name: "only lat non-zero returns non-nil Coordinates",
			result: placeResult{
				ID:          "ChIJ003",
				DisplayName: placeDisplayName{Text: "Equator Venue"},
				Location:    placeLocation{Latitude: 35.0, Longitude: 0},
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
					Places: []placeResult{tt.result},
				})
			}))
			defer server.Close()

			client := google.NewClient(staticTokenSource(), "test-project", server.Client(), nil, testLogger(t))
			client.SetBaseURL(server.URL)
			searcher := google.NewPlaceSearcher(client)

			vps, err := searcher.SearchPlace(context.Background(), "test", "")
			require.NoError(t, err)
			require.Len(t, vps, 1)
			vp := vps[0]

			assert.Equal(t, tt.result.ID, vp.ProviderUserID)
			assert.Equal(t, tt.result.DisplayName.Text, vp.Name)

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

func TestPlaceSearcher_SearchPlace_Area(t *testing.T) {
	t.Parallel()

	areas := resolvingAreas(t, map[string]map[string]string{"US": {"California": "US-CA"}, "HK": {"HK": "HK"}})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Contains(t, r.Header.Get("X-Goog-FieldMask"), "places.addressComponents")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"places":[
			{"id":"sf","displayName":{"text":"The Independent"},"addressComponents":[
				{"longText":"California","shortText":"CA","types":["administrative_area_level_1"]},
				{"longText":"United States","shortText":"US","types":["country"]}]},
			{"id":"hk","displayName":{"text":"The Independent"},"addressComponents":[
				{"longText":"Hong Kong","shortText":"HK","types":["country"]}]},
			{"id":"nowhere","displayName":{"text":"The Independent"}}]}`))
	}))
	defer server.Close()

	client := google.NewClient(staticTokenSource(), "test-project", server.Client(), areas, testLogger(t))
	client.SetBaseURL(server.URL)

	// @spec components/entity/venue/search-place "Country and area of each place"
	places, err := google.NewPlaceSearcher(client).SearchPlace(context.Background(), "The Independent", "US-CA")

	require.NoError(t, err)
	require.Len(t, places, 3)
	assert.Equal(t, "US", places[0].CountryCode)
	require.NotNil(t, places[0].AdminArea)
	assert.Equal(t, "US-CA", *places[0].AdminArea)
	// @spec components/entity/venue/search-place "Territory without subdivisions"
	assert.Equal(t, "HK", places[1].CountryCode)
	require.NotNil(t, places[1].AdminArea)
	assert.Equal(t, "HK", *places[1].AdminArea)
	// @spec components/entity/venue/search-place "Address without a country"
	assert.Empty(t, places[2].CountryCode)
	assert.Nil(t, places[2].AdminArea)
}
