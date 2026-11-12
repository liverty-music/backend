package entity_test

import (
	"fmt"
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

func TestNewHome(t *testing.T) {
	t.Parallel()

	t.Run("set CountryCode, Level1, and Level2 with generated ID", func(t *testing.T) {
		t.Parallel()

		level2 := "13"
		got := entity.NewHome("JP", "JP-13", &level2)

		assert.NotEmpty(t, got.ID)
		assert.Equal(t, "JP", got.CountryCode)
		assert.Equal(t, "JP-13", got.Level1)
		assert.Equal(t, &level2, got.Level2)
	})

	t.Run("set nil Level2 when not provided", func(t *testing.T) {
		t.Parallel()

		got := entity.NewHome("US", "US-NY", nil)

		assert.NotEmpty(t, got.ID)
		assert.Equal(t, "US", got.CountryCode)
		assert.Equal(t, "US-NY", got.Level1)
		assert.Nil(t, got.Level2)
	})

	t.Run("generate different IDs on successive calls", func(t *testing.T) {
		t.Parallel()

		first := entity.NewHome("JP", "JP-13", nil)
		second := entity.NewHome("JP", "JP-13", nil)

		assert.NotEqual(t, first.ID, second.ID)
	})
}

func TestHome_Validate(t *testing.T) {
	t.Parallel()

	level2Valid := "13"
	level2Empty := ""
	level2TooLong := "123456789012345678901" // 21 chars

	type args struct {
		home *entity.Home
	}
	tests := []struct {
		name    string
		args    args
		wantErr error
	}{
		{
			name: "return nil for valid home with no level2",
			args: args{
				home: &entity.Home{
					CountryCode: "JP",
					Level1:      "JP-13",
					Level2:      nil,
				},
			},
			wantErr: nil,
		},
		{
			name: "return nil for valid home with level2",
			args: args{
				home: &entity.Home{
					CountryCode: "JP",
					Level1:      "JP-13",
					Level2:      &level2Valid,
				},
			},
			wantErr: nil,
		},
		{
			name: "return error when country code format is invalid",
			args: args{
				home: &entity.Home{
					CountryCode: "jp",
					Level1:      "JP-13",
				},
			},
			wantErr: assert.AnError,
		},
		{
			name: "return error when level1 format is invalid",
			args: args{
				home: &entity.Home{
					CountryCode: "JP",
					Level1:      "13",
				},
			},
			wantErr: assert.AnError,
		},
		{
			name: "return error when level1 prefix does not match country code",
			args: args{
				home: &entity.Home{
					CountryCode: "US",
					Level1:      "JP-13",
				},
			},
			wantErr: assert.AnError,
		},
		{
			name: "return error when level2 is an empty string",
			args: args{
				home: &entity.Home{
					CountryCode: "JP",
					Level1:      "JP-13",
					Level2:      &level2Empty,
				},
			},
			wantErr: assert.AnError,
		},
		{
			name: "return error when level2 exceeds 20 characters",
			args: args{
				home: &entity.Home{
					CountryCode: "JP",
					Level1:      "JP-13",
					Level2:      &level2TooLong,
				},
			},
			wantErr: assert.AnError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := tt.args.home.Validate()

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
				return
			}

			assert.NoError(t, err)
		})
	}
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

func TestUser_HasPersonalDetails(t *testing.T) {
	t.Parallel()

	assert.True(t, (&entity.User{FullName: new("山田 花子"), PhoneNumber: new("+819012345678")}).HasPersonalDetails())
	assert.False(t, (&entity.User{FullName: new("山田 花子")}).HasPersonalDetails())
	assert.False(t, (&entity.User{}).HasPersonalDetails())
}

func TestNewUser_ValidateEmail(t *testing.T) {
	t.Parallel()

	const domain = "@example.com"
	email := func(n int) string { return strings.Repeat("a", n-len(domain)) + domain }

	assert.NoError(t, (&entity.NewUser{Email: email(165)}).ValidateEmail())
	assert.ErrorIs(t, (&entity.NewUser{Email: email(166)}).ValidateEmail(), apperr.ErrInvalidArgument)
}

func TestHome_ResolveCentroid(t *testing.T) {
	t.Parallel()

	for _, code := range []string{"JP-01", "JP-13", "JP-47"} {
		c, ok := (&entity.Home{CountryCode: "JP", Level1: code}).ResolveCentroid()
		assert.True(t, ok, code)
		assert.NotZero(t, c.Latitude, code)
		assert.NotZero(t, c.Longitude, code)
	}
	tokyo, _ := (&entity.Home{Level1: "JP-13"}).ResolveCentroid()
	assert.InDelta(t, 35.6894, tokyo.Latitude, 0.01)
	assert.InDelta(t, 139.6917, tokyo.Longitude, 0.01)

	for _, code := range []string{"US-NY", "", "XX-99"} {
		_, ok := (&entity.Home{Level1: code}).ResolveCentroid()
		assert.False(t, ok, "no centroid for %q", code)
	}
	for i := 1; i <= 47; i++ {
		_, ok := (&entity.Home{Level1: fmt.Sprintf("JP-%02d", i)}).ResolveCentroid()
		assert.True(t, ok, "every prefecture has one: JP-%02d", i)
	}
}

func TestUser_EmailLanguage(t *testing.T) {
	t.Parallel()
	for lang, want := range map[string]string{"en": "en", "ja": "ja", "": "ja", "fr": "ja", "EN": "ja"} {
		assert.Equal(t, want, (&entity.User{PreferredLanguage: lang}).EmailLanguage(), "preferred %q", lang)
	}
}
