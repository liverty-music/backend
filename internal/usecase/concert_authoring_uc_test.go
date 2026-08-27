package usecase_test

import (
	"context"
	"errors"
	"strings"
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
//
//go:fix inline
func ptr[T any](v T) *T { return new(v) }

// authoringDeps wires up a ConcertAuthoringUseCase with mocks for every dependency.
type authoringDeps struct {
	seriesRepo  *entitymocks.MockSeriesRepository
	venueRepo   *entitymocks.MockVenueRepository
	orgUC       *ucmocks.MockOrganizerUseCase
	publisher   *ucmocks.MockEventPublisher
	imageStorer *ucmocks.MockImageStorer
	uc          usecase.ConcertAuthoringUseCase
}

func newAuthoringDeps(t *testing.T) *authoringDeps {
	t.Helper()
	logger := newTestLogger(t)
	d := &authoringDeps{
		seriesRepo:  entitymocks.NewMockSeriesRepository(t),
		venueRepo:   entitymocks.NewMockVenueRepository(t),
		orgUC:       ucmocks.NewMockOrganizerUseCase(t),
		publisher:   ucmocks.NewMockEventPublisher(t),
		imageStorer: ucmocks.NewMockImageStorer(t),
	}
	d.uc = usecase.NewConcertAuthoringUseCase(
		d.seriesRepo, d.venueRepo, d.orgUC, d.publisher, d.imageStorer, logger,
	)
	return d
}

// stubOwnedArtist sets up ListArtists to return a single artist with the given id.
func stubOwnedArtist(d *authoringDeps, orgID, artistID string) {
	d.orgUC.EXPECT().ListArtists(mock.Anything, orgID).
		Return([]*entity.Artist{{ID: artistID}}, nil).Maybe()
}

// stubVenueGetOrCreate makes GetByListedName return NotFound then Create return a new venueID.
func stubVenueGetOrCreate(d *authoringDeps, venueName, venueID string) {
	d.venueRepo.EXPECT().GetByPlaceID(mock.Anything, mock.Anything).
		Return(nil, apperr.New(codes.NotFound, "not found")).Maybe()
	d.venueRepo.EXPECT().GetByListedName(mock.Anything, venueName, mock.Anything).
		Return(nil, apperr.New(codes.NotFound, "not found")).Maybe()
	d.venueRepo.EXPECT().Create(mock.Anything, mock.Anything).
		Return(venueID, nil).Maybe()
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
	d.orgUC.EXPECT().ListArtists(mock.Anything, orgID).
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
	d.seriesRepo.EXPECT().Get(mock.Anything, seriesID).Return(s, nil)
	d.seriesRepo.EXPECT().PublishDraft(mock.Anything, seriesID, mock.Anything).Return(newEventIDs, nil)
	// One series-level performer → exactly one CONCERT.created (keyed on that
	// artist so the notification consumer can resolve the artist's followers).
	d.seriesRepo.EXPECT().GetAuthored(mock.Anything, seriesID).Return(s, nil, []*entity.Artist{{ID: "artist-1"}}, nil)

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
	d.seriesRepo.EXPECT().Get(mock.Anything, seriesID).Return(s, nil)
	d.seriesRepo.EXPECT().PublishDraft(mock.Anything, seriesID, mock.Anything).Return(newEventIDs, nil)
	d.seriesRepo.EXPECT().SetUnlistedToken(mock.Anything, seriesID, mock.Anything).Return(nil)
	d.seriesRepo.EXPECT().GetAuthored(mock.Anything, seriesID).Return(s, nil, nil, nil)

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
	d.seriesRepo.EXPECT().Get(mock.Anything, seriesID).Return(s, nil)
	d.seriesRepo.EXPECT().PublishDraft(mock.Anything, seriesID, mock.Anything).Return([]string{}, nil)
	d.seriesRepo.EXPECT().GetAuthored(mock.Anything, seriesID).Return(s, nil, nil, nil)

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
	d.seriesRepo.EXPECT().Get(mock.Anything, seriesID).Return(s, nil)
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
	d.seriesRepo.EXPECT().Get(mock.Anything, seriesID).Return(s, nil)
	d.seriesRepo.EXPECT().PublishDraft(mock.Anything, seriesID, mock.Anything).Return(nil, conflictErr)

	_, _, _, err := d.uc.Publish(ctx, orgID, seriesID)
	require.Error(t, err)
	assert.True(t, errors.Is(err, apperr.ErrFailedPrecondition))
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

// TestConcertAuthoringUseCase_UploadCoverImage_MintsMediaAndReplacesOld verifies
// that upload stores the object directly under `cdn/{org}/{mediaId}`, persists
// the media + series_media rows via ReplaceCoverMedia, best-effort deletes the
// prior cdn object, and returns the composed CDN URL.
func TestConcertAuthoringUseCase_UploadCoverImage_MintsMediaAndReplacesOld(t *testing.T) {
	t.Setenv("ORGANIZER_MEDIA_BUCKET", "test-bucket")
	t.Setenv("ORGANIZER_MEDIA_CDN_BASE", "https://media.example.com")
	ctx := context.Background()
	d := newAuthoringDeps(t)

	const (
		orgID      = "org-1"
		seriesID   = "series-1"
		oldMediaID = "old-media-id"
	)
	data := []byte("png-bytes")
	cdnKeyPrefix := "cdn/" + orgID + "/"

	s := &entity.Series{ID: seriesID, Title: "T", Type: entity.SeriesTypeSingle, OrganizerID: ptr(orgID)}
	d.seriesRepo.EXPECT().Get(mock.Anything, seriesID).Return(s, nil)

	// Put is called with the cdn key: cdn/{org}/{mediaId} (extension-less).
	d.imageStorer.EXPECT().
		Put(mock.Anything, "test-bucket", mock.MatchedBy(func(k string) bool {
			return strings.HasPrefix(k, cdnKeyPrefix) &&
				!strings.Contains(strings.TrimPrefix(k, cdnKeyPrefix), ".")
		}), "image/png", data).
		Return(nil)

	// ReplaceCoverMedia is called with the new Media struct.
	d.seriesRepo.EXPECT().
		ReplaceCoverMedia(mock.Anything, seriesID, mock.MatchedBy(func(m *entity.Media) bool {
			return m.OrganizerID == orgID &&
				m.Kind == entity.MediaKindImage &&
				m.Attributes["content_type"] == "image/png"
		})).
		Return(oldMediaID, nil)

	// Prior cdn object is best-effort deleted.
	d.imageStorer.EXPECT().
		Delete(mock.Anything, "test-bucket", cdnKeyPrefix+oldMediaID).
		Return(nil)

	got, err := d.uc.UploadCoverImage(ctx, orgID, seriesID, "image/png", data)
	require.NoError(t, err)
	// Response is the CDN URL, not a signed URL.
	assert.True(t, strings.HasPrefix(got, "https://media.example.com/cdn/"+orgID+"/"),
		"expected CDN URL, got %q", got)
}

// TestConcertAuthoringUseCase_UploadCoverImage_NoOldMedia verifies that when
// there is no prior cover (ReplaceCoverMedia returns ""), no Delete is called
// and the CDN URL is still returned.
func TestConcertAuthoringUseCase_UploadCoverImage_NoOldMedia(t *testing.T) {
	t.Setenv("ORGANIZER_MEDIA_BUCKET", "test-bucket")
	t.Setenv("ORGANIZER_MEDIA_CDN_BASE", "https://media.example.com")
	ctx := context.Background()
	d := newAuthoringDeps(t)

	const (
		orgID    = "org-1"
		seriesID = "series-new"
	)
	data := []byte("jpg-bytes")

	s := &entity.Series{ID: seriesID, Title: "T", Type: entity.SeriesTypeSingle, OrganizerID: ptr(orgID)}
	d.seriesRepo.EXPECT().Get(mock.Anything, seriesID).Return(s, nil)
	d.imageStorer.EXPECT().
		Put(mock.Anything, "test-bucket", mock.MatchedBy(func(k string) bool {
			return strings.HasPrefix(k, "cdn/"+orgID+"/")
		}), "image/jpeg", data).
		Return(nil)
	d.seriesRepo.EXPECT().
		ReplaceCoverMedia(mock.Anything, seriesID, mock.Anything).
		Return("", nil) // no prior cover — Delete must NOT be called

	got, err := d.uc.UploadCoverImage(ctx, orgID, seriesID, "image/jpeg", data)
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(got, "https://media.example.com/cdn/"+orgID+"/"),
		"expected CDN URL, got %q", got)
}

// TestConcertAuthoringUseCase_UploadCoverImage_MissingBucket verifies a clear
// Internal error when the media bucket env var is unset.
func TestConcertAuthoringUseCase_UploadCoverImage_MissingBucket(t *testing.T) {
	t.Setenv("ORGANIZER_MEDIA_BUCKET", "")
	ctx := context.Background()
	d := newAuthoringDeps(t)

	const (
		orgID    = "org-1"
		seriesID = "series-1"
	)
	s := &entity.Series{ID: seriesID, OrganizerID: ptr(orgID)}
	d.seriesRepo.EXPECT().Get(mock.Anything, seriesID).Return(s, nil)

	_, err := d.uc.UploadCoverImage(ctx, orgID, seriesID, "image/png", []byte("x"))
	require.Error(t, err)
	assert.True(t, errors.Is(err, apperr.ErrInternal), "expected Internal, got %v", err)
}
