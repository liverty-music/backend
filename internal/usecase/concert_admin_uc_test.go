package usecase_test

import (
	"context"
	"testing"
	"time"

	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/messaging"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- test doubles for admin UC ---

// fakeRejectedConcertLogRepo is an in-memory append-only log repo.
type fakeRejectedConcertLogRepo struct {
	entries []*entity.RejectedConcertLog
}

func (r *fakeRejectedConcertLogRepo) Append(_ context.Context, log *entity.RejectedConcertLog) error {
	r.entries = append(r.entries, log)
	return nil
}

// fakeArtistRepo is a minimal artist repository for admin UC tests.
type fakeArtistRepo struct {
	artists map[string]*entity.Artist
}

func newFakeArtistRepo(artists ...*entity.Artist) *fakeArtistRepo {
	r := &fakeArtistRepo{artists: make(map[string]*entity.Artist)}
	for _, a := range artists {
		r.artists[a.ID] = a
	}
	return r
}

func (r *fakeArtistRepo) Get(_ context.Context, id string) (*entity.Artist, error) {
	if a, ok := r.artists[id]; ok {
		return a, nil
	}
	return nil, apperr.New(codes.NotFound, "artist not found")
}

func (r *fakeArtistRepo) GetOfficialSite(_ context.Context, _ string) (*entity.OfficialSite, error) {
	return nil, apperr.New(codes.NotFound, "official site not found")
}

func (r *fakeArtistRepo) List(_ context.Context) ([]*entity.Artist, error) { return nil, nil }
func (r *fakeArtistRepo) Create(_ context.Context, _ ...*entity.Artist) ([]*entity.Artist, error) {
	return nil, nil
}
func (r *fakeArtistRepo) GetByMBID(_ context.Context, _ string) (*entity.Artist, error) {
	return nil, apperr.New(codes.NotFound, "not found")
}
func (r *fakeArtistRepo) ListByMBIDs(_ context.Context, _ []string) ([]*entity.Artist, error) {
	return nil, nil
}
func (r *fakeArtistRepo) ListByIDs(_ context.Context, _ []string) ([]*entity.Artist, error) {
	return nil, nil
}
func (r *fakeArtistRepo) UpdateName(_ context.Context, _, _ string) error { return nil }
func (r *fakeArtistRepo) CreateOfficialSite(_ context.Context, _ *entity.OfficialSite) error {
	return nil
}
func (r *fakeArtistRepo) UpdateFanart(_ context.Context, _ string, _ *entity.Fanart, _ time.Time) error {
	return nil
}
func (r *fakeArtistRepo) ListStaleOrMissingFanart(_ context.Context, _ time.Duration, _ int) ([]*entity.Artist, error) {
	return nil, nil
}

// approvalTestDeps bundles dependencies for AdminConcertUseCase tests.
type approvalTestDeps struct {
	stagedRepo  *fakeStagedConcertRepo
	rejectedLog *fakeRejectedConcertLogRepo
	venueRepo   *fakeVenueRepo
	seriesRepo  *fakeSeriesRepo
	concertRepo *fakeConcertRepo
	artistRepo  *fakeArtistRepo
	publisher   interface {
		Subscribe(ctx context.Context, topic string) (<-chan *message.Message, error)
	}
	uc usecase.AdminConcertUseCase
}

func newApprovalTestDeps(t *testing.T, artist *entity.Artist) *approvalTestDeps {
	t.Helper()
	pub := newGoChannelPub(t)
	d := &approvalTestDeps{
		stagedRepo:  &fakeStagedConcertRepo{},
		rejectedLog: &fakeRejectedConcertLogRepo{},
		venueRepo:   newFakeVenueRepo(),
		seriesRepo:  &fakeSeriesRepo{},
		concertRepo: &fakeConcertRepo{},
		artistRepo:  newFakeArtistRepo(artist),
		publisher:   pub,
	}
	// Pass nil for repos/deps that Approve/Reject/ListPending/List/Delete never touch:
	// searchLogRepo, concertSearcher, centroidResolver, and metrics.
	d.uc = usecase.NewConcertUseCase(
		d.artistRepo,
		d.concertRepo,
		d.venueRepo,
		d.seriesRepo,
		nil, // searchLogRepo — not used by admin methods
		d.stagedRepo,
		d.rejectedLog,
		nil, // concertSearcher — not used by admin methods
		nil, // centroidResolver — not used by admin methods
		messaging.NewEventPublisher(pub),
		noopMetrics{},
		0, // searchCacheTTL — not used by admin methods
		0, // discoveryWindow — not used by admin methods
		newTestLogger(t),
	)
	t.Cleanup(func() { _ = pub.Close() })
	return d
}

// seedStaged inserts a staged concert into the fake repo and returns it.
func seedStaged(d *approvalTestDeps, artistID string) *entity.StagedConcert {
	placeID := "place-abc"
	venueName := "Venue ABC Canonical"
	sourceURL := "https://example.com/show"
	sc := &entity.StagedConcert{
		ID:                "staged-001",
		ArtistID:          artistID,
		Title:             "Approval Test Concert",
		LocalDate:         time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
		ListedVenueName:   "Venue ABC",
		SourceURL:         &sourceURL,
		ResolvedPlaceID:   &placeID,
		ResolvedVenueName: &venueName,
	}
	d.stagedRepo.upserted = append(d.stagedRepo.upserted, sc)
	return sc
}

func TestAdminConcertUseCase_Approve(t *testing.T) {
	t.Parallel()

	artist := &entity.Artist{ID: "artist-1", Name: "Test Artist", MBID: "11111111-1111-1111-1111-111111111111"}

	t.Run("approve inserts concert, publishes CONCERT.created, and deletes staged row", func(t *testing.T) {
		t.Parallel()
		d := newApprovalTestDeps(t, artist)
		sc := seedStaged(d, artist.ID)

		ctx := context.Background()
		sub, err := d.publisher.Subscribe(ctx, entity.SubjectConcertCreated)
		require.NoError(t, err)

		_, err = d.uc.Approve(ctx, sc.ID, usecase.ApproveResolutionUnspecified, "")
		require.NoError(t, err)

		// Concert was created.
		assert.Len(t, d.concertRepo.created, 1)
		assert.Equal(t, artist.ID, d.concertRepo.created[0].PerformerIDs()[0])

		// Series was created.
		assert.Len(t, d.seriesRepo.created, 1)
		assert.Equal(t, sc.Title, d.seriesRepo.created[0].Title)

		// Venue was created from resolved fields.
		assert.Len(t, d.venueRepo.created, 1)
		assert.Equal(t, "Venue ABC Canonical", d.venueRepo.created[0].Name)

		// Staged row was deleted.
		assert.Empty(t, d.stagedRepo.upserted)

		// CONCERT.created was published.
		select {
		case msg := <-sub:
			msg.Ack()
			var published usecase.ConcertCreatedData
			require.NoError(t, messaging.ParseCloudEventData(msg, &published))
			assert.Equal(t, artist.ID, published.ArtistID)
			assert.NotEmpty(t, published.ConcertIDs)
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for CONCERT.created event")
		}
	})

	t.Run("approve is idempotent when staged row is already gone", func(t *testing.T) {
		t.Parallel()
		d := newApprovalTestDeps(t, artist)
		// Do NOT seed — staged row does not exist.

		_, err := d.uc.Approve(context.Background(), "staged-nonexistent", usecase.ApproveResolutionUnspecified, "")
		require.NoError(t, err)

		// No concerts or venues created.
		assert.Empty(t, d.concertRepo.created)
		assert.Empty(t, d.venueRepo.created)
	})

	t.Run("approve reuses an existing venue by place_id", func(t *testing.T) {
		t.Parallel()
		d := newApprovalTestDeps(t, artist)
		// Pre-seed a venue with the same place_id.
		existingPlaceID := "place-abc"
		existingVenue := &entity.Venue{
			ID:            "venue-existing",
			Name:          "Venue ABC Canonical",
			GooglePlaceID: &existingPlaceID,
		}
		d.venueRepo.venues["Venue ABC Canonical"] = existingVenue

		sc := seedStaged(d, artist.ID)

		_, err := d.uc.Approve(context.Background(), sc.ID, usecase.ApproveResolutionUnspecified, "")
		require.NoError(t, err)

		// Venue was reused, not re-created.
		assert.Empty(t, d.venueRepo.created)
		require.Len(t, d.concertRepo.created, 1)
		assert.Equal(t, "venue-existing", d.concertRepo.created[0].VenueID)
	})

	t.Run("approve resolves an existing venue by listed name when place_id differs", func(t *testing.T) {
		t.Parallel()
		d := newApprovalTestDeps(t, artist)
		// Existing venue already carries a place_id (P1) — Google Places resolved a
		// DIFFERENT place_id (P2) for the same physical venue on this discovery.
		existingPlaceID := "place-P1"
		existingVenue := &entity.Venue{
			ID:              "venue-existing",
			Name:            "Venue ABC Canonical",
			AdminArea:       new("JP-27"),
			GooglePlaceID:   &existingPlaceID,
			ListedVenueName: new("Venue ABC"),
		}
		d.venueRepo.venues["Venue ABC Canonical"] = existingVenue

		differentPlaceID := "place-P2"
		venueName := "Venue ABC Canonical"
		sc := &entity.StagedConcert{
			ID:                "staged-diff-placeid",
			ArtistID:          artist.ID,
			Title:             "Approval Test Concert",
			LocalDate:         time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
			ListedVenueName:   "Venue ABC",
			AdminArea:         new("JP-27"),
			ResolvedPlaceID:   &differentPlaceID,
			ResolvedVenueName: &venueName,
			ResolvedAdminArea: new("JP-27"),
		}
		d.stagedRepo.upserted = append(d.stagedRepo.upserted, sc)

		_, err := d.uc.Approve(context.Background(), sc.ID, usecase.ApproveResolutionUnspecified, "")
		require.NoError(t, err)

		// Venue was reused via the listed-name fallback, not re-created.
		assert.Empty(t, d.venueRepo.created)
		require.Len(t, d.concertRepo.created, 1)
		assert.Equal(t, "venue-existing", d.concertRepo.created[0].VenueID)
		// A non-NULL place_id is never overwritten by the backfill.
		require.NotNil(t, existingVenue.GooglePlaceID)
		assert.Equal(t, "place-P1", *existingVenue.GooglePlaceID)
	})

	t.Run("approve backfills a NULL place_id on the resolved venue", func(t *testing.T) {
		t.Parallel()
		d := newApprovalTestDeps(t, artist)
		// Existing venue was created before Places resolution — its place_id is NULL.
		existingVenue := &entity.Venue{
			ID:              "venue-existing",
			Name:            "Venue ABC Canonical",
			AdminArea:       new("JP-27"),
			ListedVenueName: new("Venue ABC"),
		}
		d.venueRepo.venues["Venue ABC Canonical"] = existingVenue

		resolvedPlaceID := "place-P2"
		venueName := "Venue ABC Canonical"
		sc := &entity.StagedConcert{
			ID:                "staged-backfill",
			ArtistID:          artist.ID,
			Title:             "Approval Test Concert",
			LocalDate:         time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
			ListedVenueName:   "Venue ABC",
			AdminArea:         new("JP-27"),
			ResolvedPlaceID:   &resolvedPlaceID,
			ResolvedVenueName: &venueName,
			ResolvedAdminArea: new("JP-27"),
		}
		d.stagedRepo.upserted = append(d.stagedRepo.upserted, sc)

		_, err := d.uc.Approve(context.Background(), sc.ID, usecase.ApproveResolutionUnspecified, "")
		require.NoError(t, err)

		// Venue reused, and its NULL place_id was backfilled from the staged row.
		assert.Empty(t, d.venueRepo.created)
		require.NotNil(t, existingVenue.GooglePlaceID)
		assert.Equal(t, "place-P2", *existingVenue.GooglePlaceID)
	})

	t.Run("approve uses the resolved admin_area symmetrically for the listed-name lookup", func(t *testing.T) {
		t.Parallel()
		d := newApprovalTestDeps(t, artist)
		// Existing venue lives under the RESOLVED admin_area (JP-27).
		existingVenue := &entity.Venue{
			ID:              "venue-existing",
			Name:            "Venue ABC Canonical",
			AdminArea:       new("JP-27"),
			ListedVenueName: new("Venue ABC"),
		}
		d.venueRepo.venues["Venue ABC Canonical"] = existingVenue

		// Staged row's raw admin_area (JP-99) disagrees with the resolved one
		// (JP-27); the lookup MUST use the resolved value to find the venue. No
		// place_id is set so resolution goes straight to the listed-name lookup.
		venueName := "Venue ABC Canonical"
		sc := &entity.StagedConcert{
			ID:                "staged-admin-symmetry",
			ArtistID:          artist.ID,
			Title:             "Approval Test Concert",
			LocalDate:         time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
			ListedVenueName:   "Venue ABC",
			AdminArea:         new("JP-99"),
			ResolvedVenueName: &venueName,
			ResolvedAdminArea: new("JP-27"),
		}
		d.stagedRepo.upserted = append(d.stagedRepo.upserted, sc)

		_, err := d.uc.Approve(context.Background(), sc.ID, usecase.ApproveResolutionUnspecified, "")
		require.NoError(t, err)

		// Reused via the resolved admin_area, not re-created under the raw one.
		assert.Empty(t, d.venueRepo.created)
		require.Len(t, d.concertRepo.created, 1)
		assert.Equal(t, "venue-existing", d.concertRepo.created[0].VenueID)
	})

	t.Run("CONCERT.created is published ONLY from Approve, never from CreateFromDiscovered", func(t *testing.T) {
		t.Parallel()
		// This test verifies the architectural guarantee: the discovery path
		// (CreateFromDiscovered) must never publish CONCERT.created; only Approve does.

		// CreateFromDiscovered side: stage without publishing.
		stagedRepo := &fakeStagedConcertRepo{}
		ps := newStubPlaceSearcher()
		ps.places["Hall X"] = &entity.VenuePlace{ExternalID: "place-x", Name: "Hall X Canonical"}
		discoveryUC := usecase.NewConcertCreationUseCase(stagedRepo, ps, newTestLogger(t))

		pubForDiscovery := newGoChannelPub(t)
		ctx := context.Background()
		createdCh, err := pubForDiscovery.Subscribe(ctx, entity.SubjectConcertCreated)
		require.NoError(t, err)

		data := entity.ConcertDiscoveredData{
			ArtistID: "artist-1",
			Concerts: entity.ScrapedConcerts{
				{Title: "Show", ListedVenueName: "Hall X", LocalDate: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC), SourceURL: "https://example.com"},
			},
		}
		require.NoError(t, discoveryUC.CreateFromDiscovered(ctx, data))

		// One row staged.
		assert.Len(t, stagedRepo.upserted, 1)

		// No CONCERT.created from discovery.
		select {
		case msg := <-createdCh:
			t.Fatalf("unexpected CONCERT.created published by discovery path: %s", msg.Payload)
		case <-time.After(50 * time.Millisecond):
			// Correct.
		}

		// Now approve: CONCERT.created should be published.
		approvalDeps := newApprovalTestDeps(t, artist)
		approvalDeps.stagedRepo.upserted = stagedRepo.upserted
		approvalCreatedCh, err := approvalDeps.publisher.Subscribe(ctx, entity.SubjectConcertCreated)
		require.NoError(t, err)

		_, err = approvalDeps.uc.Approve(ctx, stagedRepo.upserted[0].ID, usecase.ApproveResolutionUnspecified, "")
		require.NoError(t, err)

		select {
		case msg := <-approvalCreatedCh:
			msg.Ack()
		case <-time.After(2 * time.Second):
			t.Fatal("expected CONCERT.created from Approve but got none")
		}
	})
}

// seedConflict wires a staged concert that duplicates an already-published event
// at the same (venue, date, start time): the venue is pre-seeded so it resolves
// to a known id, and an existing known-start event plus its series are seeded.
func seedConflict(d *approvalTestDeps, artistID string) *entity.StagedConcert {
	d.venueRepo.venues["Venue One"] = &entity.Venue{
		ID:              "venue-1",
		Name:            "Venue One",
		ListedVenueName: new("Venue ABC"),
	}
	start := time.Date(2026, 8, 1, 19, 0, 0, 0, time.UTC)
	d.concertRepo.existing = map[string][]*entity.Event{
		"venue-1|2026-08-01": {{
			ID:              "event-1",
			SeriesID:        "series-1",
			VenueID:         "venue-1",
			LocalDate:       time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
			StartTime:       &start,
			ListedVenueName: new("Old Listed Name"),
		}},
	}
	d.seriesRepo.byID = map[string]*entity.Series{
		"series-1": {ID: "series-1", Title: "Existing Title"},
	}
	sc := &entity.StagedConcert{
		ID:              "staged-dup",
		ArtistID:        artistID,
		Title:           "Staged Title",
		LocalDate:       time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
		ListedVenueName: "Venue ABC",
		StartTime:       &start,
	}
	d.stagedRepo.upserted = append(d.stagedRepo.upserted, sc)
	return sc
}

func TestAdminConcertUseCase_Approve_Reconciliation(t *testing.T) {
	t.Parallel()

	artist := &entity.Artist{ID: "artist-1", Name: "Test Artist", MBID: "11111111-1111-1111-1111-111111111111"}

	t.Run("unresolved duplicate returns a conflict and does not mutate", func(t *testing.T) {
		t.Parallel()
		d := newApprovalTestDeps(t, artist)
		sc := seedConflict(d, artist.ID)

		result, err := d.uc.Approve(context.Background(), sc.ID, usecase.ApproveResolutionUnspecified, "reviewer-1")
		require.NoError(t, err)

		require.NotNil(t, result.Conflict)
		assert.Equal(t, "event-1", result.Conflict.Existing.EventID)
		assert.Equal(t, "Existing Title", result.Conflict.Existing.Title)
		assert.Equal(t, "Old Listed Name", result.Conflict.Existing.ListedVenueName)
		require.NotNil(t, result.Conflict.Staged)
		assert.Equal(t, "staged-dup", result.Conflict.Staged.ID)
		require.NotNil(t, result.Conflict.StagedPerformer)

		// No mutation: staged row preserved, nothing created/logged/updated.
		assert.Len(t, d.stagedRepo.upserted, 1)
		assert.Empty(t, d.concertRepo.created)
		assert.Empty(t, d.rejectedLog.entries)
		assert.Empty(t, d.concertRepo.updatedListedNames)
	})

	t.Run("KEEP_EXISTING logs the staged row and clears it, leaving the event unchanged", func(t *testing.T) {
		t.Parallel()
		d := newApprovalTestDeps(t, artist)
		sc := seedConflict(d, artist.ID)

		result, err := d.uc.Approve(context.Background(), sc.ID, usecase.ApproveResolutionKeepExisting, "reviewer-1")
		require.NoError(t, err)
		assert.Nil(t, result.Conflict)

		// Staged row logged (with the reviewer) and cleared; event untouched.
		require.Len(t, d.rejectedLog.entries, 1)
		assert.Contains(t, d.rejectedLog.entries[0].Reason, "duplicate")
		require.NotNil(t, d.rejectedLog.entries[0].ReviewedBy)
		assert.Equal(t, "reviewer-1", *d.rejectedLog.entries[0].ReviewedBy)
		assert.Empty(t, d.stagedRepo.upserted)
		assert.Empty(t, d.concertRepo.created)
		assert.Empty(t, d.concertRepo.updatedListedNames)
	})

	t.Run("ADOPT_STAGED overwrites the display fields and clears the staged row", func(t *testing.T) {
		t.Parallel()
		d := newApprovalTestDeps(t, artist)
		sc := seedConflict(d, artist.ID)

		result, err := d.uc.Approve(context.Background(), sc.ID, usecase.ApproveResolutionAdoptStaged, "reviewer-1")
		require.NoError(t, err)
		assert.Nil(t, result.Conflict)

		// Existing event's listed name overwritten from the staged row; start/open
		// filled via COALESCE; staged row cleared; nothing logged or newly created.
		assert.Equal(t, "Venue ABC", d.concertRepo.updatedListedNames["event-1"])
		assert.Contains(t, d.concertRepo.filledIDs, "event-1")
		assert.Empty(t, d.stagedRepo.upserted)
		assert.Empty(t, d.concertRepo.created)
		assert.Empty(t, d.rejectedLog.entries)
	})

	t.Run("second reconcile call is idempotent once the staged row is gone", func(t *testing.T) {
		t.Parallel()
		d := newApprovalTestDeps(t, artist)
		sc := seedConflict(d, artist.ID)

		_, err := d.uc.Approve(context.Background(), sc.ID, usecase.ApproveResolutionKeepExisting, "reviewer-1")
		require.NoError(t, err)

		// Second call: staged row already cleared → non-conflict success.
		result, err := d.uc.Approve(context.Background(), sc.ID, usecase.ApproveResolutionAdoptStaged, "reviewer-1")
		require.NoError(t, err)
		assert.Nil(t, result.Conflict)
		// No second log entry, no event mutation.
		assert.Len(t, d.rejectedLog.entries, 1)
		assert.Empty(t, d.concertRepo.updatedListedNames)
	})
}

func TestAdminConcertUseCase_Reject(t *testing.T) {
	t.Parallel()

	artist := &entity.Artist{ID: "artist-1", Name: "Test Artist", MBID: "11111111-1111-1111-1111-111111111111"}

	t.Run("reject appends log entry and deletes staged row", func(t *testing.T) {
		t.Parallel()
		d := newApprovalTestDeps(t, artist)
		sc := seedStaged(d, artist.ID)

		err := d.uc.Reject(context.Background(), sc.ID, "wrong artist", "reviewer@example.com")
		require.NoError(t, err)

		// Rejection log has one entry.
		require.Len(t, d.rejectedLog.entries, 1)
		logEntry := d.rejectedLog.entries[0]
		assert.Equal(t, artist.ID, logEntry.ArtistID)
		assert.Equal(t, artist.Name, logEntry.ArtistName)
		assert.Equal(t, sc.Title, logEntry.Title)
		assert.Equal(t, "wrong artist", logEntry.Reason)
		require.NotNil(t, logEntry.ReviewedBy)
		assert.Equal(t, "reviewer@example.com", *logEntry.ReviewedBy)

		// Staged row was deleted.
		assert.Empty(t, d.stagedRepo.upserted)
	})

	t.Run("reject is idempotent when staged row is already gone", func(t *testing.T) {
		t.Parallel()
		d := newApprovalTestDeps(t, artist)
		// Do NOT seed.

		err := d.uc.Reject(context.Background(), "staged-nonexistent", "reason", "reviewer")
		require.NoError(t, err)

		// No log entry created for a row that was not found.
		assert.Empty(t, d.rejectedLog.entries)
	})

	t.Run("reject with empty reviewed_by sets ReviewedBy to nil in the log", func(t *testing.T) {
		t.Parallel()
		d := newApprovalTestDeps(t, artist)
		sc := seedStaged(d, artist.ID)

		err := d.uc.Reject(context.Background(), sc.ID, "bad data", "")
		require.NoError(t, err)

		require.Len(t, d.rejectedLog.entries, 1)
		assert.Nil(t, d.rejectedLog.entries[0].ReviewedBy)
	})
}

func TestAdminConcertUseCase_List(t *testing.T) {
	t.Parallel()

	artist := &entity.Artist{ID: "artist-1", Name: "Test Artist", MBID: "11111111-1111-1111-1111-111111111111"}

	t.Run("return all published concerts from the repo", func(t *testing.T) {
		t.Parallel()
		d := newApprovalTestDeps(t, artist)

		// Pre-seed two published concerts.
		d.concertRepo.published = []*entity.Concert{
			{
				Event:      entity.Event{ID: "event-1"},
				Series:     &entity.Series{ID: "series-1", Title: "Tour A"},
				Performers: []*entity.Artist{artist},
			},
			{
				Event:      entity.Event{ID: "event-2"},
				Series:     &entity.Series{ID: "series-2", Title: "Tour B"},
				Performers: []*entity.Artist{artist},
			},
		}

		concerts, err := d.uc.List(context.Background())
		require.NoError(t, err)
		require.Len(t, concerts, 2)
		assert.Equal(t, "event-1", concerts[0].ID)
		assert.Equal(t, "event-2", concerts[1].ID)
	})

	t.Run("return empty slice when no published concerts exist", func(t *testing.T) {
		t.Parallel()
		d := newApprovalTestDeps(t, artist)
		// No concerts seeded.

		concerts, err := d.uc.List(context.Background())
		require.NoError(t, err)
		assert.Empty(t, concerts)
	})
}

func TestAdminConcertUseCase_Delete(t *testing.T) {
	t.Parallel()

	artist := &entity.Artist{ID: "artist-1", Name: "Test Artist", MBID: "11111111-1111-1111-1111-111111111111"}

	type args struct {
		eventID string
	}
	tests := []struct {
		name       string
		args       args
		seedEvents []string // event IDs to pre-populate in fakeConcertRepo
		wantErr    error
		checkRepo  func(t *testing.T, repo *fakeConcertRepo)
	}{
		{
			name:    "return InvalidArgument when event id is empty",
			args:    args{eventID: ""},
			wantErr: apperr.ErrInvalidArgument,
			checkRepo: func(t *testing.T, repo *fakeConcertRepo) {
				t.Helper()
				// repo.Delete must never be called for an empty id.
				assert.False(t, repo.deleteCalled, "repo.Delete must not be called for an empty event id")
			},
		},
		{
			name:       "call repo.Delete and succeed when event id is valid",
			args:       args{eventID: "event-abc"},
			seedEvents: []string{"event-abc"},
			checkRepo: func(t *testing.T, repo *fakeConcertRepo) {
				t.Helper()
				assert.True(t, repo.deleteCalled, "repo.Delete must be called for a valid event id")
				assert.Empty(t, repo.published, "published concert must have been removed")
			},
		},
		{
			name:       "succeed idempotently when event id is absent from repo",
			args:       args{eventID: "event-nonexistent"},
			seedEvents: nil,
			checkRepo: func(t *testing.T, repo *fakeConcertRepo) {
				t.Helper()
				assert.True(t, repo.deleteCalled, "repo.Delete must still be called for an absent event id")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			d := newApprovalTestDeps(t, artist)
			for _, id := range tt.seedEvents {
				d.concertRepo.published = append(d.concertRepo.published, &entity.Concert{
					Event: entity.Event{ID: id},
				})
			}

			err := d.uc.Delete(context.Background(), tt.args.eventID)

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
			} else {
				require.NoError(t, err)
			}
			if tt.checkRepo != nil {
				tt.checkRepo(t, d.concertRepo)
			}
		})
	}
}
