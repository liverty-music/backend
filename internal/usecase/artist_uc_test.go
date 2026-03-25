package usecase_test

import (
	"context"
	"testing"
	"time"

	"github.com/ThreeDotsLabs/watermill"
	"github.com/ThreeDotsLabs/watermill/pubsub/gochannel"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/entity/mocks"
	"github.com/liverty-music/backend/internal/infrastructure/messaging"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/liverty-music/backend/pkg/cache"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// anyCtx matches any context.Context regardless of type (e.g. context.WithoutCancel).
var anyCtx = mock.MatchedBy(func(context.Context) bool { return true })

func newTestPublisher() *gochannel.GoChannel {
	return gochannel.NewGoChannel(gochannel.Config{OutputChannelBuffer: 64}, watermill.NopLogger{})
}

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
	d.uc = usecase.NewArtistUseCase(d.repo, d.searcher, d.idManager, messaging.NewEventPublisher(newTestPublisher()), cache.NewMemoryCache(1*time.Hour), newTestLogger(t))
	return d
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

	t.Run("error - empty name", func(t *testing.T) {
		t.Parallel()
		d := newArtistTestDeps(t)

		artist := &entity.Artist{
			ID:   "artist-1",
			Name: "",
			MBID: "",
		}

		result, err := d.uc.Create(ctx, artist)

		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
		assert.Nil(t, result)
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
		d.repo.EXPECT().ListByMBIDs(ctx, []string{"mbid-a", "mbid-b"}).Return([]*entity.Artist{}, nil).Once()
		d.repo.EXPECT().Create(ctx, mock.AnythingOfType("*entity.Artist"), mock.AnythingOfType("*entity.Artist")).Return(persisted, nil).Once()

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
		d.repo.EXPECT().ListByMBIDs(ctx, []string{"mbid-x"}).Return([]*entity.Artist{}, nil).Once()
		d.repo.EXPECT().Create(ctx, mock.AnythingOfType("*entity.Artist")).Return(persisted, nil).Once()

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

	t.Run("returns cached results on second call", func(t *testing.T) {
		t.Parallel()
		d := newArtistTestDeps(t)

		persisted := []*entity.Artist{
			{ID: "id-a", Name: "Artist A", MBID: "mbid-a"},
		}

		d.searcher.EXPECT().ListTop(ctx, "US", "", int32(0)).Return([]*entity.Artist{{Name: "Artist A", MBID: "mbid-a"}}, nil).Once()
		d.repo.EXPECT().ListByMBIDs(ctx, []string{"mbid-a"}).Return([]*entity.Artist{}, nil).Once()
		d.repo.EXPECT().Create(ctx, mock.AnythingOfType("*entity.Artist")).Return(persisted, nil).Once()

		// First call — cache miss
		_, err := d.uc.ListTop(ctx, "US", "", int32(0))
		assert.NoError(t, err)

		// Second call — cache hit (no additional mock calls expected)
		result, err := d.uc.ListTop(ctx, "US", "", int32(0))
		assert.NoError(t, err)
		assert.Len(t, result, 1)
	})
}

func TestArtistUseCase_ListSimilar(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	t.Run("error - seed artist not found", func(t *testing.T) {
		t.Parallel()
		d := newArtistTestDeps(t)

		d.repo.EXPECT().Get(ctx, "missing-id").Return(nil, apperr.ErrNotFound).Once()

		result, err := d.uc.ListSimilar(ctx, "missing-id", int32(0))

		assert.ErrorIs(t, err, apperr.ErrNotFound)
		assert.Nil(t, result)
	})

	t.Run("returns persisted artists with valid IDs", func(t *testing.T) {
		t.Parallel()
		d := newArtistTestDeps(t)

		seedArtist := &entity.Artist{ID: "seed-id", Name: "Seed", MBID: "seed-mbid"}
		fetched := []*entity.Artist{
			{Name: "Similar A", MBID: "sim-a"},
			{Name: "Similar B", MBID: "sim-b"},
		}
		persisted := []*entity.Artist{
			{ID: "id-sim-a", Name: "Similar A", MBID: "sim-a"},
			{ID: "id-sim-b", Name: "Similar B", MBID: "sim-b"},
		}

		d.repo.EXPECT().Get(ctx, "seed-id").Return(seedArtist, nil).Once()
		d.searcher.EXPECT().ListSimilar(ctx, seedArtist, int32(0)).Return(fetched, nil).Once()
		d.repo.EXPECT().ListByMBIDs(ctx, []string{"sim-a", "sim-b"}).Return([]*entity.Artist{}, nil).Once()
		d.repo.EXPECT().Create(ctx, mock.AnythingOfType("*entity.Artist"), mock.AnythingOfType("*entity.Artist")).Return(persisted, nil).Once()

		result, err := d.uc.ListSimilar(ctx, "seed-id", int32(0))

		assert.NoError(t, err)
		assert.Len(t, result, 2)
		assert.Equal(t, "id-sim-a", result[0].ID)
		assert.Equal(t, "id-sim-b", result[1].ID)
	})

	t.Run("filters out empty MBID entries", func(t *testing.T) {
		t.Parallel()
		d := newArtistTestDeps(t)

		seedArtist := &entity.Artist{ID: "seed-id", Name: "Seed", MBID: "seed-mbid"}
		fetched := []*entity.Artist{
			{Name: "With MBID", MBID: "sim-x"},
			{Name: "No MBID", MBID: ""},
		}
		persisted := []*entity.Artist{
			{ID: "id-sim-x", Name: "With MBID", MBID: "sim-x"},
		}

		d.repo.EXPECT().Get(ctx, "seed-id").Return(seedArtist, nil).Once()
		d.searcher.EXPECT().ListSimilar(ctx, seedArtist, int32(0)).Return(fetched, nil).Once()
		d.repo.EXPECT().ListByMBIDs(ctx, []string{"sim-x"}).Return([]*entity.Artist{}, nil).Once()
		d.repo.EXPECT().Create(ctx, mock.AnythingOfType("*entity.Artist")).Return(persisted, nil).Once()

		result, err := d.uc.ListSimilar(ctx, "seed-id", int32(0))

		assert.NoError(t, err)
		assert.Len(t, result, 1)
		assert.Equal(t, "sim-x", result[0].MBID)
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
		d.repo.EXPECT().ListByMBIDs(ctx, []string{"abc", "def"}).Return([]*entity.Artist{}, nil).Once()
		d.repo.EXPECT().Create(ctx, mock.AnythingOfType("*entity.Artist"), mock.AnythingOfType("*entity.Artist")).Return(persisted, nil).Once()

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

	t.Run("error - empty query", func(t *testing.T) {
		t.Parallel()
		d := newArtistTestDeps(t)

		result, err := d.uc.Search(ctx, "")

		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
		assert.Nil(t, result)
	})

	t.Run("returns cached results on second call", func(t *testing.T) {
		t.Parallel()
		d := newArtistTestDeps(t)

		persisted := []*entity.Artist{
			{ID: "id-1", Name: "Artist", MBID: "mbid-1"},
		}

		d.searcher.EXPECT().Search(ctx, "cached").Return([]*entity.Artist{{Name: "Artist", MBID: "mbid-1"}}, nil).Once()
		d.repo.EXPECT().ListByMBIDs(ctx, []string{"mbid-1"}).Return([]*entity.Artist{}, nil).Once()
		d.repo.EXPECT().Create(ctx, mock.AnythingOfType("*entity.Artist")).Return(persisted, nil).Once()

		// First call — cache miss
		_, err := d.uc.Search(ctx, "cached")
		assert.NoError(t, err)

		// Second call — cache hit
		result, err := d.uc.Search(ctx, "cached")
		assert.NoError(t, err)
		assert.Len(t, result, 1)
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
		d.repo.EXPECT().ListByMBIDs(ctx, []string{"mbid-existing", "mbid-new"}).Return(existingFromDB, nil).Once()
		d.repo.EXPECT().Create(ctx, mock.MatchedBy(func(a *entity.Artist) bool {
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
		d.repo.EXPECT().ListByMBIDs(ctx, []string{"mbid-a", "mbid-b"}).Return([]*entity.Artist{
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
