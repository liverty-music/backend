package google

import (
	"context"

	"github.com/liverty-music/backend/internal/entity"
)

// PlaceSearcher adapts the Google Maps client to satisfy entity.VenuePlaceSearcher.
type PlaceSearcher struct {
	client *Client
}

// NewPlaceSearcher returns an entity.VenuePlaceSearcher backed by the Google Maps Places API.
func NewPlaceSearcher(c *Client) *PlaceSearcher {
	return &PlaceSearcher{client: c}
}

// SearchPlace implements entity.VenuePlaceSearcher.
func (s *PlaceSearcher) SearchPlace(ctx context.Context, name, adminArea string) ([]*entity.VenuePlace, error) {
	places, err := s.client.SearchText(ctx, name, adminArea)
	if err != nil {
		return nil, err
	}
	result := make([]*entity.VenuePlace, 0, len(places))
	for _, place := range places {
		var coords *entity.Coordinates
		if place.Latitude != nil && place.Longitude != nil {
			coords = &entity.Coordinates{Latitude: *place.Latitude, Longitude: *place.Longitude}
		}
		result = append(result, &entity.VenuePlace{ProviderUserID: place.PlaceID, Name: place.Name, Coordinates: coords})
	}
	return result, nil
}
