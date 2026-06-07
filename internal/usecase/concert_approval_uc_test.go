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

// --- test doubles for approval UC ---

// fakeRejectedConcertLogRepo is an in-memory append-only log repo.
type fakeRejectedConcertLogRepo struct {
	entries []*entity.RejectedConcertLog
}

func (r *fakeRejectedConcertLogRepo) Append(_ context.Context, log *entity.RejectedConcertLog) error {
	r.entries = append(r.entries, log)
	return nil
}

// fakeArtistRepo is a minimal artist repository for approval tests.
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

// approvalTestDeps bundles dependencies for ConcertApprovalUseCase tests.
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
	uc usecase.ConcertApprovalUseCase
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
	d.uc = usecase.NewConcertApprovalUseCase(
		d.stagedRepo,
		d.rejectedLog,
		d.venueRepo,
		d.seriesRepo,
		d.concertRepo,
		d.artistRepo,
		messaging.NewEventPublisher(pub),
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

func TestConcertApprovalUseCase_Approve(t *testing.T) {
	t.Parallel()

	artist := &entity.Artist{ID: "artist-1", Name: "Test Artist", MBID: "11111111-1111-1111-1111-111111111111"}

	t.Run("approve inserts concert, publishes CONCERT.created, and deletes staged row", func(t *testing.T) {
		t.Parallel()
		d := newApprovalTestDeps(t, artist)
		sc := seedStaged(d, artist.ID)

		ctx := context.Background()
		sub, err := d.publisher.Subscribe(ctx, entity.SubjectConcertCreated)
		require.NoError(t, err)

		err = d.uc.Approve(ctx, sc.ID)
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

		err := d.uc.Approve(context.Background(), "staged-nonexistent")
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

		err := d.uc.Approve(context.Background(), sc.ID)
		require.NoError(t, err)

		// Venue was reused, not re-created.
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

		err = approvalDeps.uc.Approve(ctx, stagedRepo.upserted[0].ID)
		require.NoError(t, err)

		select {
		case msg := <-approvalCreatedCh:
			msg.Ack()
		case <-time.After(2 * time.Second):
			t.Fatal("expected CONCERT.created from Approve but got none")
		}
	})
}

func TestConcertApprovalUseCase_Reject(t *testing.T) {
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
