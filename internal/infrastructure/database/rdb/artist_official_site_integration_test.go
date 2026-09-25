package rdb_test

import (
	"context"
	"testing"

	"github.com/liverty-music/backend/internal/entity"
	entitymocks "github.com/liverty-music/backend/internal/entity/mocks"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
	"github.com/liverty-music/backend/internal/usecase"
	usecasemocks "github.com/liverty-music/backend/internal/usecase/mocks"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newArtistUseCaseWithRealRepo wires ArtistUseCase to the real, Postgres-backed
// ArtistRepository so CreateOfficialSite is exercised through the same ID-minting
// path (entity.NewOfficialSite) and database constraints (UNIQUE artist_id, FK to
// artists) it uses in production. The other collaborators are never invoked by
// CreateOfficialSite, so bare mocks with no expectations are sufficient.
func newArtistUseCaseWithRealRepo(t *testing.T) usecase.ArtistUseCase {
	t.Helper()

	logger, err := logging.New()
	require.NoError(t, err)

	return usecase.NewArtistUseCase(
		rdb.NewArtistRepository(testDB),
		entitymocks.NewMockArtistSearcher(t),
		entitymocks.NewMockArtistIdentityManager(t),
		usecasemocks.NewMockEventPublisher(t),
		entitymocks.NewMockCache(t),
		logger,
	)
}

// TestArtistUseCase_CreateOfficialSite_Integration exercises the fix for
// liverty-music/backend#470: ArtistHandler.CreateOfficialSite used to build an
// entity.OfficialSite without an ID, so every insert failed. This test drives
// the real ID-minting path (usecase -> entity.NewOfficialSite -> real repo ->
// real Postgres) end to end, confirming a fresh id is stored and that a second
// site for the same artist is rejected as AlreadyExists.
func TestArtistUseCase_CreateOfficialSite_Integration(t *testing.T) {
	ctx := context.Background()
	uc := newArtistUseCaseWithRealRepo(t)
	repo := rdb.NewArtistRepository(testDB)

	t.Run("stores a new official site with a fresh id for an artist without one", func(t *testing.T) {
		cleanDatabase(t)
		artistID := seedArtist(t, "Site Artist", "aa000000-0000-0000-0000-00000ucsite1")

		err := uc.CreateOfficialSite(ctx, artistID, "https://example.com")
		require.NoError(t, err)

		got, err := repo.GetOfficialSite(ctx, artistID)
		require.NoError(t, err)
		assert.NotEmpty(t, got.ID)
		assert.Equal(t, artistID, got.ArtistID)
		assert.Equal(t, "https://example.com", got.URL)
	})

	t.Run("a second official site for the same artist fails with AlreadyExists", func(t *testing.T) {
		cleanDatabase(t)
		artistID := seedArtist(t, "Site Artist Dup", "aa000000-0000-0000-0000-00000ucsite2")

		require.NoError(t, uc.CreateOfficialSite(ctx, artistID, "https://first.example.com"))

		err := uc.CreateOfficialSite(ctx, artistID, "https://second.example.com")

		assert.ErrorIs(t, err, apperr.ErrAlreadyExists)

		// The original site is kept, untouched by the failed second call.
		got, err := repo.GetOfficialSite(ctx, artistID)
		require.NoError(t, err)
		assert.Equal(t, "https://first.example.com", got.URL)
	})

	t.Run("an unknown artist id fails with FailedPrecondition", func(t *testing.T) {
		cleanDatabase(t)

		err := uc.CreateOfficialSite(ctx, entity.NewID(), "https://example.com")

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
	})

	t.Run("successive calls mint different ids", func(t *testing.T) {
		cleanDatabase(t)
		artistA := seedArtist(t, "Artist A", "aa000000-0000-0000-0000-00000ucsite3")
		artistB := seedArtist(t, "Artist B", "aa000000-0000-0000-0000-00000ucsite4")

		require.NoError(t, uc.CreateOfficialSite(ctx, artistA, "https://a.example.com"))
		require.NoError(t, uc.CreateOfficialSite(ctx, artistB, "https://b.example.com"))

		siteA, err := repo.GetOfficialSite(ctx, artistA)
		require.NoError(t, err)
		siteB, err := repo.GetOfficialSite(ctx, artistB)
		require.NoError(t, err)

		assert.NotEqual(t, siteA.ID, siteB.ID)
	})
}
