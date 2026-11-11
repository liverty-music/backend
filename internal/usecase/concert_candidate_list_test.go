package usecase_test

import (
	"context"
	"testing"

	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/liverty-music/backend/internal/entity"
	entitymocks "github.com/liverty-music/backend/internal/entity/mocks"
	"github.com/liverty-music/backend/internal/usecase"
)

// candidateListDeps is a ConcertCandidateUseCase over mocked operations; List
// reads only the candidates and their Artists.
type candidateListDeps struct {
	candidates *entitymocks.MockConcertCandidateRepository
	artists    *entitymocks.MockArtistRepository
	uc         usecase.ConcertCandidateUseCase
}

func newCandidateListDeps(t *testing.T) *candidateListDeps {
	t.Helper()
	d := &candidateListDeps{
		candidates: entitymocks.NewMockConcertCandidateRepository(t),
		artists:    entitymocks.NewMockArtistRepository(t),
	}
	// The other dependencies are nil: List never touches them.
	d.uc = usecase.NewConcertCandidateUseCase(usecase.NewConcertUseCase(
		d.artists, nil, nil, nil, nil, nil, d.candidates, nil, nil, nil,
		&recordingWorkflows{}, noopMetrics{}, nil, 0, 0, newTestLogger(t),
	))
	return d
}

func TestConcertCandidateUseCase_List(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// @spec components/usecase/concert-candidate/list "Two pending concerts"
	t.Run("two pending candidates are returned oldest first, each with its Artist", func(t *testing.T) {
		t.Parallel()
		d := newCandidateListDeps(t)
		older := &entity.ConcertCandidate{ID: "candidate-older", ArtistID: "artist-1"}
		newer := &entity.ConcertCandidate{ID: "candidate-newer", ArtistID: "artist-2"}
		first := &entity.Artist{ID: "artist-1", Name: "First"}
		second := &entity.Artist{ID: "artist-2", Name: "Second"}
		// Oldest first, as ConcertCandidate.ListPending orders them.
		d.candidates.EXPECT().ListPending(ctx).Return([]*entity.ConcertCandidate{older, newer}, nil).Once()
		d.artists.EXPECT().Get(ctx, "artist-1").Return(first, nil).Once()
		d.artists.EXPECT().Get(ctx, "artist-2").Return(second, nil).Once()

		reviews, err := d.uc.List(ctx)

		require.NoError(t, err)
		assert.Equal(t, []*usecase.ConcertCandidateReview{
			{ConcertCandidate: older, Performer: first},
			{ConcertCandidate: newer, Performer: second},
		}, reviews)
	})

	// @spec components/usecase/concert-candidate/list "Artist lookup fails"
	t.Run("an Artist that cannot be read fails the call", func(t *testing.T) {
		t.Parallel()
		d := newCandidateListDeps(t)
		d.candidates.EXPECT().ListPending(ctx).Return([]*entity.ConcertCandidate{
			{ID: "candidate-1", ArtistID: "artist-1"},
			{ID: "candidate-2", ArtistID: "artist-gone"},
		}, nil).Once()
		d.artists.EXPECT().Get(ctx, "artist-1").Return(&entity.Artist{ID: "artist-1"}, nil).Once()
		d.artists.EXPECT().Get(ctx, "artist-gone").Return(nil, apperr.New(codes.NotFound, "no such artist")).Once()

		reviews, err := d.uc.List(ctx)

		require.ErrorIs(t, err, apperr.ErrNotFound)
		assert.Nil(t, reviews)
	})

	// @spec components/usecase/concert-candidate/list "Empty queue"
	t.Run("an empty queue returns an empty list", func(t *testing.T) {
		t.Parallel()
		d := newCandidateListDeps(t)
		d.candidates.EXPECT().ListPending(ctx).Return(nil, nil).Once()

		reviews, err := d.uc.List(ctx)

		require.NoError(t, err)
		assert.Empty(t, reviews)
	})

	t.Run("a queue that cannot be read fails the call", func(t *testing.T) {
		t.Parallel()
		d := newCandidateListDeps(t)
		d.candidates.EXPECT().ListPending(ctx).Return(nil, apperr.New(codes.Unavailable, "db down")).Once()

		reviews, err := d.uc.List(ctx)

		require.ErrorIs(t, err, apperr.ErrUnavailable)
		assert.Nil(t, reviews)
	})
}
