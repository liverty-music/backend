package usecase_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

const (
	// ukBand, jpBand and plBand are MBIDs of catalog artists that share the
	// name "Penthouse".
	ukBand = "c94eaf81-baae-465d-8197-9951c2ef52cd"
	jpBand = "9604bb00-65c5-4b67-a174-c46c063ba794"
	plBand = "0a427acb-c9f4-4ba6-a043-4ca854134dc0"
)

// unverifiedPenthouse is Penthouse registered with the UK band's MBID, not
// checked yet.
func unverifiedPenthouse() *entity.Artist {
	return &entity.Artist{ID: "artist-1", Name: "Penthouse", MBID: ukBand, ListeningMBID: ukBand, MBIDStatus: entity.MBIDStatusUnverified}
}

// expectNamesakes makes the catalog list the MBIDs as the artists named
// "Penthouse", each with an official homepage when sites says so.
func (d *artistTestDeps) expectNamesakes(sites map[string]bool, mbids ...string) {
	namesakes := make([]*entity.CatalogArtist, len(mbids))
	for i, mbid := range mbids {
		namesakes[i] = &entity.CatalogArtist{MBID: mbid, Name: "Penthouse"}
		url := ""
		if sites[mbid] {
			url = "https://" + mbid[:8] + ".example/"
		}
		d.officialSites.EXPECT().ListOfficialSiteRelations(anyCtx, mbid).Return(homepageRelations("Penthouse", url), nil).Once()
	}
	d.namesakes.EXPECT().ListNamesakes(anyCtx, "Penthouse").Return(namesakes, nil).Once()
}

func TestArtistUseCase_VerifyMBID(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// @spec components/usecase/artist/verify-mbid "No namesake"
	t.Run("No namesake", func(t *testing.T) {
		t.Parallel()
		d := newArtistTestDeps(t)
		artist := &entity.Artist{ID: "artist-1", Name: "ado", MBID: jpBand, ListeningMBID: jpBand, MBIDStatus: entity.MBIDStatusUnverified}
		d.repo.EXPECT().Get(anyCtx, "artist-1").Return(artist, nil).Once()
		d.namesakes.EXPECT().ListNamesakes(anyCtx, "ado").Return([]*entity.CatalogArtist{{MBID: jpBand, Name: "Ado"}}, nil).Once()
		d.idManager.EXPECT().GetArtist(anyCtx, jpBand).Return(&entity.Artist{Name: "Ado", MBID: jpBand, ActivityEnded: true}, nil).Once()
		d.repo.EXPECT().UpdateMBID(anyCtx, "artist-1", jpBand, "Ado", true).Return(nil).Once()

		require.NoError(t, d.uc.VerifyMBID(ctx, "artist-1"))
	})

	// @spec components/usecase/artist/verify-mbid "Its own MBID is the only one with an official site"
	t.Run("Its own MBID is the only one with an official site", func(t *testing.T) {
		t.Parallel()
		d := newArtistTestDeps(t)
		d.repo.EXPECT().Get(anyCtx, "artist-1").Return(unverifiedPenthouse(), nil).Once()
		d.expectNamesakes(map[string]bool{ukBand: true}, ukBand, jpBand)
		d.idManager.EXPECT().GetArtist(anyCtx, ukBand).Return(&entity.Artist{Name: "Penthouse", MBID: ukBand}, nil).Once()
		d.repo.EXPECT().UpdateMBID(anyCtx, "artist-1", ukBand, "Penthouse", false).Return(nil).Once()

		require.NoError(t, d.uc.VerifyMBID(ctx, "artist-1"))
	})

	// @spec components/usecase/artist/verify-mbid "A namesake is the only one with an official site"
	t.Run("A namesake is the only one with an official site", func(t *testing.T) {
		t.Parallel()
		d := newArtistTestDeps(t)
		d.repo.EXPECT().Get(anyCtx, "artist-1").Return(unverifiedPenthouse(), nil).Once()
		d.idManager.EXPECT().GetArtist(anyCtx, ukBand).Return(&entity.Artist{Name: "Penthouse", MBID: ukBand}, nil).Once()
		d.expectNamesakes(map[string]bool{jpBand: true}, ukBand, jpBand)
		d.idManager.EXPECT().GetArtist(anyCtx, jpBand).Return(&entity.Artist{Name: "PENTHOUSE", MBID: jpBand, ActivityEnded: false}, nil).Once()
		// UpdateMBID keeps the listening MBID; the corrected MBID and the
		// Japanese band's name and status go in one write.
		d.repo.EXPECT().UpdateMBID(anyCtx, "artist-1", jpBand, "PENTHOUSE", false).Return(nil).Once()

		require.NoError(t, d.uc.VerifyMBID(ctx, "artist-1"))
	})

	// @spec components/usecase/artist/verify-mbid "No namesake has an official site"
	t.Run("No namesake has an official site", func(t *testing.T) {
		t.Parallel()
		d := newArtistTestDeps(t)
		d.repo.EXPECT().Get(anyCtx, "artist-1").Return(unverifiedPenthouse(), nil).Once()
		d.idManager.EXPECT().GetArtist(anyCtx, ukBand).Return(&entity.Artist{Name: "Penthouse", MBID: ukBand}, nil).Once()
		d.expectNamesakes(nil, ukBand, jpBand, plBand)
		d.repo.EXPECT().MarkMBIDAmbiguous(anyCtx, "artist-1").Return(nil).Once()

		require.NoError(t, d.uc.VerifyMBID(ctx, "artist-1"))

		logs := d.logs.String()
		assert.Contains(t, logs, `"level":"ERROR"`)
		assert.Contains(t, logs, "artist MBID is ambiguous")
		assert.Contains(t, logs, `"artist_id":"artist-1"`)
		for _, mbid := range []string{ukBand, jpBand, plBand} {
			assert.Contains(t, logs, mbid)
		}
	})

	// @spec components/usecase/artist/verify-mbid "Its own MBID unknown to the catalog"
	t.Run("Its own MBID unknown to the catalog", func(t *testing.T) {
		t.Parallel()
		d := newArtistTestDeps(t)
		d.repo.EXPECT().Get(anyCtx, "artist-1").Return(unverifiedPenthouse(), nil).Once()
		d.idManager.EXPECT().GetArtist(anyCtx, ukBand).Return(nil, apperr.New(codes.NotFound, "no such artist")).Once()
		d.namesakes.EXPECT().ListNamesakes(anyCtx, "Penthouse").Return([]*entity.CatalogArtist{{MBID: jpBand, Name: "Penthouse"}}, nil).Once()
		d.officialSites.EXPECT().ListOfficialSiteRelations(anyCtx, ukBand).Return(nil, apperr.New(codes.NotFound, "no such artist")).Once()
		d.officialSites.EXPECT().ListOfficialSiteRelations(anyCtx, jpBand).Return(homepageRelations("Penthouse", "https://penthouse.example/"), nil).Once()
		d.idManager.EXPECT().GetArtist(anyCtx, jpBand).Return(&entity.Artist{Name: "Penthouse", MBID: jpBand}, nil).Once()
		d.repo.EXPECT().UpdateMBID(anyCtx, "artist-1", jpBand, "Penthouse", false).Return(nil).Once()

		require.NoError(t, d.uc.VerifyMBID(ctx, "artist-1"))
	})

	// @spec components/usecase/artist/verify-mbid "Several have an official site"
	t.Run("Several have an official site", func(t *testing.T) {
		t.Parallel()
		d := newArtistTestDeps(t)
		d.repo.EXPECT().Get(anyCtx, "artist-1").Return(unverifiedPenthouse(), nil).Once()
		d.idManager.EXPECT().GetArtist(anyCtx, ukBand).Return(&entity.Artist{Name: "Penthouse", MBID: ukBand}, nil).Once()
		d.expectNamesakes(map[string]bool{ukBand: true, jpBand: true}, ukBand, jpBand)
		d.repo.EXPECT().MarkMBIDAmbiguous(anyCtx, "artist-1").Return(nil).Once()

		require.NoError(t, d.uc.VerifyMBID(ctx, "artist-1"))

		assert.Contains(t, d.logs.String(), "artist MBID is ambiguous")
	})

	// @spec components/usecase/artist/verify-mbid "Already verified"
	t.Run("Already verified", func(t *testing.T) {
		t.Parallel()
		for _, status := range []entity.MBIDStatus{entity.MBIDStatusVerified, entity.MBIDStatusAmbiguous} {
			t.Run(string(status), func(t *testing.T) {
				t.Parallel()
				d := newArtistTestDeps(t)
				artist := unverifiedPenthouse()
				artist.MBIDStatus = status
				d.repo.EXPECT().Get(anyCtx, "artist-1").Return(artist, nil).Once()
				// No other port is expected: the mocks fail the test when one is called.

				require.NoError(t, d.uc.VerifyMBID(ctx, "artist-1"))
			})
		}
	})

	// @spec components/usecase/artist/verify-mbid "Catalog unavailable"
	t.Run("Catalog unavailable", func(t *testing.T) {
		t.Parallel()
		d := newArtistTestDeps(t)
		d.repo.EXPECT().Get(anyCtx, "artist-1").Return(unverifiedPenthouse(), nil).Once()
		d.idManager.EXPECT().GetArtist(anyCtx, ukBand).Return(&entity.Artist{Name: "Penthouse", MBID: ukBand}, nil).Once()
		d.namesakes.EXPECT().ListNamesakes(anyCtx, "Penthouse").Return(nil, apperr.New(codes.Unavailable, "catalog down")).Once()
		// No write is expected: the artist stays Unverified.

		err := d.uc.VerifyMBID(ctx, "artist-1")

		assert.ErrorIs(t, err, apperr.ErrUnavailable)
	})

	// @spec components/usecase/artist/verify-mbid "MBID the catalog has never known"
	t.Run("MBID the catalog has never known", func(t *testing.T) {
		t.Parallel()
		d := newArtistTestDeps(t)
		const unknownMBID = "e2e00000-0000-4000-8000-000000000001"
		artist := &entity.Artist{ID: "artist-1", Name: "Nobody Knows", MBID: unknownMBID, ListeningMBID: unknownMBID, MBIDStatus: entity.MBIDStatusUnverified}
		d.repo.EXPECT().Get(anyCtx, "artist-1").Return(artist, nil).Once()
		d.namesakes.EXPECT().ListNamesakes(anyCtx, "Nobody Knows").Return([]*entity.CatalogArtist{}, nil).Once()
		d.idManager.EXPECT().GetArtist(anyCtx, unknownMBID).Return(nil, apperr.New(codes.NotFound, "no such artist")).Once()
		d.repo.EXPECT().MarkMBIDAmbiguous(anyCtx, "artist-1").Return(nil).Once()

		require.NoError(t, d.uc.VerifyMBID(ctx, "artist-1"))

		logs := d.logs.String()
		assert.Contains(t, logs, `"level":"ERROR"`)
		assert.Contains(t, logs, "artist MBID is ambiguous")
		assert.Contains(t, logs, `"artist_id":"artist-1"`)
		assert.Contains(t, logs, unknownMBID)
	})

	// @spec components/usecase/artist/verify-mbid "Catalog unavailable when reading the name"
	t.Run("Catalog unavailable when reading the name", func(t *testing.T) {
		t.Parallel()
		d := newArtistTestDeps(t)
		d.repo.EXPECT().Get(anyCtx, "artist-1").Return(unverifiedPenthouse(), nil).Once()
		d.idManager.EXPECT().GetArtist(anyCtx, ukBand).Return(&entity.Artist{Name: "Penthouse", MBID: ukBand}, nil).Once()
		d.expectNamesakes(map[string]bool{jpBand: true}, ukBand, jpBand)
		d.idManager.EXPECT().GetArtist(anyCtx, jpBand).Return(nil, apperr.New(codes.Unavailable, "catalog down")).Once()
		// No write is expected: nothing is stored before every read succeeded.

		err := d.uc.VerifyMBID(ctx, "artist-1")

		assert.ErrorIs(t, err, apperr.ErrUnavailable)
	})

	// @spec components/usecase/artist/verify-mbid "Unknown artist"
	t.Run("Unknown artist", func(t *testing.T) {
		t.Parallel()
		d := newArtistTestDeps(t)
		d.repo.EXPECT().Get(anyCtx, "artist-1").Return(nil, apperr.New(codes.NotFound, "artist not found")).Once()

		err := d.uc.VerifyMBID(ctx, "artist-1")

		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})

	// @spec components/usecase/artist/verify-mbid "Corrected MBID already registered"
	t.Run("Corrected MBID already registered", func(t *testing.T) {
		t.Parallel()
		d := newArtistTestDeps(t)
		d.repo.EXPECT().Get(anyCtx, "artist-1").Return(unverifiedPenthouse(), nil).Once()
		d.idManager.EXPECT().GetArtist(anyCtx, ukBand).Return(&entity.Artist{Name: "Penthouse", MBID: ukBand}, nil).Once()
		d.expectNamesakes(map[string]bool{jpBand: true}, ukBand, jpBand)
		d.idManager.EXPECT().GetArtist(anyCtx, jpBand).Return(&entity.Artist{Name: "Penthouse", MBID: jpBand}, nil).Once()
		d.repo.EXPECT().UpdateMBID(anyCtx, "artist-1", jpBand, "Penthouse", false).Return(apperr.New(codes.AlreadyExists, "another artist has the mbid")).Once()
		d.repo.EXPECT().MarkMBIDAmbiguous(anyCtx, "artist-1").Return(nil).Once()

		require.NoError(t, d.uc.VerifyMBID(ctx, "artist-1"))

		logs := d.logs.String()
		assert.Contains(t, logs, "artist MBID is ambiguous")
		assert.Contains(t, logs, `"artist_id":"artist-1"`)
		assert.Contains(t, logs, "another artist holds MBID "+jpBand)
	})

	// @spec components/usecase/artist/verify-mbid "Merged MBID"
	t.Run("Merged MBID", func(t *testing.T) {
		t.Parallel()
		d := newArtistTestDeps(t)
		// The listening service files the artist under ukBand, which the
		// catalog merged into jpBand: the lookup answers with jpBand, and the
		// name search lists only jpBand.
		d.repo.EXPECT().Get(anyCtx, "artist-1").Return(unverifiedPenthouse(), nil).Once()
		d.idManager.EXPECT().GetArtist(anyCtx, ukBand).Return(&entity.Artist{Name: "PENTHOUSE", MBID: jpBand, ActivityEnded: true}, nil).Once()
		d.namesakes.EXPECT().ListNamesakes(anyCtx, "Penthouse").Return([]*entity.CatalogArtist{{MBID: jpBand, Name: "PENTHOUSE"}}, nil).Once()
		// No official-site lookup is expected: the survivor is the artist itself.
		d.repo.EXPECT().UpdateMBID(anyCtx, "artist-1", jpBand, "PENTHOUSE", true).Return(nil).Once()

		require.NoError(t, d.uc.VerifyMBID(ctx, "artist-1"))
	})

	t.Run("a namesake the catalog does not know makes the MBID ambiguous", func(t *testing.T) {
		t.Parallel()
		d := newArtistTestDeps(t)
		d.repo.EXPECT().Get(anyCtx, "artist-1").Return(unverifiedPenthouse(), nil).Once()
		d.idManager.EXPECT().GetArtist(anyCtx, ukBand).Return(&entity.Artist{Name: "Penthouse", MBID: ukBand}, nil).Once()
		d.expectNamesakes(map[string]bool{jpBand: true}, ukBand, jpBand)
		d.idManager.EXPECT().GetArtist(anyCtx, jpBand).Return(nil, apperr.New(codes.NotFound, "no such artist")).Once()
		d.repo.EXPECT().MarkMBIDAmbiguous(anyCtx, "artist-1").Return(nil).Once()

		require.NoError(t, d.uc.VerifyMBID(ctx, "artist-1"))

		assert.Contains(t, d.logs.String(), "the catalog does not know MBID "+jpBand)
	})
}

func TestArtistUseCase_RefreshDue_VerifiesUnverified(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// unverified returns n Unverified artists, oldest first, with no namesake.
	unverified := func(n int) []*entity.Artist {
		artists := make([]*entity.Artist, n)
		for i := range artists {
			mbid := fmt.Sprintf("%08d-0000-4000-8000-000000000000", i)
			artists[i] = &entity.Artist{ID: fmt.Sprintf("artist-%d", i), Name: fmt.Sprintf("Band %d", i), MBID: mbid, ListeningMBID: mbid, MBIDStatus: entity.MBIDStatusUnverified}
		}
		return artists
	}

	// @spec components/usecase/artist/verify-mbid "Announcement retries exhausted"
	t.Run("the daily run verifies an artist its announcement left unverified", func(t *testing.T) {
		t.Parallel()
		d := newArtistTestDeps(t)
		artists := unverified(2)
		var order []string
		d.repo.EXPECT().ListByMBIDStatus(anyCtx, entity.MBIDStatusUnverified).Return(artists, nil).Once()
		for _, a := range artists {
			d.repo.EXPECT().Get(anyCtx, a.ID).Return(a, nil).Once()
			d.expectNoNamesake(a.Name, a.MBID, a.Name)
			d.repo.EXPECT().UpdateMBID(anyCtx, a.ID, a.MBID, a.Name, false).
				Run(func(_ context.Context, id, _, _ string, _ bool) { order = append(order, id) }).Return(nil).Once()
		}
		d.repo.EXPECT().ListRefreshDue(anyCtx, 30, mock.AnythingOfType("int")).Return([]*entity.Artist{}, nil).Once()

		summary, err := d.uc.RefreshDue(ctx)

		require.NoError(t, err)
		assert.Equal(t, []string{"artist-0", "artist-1"}, order, "oldest first, one at a time")
		assert.Equal(t, 2, summary.Unverified)
		assert.Equal(t, 2, summary.Verified)
		assert.False(t, summary.VerifyCircuitBroken)
	})

	// @spec components/usecase/artist/verify-mbid "Consecutive failures in the daily run"
	t.Run("the daily run stops verifying after 3 consecutive failures", func(t *testing.T) {
		t.Parallel()
		d := newArtistTestDeps(t)
		artists := unverified(5)
		d.repo.EXPECT().ListByMBIDStatus(anyCtx, entity.MBIDStatusUnverified).Return(artists, nil).Once()
		// The first three verifications fail; nothing is written for them and
		// the last two artists are never looked at.
		for _, a := range artists[:3] {
			d.repo.EXPECT().Get(anyCtx, a.ID).Return(a, nil).Once()
			d.idManager.EXPECT().GetArtist(anyCtx, a.MBID).Return(nil, apperr.New(codes.Unavailable, "catalog down")).Once()
		}
		d.repo.EXPECT().ListRefreshDue(anyCtx, 30, mock.AnythingOfType("int")).Return([]*entity.Artist{}, nil).Once()

		summary, err := d.uc.RefreshDue(ctx)

		require.NoError(t, err)
		assert.True(t, summary.VerifyCircuitBroken)
		assert.Equal(t, 3, summary.VerifyAttempted)
		assert.Equal(t, 3, summary.VerifyFailed)
	})

	t.Run("an ambiguous artist is counted and the run goes on", func(t *testing.T) {
		t.Parallel()
		d := newArtistTestDeps(t)
		penthouse := unverifiedPenthouse()
		d.repo.EXPECT().ListByMBIDStatus(anyCtx, entity.MBIDStatusUnverified).Return([]*entity.Artist{penthouse}, nil).Once()
		d.repo.EXPECT().Get(anyCtx, penthouse.ID).Return(penthouse, nil).Once()
		d.idManager.EXPECT().GetArtist(anyCtx, ukBand).Return(&entity.Artist{Name: "Penthouse", MBID: ukBand}, nil).Once()
		d.expectNamesakes(nil, ukBand, jpBand)
		d.repo.EXPECT().MarkMBIDAmbiguous(anyCtx, penthouse.ID).Return(nil).Once()
		d.repo.EXPECT().ListRefreshDue(anyCtx, 30, mock.AnythingOfType("int")).Return([]*entity.Artist{}, nil).Once()

		summary, err := d.uc.RefreshDue(ctx)

		require.NoError(t, err)
		assert.Equal(t, 1, summary.Ambiguous)
	})

	t.Run("listing the unverified artists fails", func(t *testing.T) {
		t.Parallel()
		d := newArtistTestDeps(t)
		d.repo.EXPECT().ListByMBIDStatus(anyCtx, entity.MBIDStatusUnverified).Return(nil, apperr.ErrInternal).Once()

		_, err := d.uc.RefreshDue(ctx)

		assert.ErrorIs(t, err, apperr.ErrInternal)
	})

}
