package usecase_test

import (
	"context"
	"testing"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

const (
	oldMBID = "aaaaaaaa-0000-0000-0000-00000000000a"
	newMBID = "bbbbbbbb-0000-0000-0000-00000000000b"
	otherID = "cccccccc-0000-0000-0000-00000000000c"
)

// correctedArtist is Penthouse after a correction from oldMBID to newMBID.
func correctedArtist() *entity.Artist {
	return &entity.Artist{ID: "artist-1", Name: "Penthouse", MBID: newMBID, OtherMBIDs: []string{oldMBID}}
}

func TestArtistUseCase_Search_ReplacedMBID(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// @spec components/usecase/artist/search "Match under a replaced MBID"
	t.Run("Match under a replaced MBID", func(t *testing.T) {
		t.Parallel()
		d := newArtistTestDeps(t)
		corrected := correctedArtist()
		d.searcher.EXPECT().Search(ctx, "Penthouse").Return([]*entity.Artist{{Name: "Penthouse", MBID: oldMBID}}, nil).Once()
		d.repo.EXPECT().ListByMBIDs(mock.Anything, []string{oldMBID}).Return([]*entity.Artist{corrected}, nil).Once()
		// Create is not expected: the mock fails the test when it is called.

		got, err := d.uc.Search(ctx, "Penthouse")

		require.NoError(t, err)
		assert.Equal(t, []*entity.Artist{corrected}, got)
	})

	// @spec components/usecase/artist/search "Two matches of one artist"
	t.Run("Two matches of one artist", func(t *testing.T) {
		t.Parallel()
		d := newArtistTestDeps(t)
		corrected := correctedArtist()
		d.searcher.EXPECT().Search(ctx, "Penthouse").Return([]*entity.Artist{
			{Name: "Penthouse", MBID: oldMBID},
			{Name: "Penthouse", MBID: newMBID},
		}, nil).Once()
		d.repo.EXPECT().ListByMBIDs(mock.Anything, []string{oldMBID, newMBID}).Return([]*entity.Artist{corrected}, nil).Once()

		got, err := d.uc.Search(ctx, "Penthouse")

		require.NoError(t, err)
		assert.Equal(t, []*entity.Artist{corrected}, got)
	})
}

func TestArtistUseCase_ListSimilar_ReplacedMBID(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := newArtistTestDeps(t)
	seed := &entity.Artist{ID: "seed-id", Name: "Seed", MBID: otherID}
	corrected := correctedArtist()
	d.repo.EXPECT().Get(ctx, "seed-id").Return(seed, nil).Once()
	d.searcher.EXPECT().ListSimilar(ctx, seed, int32(50)).Return([]*entity.Artist{{Name: "Penthouse", MBID: oldMBID}}, nil).Once()
	d.repo.EXPECT().ListByMBIDs(mock.Anything, []string{oldMBID}).Return([]*entity.Artist{corrected}, nil).Once()

	// @spec components/usecase/artist/list-similar "Result under a replaced MBID"
	got, err := d.uc.ListSimilar(ctx, "seed-id", 0)

	require.NoError(t, err)
	assert.Equal(t, []*entity.Artist{corrected}, got)
}

func TestArtistUseCase_ListTop_ReplacedMBID(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := newArtistTestDeps(t)
	corrected := correctedArtist()
	d.searcher.EXPECT().ListTop(ctx, "JP", "", int32(0)).Return([]*entity.Artist{{Name: "Penthouse", MBID: oldMBID}}, nil).Once()
	d.repo.EXPECT().ListByMBIDs(mock.Anything, []string{oldMBID}).Return([]*entity.Artist{corrected}, nil).Once()

	// @spec components/usecase/artist/list-top "Chart artist under a replaced MBID"
	got, err := d.uc.ListTop(ctx, "JP", "", 0)

	require.NoError(t, err)
	assert.Equal(t, []*entity.Artist{corrected}, got)
}

func TestArtistUseCase_CorrectMBID(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	registered := &entity.Artist{ID: "artist-1", Name: "Penthouse", MBID: oldMBID}

	// @spec components/usecase/artist/correct-mbid "Namesake corrected"
	t.Run("Namesake corrected", func(t *testing.T) {
		t.Parallel()
		d := newArtistTestDeps(t)
		d.repo.EXPECT().Get(anyCtx, "artist-1").Return(registered, nil).Once()
		d.idManager.EXPECT().GetArtist(anyCtx, newMBID).Return(&entity.Artist{Name: "PENTHOUSE", MBID: newMBID}, nil).Twice()
		d.repo.EXPECT().UpdateMBID(anyCtx, "artist-1", newMBID).Return(nil).Once()
		d.repo.EXPECT().UpdateName(anyCtx, "artist-1", "PENTHOUSE").Return(nil).Once()
		d.repo.EXPECT().UpdateActivityEnded(anyCtx, "artist-1", false).Return(nil).Once()
		after := &entity.Artist{ID: "artist-1", Name: "PENTHOUSE", MBID: newMBID, OtherMBIDs: []string{oldMBID}}
		d.repo.EXPECT().Get(anyCtx, "artist-1").Return(after, nil).Once()

		got, err := d.uc.CorrectMBID(ctx, "artist-1", newMBID)

		require.NoError(t, err)
		assert.Equal(t, after, got)
	})

	// @spec components/usecase/artist/correct-mbid "Redirected MBID"
	t.Run("Redirected MBID", func(t *testing.T) {
		t.Parallel()
		const canonical = "dddddddd-0000-0000-0000-00000000000d"
		d := newArtistTestDeps(t)
		d.repo.EXPECT().Get(anyCtx, "artist-1").Return(registered, nil).Once()
		d.idManager.EXPECT().GetArtist(anyCtx, newMBID).Return(&entity.Artist{Name: "Penthouse", MBID: canonical}, nil).Once()
		d.repo.EXPECT().UpdateMBID(anyCtx, "artist-1", canonical).Return(apperr.New(codes.AlreadyExists, "another artist has the mbid")).Once()
		// The artist keeps its MBID when another artist has the canonical one.

		_, err := d.uc.CorrectMBID(ctx, "artist-1", newMBID)

		assert.ErrorIs(t, err, apperr.ErrAlreadyExists)
	})

	// @spec components/usecase/artist/correct-mbid "Redirected MBID"
	t.Run("Redirected MBID is stored as the canonical one", func(t *testing.T) {
		t.Parallel()
		const canonical = "dddddddd-0000-0000-0000-00000000000d"
		d := newArtistTestDeps(t)
		d.repo.EXPECT().Get(anyCtx, "artist-1").Return(registered, nil).Once()
		d.idManager.EXPECT().GetArtist(anyCtx, newMBID).Return(&entity.Artist{Name: "Penthouse", MBID: canonical}, nil).Once()
		d.repo.EXPECT().UpdateMBID(anyCtx, "artist-1", canonical).Return(nil).Once()
		d.idManager.EXPECT().GetArtist(anyCtx, canonical).Return(&entity.Artist{Name: "Penthouse", MBID: canonical}, nil).Once()
		d.repo.EXPECT().UpdateActivityEnded(anyCtx, "artist-1", false).Return(nil).Once()
		after := &entity.Artist{ID: "artist-1", Name: "Penthouse", MBID: canonical, OtherMBIDs: []string{oldMBID}}
		d.repo.EXPECT().Get(anyCtx, "artist-1").Return(after, nil).Once()

		got, err := d.uc.CorrectMBID(ctx, "artist-1", newMBID)

		require.NoError(t, err)
		assert.Equal(t, canonical, got.MBID)
	})

	// @spec components/usecase/artist/correct-mbid "Catalog does not know the MBID"
	t.Run("Catalog does not know the MBID", func(t *testing.T) {
		t.Parallel()
		d := newArtistTestDeps(t)
		d.repo.EXPECT().Get(anyCtx, "artist-1").Return(registered, nil).Once()
		d.idManager.EXPECT().GetArtist(anyCtx, newMBID).Return(nil, apperr.New(codes.NotFound, "no such artist")).Once()
		// UpdateMBID is not expected: the mock fails the test when it is called.

		got, err := d.uc.CorrectMBID(ctx, "artist-1", newMBID)

		assert.ErrorIs(t, err, apperr.ErrNotFound)
		assert.Nil(t, got)
	})

	// @spec components/usecase/artist/correct-mbid "Same MBID again"
	t.Run("Same MBID again", func(t *testing.T) {
		t.Parallel()
		d := newArtistTestDeps(t)
		current := correctedArtist()
		d.repo.EXPECT().Get(anyCtx, "artist-1").Return(current, nil).Twice()
		d.idManager.EXPECT().GetArtist(anyCtx, newMBID).Return(&entity.Artist{Name: "Penthouse", MBID: newMBID}, nil).Twice()
		d.repo.EXPECT().UpdateMBID(anyCtx, "artist-1", newMBID).Return(nil).Once()
		d.repo.EXPECT().UpdateActivityEnded(anyCtx, "artist-1", false).Return(nil).Once()

		got, err := d.uc.CorrectMBID(ctx, "artist-1", newMBID)

		require.NoError(t, err)
		assert.Equal(t, current, got)
	})

	// @spec components/usecase/artist/correct-mbid "Malformed MBID"
	t.Run("Malformed MBID", func(t *testing.T) {
		t.Parallel()
		d := newArtistTestDeps(t)
		// No port is expected: the mocks fail the test when one is called.

		_, err := d.uc.CorrectMBID(ctx, "artist-1", "too-short")

		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
	})

	// @spec components/usecase/artist/correct-mbid "Unknown artist"
	t.Run("Unknown artist", func(t *testing.T) {
		t.Parallel()
		d := newArtistTestDeps(t)
		d.repo.EXPECT().Get(anyCtx, "artist-1").Return(nil, apperr.New(codes.NotFound, "artist not found")).Once()

		_, err := d.uc.CorrectMBID(ctx, "artist-1", newMBID)

		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})

	// @spec components/usecase/artist/correct-mbid "MBID of another artist"
	t.Run("MBID of another artist", func(t *testing.T) {
		t.Parallel()
		d := newArtistTestDeps(t)
		d.repo.EXPECT().Get(anyCtx, "artist-1").Return(registered, nil).Once()
		d.idManager.EXPECT().GetArtist(anyCtx, newMBID).Return(&entity.Artist{Name: "Penthouse", MBID: newMBID}, nil).Once()
		d.repo.EXPECT().UpdateMBID(anyCtx, "artist-1", newMBID).Return(apperr.New(codes.AlreadyExists, "another artist has the mbid")).Once()

		_, err := d.uc.CorrectMBID(ctx, "artist-1", newMBID)

		assert.ErrorIs(t, err, apperr.ErrAlreadyExists)
	})

	// @spec components/usecase/artist/correct-mbid "Catalog unavailable after the change"
	t.Run("Catalog unavailable after the change", func(t *testing.T) {
		t.Parallel()
		d := newArtistTestDeps(t)
		d.repo.EXPECT().Get(anyCtx, "artist-1").Return(registered, nil).Once()
		d.idManager.EXPECT().GetArtist(anyCtx, newMBID).Return(&entity.Artist{Name: "Penthouse", MBID: newMBID}, nil).Once()
		d.repo.EXPECT().UpdateMBID(anyCtx, "artist-1", newMBID).Return(nil).Once()
		d.idManager.EXPECT().GetArtist(anyCtx, newMBID).Return(nil, apperr.New(codes.Unavailable, "catalog down")).Once()

		_, err := d.uc.CorrectMBID(ctx, "artist-1", newMBID)

		assert.ErrorIs(t, err, apperr.ErrUnavailable)
	})
}
