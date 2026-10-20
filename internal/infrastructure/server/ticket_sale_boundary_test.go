package server_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	ticketsalev1connect "buf.build/gen/go/liverty-music/schema/connectrpc/go/liverty_music/rpc/ticket_sale/v1/ticket_salev1connect"
	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
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
		nil, nil, handlers...)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts
}

func TestFanServer_TicketSaleListByEvent(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, 11, 1, 1, 0, 0, 0, time.UTC)
	// onSale is a first-come sale of a two-event Series, read for saleEventID:
	// only that event's TicketType is in the view, 150 offered, 15 left.
	onSale := func() []*usecase.TicketSaleView {
		return []*usecase.TicketSaleView{{
			Sale: &entity.TicketSale{
				ID: "019a0000-0000-7000-8000-0000000000e2", SeriesID: "019a0000-0000-7000-8000-0000000000e0", Name: "一般発売",
				Method: entity.TicketSaleMethodFirstCome, SaleStartTime: start, SaleEndTime: start.Add(19 * 24 * time.Hour),
			},
			TicketTypes: []*usecase.TicketTypeView{{
				TicketType: &entity.TicketType{
					ID: "019a0000-0000-7000-8000-0000000000e3", TicketSaleID: "019a0000-0000-7000-8000-0000000000e2", EventID: saleEventID,
					Price: 3000, Quantity: 150, PerAccountLimit: 4, SoldCount: 130, HeldCount: 5,
				},
				State:    entity.TicketTypeStateOnSale,
				LowStock: true,
			}},
		}}
	}
	listByEvent := func(ts *httptest.Server, req *ticketsalev1.ListByEventRequest) (*connect.Response[ticketsalev1.ListByEventResponse], error) {
		// No Authorization header: the procedure is public.
		return ticketsalev1connect.NewTicketSaleServiceClient(ts.Client(), ts.URL).ListByEvent(context.Background(), connect.NewRequest(req))
	}

	t.Run("guest opens the event page", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/fan/api/rpc/ticket-sale "Guest opens the event page"
		uc := usecasemocks.NewMockTicketSaleUseCase(t)
		uc.EXPECT().ListByEvent(mock.Anything, saleEventID).Return(onSale(), nil)
		ts := newTestFanTicketSaleServer(t, uc)

		resp, err := listByEvent(ts, &ticketsalev1.ListByEventRequest{EventId: &entityv1.EventId{Value: saleEventID}})

		require.NoError(t, err)
		require.Len(t, resp.Msg.GetTicketSales(), 1)
		sale := resp.Msg.GetTicketSales()[0]
		assert.Equal(t, "一般発売", sale.GetName().GetValue())
		require.Len(t, sale.GetTicketTypes(), 1)
		assert.Equal(t, saleEventID, sale.GetTicketTypes()[0].GetEventId().GetValue())
		assert.Equal(t, entityv1.TicketTypeState_TICKET_TYPE_STATE_ON_SALE, sale.GetTicketTypes()[0].GetState())
	})

	t.Run("few left without a count", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/fan/api/rpc/ticket-sale "Few left without a count"
		uc := usecasemocks.NewMockTicketSaleUseCase(t)
		uc.EXPECT().ListByEvent(mock.Anything, saleEventID).Return(onSale(), nil)
		ts := newTestFanTicketSaleServer(t, uc)

		resp, err := listByEvent(ts, &ticketsalev1.ListByEventRequest{EventId: &entityv1.EventId{Value: saleEventID}})

		require.NoError(t, err)
		tt := resp.Msg.GetTicketSales()[0].GetTicketTypes()[0]
		assert.Equal(t, entityv1.TicketTypeState_TICKET_TYPE_STATE_ON_SALE, tt.GetState())
		assert.True(t, tt.GetLowStock())
		assert.Equal(t, int64(3000), tt.GetPrice().GetValue())
		assert.Nil(t, tt.Quantity, "the quantity is never shown to fans")
		assert.Nil(t, tt.SoldCount, "the sold count is never shown to fans")
		assert.Nil(t, tt.HeldCount, "the held count is never shown to fans")
	})

	t.Run("resale without a count", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/fan/api/rpc/ticket-sale "Resale without a count"
		views := onSale()
		ttv := views[0].TicketTypes[0]
		opened := start.Add(24 * time.Hour)
		ttv.TicketType.SoldCount, ttv.TicketType.HeldCount = 150, 0
		ttv.TicketType.ResaleOpenTime, ttv.TicketType.FreeResaleCount = &opened, 3
		ttv.State, ttv.LowStock = entity.TicketTypeStateSoldOut, false
		ttv.ResaleOpen, ttv.ResaleAvailable = true, true
		uc := usecasemocks.NewMockTicketSaleUseCase(t)
		uc.EXPECT().ListByEvent(mock.Anything, saleEventID).Return(views, nil)
		ts := newTestFanTicketSaleServer(t, uc)

		resp, err := listByEvent(ts, &ticketsalev1.ListByEventRequest{EventId: &entityv1.EventId{Value: saleEventID}})

		require.NoError(t, err)
		tt := resp.Msg.GetTicketSales()[0].GetTicketTypes()[0]
		assert.Equal(t, entityv1.TicketTypeState_TICKET_TYPE_STATE_SOLD_OUT, tt.GetState())
		assert.True(t, tt.GetResaleOpen())
		assert.True(t, tt.GetResaleAvailable())
		assert.Nil(t, tt.Quantity)
		assert.Nil(t, tt.SoldCount)
		assert.Nil(t, tt.HeldCount)
	})

	t.Run("missing event", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/fan/api/rpc/ticket-sale "Missing event"
		ts := newTestFanTicketSaleServer(t, usecasemocks.NewMockTicketSaleUseCase(t))

		_, err := listByEvent(ts, &ticketsalev1.ListByEventRequest{})

		assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
	})

	t.Run("malformed event", func(t *testing.T) {
		t.Parallel()
		ts := newTestFanTicketSaleServer(t, usecasemocks.NewMockTicketSaleUseCase(t))

		_, err := listByEvent(ts, &ticketsalev1.ListByEventRequest{EventId: &entityv1.EventId{Value: "not-a-uuid"}})

		assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
	})
}
