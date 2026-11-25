package rpc_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	organizerareav1connect "buf.build/gen/go/liverty-music/schema/connectrpc/go/v2/liverty_music/rpc/organizer/area/v1/areav1connect"
	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	organizerareav1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/organizer/area/v1"
	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"connectrpc.com/validate"
	"github.com/liverty-music/backend/internal/adapter/rpc"
	"github.com/liverty-music/backend/internal/entity"
	ucmocks "github.com/liverty-music/backend/internal/usecase/mocks"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// japanAreas is Japan's Areas as Area.ListByCountry gives them, cut to Tokyo.
func japanAreas() *entity.CountryAreas {
	return &entity.CountryAreas{
		Areas: []*entity.Area{{
			Code: "JP-13", Kind: entity.AreaKindSubdivision, CountryCode: "JP", NameEN: "Tokyo", NameJA: new("東京都"),
			TimeZone: new("Asia/Tokyo"), RegionCodes: []string{"JP-KANTO"},
		}},
		Regions:   []*entity.Area{{Code: "JP-KANTO", Kind: entity.AreaKindRegion, CountryCode: "JP", NameEN: "Kanto", NameJA: new("関東")}},
		TimeZones: []*entity.AreaTimeZone{{Name: "Asia/Tokyo", NameEN: "Japan", NameJA: new("日本")}},
	}
}

func newOrganizerAreaHandler(t *testing.T) (*rpc.OrganizerAreaHandler, *ucmocks.MockAreaUseCase, *ucmocks.MockOrganizerUseCase) {
	t.Helper()
	areaUC := ucmocks.NewMockAreaUseCase(t)
	organizerUC := ucmocks.NewMockOrganizerUseCase(t)
	return rpc.NewOrganizerAreaHandler(areaUC, organizerUC), areaUC, organizerUC
}

func TestOrganizerAreaHandler_ListByCountry(t *testing.T) {
	t.Parallel()

	// @spec components/adapter/organizer/api/rpc/area "Areas for an active Organizer"
	// @spec components/adapter/organizer/api/rpc/area "Prefecture with its region and zone"
	t.Run("return JP-13 with its names, zone and region, and Asia/Tokyo with its names", func(t *testing.T) {
		t.Parallel()
		h, areaUC, organizerUC := newOrganizerAreaHandler(t)
		organizerUC.EXPECT().ResolveCaller(mock.Anything, testProviderOrganizerID).Return(activeOrganizer(), nil).Once()
		areaUC.EXPECT().ListByCountry(mock.Anything, "JP").Return(japanAreas(), nil).Once()

		resp, err := h.ListByCountry(orgCtx(testProviderOrganizerID), &organizerareav1.ListByCountryRequest{
			CountryCode: &entityv1.CountryCode{Value: "JP"},
		})

		require.NoError(t, err)
		require.Len(t, resp.GetAreas(), 1)
		tokyo := resp.GetAreas()[0]
		assert.Equal(t, "JP-13", tokyo.GetCode().GetValue())
		assert.Equal(t, entityv1.AreaKind_AREA_KIND_SUBDIVISION, tokyo.GetKind())
		assert.Equal(t, "Tokyo", tokyo.GetNameEn())
		assert.Equal(t, "東京都", tokyo.GetNameJa())
		assert.Equal(t, "Asia/Tokyo", tokyo.GetTimeZone().GetValue())
		require.Len(t, tokyo.GetRegionCodes(), 1)
		assert.Equal(t, "JP-KANTO", tokyo.GetRegionCodes()[0].GetValue())
		require.Len(t, resp.GetRegions(), 1)
		assert.Equal(t, "関東", resp.GetRegions()[0].GetNameJa())
		require.Len(t, resp.GetTimeZones(), 1)
		assert.Equal(t, "Asia/Tokyo", resp.GetTimeZones()[0].GetTimeZone().GetValue())
		assert.Equal(t, "日本", resp.GetTimeZones()[0].GetNameJa())
		assert.Equal(t, "Japan", resp.GetTimeZones()[0].GetNameEn())
	})

	// @spec components/adapter/organizer/api/rpc/area "Missing country code"
	t.Run("return INVALID_ARGUMENT before any usecase runs without a country code", func(t *testing.T) {
		t.Parallel()
		// No usecase has an expectation: reaching one fails the test.
		h, _, _ := newOrganizerAreaHandler(t)
		server := connect.NewServer(validate.NewServerInterceptor())
		organizerareav1connect.RegisterAreaServiceHandler(server, h)
		mux := http.NewServeMux()
		connecthttp.Mount(mux, server)
		srv := httptest.NewServer(mux)
		t.Cleanup(srv.Close)
		client := organizerareav1connect.NewAreaServiceClient(connect.NewClient(connecthttp.NewTransport(srv.Client(), srv.URL)))

		_, err := client.ListByCountry(context.Background(), &organizerareav1.ListByCountryRequest{})

		assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
	})
}

func TestOrganizerAreaHandler_ListCountries(t *testing.T) {
	t.Parallel()

	// @spec components/adapter/organizer/api/rpc/area "Countries for an active Organizer"
	// @spec components/adapter/organizer/api/rpc/area "Country names"
	t.Run("return the countries with KR named South Korea and 韓国", func(t *testing.T) {
		t.Parallel()
		h, areaUC, organizerUC := newOrganizerAreaHandler(t)
		organizerUC.EXPECT().ResolveCaller(mock.Anything, testProviderOrganizerID).Return(activeOrganizer(), nil).Once()
		areaUC.EXPECT().ListCountries(mock.Anything).Return([]*entity.Country{
			{Code: "JP", NameEN: "Japan", NameJA: new("日本")},
			{Code: "KR", NameEN: "South Korea", NameJA: new("韓国")},
		}, nil).Once()

		resp, err := h.ListCountries(orgCtx(testProviderOrganizerID), &organizerareav1.ListCountriesRequest{})

		require.NoError(t, err)
		require.Len(t, resp.GetCountries(), 2)
		kr := resp.GetCountries()[1]
		assert.Equal(t, "KR", kr.GetCode().GetValue())
		assert.Equal(t, "South Korea", kr.GetNameEn())
		assert.Equal(t, "韓国", kr.GetNameJa())
	})
}

// @spec components/adapter/organizer/api/rpc/area "Areas without an Organizer"
func TestOrganizerAreaHandler_ResolveCallerFailure(t *testing.T) {
	t.Parallel()

	resolveErr := apperr.New(apperr.ErrPermissionDenied.Code, "permission denied")
	calls := map[string]func(h *rpc.OrganizerAreaHandler) error{
		"ListByCountry": func(h *rpc.OrganizerAreaHandler) error {
			_, err := h.ListByCountry(orgCtx(testProviderOrganizerID), &organizerareav1.ListByCountryRequest{CountryCode: &entityv1.CountryCode{Value: "JP"}})
			return err
		},
		"ListCountries": func(h *rpc.OrganizerAreaHandler) error {
			_, err := h.ListCountries(orgCtx(testProviderOrganizerID), &organizerareav1.ListCountriesRequest{})
			return err
		},
	}
	for name, call := range calls {
		t.Run(name+" returns PERMISSION_DENIED and reads no Area when no organizer is linked", func(t *testing.T) {
			t.Parallel()
			// AreaUseCase has no expectation: reaching it fails the test.
			h, _, organizerUC := newOrganizerAreaHandler(t)
			organizerUC.EXPECT().ResolveCaller(mock.Anything, testProviderOrganizerID).Return(nil, resolveErr).Once()

			err := call(h)

			assert.ErrorIs(t, err, resolveErr)
			assert.Equal(t, connect.CodePermissionDenied, connectCodeOf(err))
		})
	}
}
