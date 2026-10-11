package entity_test

import (
	"strings"
	"testing"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/stretchr/testify/assert"
)

func TestCreateUser(t *testing.T) {
	t.Parallel()

	t.Run("set all fields from params and generate UUIDv7 ID", func(t *testing.T) {
		t.Parallel()

		params := &entity.NewUser{
			ExternalID:        "ext-123",
			Email:             "test@example.com",
			Name:              "Test User",
			PreferredLanguage: "en",
			TimeZone:          "America/New_York",
		}

		got := entity.CreateUser(params)

		assert.NotEmpty(t, got.ID)
		assert.Equal(t, params.ExternalID, got.ExternalID)
		assert.Equal(t, params.Email, got.Email)
		assert.Equal(t, params.Name, got.Name)
		assert.Equal(t, params.PreferredLanguage, got.PreferredLanguage)
		assert.Equal(t, params.TimeZone, got.TimeZone)
	})

	t.Run("generate different IDs on successive calls", func(t *testing.T) {
		t.Parallel()

		params := &entity.NewUser{
			ExternalID: "ext-456",
			Email:      "a@example.com",
			Name:       "User A",
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
				assert.Error(t, err)
				return
			}

			assert.NoError(t, err)
		})
	}
}

func TestValidatePersonalDetails(t *testing.T) {
	t.Parallel()

	type args struct {
		fullName    string
		phoneNumber string
	}
	tests := []struct {
		name  string
		args  args
		valid bool
	}{
		{
			// @spec components/entity/user "Details complete"
			name:  "details complete",
			args:  args{fullName: "山田 花子", phoneNumber: "+819012345678"},
			valid: true,
		},
		{
			// @spec components/entity/user "Domestic-format phone number"
			name: "domestic format with hyphens",
			args: args{fullName: "山田 花子", phoneNumber: "090-1234-5678"},
		},
		{
			name: "domestic format without hyphens",
			args: args{fullName: "山田 花子", phoneNumber: "09012345678"},
		},
		{
			// @spec components/entity/user "Phone number too long"
			name: "phone number too long",
			args: args{fullName: "山田 花子", phoneNumber: "+" + strings.Repeat("1", 16)},
		},
		{
			name:  "phone number of 15 digits",
			args:  args{fullName: "山田 花子", phoneNumber: "+" + strings.Repeat("1", 15)},
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
			args:  args{fullName: strings.Repeat("名", 200), phoneNumber: "+819012345678"},
			valid: true,
		},
		{
			name: "name of 201 characters",
			args: args{fullName: strings.Repeat("名", 201), phoneNumber: "+819012345678"},
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
			assert.Error(t, err)
		})
	}
}

func TestUser_HasPersonalDetails(t *testing.T) {
	t.Parallel()

	assert.True(t, (&entity.User{FullName: "山田 花子", PhoneNumber: "+819012345678"}).HasPersonalDetails())
	assert.False(t, (&entity.User{FullName: "山田 花子"}).HasPersonalDetails())
	assert.False(t, (&entity.User{}).HasPersonalDetails())
}
