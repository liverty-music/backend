package usecase_test

import (
	"context"
	"testing"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func vettedSellerDetails() entity.SellerDetails {
	return entity.SellerDetails{
		LegalName: "株式会社リバティ", RepresentativeName: "代表 太郎", Address: "東京都渋谷区1-2-3",
		PhoneNumber: "+81312345678", ContactEmail: "contact@example.com",
	}
}

func TestOrganizerUseCase_UpdateSellerDetails(t *testing.T) {
	t.Parallel()

	t.Run("details recorded at vetting", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/organizer/update-seller-details "Details recorded at vetting"
		d := newOrganizerTestDeps(t)
		details := vettedSellerDetails()
		d.orgRepo.EXPECT().Get(mock.Anything, "org-1").Return(&entity.Organizer{ID: "org-1", Status: entity.OrganizerStatusActive}, nil).Once()
		d.orgRepo.EXPECT().SetSellerDetails(mock.Anything, "org-1", details).Return(nil)
		d.orgRepo.EXPECT().Get(mock.Anything, "org-1").Return(&entity.Organizer{ID: "org-1", Status: entity.OrganizerStatusActive, SellerDetails: &details}, nil).Once()

		org, err := d.uc.UpdateSellerDetails(context.Background(), "org-1", details)

		require.NoError(t, err)
		assert.True(t, org.HasCompleteSellerDetails())
	})

	t.Run("deactivated organizer", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/organizer/update-seller-details "Deactivated Organizer"
		d := newOrganizerTestDeps(t)
		d.orgRepo.EXPECT().Get(mock.Anything, "org-1").Return(&entity.Organizer{ID: "org-1", Status: entity.OrganizerStatusDeactivated}, nil)

		_, err := d.uc.UpdateSellerDetails(context.Background(), "org-1", vettedSellerDetails())

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
	})

	t.Run("invalid details are returned unchanged", func(t *testing.T) {
		t.Parallel()
		d := newOrganizerTestDeps(t)
		d.orgRepo.EXPECT().Get(mock.Anything, "org-1").Return(&entity.Organizer{ID: "org-1", Status: entity.OrganizerStatusActive}, nil)
		d.orgRepo.EXPECT().SetSellerDetails(mock.Anything, "org-1", mock.Anything).Return(apperr.New(apperr.ErrInvalidArgument.Code, "bad phone"))

		_, err := d.uc.UpdateSellerDetails(context.Background(), "org-1", vettedSellerDetails())

		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
	})
}

func TestOrganizerUseCase_SetPlatformFeeRate(t *testing.T) {
	t.Parallel()

	t.Run("pilot organizer", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/organizer/set-platform-fee-rate "Pilot Organizer"
		d := newOrganizerTestDeps(t)
		d.orgRepo.EXPECT().SetPlatformFeeRate(mock.Anything, "org-1", 500).Return(nil)
		d.orgRepo.EXPECT().Get(mock.Anything, "org-1").Return(&entity.Organizer{ID: "org-1", PlatformFeeRateBps: 500}, nil)

		org, err := d.uc.SetPlatformFeeRate(context.Background(), "org-1", 500)

		require.NoError(t, err)
		assert.Equal(t, 500, org.PlatformFeeRateBps)
	})

	t.Run("unknown organizer", func(t *testing.T) {
		t.Parallel()
		d := newOrganizerTestDeps(t)
		d.orgRepo.EXPECT().SetPlatformFeeRate(mock.Anything, "org-x", 500).Return(apperr.ErrNotFound)

		_, err := d.uc.SetPlatformFeeRate(context.Background(), "org-x", 500)

		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})
}
