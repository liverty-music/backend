package usecase_test

import (
	"context"
	"testing"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/entity/mocks"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestAreaUseCase_Resolve(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		text    string
		area    *entity.Area
		err     error
		wantErr error
	}{
		{
			// @spec components/usecase/area/resolve "Name of a state"
			name: "return US-CA with its country and zone for California",
			text: "California",
			area: &entity.Area{Code: "US-CA", Kind: entity.AreaKindSubdivision, CountryCode: "US", NameEN: "California", TimeZone: new("America/Los_Angeles")},
		},
		{
			// @spec components/usecase/area/resolve "Lower-level code"
			name: "return FR-NAQ for FR-33",
			text: "FR-33",
			area: &entity.Area{Code: "FR-NAQ", Kind: entity.AreaKindSubdivision, CountryCode: "FR", NameEN: "Nouvelle-Aquitaine"},
		},
		{
			// @spec components/usecase/area/resolve "Text that stands for no Area"
			name:    "return NOT_FOUND for JP-99 unchanged",
			text:    "JP-99",
			err:     apperr.New(codes.NotFound, "no area"),
			wantErr: apperr.ErrNotFound,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			areas := mocks.NewMockAreaRepository(t)
			areas.EXPECT().Resolve(mock.Anything, tt.text, (*string)(nil)).Return(tt.area, tt.err).Once()

			got, err := usecase.NewAreaUseCase(areas).Resolve(context.Background(), tt.text, nil)

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.area, got)
		})
	}
}

func TestAreaUseCase_ListByCountry(t *testing.T) {
	t.Parallel()

	// @spec components/usecase/area/list-by-country "Japan's areas"
	t.Run("return Japan's Areas, regions and zone as Area.ListByCountry gives them", func(t *testing.T) {
		t.Parallel()
		japan := &entity.CountryAreas{
			Areas:     []*entity.Area{{Code: "JP-13", Kind: entity.AreaKindSubdivision, CountryCode: "JP", NameEN: "Tokyo", RegionCodes: []string{"JP-KANTO"}}},
			Regions:   []*entity.Area{{Code: "JP-KANTO", Kind: entity.AreaKindRegion, CountryCode: "JP", NameEN: "Kanto"}},
			TimeZones: []*entity.AreaTimeZone{{Name: "Asia/Tokyo", NameEN: "Japan"}},
		}
		areas := mocks.NewMockAreaRepository(t)
		areas.EXPECT().ListByCountry(mock.Anything, "JP").Return(japan, nil).Once()

		got, err := usecase.NewAreaUseCase(areas).ListByCountry(context.Background(), "JP")

		require.NoError(t, err)
		assert.Same(t, japan, got)
	})

	// @spec components/usecase/area/list-by-country "Malformed country code"
	t.Run("return INVALID_ARGUMENT unchanged", func(t *testing.T) {
		t.Parallel()
		areas := mocks.NewMockAreaRepository(t)
		areas.EXPECT().ListByCountry(mock.Anything, "usa").Return(nil, apperr.New(codes.InvalidArgument, "bad code")).Once()

		_, err := usecase.NewAreaUseCase(areas).ListByCountry(context.Background(), "usa")

		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
	})
}

func TestAreaUseCase_ListCountries(t *testing.T) {
	t.Parallel()

	// @spec components/usecase/area/list-countries "Countries listed"
	t.Run("return the countries Area.ListCountries gives", func(t *testing.T) {
		t.Parallel()
		countries := []*entity.Country{{Code: "JP", NameEN: "Japan", NameJA: new("日本")}, {Code: "KR", NameEN: "South Korea", NameJA: new("韓国")}}
		areas := mocks.NewMockAreaRepository(t)
		areas.EXPECT().ListCountries(mock.Anything).Return(countries, nil).Once()

		got, err := usecase.NewAreaUseCase(areas).ListCountries(context.Background())

		require.NoError(t, err)
		assert.Equal(t, countries, got)
	})

	// @spec components/usecase/area/list-countries "Failure returned unchanged"
	t.Run("return UNAVAILABLE unchanged", func(t *testing.T) {
		t.Parallel()
		areas := mocks.NewMockAreaRepository(t)
		areas.EXPECT().ListCountries(mock.Anything).Return(nil, apperr.New(codes.Unavailable, "db down")).Once()

		_, err := usecase.NewAreaUseCase(areas).ListCountries(context.Background())

		assert.ErrorIs(t, err, apperr.ErrUnavailable)
	})
}
