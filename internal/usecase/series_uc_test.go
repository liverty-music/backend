package usecase_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	entitymocks "github.com/liverty-music/backend/internal/entity/mocks"
	"github.com/liverty-music/backend/internal/infrastructure/messaging"
	"github.com/liverty-music/backend/internal/usecase"
	ucmocks "github.com/liverty-music/backend/internal/usecase/mocks"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
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
	publisher     *ucmocks.MockEventPublisher
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
		publisher:     ucmocks.NewMockEventPublisher(t),
	}
	d.uc = usecase.NewSeriesUseCase(
		d.seriesRepo, entitymocks.NewMockSeriesImageRepository(t), d.venueRepo, d.mapCatalog, d.organizerRepo,
		d.ticketSales, d.publisher, logger, func() time.Time { return authoringNow },
	)
	return d
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

	_, _, _, err := d.uc.Create(ctx, orgID, series, inp, []string{artistID})
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

func TestConcertUseCase_SearchNewConcerts_DiscoveryExclusionOn(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	d := newConcertTestDeps(t)
	logger := newTestLogger(t)

	orgRepo := entitymocks.NewMockOrganizerRepository(t)
	orgRepo.EXPECT().IsArtistRepresentedByActiveOrganizer(mock.Anything, "artist-1").
		Return(true, nil)

	uc := usecase.NewConcertUseCase(
		d.artistRepo, d.concertRepo, d.venueRepo, d.seriesRepo,
		orgRepo,
		d.concertSearchRepo, d.concertCandidateRepo, d.rejectedConcertRepo,
		d.searcher, d.centroidResolver,
		messaging.NewEventPublisher(d.publisher),
		noopMetrics{},
		testSearchCacheTTL, testDiscoveryWindow, logger,
	)

	concerts, err := uc.SearchNewConcerts(ctx, "artist-1")
	require.NoError(t, err)
	assert.Nil(t, concerts, "expected nil result when artist is organizer-represented")
	// Searcher mock has no expectations — no Gemini call must have been made.
}

func TestConcertUseCase_SearchNewConcerts_DiscoveryExclusionOff(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	d := newConcertTestDeps(t)
	logger := newTestLogger(t)

	orgRepo := entitymocks.NewMockOrganizerRepository(t)
	orgRepo.EXPECT().IsArtistRepresentedByActiveOrganizer(mock.Anything, "artist-2").
		Return(false, nil)

	uc := usecase.NewConcertUseCase(
		d.artistRepo, d.concertRepo, d.venueRepo, d.seriesRepo,
		orgRepo,
		d.concertSearchRepo, d.concertCandidateRepo, d.rejectedConcertRepo,
		d.searcher, d.centroidResolver,
		messaging.NewEventPublisher(d.publisher),
		noopMetrics{},
		testSearchCacheTTL, testDiscoveryWindow, logger,
	)

	// Artist not represented → discovery pipeline proceeds.
	d.concertSearchRepo.EXPECT().GetByArtistID(mock.Anything, "artist-2").
		Return(nil, apperr.New(codes.NotFound, "not found"))
	d.concertSearchRepo.EXPECT().Upsert(mock.Anything, "artist-2", entity.ConcertSearchStatusPending).
		Return(nil)
	d.artistRepo.EXPECT().Get(mock.Anything, "artist-2").
		Return(&entity.Artist{ID: "artist-2", Name: "Test Artist", MBID: "mbid-1"}, nil)
	d.artistRepo.EXPECT().GetOfficialSite(mock.Anything, "artist-2").
		Return(nil, apperr.New(codes.NotFound, "not found"))
	d.concertRepo.EXPECT().ListByArtist(mock.Anything, "artist-2", true).
		Return(nil, nil)
	d.concertCandidateRepo.EXPECT().ListPendingDedupKeysByArtist(mock.Anything, "artist-2").
		Return(nil, nil)
	d.searcher.EXPECT().Search(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil, nil)
	d.concertSearchRepo.EXPECT().UpdateStatus(mock.Anything, "artist-2", entity.ConcertSearchStatusCompleted).
		Return(nil).Maybe()

	concerts, err := uc.SearchNewConcerts(ctx, "artist-2")
	require.NoError(t, err)
	assert.Nil(t, concerts)
}
