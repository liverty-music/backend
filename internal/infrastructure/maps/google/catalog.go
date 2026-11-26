package google

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/pkg/api"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
)

const (
	// defaultAutocompleteURL is the Autocomplete (New) endpoint SearchPlaces
	// posts to.
	defaultAutocompleteURL = "https://places.googleapis.com/v1/places:autocomplete"
	// defaultDetailsBaseURL is the Places API (New) resource collection; a
	// Place Details request is a GET on {defaultDetailsBaseURL}/{place id}.
	defaultDetailsBaseURL = "https://places.googleapis.com/v1/places"

	// catalogLanguageCode asks for Japanese names and addresses: the organizer
	// console serves members in Japan.
	catalogLanguageCode = "ja"
	// catalogRegionCode ranks places in Japan before places elsewhere and
	// formats addresses for Japan. Autocomplete takes a two-letter ccTLD.
	catalogRegionCode = "jp"
	// catalogSearchPageSize is the most candidates a member picks from.
	// Autocomplete returns at most 5 predictions; the cap keeps the contract
	// if that ever grows.
	catalogSearchPageSize = 5

	searchPlacesFieldMask = "suggestions.placePrediction.placeId,suggestions.placePrediction.structuredFormat"
	getPlaceFieldMask     = "id,displayName,location,addressComponents,timeZone"
	// getPlaceCoordinatesFieldMask asks for the location only, so the call
	// bills as Place Details Essentials.
	getPlaceCoordinatesFieldMask = "id,location"

	addressTypeAdminAreaLevel1 = "administrative_area_level_1"
	addressTypeCountry         = "country"
)

// addressComponent is one part of a place's address.
type addressComponent struct {
	LongText  string   `json:"longText"`
	ShortText string   `json:"shortText"`
	Types     []string `json:"types"`
}

// countryAndRegion returns the ISO 3166-1 alpha-2 code of an address's
// country ("" when it names none) and the full and short names of its
// first-level administrative area (nil when it names none).
func countryAndRegion(components []addressComponent) (country string, region []string) {
	for _, comp := range components {
		switch {
		case slices.Contains(comp.Types, addressTypeCountry):
			country = strings.ToUpper(comp.ShortText)
		case slices.Contains(comp.Types, addressTypeAdminAreaLevel1):
			region = []string{comp.LongText, comp.ShortText}
		}
	}
	return country, region
}

// Compile-time check that Client is the organizer console's map catalog.
var _ entity.VenueMapCatalog = (*Client)(nil)

// searchPlacesRequest is the JSON body of an Autocomplete (New) request for
// member-facing candidates. It sets no type filter (a live house's primary
// type may be bar or night_club), no session token (billing is per request;
// a session would bill GetPlace's Details call at a higher SKU) and no query
// predictions, so every suggestion is a place prediction.
type searchPlacesRequest struct {
	Input        string `json:"input"`
	LanguageCode string `json:"languageCode"`
	RegionCode   string `json:"regionCode"`
}

type localizedText struct {
	Text string `json:"text"`
}

// searchPlacesResponse is the Autocomplete (New) response restricted to
// searchPlacesFieldMask. The catalog answers no match with an empty object.
type searchPlacesResponse struct {
	Suggestions []struct {
		PlacePrediction *struct {
			PlaceID          string `json:"placeId"`
			StructuredFormat struct {
				MainText      localizedText `json:"mainText"`
				SecondaryText localizedText `json:"secondaryText"`
			} `json:"structuredFormat"`
		} `json:"placePrediction"`
	} `json:"suggestions"`
}

// placeDetailsResponse is the Place Details (New) response restricted to
// getPlaceFieldMask.
type placeDetailsResponse struct {
	ID          string        `json:"id"`
	DisplayName localizedText `json:"displayName"`
	Location    *struct {
		Latitude  float64 `json:"latitude"`
		Longitude float64 `json:"longitude"`
	} `json:"location"`
	AddressComponents []addressComponent `json:"addressComponents"`
	TimeZone          *struct {
		ID string `json:"id"`
	} `json:"timeZone"`
}

// SearchPlaces implements entity.VenueMapCatalog with Place Autocomplete
// (New), so the beginning of a name already matches: at most 5 places in the
// catalog's order, Japanese names, places in Japan ranked first. The name is
// the prediction's main text and the address its secondary text. A
// prediction without a place id, main text or secondary text is dropped: a
// candidate needs all three (the address tells same-named places apart).
func (c *Client) SearchPlaces(ctx context.Context, text string) ([]*entity.MapPlaceCandidate, error) {
	body, err := json.Marshal(searchPlacesRequest{
		Input:        text,
		LanguageCode: catalogLanguageCode,
		RegionCode:   catalogRegionCode,
	})
	if err != nil {
		return nil, apperr.Wrap(err, codes.Internal, "failed to marshal google maps search request body")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.autocompleteURL, bytes.NewReader(body))
	if err != nil {
		return nil, apperr.Wrap(err, codes.Internal, "failed to create google maps search request")
	}
	req.Header.Set("Content-Type", "application/json")

	var data searchPlacesResponse
	if err := c.doCatalogRequest(ctx, req, searchPlacesFieldMask, "google maps places search failed", &data); err != nil {
		return nil, err
	}

	out := make([]*entity.MapPlaceCandidate, 0, catalogSearchPageSize)
	for _, s := range data.Suggestions {
		p := s.PlacePrediction
		if p == nil || p.PlaceID == "" || p.StructuredFormat.MainText.Text == "" || p.StructuredFormat.SecondaryText.Text == "" {
			continue
		}
		out = append(out, &entity.MapPlaceCandidate{
			PlaceID: p.PlaceID,
			Name:    p.StructuredFormat.MainText.Text,
			Address: p.StructuredFormat.SecondaryText.Text,
		})
		if len(out) == catalogSearchPageSize {
			break
		}
	}
	return out, nil
}

// GetPlaceCoordinates implements entity.VenueMapCatalog with Place Details
// (New), asking only for the location.
func (c *Client) GetPlaceCoordinates(ctx context.Context, placeID string) (*entity.PlaceCoordinates, error) {
	data, err := c.placeDetails(ctx, placeID, getPlaceCoordinatesFieldMask)
	if err != nil {
		return nil, err
	}
	out := &entity.PlaceCoordinates{FetchTime: time.Now()}
	if data.Location != nil {
		out.Coordinates = &entity.Coordinates{Latitude: data.Location.Latitude, Longitude: data.Location.Longitude}
	}
	return out, nil
}

// placeDetails reads one place with the given field mask.
func (c *Client) waitForDetailsSlot(ctx context.Context) error {
	err := c.details.Wait(ctx)
	if err == nil {
		return nil
	}
	// The limiter refuses at once, with an error of its own, a wait that would
	// end after the deadline.
	if ctx.Err() == nil {
		err = fmt.Errorf("%w: %w", context.DeadlineExceeded, err)
	}
	return api.FromHTTP(err, nil, "wait for a google maps place details slot")
}

func (c *Client) placeDetails(ctx context.Context, placeID, fieldMask string) (*placeDetailsResponse, error) {
	u := c.detailsBaseURL + "/" + url.PathEscape(placeID) + "?languageCode=" + catalogLanguageCode
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, apperr.Wrap(err, codes.Internal, "failed to create google maps place details request")
	}

	if err := c.waitForDetailsSlot(ctx); err != nil {
		return nil, err
	}

	var data placeDetailsResponse
	err = c.doCatalogRequest(ctx, req, fieldMask, "google maps place details failed", &data)
	// The catalog answers an unknown place id with 400 INVALID_ARGUMENT
	// ("The provided Place ID: ... is not valid.") and an obsolete one with
	// 404 NOT_FOUND; the place id is the only input, so both mean no such place.
	if errors.Is(err, apperr.ErrInvalidArgument) || errors.Is(err, apperr.ErrNotFound) {
		return nil, apperr.Wrap(err, codes.NotFound, "no place with this id in google maps")
	}
	if err != nil {
		return nil, err
	}
	return &data, nil
}

// GetPlace implements entity.VenueMapCatalog with Place Details (New): the
// place's name, country, coordinates with the time they were read, time zone,
// and the first-level Area its address suggests (see suggestArea).
func (c *Client) GetPlace(ctx context.Context, placeID string) (*entity.MapPlace, error) {
	data, err := c.placeDetails(ctx, placeID, getPlaceFieldMask)
	if err != nil {
		return nil, err
	}

	place := &entity.MapPlace{PlaceID: data.ID, Name: data.DisplayName.Text, CoordinatesFetchTime: time.Now()}
	if data.Location != nil {
		place.Coordinates = &entity.Coordinates{Latitude: data.Location.Latitude, Longitude: data.Location.Longitude}
	}
	if data.TimeZone != nil && data.TimeZone.ID != "" {
		zone := data.TimeZone.ID
		place.TimeZone = &zone
	}
	var region []string
	place.CountryCode, region = countryAndRegion(data.AddressComponents)
	if place.SuggestedAdminArea, err = c.suggestArea(ctx, place.CountryCode, region); err != nil {
		return nil, err
	}
	return place, nil
}

// suggestArea returns the first-level Area a place's address suggests: the
// country itself when it is its own first-level Area (HK), else the Area the
// region's full name, then its short name, resolves to within the country.
// Nil when neither applies.
func (c *Client) suggestArea(ctx context.Context, country string, region []string) (*string, error) {
	if country == "" || c.areas == nil {
		return nil, nil
	}
	resolve := func(text string) (*string, error) {
		if text == "" {
			return nil, nil
		}
		area, err := c.areas.Resolve(ctx, text, &country)
		if errors.Is(err, apperr.ErrNotFound) || errors.Is(err, apperr.ErrInvalidArgument) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		if area.CountryCode != country {
			return nil, nil
		}
		return &area.Code, nil
	}
	if code, err := resolve(country); err != nil || (code != nil && *code == country) {
		return code, err
	}
	for _, text := range region {
		if code, err := resolve(text); err != nil || code != nil {
			return code, err
		}
	}
	return nil, nil
}

// doCatalogRequest authenticates req, sends it with the given field mask and
// decodes a 2xx JSON body into out. Network failures and 5xx responses are
// Unavailable, a cancelled or timed-out request is Canceled or
// DeadlineExceeded, and other statuses map through api.FromHTTP.
func (c *Client) doCatalogRequest(ctx context.Context, req *http.Request, fieldMask, failMsg string, out any) error {
	token, err := c.tokenSource.Token()
	if err != nil {
		return apperr.Wrap(err, codes.Unavailable, "failed to obtain oauth2 token for google maps")
	}
	req.Header.Set("Authorization", "Bearer "+token.AccessToken)
	req.Header.Set("X-Goog-FieldMask", fieldMask)
	req.Header.Set("X-Goog-User-Project", c.projectID)

	resp, err := c.httpClient.Do(req)
	if resp != nil {
		defer func() { _ = resp.Body.Close() }()
	}
	if err := api.FromHTTP(err, resp, failMsg); err != nil {
		attrs := []slog.Attr{slog.String("path", req.URL.Path)}
		if resp != nil {
			attrs = append(attrs, slog.Int("statusCode", resp.StatusCode))
		}
		// A 4xx answers the request (e.g. an unknown place id) and is not an
		// outage, so only network failures and 5xx are logged as errors.
		if resp == nil || resp.StatusCode >= http.StatusInternalServerError {
			c.logger.Error(ctx, failMsg, err, attrs...)
		} else {
			c.logger.Warn(ctx, failMsg, append(attrs, slog.Any("error", err))...)
		}
		return err
	}

	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return apperr.Wrap(err, codes.Internal, "failed to decode google maps response")
	}
	return nil
}
