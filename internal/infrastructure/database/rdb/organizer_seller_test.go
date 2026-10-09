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

func sellerDetails() entity.SellerDetails {
	return entity.SellerDetails{
		LegalName:          "株式会社リバティ",
		RepresentativeName: "代表 太郎",
		Address:            "東京都渋谷区1-2-3",
		PhoneNumber:        "+81312345678",
		ContactEmail:       "contact@example.com",
	}
}

func TestOrganizerRepository_SellerDetailsAndFeeRate(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	repo := rdb.NewOrganizerRepository(testDB)
	ctx := context.Background()

	t.Run("new organizer has the default rate and no seller details", func(t *testing.T) {
		created, err := repo.Create(ctx, &entity.Organizer{ID: entity.NewID(), Name: "New Label", OperatorEmail: "op@example.com", Status: entity.OrganizerStatusProvisioning})
		require.NoError(t, err)

		assert.Equal(t, entity.DefaultPlatformFeeRateBps, created.PlatformFeeRateBps)
		assert.Nil(t, created.SellerDetails)
	})

	t.Run("details entered at vetting", func(t *testing.T) {
		// @spec components/entity/organizer/set-seller-details "Details entered at vetting"
		id := seedOrganizer(t)

		require.NoError(t, repo.SetSellerDetails(ctx, id, sellerDetails()))

		got, err := repo.Get(ctx, id)
		require.NoError(t, err)
		want := sellerDetails()
		assert.Equal(t, &want, got.SellerDetails)
		assert.True(t, got.HasCompleteSellerDetails())
	})

	t.Run("malformed phone number", func(t *testing.T) {
		// @spec components/entity/organizer/set-seller-details "Malformed phone number"
		id := seedOrganizer(t)
		details := sellerDetails()
		details.PhoneNumber = "03-1234-5678"

		err := repo.SetSellerDetails(ctx, id, details)

		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
		got, err := repo.Get(ctx, id)
		require.NoError(t, err)
		assert.Nil(t, got.SellerDetails)
	})

	t.Run("unknown organizer", func(t *testing.T) {
		assert.ErrorIs(t, repo.SetSellerDetails(ctx, entity.NewID(), sellerDetails()), apperr.ErrNotFound)
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

func TestUserRepository_UpdateHolderIdentity(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	repo := rdb.NewUserRepository(testDB)
	ctx := context.Background()

	t.Run("first checkout", func(t *testing.T) {
		// @spec components/entity/user/update-holder-identity "First checkout"
		id := seedUser(t, "holder", entity.NewID()+"@example.com", entity.NewID())
		before, err := repo.Get(ctx, id)
		require.NoError(t, err)
		require.Nil(t, before.HolderIdentity)

		updated, err := repo.UpdateHolderIdentity(ctx, id, entity.HolderIdentity{FullName: "山田 花子", PhoneNumber: "+819012345678"})

		require.NoError(t, err)
		assert.Equal(t, &entity.HolderIdentity{FullName: "山田 花子", PhoneNumber: "+819012345678"}, updated.HolderIdentity)
		got, err := repo.Get(ctx, id)
		require.NoError(t, err)
		assert.Equal(t, updated.HolderIdentity, got.HolderIdentity)
	})

	t.Run("invalid name", func(t *testing.T) {
		// @spec components/entity/user/update-holder-identity "Invalid name"
		id := seedUser(t, "holder2", entity.NewID()+"@example.com", entity.NewID())

		_, err := repo.UpdateHolderIdentity(ctx, id, entity.HolderIdentity{FullName: "", PhoneNumber: "+819012345678"})

		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
		got, err := repo.Get(ctx, id)
		require.NoError(t, err)
		assert.Nil(t, got.HolderIdentity)
	})

	t.Run("unknown user", func(t *testing.T) {
		_, err := repo.UpdateHolderIdentity(ctx, entity.NewID(), entity.HolderIdentity{FullName: "x", PhoneNumber: "+819012345678"})

		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})
}
