package rpc_test

import (
	"context"
	"strings"
	"testing"

	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	adminareav1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/admin/area/v1"
	"connectrpc.com/connect/v2"
	"github.com/liverty-music/backend/internal/adapter/rpc"
	"github.com/liverty-music/backend/internal/entity"
	ucmocks "github.com/liverty-music/backend/internal/usecase/mocks"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// adminResolve calls Resolve through the admin server's interceptors.
func adminResolve(t *testing.T, ctx context.Context, h *rpc.AdminAreaHandler, req *adminareav1.ResolveRequest) (*adminareav1.ResolveResponse, error) {
	t.Helper()
	return callThroughAdminPipeline(t, ctx, req, h.Resolve)
}

// adminListByCountry calls ListByCountry through the admin server's interceptors.
func adminListByCountry(t *testing.T, ctx context.Context, h *rpc.AdminAreaHandler, country string) (*adminareav1.ListByCountryResponse, error) {
	t.Helper()
	return callThroughAdminPipeline(t, ctx, &adminareav1.ListByCountryRequest{CountryCode: &entityv1.CountryCode{Value: country}}, h.ListByCountry)
}

func TestAdminAreaHandler_Resolve(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		req       *adminareav1.ResolveRequest
		country   *string
		area      *entity.Area
		ucErr     error
		wantArea  string
		wantNoRun bool
		wantCode  connect.Code
	}{
		{
			// @spec components/adapter/admin/api/rpc/area "Admin enters a lower-level code"
			name:     "return FR-NAQ with country FR for FR-33",
			req:      &adminareav1.ResolveRequest{Area: &entityv1.AreaQuery{Value: "FR-33"}},
			area:     &entity.Area{Code: "FR-NAQ", Kind: entity.AreaKindSubdivision, CountryCode: "FR", NameEN: "Nouvelle-Aquitaine"},
			wantArea: "FR-NAQ",
		},
		{
			// @spec components/adapter/admin/api/rpc/area "Admin enters a name with a country"
			name:     "return AU-VIC for Victoria in AU",
			req:      &adminareav1.ResolveRequest{Area: &entityv1.AreaQuery{Value: "Victoria"}, CountryCode: &entityv1.CountryCode{Value: "AU"}},
			country:  new("AU"),
			area:     &entity.Area{Code: "AU-VIC", Kind: entity.AreaKindSubdivision, CountryCode: "AU", NameEN: "Victoria", TimeZone: new("Australia/Melbourne")},
			wantArea: "AU-VIC",
		},
		{
			// @spec components/adapter/admin/api/rpc/area "Ambiguous name"
			name:     "return NOT_FOUND for Victoria without a country",
			req:      &adminareav1.ResolveRequest{Area: &entityv1.AreaQuery{Value: "Victoria"}},
			ucErr:    apperr.New(codes.NotFound, "ambiguous"),
			wantCode: connect.CodeNotFound,
		},
		{
			// @spec components/adapter/admin/api/rpc/area "Empty area text"
			name:      "return INVALID_ARGUMENT before any usecase runs without an area text",
			req:       &adminareav1.ResolveRequest{},
			wantNoRun: true,
			wantCode:  connect.CodeInvalidArgument,
		},
		{
			name:      "return INVALID_ARGUMENT before any usecase runs for an area text over 100 characters",
			req:       &adminareav1.ResolveRequest{Area: &entityv1.AreaQuery{Value: strings.Repeat("a", 101)}},
			wantNoRun: true,
			wantCode:  connect.CodeInvalidArgument,
		},
		{
			name:      "return INVALID_ARGUMENT before any usecase runs for a malformed country code",
			req:       &adminareav1.ResolveRequest{Area: &entityv1.AreaQuery{Value: "Victoria"}, CountryCode: &entityv1.CountryCode{Value: "aus"}},
			wantNoRun: true,
			wantCode:  connect.CodeInvalidArgument,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			areaUC := ucmocks.NewMockAreaUseCase(t)
			if !tt.wantNoRun {
				areaUC.EXPECT().Resolve(mock.Anything, tt.req.GetArea().GetValue(), tt.country).Return(tt.area, tt.ucErr).Once()
			}

			resp, err := adminResolve(t, adminCtx(), rpc.NewAdminAreaHandler(areaUC), tt.req)

			if tt.wantCode != 0 {
				assert.Equal(t, tt.wantCode, connect.CodeOf(err))
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantArea, resp.GetArea().GetCode().GetValue())
			assert.Equal(t, tt.area.CountryCode, resp.GetArea().GetCountryCode().GetValue())
		})
	}
}

func TestAdminAreaHandler_ListByCountry(t *testing.T) {
	t.Parallel()

	// @spec components/adapter/admin/api/rpc/area "Admin lists a country's time zones"
	// @spec components/adapter/admin/api/rpc/area "Country with several zones"
	t.Run("return the zones of US with their names and US-TX without a zone", func(t *testing.T) {
		t.Parallel()
		areaUC := ucmocks.NewMockAreaUseCase(t)
		areaUC.EXPECT().ListByCountry(mock.Anything, "US").Return(&entity.CountryAreas{
			Areas: []*entity.Area{{Code: "US-TX", Kind: entity.AreaKindSubdivision, CountryCode: "US", NameEN: "Texas", NameJA: new("テキサス州")}},
			TimeZones: []*entity.AreaTimeZone{
				{Name: "America/Chicago", NameEN: "Chicago", NameJA: new("シカゴ")},
				{Name: "America/Los_Angeles", NameEN: "Los Angeles", NameJA: new("ロサンゼルス")},
			},
		}, nil).Once()

		resp, err := adminListByCountry(t, adminCtx(), rpc.NewAdminAreaHandler(areaUC), "US")

		require.NoError(t, err)
		require.Len(t, resp.GetAreas(), 1)
		assert.False(t, resp.GetAreas()[0].HasTimeZone())
		zones := map[string]string{}
		for _, z := range resp.GetTimeZones() {
			zones[z.GetTimeZone().GetValue()] = z.GetNameJa()
		}
		assert.Equal(t, map[string]string{"America/Chicago": "シカゴ", "America/Los_Angeles": "ロサンゼルス"}, zones)
	})

	// @spec components/adapter/admin/api/rpc/area "Malformed country code"
	t.Run("return INVALID_ARGUMENT before any usecase runs for usa", func(t *testing.T) {
		t.Parallel()
		_, err := adminListByCountry(t, adminCtx(), rpc.NewAdminAreaHandler(ucmocks.NewMockAreaUseCase(t)), "usa")

		assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
	})
}

func TestAdminAreaHandler_Role(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		ctx  context.Context
		want connect.Code
	}{
		// @spec components/adapter/admin/api/rpc/area "Signed-in caller without the admin role"
		{"return PERMISSION_DENIED for a signed-in caller without the admin role", nonAdminCtx(), connect.CodePermissionDenied},
		// @spec components/adapter/admin/api/rpc/area "Not signed in"
		{"return UNAUTHENTICATED for a caller who is not signed in", context.Background(), connect.CodeUnauthenticated},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			// No usecase has an expectation: reaching one fails the test.
			h := rpc.NewAdminAreaHandler(ucmocks.NewMockAreaUseCase(t))

			_, err := adminResolve(t, tt.ctx, h, &adminareav1.ResolveRequest{Area: &entityv1.AreaQuery{Value: "FR-33"}})
			assert.Equal(t, tt.want, connect.CodeOf(err), "Resolve")
			_, err = adminListByCountry(t, tt.ctx, h, "US")
			assert.Equal(t, tt.want, connect.CodeOf(err), "ListByCountry")
		})
	}
}
