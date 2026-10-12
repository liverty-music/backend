package usecase_test

import (
	"context"
	"strings"
	"testing"

	"github.com/liverty-music/backend/internal/entity"
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
			// @spec components/usecase/venue/search "Operator searches a venue"
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

			got, err := usecase.NewVenueUseCase(d.mapCatalog).Search(context.Background(), authoringOrgID, tt.args.text)

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
