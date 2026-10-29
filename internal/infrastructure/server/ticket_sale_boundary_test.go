package server_test

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	ticketsalev1connect "buf.build/gen/go/liverty-music/schema/connectrpc/go/v2/liverty_music/rpc/ticket_sale/v1/ticket_salev1connect"
	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	ticketsalev1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/ticket_sale/v1"
	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
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
	healthHandler := func(*connect.Server) {}
	handlers := []server.RPCHandlerFunc{
		func(s *connect.Server) {
			ticketsalev1connect.RegisterTicketSaleServiceHandler(s, rpc.NewTicketSaleHandler(ticketSaleUC, logger))
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
	// only that event's stock is returned, 150 offered, 15 left.
	onSale := func() *usecase.EventSales {
		limit := 4
		return &usecase.EventSales{
			Sales: []*entity.TicketSale{{
				ID: "019a0000-0000-7000-8000-0000000000e2", SeriesID: "019a0000-0000-7000-8000-0000000000e0", Name: "一般発売",
				Method: entity.TicketSaleMethodFirstCome, SaleStartTime: start, SaleEndTime: start.Add(19 * 24 * time.Hour),
				EventIDs: []string{saleEventID, "019a0000-0000-7000-8000-0000000000e5"},
			}},
			Stocks: []*usecase.TicketStockView{{Stock: &entity.TicketStock{
				ID: "019a0000-0000-7000-8000-0000000000e3", EventID: saleEventID, SeatTypeID: "019a0000-0000-7000-8000-0000000000e4",
				Quantity: 150, SoldCount: 130, HeldCount: 5,
				SeatType: &entity.SeatType{ID: "019a0000-0000-7000-8000-0000000000e4", SeriesID: "019a0000-0000-7000-8000-0000000000e0", Name: "S席", Price: 3000},
			}}},
			PerAccountLimit: &limit,
		}
	}
	listByEvent := func(ts *httptest.Server, req *ticketsalev1.ListByEventRequest) (*ticketsalev1.ListByEventResponse, error) {
		// No Authorization header: the procedure is public.
		return ticketsalev1connect.NewTicketSaleServiceClient(connect.NewClient(connecthttp.NewTransport(ts.Client(), ts.URL))).ListByEvent(context.Background(), req)
	}

	t.Run("guest opens the event page", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/fan/api/rpc/ticket-sale "Guest opens the event page"
		uc := usecasemocks.NewMockTicketSaleUseCase(t)
		uc.EXPECT().ListByEvent(mock.Anything, saleEventID).Return(onSale(), nil)
		ts := newTestFanTicketSaleServer(t, uc)

		resp, err := listByEvent(ts, &ticketsalev1.ListByEventRequest{EventId: &entityv1.EventId{Value: saleEventID}})

		require.NoError(t, err)
		require.Len(t, resp.GetTicketSales(), 1)
		assert.Equal(t, "一般発売", resp.GetTicketSales()[0].GetName().GetValue())
		require.Len(t, resp.GetTicketStocks(), 1)
		assert.Equal(t, saleEventID, resp.GetTicketStocks()[0].GetEventId().GetValue())
		require.Len(t, resp.GetSeatTypes(), 1)
		assert.Equal(t, "S席", resp.GetSeatTypes()[0].GetName().GetValue())
		assert.Equal(t, int64(3000), resp.GetSeatTypes()[0].GetPrice().GetValue())
		assert.Equal(t, int32(4), resp.GetPerAccountLimit())
	})

	t.Run("few left without a count", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/fan/api/rpc/ticket-sale "Few left without a count"
		uc := usecasemocks.NewMockTicketSaleUseCase(t)
		uc.EXPECT().ListByEvent(mock.Anything, saleEventID).Return(onSale(), nil)
		ts := newTestFanTicketSaleServer(t, uc)

		resp, err := listByEvent(ts, &ticketsalev1.ListByEventRequest{EventId: &entityv1.EventId{Value: saleEventID}})

		require.NoError(t, err)
		stock := resp.GetTicketStocks()[0]
		assert.Equal(t, entityv1.TicketStockState_TICKET_STOCK_STATE_AVAILABLE, stock.GetState())
		assert.True(t, stock.GetLowStock())
		assert.Nil(t, stock.Quantity, "the quantity is never shown to fans")
		assert.Nil(t, stock.SoldCount, "the sold count is never shown to fans")
		assert.Nil(t, stock.HeldCount, "the held count is never shown to fans")
	})

	t.Run("sold out", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/fan/api/rpc/ticket-sale "Sold out"
		sales := onSale()
		v := sales.Stocks[0]
		v.Stock.SoldCount, v.Stock.HeldCount = 150, 0
		v.ResaleOpen = true
		uc := usecasemocks.NewMockTicketSaleUseCase(t)
		uc.EXPECT().ListByEvent(mock.Anything, saleEventID).Return(sales, nil)
		ts := newTestFanTicketSaleServer(t, uc)

		resp, err := listByEvent(ts, &ticketsalev1.ListByEventRequest{EventId: &entityv1.EventId{Value: saleEventID}})

		require.NoError(t, err)
		stock := resp.GetTicketStocks()[0]
		assert.Equal(t, entityv1.TicketStockState_TICKET_STOCK_STATE_SOLD_OUT, stock.GetState())
		assert.True(t, stock.GetResaleOpen())
		assert.Nil(t, stock.Quantity)
		assert.Nil(t, stock.SoldCount)
		assert.Nil(t, stock.HeldCount)
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
