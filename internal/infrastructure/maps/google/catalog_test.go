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
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/entity/mocks"
	"github.com/liverty-music/backend/internal/infrastructure/maps/google"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// The testdata files are responses recorded from the Places API (New) with
// the same field masks, language and region the client sends:
//   - autocomplete_zepp_haneda.json: places:autocomplete "Zepp Haneda" (2026-10-13)
//   - autocomplete_liquidro.json: places:autocomplete "LIQUIDRO" (2026-10-13)
//   - autocomplete_no_match.json: places:autocomplete for a text with no match (2026-10-13)
// on 2026-10-11:
//   - place_details_invalid_id.json: GET places/ChIJAAAAAAAAAAAAAAAAAAAAAAA (HTTP 400)
// and on 2026-11-25, with the time zone in the field mask:
//   - place_details_zepp_haneda.json: GET places/ChIJeVmAOuphGGARoWY4-GPmhhA
//   - place_details_fillmore.json: GET places/ChIJO2EGsrmAhYARdDfDm-uVTQQ (San Francisco)
//   - place_details_asiaworld_expo.json: GET places/ChIJh5kkFNIGBDQRR8StxWm9pRs (Hong Kong)

const (
	zeppHanedaPlaceID = "ChIJeVmAOuphGGARoWY4-GPmhhA"
	liquidroomPlaceID = "ChIJxTAYVt3gGGARc6fo_RMKO5I"
)

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
	return catalogServerWithAreas(t, nil, status, body, inspect)
}

// catalogServerWithAreas is catalogServer with the Areas GetPlace resolves
// the place's region against.
func catalogServerWithAreas(t *testing.T, areas entity.AreaRepository, status int, body []byte, inspect func(r *http.Request, reqBody []byte)) *google.Client {
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
	c := google.NewClient(staticTokenSource(), "test-project", srv.Client(), areas, testLogger(t))
	c.SetAutocompleteURL(srv.URL + "/v1/places:autocomplete")
	c.SetDetailsBaseURL(srv.URL + "/v1/places")
	return c
}

// unreachableClient points at a server that is already closed, so every
// request fails to connect.
func unreachableClient(t *testing.T) *google.Client {
	t.Helper()
	srv := httptest.NewServer(http.NotFoundHandler())
	srv.Close()
	c := google.NewClient(staticTokenSource(), "test-project", http.DefaultClient, nil, testLogger(t))
	c.SetAutocompleteURL(srv.URL + "/v1/places:autocomplete")
	c.SetDetailsBaseURL(srv.URL + "/v1/places")
	return c
}

func TestClient_SearchPlaces(t *testing.T) {
	t.Parallel()

	// @spec components/entity/venue/search-places "Known venue"
	t.Run("return Zepp Haneda first with its place id and Japanese address", func(t *testing.T) {
		t.Parallel()
		var gotPath, gotMask, gotAuth, gotProject string
		var gotBody map[string]any
		c := catalogServer(t, http.StatusOK, recordedResponse(t, "autocomplete_zepp_haneda.json"), func(r *http.Request, body []byte) {
			gotPath = r.URL.Path
			gotMask = r.Header.Get("X-Goog-FieldMask")
			gotAuth = r.Header.Get("Authorization")
			gotProject = r.Header.Get("X-Goog-User-Project")
			_ = json.Unmarshal(body, &gotBody)
		})

		got, err := c.SearchPlaces(context.Background(), "Zepp Haneda")

		require.NoError(t, err)
		require.NotEmpty(t, got)
		assert.LessOrEqual(t, len(got), 5)
		assert.Equal(t, &entity.MapPlaceCandidate{
			PlaceID: zeppHanedaPlaceID,
			Name:    "Zepp Haneda (TOKYO)",
			Address: "東京都大田区羽田空港１丁目１−４",
		}, got[0])
		assert.Equal(t, "/v1/places:autocomplete", gotPath)
		assert.Equal(t, "suggestions.placePrediction.placeId,suggestions.placePrediction.structuredFormat", gotMask)
		assert.Equal(t, "Bearer test-token", gotAuth)
		assert.Equal(t, "test-project", gotProject)
		assert.Equal(t, map[string]any{
			"input":        "Zepp Haneda",
			"languageCode": "ja",
			"regionCode":   "jp",
		}, gotBody)
	})

	// @spec components/entity/venue/search-places "Beginning of a name"
	t.Run("return LIQUIDROOM among the places for LIQUIDRO, in the catalog's order", func(t *testing.T) {
		t.Parallel()
		c := catalogServer(t, http.StatusOK, recordedResponse(t, "autocomplete_liquidro.json"), nil)

		got, err := c.SearchPlaces(context.Background(), "LIQUIDRO")

		require.NoError(t, err)
		assert.Len(t, got, 5)
		assert.Contains(t, got, &entity.MapPlaceCandidate{
			PlaceID: liquidroomPlaceID,
			Name:    "LIQUIDROOM",
			Address: "東京都渋谷区東３丁目１６−６",
		})
		assert.Equal(t, liquidroomPlaceID, got[0].PlaceID)
	})

	t.Run("drop a prediction without an address and keep at most 5", func(t *testing.T) {
		t.Parallel()
		prediction := func(id, name, address string) string {
			return `{"placePrediction":{"placeId":"` + id + `","structuredFormat":{"mainText":{"text":"` + name +
				`"},"secondaryText":{"text":"` + address + `"}}}}`
		}
		body := `{"suggestions":[` + `{"placePrediction":{"placeId":"p0","structuredFormat":{"mainText":{"text":"日本"}}}},` +
			prediction("p1", "a", "x") + "," + prediction("p2", "b", "x") + "," + prediction("p3", "c", "x") + "," +
			prediction("p4", "d", "x") + "," + prediction("p5", "e", "x") + "," + prediction("p6", "f", "x") + `]}`
		c := catalogServer(t, http.StatusOK, []byte(body), nil)

		got, err := c.SearchPlaces(context.Background(), "日本")

		require.NoError(t, err)
		ids := make([]string, 0, len(got))
		for _, p := range got {
			ids = append(ids, p.PlaceID)
		}
		assert.Equal(t, []string{"p1", "p2", "p3", "p4", "p5"}, ids)
	})

	// @spec components/entity/venue/search-places "No match"
	t.Run("return an empty list when the catalog has no match", func(t *testing.T) {
		t.Parallel()
		c := catalogServer(t, http.StatusOK, recordedResponse(t, "autocomplete_no_match.json"), nil)

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

// resolvingAreas is an AreaRepository whose Resolve knows only the given
// region texts of each country (country code -> text -> Area code), plus HK as
// its own first-level Area; every other text is NotFound.
func resolvingAreas(t *testing.T, known map[string]map[string]string) *mocks.MockAreaRepository {
	t.Helper()
	areas := mocks.NewMockAreaRepository(t)
	areas.EXPECT().Resolve(mock.Anything, mock.Anything, mock.Anything).RunAndReturn(
		func(_ context.Context, text string, country *string) (*entity.Area, error) {
			if country != nil {
				if code, ok := known[*country][text]; ok {
					return &entity.Area{Code: code, CountryCode: *country}, nil
				}
			}
			return nil, apperr.New(codes.NotFound, "no area")
		}).Maybe()
	return areas
}

func TestClient_GetPlace(t *testing.T) {
	t.Parallel()

	japan := map[string]map[string]string{"JP": {"東京都": "JP-13"}}

	// @spec components/entity/venue/get-place "Known place"
	t.Run("return the name, country JP, coordinates with their read time, JP-13 and Asia/Tokyo of Zepp Haneda", func(t *testing.T) {
		t.Parallel()
		var gotPath, gotLang, gotMask string
		c := catalogServerWithAreas(t, resolvingAreas(t, japan), http.StatusOK, recordedResponse(t, "place_details_zepp_haneda.json"), func(r *http.Request, _ []byte) {
			gotPath = r.URL.Path
			gotLang = r.URL.Query().Get("languageCode")
			gotMask = r.Header.Get("X-Goog-FieldMask")
		})
		before := time.Now()

		got, err := c.GetPlace(context.Background(), zeppHanedaPlaceID)

		require.NoError(t, err)
		assert.WithinRange(t, got.CoordinatesFetchTime, before, time.Now())
		got.CoordinatesFetchTime = time.Time{}
		assert.Equal(t, &entity.MapPlace{
			PlaceID:            zeppHanedaPlaceID,
			Name:               "Zepp Haneda (TOKYO)",
			CountryCode:        "JP",
			Coordinates:        &entity.Coordinates{Latitude: 35.5473958, Longitude: 139.7565213},
			SuggestedAdminArea: new("JP-13"),
			TimeZone:           new("Asia/Tokyo"),
		}, got)
		assert.Equal(t, "/v1/places/"+zeppHanedaPlaceID, gotPath)
		assert.Equal(t, "ja", gotLang)
		assert.Equal(t, "id,displayName,location,addressComponents,timeZone", gotMask)
	})

	// @spec components/entity/venue/get-place "Overseas place"
	t.Run("return the country US and US-CA for a place in California", func(t *testing.T) {
		t.Parallel()
		areas := resolvingAreas(t, map[string]map[string]string{"US": {"California": "US-CA"}})
		c := catalogServerWithAreas(t, areas, http.StatusOK, recordedResponse(t, "place_details_fillmore.json"), nil)

		got, err := c.GetPlace(context.Background(), "ChIJO2EGsrmAhYARdDfDm-uVTQQ")

		require.NoError(t, err)
		assert.Equal(t, "US", got.CountryCode)
		assert.Equal(t, new("US-CA"), got.SuggestedAdminArea)
		assert.Equal(t, new("America/Los_Angeles"), got.TimeZone)
	})

	t.Run("try the region's short name when its full name does not resolve", func(t *testing.T) {
		t.Parallel()
		areas := resolvingAreas(t, map[string]map[string]string{"US": {"CA": "US-CA"}})
		c := catalogServerWithAreas(t, areas, http.StatusOK, recordedResponse(t, "place_details_fillmore.json"), nil)

		got, err := c.GetPlace(context.Background(), "ChIJO2EGsrmAhYARdDfDm-uVTQQ")

		require.NoError(t, err)
		assert.Equal(t, new("US-CA"), got.SuggestedAdminArea)
	})

	// @spec components/entity/venue/get-place "Territory without subdivisions"
	t.Run("return the country HK and the suggested admin area HK for a place in Hong Kong", func(t *testing.T) {
		t.Parallel()
		areas := resolvingAreas(t, map[string]map[string]string{"HK": {"HK": "HK"}})
		c := catalogServerWithAreas(t, areas, http.StatusOK, recordedResponse(t, "place_details_asiaworld_expo.json"), nil)

		got, err := c.GetPlace(context.Background(), "ChIJh5kkFNIGBDQRR8StxWm9pRs")

		require.NoError(t, err)
		assert.Equal(t, "HK", got.CountryCode)
		assert.Equal(t, new("HK"), got.SuggestedAdminArea)
	})

	// @spec components/entity/venue/get-place "Region that does not resolve"
	t.Run("return the place without a suggested admin area when the region names no Area of the country", func(t *testing.T) {
		t.Parallel()
		tests := []struct {
			name string
			body []byte
		}{
			{"region matches no Area", recordedResponse(t, "place_details_fillmore.json")},
			{"no region in the address", []byte(`{"id":"ChIJx","displayName":{"text":"Somewhere"},
				"addressComponents":[{"longText":"アメリカ合衆国","shortText":"US","types":["country","political"]}],
				"timeZone":{"id":"America/Chicago"}}`)},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				c := catalogServerWithAreas(t, resolvingAreas(t, nil), http.StatusOK, tt.body, nil)

				got, err := c.GetPlace(context.Background(), "ChIJx")

				require.NoError(t, err)
				assert.Equal(t, "US", got.CountryCode)
				assert.Nil(t, got.SuggestedAdminArea)
			})
		}
	})

	// @spec components/entity/venue/get-place "No time zone from the catalog"
	t.Run("return the place without a time zone when the catalog gives none", func(t *testing.T) {
		t.Parallel()
		body := []byte(`{"id":"ChIJx","displayName":{"text":"Zepp Haneda (TOKYO)"},
			"addressComponents":[{"longText":"東京都","shortText":"東京都","types":["administrative_area_level_1","political"]},
				{"longText":"日本","shortText":"JP","types":["country","political"]}]}`)
		c := catalogServerWithAreas(t, resolvingAreas(t, japan), http.StatusOK, body, nil)

		got, err := c.GetPlace(context.Background(), "ChIJx")

		require.NoError(t, err)
		assert.Nil(t, got.TimeZone)
		assert.Equal(t, new("JP-13"), got.SuggestedAdminArea)
	})

	t.Run("fail when the Areas cannot be read", func(t *testing.T) {
		t.Parallel()
		areas := mocks.NewMockAreaRepository(t)
		areas.EXPECT().Resolve(mock.Anything, mock.Anything, mock.Anything).
			Return(nil, apperr.New(codes.Unavailable, "database down")).Once()
		c := catalogServerWithAreas(t, areas, http.StatusOK, recordedResponse(t, "place_details_zepp_haneda.json"), nil)

		_, err := c.GetPlace(context.Background(), zeppHanedaPlaceID)

		assert.ErrorIs(t, err, apperr.ErrUnavailable)
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

func TestClient_GetPlaceCoordinates(t *testing.T) {
	t.Parallel()

	// @spec components/entity/venue/get-place-coordinates "Known place"
	t.Run("return the coordinates of Zepp Haneda with the time they were read, asking for the location only", func(t *testing.T) {
		t.Parallel()
		var gotMask string
		c := catalogServer(t, http.StatusOK, recordedResponse(t, "place_details_zepp_haneda.json"), func(r *http.Request, _ []byte) {
			gotMask = r.Header.Get("X-Goog-FieldMask")
		})
		before := time.Now()

		got, err := c.GetPlaceCoordinates(context.Background(), zeppHanedaPlaceID)

		require.NoError(t, err)
		assert.Equal(t, &entity.Coordinates{Latitude: 35.5473958, Longitude: 139.7565213}, got.Coordinates)
		assert.WithinRange(t, got.FetchTime, before, time.Now())
		assert.Equal(t, "id,location", gotMask)
	})

	// @spec components/entity/venue/get-place-coordinates "Place without a location"
	t.Run("return no coordinates and no error when the place has no location", func(t *testing.T) {
		t.Parallel()
		c := catalogServer(t, http.StatusOK, []byte(`{"id":"ChIJx"}`), nil)

		got, err := c.GetPlaceCoordinates(context.Background(), "ChIJx")

		require.NoError(t, err)
		assert.Nil(t, got.Coordinates)
	})

	// @spec components/entity/venue/get-place-coordinates "Place no longer exists"
	t.Run("fail with NOT_FOUND when the catalog has no place with the id", func(t *testing.T) {
		t.Parallel()
		c := catalogServer(t, http.StatusNotFound, []byte(`{"error":{"code":404,"status":"NOT_FOUND"}}`), nil)

		_, err := c.GetPlaceCoordinates(context.Background(), "ChIJgone")

		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})

	// @spec components/entity/venue/get-place-coordinates "Catalog down"
	t.Run("fail with UNAVAILABLE when the catalog cannot be reached", func(t *testing.T) {
		t.Parallel()
		_, err := unreachableClient(t).GetPlaceCoordinates(context.Background(), zeppHanedaPlaceID)

		assert.ErrorIs(t, err, apperr.ErrUnavailable)
	})
}
