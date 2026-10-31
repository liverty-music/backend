package usecase_test

import (
	"context"
	"fmt"
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
	repo      *mocks.MockArtistRepository
	searcher  *mocks.MockArtistSearcher
	idManager *mocks.MockArtistIdentityManager
	uc        usecase.ArtistUseCase
}

func newArtistTestDeps(t *testing.T) *artistTestDeps {
	t.Helper()
	d := &artistTestDeps{
		repo:      mocks.NewMockArtistRepository(t),
		searcher:  mocks.NewMockArtistSearcher(t),
		idManager: mocks.NewMockArtistIdentityManager(t),
	}
	// The cache uses the production TTL so the expiry scenarios exercise the
	// real value; tests that cross it run inside a synctest bubble.
	c := cache.NewMemoryCache(usecase.ArtistCacheTTL)
	t.Cleanup(func() { assert.NoError(t, c.Close()) })
	d.uc = usecase.NewArtistUseCase(d.repo, mocks.NewMockOrganizerRepository(t), d.searcher, d.idManager, c, newTestLogger(t))
	return d
}

// registeredArtists returns n catalog results with an MBID and the same
// artists as already registered, in the same order.
func registeredArtists(t *testing.T, n int) (fetched, registered []*entity.Artist, mbids []string) {
	t.Helper()
	registered = make([]*entity.Artist, 0, n)
	for i := range n {
		mbid := fmt.Sprintf("mbid-%02d", i)
		name := fmt.Sprintf("Artist %02d", i)
		fetched = append(fetched, &entity.Artist{Name: name, MBID: mbid})
		registered = append(registered, &entity.Artist{ID: fmt.Sprintf("id-%02d", i), Name: name, MBID: mbid})
		mbids = append(mbids, mbid)
	}
	return fetched, registered, mbids
}

func TestArtistUseCase_CreateArtist(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		t.Parallel()
		d := newArtistTestDeps(t)

		artist := &entity.Artist{
			ID:   "artist-1",
			Name: "The Beatles",
			MBID: "5b11f448-2d57-455b-8292-629df8357062",
		}

		d.idManager.EXPECT().GetArtist(ctx, artist.MBID).Return(&entity.Artist{
			MBID: artist.MBID,
			Name: artist.Name,
		}, nil).Once()
		d.repo.EXPECT().Create(ctx, artist).Return([]*entity.Artist{artist}, nil).Once()

		result, err := d.uc.Create(ctx, artist)

		assert.NoError(t, err)
		assert.Equal(t, artist, result)
	})

	t.Run("no announcement", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/artist/create "No announcement"
		// Artist.Create announces the registered artist; the strict publisher
		// mock fails the test on any announcement by the usecase.
		d := newArtistTestDeps(t)
		c := cache.NewMemoryCache(usecase.ArtistCacheTTL)
		t.Cleanup(func() { assert.NoError(t, c.Close()) })
		uc := usecase.NewArtistUseCase(d.repo, mocks.NewMockOrganizerRepository(t), d.searcher, d.idManager, c, newTestLogger(t))
		artist := &entity.Artist{ID: "artist-1", Name: "New Band", MBID: "11111111-1111-1111-1111-111111111111"}
		d.idManager.EXPECT().GetArtist(ctx, artist.MBID).Return(&entity.Artist{MBID: artist.MBID, Name: artist.Name}, nil).Once()
		d.repo.EXPECT().Create(ctx, artist).Return([]*entity.Artist{artist}, nil).Once()

		_, err := uc.Create(ctx, artist)

		assert.NoError(t, err)
	})

	t.Run("registration rejected", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/artist/create "Registration rejected"
		d := newArtistTestDeps(t)
		artist := &entity.Artist{Name: "No MBID"}
		d.repo.EXPECT().Create(ctx, artist).Return(nil, apperr.ErrInvalidArgument).Once()

		_, err := d.uc.Create(ctx, artist)

		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
	})

	t.Run("nothing returned", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/artist/create "Nothing returned"
		d := newArtistTestDeps(t)
		artist := &entity.Artist{Name: "Ghost"}
		d.repo.EXPECT().Create(ctx, artist).Return([]*entity.Artist{}, nil).Once()

		_, err := d.uc.Create(ctx, artist)

		assert.ErrorIs(t, err, apperr.ErrInternal)
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

	t.Run("returns persisted artists with valid IDs", func(t *testing.T) {
		t.Parallel()
		d := newArtistTestDeps(t)

		fetched := []*entity.Artist{
			{Name: "Artist A", MBID: "mbid-a"},
			{Name: "Artist B", MBID: "mbid-b"},
		}
		persisted := []*entity.Artist{
			{ID: "id-a", Name: "Artist A", MBID: "mbid-a"},
			{ID: "id-b", Name: "Artist B", MBID: "mbid-b"},
		}

		d.searcher.EXPECT().ListTop(ctx, "JP", "", int32(0)).Return(fetched, nil).Once()
		d.repo.EXPECT().ListByMBIDs(mock.Anything, []string{"mbid-a", "mbid-b"}).Return([]*entity.Artist{}, nil).Once()
		d.repo.EXPECT().Create(mock.Anything, mock.AnythingOfType("*entity.Artist"), mock.AnythingOfType("*entity.Artist")).Return(persisted, nil).Once()

		result, err := d.uc.ListTop(ctx, "JP", "", int32(0))

		assert.NoError(t, err)
		assert.Len(t, result, 2)
		assert.Equal(t, "id-a", result[0].ID)
		assert.Equal(t, "id-b", result[1].ID)
	})

	t.Run("filters out empty MBID entries", func(t *testing.T) {
		t.Parallel()
		d := newArtistTestDeps(t)

		fetched := []*entity.Artist{
			{Name: "With MBID", MBID: "mbid-x"},
			{Name: "No MBID", MBID: ""},
		}
		persisted := []*entity.Artist{
			{ID: "id-x", Name: "With MBID", MBID: "mbid-x"},
		}

		d.searcher.EXPECT().ListTop(ctx, "JP", "", int32(0)).Return(fetched, nil).Once()
		d.repo.EXPECT().ListByMBIDs(mock.Anything, []string{"mbid-x"}).Return([]*entity.Artist{}, nil).Once()
		d.repo.EXPECT().Create(mock.Anything, mock.AnythingOfType("*entity.Artist")).Return(persisted, nil).Once()

		result, err := d.uc.ListTop(ctx, "JP", "", int32(0))

		assert.NoError(t, err)
		assert.Len(t, result, 1)
		assert.Equal(t, "mbid-x", result[0].MBID)
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

			persisted := []*entity.Artist{
				{ID: "id-a", Name: "Artist A", MBID: "mbid-a"},
			}

			d.searcher.EXPECT().ListTop(ctx, "US", "", int32(0)).Return([]*entity.Artist{{Name: "Artist A", MBID: "mbid-a"}}, nil).Once()
			d.repo.EXPECT().ListByMBIDs(mock.Anything, []string{"mbid-a"}).Return([]*entity.Artist{}, nil).Once()
			d.repo.EXPECT().Create(mock.Anything, mock.AnythingOfType("*entity.Artist")).Return(persisted, nil).Once()

			first, err := d.uc.ListTop(ctx, "US", "", int32(0))
			require.NoError(t, err)

			time.Sleep(10 * time.Minute)

			// Cache hit: the Once() expectations above fail on a second lookup.
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
}

func TestArtistUseCase_ListSimilar(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	seedArtist := &entity.Artist{ID: "seed-id", Name: "Seed", MBID: "seed-mbid"}

	t.Run("error - seed artist not found", func(t *testing.T) {
		t.Parallel()
		d := newArtistTestDeps(t)

		d.repo.EXPECT().Get(ctx, "missing-id").Return(nil, apperr.ErrNotFound).Once()

		result, err := d.uc.ListSimilar(ctx, "missing-id", int32(0))

		assert.ErrorIs(t, err, apperr.ErrNotFound)
		assert.Nil(t, result)
	})

	t.Run("registers only the unregistered artist and keeps the catalog order", func(t *testing.T) {
		t.Parallel()
		d := newArtistTestDeps(t)

		// @spec components/usecase/artist/list-similar "Known and new similar artists"
		fetched := []*entity.Artist{
			{Name: "New", MBID: "sim-new"},
			{Name: "Known", MBID: "sim-known"},
		}
		known := &entity.Artist{ID: "id-known", Name: "Known", MBID: "sim-known"}
		created := &entity.Artist{ID: "id-new", Name: "New", MBID: "sim-new"}

		d.repo.EXPECT().Get(ctx, "seed-id").Return(seedArtist, nil).Once()
		d.searcher.EXPECT().ListSimilar(ctx, seedArtist, int32(50)).Return(fetched, nil).Once()
		d.repo.EXPECT().ListByMBIDs(mock.Anything, []string{"sim-new", "sim-known"}).Return([]*entity.Artist{known}, nil).Once()
		d.repo.EXPECT().Create(mock.Anything, mock.MatchedBy(func(a *entity.Artist) bool {
			return a.MBID == "sim-new"
		})).Return([]*entity.Artist{created}, nil).Once()

		result, err := d.uc.ListSimilar(ctx, "seed-id", int32(0))

		assert.NoError(t, err)
		assert.Equal(t, []*entity.Artist{created, known}, result)
	})

	t.Run("drops results without an MBID", func(t *testing.T) {
		t.Parallel()
		d := newArtistTestDeps(t)

		// @spec components/usecase/artist/list-similar "Result without MBID"
		fetched := []*entity.Artist{
			{Name: "With MBID", MBID: "sim-x"},
			{Name: "No MBID", MBID: ""},
		}
		persisted := []*entity.Artist{
			{ID: "id-sim-x", Name: "With MBID", MBID: "sim-x"},
		}

		d.repo.EXPECT().Get(ctx, "seed-id").Return(seedArtist, nil).Once()
		d.searcher.EXPECT().ListSimilar(ctx, seedArtist, int32(50)).Return(fetched, nil).Once()
		d.repo.EXPECT().ListByMBIDs(mock.Anything, []string{"sim-x"}).Return([]*entity.Artist{}, nil).Once()
		d.repo.EXPECT().Create(mock.Anything, mock.MatchedBy(func(a *entity.Artist) bool {
			return a.MBID == "sim-x"
		})).Return(persisted, nil).Once()

		result, err := d.uc.ListSimilar(ctx, "seed-id", int32(0))

		assert.NoError(t, err)
		assert.Equal(t, persisted, result)
	})

	t.Run("asks the catalog for 50 artists and cuts the result to the limit", func(t *testing.T) {
		t.Parallel()

		// @spec components/usecase/artist/list-similar "Limit given"
		// @spec components/usecase/artist/list-similar "Limit of zero"
		// @spec components/usecase/artist/list-similar "No similar artists"
		type args struct {
			limit int32
		}
		tests := []struct {
			name  string
			args  args
			found int
			want  int
		}{
			{name: "limit given", args: args{limit: 10}, found: 42, want: 10},  // Limit given
			{name: "limit of zero", args: args{limit: 0}, found: 42, want: 42}, // Limit of zero
			{name: "limit above the result", args: args{limit: 50}, found: 3, want: 3},
			{name: "no similar artists", args: args{limit: 10}, found: 0, want: 0}, // No similar artists
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

				result, err := d.uc.ListSimilar(ctx, "seed-id", tc.args.limit)

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
					// Once(): the seed and the catalog are consulted for the first call only.
					d.repo.EXPECT().Get(ctx, "seed-id").Return(seedArtist, nil).Once()
					d.searcher.EXPECT().ListSimilar(ctx, seedArtist, int32(50)).Return(fetched, nil).Once()
					d.repo.EXPECT().ListByMBIDs(mock.Anything, mbids).Return(registered, nil).Once()

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
}

func TestArtistUseCase_Search(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	t.Run("filters empty MBID and deduplicates by MBID", func(t *testing.T) {
		t.Parallel()
		d := newArtistTestDeps(t)

		fetched := []*entity.Artist{
			{Name: "ヨルシカ", MBID: "abc"},
			{Name: "ヨルシカ Live", MBID: "abc"},
			{Name: "User Page", MBID: ""},
			{Name: "suis from ヨルシカ", MBID: "def"},
		}
		persisted := []*entity.Artist{
			{ID: "id-1", Name: "ヨルシカ", MBID: "abc"},
			{ID: "id-2", Name: "suis from ヨルシカ", MBID: "def"},
		}

		d.searcher.EXPECT().Search(ctx, "ヨルシカ").Return(fetched, nil).Once()
		d.repo.EXPECT().ListByMBIDs(mock.Anything, []string{"abc", "def"}).Return([]*entity.Artist{}, nil).Once()
		d.repo.EXPECT().Create(mock.Anything, mock.AnythingOfType("*entity.Artist"), mock.AnythingOfType("*entity.Artist")).Return(persisted, nil).Once()

		result, err := d.uc.Search(ctx, "ヨルシカ")

		assert.NoError(t, err)
		assert.Len(t, result, 2)
		assert.Equal(t, "id-1", result[0].ID)
		assert.Equal(t, "id-2", result[1].ID)
	})

	t.Run("returns NotFound when all entries filtered out", func(t *testing.T) {
		t.Parallel()
		d := newArtistTestDeps(t)

		d.searcher.EXPECT().Search(ctx, "test").Return([]*entity.Artist{
			{Name: "No MBID 1", MBID: ""},
			{Name: "No MBID 2", MBID: ""},
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

			persisted := []*entity.Artist{
				{ID: "id-1", Name: "Artist", MBID: "mbid-1"},
			}

			d.searcher.EXPECT().Search(ctx, "cached").Return([]*entity.Artist{{Name: "Artist", MBID: "mbid-1"}}, nil).Once()
			d.repo.EXPECT().ListByMBIDs(mock.Anything, []string{"mbid-1"}).Return([]*entity.Artist{}, nil).Once()
			d.repo.EXPECT().Create(mock.Anything, mock.AnythingOfType("*entity.Artist")).Return(persisted, nil).Once()

			first, err := d.uc.Search(ctx, "cached")
			require.NoError(t, err)

			time.Sleep(10 * time.Minute)

			// Cache hit: no catalog lookup and nothing registered again.
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

	t.Run("persistArtists reuses existing artists from DB", func(t *testing.T) {
		t.Parallel()
		d := newArtistTestDeps(t)

		fetched := []*entity.Artist{
			{Name: "Existing", MBID: "mbid-existing"},
			{Name: "New", MBID: "mbid-new"},
		}
		existingFromDB := []*entity.Artist{
			{ID: "db-id-1", Name: "Existing", MBID: "mbid-existing"},
		}
		createdNew := []*entity.Artist{
			{ID: "db-id-2", Name: "New", MBID: "mbid-new"},
		}

		d.searcher.EXPECT().Search(ctx, "mixed").Return(fetched, nil).Once()
		d.repo.EXPECT().ListByMBIDs(mock.Anything, []string{"mbid-existing", "mbid-new"}).Return(existingFromDB, nil).Once()
		d.repo.EXPECT().Create(mock.Anything, mock.MatchedBy(func(a *entity.Artist) bool {
			return a.MBID == "mbid-new"
		})).Return(createdNew, nil).Once()

		result, err := d.uc.Search(ctx, "mixed")

		assert.NoError(t, err)
		assert.Len(t, result, 2)
		assert.Equal(t, "db-id-1", result[0].ID)
		assert.Equal(t, "db-id-2", result[1].ID)
	})

	t.Run("persistArtists skips Create when all exist", func(t *testing.T) {
		t.Parallel()
		d := newArtistTestDeps(t)

		d.searcher.EXPECT().Search(ctx, "all-exist").Return([]*entity.Artist{
			{Name: "A", MBID: "mbid-a"},
			{Name: "B", MBID: "mbid-b"},
		}, nil).Once()
		d.repo.EXPECT().ListByMBIDs(mock.Anything, []string{"mbid-a", "mbid-b"}).Return([]*entity.Artist{
			{ID: "db-a", Name: "A", MBID: "mbid-a"},
			{ID: "db-b", Name: "B", MBID: "mbid-b"},
		}, nil).Once()
		// No Create call expected

		result, err := d.uc.Search(ctx, "all-exist")

		assert.NoError(t, err)
		assert.Len(t, result, 2)
		assert.Equal(t, "db-a", result[0].ID)
		assert.Equal(t, "db-b", result[1].ID)
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
