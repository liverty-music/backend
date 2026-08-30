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
	seriesRepo *entitymocks.MockSeriesRepository
	venueRepo  *entitymocks.MockVenueRepository
	orgUC      *ucmocks.MockOrganizerUseCase
	publisher  *ucmocks.MockEventPublisher
	uc         usecase.ConcertAuthoringUseCase
}

func newAuthoringDeps(t *testing.T) *authoringDeps {
	t.Helper()
	logger := newTestLogger(t)
	d := &authoringDeps{
		seriesRepo: entitymocks.NewMockSeriesRepository(t),
		venueRepo:  entitymocks.NewMockVenueRepository(t),
		orgUC:      ucmocks.NewMockOrganizerUseCase(t),
		publisher:  ucmocks.NewMockEventPublisher(t),
	}
	d.uc = usecase.NewConcertAuthoringUseCase(
		d.seriesRepo, d.venueRepo, d.orgUC, d.publisher, logger,
	)
	return d
}

// mediaDeps wires up a MediaUseCase with mocks for every dependency.
type mediaDeps struct {
	seriesRepo  *entitymocks.MockSeriesRepository
	mediaRepo   *entitymocks.MockMediaRepository
	orgUC       *ucmocks.MockOrganizerUseCase
	imageStorer *ucmocks.MockImageStorer
	publisher   *ucmocks.MockEventPublisher
	uc          usecase.MediaUseCase
}

func newMediaDeps(t *testing.T) *mediaDeps {
	t.Helper()
	logger := newTestLogger(t)
	d := &mediaDeps{
		seriesRepo:  entitymocks.NewMockSeriesRepository(t),
		mediaRepo:   entitymocks.NewMockMediaRepository(t),
		orgUC:       ucmocks.NewMockOrganizerUseCase(t),
		imageStorer: ucmocks.NewMockImageStorer(t),
		publisher:   ucmocks.NewMockEventPublisher(t),
	}
	d.uc = usecase.NewMediaUseCase(
		d.seriesRepo, d.mediaRepo, d.orgUC, d.imageStorer, d.publisher, logger,
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

// --- MediaUseCase tests ---

// TestMediaUseCase_CreateMediaUploadURL_IssuesSignedURL verifies that a valid
// content type triggers a signed PUT URL and returns the media id + max bytes.
func TestMediaUseCase_CreateMediaUploadURL_IssuesSignedURL(t *testing.T) {
	// t.Setenv requires sequential execution.
	t.Setenv("ORGANIZER_MEDIA_INTERNAL_BUCKET", "originals-bucket")
	ctx := context.Background()
	d := newMediaDeps(t)

	const orgID = "org-1"
	const wantURL = "https://storage.googleapis.com/signed"

	d.imageStorer.EXPECT().
		SignedPutURL(mock.Anything, "originals-bucket",
			mock.MatchedBy(func(k string) bool { return strings.HasPrefix(k, orgID+"/") }),
			"image/jpeg", int64(10*1024*1024), mock.Anything).
		Return(wantURL, nil)

	out, err := d.uc.CreateMediaUploadURL(ctx, orgID, usecase.CreateMediaUploadURLInput{ContentType: "image/jpeg"})
	require.NoError(t, err)
	assert.Equal(t, wantURL, out.UploadURL)
	assert.NotEmpty(t, out.MediaID)
	assert.Equal(t, int64(10*1024*1024), out.MaxBytes)
}

// TestMediaUseCase_CreateMediaUploadURL_RejectsInvalidType verifies that a
// non-allowlisted content type returns InvalidArgument before any GCS call.
func TestMediaUseCase_CreateMediaUploadURL_RejectsInvalidType(t *testing.T) {
	ctx := context.Background()
	d := newMediaDeps(t)

	_, err := d.uc.CreateMediaUploadURL(ctx, "org-1", usecase.CreateMediaUploadURLInput{ContentType: "image/svg+xml"})
	require.Error(t, err)
	assert.True(t, errors.Is(err, apperr.ErrInvalidArgument), "expected InvalidArgument, got %v", err)
}

// TestMediaUseCase_CreateMediaUploadURL_MissingBucket verifies a clear Internal
// error when ORGANIZER_MEDIA_INTERNAL_BUCKET is unset.
func TestMediaUseCase_CreateMediaUploadURL_MissingBucket(t *testing.T) {
	// t.Setenv requires sequential execution.
	t.Setenv("ORGANIZER_MEDIA_INTERNAL_BUCKET", "")
	ctx := context.Background()
	d := newMediaDeps(t)

	_, err := d.uc.CreateMediaUploadURL(ctx, "org-1", usecase.CreateMediaUploadURLInput{ContentType: "image/png"})
	require.Error(t, err)
	assert.True(t, errors.Is(err, apperr.ErrInternal), "expected Internal, got %v", err)
}

// TestMediaUseCase_AttachMedia_InsertsAndPublishes verifies the happy path:
// ownership check passes, media row is inserted, event is published.
func TestMediaUseCase_AttachMedia_InsertsAndPublishes(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := newMediaDeps(t)

	const (
		orgID    = "org-1"
		seriesID = "series-1"
		mediaID  = "media-1"
	)
	s := &entity.Series{ID: seriesID, OrganizerID: ptr(orgID)}
	d.seriesRepo.EXPECT().Get(mock.Anything, seriesID).Return(s, nil)
	d.mediaRepo.EXPECT().InsertMedia(mock.Anything, mock.MatchedBy(func(m *entity.Media) bool {
		return m.ID == mediaID && m.OrganizerID == orgID && m.Kind == entity.MediaKindImage
	})).Return(nil)
	d.publisher.EXPECT().PublishEvent(mock.Anything, entity.SubjectMediaUploaded, mock.Anything).Return(nil)

	err := d.uc.AttachMedia(ctx, orgID, seriesID, mediaID)
	require.NoError(t, err)
}

// TestMediaUseCase_AttachMedia_NonOwnerDenied verifies that a caller who does
// not own the series receives PermissionDenied (non-revealing).
func TestMediaUseCase_AttachMedia_NonOwnerDenied(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := newMediaDeps(t)

	const (
		orgID    = "org-other"
		seriesID = "series-1"
		mediaID  = "media-1"
	)
	// Series is owned by a different organizer.
	s := &entity.Series{ID: seriesID, OrganizerID: ptr("org-owner")}
	d.seriesRepo.EXPECT().Get(mock.Anything, seriesID).Return(s, nil)

	err := d.uc.AttachMedia(ctx, orgID, seriesID, mediaID)
	require.Error(t, err)
	assert.True(t, errors.Is(err, apperr.ErrPermissionDenied), "expected PermissionDenied, got %v", err)
}

// TestMediaUseCase_AttachMedia_Idempotent verifies that a second AttachMedia
// for the same media_id succeeds (InsertMedia is ON CONFLICT DO NOTHING).
func TestMediaUseCase_AttachMedia_Idempotent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := newMediaDeps(t)

	const (
		orgID    = "org-1"
		seriesID = "series-1"
		mediaID  = "media-dup"
	)
	s := &entity.Series{ID: seriesID, OrganizerID: ptr(orgID)}
	// Both calls use the same mock stubs — idempotent at the DB layer.
	d.seriesRepo.EXPECT().Get(mock.Anything, seriesID).Return(s, nil).Times(2)
	d.mediaRepo.EXPECT().InsertMedia(mock.Anything, mock.Anything).Return(nil).Times(2)
	d.publisher.EXPECT().PublishEvent(mock.Anything, entity.SubjectMediaUploaded, mock.Anything).Return(nil).Times(2)

	require.NoError(t, d.uc.AttachMedia(ctx, orgID, seriesID, mediaID))
	require.NoError(t, d.uc.AttachMedia(ctx, orgID, seriesID, mediaID))
}
