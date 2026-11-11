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
	uc              usecase.ConcertCandidateUseCase

	createdVenues   []*entity.Venue
	createdConcerts []*entity.Concert
	rejections      []*entity.RejectedConcert
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
	}
	// Pass nil for repos/deps that Approve/Reject/ListPending/List/Delete never touch:
	// concertSearchRepo, concertSearcher, centroidResolver, and metrics.
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
		nil, // centroidResolver — not used by admin methods
		ucmocks.NewMockWorkflows(t),
		noopMetrics{},
		nil,         // analytics — not used by admin methods
		0,           // searchCacheTTL — not used by admin methods
		0, time.Now, // discoveryWindow — not used by admin methods
		newTestLogger(t),
	)
	d.uc = usecase.NewConcertCandidateUseCase(concerts)
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

// createsVenue expects the venue of placeID, stored under neither that place
// id nor (listedName, adminArea), to be created, and records it.
func (d *approvalTestDeps) createsVenue(placeID, listedName string, adminArea *string) {
	d.venueRepo.EXPECT().GetByPlaceID(mock.Anything, placeID).Return(nil, notFound("venue")).Once()
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

// seedCandidate returns a pending concert candidate whose place resolves to
// no stored venue. The concert candidate always carries a SeriesID referencing
// a pre-existing series (created at discovery time), matching the model where
// Approve never mints a series.
func seedCandidate(d *approvalTestDeps, artistID string) *entity.ConcertCandidate {
	placeID := "place-abc"
	venueName := "Venue ABC Canonical"
	sourceURL := "https://example.com/show"
	sc := &entity.ConcertCandidate{
		ID:                "candidate-001",
		ArtistID:          artistID,
		SeriesID:          "series-candidate-001",
		Title:             "Approval Test Concert",
		LocalDate:         time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
		ListedVenueName:   "Venue ABC",
		SourceURL:         &sourceURL,
		ResolvedPlaceID:   &placeID,
		ResolvedVenueName: &venueName,
	}
	d.pending(sc)
	return sc
}

func TestConcertCandidateUseCase_Approve(t *testing.T) {
	t.Parallel()

	artist := &entity.Artist{ID: "artist-1", Name: "Test Artist", MBID: "11111111-1111-1111-1111-111111111111"}

	t.Run("approve inserts concert and deletes concert candidate", func(t *testing.T) {
		// @spec components/usecase/concert-candidate/approve "New concert"
		t.Parallel()
		d := newApprovalTestDeps(t, artist)
		sc := seedCandidate(d, artist.ID)
		d.createsVenue("place-abc", "Venue ABC", nil)
		d.slotFree()
		d.inserts()
		// Approve NEVER mints a series (no Series.Create is expected) — the series
		// row already exists (created at discovery time, referenced via
		// ConcertCandidate.SeriesID).
		d.deletes(sc.ID)

		_, err := d.uc.Approve(context.Background(), sc.ID, usecase.ApproveResolutionUnspecified, "")
		require.NoError(t, err)

		// Concert was created.
		require.Len(t, d.createdConcerts, 1)
		assert.Equal(t, artist.ID, d.createdConcerts[0].ArtistIDs()[0])
		assert.Equal(t, sc.SeriesID, d.createdConcerts[0].SeriesID,
			"approved concert must be inserted under the concert candidate's existing series ID")

		// Venue was created from resolved fields.
		require.Len(t, d.createdVenues, 1)
		assert.Equal(t, "Venue ABC Canonical", d.createdVenues[0].Name)
	})

	t.Run("keep the concert candidate when the concert cannot be added", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/concert-candidate/approve "Concert cannot be added"
		d := newApprovalTestDeps(t, artist)
		sc := seedCandidate(d, artist.ID)
		d.createsVenue("place-abc", "Venue ABC", nil)
		d.slotFree()
		d.concertRepo.EXPECT().Create(mock.Anything, mock.Anything).
			Return(nil, apperr.New(codes.Unavailable, "failed to record the announcement")).Once()

		// The concert candidate is kept: no Delete is expected.
		_, err := d.uc.Approve(context.Background(), sc.ID, usecase.ApproveResolutionUnspecified, "")

		assert.ErrorIs(t, err, apperr.ErrUnavailable)
	})

	t.Run("approve is idempotent when concert candidate is already gone", func(t *testing.T) {
		t.Parallel()
		d := newApprovalTestDeps(t, artist)
		d.gone("candidate-nonexistent")

		// No concerts or venues are created: no Create is expected.
		_, err := d.uc.Approve(context.Background(), "candidate-nonexistent", usecase.ApproveResolutionUnspecified, "")
		require.NoError(t, err)
	})

	t.Run("approve reuses an existing venue by place_id", func(t *testing.T) {
		t.Parallel()
		d := newApprovalTestDeps(t, artist)
		sc := seedCandidate(d, artist.ID)
		// A venue with the same place_id is stored, so it is reused, not
		// re-created (no Venue.Create is expected).
		existingPlaceID := "place-abc"
		d.venueRepo.EXPECT().GetByPlaceID(mock.Anything, existingPlaceID).Return(&entity.Venue{
			ID:            "venue-existing",
			Name:          "Venue ABC Canonical",
			GooglePlaceID: &existingPlaceID,
		}, nil).Once()
		d.slotFree()
		d.inserts()
		d.deletes(sc.ID)

		_, err := d.uc.Approve(context.Background(), sc.ID, usecase.ApproveResolutionUnspecified, "")
		require.NoError(t, err)

		require.Len(t, d.createdConcerts, 1)
		assert.Equal(t, "venue-existing", d.createdConcerts[0].VenueID)
	})

	t.Run("approve resolves an existing venue by listed name when place_id differs", func(t *testing.T) {
		t.Parallel()
		d := newApprovalTestDeps(t, artist)
		// Existing venue already carries a place_id (P1) — Google Places resolved a
		// DIFFERENT place_id (P2) for the same physical venue on this discovery.
		existingPlaceID := "place-P1"
		differentPlaceID := "place-P2"
		d.venueRepo.EXPECT().GetByPlaceID(mock.Anything, differentPlaceID).Return(nil, notFound("venue")).Once()
		// A non-NULL place_id is never overwritten: no BackfillPlaceID is
		// expected, and the venue is reused, not re-created.
		d.venueRepo.EXPECT().GetByListedName(mock.Anything, "Venue ABC", new("JP-27")).Return(&entity.Venue{
			ID:              "venue-existing",
			Name:            "Venue ABC Canonical",
			AdminArea:       new("JP-27"),
			GooglePlaceID:   &existingPlaceID,
			ListedVenueName: new("Venue ABC"),
		}, nil).Once()
		d.slotFree()
		d.inserts()

		venueName := "Venue ABC Canonical"
		sc := &entity.ConcertCandidate{
			ID:                "candidate-diff-placeid",
			ArtistID:          artist.ID,
			Title:             "Approval Test Concert",
			LocalDate:         time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
			ListedVenueName:   "Venue ABC",
			AdminArea:         new("JP-27"),
			ResolvedPlaceID:   &differentPlaceID,
			ResolvedVenueName: &venueName,
			ResolvedAdminArea: new("JP-27"),
		}
		d.pending(sc)
		d.deletes(sc.ID)

		_, err := d.uc.Approve(context.Background(), sc.ID, usecase.ApproveResolutionUnspecified, "")
		require.NoError(t, err)

		require.Len(t, d.createdConcerts, 1)
		assert.Equal(t, "venue-existing", d.createdConcerts[0].VenueID)
	})

	t.Run("approve backfills a NULL place_id on the resolved venue", func(t *testing.T) {
		t.Parallel()
		d := newApprovalTestDeps(t, artist)
		resolvedPlaceID := "place-P2"
		d.venueRepo.EXPECT().GetByPlaceID(mock.Anything, resolvedPlaceID).Return(nil, notFound("venue")).Once()
		// Existing venue was created before Places resolution — its place_id is NULL.
		d.venueRepo.EXPECT().GetByListedName(mock.Anything, "Venue ABC", new("JP-27")).Return(&entity.Venue{
			ID:              "venue-existing",
			Name:            "Venue ABC Canonical",
			AdminArea:       new("JP-27"),
			ListedVenueName: new("Venue ABC"),
		}, nil).Once()
		// The venue is reused (no Venue.Create is expected), and its NULL
		// place_id is backfilled from the concert candidate.
		d.venueRepo.EXPECT().BackfillPlaceID(mock.Anything, "venue-existing", "place-P2").Return(nil).Once()
		d.slotFree()
		d.inserts()

		venueName := "Venue ABC Canonical"
		sc := &entity.ConcertCandidate{
			ID:                "candidate-backfill",
			ArtistID:          artist.ID,
			Title:             "Approval Test Concert",
			LocalDate:         time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
			ListedVenueName:   "Venue ABC",
			AdminArea:         new("JP-27"),
			ResolvedPlaceID:   &resolvedPlaceID,
			ResolvedVenueName: &venueName,
			ResolvedAdminArea: new("JP-27"),
		}
		d.pending(sc)
		d.deletes(sc.ID)

		_, err := d.uc.Approve(context.Background(), sc.ID, usecase.ApproveResolutionUnspecified, "")
		require.NoError(t, err)
	})

	t.Run("approve uses the resolved admin_area symmetrically for the listed-name lookup", func(t *testing.T) {
		t.Parallel()
		d := newApprovalTestDeps(t, artist)
		// Existing venue lives under the RESOLVED admin_area (JP-27): the lookup
		// MUST use the resolved value to find it, and the venue is reused, not
		// re-created under the raw one (no Venue.Create is expected).
		d.venueRepo.EXPECT().GetByListedName(mock.Anything, "Venue ABC", new("JP-27")).Return(&entity.Venue{
			ID:              "venue-existing",
			Name:            "Venue ABC Canonical",
			AdminArea:       new("JP-27"),
			ListedVenueName: new("Venue ABC"),
		}, nil).Once()
		d.slotFree()
		d.inserts()

		// Concert candidate's raw admin_area (JP-99) disagrees with the resolved one
		// (JP-27). No place_id is set so resolution goes straight to the
		// listed-name lookup.
		venueName := "Venue ABC Canonical"
		sc := &entity.ConcertCandidate{
			ID:                "candidate-admin-symmetry",
			ArtistID:          artist.ID,
			Title:             "Approval Test Concert",
			LocalDate:         time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
			ListedVenueName:   "Venue ABC",
			AdminArea:         new("JP-99"),
			ResolvedVenueName: &venueName,
			ResolvedAdminArea: new("JP-27"),
		}
		d.pending(sc)
		d.deletes(sc.ID)

		_, err := d.uc.Approve(context.Background(), sc.ID, usecase.ApproveResolutionUnspecified, "")
		require.NoError(t, err)

		require.Len(t, d.createdConcerts, 1)
		assert.Equal(t, "venue-existing", d.createdConcerts[0].VenueID)
	})
}

// seedConflict wires a pending concert candidate that duplicates an
// already-published event at the same (venue, date, start time): its listed
// name resolves to the stored venue-1, where a known-start event of series-1
// is stored.
func seedConflict(d *approvalTestDeps, artistID string) *entity.ConcertCandidate {
	d.venueRepo.EXPECT().GetByListedName(mock.Anything, "Venue ABC", (*string)(nil)).Return(&entity.Venue{
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
			StartTime:       &start,
			ListedVenueName: new("Old Listed Name"),
		}}, nil).Once()
	sc := &entity.ConcertCandidate{
		ID:              "candidate-dup",
		ArtistID:        artistID,
		Title:           "Candidate Title",
		LocalDate:       day,
		ListedVenueName: "Venue ABC",
		StartTime:       &start,
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
		result, err := d.uc.Approve(context.Background(), sc.ID, usecase.ApproveResolutionUnspecified, "reviewer-1")
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
		result, err := d.uc.Approve(context.Background(), sc.ID, usecase.ApproveResolutionKeepExisting, "reviewer-1")
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

		result, err := d.uc.Approve(context.Background(), sc.ID, usecase.ApproveResolutionAdoptCandidate, "reviewer-1")
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

		_, err := d.uc.Approve(context.Background(), sc.ID, usecase.ApproveResolutionKeepExisting, "reviewer-1")
		require.NoError(t, err)

		// Second call: concert candidate already cleared → non-conflict success,
		// with no event mutation (no UpdateEventListedVenueName is expected).
		result, err := d.uc.Approve(context.Background(), sc.ID, usecase.ApproveResolutionAdoptCandidate, "reviewer-1")
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
		sc := seedCandidate(d, artist.ID)
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
		sc := seedCandidate(d, artist.ID)
		d.logsRejection()
		d.deletes(sc.ID)
		d.seriesRepo.EXPECT().DeleteOrphaned(mock.Anything, []string{sc.SeriesID}).Return(0, nil).Once()

		err := d.uc.Reject(context.Background(), sc.ID, "bad data", "")
		require.NoError(t, err)

		require.Len(t, d.rejections, 1)
		assert.Nil(t, d.rejections[0].ReviewedBy)
	})
}
