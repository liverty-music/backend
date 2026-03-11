package entity_test

import (
	"testing"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/stretchr/testify/assert"
)

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
