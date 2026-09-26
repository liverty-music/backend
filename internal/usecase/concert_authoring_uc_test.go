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

// ptr returns a pointer to v, used to build test fixtures inline.
func ptr[T any](v T) *T { return &v }

// validPublishEvents returns a one-event slice that satisfies the publish
// readiness gate (a non-blank venue and a non-zero local date), so tests that
// exercise the notification / conflict paths are not blocked by the gate.
func validPublishEvents() []*entity.Event {
	return []*entity.Event{
		{
			ID:              "evt-1",
			ListedVenueName: ptr("Zepp Tokyo"),
			LocalDate:       time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC),
		},
	}
}

// authoringDeps wires up a ConcertAuthoringUseCase with mocks for every dependency.
type authoringDeps struct {
	seriesRepo    *entitymocks.MockSeriesRepository
	venueRepo     *entitymocks.MockVenueRepository
	organizerRepo *entitymocks.MockOrganizerRepository
	publisher     *ucmocks.MockEventPublisher
	uc            usecase.ConcertAuthoringUseCase
}

func newAuthoringDeps(t *testing.T) *authoringDeps {
	t.Helper()
	logger := newTestLogger(t)
	d := &authoringDeps{
		seriesRepo:    entitymocks.NewMockSeriesRepository(t),
		venueRepo:     entitymocks.NewMockVenueRepository(t),
		organizerRepo: entitymocks.NewMockOrganizerRepository(t),
		publisher:     ucmocks.NewMockEventPublisher(t),
	}
	d.uc = usecase.NewConcertAuthoringUseCase(
		d.seriesRepo, d.venueRepo, d.organizerRepo, d.publisher, logger,
	)
	return d
}

// futureDate returns a date 30 days from now so validation passes.
func futureDate() time.Time {
	return time.Now().UTC().Add(30 * 24 * time.Hour).Truncate(24 * time.Hour)
}

// --- Tests ---

func TestConcertAuthoringUseCase_CreateDraft_OwnershipReject(t *testing.T) {
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

	_, _, _, err := d.uc.CreateDraft(ctx, orgID, series, inp, []string{artistID})
	require.Error(t, err)
	assert.True(t, errors.Is(err, apperr.ErrPermissionDenied), "expected PermissionDenied, got %v", err)
}

func TestConcertAuthoringUseCase_Publish_NotifyOncePublic(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := newAuthoringDeps(t)

	const (
		orgID    = "org-1"
		seriesID = "series-1"
	)
	pub := entity.SeriesVisibilityPublic
	ps := entity.SeriesPublishStateDraft
	s := &entity.Series{
		ID:           seriesID,
		Title:        "Tour",
		Type:         entity.SeriesTypeTour,
		OrganizerID:  ptr(orgID),
		Visibility:   &pub,
		PublishState: &ps,
	}

	newEventIDs := []string{"evt-1", "evt-2"}
	d.seriesRepo.EXPECT().PublishDraft(mock.Anything, seriesID, mock.Anything).Return(newEventIDs, nil)
	// GetAuthored is called both before publish (feeding the readiness gate) and
	// after (for notification). One series-level performer → exactly one
	// CONCERT.created (keyed on that artist so the notification consumer can
	// resolve the artist's followers). The event carries a venue + date so the
	// publish readiness gate passes.
	d.seriesRepo.EXPECT().GetAuthored(mock.Anything, seriesID).
		Return(s, validPublishEvents(), []*entity.Artist{{ID: "artist-1"}}, nil)

	// Expect exactly ONE CONCERT.created and ONE ORGANIZER.concert_published.
	var concertCreatedCount, orgPublishedCount int
	d.publisher.EXPECT().PublishEvent(mock.Anything, mock.MatchedBy(func(subj string) bool {
		if subj == entity.SubjectConcertCreated {
			concertCreatedCount++
		}
		if subj == entity.SubjectOrganizerConcertPublished {
			orgPublishedCount++
		}
		return true
	}), mock.Anything).Return(nil).Times(2)

	_, _, _, err := d.uc.Publish(ctx, orgID, seriesID)
	require.NoError(t, err)
	assert.Equal(t, 1, concertCreatedCount, "CONCERT.created must be emitted exactly once")
	assert.Equal(t, 1, orgPublishedCount, "ORGANIZER.concert_published must be emitted exactly once")
}

func TestConcertAuthoringUseCase_Publish_NoNotifyDraft(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := newAuthoringDeps(t)

	const (
		orgID    = "org-1"
		seriesID = "series-draft"
	)
	// Use UNLISTED visibility — should publish but not emit CONCERT.created.
	unl := entity.SeriesVisibilityUnlisted
	ps := entity.SeriesPublishStateDraft
	s := &entity.Series{
		ID: seriesID, Title: "Secret", Type: entity.SeriesTypeSingle,
		OrganizerID: ptr(orgID), Visibility: &unl, PublishState: &ps,
	}

	newEventIDs := []string{"evt-x"}
	d.seriesRepo.EXPECT().PublishDraft(mock.Anything, seriesID, mock.Anything).Return(newEventIDs, nil)
	d.seriesRepo.EXPECT().SetUnlistedToken(mock.Anything, seriesID, mock.Anything).Return(nil)
	d.seriesRepo.EXPECT().GetAuthored(mock.Anything, seriesID).
		Return(s, validPublishEvents(), []*entity.Artist{{ID: "artist-1"}}, nil)

	// Only ORGANIZER.concert_published, never CONCERT.created.
	d.publisher.EXPECT().PublishEvent(mock.Anything, entity.SubjectOrganizerConcertPublished, mock.Anything).Return(nil)

	_, _, _, err := d.uc.Publish(ctx, orgID, seriesID)
	require.NoError(t, err)
}

func TestConcertAuthoringUseCase_Publish_SupersedeClaimedNoDoubleNotify(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := newAuthoringDeps(t)

	const (
		orgID    = "org-1"
		seriesID = "series-sup"
	)
	pub := entity.SeriesVisibilityPublic
	ps := entity.SeriesPublishStateDraft
	s := &entity.Series{
		ID: seriesID, Title: "Tour", Type: entity.SeriesTypeTour,
		OrganizerID: ptr(orgID), Visibility: &pub, PublishState: &ps,
	}

	// PublishDraft returns empty new-event-ids: all slots were claimed (no new).
	d.seriesRepo.EXPECT().PublishDraft(mock.Anything, seriesID, mock.Anything).Return([]string{}, nil)
	d.seriesRepo.EXPECT().GetAuthored(mock.Anything, seriesID).
		Return(s, validPublishEvents(), []*entity.Artist{{ID: "artist-1"}}, nil)

	// Only ORGANIZER.concert_published; no CONCERT.created (empty new-event-ids).
	d.publisher.EXPECT().PublishEvent(mock.Anything, entity.SubjectOrganizerConcertPublished, mock.Anything).Return(nil)

	_, _, _, err := d.uc.Publish(ctx, orgID, seriesID)
	require.NoError(t, err)
}

func TestConcertAuthoringUseCase_Publish_SuppressedSlot(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := newAuthoringDeps(t)

	const (
		orgID    = "org-1"
		seriesID = "series-supp"
	)
	pub := entity.SeriesVisibilityPublic
	ps := entity.SeriesPublishStateDraft
	s := &entity.Series{
		ID: seriesID, Title: "Tour", Type: entity.SeriesTypeTour,
		OrganizerID: ptr(orgID), Visibility: &pub, PublishState: &ps,
	}

	suppErr := apperr.New(codes.FailedPrecondition, "publish blocked: one or more event slots are suppressed")
	d.seriesRepo.EXPECT().GetAuthored(mock.Anything, seriesID).
		Return(s, validPublishEvents(), []*entity.Artist{{ID: "artist-1"}}, nil)
	d.seriesRepo.EXPECT().PublishDraft(mock.Anything, seriesID, mock.Anything).Return(nil, suppErr)

	_, _, _, err := d.uc.Publish(ctx, orgID, seriesID)
	require.Error(t, err)
	assert.True(t, errors.Is(err, apperr.ErrFailedPrecondition))
}

func TestConcertAuthoringUseCase_Publish_CrossOrgConflict(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := newAuthoringDeps(t)

	const (
		orgID    = "org-1"
		seriesID = "series-conflict"
	)
	pub := entity.SeriesVisibilityPublic
	ps := entity.SeriesPublishStateDraft
	s := &entity.Series{
		ID: seriesID, Title: "Tour", Type: entity.SeriesTypeTour,
		OrganizerID: ptr(orgID), Visibility: &pub, PublishState: &ps,
	}

	conflictErr := apperr.New(codes.FailedPrecondition, "publish blocked: event slot already claimed by another organizer")
	d.seriesRepo.EXPECT().GetAuthored(mock.Anything, seriesID).
		Return(s, validPublishEvents(), []*entity.Artist{{ID: "artist-1"}}, nil)
	d.seriesRepo.EXPECT().PublishDraft(mock.Anything, seriesID, mock.Anything).Return(nil, conflictErr)

	_, _, _, err := d.uc.Publish(ctx, orgID, seriesID)
	require.Error(t, err)
	assert.True(t, errors.Is(err, apperr.ErrFailedPrecondition))
}

func TestConcertAuthoringUseCase_Publish_RejectsIncompleteDraft(t *testing.T) {
	t.Parallel()

	const (
		orgID    = "org-1"
		seriesID = "series-incomplete"
	)
	pub := entity.SeriesVisibilityPublic
	ps := entity.SeriesPublishStateDraft
	base := func() *entity.Series {
		return &entity.Series{
			ID: seriesID, Title: "Tour", Type: entity.SeriesTypeTour,
			OrganizerID: ptr(orgID), Visibility: &pub, PublishState: &ps,
		}
	}

	tests := []struct {
		name    string
		series  *entity.Series
		events  []*entity.Event
		artists []*entity.Artist
	}{
		{
			name:    "no performer",
			series:  base(),
			events:  validPublishEvents(),
			artists: nil,
		},
		{
			name:    "no events",
			series:  base(),
			events:  nil,
			artists: []*entity.Artist{{ID: "artist-1"}},
		},
		{
			name:    "event missing venue",
			series:  base(),
			events:  []*entity.Event{{ID: "evt-1", LocalDate: time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)}},
			artists: []*entity.Artist{{ID: "artist-1"}},
		},
		{
			name:    "event missing date",
			series:  base(),
			events:  []*entity.Event{{ID: "evt-1", ListedVenueName: ptr("Zepp Tokyo")}},
			artists: []*entity.Artist{{ID: "artist-1"}},
		},
		{
			name: "blank title",
			series: &entity.Series{
				ID: seriesID, Title: "   ", Type: entity.SeriesTypeTour,
				OrganizerID: ptr(orgID), Visibility: &pub, PublishState: &ps,
			},
			events:  validPublishEvents(),
			artists: []*entity.Artist{{ID: "artist-1"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			d := newAuthoringDeps(t)

			// The gate runs before PublishDraft, so PublishDraft is never reached
			// (the mock would fail on an unexpected call).
			d.seriesRepo.EXPECT().GetAuthored(mock.Anything, seriesID).
				Return(tt.series, tt.events, tt.artists, nil)

			_, _, _, err := d.uc.Publish(ctx, orgID, seriesID)
			require.Error(t, err)
			assert.True(t, errors.Is(err, apperr.ErrFailedPrecondition),
				"incomplete draft must be rejected with FailedPrecondition")
		})
	}
}

func TestConcertAuthoringUseCase_RegenerateToken(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := newAuthoringDeps(t)

	const (
		orgID    = "org-1"
		seriesID = "series-unlisted"
	)
	unl := entity.SeriesVisibilityUnlisted
	ps := entity.SeriesPublishStatePublished
	s := &entity.Series{
		ID: seriesID, Title: "Secret", Type: entity.SeriesTypeSingle,
		OrganizerID: ptr(orgID), Visibility: &unl, PublishState: &ps,
	}

	d.seriesRepo.EXPECT().Get(mock.Anything, seriesID).Return(s, nil)
	d.seriesRepo.EXPECT().SetUnlistedToken(mock.Anything, seriesID, mock.Anything).Return(nil)

	token, err := d.uc.RegenerateToken(ctx, orgID, seriesID)
	require.NoError(t, err)
	assert.NotEmpty(t, token)
}

func TestConcertAuthoringUseCase_Cancel_EmitsCancelled(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := newAuthoringDeps(t)

	const (
		orgID    = "org-1"
		seriesID = "series-cancel"
	)
	pub := entity.SeriesVisibilityPublic
	ps := entity.SeriesPublishStatePublished
	s := &entity.Series{
		ID: seriesID, Title: "Tour", Type: entity.SeriesTypeTour,
		OrganizerID: ptr(orgID), Visibility: &pub, PublishState: &ps,
	}

	d.seriesRepo.EXPECT().Get(mock.Anything, seriesID).Return(s, nil)
	d.seriesRepo.EXPECT().MarkCancelled(mock.Anything, seriesID, mock.Anything).Return(nil)

	cancelledPS := entity.SeriesPublishStateCancelled
	cancelled := *s
	cancelled.PublishState = &cancelledPS
	d.seriesRepo.EXPECT().GetAuthored(mock.Anything, seriesID).Return(&cancelled, nil, nil, nil)

	d.publisher.EXPECT().PublishEvent(mock.Anything, entity.SubjectConcertCancelled, mock.Anything).Return(nil)

	err := d.uc.Cancel(ctx, orgID, seriesID)
	require.NoError(t, err)
}

func TestConcertAuthoringUseCase_Cancel_AlreadyCancelled(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := newAuthoringDeps(t)

	const (
		orgID    = "org-1"
		seriesID = "series-already-cancelled"
	)
	ps := entity.SeriesPublishStateCancelled
	s := &entity.Series{
		ID: seriesID, Title: "Tour", Type: entity.SeriesTypeTour,
		OrganizerID: ptr(orgID), PublishState: &ps,
	}

	d.seriesRepo.EXPECT().Get(mock.Anything, seriesID).Return(s, nil)

	err := d.uc.Cancel(ctx, orgID, seriesID)
	require.Error(t, err)
	assert.True(t, errors.Is(err, apperr.ErrFailedPrecondition))
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
		d.searchLogRepo, d.stagedConcertRepo, d.rejectedConcertRepo,
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
		d.searchLogRepo, d.stagedConcertRepo, d.rejectedConcertRepo,
		d.searcher, d.centroidResolver,
		messaging.NewEventPublisher(d.publisher),
		noopMetrics{},
		testSearchCacheTTL, testDiscoveryWindow, logger,
	)

	// Artist not represented → discovery pipeline proceeds.
	d.searchLogRepo.EXPECT().GetByArtistID(mock.Anything, "artist-2").
		Return(nil, apperr.New(codes.NotFound, "not found"))
	d.searchLogRepo.EXPECT().Upsert(mock.Anything, "artist-2", entity.SearchLogStatusPending).
		Return(nil)
	d.artistRepo.EXPECT().Get(mock.Anything, "artist-2").
		Return(&entity.Artist{ID: "artist-2", Name: "Test Artist", MBID: "mbid-1"}, nil)
	d.artistRepo.EXPECT().GetOfficialSite(mock.Anything, "artist-2").
		Return(nil, apperr.New(codes.NotFound, "not found"))
	d.concertRepo.EXPECT().ListByArtist(mock.Anything, "artist-2", true).
		Return(nil, nil)
	d.stagedConcertRepo.EXPECT().ListPendingDedupKeysByArtist(mock.Anything, "artist-2").
		Return(nil, nil)
	d.searcher.EXPECT().Search(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil, nil)
	d.searchLogRepo.EXPECT().UpdateStatus(mock.Anything, "artist-2", entity.SearchLogStatusCompleted).
		Return(nil).Maybe()

	concerts, err := uc.SearchNewConcerts(ctx, "artist-2")
	require.NoError(t, err)
	assert.Nil(t, concerts)
}
