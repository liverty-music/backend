package usecase_test

import (
	"bytes"
	"context"
	"fmt"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/entity/mocks"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/liverty-music/backend/pkg/cache"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// anyCtx matches any context.Context regardless of type (e.g. context.WithoutCancel).
var anyCtx = mock.MatchedBy(func(context.Context) bool { return true })

// artistTestDeps holds all dependencies for ArtistUseCase tests.
type artistTestDeps struct {
	repo          *mocks.MockArtistRepository
	searcher      *mocks.MockArtistSearcher
	idManager     *mocks.MockArtistIdentityManager
	officialSites *mocks.MockOfficialSiteCatalog
	namesakes     *mocks.MockNamesakeCatalog
	uc            usecase.ArtistUseCase
	// logs holds what the use case logged, as JSON lines.
	logs *bytes.Buffer
}

func newArtistTestDeps(t *testing.T) *artistTestDeps {
	t.Helper()
	logger, logs := newCaptureLogger(t)
	d := &artistTestDeps{
		repo:          mocks.NewMockArtistRepository(t),
		searcher:      mocks.NewMockArtistSearcher(t),
		idManager:     mocks.NewMockArtistIdentityManager(t),
		officialSites: mocks.NewMockOfficialSiteCatalog(t),
		namesakes:     mocks.NewMockNamesakeCatalog(t),
		logs:          logs,
	}
	// The cache uses the production TTL so the expiry scenarios exercise the
	// real value; tests that cross it run inside a synctest bubble.
	c := cache.NewMemoryCache(usecase.ArtistCacheTTL)
	t.Cleanup(func() { assert.NoError(t, c.Close()) })
	d.uc = usecase.NewArtistUseCase(d.repo, mocks.NewMockOrganizerRepository(t), d.searcher, d.idManager,
		d.officialSites, d.namesakes, c, time.Now, logger)
	return d
}

// verifiedArtist returns a registered artist found under mbid whose MBID is
// Verified and whose activity has not ended: an artist offered to fans.
func verifiedArtist(id, name, mbid string) *entity.Artist {
	return &entity.Artist{ID: id, Name: name, MBID: mbid, ListeningMBID: mbid, MBIDStatus: entity.MBIDStatusVerified}
}

// unverifiedArtist returns a registered artist found under mbid whose MBID is
// still Unverified.
func unverifiedArtist(id, name, mbid string) *entity.Artist {
	return &entity.Artist{ID: id, Name: name, MBID: mbid, ListeningMBID: mbid, MBIDStatus: entity.MBIDStatusUnverified}
}

// registeredArtists returns n catalog results with an MBID and the same
// artists as already registered and offered to fans, in the same order.
func registeredArtists(t *testing.T, n int) (fetched, registered []*entity.Artist, mbids []string) {
	t.Helper()
	registered = make([]*entity.Artist, 0, n)
	for i := range n {
		mbid := fmt.Sprintf("mbid-%02d", i)
		name := fmt.Sprintf("Artist %02d", i)
		fetched = append(fetched, entity.NewArtist(name, mbid))
		registered = append(registered, verifiedArtist(fmt.Sprintf("id-%02d", i), name, mbid))
		mbids = append(mbids, mbid)
	}
	return fetched, registered, mbids
}

// homepageRelations returns the catalog's official homepage links of an artist:
// one top-page link at url, or none when url is empty.
func homepageRelations(name, url string) *entity.OfficialSiteRelations {
	rels := &entity.OfficialSiteRelations{ArtistName: name}
	if url != "" {
		rels.Relations = []entity.OfficialSiteRelation{{URL: url}}
	}
	return rels
}

// expectNoNamesake makes the catalog know the name only under mbid, under the
// catalog name catalogName, so the MBID is verified as it is.
func (d *artistTestDeps) expectNoNamesake(name, mbid, catalogName string) {
	d.namesakes.EXPECT().ListNamesakes(anyCtx, name).Return([]*entity.CatalogArtist{{MBID: mbid, Name: name}}, nil).Once()
	d.idManager.EXPECT().GetArtist(anyCtx, mbid).Return(&entity.Artist{Name: catalogName, MBID: mbid}, nil).Once()
}

// withStatus matches a registration of an artist with the listening MBID, the
// MBID and the MBID status.
func withStatus(listening, mbid string, status entity.MBIDStatus) any {
	return mock.MatchedBy(func(a *entity.Artist) bool {
		return a.ListeningMBID == listening && a.MBID == mbid && a.MBIDStatus == status
	})
}

func TestArtistUseCase_CreateArtist(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	const mbid = "5b11f448-2d57-455b-8292-629df8357062"

	t.Run("catalog has a different name", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/artist/create "Catalog has a different name"
		d := newArtistTestDeps(t)
		artist := &entity.Artist{Name: "the band", MBID: mbid}
		// @spec components/usecase/artist/create "Catalog lists aliases"
		names := []string{"the band", "ザ・バンド"}
		d.idManager.EXPECT().GetArtist(anyCtx, mbid).Return(&entity.Artist{Name: "The Band", MBID: mbid, KnownNames: names}, nil).Once()
		d.repo.EXPECT().Create(anyCtx, mock.MatchedBy(func(a *entity.Artist) bool {
			return a.Name == "The Band" && a.MBID == mbid && a.ListeningMBID == mbid && a.MBIDStatus == entity.MBIDStatusVerified &&
				slices.Equal(a.KnownNames, names)
		})).RunAndReturn(func(_ context.Context, a ...*entity.Artist) ([]*entity.Artist, error) {
			return a, nil
		}).Once()

		got, err := d.uc.Create(ctx, artist)

		require.NoError(t, err)
		assert.Equal(t, "The Band", got.Name)
		assert.Equal(t, entity.MBIDStatusVerified, got.MBIDStatus)
	})

	t.Run("catalog lookup fails", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/artist/create "Catalog lookup fails"
		d := newArtistTestDeps(t)
		artist := &entity.Artist{Name: "the band", MBID: mbid, ActivityEnded: true}
		d.idManager.EXPECT().GetArtist(anyCtx, mbid).Return(nil, apperr.New(codes.Unavailable, "catalog down")).Once()
		d.repo.EXPECT().Create(anyCtx, mock.MatchedBy(func(a *entity.Artist) bool {
			// No known name is given, so Artist.Create stores the name as the only one.
			return a.Name == "the band" && a.ListeningMBID == mbid && a.MBIDStatus == entity.MBIDStatusVerified && !a.ActivityEnded &&
				a.KnownNames == nil
		})).RunAndReturn(func(_ context.Context, a ...*entity.Artist) ([]*entity.Artist, error) {
			return a, nil
		}).Once()

		got, err := d.uc.Create(ctx, artist)

		require.NoError(t, err)
		assert.Equal(t, "the band", got.Name)
		assert.Equal(t, entity.MBIDStatusVerified, got.MBIDStatus)
		assert.False(t, got.ActivityEnded)
	})

	t.Run("ended artist registered", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/artist/create "Ended artist registered"
		d := newArtistTestDeps(t)
		d.idManager.EXPECT().GetArtist(anyCtx, mbid).Return(&entity.Artist{Name: "SMAP", MBID: mbid, ActivityEnded: true}, nil).Once()
		d.repo.EXPECT().Create(anyCtx, mock.MatchedBy(func(a *entity.Artist) bool {
			return a.ActivityEnded && a.MBIDStatus == entity.MBIDStatusVerified
		})).RunAndReturn(func(_ context.Context, a ...*entity.Artist) ([]*entity.Artist, error) {
			return a, nil
		}).Once()

		got, err := d.uc.Create(ctx, &entity.Artist{Name: "SMAP", MBID: mbid})

		require.NoError(t, err)
		assert.True(t, got.ActivityEnded)
	})

	t.Run("test artist", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/artist/create "Test artist"
		d := newArtistTestDeps(t)
		const testMBID = "e2e00000-0000-4000-8000-000000000001"
		d.idManager.EXPECT().GetArtist(anyCtx, testMBID).Return(nil, apperr.New(codes.NotFound, "no such artist")).Once()
		d.repo.EXPECT().Create(anyCtx, mock.MatchedBy(func(a *entity.Artist) bool {
			return a.Name == "E2E Test Artist" && a.MBID == testMBID && a.ListeningMBID == testMBID &&
				a.MBIDStatus == entity.MBIDStatusVerified && !a.ActivityEnded
		})).RunAndReturn(func(_ context.Context, a ...*entity.Artist) ([]*entity.Artist, error) {
			return a, nil
		}).Once()

		got, err := d.uc.Create(ctx, &entity.Artist{Name: "E2E Test Artist", MBID: testMBID})

		require.NoError(t, err)
		// Registered Verified, so its announcement's VerifyMBID changes nothing.
		assert.Equal(t, entity.MBIDStatusVerified, got.MBIDStatus)
	})

	t.Run("MBID already registered", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/artist/create "MBID already registered"
		d := newArtistTestDeps(t)
		registered := verifiedArtist("artist-1", "The Band", mbid)
		d.idManager.EXPECT().GetArtist(anyCtx, mbid).Return(&entity.Artist{Name: "The Band", MBID: mbid}, nil).Once()
		d.repo.EXPECT().Create(anyCtx, mock.AnythingOfType("*entity.Artist")).Return([]*entity.Artist{registered}, nil).Once()

		got, err := d.uc.Create(ctx, &entity.Artist{Name: "The Band", MBID: mbid})

		require.NoError(t, err)
		assert.Same(t, registered, got, "the registered artist is returned unchanged, whatever its mbid_status")
	})

	t.Run("no announcement", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/artist/create "No announcement"
		// Artist.Create announces the registered artist; the use case has no
		// publisher to announce with.
		d := newArtistTestDeps(t)
		artist := &entity.Artist{ID: "artist-1", Name: "New Band", MBID: "11111111-1111-1111-1111-111111111111"}
		d.idManager.EXPECT().GetArtist(ctx, artist.MBID).Return(&entity.Artist{MBID: artist.MBID, Name: artist.Name}, nil).Once()
		d.repo.EXPECT().Create(ctx, artist).Return([]*entity.Artist{artist}, nil).Once()

		_, err := d.uc.Create(ctx, artist)

		assert.NoError(t, err)
	})

	t.Run("registration rejected", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/artist/create "Registration rejected"
		d := newArtistTestDeps(t)
		artist := &entity.Artist{Name: "Rejected", MBID: mbid}
		d.idManager.EXPECT().GetArtist(anyCtx, mbid).Return(&entity.Artist{Name: "Rejected", MBID: mbid}, nil).Once()
		d.repo.EXPECT().Create(anyCtx, artist).Return(nil, apperr.ErrInvalidArgument).Once()

		_, err := d.uc.Create(ctx, artist)

		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
	})

	t.Run("nothing returned", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/artist/create "Nothing returned"
		d := newArtistTestDeps(t)
		artist := &entity.Artist{Name: "Ghost", MBID: mbid}
		d.idManager.EXPECT().GetArtist(anyCtx, mbid).Return(&entity.Artist{Name: "Ghost", MBID: mbid}, nil).Once()
		d.repo.EXPECT().Create(anyCtx, artist).Return([]*entity.Artist{}, nil).Once()

		_, err := d.uc.Create(ctx, artist)

		assert.ErrorIs(t, err, apperr.ErrInternal)
	})

	t.Run("malformed MBID", func(t *testing.T) {
		t.Parallel()
		d := newArtistTestDeps(t)
		// No port is expected: the mocks fail the test when one is called.

		_, err := d.uc.Create(ctx, &entity.Artist{Name: "No MBID"})

		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
	})
}

func TestOfficialSiteUseCase_Create(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	t.Run("mints a fresh id and persists the site", func(t *testing.T) {
		t.Parallel()
		d := newArtistTestDeps(t)

		var captured *entity.OfficialSite
		d.repo.EXPECT().
			CreateOfficialSite(anyCtx, mock.MatchedBy(func(site *entity.OfficialSite) bool {
				captured = site
				return site.ID != "" && site.ArtistID == "artist-1" && site.URL == "https://example.com"
			})).
			Return(nil).
			Once()

		err := usecase.NewOfficialSiteUseCase(d.repo).Create(ctx, "artist-1", "https://example.com")

		assert.NoError(t, err)
		require.NotNil(t, captured)
		assert.NotEmpty(t, captured.ID)
	})

	t.Run("generates a different id on each call", func(t *testing.T) {
		t.Parallel()
		d := newArtistTestDeps(t)

		var ids []string
		d.repo.EXPECT().
			CreateOfficialSite(anyCtx, mock.AnythingOfType("*entity.OfficialSite")).
			Run(func(_ context.Context, site *entity.OfficialSite) { ids = append(ids, site.ID) }).
			Return(nil).
			Twice()

		require.NoError(t, usecase.NewOfficialSiteUseCase(d.repo).Create(ctx, "artist-1", "https://example.com"))
		require.NoError(t, usecase.NewOfficialSiteUseCase(d.repo).Create(ctx, "artist-1", "https://example.com"))

		require.Len(t, ids, 2)
		assert.NotEqual(t, ids[0], ids[1])
	})

	t.Run("returns the repository error unchanged", func(t *testing.T) {
		t.Parallel()
		d := newArtistTestDeps(t)

		d.repo.EXPECT().
			CreateOfficialSite(anyCtx, mock.AnythingOfType("*entity.OfficialSite")).
			Return(apperr.ErrAlreadyExists).
			Once()

		err := usecase.NewOfficialSiteUseCase(d.repo).Create(ctx, "artist-1", "https://example.com")

		assert.ErrorIs(t, err, apperr.ErrAlreadyExists)
	})
}

func TestOfficialSiteUseCase_Update(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	t.Run("artist with a site", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/official-site/update "Artist with a site"
		d := newArtistTestDeps(t)
		d.repo.EXPECT().UpdateOfficialSiteURL(anyCtx, "artist-1", "https://www.example.com").Return(nil).Once()

		err := usecase.NewOfficialSiteUseCase(d.repo).Update(ctx, "artist-1", "https://www.example.com")

		assert.NoError(t, err)
	})

	t.Run("artist without a site", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/official-site/update "Artist without a site"
		d := newArtistTestDeps(t)
		d.repo.EXPECT().UpdateOfficialSiteURL(anyCtx, "artist-1", "https://www.example.com").Return(apperr.ErrNotFound).Once()

		err := usecase.NewOfficialSiteUseCase(d.repo).Update(ctx, "artist-1", "https://www.example.com")

		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})
}

func TestArtistUseCase_ListArtists(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		t.Parallel()
		d := newArtistTestDeps(t)

		artists := []*entity.Artist{
			{ID: "1", Name: "Artist 1"},
			{ID: "2", Name: "Artist 2"},
		}

		d.repo.EXPECT().List(ctx).Return(artists, nil).Once()

		result, err := d.uc.List(ctx)

		assert.NoError(t, err)
		assert.Len(t, result, 2)
		assert.Equal(t, artists, result)
	})
}

func TestArtistUseCase_ListTop(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	// @spec components/usecase/artist/list-top "First call for a country"
	t.Run("registers the chart artists unverified and returns none of them", func(t *testing.T) {
		t.Parallel()
		d := newArtistTestDeps(t)

		fetched := []*entity.Artist{entity.NewArtist("Artist A", "mbid-a"), entity.NewArtist("Artist B", "mbid-b")}
		created := []*entity.Artist{unverifiedArtist("id-a", "Artist A", "mbid-a"), unverifiedArtist("id-b", "Artist B", "mbid-b")}

		d.searcher.EXPECT().ListTop(ctx, "JP", "", int32(0)).Return(fetched, nil).Once()
		d.repo.EXPECT().ListByMBIDs(mock.Anything, []string{"mbid-a", "mbid-b"}).Return([]*entity.Artist{}, nil).Once()
		d.repo.EXPECT().Create(mock.Anything,
			withStatus("mbid-a", "mbid-a", entity.MBIDStatusUnverified),
			withStatus("mbid-b", "mbid-b", entity.MBIDStatusUnverified),
		).Return(created, nil).Once()

		result, err := d.uc.ListTop(ctx, "JP", "", int32(0))

		require.NoError(t, err)
		assert.Empty(t, result)
	})

	// @spec components/usecase/artist/list-top "Chart artist without MBID"
	t.Run("neither registers nor returns an entry without an MBID", func(t *testing.T) {
		t.Parallel()
		d := newArtistTestDeps(t)

		known := verifiedArtist("id-x", "With MBID", "mbid-x")
		d.searcher.EXPECT().ListTop(ctx, "JP", "", int32(0)).Return([]*entity.Artist{
			entity.NewArtist("With MBID", "mbid-x"),
			entity.NewArtist("No MBID", ""),
		}, nil).Once()
		// No Create expectation: registering the entry without an MBID fails the mock.
		d.repo.EXPECT().ListByMBIDs(mock.Anything, []string{"mbid-x"}).Return([]*entity.Artist{known}, nil).Once()

		result, err := d.uc.ListTop(ctx, "JP", "", int32(0))

		require.NoError(t, err)
		assert.Equal(t, []*entity.Artist{known}, result)
	})

	t.Run("error - searcher fails", func(t *testing.T) {
		t.Parallel()
		d := newArtistTestDeps(t)

		d.searcher.EXPECT().ListTop(ctx, "JP", "", int32(0)).Return(nil, apperr.ErrInternal).Once()

		result, err := d.uc.ListTop(ctx, "JP", "", int32(0))

		assert.ErrorIs(t, err, apperr.ErrInternal)
		assert.Nil(t, result)
	})

	t.Run("returns cached results on a repeated call", func(t *testing.T) {
		t.Parallel()

		// @spec components/usecase/artist/list-top "Repeated call"
		synctest.Test(t, func(t *testing.T) {
			d := newArtistTestDeps(t)

			fetched, registered, mbids := registeredArtists(t, 1)
			d.searcher.EXPECT().ListTop(ctx, "US", "", int32(0)).Return(fetched, nil).Once()
			d.repo.EXPECT().ListByMBIDs(mock.Anything, mbids).Return(registered, nil).Once()

			first, err := d.uc.ListTop(ctx, "US", "", int32(0))
			require.NoError(t, err)

			time.Sleep(10 * time.Minute)

			// Cache hit: the Once() expectation above fails on a second lookup;
			// the hit reads the artists' current status with one ListByMBIDs.
			d.repo.EXPECT().ListByMBIDs(mock.Anything, mbids).Return(registered, nil).Once()
			second, err := d.uc.ListTop(ctx, "US", "", int32(0))
			assert.NoError(t, err)
			assert.Equal(t, first, second)
		})
	})

	t.Run("consults the catalog again after the cache TTL", func(t *testing.T) {
		t.Parallel()

		// @spec components/usecase/artist/list-top "Result older than 6 hours"
		synctest.Test(t, func(t *testing.T) {
			d := newArtistTestDeps(t)

			fetched, registered, mbids := registeredArtists(t, 1)
			d.searcher.EXPECT().ListTop(ctx, "JP", "rock", int32(10)).Return(fetched, nil).Twice()
			d.repo.EXPECT().ListByMBIDs(mock.Anything, mbids).Return(registered, nil).Twice()

			_, err := d.uc.ListTop(ctx, "JP", "rock", int32(10))
			require.NoError(t, err)

			time.Sleep(usecase.ArtistCacheTTL + time.Minute)

			result, err := d.uc.ListTop(ctx, "JP", "rock", int32(10))
			assert.NoError(t, err)
			assert.Equal(t, registered, result)
		})
	})

	// @spec components/usecase/artist/list-top "Repeated call keeps identities"
	t.Run("resolves the MBID to the artist registered once, returned once verified", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			d := newArtistTestDeps(t)

			created := unverifiedArtist("id-a", "Artist A", "mbid-a")
			d.searcher.EXPECT().ListTop(ctx, "JP", "", int32(0)).Return([]*entity.Artist{entity.NewArtist("Artist A", "mbid-a")}, nil).Twice()
			// First call registers the artist; the second, after the cache
			// expired, finds it registered (and Verified by now) and creates nothing.
			d.repo.EXPECT().ListByMBIDs(mock.Anything, []string{"mbid-a"}).Return([]*entity.Artist{}, nil).Once()
			d.repo.EXPECT().Create(mock.Anything, mock.AnythingOfType("*entity.Artist")).Return([]*entity.Artist{created}, nil).Once()

			first, err := d.uc.ListTop(ctx, "JP", "", int32(0))
			require.NoError(t, err)
			assert.Empty(t, first)

			time.Sleep(usecase.ArtistCacheTTL + time.Minute)
			verified := verifiedArtist("id-a", "Artist A", "mbid-a")
			d.repo.EXPECT().ListByMBIDs(mock.Anything, []string{"mbid-a"}).Return([]*entity.Artist{verified}, nil).Once()

			second, err := d.uc.ListTop(ctx, "JP", "", int32(0))
			require.NoError(t, err)
			require.Len(t, second, 1)
			assert.Equal(t, created.ID, second[0].ID)
		})
	})

	// @spec components/usecase/artist/list-top "Chart artist already registered"
	t.Run("returns the registered artist without registering it again", func(t *testing.T) {
		t.Parallel()
		d := newArtistTestDeps(t)

		fetched, registered, mbids := registeredArtists(t, 2)
		d.searcher.EXPECT().ListTop(ctx, "JP", "", int32(0)).Return(fetched, nil).Once()
		// No Create expectation: registering again fails the mock.
		d.repo.EXPECT().ListByMBIDs(mock.Anything, mbids).Return(registered, nil).Once()

		result, err := d.uc.ListTop(ctx, "JP", "", int32(0))

		require.NoError(t, err)
		assert.Equal(t, registered, result)
	})

	// @spec components/usecase/artist/list-top "Empty chart"
	t.Run("returns an empty list for an empty chart", func(t *testing.T) {
		t.Parallel()
		d := newArtistTestDeps(t)

		d.searcher.EXPECT().ListTop(ctx, "JP", "", int32(0)).Return([]*entity.Artist{}, nil).Once()

		result, err := d.uc.ListTop(ctx, "JP", "", int32(0))

		require.NoError(t, err)
		assert.Empty(t, result)
	})

	// @spec components/usecase/artist/list-top "Chart artist under a replaced MBID"
	t.Run("returns the artist registered under the listening MBID", func(t *testing.T) {
		t.Parallel()
		d := newArtistTestDeps(t)

		corrected := correctedArtist()
		d.searcher.EXPECT().ListTop(ctx, "JP", "", int32(0)).Return([]*entity.Artist{entity.NewArtist("Penthouse", oldMBID)}, nil).Once()
		d.repo.EXPECT().ListByMBIDs(mock.Anything, []string{oldMBID}).Return([]*entity.Artist{corrected}, nil).Once()
		// No Create expectation: registering fails the mock.

		result, err := d.uc.ListTop(ctx, "JP", "", int32(0))

		require.NoError(t, err)
		assert.Equal(t, []*entity.Artist{corrected}, result)
	})

	// @spec components/usecase/artist/list-top "Chart lists an ended artist"
	t.Run("leaves out an ended artist and keeps the chart order", func(t *testing.T) {
		t.Parallel()
		d := newArtistTestDeps(t)

		fetched, registered, mbids := registeredArtists(t, 3)
		registered[1].ActivityEnded = true
		d.searcher.EXPECT().ListTop(ctx, "JP", "", int32(0)).Return(fetched, nil).Once()
		d.repo.EXPECT().ListByMBIDs(mock.Anything, mbids).Return(registered, nil).Once()

		result, err := d.uc.ListTop(ctx, "JP", "", int32(0))

		require.NoError(t, err)
		assert.Equal(t, []*entity.Artist{registered[0], registered[2]}, result)
	})

	// @spec components/usecase/artist/list-top "Artist marked ended after the result was reused"
	t.Run("leaves out an artist marked ended since the cached result", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			d := newArtistTestDeps(t)

			fetched, registered, mbids := registeredArtists(t, 3)
			// Once(): the catalog is consulted for the first call only.
			d.searcher.EXPECT().ListTop(ctx, "JP", "", int32(0)).Return(fetched, nil).Once()
			d.repo.EXPECT().ListByMBIDs(mock.Anything, mbids).Return(registered, nil).Once()

			first, err := d.uc.ListTop(ctx, "JP", "", int32(0))
			require.NoError(t, err)
			require.Len(t, first, 3)

			time.Sleep(time.Hour)
			ended := verifiedArtist("id-01", "Artist 01", "mbid-01")
			ended.ActivityEnded = true
			d.repo.EXPECT().ListByMBIDs(mock.Anything, mbids).Return([]*entity.Artist{registered[0], ended, registered[2]}, nil).Once()

			second, err := d.uc.ListTop(ctx, "JP", "", int32(0))

			require.NoError(t, err)
			assert.Equal(t, []*entity.Artist{registered[0], registered[2]}, second)
		})
	})

	// @spec components/usecase/artist/list-top "Chart artist registered by this call"
	t.Run("does not return an artist it registered", func(t *testing.T) {
		t.Parallel()
		d := newArtistTestDeps(t)

		known := verifiedArtist("id-known", "Known", "mbid-known")
		created := unverifiedArtist("id-new", "New", "mbid-new")
		d.searcher.EXPECT().ListTop(ctx, "JP", "", int32(0)).Return([]*entity.Artist{
			entity.NewArtist("Known", "mbid-known"), entity.NewArtist("New", "mbid-new"),
		}, nil).Once()
		d.repo.EXPECT().ListByMBIDs(mock.Anything, []string{"mbid-known", "mbid-new"}).Return([]*entity.Artist{known}, nil).Once()
		d.repo.EXPECT().Create(mock.Anything, withStatus("mbid-new", "mbid-new", entity.MBIDStatusUnverified)).Return([]*entity.Artist{created}, nil).Once()

		result, err := d.uc.ListTop(ctx, "JP", "", int32(0))

		require.NoError(t, err)
		assert.Equal(t, []*entity.Artist{known}, result)
	})

	// @spec components/usecase/artist/list-top "Chart artist verified after the result"
	t.Run("returns an artist verified since the cached result", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			d := newArtistTestDeps(t)

			created := unverifiedArtist("id-new", "New", "mbid-new")
			d.searcher.EXPECT().ListTop(ctx, "JP", "", int32(0)).Return([]*entity.Artist{entity.NewArtist("New", "mbid-new")}, nil).Once()
			d.repo.EXPECT().ListByMBIDs(mock.Anything, []string{"mbid-new"}).Return([]*entity.Artist{}, nil).Once()
			d.repo.EXPECT().Create(mock.Anything, mock.AnythingOfType("*entity.Artist")).Return([]*entity.Artist{created}, nil).Once()

			first, err := d.uc.ListTop(ctx, "JP", "", int32(0))
			require.NoError(t, err)
			require.Empty(t, first)

			time.Sleep(time.Hour)
			verified := verifiedArtist("id-new", "New", "mbid-new")
			d.repo.EXPECT().ListByMBIDs(mock.Anything, []string{"mbid-new"}).Return([]*entity.Artist{verified}, nil).Once()

			second, err := d.uc.ListTop(ctx, "JP", "", int32(0))

			require.NoError(t, err)
			assert.Equal(t, []*entity.Artist{verified}, second)
		})
	})
}

func TestArtistUseCase_ListSimilar(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	seedArtist := verifiedArtist("seed-id", "Seed", "seed-mbid")

	t.Run("error - seed artist not found", func(t *testing.T) {
		t.Parallel()
		d := newArtistTestDeps(t)

		d.repo.EXPECT().Get(ctx, "missing-id").Return(nil, apperr.ErrNotFound).Once()

		result, err := d.uc.ListSimilar(ctx, "missing-id", int32(0))

		assert.ErrorIs(t, err, apperr.ErrNotFound)
		assert.Nil(t, result)
	})

	// @spec components/usecase/artist/list-similar "Known and new similar artists"
	t.Run("registers only the unregistered artist, unverified, and returns the verified one", func(t *testing.T) {
		t.Parallel()
		d := newArtistTestDeps(t)

		known := verifiedArtist("id-known", "Known", "sim-known")
		created := unverifiedArtist("id-new", "New", "sim-new")

		d.repo.EXPECT().Get(ctx, "seed-id").Return(seedArtist, nil).Once()
		d.searcher.EXPECT().ListSimilar(ctx, seedArtist, int32(50)).Return([]*entity.Artist{
			entity.NewArtist("New", "sim-new"), entity.NewArtist("Known", "sim-known"),
		}, nil).Once()
		d.repo.EXPECT().ListByMBIDs(mock.Anything, []string{"sim-new", "sim-known"}).Return([]*entity.Artist{known}, nil).Once()
		d.repo.EXPECT().Create(mock.Anything, withStatus("sim-new", "sim-new", entity.MBIDStatusUnverified)).Return([]*entity.Artist{created}, nil).Once()

		result, err := d.uc.ListSimilar(ctx, "seed-id", int32(0))

		require.NoError(t, err)
		assert.Equal(t, []*entity.Artist{known}, result)
	})

	// @spec components/usecase/artist/list-similar "Result without MBID"
	t.Run("neither registers nor returns a result without an MBID", func(t *testing.T) {
		t.Parallel()
		d := newArtistTestDeps(t)

		known := verifiedArtist("id-sim-x", "With MBID", "sim-x")
		d.repo.EXPECT().Get(ctx, "seed-id").Return(seedArtist, nil).Once()
		d.searcher.EXPECT().ListSimilar(ctx, seedArtist, int32(50)).Return([]*entity.Artist{
			entity.NewArtist("With MBID", "sim-x"), entity.NewArtist("No MBID", ""),
		}, nil).Once()
		// No Create expectation: registering the result without an MBID fails the mock.
		d.repo.EXPECT().ListByMBIDs(mock.Anything, []string{"sim-x"}).Return([]*entity.Artist{known}, nil).Once()
		d.repo.EXPECT().ListByKnownNames(anyCtx, []string{"No MBID"}).Return([]*entity.Artist{}, nil).Once()

		result, err := d.uc.ListSimilar(ctx, "seed-id", int32(0))

		require.NoError(t, err)
		assert.Equal(t, []*entity.Artist{known}, result)
	})

	t.Run("asks the catalog for 50 artists and cuts the result to the limit", func(t *testing.T) {
		t.Parallel()

		// @spec components/usecase/artist/list-similar "Limit given"
		// @spec components/usecase/artist/list-similar "Limit of zero"
		// @spec components/usecase/artist/list-similar "No similar artists"
		tests := []struct {
			name  string
			limit int32
			found int
			want  int
		}{
			{name: "limit given", limit: 10, found: 42, want: 10},  // Limit given
			{name: "limit of zero", limit: 0, found: 42, want: 42}, // Limit of zero
			{name: "limit above the result", limit: 50, found: 3, want: 3},
			{name: "no similar artists", limit: 10, found: 0, want: 0}, // No similar artists
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				d := newArtistTestDeps(t)

				fetched, registered, mbids := registeredArtists(t, tc.found)
				d.repo.EXPECT().Get(ctx, "seed-id").Return(seedArtist, nil).Once()
				d.searcher.EXPECT().ListSimilar(ctx, seedArtist, int32(50)).Return(fetched, nil).Once()
				if tc.found > 0 {
					d.repo.EXPECT().ListByMBIDs(mock.Anything, mbids).Return(registered, nil).Once()
				}

				result, err := d.uc.ListSimilar(ctx, "seed-id", tc.limit)

				assert.NoError(t, err)
				assert.Equal(t, registered[:tc.want], result)
			})
		}
	})

	t.Run("reuses the cached lookup for the same artist", func(t *testing.T) {
		t.Parallel()

		// @spec components/usecase/artist/list-similar "Repeated call"
		// @spec components/usecase/artist/list-similar "Repeated call with a different limit"
		type call struct {
			after time.Duration
			limit int32
		}
		tests := []struct {
			name   string
			first  call
			second call
		}{
			{name: "same limit 10 minutes apart", first: call{limit: 10}, second: call{after: 10 * time.Minute, limit: 10}}, // Repeated call
			{name: "smaller limit 1 hour later", first: call{limit: 30}, second: call{after: time.Hour, limit: 10}},         // Repeated call with a different limit
			{name: "larger limit 1 hour later", first: call{limit: 10}, second: call{after: time.Hour, limit: 30}},
			{name: "same limit just under 6 hours later", first: call{limit: 10}, second: call{after: 6*time.Hour - time.Minute, limit: 10}},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				synctest.Test(t, func(t *testing.T) {
					d := newArtistTestDeps(t)

					fetched, registered, mbids := registeredArtists(t, 42)
					// Once(): the seed and the catalog are consulted for the first call
					// only; the second call reads the artists' current status.
					d.repo.EXPECT().Get(ctx, "seed-id").Return(seedArtist, nil).Once()
					d.searcher.EXPECT().ListSimilar(ctx, seedArtist, int32(50)).Return(fetched, nil).Once()
					d.repo.EXPECT().ListByMBIDs(mock.Anything, mbids).Return(registered, nil).Twice()

					first, err := d.uc.ListSimilar(ctx, "seed-id", tc.first.limit)
					require.NoError(t, err)
					assert.Equal(t, registered[:tc.first.limit], first)

					time.Sleep(tc.second.after)

					second, err := d.uc.ListSimilar(ctx, "seed-id", tc.second.limit)
					assert.NoError(t, err)
					assert.Equal(t, registered[:tc.second.limit], second)
				})
			})
		}
	})

	t.Run("consults the catalog again after the cache TTL", func(t *testing.T) {
		t.Parallel()

		// @spec components/usecase/artist/list-similar "Result older than 6 hours"
		synctest.Test(t, func(t *testing.T) {
			d := newArtistTestDeps(t)

			fetched, registered, mbids := registeredArtists(t, 5)
			d.repo.EXPECT().Get(ctx, "seed-id").Return(seedArtist, nil).Twice()
			d.searcher.EXPECT().ListSimilar(ctx, seedArtist, int32(50)).Return(fetched, nil).Twice()
			d.repo.EXPECT().ListByMBIDs(mock.Anything, mbids).Return(registered, nil).Twice()

			_, err := d.uc.ListSimilar(ctx, "seed-id", int32(10))
			require.NoError(t, err)

			time.Sleep(usecase.ArtistCacheTTL + time.Minute)

			result, err := d.uc.ListSimilar(ctx, "seed-id", int32(10))
			assert.NoError(t, err)
			assert.Equal(t, registered, result)
		})
	})

	// @spec components/usecase/artist/list-similar "Result under a replaced MBID"
	t.Run("returns the artist registered under the listening MBID", func(t *testing.T) {
		t.Parallel()
		d := newArtistTestDeps(t)

		corrected := correctedArtist()
		d.repo.EXPECT().Get(ctx, "seed-id").Return(seedArtist, nil).Once()
		d.searcher.EXPECT().ListSimilar(ctx, seedArtist, int32(50)).Return([]*entity.Artist{entity.NewArtist("Penthouse", oldMBID)}, nil).Once()
		d.repo.EXPECT().ListByMBIDs(mock.Anything, []string{oldMBID}).Return([]*entity.Artist{corrected}, nil).Once()
		// No Create expectation: registering fails the mock.

		result, err := d.uc.ListSimilar(ctx, "seed-id", 0)

		require.NoError(t, err)
		assert.Equal(t, []*entity.Artist{corrected}, result)
	})

	// @spec components/usecase/artist/list-similar "Seed whose MBID was corrected"
	t.Run("looks up the similar artists of a corrected seed by its listening MBID", func(t *testing.T) {
		t.Parallel()
		d := newArtistTestDeps(t)

		seed := correctedArtist()
		d.repo.EXPECT().Get(ctx, seed.ID).Return(seed, nil).Once()
		d.searcher.EXPECT().ListSimilar(ctx, mock.MatchedBy(func(a *entity.Artist) bool {
			return a.ListeningMBID == oldMBID && a.MBID == newMBID
		}), int32(50)).Return([]*entity.Artist{}, nil).Once()

		result, err := d.uc.ListSimilar(ctx, seed.ID, 0)

		require.NoError(t, err)
		assert.Empty(t, result)
	})

	// @spec components/usecase/artist/list-similar "Ended artist within the limit"
	t.Run("leaves out an ended artist before the limit cut", func(t *testing.T) {
		t.Parallel()
		d := newArtistTestDeps(t)

		fetched, registered, mbids := registeredArtists(t, 12)
		registered[2].ActivityEnded = true
		d.repo.EXPECT().Get(ctx, "seed-id").Return(seedArtist, nil).Once()
		d.searcher.EXPECT().ListSimilar(ctx, seedArtist, int32(50)).Return(fetched, nil).Once()
		d.repo.EXPECT().ListByMBIDs(mock.Anything, mbids).Return(registered, nil).Once()

		result, err := d.uc.ListSimilar(ctx, "seed-id", int32(10))

		require.NoError(t, err)
		want := append([]*entity.Artist{registered[0], registered[1]}, registered[3:11]...)
		assert.Equal(t, want, result)
	})

	// @spec components/usecase/artist/list-similar "Artist marked ended after the result was reused"
	t.Run("leaves out an artist marked ended since the cached result", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			d := newArtistTestDeps(t)

			fetched, registered, mbids := registeredArtists(t, 3)
			// Once(): the seed and the catalog are consulted for the first call only.
			d.repo.EXPECT().Get(ctx, "seed-id").Return(seedArtist, nil).Once()
			d.searcher.EXPECT().ListSimilar(ctx, seedArtist, int32(50)).Return(fetched, nil).Once()
			d.repo.EXPECT().ListByMBIDs(mock.Anything, mbids).Return(registered, nil).Once()

			first, err := d.uc.ListSimilar(ctx, "seed-id", int32(0))
			require.NoError(t, err)
			require.Len(t, first, 3)

			time.Sleep(time.Hour)
			ended := verifiedArtist("id-01", "Artist 01", "mbid-01")
			ended.ActivityEnded = true
			d.repo.EXPECT().ListByMBIDs(mock.Anything, mbids).Return([]*entity.Artist{registered[0], ended, registered[2]}, nil).Once()

			second, err := d.uc.ListSimilar(ctx, "seed-id", int32(0))

			require.NoError(t, err)
			assert.Equal(t, []*entity.Artist{registered[0], registered[2]}, second)
		})
	})

	// @spec components/usecase/artist/list-similar "Similar artist verified after the result"
	t.Run("returns a similar artist verified since the cached result", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			d := newArtistTestDeps(t)

			created := unverifiedArtist("id-new", "New", "sim-new")
			d.repo.EXPECT().Get(ctx, "seed-id").Return(seedArtist, nil).Once()
			d.searcher.EXPECT().ListSimilar(ctx, seedArtist, int32(50)).Return([]*entity.Artist{entity.NewArtist("New", "sim-new")}, nil).Once()
			d.repo.EXPECT().ListByMBIDs(mock.Anything, []string{"sim-new"}).Return([]*entity.Artist{}, nil).Once()
			d.repo.EXPECT().Create(mock.Anything, mock.AnythingOfType("*entity.Artist")).Return([]*entity.Artist{created}, nil).Once()

			first, err := d.uc.ListSimilar(ctx, "seed-id", int32(0))
			require.NoError(t, err)
			require.Empty(t, first)

			time.Sleep(time.Hour)
			verified := verifiedArtist("id-new", "New", "sim-new")
			d.repo.EXPECT().ListByMBIDs(mock.Anything, []string{"sim-new"}).Return([]*entity.Artist{verified}, nil).Once()

			second, err := d.uc.ListSimilar(ctx, "seed-id", int32(0))

			require.NoError(t, err)
			assert.Equal(t, []*entity.Artist{verified}, second)
		})
	})

	// @spec components/usecase/artist/list-similar "Seed artist whose activity has ended"
	t.Run("returns the similar artists of an ended seed", func(t *testing.T) {
		t.Parallel()
		d := newArtistTestDeps(t)

		endedSeed := verifiedArtist("ended-seed-id", "SMAP", "ended-seed-mbid")
		endedSeed.ActivityEnded = true
		fetched, registered, mbids := registeredArtists(t, 3)
		d.repo.EXPECT().Get(ctx, "ended-seed-id").Return(endedSeed, nil).Once()
		d.searcher.EXPECT().ListSimilar(ctx, endedSeed, int32(50)).Return(fetched, nil).Once()
		d.repo.EXPECT().ListByMBIDs(mock.Anything, mbids).Return(registered, nil).Once()

		result, err := d.uc.ListSimilar(ctx, "ended-seed-id", int32(0))

		require.NoError(t, err)
		assert.Equal(t, registered, result)
	})
}

func TestArtistUseCase_ListSimilar_NameMatch(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	// knownAs returns a registered artist offered to fans that is known by names.
	knownAs := func(id, mbid string, names ...string) *entity.Artist {
		a := verifiedArtist(id, names[0], mbid)
		a.KnownNames = entity.NewKnownNames(names...)
		return a
	}
	// run expects a lookup whose similar artists are results, those with an
	// MBID being the registered ones, and whose names without an MBID find
	// named, and returns what ListSimilar answers for the seed.
	run := func(t *testing.T, d *artistTestDeps, seed *entity.Artist, results, registered []*entity.Artist, names []string, named []*entity.Artist) []*entity.Artist {
		t.Helper()
		d.repo.EXPECT().Get(ctx, seed.ID).Return(seed, nil).Once()
		d.searcher.EXPECT().ListSimilar(ctx, seed, int32(50)).Return(results, nil).Once()
		if len(registered) > 0 {
			d.repo.EXPECT().ListByMBIDs(anyCtx, mock.Anything).Return(registered, nil).Once()
		}
		// No Create expectation: registering anything here fails the mock.
		d.repo.EXPECT().ListByKnownNames(anyCtx, names).Return(named, nil).Once()

		got, err := d.uc.ListSimilar(ctx, seed.ID, 0)

		require.NoError(t, err)
		return got
	}
	seed := knownAs("seed-id", "seed-mbid", "Seed", "シード")

	// @spec components/usecase/artist/list-similar "Registered artist listed by a romanized name"
	// @spec stories/discover-artists "Romanized name"
	t.Run("returns the artist known by the alias at the result's position", func(t *testing.T) {
		t.Parallel()
		d := newArtistTestDeps(t)
		first := knownAs("id-first", "mbid-first", "First")
		last := knownAs("id-last", "mbid-last", "Last")
		yorushika := knownAs("id-yorushika", "mbid-yorushika", "ヨルシカ", "Yorushika")

		got := run(t, d, seed, []*entity.Artist{
			entity.NewArtist("First", "mbid-first"), entity.NewArtist("Yorushika", ""), entity.NewArtist("Last", "mbid-last"),
		}, []*entity.Artist{first, last}, []string{"Yorushika"}, []*entity.Artist{yorushika})

		assert.Equal(t, []*entity.Artist{first, yorushika, last}, got)
		logs := d.logs.String()
		assert.Contains(t, logs, "similar artists without MBID")
		assert.Contains(t, logs, `"seed_artist_id":"seed-id"`)
		assert.Contains(t, logs, `"matched":1,"matched_several":0,"unmatched":0`)
	})

	// @spec components/usecase/artist/list-similar "Two registered artists have the name"
	t.Run("returns neither of two artists sharing the name", func(t *testing.T) {
		t.Parallel()
		d := newArtistTestDeps(t)
		offeredOne := knownAs("id-1", "mbid-1", "tomoo")
		unoffered := knownAs("id-2", "mbid-2", "TOMOO")
		unoffered.MBIDStatus = entity.MBIDStatusUnverified

		got := run(t, d, seed, []*entity.Artist{entity.NewArtist("tomoo", ""), entity.NewArtist("Nobody", "")}, nil,
			[]string{"tomoo", "Nobody"}, []*entity.Artist{offeredOne, unoffered})

		assert.Empty(t, got)
		logs := d.logs.String()
		assert.Contains(t, logs, `"matched":0,"matched_several":1,"unmatched":1`)
		assert.Contains(t, logs, `"unmatched_names":["Nobody"]`)
	})

	// @spec components/usecase/artist/list-similar "Matched artist not offered"
	t.Run("leaves out a matched artist not offered to fans", func(t *testing.T) {
		t.Parallel()
		d := newArtistTestDeps(t)
		unoffered := knownAs("id-1", "mbid-1", "tomoo")
		unoffered.ActivityEnded = true

		got := run(t, d, seed, []*entity.Artist{entity.NewArtist("tomoo", "")}, nil, []string{"tomoo"}, []*entity.Artist{unoffered})

		assert.Empty(t, got)
	})

	// @spec components/usecase/artist/list-similar "Same artist listed twice"
	t.Run("lists an artist found by MBID and by name once, at its first position", func(t *testing.T) {
		t.Parallel()
		d := newArtistTestDeps(t)
		other := knownAs("id-other", "mbid-other", "Other")
		yorushika := knownAs("id-yorushika", "mbid-yorushika", "ヨルシカ", "Yorushika")

		got := run(t, d, seed, []*entity.Artist{
			entity.NewArtist("Yorushika", ""), entity.NewArtist("Other", "mbid-other"), entity.NewArtist("ヨルシカ", "mbid-yorushika"),
		}, []*entity.Artist{other, yorushika}, []string{"Yorushika"}, []*entity.Artist{yorushika})

		assert.Equal(t, []*entity.Artist{yorushika, other}, got)
	})

	// @spec components/usecase/artist/list-similar "Match is the seed"
	t.Run("does not return the seed", func(t *testing.T) {
		t.Parallel()
		d := newArtistTestDeps(t)

		got := run(t, d, seed, []*entity.Artist{entity.NewArtist("シード", "")}, nil, []string{"シード"}, []*entity.Artist{seed})

		assert.Empty(t, got)
	})

	// @spec components/usecase/artist/list-similar "Name lookup fails"
	t.Run("fails when the name lookup fails", func(t *testing.T) {
		t.Parallel()
		d := newArtistTestDeps(t)
		d.repo.EXPECT().Get(ctx, "seed-id").Return(seed, nil).Once()
		d.searcher.EXPECT().ListSimilar(ctx, seed, int32(50)).Return([]*entity.Artist{entity.NewArtist("tomoo", "")}, nil).Once()
		d.repo.EXPECT().ListByKnownNames(anyCtx, []string{"tomoo"}).Return(nil, apperr.New(codes.Unavailable, "db down")).Once()

		_, err := d.uc.ListSimilar(ctx, "seed-id", 0)

		assert.ErrorIs(t, err, apperr.ErrUnavailable)
	})
}

func TestArtistUseCase_Search(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	t.Run("filters empty MBID and deduplicates by MBID", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/artist/search "Match without MBID"
		d := newArtistTestDeps(t)

		fetched := []*entity.Artist{
			entity.NewArtist("ヨルシカ", "abc"),
			entity.NewArtist("ヨルシカ Live", "abc"),
			entity.NewArtist("User Page", ""),
			entity.NewArtist("suis from ヨルシカ", "def"),
		}
		persisted := []*entity.Artist{
			unverifiedArtist("id-1", "ヨルシカ", "abc"),
			unverifiedArtist("id-2", "suis from ヨルシカ", "def"),
		}

		d.searcher.EXPECT().Search(ctx, "ヨルシカ").Return(fetched, nil).Once()
		d.repo.EXPECT().ListByMBIDs(mock.Anything, []string{"abc", "def"}).Return([]*entity.Artist{}, nil).Once()
		// Only the two matches with an MBID are registered, once each.
		d.repo.EXPECT().Create(mock.Anything,
			withStatus("abc", "abc", entity.MBIDStatusUnverified),
			withStatus("def", "def", entity.MBIDStatusUnverified),
		).Return(persisted, nil).Once()

		result, err := d.uc.Search(ctx, "ヨルシカ")

		require.NoError(t, err)
		assert.Empty(t, result, "new matches are Unverified until their announcement verifies them")
	})

	t.Run("returns NotFound when all entries filtered out", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/artist/search "No match with an MBID"
		d := newArtistTestDeps(t)

		d.searcher.EXPECT().Search(ctx, "test").Return([]*entity.Artist{
			entity.NewArtist("No MBID 1", ""),
			entity.NewArtist("No MBID 2", ""),
		}, nil).Once()

		result, err := d.uc.Search(ctx, "test")

		assert.ErrorIs(t, err, apperr.ErrNotFound)
		assert.Nil(t, result)
	})

	t.Run("propagates the searcher's error code unchanged", func(t *testing.T) {
		t.Parallel()

		// Table-driven per apperr code: Search must forward the port error as-is
		// (matching ListSimilar/ListTop), never collapsing it into Internal.
		//
		// @spec components/usecase/artist/search "Catalog unavailable"
		// @spec components/usecase/artist/search "Catalog rate-limited"
		// @spec components/usecase/artist/search "Catalog request timed out"
		tests := []struct {
			name    string
			wantErr error
		}{
			{name: "NotFound", wantErr: apperr.ErrNotFound},
			{name: "Unavailable", wantErr: apperr.ErrUnavailable},             // Catalog unavailable
			{name: "ResourceExhausted", wantErr: apperr.ErrResourceExhausted}, // Catalog rate-limited
			{name: "DeadlineExceeded", wantErr: apperr.ErrDeadlineExceeded},   // Catalog request timed out
			{name: "Internal", wantErr: apperr.ErrInternal},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				d := newArtistTestDeps(t)

				d.searcher.EXPECT().Search(ctx, "query").Return(nil, tc.wantErr).Once()

				result, err := d.uc.Search(ctx, "query")

				assert.ErrorIs(t, err, tc.wantErr)
				assert.Nil(t, result)
			})
		}
	})

	t.Run("returns cached results on a repeated query", func(t *testing.T) {
		t.Parallel()

		// @spec components/usecase/artist/search "Repeated query"
		synctest.Test(t, func(t *testing.T) {
			d := newArtistTestDeps(t)

			registered := verifiedArtist("id-1", "Artist", "mbid-1")
			d.searcher.EXPECT().Search(ctx, "cached").Return([]*entity.Artist{entity.NewArtist("Artist", "mbid-1")}, nil).Once()
			d.repo.EXPECT().ListByMBIDs(mock.Anything, []string{"mbid-1"}).Return([]*entity.Artist{}, nil).Once()
			d.repo.EXPECT().Create(mock.Anything, mock.AnythingOfType("*entity.Artist")).Return([]*entity.Artist{registered}, nil).Once()

			first, err := d.uc.Search(ctx, "cached")
			require.NoError(t, err)

			time.Sleep(10 * time.Minute)

			// Cache hit: no catalog lookup and nothing registered again; the
			// artists' current status is read with one ListByMBIDs.
			d.repo.EXPECT().ListByMBIDs(mock.Anything, []string{"mbid-1"}).Return([]*entity.Artist{registered}, nil).Once()
			second, err := d.uc.Search(ctx, "cached")
			assert.NoError(t, err)
			assert.Equal(t, first, second)
		})
	})

	t.Run("consults the catalog again after the cache TTL", func(t *testing.T) {
		t.Parallel()

		// @spec components/usecase/artist/search "Result older than 6 hours"
		synctest.Test(t, func(t *testing.T) {
			d := newArtistTestDeps(t)

			fetched, registered, mbids := registeredArtists(t, 1)
			d.searcher.EXPECT().Search(ctx, "expired").Return(fetched, nil).Twice()
			d.repo.EXPECT().ListByMBIDs(mock.Anything, mbids).Return(registered, nil).Twice()

			_, err := d.uc.Search(ctx, "expired")
			require.NoError(t, err)

			time.Sleep(usecase.ArtistCacheTTL + time.Minute)

			result, err := d.uc.Search(ctx, "expired")
			assert.NoError(t, err)
			assert.Equal(t, registered, result)
		})
	})

	// @spec components/usecase/artist/search "Artist marked ended after the result was reused"
	t.Run("leaves out an artist marked ended since the cached result", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			d := newArtistTestDeps(t)

			fetched, registered, mbids := registeredArtists(t, 2)
			// Once(): the catalog is consulted for the first call only.
			d.searcher.EXPECT().Search(ctx, "reused").Return(fetched, nil).Once()
			d.repo.EXPECT().ListByMBIDs(mock.Anything, mbids).Return(registered, nil).Once()

			first, err := d.uc.Search(ctx, "reused")
			require.NoError(t, err)
			require.Len(t, first, 2)

			time.Sleep(time.Hour)
			ended := verifiedArtist("id-01", "Artist 01", "mbid-01")
			ended.ActivityEnded = true
			d.repo.EXPECT().ListByMBIDs(mock.Anything, mbids).Return([]*entity.Artist{registered[0], ended}, nil).Once()

			second, err := d.uc.Search(ctx, "reused")

			require.NoError(t, err)
			assert.Equal(t, []*entity.Artist{registered[0]}, second)
		})
	})

	t.Run("registers only the new match, unverified, and returns the known one", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/artist/search "Known and new matches"
		// @spec components/usecase/artist/search "New match registered under its name"
		d := newArtistTestDeps(t)

		existing := verifiedArtist("db-id-1", "Existing", "mbid-existing")
		created := unverifiedArtist("db-id-2", "New", "mbid-new")

		d.searcher.EXPECT().Search(ctx, "mixed").Return([]*entity.Artist{
			entity.NewArtist("Existing", "mbid-existing"), entity.NewArtist("New", "mbid-new"),
		}, nil).Once()
		d.repo.EXPECT().ListByMBIDs(mock.Anything, []string{"mbid-existing", "mbid-new"}).Return([]*entity.Artist{existing}, nil).Once()
		// No catalog read: the namesake and identity mocks fail the test on one.
		d.repo.EXPECT().Create(mock.Anything, mock.MatchedBy(func(a *entity.Artist) bool {
			return a.ListeningMBID == "mbid-new" && a.MBID == "mbid-new" && a.MBIDStatus == entity.MBIDStatusUnverified && a.KnownNames == nil
		})).Return([]*entity.Artist{created}, nil).Once()

		result, err := d.uc.Search(ctx, "mixed")

		require.NoError(t, err)
		assert.Equal(t, []*entity.Artist{existing}, result)
	})

	t.Run("skips Create when all exist", func(t *testing.T) {
		t.Parallel()
		d := newArtistTestDeps(t)

		fetched, registered, mbids := registeredArtists(t, 2)
		d.searcher.EXPECT().Search(ctx, "all-exist").Return(fetched, nil).Once()
		d.repo.EXPECT().ListByMBIDs(mock.Anything, mbids).Return(registered, nil).Once()
		// No Create call expected

		result, err := d.uc.Search(ctx, "all-exist")

		require.NoError(t, err)
		assert.Equal(t, registered, result)
	})

	// @spec components/usecase/artist/search "Match under a replaced MBID"
	t.Run("returns the artist registered under the listening MBID", func(t *testing.T) {
		t.Parallel()
		d := newArtistTestDeps(t)
		corrected := correctedArtist()
		d.searcher.EXPECT().Search(ctx, "Penthouse").Return([]*entity.Artist{entity.NewArtist("Penthouse", oldMBID)}, nil).Once()
		d.repo.EXPECT().ListByMBIDs(mock.Anything, []string{oldMBID}).Return([]*entity.Artist{corrected}, nil).Once()
		// Create is not expected: the mock fails the test when it is called.

		got, err := d.uc.Search(ctx, "Penthouse")

		require.NoError(t, err)
		assert.Equal(t, []*entity.Artist{corrected}, got)
	})

	// @spec components/usecase/artist/search "Two matches of one artist"
	t.Run("lists an artist found under both of its MBIDs once", func(t *testing.T) {
		t.Parallel()
		d := newArtistTestDeps(t)
		corrected := correctedArtist()
		d.searcher.EXPECT().Search(ctx, "Penthouse").Return([]*entity.Artist{
			entity.NewArtist("Penthouse", oldMBID),
			entity.NewArtist("Penthouse", newMBID),
		}, nil).Once()
		d.repo.EXPECT().ListByMBIDs(mock.Anything, []string{oldMBID, newMBID}).Return([]*entity.Artist{corrected}, nil).Once()

		got, err := d.uc.Search(ctx, "Penthouse")

		require.NoError(t, err)
		assert.Equal(t, []*entity.Artist{corrected}, got)
	})

	// @spec components/usecase/artist/search "Registered artist not yet verified"
	t.Run("neither returns nor verifies a registered unverified artist", func(t *testing.T) {
		t.Parallel()
		d := newArtistTestDeps(t)
		registered := unverifiedArtist("id-1", "Band", "mbid-1")
		d.searcher.EXPECT().Search(ctx, "Band").Return([]*entity.Artist{entity.NewArtist("Band", "mbid-1")}, nil).Once()
		d.repo.EXPECT().ListByMBIDs(mock.Anything, []string{"mbid-1"}).Return([]*entity.Artist{registered}, nil).Once()
		// No namesake lookup is expected: verifying fails the mock.

		got, err := d.uc.Search(ctx, "Band")

		require.NoError(t, err)
		assert.Empty(t, got)
	})

	// @spec components/usecase/artist/search "Ended artist"
	t.Run("does not return a verified artist whose activity has ended", func(t *testing.T) {
		t.Parallel()
		d := newArtistTestDeps(t)
		ended := verifiedArtist("id-1", "SMAP", "mbid-1")
		ended.ActivityEnded = true
		d.searcher.EXPECT().Search(ctx, "SMAP").Return([]*entity.Artist{entity.NewArtist("SMAP", "mbid-1")}, nil).Once()
		d.repo.EXPECT().ListByMBIDs(mock.Anything, []string{"mbid-1"}).Return([]*entity.Artist{ended}, nil).Once()

		got, err := d.uc.Search(ctx, "SMAP")

		require.NoError(t, err)
		assert.Empty(t, got)
	})
}

func TestArtistUseCase_Get(t *testing.T) {
	t.Parallel()

	t.Run("return a registered artist", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/artist/get "Known artist"
		want := &entity.Artist{ID: "artist-1", Name: "E2E Test Artist"}
		d := newArtistTestDeps(t)
		d.repo.EXPECT().Get(mock.Anything, "artist-1").Return(want, nil).Once()

		got, err := d.uc.Get(context.Background(), "artist-1")

		require.NoError(t, err)
		assert.Equal(t, want, got)
	})

	t.Run("return NotFound unchanged for an unknown id", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/artist/get "Unknown artist"
		d := newArtistTestDeps(t)
		d.repo.EXPECT().Get(mock.Anything, "artist-unknown").Return(nil, apperr.New(codes.NotFound, "artist not found")).Once()

		_, err := d.uc.Get(context.Background(), "artist-unknown")

		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})
}
