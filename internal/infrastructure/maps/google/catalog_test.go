package google_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/maps/google"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The testdata files are responses recorded from the Places API (New) on
// 2026-10-11 with the same field masks and language the client sends:
//   - search_text_zepp_haneda.json: places:searchText "Zepp Haneda"
//   - search_text_no_match.json: places:searchText for a text with no match
//   - place_details_zepp_haneda.json: GET places/ChIJeVmAOuphGGARoWY4-GPmhhA
//   - place_details_invalid_id.json: GET places/ChIJAAAAAAAAAAAAAAAAAAAAAAA (HTTP 400)

const zeppHanedaPlaceID = "ChIJeVmAOuphGGARoWY4-GPmhhA"

// recordedResponse reads a recorded catalog response from testdata.
func recordedResponse(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	require.NoError(t, err)
	return b
}

// catalogServer serves a fixed status and body and hands each request to
// inspect, which runs on the server goroutine.
func catalogServer(t *testing.T, status int, body []byte, inspect func(r *http.Request, reqBody []byte)) *google.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqBody, _ := io.ReadAll(r.Body)
		if inspect != nil {
			inspect(r, reqBody)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	c := google.NewClient(staticTokenSource(), "test-project", srv.Client(), testLogger(t))
	c.SetBaseURL(srv.URL + "/v1/places:searchText")
	c.SetDetailsBaseURL(srv.URL + "/v1/places")
	return c
}

// unreachableClient points at a server that is already closed, so every
// request fails to connect.
func unreachableClient(t *testing.T) *google.Client {
	t.Helper()
	srv := httptest.NewServer(http.NotFoundHandler())
	srv.Close()
	c := google.NewClient(staticTokenSource(), "test-project", http.DefaultClient, testLogger(t))
	c.SetBaseURL(srv.URL + "/v1/places:searchText")
	c.SetDetailsBaseURL(srv.URL + "/v1/places")
	return c
}

func TestClient_SearchPlaces(t *testing.T) {
	t.Parallel()

	// @spec components/entity/venue/search-places "Known venue"
	t.Run("return Zepp Haneda first with its place id and Japanese address", func(t *testing.T) {
		t.Parallel()
		var gotPath, gotMask string
		var gotBody map[string]any
		c := catalogServer(t, http.StatusOK, recordedResponse(t, "search_text_zepp_haneda.json"), func(r *http.Request, body []byte) {
			gotPath = r.URL.Path
			gotMask = r.Header.Get("X-Goog-FieldMask")
			_ = json.Unmarshal(body, &gotBody)
		})

		got, err := c.SearchPlaces(context.Background(), "Zepp Haneda")

		require.NoError(t, err)
		require.NotEmpty(t, got)
		assert.LessOrEqual(t, len(got), 5)
		assert.Equal(t, &entity.MapPlaceCandidate{
			PlaceID: zeppHanedaPlaceID,
			Name:    "Zepp Haneda (TOKYO)",
			Address: "〒144-0041 東京都大田区羽田空港１丁目１−４",
		}, got[0])
		assert.Equal(t, "/v1/places:searchText", gotPath)
		assert.Equal(t, "places.id,places.displayName,places.formattedAddress", gotMask)
		assert.Equal(t, map[string]any{
			"textQuery":    "Zepp Haneda",
			"languageCode": "ja",
			"regionCode":   "JP",
			"pageSize":     float64(5),
		}, gotBody)
	})

	// @spec components/entity/venue/search-places "No match"
	t.Run("return an empty list when the catalog has no match", func(t *testing.T) {
		t.Parallel()
		c := catalogServer(t, http.StatusOK, recordedResponse(t, "search_text_no_match.json"), nil)

		got, err := c.SearchPlaces(context.Background(), "xqzvwplkjh qqqzzzxxyy")

		require.NoError(t, err)
		assert.Empty(t, got)
	})

	// @spec components/entity/venue/search-places "Catalog down"
	t.Run("fail with UNAVAILABLE when the catalog cannot be reached", func(t *testing.T) {
		t.Parallel()
		tests := []struct {
			name   string
			client func(t *testing.T) *google.Client
		}{
			{"connection refused", unreachableClient},
			{"server error", func(t *testing.T) *google.Client {
				t.Helper()
				return catalogServer(t, http.StatusInternalServerError, []byte(`{}`), nil)
			}},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				got, err := tt.client(t).SearchPlaces(context.Background(), "Zepp Haneda")

				assert.ErrorIs(t, err, apperr.ErrUnavailable)
				assert.Nil(t, got)
			})
		}
	})
}

func TestClient_GetPlace(t *testing.T) {
	t.Parallel()

	// @spec components/entity/venue/get-place "Known place"
	t.Run("return the name, coordinates and admin area JP-13 of Zepp Haneda", func(t *testing.T) {
		t.Parallel()
		var gotPath, gotLang, gotMask string
		c := catalogServer(t, http.StatusOK, recordedResponse(t, "place_details_zepp_haneda.json"), func(r *http.Request, _ []byte) {
			gotPath = r.URL.Path
			gotLang = r.URL.Query().Get("languageCode")
			gotMask = r.Header.Get("X-Goog-FieldMask")
		})

		got, err := c.GetPlace(context.Background(), zeppHanedaPlaceID)

		require.NoError(t, err)
		assert.Equal(t, &entity.MapPlace{
			PlaceID:     zeppHanedaPlaceID,
			Name:        "Zepp Haneda (TOKYO)",
			Coordinates: &entity.Coordinates{Latitude: 35.5473958, Longitude: 139.7565213},
			AdminArea:   new("JP-13"),
		}, got)
		assert.Equal(t, "/v1/places/"+zeppHanedaPlaceID, gotPath)
		assert.Equal(t, "ja", gotLang)
		assert.Equal(t, "id,displayName,location,addressComponents", gotMask)
	})

	// @spec components/entity/venue/get-place "Unknown place id"
	t.Run("fail with NOT_FOUND when the catalog has no place with the id", func(t *testing.T) {
		t.Parallel()
		tests := []struct {
			name   string
			status int
			body   []byte
		}{
			{"recorded invalid place id answer", http.StatusBadRequest, recordedResponse(t, "place_details_invalid_id.json")},
			{"obsolete place id", http.StatusNotFound, []byte(`{"error":{"code":404,"status":"NOT_FOUND"}}`)},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				c := catalogServer(t, tt.status, tt.body, nil)

				got, err := c.GetPlace(context.Background(), "ChIJAAAAAAAAAAAAAAAAAAAAAAA")

				assert.ErrorIs(t, err, apperr.ErrNotFound)
				assert.Nil(t, got)
			})
		}
	})

	// @spec components/entity/venue/get-place "Catalog down"
	t.Run("fail with UNAVAILABLE when the catalog cannot be reached", func(t *testing.T) {
		t.Parallel()
		tests := []struct {
			name   string
			client func(t *testing.T) *google.Client
		}{
			{"connection refused", unreachableClient},
			{"server error", func(t *testing.T) *google.Client {
				t.Helper()
				return catalogServer(t, http.StatusInternalServerError, []byte(`{}`), nil)
			}},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				got, err := tt.client(t).GetPlace(context.Background(), zeppHanedaPlaceID)

				assert.ErrorIs(t, err, apperr.ErrUnavailable)
				assert.Nil(t, got)
			})
		}
	})
}
