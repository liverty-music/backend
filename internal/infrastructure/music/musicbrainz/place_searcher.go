package musicbrainz

import (
	"context"

	"github.com/liverty-music/backend/internal/entity"
)

// PlaceSearcher adapts the MusicBrainz client to satisfy entity.VenuePlaceSearcher.
type PlaceSearcher struct {
	client *client
}

// NewPlaceSearcher returns an entity.VenuePlaceSearcher backed by the MusicBrainz Places API.
func NewPlaceSearcher(c *client) *PlaceSearcher {
	return &PlaceSearcher{client: c}
}

// SearchPlace implements entity.VenuePlaceSearcher.
func (s *PlaceSearcher) SearchPlace(ctx context.Context, name, adminArea string) (*entity.VenuePlace, error) {
	place, err := s.client.SearchPlace(ctx, name, adminArea)
	if err != nil {
		return nil, err
	}
	return &entity.VenuePlace{ExternalID: place.ID, Name: place.Name}, nil
}
