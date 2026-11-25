package usecase_test

import (
	"context"
	"strings"
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

func TestVenueUseCase_Search(t *testing.T) {
	t.Parallel()

	liquidroom := []*entity.MapPlaceCandidate{
		{PlaceID: "ChIJliquidroom", Name: "LIQUIDROOM", Address: "日本、〒150-0011 東京都渋谷区東３丁目１６−６"},
		{PlaceID: "ChIJliquidroom-2", Name: "LIQUIDROOM 2F", Address: "日本、〒150-0011 東京都渋谷区東３丁目１６−６ 2F"},
	}

	type args struct {
		text string
	}
	tests := []struct {
		name    string
		args    args
		setup   func(d *authoringDeps)
		want    []*entity.MapPlaceCandidate
		wantErr error
	}{
		{
			// @spec components/usecase/venue/search "Member searches a venue"
			name: "return the catalog's candidates unchanged and store no venue",
			args: args{text: "LIQUIDROOM"},
			setup: func(d *authoringDeps) {
				// No repository has an expectation: storing anything fails the test.
				d.mapCatalog.EXPECT().SearchPlaces(mock.Anything, "LIQUIDROOM").Return(liquidroom, nil).Once()
			},
			want: liquidroom,
		},
		{
			name: "search the text with surrounding spaces trimmed",
			args: args{text: "　 LIQUIDROOM  "},
			setup: func(d *authoringDeps) {
				d.mapCatalog.EXPECT().SearchPlaces(mock.Anything, "LIQUIDROOM").Return(liquidroom, nil).Once()
			},
			want: liquidroom,
		},
		{
			// @spec components/usecase/venue/search "Text too short"
			name:    "fail with INVALID_ARGUMENT without calling the catalog when the text is one character",
			args:    args{text: "Z"},
			wantErr: apperr.ErrInvalidArgument,
		},
		{
			name:    "fail with INVALID_ARGUMENT when the text is one character after trimming",
			args:    args{text: "  Z  "},
			wantErr: apperr.ErrInvalidArgument,
		},
		{
			name:    "fail with INVALID_ARGUMENT when the text is longer than 100 characters",
			args:    args{text: strings.Repeat("あ", 101)},
			wantErr: apperr.ErrInvalidArgument,
		},
		{
			name: "accept a text of exactly 100 characters",
			args: args{text: strings.Repeat("あ", 100)},
			setup: func(d *authoringDeps) {
				d.mapCatalog.EXPECT().SearchPlaces(mock.Anything, strings.Repeat("あ", 100)).Return([]*entity.MapPlaceCandidate{}, nil).Once()
			},
			want: []*entity.MapPlaceCandidate{},
		},
		{
			// @spec components/usecase/venue/search "Catalog down"
			name: "return UNAVAILABLE when the catalog cannot be reached",
			args: args{text: "LIQUIDROOM"},
			setup: func(d *authoringDeps) {
				d.mapCatalog.EXPECT().SearchPlaces(mock.Anything, "LIQUIDROOM").
					Return(nil, apperr.New(codes.Unavailable, "google maps places search failed")).Once()
			},
			wantErr: apperr.ErrUnavailable,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			d := newAuthoringDeps(t)
			if tt.setup != nil {
				tt.setup(d)
			}

			got, err := usecase.NewVenueUseCase(d.mapCatalog, d.venueRepo, nil).Search(context.Background(), authoringOrgID, tt.args.text)

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				assert.Nil(t, got)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestVenueUseCase_ResolvePlace(t *testing.T) {
	t.Parallel()

	const gocheok = "ChIJgocheok"
	notFound := apperr.New(codes.NotFound, "venue not found")
	korea := &entity.CountryAreas{
		Areas:     []*entity.Area{{Code: "KR-11", Kind: entity.AreaKindSubdivision, CountryCode: "KR", NameEN: "Seoul", TimeZone: new("Asia/Seoul")}},
		TimeZones: []*entity.AreaTimeZone{{Name: "Asia/Seoul", NameEN: "Seoul"}},
	}
	us := &entity.CountryAreas{
		Areas: []*entity.Area{{Code: "US-TX", Kind: entity.AreaKindSubdivision, CountryCode: "US", NameEN: "Texas"}},
		TimeZones: []*entity.AreaTimeZone{
			{Name: "America/Chicago", NameEN: "Chicago"}, {Name: "America/Denver", NameEN: "Denver"},
		},
	}
	place := func(country string, area, zone *string) *entity.MapPlace {
		return &entity.MapPlace{PlaceID: gocheok, Name: "Gocheok Sky Dome", CountryCode: country, SuggestedAdminArea: area, TimeZone: zone}
	}

	tests := []struct {
		name    string
		placeID string
		// setup sets the expectations; a call without one fails the test,
		// which is how "the catalog is not read" and "nothing is read" are
		// asserted. Nothing is stored: the mocks expect no write.
		setup   func(d *authoringDeps, areas *mocks.MockAreaRepository)
		want    *usecase.ResolvedPlace
		wantErr error
	}{
		{
			// @spec components/usecase/venue/resolve-place "Place already a Venue"
			name:    "return the Venue holding the place id without reading the catalog",
			placeID: zeppHanedaPlaceID,
			setup: func(d *authoringDeps, _ *mocks.MockAreaRepository) {
				d.venueRepo.EXPECT().GetByPlaceID(mock.Anything, zeppHanedaPlaceID).Return(&entity.Venue{
					ID: existingVenueID, Name: "Zepp Haneda", AdminArea: new("JP-13"), TimeZone: new("Asia/Tokyo"),
				}, nil).Once()
			},
			want: &usecase.ResolvedPlace{Venue: &entity.Venue{
				ID: existingVenueID, Name: "Zepp Haneda", AdminArea: new("JP-13"), TimeZone: new("Asia/Tokyo"),
			}},
		},
		{
			// @spec components/usecase/venue/resolve-place "New place in an area with one time zone"
			name:    "return a draft for Gocheok Sky Dome in KR-11 with the Area's zone Asia/Seoul",
			placeID: gocheok,
			setup: func(d *authoringDeps, areas *mocks.MockAreaRepository) {
				d.venueRepo.EXPECT().GetByPlaceID(mock.Anything, gocheok).Return(nil, notFound).Once()
				d.mapCatalog.EXPECT().GetPlace(mock.Anything, gocheok).Return(place("KR", new("KR-11"), nil), nil).Once()
				areas.EXPECT().ListByCountry(mock.Anything, "KR").Return(korea, nil).Once()
			},
			want: &usecase.ResolvedPlace{Draft: &usecase.PlaceDraft{
				Name: "Gocheok Sky Dome", CountryCode: "KR", SuggestedAdminArea: new("KR-11"), TimeZone: new("Asia/Seoul"),
			}},
		},
		{
			// @spec components/usecase/venue/resolve-place "Area without a single time zone"
			name:    "take the catalog's zone America/Chicago for a place in US-TX",
			placeID: gocheok,
			setup: func(d *authoringDeps, areas *mocks.MockAreaRepository) {
				d.venueRepo.EXPECT().GetByPlaceID(mock.Anything, gocheok).Return(nil, notFound).Once()
				d.mapCatalog.EXPECT().GetPlace(mock.Anything, gocheok).
					Return(place("US", new("US-TX"), new("America/Chicago")), nil).Once()
				areas.EXPECT().ListByCountry(mock.Anything, "US").Return(us, nil).Once()
			},
			want: &usecase.ResolvedPlace{Draft: &usecase.PlaceDraft{
				Name: "Gocheok Sky Dome", CountryCode: "US", SuggestedAdminArea: new("US-TX"), TimeZone: new("America/Chicago"),
			}},
		},
		{
			// @spec components/usecase/venue/resolve-place "Catalog time zone of another country"
			name:    "carry no zone when the catalog's zone is not one of the country's",
			placeID: gocheok,
			setup: func(d *authoringDeps, areas *mocks.MockAreaRepository) {
				d.venueRepo.EXPECT().GetByPlaceID(mock.Anything, gocheok).Return(nil, notFound).Once()
				d.mapCatalog.EXPECT().GetPlace(mock.Anything, gocheok).
					Return(place("US", new("US-TX"), new("Asia/Tokyo")), nil).Once()
				areas.EXPECT().ListByCountry(mock.Anything, "US").Return(us, nil).Once()
			},
			want: &usecase.ResolvedPlace{Draft: &usecase.PlaceDraft{
				Name: "Gocheok Sky Dome", CountryCode: "US", SuggestedAdminArea: new("US-TX"),
			}},
		},
		{
			// @spec components/usecase/venue/resolve-place "Place without a suggested area"
			name:    "carry no suggested area when the place has none",
			placeID: gocheok,
			setup: func(d *authoringDeps, areas *mocks.MockAreaRepository) {
				d.venueRepo.EXPECT().GetByPlaceID(mock.Anything, gocheok).Return(nil, notFound).Once()
				d.mapCatalog.EXPECT().GetPlace(mock.Anything, gocheok).
					Return(place("US", nil, new("America/Denver")), nil).Once()
				areas.EXPECT().ListByCountry(mock.Anything, "US").Return(us, nil).Once()
			},
			want: &usecase.ResolvedPlace{Draft: &usecase.PlaceDraft{
				Name: "Gocheok Sky Dome", CountryCode: "US", TimeZone: new("America/Denver"),
			}},
		},
		{
			// @spec components/usecase/venue/resolve-place "Empty place id"
			name:    "fail with INVALID_ARGUMENT and read nothing for an empty place id",
			placeID: " ",
			wantErr: apperr.ErrInvalidArgument,
		},
		{
			// @spec components/usecase/venue/resolve-place "Unknown place id"
			name:    "return NOT_FOUND from the catalog unchanged",
			placeID: gocheok,
			setup: func(d *authoringDeps, _ *mocks.MockAreaRepository) {
				d.venueRepo.EXPECT().GetByPlaceID(mock.Anything, gocheok).Return(nil, notFound).Once()
				d.mapCatalog.EXPECT().GetPlace(mock.Anything, gocheok).
					Return(nil, apperr.New(codes.NotFound, "no place with this id")).Once()
			},
			wantErr: apperr.ErrNotFound,
		},
		{
			// @spec components/usecase/venue/resolve-place "Catalog down"
			name:    "return UNAVAILABLE from the catalog unchanged",
			placeID: gocheok,
			setup: func(d *authoringDeps, _ *mocks.MockAreaRepository) {
				d.venueRepo.EXPECT().GetByPlaceID(mock.Anything, gocheok).Return(nil, notFound).Once()
				d.mapCatalog.EXPECT().GetPlace(mock.Anything, gocheok).
					Return(nil, apperr.New(codes.Unavailable, "catalog down")).Once()
			},
			wantErr: apperr.ErrUnavailable,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			d := newAuthoringDeps(t)
			areas := mocks.NewMockAreaRepository(t)
			if tt.setup != nil {
				tt.setup(d, areas)
			}

			got, err := usecase.NewVenueUseCase(d.mapCatalog, d.venueRepo, areas).ResolvePlace(context.Background(), authoringOrgID, tt.placeID)

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				assert.Nil(t, got)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}
