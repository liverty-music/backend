package usecase_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	entitymocks "github.com/liverty-music/backend/internal/entity/mocks"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// authoringDeps wires up a SeriesUseCase with mocks for every dependency.
type authoringDeps struct {
	seriesRepo    *entitymocks.MockSeriesRepository
	venueRepo     *entitymocks.MockVenueRepository
	mapCatalog    *entitymocks.MockVenueMapCatalog
	organizerRepo *entitymocks.MockOrganizerRepository
	ticketSales   *entitymocks.MockTicketSaleRepository
	uc            usecase.SeriesUseCase
}

func newAuthoringDeps(t *testing.T) *authoringDeps {
	t.Helper()
	logger := newTestLogger(t)
	d := &authoringDeps{
		seriesRepo:    entitymocks.NewMockSeriesRepository(t),
		venueRepo:     entitymocks.NewMockVenueRepository(t),
		mapCatalog:    entitymocks.NewMockVenueMapCatalog(t),
		organizerRepo: entitymocks.NewMockOrganizerRepository(t),
		ticketSales:   entitymocks.NewMockTicketSaleRepository(t),
	}
	// A planned new Venue is checked before any Venue is stored.
	d.venueRepo.EXPECT().CheckNew(mock.Anything, mock.Anything).Return(nil).Maybe()
	// A Create that answers with another id stands for a place another writer
	// stored first: the event is checked again against that Venue.
	d.venueRepo.EXPECT().Get(mock.Anything, mock.Anything).RunAndReturn(
		func(_ context.Context, id string) (*entity.Venue, error) {
			return &entity.Venue{ID: id, TimeZone: new("Asia/Tokyo")}, nil
		}).Maybe()
	d.uc = usecase.NewSeriesUseCase(
		d.seriesRepo, entitymocks.NewMockSeriesImageRepository(t), d.venueRepo, d.mapCatalog, d.organizerRepo,
		d.ticketSales, nil, usecase.StorageBuckets{}, logger, func() time.Time { return authoringNow },
	)
	return d
}

// concertsOf builds the Concerts Series.GetAuthored returns: one per event,
// each performed by the artists.
func concertsOf(events []*entity.Event, artists []*entity.Artist) []*entity.Concert {
	concerts := make([]*entity.Concert, len(events))
	for i, e := range events {
		concerts[i] = &entity.Concert{Event: *e, Artists: artists}
	}
	return concerts
}

// authoringNow is the clock of every SeriesUseCase under test.
var authoringNow = time.Date(2026, 10, 12, 3, 0, 0, 0, time.UTC)

// futureDate returns a date 30 days after authoringNow so validation passes.
func futureDate() time.Time {
	return authoringNow.Add(30 * 24 * time.Hour).Truncate(24 * time.Hour)
}

// --- Tests ---

func TestSeriesUseCase_Create_OwnershipReject(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := newAuthoringDeps(t)

	const (
		orgID    = "org-1"
		artistID = "artist-unknown"
	)
	// The organizer owns a different artist; artistID is not in the list.
	d.organizerRepo.EXPECT().ListArtists(mock.Anything, orgID).
		Return([]*entity.Artist{{ID: "artist-other"}}, nil)

	series := &entity.Series{Title: "Tour", Type: entity.SeriesTypeTour}
	vis := entity.SeriesVisibilityPublic
	series.Visibility = &vis
	inp := []*usecase.DraftEventInput{{
		VenueName: "Zepp Tokyo",
		LocalDate: futureDate(),
	}}

	_, _, err := d.uc.Create(ctx, orgID, series, inp, []string{artistID})
	require.Error(t, err)
	assert.True(t, errors.Is(err, apperr.ErrPermissionDenied), "expected PermissionDenied, got %v", err)
}

func TestSeriesUseCase_RegenerateShareURL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := newAuthoringDeps(t)

	const (
		orgID    = "org-1"
		seriesID = "series-unlisted"
	)
	unl := entity.SeriesVisibilityUnlisted
	ps := entity.PublishStatePublished
	s := &entity.Series{
		ID: seriesID, Title: "Secret", Type: entity.SeriesTypeSingle,
		OrganizerID: new(orgID), Visibility: &unl, PublishState: &ps,
	}

	d.seriesRepo.EXPECT().Get(mock.Anything, seriesID).Return(s, nil)
	d.seriesRepo.EXPECT().SetUnlistedToken(mock.Anything, seriesID, mock.Anything).Return(nil)

	token, err := d.uc.RegenerateShareURL(ctx, orgID, seriesID)
	require.NoError(t, err)
	assert.NotEmpty(t, token)
}
