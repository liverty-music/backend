package entity_test

import (
	"strings"
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

	t.Run("contact email too long", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/organizer "Contact email too long"
		d := completeBusinessDetails()
		d.ContactEmail = emailOfLength(166)

		assert.Error(t, d.Validate())

		d.ContactEmail = emailOfLength(165)
		assert.NoError(t, d.Validate())
	})
}

// emailOfLength returns a well-formed email address of exactly n characters.
func emailOfLength(n int) string {
	const domain = "@example.com"
	return strings.Repeat("a", n-len(domain)) + domain
}

func TestOrganizer_Validate(t *testing.T) {
	t.Parallel()

	valid := func() *entity.Organizer {
		return entity.NewOrganizer("XX Records", "xx-records", "band@example.com")
	}

	tests := []struct {
		name    string
		mutate  func(o *entity.Organizer)
		wantErr bool
	}{
		{
			name:   "accepts a valid Organizer",
			mutate: func(*entity.Organizer) {},
		},
		{
			// @spec components/entity/organizer "Empty name"
			name:    "rejects an empty name",
			mutate:  func(o *entity.Organizer) { o.Name = "" },
			wantErr: true,
		},
		{
			// @spec components/entity/organizer "Name too long"
			name:    "rejects a 201-character name",
			mutate:  func(o *entity.Organizer) { o.Name = strings.Repeat("あ", 201) },
			wantErr: true,
		},
		{
			name:   "accepts a 200-character name",
			mutate: func(o *entity.Organizer) { o.Name = strings.Repeat("あ", 200) },
		},
		{
			// @spec components/entity/organizer "Valid short name"
			name:   "accepts the short name xx-records",
			mutate: func(o *entity.Organizer) { o.ShortName = "xx-records" },
		},
		{
			// @spec components/entity/organizer "Valid short name"
			name:   "accepts the short name band2026",
			mutate: func(o *entity.Organizer) { o.ShortName = "band2026" },
		},
		{
			name:   "accepts a 32-character short name",
			mutate: func(o *entity.Organizer) { o.ShortName = "a" + strings.Repeat("b", 31) },
		},
		{
			// @spec components/entity/organizer "Malformed short name"
			name:    "rejects the short name XX Records",
			mutate:  func(o *entity.Organizer) { o.ShortName = "XX Records" },
			wantErr: true,
		},
		{
			// @spec components/entity/organizer "Malformed short name"
			name:    "rejects a short name starting with a digit",
			mutate:  func(o *entity.Organizer) { o.ShortName = "2band" },
			wantErr: true,
		},
		{
			// @spec components/entity/organizer "Malformed short name"
			name:    "rejects a 2-character short name",
			mutate:  func(o *entity.Organizer) { o.ShortName = "ab" },
			wantErr: true,
		},
		{
			// @spec components/entity/organizer "Malformed short name"
			name:    "rejects a 33-character short name",
			mutate:  func(o *entity.Organizer) { o.ShortName = "a" + strings.Repeat("b", 32) },
			wantErr: true,
		},
		{
			name:    "rejects a missing short name",
			mutate:  func(o *entity.Organizer) { o.ShortName = "" },
			wantErr: true,
		},
		{
			// @spec components/entity/organizer "Reserved short name"
			name:    "rejects the reserved short name admin",
			mutate:  func(o *entity.Organizer) { o.ShortName = "admin" },
			wantErr: true,
		},
		{
			// @spec components/entity/organizer "Reserved short name"
			name:    "rejects the reserved short name liverty-music",
			mutate:  func(o *entity.Organizer) { o.ShortName = "liverty-music" },
			wantErr: true,
		},
		{
			// @spec components/entity/organizer "Malformed owner email"
			name:    "rejects an owner email that is not an email address",
			mutate:  func(o *entity.Organizer) { o.OwnerEmail = "band@" },
			wantErr: true,
		},
		{
			// @spec components/entity/organizer "Owner email too long"
			name:    "rejects a 166-character owner email",
			mutate:  func(o *entity.Organizer) { o.OwnerEmail = emailOfLength(166) },
			wantErr: true,
		},
		{
			name:   "accepts a 165-character owner email",
			mutate: func(o *entity.Organizer) { o.OwnerEmail = emailOfLength(165) },
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			o := valid()
			tt.mutate(o)

			err := o.Validate()

			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
		})
	}
}

func TestOrganizer_PlatformFeeRate(t *testing.T) {
	t.Parallel()

	// @spec components/entity/organizer "New Organizer"
	assert.Equal(t, 800, entity.NewOrganizer("Label", "label", "owner@example.com").PlatformFeeRateBps)

	// @spec components/entity/organizer "Rate over the bound"
	assert.Error(t, entity.ValidatePlatformFeeRate(3100))
	assert.Error(t, entity.ValidatePlatformFeeRate(-1))
	assert.NoError(t, entity.ValidatePlatformFeeRate(0))
	assert.NoError(t, entity.ValidatePlatformFeeRate(3000))
}
