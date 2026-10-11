package entity_test

import (
	"testing"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/stretchr/testify/assert"
)

func completeBusinessDetails() *entity.BusinessDetails {
	return &entity.BusinessDetails{
		LegalName:           "株式会社リバティ",
		RepresentativeName:  "代表 太郎",
		BusinessAddress:     "東京都渋谷区1-2-3",
		BusinessPhoneNumber: "+81312345678",
		ContactEmail:        "contact@example.com",
	}
}

func TestOrganizer_HasCompleteBusinessDetails(t *testing.T) {
	t.Parallel()

	t.Run("corporation with all details", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/organizer "Corporation with all details"
		o := &entity.Organizer{BusinessDetails: completeBusinessDetails()}

		assert.True(t, o.HasCompleteBusinessDetails())
	})

	t.Run("address missing", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/organizer "Address missing"
		d := completeBusinessDetails()
		d.BusinessAddress = ""
		o := &entity.Organizer{BusinessDetails: d}

		assert.False(t, o.HasCompleteBusinessDetails())
	})

	t.Run("no business details", func(t *testing.T) {
		t.Parallel()
		assert.False(t, (&entity.Organizer{}).HasCompleteBusinessDetails())
	})

	t.Run("domestic-format phone number", func(t *testing.T) {
		t.Parallel()
		d := completeBusinessDetails()
		d.BusinessPhoneNumber = "03-1234-5678"

		assert.Error(t, d.Validate())
	})

	t.Run("malformed contact email", func(t *testing.T) {
		t.Parallel()
		d := completeBusinessDetails()
		d.ContactEmail = "not-an-email"

		assert.Error(t, d.Validate())
	})
}

func TestOrganizer_PlatformFeeRate(t *testing.T) {
	t.Parallel()

	// @spec components/entity/organizer "New Organizer"
	assert.Equal(t, 800, entity.NewOrganizer("Label").PlatformFeeRateBps)

	// @spec components/entity/organizer "Rate over the bound"
	assert.Error(t, entity.ValidatePlatformFeeRate(3100))
	assert.Error(t, entity.ValidatePlatformFeeRate(-1))
	assert.NoError(t, entity.ValidatePlatformFeeRate(0))
	assert.NoError(t, entity.ValidatePlatformFeeRate(3000))
}
