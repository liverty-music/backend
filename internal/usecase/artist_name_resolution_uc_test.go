package usecase_test

import (
	"fmt"
	"testing"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/entity/mocks"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
)

func TestArtistNameResolutionUseCase_ResolveCanonicalName(t *testing.T) {
	ctx := anyCtx
	logger, _ := logging.New()

	t.Run("updates name when canonical differs", func(t *testing.T) {
		artistRepo := mocks.NewMockArtistRepository(t)
		idManager := mocks.NewMockArtistIdentityManager(t)
		uc := usecase.NewArtistNameResolutionUseCase(artistRepo, idManager, logger)

		idManager.EXPECT().GetArtist(ctx, "mbid-001").Return(&entity.Artist{
			Name: "Canonical Name",
			MBID: "mbid-001",
		}, nil).Once()
		artistRepo.EXPECT().UpdateName(ctx, "artist-1", "Canonical Name").Return(nil).Once()

		err := uc.ResolveCanonicalName(t.Context(), "artist-1", "mbid-001", "Original Name")
		assert.NoError(t, err)
	})

	t.Run("no-op when name already matches", func(t *testing.T) {
		artistRepo := mocks.NewMockArtistRepository(t)
		idManager := mocks.NewMockArtistIdentityManager(t)
		uc := usecase.NewArtistNameResolutionUseCase(artistRepo, idManager, logger)

		idManager.EXPECT().GetArtist(ctx, "mbid-002").Return(&entity.Artist{
			Name: "Same Name",
			MBID: "mbid-002",
		}, nil).Once()

		err := uc.ResolveCanonicalName(t.Context(), "artist-2", "mbid-002", "Same Name")
		assert.NoError(t, err)
	})

	t.Run("returns error when MusicBrainz lookup fails", func(t *testing.T) {
		artistRepo := mocks.NewMockArtistRepository(t)
		idManager := mocks.NewMockArtistIdentityManager(t)
		uc := usecase.NewArtistNameResolutionUseCase(artistRepo, idManager, logger)

		idManager.EXPECT().GetArtist(ctx, "mbid-fail").Return(nil, fmt.Errorf("rate limited")).Once()

		err := uc.ResolveCanonicalName(t.Context(), "artist-3", "mbid-fail", "Some Artist")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "resolve canonical name")
	})

	t.Run("returns error when UpdateName fails", func(t *testing.T) {
		artistRepo := mocks.NewMockArtistRepository(t)
		idManager := mocks.NewMockArtistIdentityManager(t)
		uc := usecase.NewArtistNameResolutionUseCase(artistRepo, idManager, logger)

		idManager.EXPECT().GetArtist(ctx, "mbid-003").Return(&entity.Artist{
			Name: "New Name",
			MBID: "mbid-003",
		}, nil).Once()
		artistRepo.EXPECT().UpdateName(ctx, "artist-4", "New Name").Return(fmt.Errorf("db error")).Once()

		err := uc.ResolveCanonicalName(t.Context(), "artist-4", "mbid-003", "Old Name")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "update artist name")
	})
}
