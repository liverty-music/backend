package usecase_test

import (
	"context"
	"testing"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	entitymocks "github.com/liverty-music/backend/internal/entity/mocks"
	"github.com/liverty-music/backend/internal/usecase"
	ucmocks "github.com/liverty-music/backend/internal/usecase/mocks"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// approvalTestDeps bundles the mocks of a ConcertCandidateUseCase. Its
// expectation helpers record what the usecase passes to the mocks, so a test
// can assert on what was stored; a call no test expects fails the test.
type approvalTestDeps struct {
	artist          *entity.Artist
	candidateRepo   *entitymocks.MockConcertCandidateRepository
	rejectedConcert *entitymocks.MockRejectedConcertRepository
	venueRepo       *entitymocks.MockVenueRepository
	seriesRepo      *entitymocks.MockSeriesRepository
	concertRepo     *entitymocks.MockConcertRepository
	artistRepo      *entitymocks.MockArtistRepository
	areas           *entitymocks.MockAreaRepository
	places          *entitymocks.MockVenueMapCatalog
	uc              usecase.ConcertCandidateUseCase

	createdVenues   []*entity.Venue
	createdConcerts []*entity.Concert
	rejections      []*entity.RejectedConcert
}

// approvalAreas answers Area.Resolve and Area.ListByCountry for the areas the
// approval tests use: JP-13, US-CA (America/Los_Angeles), US-TX (no zone of
// its own), FR-NAQ (with FR-33 inside it) and the name California.
func approvalAreas(m *entitymocks.MockAreaRepository) {
	area := func(code string, zone *string) *entity.Area {
		return &entity.Area{Code: code, Kind: entity.AreaKindSubdivision, CountryCode: code[:2], NameEN: code, TimeZone: zone}
	}
	m.EXPECT().Resolve(mock.Anything, mock.Anything, (*string)(nil)).RunAndReturn(
		func(_ context.Context, text string, _ *string) (*entity.Area, error) {
			switch text {
			case "JP-13":
				return area("JP-13", new("Asia/Tokyo")), nil
			case "US-CA", "California":
				return area("US-CA", new("America/Los_Angeles")), nil
			case "US-TX":
				return area("US-TX", nil), nil
			case "FR-33", "FR-NAQ":
				return area("FR-NAQ", new("Europe/Paris")), nil
			}
			return nil, apperr.New(codes.NotFound, "no area")
		}).Maybe()
	zones := func(names ...string) *entity.CountryAreas {
		out := &entity.CountryAreas{}
		for _, n := range names {
			out.TimeZones = append(out.TimeZones, &entity.AreaTimeZone{Name: n, NameEN: n})
		}
		return out
	}
	m.EXPECT().ListByCountry(mock.Anything, "JP").Return(zones("Asia/Tokyo"), nil).Maybe()
	m.EXPECT().ListByCountry(mock.Anything, "US").Return(zones("America/New_York", "America/Chicago", "America/Denver", "America/Los_Angeles"), nil).Maybe()
	m.EXPECT().ListByCountry(mock.Anything, "FR").Return(zones("Europe/Paris"), nil).Maybe()
}

func newApprovalTestDeps(t *testing.T, artist *entity.Artist) *approvalTestDeps {
	t.Helper()
	d := &approvalTestDeps{
		artist:          artist,
		candidateRepo:   entitymocks.NewMockConcertCandidateRepository(t),
		rejectedConcert: entitymocks.NewMockRejectedConcertRepository(t),
		venueRepo:       entitymocks.NewMockVenueRepository(t),
		seriesRepo:      entitymocks.NewMockSeriesRepository(t),
		concertRepo:     entitymocks.NewMockConcertRepository(t),
		artistRepo:      entitymocks.NewMockArtistRepository(t),
		areas:           entitymocks.NewMockAreaRepository(t),
		places:          entitymocks.NewMockVenueMapCatalog(t),
	}
	approvalAreas(d.areas)
	// Pass nil for repos/deps that Approve/Reject/ListPending/List/Delete never touch:
	// concertSearchRepo, concertSearcher and metrics.
	concerts := usecase.NewConcertUseCase(
		d.artistRepo,
		d.concertRepo,
		d.venueRepo,
		d.seriesRepo,
		nil, // organizerRepo — not used by admin methods
		nil, // concertSearchRepo — not used by admin methods
		d.candidateRepo,
		d.rejectedConcert,
		nil, // concertSearcher — not used by admin methods
		d.areas,
		ucmocks.NewMockWorkflows(t),
		anyConcertMetrics(t),
		nil,         // analytics — not used by admin methods
		0,           // searchCacheTTL — not used by admin methods
		0, time.Now, // discoveryWindow — not used by admin methods
		newTestLogger(t),
	)
	d.uc = usecase.NewConcertCandidateUseCase(concerts, d.places)
	return d
}

// pending expects sc to be read once by its id.
func (d *approvalTestDeps) pending(sc *entity.ConcertCandidate) {
	d.candidateRepo.EXPECT().GetByID(mock.Anything, sc.ID).Return(sc, nil).Once()
}

// gone expects the concert candidate id to be read once and not found.
func (d *approvalTestDeps) gone(id string) {
	d.candidateRepo.EXPECT().GetByID(mock.Anything, id).Return(nil, notFound("concert candidate")).Once()
}

// deletes expects the concert candidate id to be deleted once.
func (d *approvalTestDeps) deletes(id string) {
	d.candidateRepo.EXPECT().Delete(mock.Anything, id).Return(nil).Once()
}

// createsVenue expects a venue stored under neither placeID (when given) nor
// (listedName, adminArea) to be created, and records it.
func (d *approvalTestDeps) createsVenue(placeID *string, listedName, adminArea string) {
	if placeID != nil {
		d.venueRepo.EXPECT().GetByPlaceID(mock.Anything, *placeID).Return(nil, notFound("venue")).Once()
	}
	d.venueRepo.EXPECT().GetByListedName(mock.Anything, listedName, adminArea).Return(nil, notFound("venue")).Once()
	d.venueRepo.EXPECT().Create(mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, v *entity.Venue) (string, error) {
			d.createdVenues = append(d.createdVenues, v)
			return v.ID, nil
		}).Once()
}

// slotFree expects the lookup at the resolved venue and date to find no
// stored Event.
func (d *approvalTestDeps) slotFree() {
	d.concertRepo.EXPECT().FindEventsByVenueAndDate(mock.Anything, mock.Anything, mock.Anything).Return(nil, nil).Once()
}

// inserts expects one concert to be created and records it.
func (d *approvalTestDeps) inserts() {
	d.concertRepo.EXPECT().Create(mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, concerts ...*entity.Concert) ([]string, error) {
			d.createdConcerts = append(d.createdConcerts, concerts...)
			return []string{concerts[0].ID}, nil
		}).Once()
}

// logsRejection expects the Artist to be read and one rejection to be
// appended to the log, and records it.
func (d *approvalTestDeps) logsRejection() {
	d.artistRepo.EXPECT().Get(mock.Anything, d.artist.ID).Return(d.artist, nil).Once()
	d.rejectedConcert.EXPECT().Append(mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, log *entity.RejectedConcert) error {
			d.rejections = append(d.rejections, log)
			return nil
		}).Once()
}

// candidate returns a pending concert candidate listing "Zepp Haneda(TOKYO)"
// with the given area, time zone and resolved place, under a series created
// at discovery time.
func (d *approvalTestDeps) candidate(area, zone, placeID *string) *entity.ConcertCandidate {
	sourceURL := "https://example.com/show"
	sc := &entity.ConcertCandidate{
		ID:              "candidate-001",
		ArtistID:        d.artist.ID,
		SeriesID:        "series-candidate-001",
		Title:           "Approval Test Concert",
		LocalDate:       time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
		ListedVenueName: "Zepp Haneda(TOKYO)",
		SourceURL:       &sourceURL,
		AdminArea:       area,
		TimeZone:        zone,
		ResolvedPlaceID: placeID,
	}
	d.pending(sc)
	return sc
}

// approve runs Approve with no resolution and the given area and zone.
func (d *approvalTestDeps) approve(sc *entity.ConcertCandidate, area, zone *string) error {
	_, err := d.uc.Approve(context.Background(), sc.ID, usecase.ApproveResolutionUnspecified, area, zone, "")
	return err
}

func TestConcertCandidateUseCase_Approve(t *testing.T) {
	t.Parallel()

	artist := &entity.Artist{ID: "artist-1", Name: "Test Artist", MBID: "11111111-1111-1111-1111-111111111111"}
	haneda := &entity.Coordinates{Latitude: 35.55, Longitude: 139.78}
	fetched := time.Date(2026, 11, 24, 4, 0, 0, 0, time.UTC)

	t.Run("New venue at a resolved place", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/concert-candidate/approve "New venue at a resolved place"
		// @spec components/usecase/concert-candidate/approve "New concert"
		d := newApprovalTestDeps(t, artist)
		sc := d.candidate(new("JP-13"), nil, new("P"))
		d.createsVenue(new("P"), "Zepp Haneda(TOKYO)", "JP-13")
		d.places.EXPECT().GetPlaceCoordinates(mock.Anything, "P").Return(&entity.PlaceCoordinates{Coordinates: haneda, FetchTime: fetched}, nil).Once()
		d.slotFree()
		d.inserts()
		d.deletes(sc.ID)

		require.NoError(t, d.approve(sc, nil, nil))

		require.Len(t, d.createdConcerts, 1)
		assert.Equal(t, sc.SeriesID, d.createdConcerts[0].SeriesID, "inserted under the candidate's series")
		require.Len(t, d.createdVenues, 1)
		v := d.createdVenues[0]
		assert.Equal(t, "Zepp Haneda(TOKYO)", v.Name, "named by its listed name")
		assert.Equal(t, "P", *v.GooglePlaceID)
		assert.Equal(t, haneda, v.Coordinates)
		assert.Equal(t, fetched, *v.CoordinatesFetchTime)
		assert.Equal(t, "Asia/Tokyo", *v.TimeZone, "the Area's own zone")
	})

	t.Run("Unresolved concert candidate", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/concert-candidate/approve "Unresolved concert candidate"
		d := newApprovalTestDeps(t, artist)
		sc := d.candidate(new("JP-13"), nil, nil)
		d.createsVenue(nil, "Zepp Haneda(TOKYO)", "JP-13")
		d.slotFree()
		d.inserts()
		d.deletes(sc.ID)

		require.NoError(t, d.approve(sc, nil, nil))

		v := d.createdVenues[0]
		assert.Equal(t, "JP-13", *v.AdminArea)
		assert.Equal(t, "Asia/Tokyo", *v.TimeZone)
		assert.Nil(t, v.GooglePlaceID)
		assert.Nil(t, v.Coordinates)
	})

	t.Run("Different place id for a stored venue", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/concert-candidate/approve "Different place id for a stored venue"
		d := newApprovalTestDeps(t, artist)
		sc := d.candidate(new("JP-13"), nil, new("P2"))
		d.venueRepo.EXPECT().GetByPlaceID(mock.Anything, "P2").Return(nil, notFound("venue")).Once()
		d.venueRepo.EXPECT().GetByListedName(mock.Anything, "Zepp Haneda(TOKYO)", "JP-13").Return(&entity.Venue{ID: "venue-existing"}, nil).Once()
		d.venueRepo.EXPECT().BackfillPlaceID(mock.Anything, "venue-existing", "P2").Return(nil).Once()
		d.slotFree()
		d.inserts()
		d.deletes(sc.ID)

		require.NoError(t, d.approve(sc, nil, nil))

		assert.Equal(t, "venue-existing", d.createdConcerts[0].VenueID)
	})

	t.Run("approve reuses an existing venue by place_id", func(t *testing.T) {
		t.Parallel()
		d := newApprovalTestDeps(t, artist)
		sc := d.candidate(new("JP-13"), nil, new("P"))
		d.venueRepo.EXPECT().GetByPlaceID(mock.Anything, "P").Return(&entity.Venue{ID: "venue-existing", GooglePlaceID: new("P")}, nil).Once()
		d.slotFree()
		d.inserts()
		d.deletes(sc.ID)

		require.NoError(t, d.approve(sc, nil, nil))

		assert.Equal(t, "venue-existing", d.createdConcerts[0].VenueID)
	})

	t.Run("Admin gives the area and the time zone", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/concert-candidate/approve "Admin gives the area and the time zone"
		d := newApprovalTestDeps(t, artist)
		sc := d.candidate(nil, nil, nil)
		d.createsVenue(nil, "Zepp Haneda(TOKYO)", "US-CA")
		d.slotFree()
		d.inserts()
		d.deletes(sc.ID)

		require.NoError(t, d.approve(sc, new("California"), new("America/Los_Angeles")))

		assert.Equal(t, "US-CA", *d.createdVenues[0].AdminArea)
		assert.Equal(t, "America/Los_Angeles", *d.createdVenues[0].TimeZone)
	})

	t.Run("Candidate staged with a lower-level code", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/concert-candidate/approve "Candidate staged with a lower-level code"
		d := newApprovalTestDeps(t, artist)
		sc := d.candidate(new("FR-33"), nil, nil)
		d.createsVenue(nil, "Zepp Haneda(TOKYO)", "FR-NAQ")
		d.slotFree()
		d.inserts()
		d.deletes(sc.ID)

		require.NoError(t, d.approve(sc, nil, nil))

		assert.Equal(t, "FR-NAQ", *d.createdVenues[0].AdminArea)
		assert.Equal(t, "Europe/Paris", *d.createdVenues[0].TimeZone)
	})

	t.Run("the candidate's zone when the Area has none", func(t *testing.T) {
		t.Parallel()
		d := newApprovalTestDeps(t, artist)
		sc := d.candidate(new("US-TX"), new("America/Chicago"), nil)
		d.createsVenue(nil, "Zepp Haneda(TOKYO)", "US-TX")
		d.slotFree()
		d.inserts()
		d.deletes(sc.ID)

		require.NoError(t, d.approve(sc, nil, nil))

		assert.Equal(t, "America/Chicago", *d.createdVenues[0].TimeZone)
	})

	for _, tc := range []struct {
		name       string
		area, zone *string // what the candidate carries
		give, tz   *string // what the admin gives
	}{
		// @spec components/usecase/concert-candidate/approve "Area still unknown"
		{name: "Area still unknown"},
		// @spec components/usecase/concert-candidate/approve "Area the admin gives does not resolve"
		{name: "Area the admin gives does not resolve", area: new("JP-13"), give: new("JP-99")},
		// @spec components/usecase/concert-candidate/approve "Time zone still unknown"
		{name: "Time zone still unknown", area: new("US-TX")},
		// @spec components/usecase/concert-candidate/approve "Time zone of another country"
		{name: "Time zone of another country", area: new("US-TX"), tz: new("Asia/Tokyo")},
		// @spec components/usecase/concert-candidate/approve "Time zone other than the Area's own"
		{name: "Time zone other than the Area's own", area: new("US-CA"), tz: new("America/New_York")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			d := newApprovalTestDeps(t, artist)
			sc := d.candidate(tc.area, tc.zone, nil)

			// Nothing changes: no venue lookup, insert or delete is expected.
			assert.ErrorIs(t, d.approve(sc, tc.give, tc.tz), apperr.ErrInvalidArgument)
		})
	}

	t.Run("Catalog down while creating the venue", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/concert-candidate/approve "Catalog down while creating the venue"
		d := newApprovalTestDeps(t, artist)
		sc := d.candidate(new("JP-13"), nil, new("P"))
		d.venueRepo.EXPECT().GetByPlaceID(mock.Anything, "P").Return(nil, notFound("venue")).Once()
		d.venueRepo.EXPECT().GetByListedName(mock.Anything, "Zepp Haneda(TOKYO)", "JP-13").Return(nil, notFound("venue")).Once()
		d.places.EXPECT().GetPlaceCoordinates(mock.Anything, "P").Return(nil, apperr.New(codes.Unavailable, "catalog down")).Once()

		// The candidate is kept: no Create or Delete is expected.
		assert.ErrorIs(t, d.approve(sc, nil, nil), apperr.ErrUnavailable)
	})

	t.Run("place without coordinates", func(t *testing.T) {
		t.Parallel()
		d := newApprovalTestDeps(t, artist)
		sc := d.candidate(new("JP-13"), nil, new("P"))
		d.createsVenue(new("P"), "Zepp Haneda(TOKYO)", "JP-13")
		d.places.EXPECT().GetPlaceCoordinates(mock.Anything, "P").Return(nil, notFound("place")).Once()
		d.slotFree()
		d.inserts()
		d.deletes(sc.ID)

		require.NoError(t, d.approve(sc, nil, nil))

		assert.Equal(t, "P", *d.createdVenues[0].GooglePlaceID)
		assert.Nil(t, d.createdVenues[0].Coordinates)
	})

	t.Run("keep the concert candidate when the concert cannot be added", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/concert-candidate/approve "Concert cannot be added"
		d := newApprovalTestDeps(t, artist)
		sc := d.candidate(new("JP-13"), nil, nil)
		d.createsVenue(nil, "Zepp Haneda(TOKYO)", "JP-13")
		d.slotFree()
		d.concertRepo.EXPECT().Create(mock.Anything, mock.Anything).
			Return(nil, apperr.New(codes.Unavailable, "failed to record the announcement")).Once()

		assert.ErrorIs(t, d.approve(sc, nil, nil), apperr.ErrUnavailable)
	})

	t.Run("approve is idempotent when concert candidate is already gone", func(t *testing.T) {
		t.Parallel()
		d := newApprovalTestDeps(t, artist)
		d.gone("candidate-nonexistent")

		_, err := d.uc.Approve(context.Background(), "candidate-nonexistent", usecase.ApproveResolutionUnspecified, nil, nil, "")
		require.NoError(t, err)
	})
}

// seedConflict wires a pending concert candidate that duplicates an
// already-published event at the same (venue, date, start time): its listed
// name resolves to the stored venue-1, where a known-start event of series-1
// is stored.
func seedConflict(d *approvalTestDeps, artistID string) *entity.ConcertCandidate {
	d.venueRepo.EXPECT().GetByListedName(mock.Anything, "Venue ABC", "JP-13").Return(&entity.Venue{
		ID:              "venue-1",
		Name:            "Venue One",
		ListedVenueName: new("Venue ABC"),
	}, nil).Once()
	day := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	start := time.Date(2026, 8, 1, 19, 0, 0, 0, time.UTC)
	d.concertRepo.EXPECT().FindEventsByVenueAndDate(mock.Anything, []string{"venue-1"}, []time.Time{day}).
		Return([]*entity.Event{{
			ID:              "event-1",
			SeriesID:        "series-1",
			VenueID:         "venue-1",
			LocalDate:       day,
			StartTime:       tod(start),
			ListedVenueName: new("Old Listed Name"),
		}}, nil).Once()
	sc := &entity.ConcertCandidate{
		ID:              "candidate-dup",
		ArtistID:        artistID,
		Title:           "Candidate Title",
		LocalDate:       day,
		ListedVenueName: "Venue ABC",
		StartTime:       tod(start),
		AdminArea:       new("JP-13"),
	}
	d.pending(sc)
	return sc
}

func TestConcertCandidateUseCase_Approve_Reconciliation(t *testing.T) {
	t.Parallel()

	artist := &entity.Artist{ID: "artist-1", Name: "Test Artist", MBID: "11111111-1111-1111-1111-111111111111"}

	t.Run("unresolved duplicate returns a conflict and does not mutate", func(t *testing.T) {
		t.Parallel()
		d := newApprovalTestDeps(t, artist)
		sc := seedConflict(d, artist.ID)
		d.seriesRepo.EXPECT().Get(mock.Anything, "series-1").Return(&entity.Series{ID: "series-1", Title: "Existing Title"}, nil).Once()
		d.artistRepo.EXPECT().Get(mock.Anything, artist.ID).Return(artist, nil).Once()

		// No mutation: no Delete, Create, Append or UpdateEventListedVenueName is
		// expected, so the concert candidate is preserved.
		result, err := d.uc.Approve(context.Background(), sc.ID, usecase.ApproveResolutionUnspecified, nil, nil, "reviewer-1")
		require.NoError(t, err)

		require.NotNil(t, result.Conflict)
		assert.Equal(t, "event-1", result.Conflict.Existing.EventID)
		assert.Equal(t, "Existing Title", result.Conflict.Existing.Title)
		assert.Equal(t, "Old Listed Name", result.Conflict.Existing.ListedVenueName)
		require.NotNil(t, result.Conflict.ConcertCandidate)
		assert.Equal(t, "candidate-dup", result.Conflict.ConcertCandidate.ID)
		require.NotNil(t, result.Conflict.ConcertCandidatePerformer)
	})

	t.Run("KEEP_EXISTING logs the concert candidate and clears it, leaving the event unchanged", func(t *testing.T) {
		t.Parallel()
		d := newApprovalTestDeps(t, artist)
		sc := seedConflict(d, artist.ID)
		d.logsRejection()
		d.deletes(sc.ID)

		// The event is untouched: no Create or UpdateEventListedVenueName is expected.
		result, err := d.uc.Approve(context.Background(), sc.ID, usecase.ApproveResolutionKeepExisting, nil, nil, "reviewer-1")
		require.NoError(t, err)
		assert.Nil(t, result.Conflict)

		// Concert candidate logged with the reviewer.
		require.Len(t, d.rejections, 1)
		assert.Contains(t, d.rejections[0].Reason, "duplicate")
		require.NotNil(t, d.rejections[0].ReviewedBy)
		assert.Equal(t, "reviewer-1", *d.rejections[0].ReviewedBy)
	})

	t.Run("ADOPT_CANDIDATE overwrites the display fields and clears the concert candidate", func(t *testing.T) {
		t.Parallel()
		d := newApprovalTestDeps(t, artist)
		sc := seedConflict(d, artist.ID)
		// Existing event's listed name overwritten from the concert candidate;
		// start/open filled via COALESCE; concert candidate cleared; nothing
		// logged or newly created (no Append or Create is expected).
		d.concertRepo.EXPECT().UpdateEventListedVenueName(mock.Anything, "event-1", "Venue ABC").Return(nil).Once()
		d.concertRepo.EXPECT().FillEventStartTimes(mock.Anything, []string{"event-1"}, mock.Anything, mock.Anything).Return(nil).Once()
		d.deletes(sc.ID)

		result, err := d.uc.Approve(context.Background(), sc.ID, usecase.ApproveResolutionAdoptCandidate, nil, nil, "reviewer-1")
		require.NoError(t, err)
		assert.Nil(t, result.Conflict)
	})

	t.Run("second reconcile call is idempotent once the concert candidate is gone", func(t *testing.T) {
		t.Parallel()
		d := newApprovalTestDeps(t, artist)
		sc := seedConflict(d, artist.ID)
		d.logsRejection()
		d.deletes(sc.ID)
		// The second read finds the concert candidate already cleared.
		d.gone(sc.ID)

		_, err := d.uc.Approve(context.Background(), sc.ID, usecase.ApproveResolutionKeepExisting, nil, nil, "reviewer-1")
		require.NoError(t, err)

		// Second call: concert candidate already cleared → non-conflict success,
		// with no event mutation (no UpdateEventListedVenueName is expected).
		result, err := d.uc.Approve(context.Background(), sc.ID, usecase.ApproveResolutionAdoptCandidate, nil, nil, "reviewer-1")
		require.NoError(t, err)
		assert.Nil(t, result.Conflict)
		// No second log entry.
		assert.Len(t, d.rejections, 1)
	})
}

func TestConcertCandidateUseCase_Reject(t *testing.T) {
	t.Parallel()

	artist := &entity.Artist{ID: "artist-1", Name: "Test Artist", MBID: "11111111-1111-1111-1111-111111111111"}

	t.Run("reject appends log entry and deletes concert candidate", func(t *testing.T) {
		t.Parallel()
		d := newApprovalTestDeps(t, artist)
		sc := d.candidate(new("JP-13"), new("Asia/Tokyo"), new("P"))
		d.logsRejection()
		d.deletes(sc.ID)
		d.seriesRepo.EXPECT().DeleteOrphaned(mock.Anything, []string{sc.SeriesID}).Return(0, nil).Once()

		err := d.uc.Reject(context.Background(), sc.ID, "wrong artist", "reviewer@example.com")
		require.NoError(t, err)

		// Rejection log has one entry.
		require.Len(t, d.rejections, 1)
		logEntry := d.rejections[0]
		assert.Equal(t, artist.ID, logEntry.ArtistID)
		assert.Equal(t, artist.Name, logEntry.ArtistName)
		assert.Equal(t, sc.Title, logEntry.Title)
		assert.Equal(t, "wrong artist", logEntry.Reason)
		require.NotNil(t, logEntry.ReviewedBy)
		assert.Equal(t, "reviewer@example.com", *logEntry.ReviewedBy)
		assert.Equal(t, "Asia/Tokyo", *logEntry.TimeZone, "the candidate's time zone is kept")
		assert.Equal(t, "P", *logEntry.ResolvedPlaceID)
	})

	t.Run("reject is idempotent when concert candidate is already gone", func(t *testing.T) {
		t.Parallel()
		d := newApprovalTestDeps(t, artist)
		d.gone("candidate-nonexistent")

		// No log entry is created for a row that was not found: no Append is
		// expected.
		err := d.uc.Reject(context.Background(), "candidate-nonexistent", "reason", "reviewer")
		require.NoError(t, err)
	})

	t.Run("reject with empty reviewed_by sets ReviewedBy to nil in the log", func(t *testing.T) {
		t.Parallel()
		d := newApprovalTestDeps(t, artist)
		sc := d.candidate(new("JP-13"), new("Asia/Tokyo"), new("P"))
		d.logsRejection()
		d.deletes(sc.ID)
		d.seriesRepo.EXPECT().DeleteOrphaned(mock.Anything, []string{sc.SeriesID}).Return(0, nil).Once()

		err := d.uc.Reject(context.Background(), sc.ID, "bad data", "")
		require.NoError(t, err)

		require.Len(t, d.rejections, 1)
		assert.Nil(t, d.rejections[0].ReviewedBy)
	})
}
