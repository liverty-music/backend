package usecase_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// refreshStart is the instant every RefreshCoordinates run of these tests
// starts at.
var refreshStart = time.Date(2026, 11, 24, 4, 0, 0, 0, time.UTC)

// dueVenue is a listed Venue with the place id "P-<n>".
func dueVenue(n int) *entity.Venue {
	return &entity.Venue{ID: fmt.Sprintf("venue-%d", n), Name: "Hall", AdminArea: new("JP-13"), TimeZone: new("Asia/Tokyo"), GooglePlaceID: new(fmt.Sprintf("P-%d", n))}
}

func TestVenueUseCase_RefreshCoordinates(t *testing.T) {
	t.Parallel()

	haneda := &entity.Coordinates{Latitude: 35.55, Longitude: 139.78}
	read := refreshStart.Add(time.Second)

	// @spec components/usecase/venue/refresh-coordinates "Coordinates refreshed"
	t.Run("store the coordinates just read with their read time, and nothing else", func(t *testing.T) {
		t.Parallel()
		d := newAuthoringDeps(t)
		d.venueRepo.EXPECT().ListCoordinatesDue(mock.Anything, refreshStart, usecase.CoordinatesRefreshBatch).Return([]*entity.Venue{dueVenue(1)}, nil).Once()
		d.mapCatalog.EXPECT().GetPlaceCoordinates(mock.Anything, "P-1").Return(&entity.PlaceCoordinates{Coordinates: haneda, FetchTime: read}, nil).Once()
		// SetCoordinates writes only the coordinates and their fetch time,
		// so the Venue keeps its name, area and zone.
		d.venueRepo.EXPECT().SetCoordinates(mock.Anything, "venue-1", &entity.PlaceCoordinates{Coordinates: haneda, FetchTime: read}).Return(nil).Once()
		d.venueRepo.EXPECT().ClearExpiredCoordinates(mock.Anything, refreshStart).Return(0, nil).Once()

		got, err := usecase.NewVenueUseCase(d.mapCatalog, d.venueRepo, nil).RefreshCoordinates(context.Background(), refreshStart)

		require.NoError(t, err)
		assert.Equal(t, &usecase.CoordinatesRefreshSummary{Due: 1, Refreshed: 1}, got)
	})

	// @spec components/usecase/venue/refresh-coordinates "Place no longer exists"
	t.Run("remove the coordinates of a Venue whose place no longer exists", func(t *testing.T) {
		t.Parallel()
		d := newAuthoringDeps(t)
		d.venueRepo.EXPECT().ListCoordinatesDue(mock.Anything, refreshStart, usecase.CoordinatesRefreshBatch).Return([]*entity.Venue{dueVenue(1)}, nil).Once()
		d.mapCatalog.EXPECT().GetPlaceCoordinates(mock.Anything, "P-1").Return(nil, apperr.New(codes.NotFound, "no place")).Once()
		d.venueRepo.EXPECT().SetCoordinates(mock.Anything, "venue-1", (*entity.PlaceCoordinates)(nil)).Return(nil).Once()
		d.venueRepo.EXPECT().ClearExpiredCoordinates(mock.Anything, refreshStart).Return(0, nil).Once()

		got, err := usecase.NewVenueUseCase(d.mapCatalog, d.venueRepo, nil).RefreshCoordinates(context.Background(), refreshStart)

		require.NoError(t, err)
		assert.Equal(t, 1, got.Removed)
	})

	t.Run("leave a Venue unchanged when its place has no coordinates", func(t *testing.T) {
		t.Parallel()
		d := newAuthoringDeps(t)
		d.venueRepo.EXPECT().ListCoordinatesDue(mock.Anything, refreshStart, usecase.CoordinatesRefreshBatch).Return([]*entity.Venue{dueVenue(1)}, nil).Once()
		d.mapCatalog.EXPECT().GetPlaceCoordinates(mock.Anything, "P-1").Return(&entity.PlaceCoordinates{FetchTime: read}, nil).Once()
		d.venueRepo.EXPECT().ClearExpiredCoordinates(mock.Anything, refreshStart).Return(0, nil).Once()

		got, err := usecase.NewVenueUseCase(d.mapCatalog, d.venueRepo, nil).RefreshCoordinates(context.Background(), refreshStart)

		require.NoError(t, err)
		assert.Equal(t, &usecase.CoordinatesRefreshSummary{Due: 1}, got)
	})

	// @spec components/usecase/venue/refresh-coordinates "More than 1,000 due"
	t.Run("list at most 1,000 Venues and refresh each listed one", func(t *testing.T) {
		t.Parallel()
		d := newAuthoringDeps(t)
		listed := make([]*entity.Venue, usecase.CoordinatesRefreshBatch)
		for i := range listed {
			listed[i] = dueVenue(i)
		}
		// The repository caps the list; the usecase asks for 1,000.
		d.venueRepo.EXPECT().ListCoordinatesDue(mock.Anything, refreshStart, 1000).Return(listed, nil).Once()
		d.mapCatalog.EXPECT().GetPlaceCoordinates(mock.Anything, mock.Anything).Return(&entity.PlaceCoordinates{Coordinates: haneda, FetchTime: read}, nil).Times(1000)
		d.venueRepo.EXPECT().SetCoordinates(mock.Anything, mock.Anything, mock.Anything).Return(nil).Times(1000)
		d.venueRepo.EXPECT().ClearExpiredCoordinates(mock.Anything, refreshStart).Return(0, nil).Once()

		got, err := usecase.NewVenueUseCase(d.mapCatalog, d.venueRepo, nil).RefreshCoordinates(context.Background(), refreshStart)

		require.NoError(t, err)
		assert.Equal(t, 1000, got.Refreshed)
	})

	// @spec components/usecase/venue/refresh-coordinates "Catalog down for the whole run"
	// @spec components/usecase/venue/refresh-coordinates "Retried the next day"
	t.Run("keep failed Venues unchanged, still remove expired coordinates, and fail", func(t *testing.T) {
		t.Parallel()
		d := newAuthoringDeps(t)
		d.venueRepo.EXPECT().ListCoordinatesDue(mock.Anything, refreshStart, usecase.CoordinatesRefreshBatch).Return([]*entity.Venue{dueVenue(1), dueVenue(2)}, nil).Once()
		d.mapCatalog.EXPECT().GetPlaceCoordinates(mock.Anything, mock.Anything).Return(nil, apperr.New(codes.Unavailable, "catalog down")).Twice()
		// No SetCoordinates: both Venues keep their coordinates until the
		// expiry below removes the ones older than 30 days.
		d.venueRepo.EXPECT().ClearExpiredCoordinates(mock.Anything, refreshStart).Return(1, nil).Once()

		got, err := usecase.NewVenueUseCase(d.mapCatalog, d.venueRepo, nil).RefreshCoordinates(context.Background(), refreshStart)

		assert.ErrorIs(t, err, apperr.ErrUnavailable)
		assert.Equal(t, &usecase.CoordinatesRefreshSummary{Due: 2, Failed: 2, Expired: 1}, got)
	})

	t.Run("still remove expired coordinates when listing fails, and fail", func(t *testing.T) {
		t.Parallel()
		d := newAuthoringDeps(t)
		d.venueRepo.EXPECT().ListCoordinatesDue(mock.Anything, refreshStart, usecase.CoordinatesRefreshBatch).Return(nil, apperr.New(codes.Unavailable, "db down")).Once()
		d.venueRepo.EXPECT().ClearExpiredCoordinates(mock.Anything, refreshStart).Return(3, nil).Once()

		got, err := usecase.NewVenueUseCase(d.mapCatalog, d.venueRepo, nil).RefreshCoordinates(context.Background(), refreshStart)

		assert.ErrorIs(t, err, apperr.ErrUnavailable)
		assert.Equal(t, int64(3), got.Expired)
	})
}
