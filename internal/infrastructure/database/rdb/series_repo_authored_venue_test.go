package rdb_test

import (
	"context"
	"testing"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"uuid"
)

// TestSeriesRepository_GetAuthored_EventVenue verifies that the authored read
// fills each draft and published Event's Venue (id, name, admin area and place
// id) from the venues table, so the organizer List response can carry the
// picked place.
func TestSeriesRepository_GetAuthored_EventVenue(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	repo := rdb.NewSeriesRepository(testDB, testOutbox)
	ctx := context.Background()

	organizerID := seedOrganizer(t)
	artistID := seedArtist(t, "Authored Venue Artist", uuid.NewV4().String())
	placeID := "ChIJ-authored-" + uuid.NewV7().String()
	venueID := uuid.NewV7().String()
	_, err := testDB.Pool.Exec(ctx,
		`INSERT INTO venues (id, name, admin_area, google_place_id, latitude, longitude) VALUES ($1, 'Zepp Haneda (TOKYO)', 'JP-13', $2, 35.5473958, 139.7565213)`,
		venueID, placeID,
	)
	require.NoError(t, err)

	vis := entity.SeriesVisibilityPublic
	series := &entity.Series{
		ID:          entity.NewID(),
		Title:       "ONE MAN LIVE",
		Type:        entity.SeriesTypeSingle,
		OrganizerID: &organizerID,
		Visibility:  &vis,
	}
	listed := "Zepp Haneda"
	draftEvent := entity.NewAuthoredEvent(series.ID, venueID, &listed, time.Date(2027, 6, 1, 0, 0, 0, 0, time.UTC), nil, nil, nil)
	require.NoError(t, repo.CreateDraft(ctx, series, []*entity.Event{draftEvent}, []string{artistID}))

	wantVenue := &entity.Venue{
		ID:            venueID,
		Name:          "Zepp Haneda (TOKYO)",
		AdminArea:     new("JP-13"),
		GooglePlaceID: &placeID,
	}

	t.Run("fill the venue of each draft event", func(t *testing.T) {
		_, concerts, err := repo.GetAuthored(ctx, series.ID)
		events := entity.EventsOf(concerts)

		require.NoError(t, err)
		require.Len(t, events, 1)
		assert.Equal(t, wantVenue, events[0].Venue)
		assert.Equal(t, &listed, events[0].ListedVenueName)
	})

	t.Run("fill the venue of each published event", func(t *testing.T) {
		_, err := repo.PublishEvents(ctx, series.ID, []string{draftEvent.ID})
		require.NoError(t, err)

		_, concerts, err := repo.GetAuthored(ctx, series.ID)
		events := entity.EventsOf(concerts)

		require.NoError(t, err)
		require.Len(t, events, 1)
		assert.Equal(t, wantVenue, events[0].Venue)
		assert.Equal(t, &listed, events[0].ListedVenueName)
	})
}
