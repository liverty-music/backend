package rdb_test

import (
	"context"
	"testing"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func businessDetails() entity.BusinessDetails {
	return entity.BusinessDetails{
		LegalName:           "株式会社リバティ",
		RepresentativeName:  "代表 太郎",
		BusinessAddress:     "東京都渋谷区1-2-3",
		BusinessPhoneNumber: "+81312345678",
		ContactEmail:        "contact@example.com",
	}
}

func TestOrganizerRepository_BusinessDetailsAndFeeRate(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	repo := rdb.NewOrganizerRepository(testDB)
	ctx := context.Background()

	t.Run("new organizer has the default rate and no business details", func(t *testing.T) {
		created, err := repo.Create(ctx, &entity.Organizer{ID: entity.NewID(), Name: "New Label", OperatorEmail: "op@example.com", Status: entity.OrganizerStatusProvisioning})
		require.NoError(t, err)

		assert.Equal(t, entity.DefaultPlatformFeeRateBps, created.PlatformFeeRateBps)
		assert.Nil(t, created.BusinessDetails)
	})

	t.Run("details entered at vetting", func(t *testing.T) {
		// @spec components/entity/organizer/set-business-details "Details entered at vetting"
		id := seedOrganizer(t)

		updated, err := repo.SetBusinessDetails(ctx, id, businessDetails())

		require.NoError(t, err)
		want := businessDetails()
		assert.Equal(t, &want, updated.BusinessDetails)
		assert.True(t, updated.HasCompleteBusinessDetails())
		got, err := repo.Get(ctx, id)
		require.NoError(t, err)
		assert.Equal(t, updated, got, "the returned Organizer is the stored one")
	})

	t.Run("malformed phone number", func(t *testing.T) {
		// @spec components/entity/organizer/set-business-details "Malformed phone number"
		id := seedOrganizer(t)
		details := businessDetails()
		details.BusinessPhoneNumber = "03-1234-5678"

		_, err := repo.SetBusinessDetails(ctx, id, details)

		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
		got, err := repo.Get(ctx, id)
		require.NoError(t, err)
		assert.Nil(t, got.BusinessDetails)
	})

	t.Run("unknown organizer", func(t *testing.T) {
		// @spec components/entity/organizer/set-business-details "Unknown Organizer"
		_, err := repo.SetBusinessDetails(ctx, entity.NewID(), businessDetails())
		assert.ErrorIs(t, err, apperr.ErrNotFound)
		assert.ErrorIs(t, repo.SetPlatformFeeRate(ctx, entity.NewID(), 500), apperr.ErrNotFound)
	})

	t.Run("pilot rate", func(t *testing.T) {
		// @spec components/entity/organizer/set-platform-fee-rate "Pilot rate"
		id := seedOrganizer(t)

		require.NoError(t, repo.SetPlatformFeeRate(ctx, id, 500))

		got, err := repo.Get(ctx, id)
		require.NoError(t, err)
		assert.Equal(t, 500, got.PlatformFeeRateBps)
	})

	t.Run("out of range", func(t *testing.T) {
		// @spec components/entity/organizer/set-platform-fee-rate "Out of range"
		id := seedOrganizer(t)

		err := repo.SetPlatformFeeRate(ctx, id, 3100)

		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
		got, err := repo.Get(ctx, id)
		require.NoError(t, err)
		assert.Equal(t, entity.DefaultPlatformFeeRateBps, got.PlatformFeeRateBps)
	})
}
