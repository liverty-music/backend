package server_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	adminorganizerv1connect "buf.build/gen/go/liverty-music/schema/connectrpc/go/liverty_music/rpc/admin/organizer/v1/organizerv1connect"
	ticketsalev1connect "buf.build/gen/go/liverty-music/schema/connectrpc/go/liverty_music/rpc/ticket_sale/v1/ticket_salev1connect"
	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	adminorganizerv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/admin/organizer/v1"
	ticketsalev1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/ticket_sale/v1"
	"connectrpc.com/connect"

	"github.com/liverty-music/backend/internal/adapter/rpc"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/auth"
	authmocks "github.com/liverty-music/backend/internal/infrastructure/auth/mocks"
	"github.com/liverty-music/backend/internal/infrastructure/server"
	"github.com/liverty-music/backend/internal/infrastructure/server/ratelimit"
	"github.com/liverty-music/backend/internal/usecase"
	usecasemocks "github.com/liverty-music/backend/internal/usecase/mocks"
	"github.com/liverty-music/backend/pkg/config"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

const saleEventID = "019a0000-0000-7000-8000-0000000000e1"

// newTestFanTicketSaleServer builds a fan Connect server with the fan public
// procedures, as provider.go does, serving TicketSaleService.
func newTestFanTicketSaleServer(t *testing.T, ticketSaleUC *usecasemocks.MockTicketSaleUseCase) *httptest.Server {
	t.Helper()
	logger, err := logging.New()
	require.NoError(t, err)
	validator := authmocks.NewMockTokenValidator(t)
	rateLimiter := ratelimit.NewLimiter(ratelimit.Config{AuthRPS: 100, AuthBurst: 100, AnonRPS: 100, AnonBurst: 100}, time.Minute)
	t.Cleanup(func() { _ = rateLimiter.Close() })
	healthHandler := func(_ ...connect.HandlerOption) (string, http.Handler) {
		return "/unused-health-check/", http.NotFoundHandler()
	}
	handlers := []server.RPCHandlerFunc{
		func(opts ...connect.HandlerOption) (string, http.Handler) {
			return ticketsalev1connect.NewTicketSaleServiceHandler(rpc.NewTicketSaleHandler(ticketSaleUC, logger), opts...)
		},
	}
	cfg := config.ServerSettings{
		Host: "127.0.0.1", HandlerTimeout: 5 * time.Second, ReadHeaderTimeout: 2 * time.Second,
		ReadTimeout: 2 * time.Second, IdleTimeout: 5 * time.Second,
	}
	srv := server.NewConnectServer(cfg, logger, auth.NewAuthFunc(validator, auth.FanPublicProcedures()), rateLimiter, healthHandler,
		nil, nil, nil, handlers...)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts
}

func TestFanServer_TicketSaleGet(t *testing.T) {
	t.Parallel()

	t.Run("guest opens the event page", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/fan/api/rpc/ticket-sale "Guest opens the event page"
		uc := usecasemocks.NewMockTicketSaleUseCase(t)
		sale := &entity.TicketSale{
			ID: "019a0000-0000-7000-8000-0000000000e2", EventID: saleEventID, Method: entity.TicketSaleMethodFirstCome,
			SaleStartTime: time.Date(2026, 11, 1, 1, 0, 0, 0, time.UTC), SaleEndTime: time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC),
			Price: 3000, Quantity: 150, PerAccountLimit: 4, SoldCount: 135,
		}
		uc.EXPECT().Get(mock.Anything, saleEventID).Return(&usecase.TicketSaleView{Sale: sale, State: entity.TicketSaleStateOnSale, LowStock: true}, nil)
		ts := newTestFanTicketSaleServer(t, uc)

		// No Authorization header: the procedure is public.
		resp, err := ticketsalev1connect.NewTicketSaleServiceClient(ts.Client(), ts.URL).Get(context.Background(),
			connect.NewRequest(&ticketsalev1.GetRequest{EventId: &entityv1.EventId{Value: saleEventID}}))

		require.NoError(t, err)
		assert.Equal(t, entityv1.TicketSaleState_TICKET_SALE_STATE_ON_SALE, resp.Msg.State)
		assert.True(t, resp.Msg.LowStock)
		assert.Equal(t, int64(3000), resp.Msg.TicketSale.Price)
		assert.Nil(t, resp.Msg.TicketSale.Quantity, "the quantity is never shown to fans")
		assert.Nil(t, resp.Msg.TicketSale.SoldCount, "the sold count is never shown to fans")
	})

	t.Run("missing event", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/fan/api/rpc/ticket-sale "Missing event"
		ts := newTestFanTicketSaleServer(t, usecasemocks.NewMockTicketSaleUseCase(t))

		_, err := ticketsalev1connect.NewTicketSaleServiceClient(ts.Client(), ts.URL).Get(context.Background(),
			connect.NewRequest(&ticketsalev1.GetRequest{}))

		assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
	})
}

func TestAdminServer_OrganizerSellerDetailsAndFeeRate(t *testing.T) {
	t.Parallel()
	details := &entityv1.SellerDetails{
		LegalName: "株式会社リバティ", RepresentativeName: "代表 太郎", Address: "東京都渋谷区1-2-3",
		PhoneNumber: "+81312345678", ContactEmail: "contact@example.com",
	}
	rate := int32(500)

	call := func(t *testing.T, ts *httptest.Server, token string, fn func(c adminorganizerv1connect.OrganizerServiceClient, h http.Header) error) error {
		t.Helper()
		h := http.Header{}
		h.Set("Authorization", "Bearer "+token)
		return fn(adminorganizerv1connect.NewOrganizerServiceClient(ts.Client(), ts.URL), h)
	}
	setRate := func(organizerID *entityv1.OrganizerId, r *int32) func(c adminorganizerv1connect.OrganizerServiceClient, h http.Header) error {
		return func(c adminorganizerv1connect.OrganizerServiceClient, h http.Header) error {
			req := connect.NewRequest(&adminorganizerv1.SetPlatformFeeRateRequest{OrganizerId: organizerID, PlatformFeeRateBps: r})
			req.Header().Set("Authorization", h.Get("Authorization"))
			_, err := c.SetPlatformFeeRate(context.Background(), req)
			return err
		}
	}

	t.Run("non-admin sets a rate", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/admin/api/rpc/organizer "Non-admin sets a rate"
		ts := newTestAdminDeleteServer(t, usecasemocks.NewMockOrganizerUseCase(t), usecasemocks.NewMockUserUseCase(t))

		err := call(t, ts, deleteNonAdminToken, setRate(&entityv1.OrganizerId{Value: deleteOrganizerID}, &rate))

		assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
	})

	t.Run("missing organizer id", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/admin/api/rpc/organizer "Missing OrganizerId"
		ts := newTestAdminDeleteServer(t, usecasemocks.NewMockOrganizerUseCase(t), usecasemocks.NewMockUserUseCase(t))

		err := call(t, ts, deleteAdminToken, setRate(nil, &rate))

		assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
	})

	t.Run("missing rate", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/admin/api/rpc/organizer "Missing rate"
		ts := newTestAdminDeleteServer(t, usecasemocks.NewMockOrganizerUseCase(t), usecasemocks.NewMockUserUseCase(t))

		err := call(t, ts, deleteAdminToken, setRate(&entityv1.OrganizerId{Value: deleteOrganizerID}, nil))

		assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
	})

	t.Run("rate over the bound", func(t *testing.T) {
		t.Parallel()
		ts := newTestAdminDeleteServer(t, usecasemocks.NewMockOrganizerUseCase(t), usecasemocks.NewMockUserUseCase(t))
		over := int32(3100)

		err := call(t, ts, deleteAdminToken, setRate(&entityv1.OrganizerId{Value: deleteOrganizerID}, &over))

		assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
	})

	t.Run("admin sets a zero rate", func(t *testing.T) {
		t.Parallel()
		uc := usecasemocks.NewMockOrganizerUseCase(t)
		uc.EXPECT().SetPlatformFeeRate(mock.Anything, deleteOrganizerID, 0).
			Return(&entity.Organizer{ID: deleteOrganizerID, Name: "Label", PlatformFeeRateBps: 0}, nil)
		ts := newTestAdminDeleteServer(t, uc, usecasemocks.NewMockUserUseCase(t))
		zero := int32(0)

		err := call(t, ts, deleteAdminToken, setRate(&entityv1.OrganizerId{Value: deleteOrganizerID}, &zero))

		assert.NoError(t, err)
	})

	t.Run("admin records seller details", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/admin/api/rpc/organizer "Admin records seller details"
		uc := usecasemocks.NewMockOrganizerUseCase(t)
		uc.EXPECT().UpdateSellerDetails(mock.Anything, deleteOrganizerID, entity.SellerDetails{
			LegalName: "株式会社リバティ", RepresentativeName: "代表 太郎", Address: "東京都渋谷区1-2-3",
			PhoneNumber: "+81312345678", ContactEmail: "contact@example.com",
		}).Return(&entity.Organizer{ID: deleteOrganizerID, Name: "Label", PlatformFeeRateBps: 800, SellerDetails: &entity.SellerDetails{
			LegalName: "株式会社リバティ", RepresentativeName: "代表 太郎", Address: "東京都渋谷区1-2-3",
			PhoneNumber: "+81312345678", ContactEmail: "contact@example.com",
		}}, nil)
		ts := newTestAdminDeleteServer(t, uc, usecasemocks.NewMockUserUseCase(t))

		req := connect.NewRequest(&adminorganizerv1.UpdateSellerDetailsRequest{
			OrganizerId: &entityv1.OrganizerId{Value: deleteOrganizerID}, SellerDetails: details,
		})
		req.Header().Set("Authorization", "Bearer "+deleteAdminToken)
		resp, err := adminorganizerv1connect.NewOrganizerServiceClient(ts.Client(), ts.URL).UpdateSellerDetails(context.Background(), req)

		require.NoError(t, err)
		assert.Equal(t, "株式会社リバティ", resp.Msg.Organizer.SellerDetails.LegalName)
		assert.Equal(t, int32(800), resp.Msg.Organizer.PlatformFeeRateBps)
	})

	t.Run("missing seller detail", func(t *testing.T) {
		t.Parallel()
		ts := newTestAdminDeleteServer(t, usecasemocks.NewMockOrganizerUseCase(t), usecasemocks.NewMockUserUseCase(t))
		partial := &entityv1.SellerDetails{LegalName: "株式会社リバティ"}

		req := connect.NewRequest(&adminorganizerv1.UpdateSellerDetailsRequest{
			OrganizerId: &entityv1.OrganizerId{Value: deleteOrganizerID}, SellerDetails: partial,
		})
		req.Header().Set("Authorization", "Bearer "+deleteAdminToken)
		_, err := adminorganizerv1connect.NewOrganizerServiceClient(ts.Client(), ts.URL).UpdateSellerDetails(context.Background(), req)

		assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
	})

	t.Run("admin reads any organizer", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/admin/api/rpc/organizer "Admin reads any Organizer"
		uc := usecasemocks.NewMockOrganizerUseCase(t)
		uc.EXPECT().Get(mock.Anything, deleteOrganizerID).Return(&entity.Organizer{
			ID: deleteOrganizerID, Name: "Label", Status: entity.OrganizerStatusDeactivated, PlatformFeeRateBps: 500,
			SellerDetails: &entity.SellerDetails{
				LegalName: "株式会社リバティ", RepresentativeName: "代表 太郎", Address: "東京都渋谷区1-2-3",
				PhoneNumber: "+81312345678", ContactEmail: "contact@example.com",
			},
		}, nil)
		ts := newTestAdminDeleteServer(t, uc, usecasemocks.NewMockUserUseCase(t))

		req := connect.NewRequest(&adminorganizerv1.GetRequest{OrganizerId: &entityv1.OrganizerId{Value: deleteOrganizerID}})
		req.Header().Set("Authorization", "Bearer "+deleteAdminToken)
		resp, err := adminorganizerv1connect.NewOrganizerServiceClient(ts.Client(), ts.URL).Get(context.Background(), req)

		require.NoError(t, err)
		assert.Equal(t, deleteOrganizerID, resp.Msg.Organizer.Id.Value)
		assert.Equal(t, "Label", resp.Msg.Organizer.Name.Value)
		assert.Equal(t, int32(500), resp.Msg.Organizer.PlatformFeeRateBps)
		assert.Equal(t, "+81312345678", resp.Msg.Organizer.SellerDetails.PhoneNumber)
	})

	t.Run("errors are returned unchanged", func(t *testing.T) {
		t.Parallel()
		uc := usecasemocks.NewMockOrganizerUseCase(t)
		uc.EXPECT().SetPlatformFeeRate(mock.Anything, deleteOrganizerID, 500).Return(nil, apperr.ErrNotFound)
		ts := newTestAdminDeleteServer(t, uc, usecasemocks.NewMockUserUseCase(t))

		err := call(t, ts, deleteAdminToken, setRate(&entityv1.OrganizerId{Value: deleteOrganizerID}, &rate))

		assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
	})
}
