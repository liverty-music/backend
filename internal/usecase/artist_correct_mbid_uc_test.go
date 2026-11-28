package usecase_test

import (
	"context"
	"testing"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	oldMBID = "aaaaaaaa-0000-0000-0000-00000000000a"
	newMBID = "bbbbbbbb-0000-0000-0000-00000000000b"
	otherID = "cccccccc-0000-0000-0000-00000000000c"
)

// correctedArtist is Penthouse after a correction from oldMBID (its listening
// MBID) to newMBID.
func correctedArtist() *entity.Artist {
	return &entity.Artist{ID: "artist-1", Name: "Penthouse", MBID: newMBID, ListeningMBID: oldMBID, MBIDStatus: entity.MBIDStatusVerified}
}

func TestArtistUseCase_CorrectMBID(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	// ambiguous is Penthouse registered with the namesake's MBID oldMBID.
	ambiguous := func() *entity.Artist {
		return &entity.Artist{ID: "artist-1", Name: "Penthouse", MBID: oldMBID, ListeningMBID: oldMBID, MBIDStatus: entity.MBIDStatusAmbiguous}
	}

	// @spec components/usecase/artist/correct-mbid "Namesake corrected"
	t.Run("Namesake corrected", func(t *testing.T) {
		t.Parallel()
		d := newArtistTestDeps(t)
		d.repo.EXPECT().Get(anyCtx, "artist-1").Return(ambiguous(), nil).Once()
		d.idManager.EXPECT().GetArtist(anyCtx, newMBID).Return(&entity.Artist{Name: "PENTHOUSE", MBID: newMBID, ActivityEnded: false}, nil).Once()
		d.repo.EXPECT().UpdateMBID(anyCtx, "artist-1", newMBID, "PENTHOUSE", false).Return(nil).Once()
		after := &entity.Artist{ID: "artist-1", Name: "PENTHOUSE", MBID: newMBID, ListeningMBID: oldMBID, MBIDStatus: entity.MBIDStatusVerified}
		d.repo.EXPECT().Get(anyCtx, "artist-1").Return(after, nil).Once()

		got, err := d.uc.CorrectMBID(ctx, "artist-1", newMBID)

		require.NoError(t, err)
		assert.Equal(t, after, got)
	})

	// @spec components/usecase/artist/correct-mbid "Redirected MBID"
	t.Run("Redirected MBID taken by another artist", func(t *testing.T) {
		t.Parallel()
		const canonical = "dddddddd-0000-0000-0000-00000000000d"
		d := newArtistTestDeps(t)
		d.repo.EXPECT().Get(anyCtx, "artist-1").Return(ambiguous(), nil).Once()
		d.idManager.EXPECT().GetArtist(anyCtx, newMBID).Return(&entity.Artist{Name: "Penthouse", MBID: canonical}, nil).Once()
		d.repo.EXPECT().UpdateMBID(anyCtx, "artist-1", canonical, "Penthouse", false).Return(apperr.New(codes.AlreadyExists, "another artist has the mbid")).Once()

		_, err := d.uc.CorrectMBID(ctx, "artist-1", newMBID)

		assert.ErrorIs(t, err, apperr.ErrAlreadyExists)
	})

	// @spec components/usecase/artist/correct-mbid "Redirected MBID"
	t.Run("Redirected MBID is stored as the canonical one", func(t *testing.T) {
		t.Parallel()
		const canonical = "dddddddd-0000-0000-0000-00000000000d"
		d := newArtistTestDeps(t)
		d.repo.EXPECT().Get(anyCtx, "artist-1").Return(ambiguous(), nil).Once()
		d.idManager.EXPECT().GetArtist(anyCtx, newMBID).Return(&entity.Artist{Name: "Penthouse", MBID: canonical}, nil).Once()
		d.repo.EXPECT().UpdateMBID(anyCtx, "artist-1", canonical, "Penthouse", false).Return(nil).Once()
		after := &entity.Artist{ID: "artist-1", Name: "Penthouse", MBID: canonical, ListeningMBID: oldMBID, MBIDStatus: entity.MBIDStatusVerified}
		d.repo.EXPECT().Get(anyCtx, "artist-1").Return(after, nil).Once()

		got, err := d.uc.CorrectMBID(ctx, "artist-1", newMBID)

		require.NoError(t, err)
		assert.Equal(t, canonical, got.MBID)
	})

	// @spec components/usecase/artist/correct-mbid "Catalog does not know the MBID"
	t.Run("Catalog does not know the MBID", func(t *testing.T) {
		t.Parallel()
		d := newArtistTestDeps(t)
		d.repo.EXPECT().Get(anyCtx, "artist-1").Return(ambiguous(), nil).Once()
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
		d.repo.EXPECT().Get(anyCtx, "artist-1").Return(ambiguous(), nil).Once()
		d.idManager.EXPECT().GetArtist(anyCtx, oldMBID).Return(&entity.Artist{Name: "PENTHOUSE", MBID: oldMBID, ActivityEnded: true}, nil).Once()
		// The same MBID goes to UpdateMBID, which keeps the images and the
		// official site of an unchanged MBID and marks it Verified.
		d.repo.EXPECT().UpdateMBID(anyCtx, "artist-1", oldMBID, "PENTHOUSE", true).Return(nil).Once()
		after := &entity.Artist{ID: "artist-1", Name: "PENTHOUSE", MBID: oldMBID, ListeningMBID: oldMBID, MBIDStatus: entity.MBIDStatusVerified, ActivityEnded: true}
		d.repo.EXPECT().Get(anyCtx, "artist-1").Return(after, nil).Once()

		got, err := d.uc.CorrectMBID(ctx, "artist-1", oldMBID)

		require.NoError(t, err)
		assert.Equal(t, after, got)
	})

	t.Run("an empty catalog name keeps the current name", func(t *testing.T) {
		t.Parallel()
		d := newArtistTestDeps(t)
		d.repo.EXPECT().Get(anyCtx, "artist-1").Return(ambiguous(), nil).Once()
		d.idManager.EXPECT().GetArtist(anyCtx, newMBID).Return(&entity.Artist{Name: "", MBID: newMBID}, nil).Once()
		d.repo.EXPECT().UpdateMBID(anyCtx, "artist-1", newMBID, "Penthouse", false).Return(nil).Once()
		d.repo.EXPECT().Get(anyCtx, "artist-1").Return(correctedArtist(), nil).Once()

		_, err := d.uc.CorrectMBID(ctx, "artist-1", newMBID)

		require.NoError(t, err)
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
		d.repo.EXPECT().Get(anyCtx, "artist-1").Return(ambiguous(), nil).Once()
		d.idManager.EXPECT().GetArtist(anyCtx, newMBID).Return(&entity.Artist{Name: "Penthouse", MBID: newMBID}, nil).Once()
		d.repo.EXPECT().UpdateMBID(anyCtx, "artist-1", newMBID, "Penthouse", false).Return(apperr.New(codes.AlreadyExists, "another artist has the mbid")).Once()

		_, err := d.uc.CorrectMBID(ctx, "artist-1", newMBID)

		assert.ErrorIs(t, err, apperr.ErrAlreadyExists)
	})

	// @spec components/usecase/artist/correct-mbid "Catalog unavailable"
	t.Run("Catalog unavailable", func(t *testing.T) {
		t.Parallel()
		d := newArtistTestDeps(t)
		d.repo.EXPECT().Get(anyCtx, "artist-1").Return(ambiguous(), nil).Once()
		d.idManager.EXPECT().GetArtist(anyCtx, newMBID).Return(nil, apperr.New(codes.Unavailable, "catalog down")).Once()
		// UpdateMBID is not expected: the artist stays unchanged.

		_, err := d.uc.CorrectMBID(ctx, "artist-1", newMBID)

		assert.ErrorIs(t, err, apperr.ErrUnavailable)
	})
}

func TestArtistUseCase_ListByMBIDStatus(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// @spec components/usecase/artist/list-by-mbid-status "Ambiguous artists listed"
	t.Run("Ambiguous artists listed", func(t *testing.T) {
		t.Parallel()
		d := newArtistTestDeps(t)
		first := &entity.Artist{ID: "artist-1", Name: "Penthouse", MBID: oldMBID, MBIDStatus: entity.MBIDStatusAmbiguous}
		second := &entity.Artist{ID: "artist-2", Name: "Yama", MBID: otherID, MBIDStatus: entity.MBIDStatusAmbiguous}
		d.repo.EXPECT().ListByMBIDStatus(anyCtx, entity.MBIDStatusAmbiguous).Return([]*entity.Artist{first, second}, nil).Once()

		got, err := d.uc.ListByMBIDStatus(ctx, entity.MBIDStatusAmbiguous)

		require.NoError(t, err)
		assert.Equal(t, []*entity.Artist{first, second}, got, "the one registered first is listed first")
	})

	// @spec components/usecase/artist/list-by-mbid-status "Store unavailable"
	t.Run("Store unavailable", func(t *testing.T) {
		t.Parallel()
		d := newArtistTestDeps(t)
		d.repo.EXPECT().ListByMBIDStatus(anyCtx, entity.MBIDStatusAmbiguous).Return(nil, apperr.New(codes.Unavailable, "db down")).Once()

		_, err := d.uc.ListByMBIDStatus(ctx, entity.MBIDStatusAmbiguous)

		assert.ErrorIs(t, err, apperr.ErrUnavailable)
	})
}
