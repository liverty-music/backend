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

func vettedBusinessDetails() entity.BusinessDetails {
	return entity.BusinessDetails{
		LegalName: "株式会社リバティ", RepresentativeName: "代表 太郎", BusinessAddress: "東京都渋谷区1-2-3",
		BusinessPhoneNumber: "+81312345678", ContactEmail: "contact@example.com",
	}
}

func TestOrganizerUseCase_SetBusinessDetails(t *testing.T) {
	t.Parallel()

	t.Run("details recorded at vetting", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/organizer/set-business-details "Details recorded at vetting"
		d := newOrganizerTestDeps(t)
		details := vettedBusinessDetails()
		d.orgRepo.EXPECT().Get(mock.Anything, "org-1").Return(&entity.Organizer{ID: "org-1", Status: entity.OrganizerStatusActive}, nil).Once()
		d.orgRepo.EXPECT().SetBusinessDetails(mock.Anything, "org-1", details).
			Return(&entity.Organizer{ID: "org-1", Status: entity.OrganizerStatusActive, BusinessDetails: &details}, nil)

		org, err := d.uc.SetBusinessDetails(context.Background(), "org-1", details)

		require.NoError(t, err)
		assert.True(t, org.HasCompleteBusinessDetails())
	})

	t.Run("deactivated organizer", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/organizer/set-business-details "Deactivated Organizer"
		d := newOrganizerTestDeps(t)
		d.orgRepo.EXPECT().Get(mock.Anything, "org-1").Return(&entity.Organizer{ID: "org-1", Status: entity.OrganizerStatusDeactivated}, nil)

		_, err := d.uc.SetBusinessDetails(context.Background(), "org-1", vettedBusinessDetails())

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
		d.orgRepo.AssertNotCalled(t, "SetBusinessDetails", mock.Anything, mock.Anything, mock.Anything)
	})

	t.Run("invalid details are returned unchanged", func(t *testing.T) {
		t.Parallel()
		d := newOrganizerTestDeps(t)
		d.orgRepo.EXPECT().Get(mock.Anything, "org-1").Return(&entity.Organizer{ID: "org-1", Status: entity.OrganizerStatusActive}, nil)
		d.orgRepo.EXPECT().SetBusinessDetails(mock.Anything, "org-1", mock.Anything).Return(nil, apperr.New(apperr.ErrInvalidArgument.Code, "bad phone"))

		_, err := d.uc.SetBusinessDetails(context.Background(), "org-1", vettedBusinessDetails())

		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
	})

	t.Run("unknown organizer", func(t *testing.T) {
		t.Parallel()
		d := newOrganizerTestDeps(t)
		d.orgRepo.EXPECT().Get(mock.Anything, "org-x").Return(nil, apperr.ErrNotFound)

		_, err := d.uc.SetBusinessDetails(context.Background(), "org-x", vettedBusinessDetails())

		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})
}

func TestOrganizerUseCase_SetPlatformFeeRate(t *testing.T) {
	t.Parallel()

	t.Run("pilot organizer", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/organizer/set-platform-fee-rate "Pilot Organizer"
		d := newOrganizerTestDeps(t)
		d.orgRepo.EXPECT().Get(mock.Anything, "org-1").
			Return(&entity.Organizer{ID: "org-1", Status: entity.OrganizerStatusActive, PlatformFeeRateBps: 800}, nil).Once()
		d.orgRepo.EXPECT().SetPlatformFeeRate(mock.Anything, "org-1", 500).Return(nil).Once()

		org, err := d.uc.SetPlatformFeeRate(context.Background(), "org-1", 500)

		require.NoError(t, err)
		assert.Equal(t, 500, org.PlatformFeeRateBps)
	})

	t.Run("deactivated organizer", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/organizer/set-platform-fee-rate "Deactivated Organizer"
		d := newOrganizerTestDeps(t)
		d.orgRepo.EXPECT().Get(mock.Anything, "org-1").
			Return(&entity.Organizer{ID: "org-1", Status: entity.OrganizerStatusDeactivated, PlatformFeeRateBps: 800}, nil).Once()

		_, err := d.uc.SetPlatformFeeRate(context.Background(), "org-1", 500)

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
		d.orgRepo.AssertNotCalled(t, "SetPlatformFeeRate", mock.Anything, mock.Anything, mock.Anything)
	})

	t.Run("unknown organizer", func(t *testing.T) {
		t.Parallel()
		d := newOrganizerTestDeps(t)
		d.orgRepo.EXPECT().Get(mock.Anything, "org-x").Return(nil, apperr.ErrNotFound).Once()

		_, err := d.uc.SetPlatformFeeRate(context.Background(), "org-x", 500)

		assert.ErrorIs(t, err, apperr.ErrNotFound)
		d.orgRepo.AssertNotCalled(t, "SetPlatformFeeRate", mock.Anything, mock.Anything, mock.Anything)
	})

	t.Run("store failure is returned unchanged", func(t *testing.T) {
		t.Parallel()
		d := newOrganizerTestDeps(t)
		d.orgRepo.EXPECT().Get(mock.Anything, "org-1").
			Return(&entity.Organizer{ID: "org-1", Status: entity.OrganizerStatusActive}, nil).Once()
		d.orgRepo.EXPECT().SetPlatformFeeRate(mock.Anything, "org-1", 5000).
			Return(apperr.New(apperr.ErrInvalidArgument.Code, "rate out of range")).Once()

		_, err := d.uc.SetPlatformFeeRate(context.Background(), "org-1", 5000)

		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
	})
}
