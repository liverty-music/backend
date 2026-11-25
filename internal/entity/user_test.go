package entity_test

import (
	"strings"
	"testing"

	"github.com/pannpers/go-apperr/apperr"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/stretchr/testify/assert"
)

func TestCreateUser(t *testing.T) {
	t.Parallel()

	t.Run("set all fields from params and generate UUIDv7 ID", func(t *testing.T) {
		t.Parallel()

		params := &entity.NewUser{
			ProviderUserID:    "ext-123",
			Email:             "test@example.com",
			Name:              "Test User",
			PreferredLanguage: "en",
			TimeZone:          "America/New_York",
		}

		got := entity.CreateUser(params)

		assert.NotEmpty(t, got.ID)
		assert.Equal(t, params.ProviderUserID, got.ProviderUserID)
		assert.Equal(t, params.Email, got.Email)
		assert.Equal(t, params.Name, got.Name)
		assert.Equal(t, params.PreferredLanguage, got.PreferredLanguage)
		assert.Equal(t, params.TimeZone, got.TimeZone)
	})

	t.Run("generate different IDs on successive calls", func(t *testing.T) {
		t.Parallel()

		params := &entity.NewUser{
			ProviderUserID: "ext-456",
			Email:          "a@example.com",
			Name:           "User A",
		}

		first := entity.CreateUser(params)
		second := entity.CreateUser(params)

		assert.NotEqual(t, first.ID, second.ID)
	})
}

func TestHome_NamesFirstLevelArea(t *testing.T) {
	t.Parallel()

	area := func(code string) *entity.Area {
		return &entity.Area{Code: code, Kind: entity.AreaKindSubdivision, CountryCode: code[:2], NameEN: code}
	}
	tests := []struct {
		name     string
		home     entity.Home
		resolved *entity.Area // what the level 1 resolves to; nil for nothing
		wantErr  bool
	}{
		// @spec components/entity/user "Prefecture"
		{name: "Prefecture", home: entity.Home{CountryCode: "JP", Level1: "JP-13"}, resolved: area("JP-13")},
		// @spec components/entity/user "Territory without subdivisions"
		{name: "Territory without subdivisions", home: entity.Home{CountryCode: "HK", Level1: "HK"},
			resolved: &entity.Area{Code: "HK", Kind: entity.AreaKindCountry, CountryCode: "HK", NameEN: "Hong Kong"}},
		// @spec components/entity/user "Malformed country code"
		{name: "Malformed country code, one letter", home: entity.Home{CountryCode: "j", Level1: "JP-13"}, resolved: area("JP-13"), wantErr: true},
		{name: "Malformed country code, lower case", home: entity.Home{CountryCode: "jp", Level1: "JP-13"}, resolved: area("JP-13"), wantErr: true},
		// @spec components/entity/user "Not an Area code"
		{name: "Not an Area code", home: entity.Home{CountryCode: "JP", Level1: "INVALID"}, wantErr: true},
		// @spec components/entity/user "Level 1 in another country"
		{name: "Level 1 in another country", home: entity.Home{CountryCode: "JP", Level1: "US-CA"}, resolved: area("US-CA"), wantErr: true},
		// @spec components/entity/user "Well-formed code that is no Area"
		{name: "Well-formed code that is no Area", home: entity.Home{CountryCode: "JP", Level1: "JP-99"}, wantErr: true},
		// @spec components/entity/user "Lower-level Area"
		{name: "Lower-level Area", home: entity.Home{CountryCode: "FR", Level1: "FR-33"}, resolved: area("FR-NAQ"), wantErr: true},
		// @spec components/entity/user "Region"
		{name: "Region", home: entity.Home{CountryCode: "JP", Level1: "JP-KANTO"}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := tt.home.Validate()
			if err == nil {
				err = tt.home.CheckArea(tt.resolved)
			}
			if tt.wantErr {
				assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestHomeOf_ReferencePoint(t *testing.T) {
	t.Parallel()

	t.Run("Prefecture with a reference point", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/user "Prefecture with a reference point"
		fukuoka := &entity.Coordinates{Latitude: 33.6064, Longitude: 130.4181}
		home := entity.HomeOf(&entity.Area{Code: "JP-40", Kind: entity.AreaKindSubdivision, CountryCode: "JP", NameEN: "Fukuoka", ReferencePoint: fukuoka})
		assert.Equal(t, &entity.Home{CountryCode: "JP", Level1: "JP-40", ReferencePoint: fukuoka}, home)
	})
	t.Run("Area without a reference point", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/user "Area without a reference point"
		home := entity.HomeOf(&entity.Area{Code: "HK", Kind: entity.AreaKindCountry, CountryCode: "HK", NameEN: "Hong Kong"})
		assert.Nil(t, home.ReferencePoint)
	})
}

func TestValidatePersonalDetails(t *testing.T) {
	t.Parallel()

	type args struct {
		fullName    *string
		phoneNumber *string
	}
	tests := []struct {
		name  string
		args  args
		valid bool
	}{
		{
			// @spec components/entity/user "Details complete"
			name:  "details complete",
			args:  args{fullName: new("山田 花子"), phoneNumber: new("+819012345678")},
			valid: true,
		},
		{
			// @spec components/entity/user "Domestic-format phone number"
			name: "domestic format with hyphens",
			args: args{fullName: new("山田 花子"), phoneNumber: new("090-1234-5678")},
		},
		{
			name: "domestic format without hyphens",
			args: args{fullName: new("山田 花子"), phoneNumber: new("09012345678")},
		},
		{
			// @spec components/entity/user "Phone number too long"
			name: "phone number too long",
			args: args{fullName: new("山田 花子"), phoneNumber: new("+" + strings.Repeat("1", 16))},
		},
		{
			name:  "phone number of 15 digits",
			args:  args{fullName: new("山田 花子"), phoneNumber: new("+" + strings.Repeat("1", 15))},
			valid: true,
		},
		{
			// @spec components/entity/user "Not given yet"
			name:  "not given yet",
			args:  args{},
			valid: true,
		},
		{
			name:  "name of 200 characters",
			args:  args{fullName: new(strings.Repeat("名", 200)), phoneNumber: new("+819012345678")},
			valid: true,
		},
		{
			name: "a given name of no characters",
			args: args{fullName: new(""), phoneNumber: new("+819012345678")},
		},
		{
			name: "name of 201 characters",
			args: args{fullName: new(strings.Repeat("名", 201)), phoneNumber: new("+819012345678")},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			user := &entity.User{FullName: tt.args.fullName, PhoneNumber: tt.args.phoneNumber}
			err := user.ValidatePersonalDetails()

			if tt.valid {
				assert.NoError(t, err)
				return
			}
			assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
		})
	}
}

func TestNewUser_ValidateEmail(t *testing.T) {
	t.Parallel()

	const domain = "@example.com"
	email := func(n int) string { return strings.Repeat("a", n-len(domain)) + domain }

	assert.NoError(t, (&entity.NewUser{Email: email(165)}).ValidateEmail())
	assert.ErrorIs(t, (&entity.NewUser{Email: email(166)}).ValidateEmail(), apperr.ErrInvalidArgument)
}

func TestUser_EmailLanguage(t *testing.T) {
	t.Parallel()
	for lang, want := range map[string]string{"en": "en", "ja": "ja", "": "ja", "fr": "ja", "EN": "ja"} {
		assert.Equal(t, want, (&entity.User{PreferredLanguage: lang}).EmailLanguage(), "preferred %q", lang)
	}
}

func TestIdentityEmail_CheckPasskeyRecovery(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		email   entity.IdentityEmail
		wantErr error
	}{
		{name: "a verified address gets a link", email: entity.IdentityEmail{Address: "fan@example.com", Verified: true}},
		{name: "an unverified address gets none", email: entity.IdentityEmail{Address: "fan@example.com"}, wantErr: apperr.ErrFailedPrecondition},
		{name: "no address gets none, even marked verified", email: entity.IdentityEmail{Verified: true}, wantErr: apperr.ErrFailedPrecondition},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := tt.email.CheckPasskeyRecovery()

			if tt.wantErr == nil {
				assert.NoError(t, err)
				return
			}
			assert.ErrorIs(t, err, tt.wantErr)
		})
	}
}
