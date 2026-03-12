package entity_test

import (
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
			Country:           "US",
			TimeZone:          "America/New_York",
		}

		got := entity.CreateUser(params)

		assert.NotEmpty(t, got.ID)
		assert.Equal(t, params.ExternalID, got.ExternalID)
		assert.Equal(t, params.Email, got.Email)
		assert.Equal(t, params.Name, got.Name)
		assert.Equal(t, params.PreferredLanguage, got.PreferredLanguage)
		assert.Equal(t, params.Country, got.Country)
		assert.Equal(t, params.TimeZone, got.TimeZone)
		assert.True(t, got.IsActive)
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
