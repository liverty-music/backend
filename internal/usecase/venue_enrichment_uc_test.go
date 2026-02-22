package usecase_test

import (
	"context"
	"testing"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- test doubles ---

type fakeVenueEnrichmentRepo struct {
	pending     []*entity.Venue
	enriched    map[string]*entity.Venue
	failed      []string
	merged      [][2]string // [canonicalID, duplicateID]
	getByNameFn func(name string) (*entity.Venue, error)
}

func (r *fakeVenueEnrichmentRepo) ListPending(_ context.Context) ([]*entity.Venue, error) {
	return r.pending, nil
}

func (r *fakeVenueEnrichmentRepo) UpdateEnriched(_ context.Context, v *entity.Venue) error {
	if r.enriched == nil {
		r.enriched = make(map[string]*entity.Venue)
	}
	r.enriched[v.ID] = v
	return nil
}

func (r *fakeVenueEnrichmentRepo) MarkFailed(_ context.Context, id string) error {
	r.failed = append(r.failed, id)
	return nil
}

func (r *fakeVenueEnrichmentRepo) MergeVenues(_ context.Context, canonicalID, duplicateID string) error {
	r.merged = append(r.merged, [2]string{canonicalID, duplicateID})
	return nil
}

func (r *fakeVenueEnrichmentRepo) GetByName(_ context.Context, name string) (*entity.Venue, error) {
	if r.getByNameFn != nil {
		return r.getByNameFn(name)
	}
	return nil, apperr.New(codes.NotFound, "not found")
}

type fakePlaceSearcher struct {
	result *entity.VenuePlace
	err    error
}

func (s *fakePlaceSearcher) SearchPlace(_ context.Context, _, _ string) (*entity.VenuePlace, error) {
	return s.result, s.err
}

// --- helpers ---

func newTestLogger(t *testing.T) *logging.Logger {
	t.Helper()
	logger, err := logging.New()
	require.NoError(t, err)
	return logger
}

// --- tests ---

func TestVenueEnrichmentUseCase_EnrichPendingVenues_Success(t *testing.T) {
	repo := &fakeVenueEnrichmentRepo{
		pending: []*entity.Venue{
			{ID: "venue-1", Name: "zepp nagoya", RawName: "zepp nagoya"},
		},
	}
	searcher := &fakePlaceSearcher{
		result: &entity.VenuePlace{ExternalID: "mbid-001", Name: "Zepp Nagoya"},
	}

	uc := usecase.NewVenueEnrichmentUseCase(repo, repo, newTestLogger(t),
		usecase.VenueNamedSearcher{Searcher: searcher, AssignToMBID: true},
	)

	err := uc.EnrichPendingVenues(context.Background())
	require.NoError(t, err)

	enriched, ok := repo.enriched["venue-1"]
	require.True(t, ok, "venue should have been enriched")
	assert.Equal(t, "Zepp Nagoya", enriched.Name)
	require.NotNil(t, enriched.MBID)
	assert.Equal(t, "mbid-001", *enriched.MBID)
	assert.Equal(t, "zepp nagoya", enriched.RawName)
	assert.Empty(t, repo.failed)
}

func TestVenueEnrichmentUseCase_EnrichPendingVenues_GooglePlaceID(t *testing.T) {
	repo := &fakeVenueEnrichmentRepo{
		pending: []*entity.Venue{
			{ID: "venue-2", Name: "nippon budokan", RawName: "nippon budokan"},
		},
	}
	searcher := &fakePlaceSearcher{
		result: &entity.VenuePlace{ExternalID: "ChIJplace001", Name: "Nippon Budokan"},
	}

	uc := usecase.NewVenueEnrichmentUseCase(repo, repo, newTestLogger(t),
		usecase.VenueNamedSearcher{Searcher: searcher, AssignToMBID: false},
	)

	err := uc.EnrichPendingVenues(context.Background())
	require.NoError(t, err)

	enriched, ok := repo.enriched["venue-2"]
	require.True(t, ok)
	require.NotNil(t, enriched.GooglePlaceID)
	assert.Equal(t, "ChIJplace001", *enriched.GooglePlaceID)
	assert.Nil(t, enriched.MBID)
}

func TestVenueEnrichmentUseCase_EnrichPendingVenues_FallsBackToSecondSearcher(t *testing.T) {
	repo := &fakeVenueEnrichmentRepo{
		pending: []*entity.Venue{
			{ID: "venue-3", Name: "some venue", RawName: "some venue"},
		},
	}
	first := &fakePlaceSearcher{err: apperr.New(codes.NotFound, "not found")}
	second := &fakePlaceSearcher{result: &entity.VenuePlace{ExternalID: "ChIJfallback", Name: "Some Venue"}}

	uc := usecase.NewVenueEnrichmentUseCase(repo, repo, newTestLogger(t),
		usecase.VenueNamedSearcher{Searcher: first, AssignToMBID: true},
		usecase.VenueNamedSearcher{Searcher: second, AssignToMBID: false},
	)

	err := uc.EnrichPendingVenues(context.Background())
	require.NoError(t, err)

	enriched, ok := repo.enriched["venue-3"]
	require.True(t, ok)
	require.NotNil(t, enriched.GooglePlaceID)
	assert.Equal(t, "ChIJfallback", *enriched.GooglePlaceID)
}

func TestVenueEnrichmentUseCase_EnrichPendingVenues_NoMatchMarksFailed(t *testing.T) {
	repo := &fakeVenueEnrichmentRepo{
		pending: []*entity.Venue{
			{ID: "venue-4", Name: "unknown venue", RawName: "unknown venue"},
		},
	}
	searcher := &fakePlaceSearcher{err: apperr.New(codes.NotFound, "not found")}

	uc := usecase.NewVenueEnrichmentUseCase(repo, repo, newTestLogger(t),
		usecase.VenueNamedSearcher{Searcher: searcher, AssignToMBID: true},
	)

	err := uc.EnrichPendingVenues(context.Background())
	require.NoError(t, err) // per-venue failures are non-fatal

	assert.Contains(t, repo.failed, "venue-4")
	assert.Empty(t, repo.enriched)
}

func TestVenueEnrichmentUseCase_EnrichPendingVenues_DuplicateDetectionMerges(t *testing.T) {
	canonicalVenue := &entity.Venue{ID: "canonical-id", Name: "Zepp Nagoya"}
	repo := &fakeVenueEnrichmentRepo{
		pending: []*entity.Venue{
			{ID: "duplicate-id", Name: "zepp nagoya", RawName: "zepp nagoya"},
		},
		getByNameFn: func(name string) (*entity.Venue, error) {
			if name == "Zepp Nagoya" {
				return canonicalVenue, nil
			}
			return nil, apperr.New(codes.NotFound, "not found")
		},
	}
	searcher := &fakePlaceSearcher{
		result: &entity.VenuePlace{ExternalID: "mbid-001", Name: "Zepp Nagoya"},
	}

	uc := usecase.NewVenueEnrichmentUseCase(repo, repo, newTestLogger(t),
		usecase.VenueNamedSearcher{Searcher: searcher, AssignToMBID: true},
	)

	err := uc.EnrichPendingVenues(context.Background())
	require.NoError(t, err)

	require.Len(t, repo.merged, 1)
	assert.Equal(t, "canonical-id", repo.merged[0][0])
	assert.Equal(t, "duplicate-id", repo.merged[0][1])
	assert.Empty(t, repo.enriched)
}

func TestVenueEnrichmentUseCase_EnrichPendingVenues_TransientErrorLeavesPending(t *testing.T) {
	repo := &fakeVenueEnrichmentRepo{
		pending: []*entity.Venue{
			{ID: "venue-5", Name: "venue", RawName: "venue"},
		},
	}
	// A transient (Unavailable) error from all searchers must NOT mark the venue as failed.
	// The venue remains pending so the next run can retry.
	searcher := &fakePlaceSearcher{err: apperr.New(codes.Unavailable, "service down")}

	uc := usecase.NewVenueEnrichmentUseCase(repo, repo, newTestLogger(t),
		usecase.VenueNamedSearcher{Searcher: searcher, AssignToMBID: true},
	)

	err := uc.EnrichPendingVenues(context.Background())
	require.NoError(t, err) // non-fatal

	assert.Empty(t, repo.failed, "transient errors must not permanently mark venue as failed")
	assert.Empty(t, repo.enriched)
}
