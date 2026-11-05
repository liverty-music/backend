package rpc_test

import (
	"context"
	"testing"
	"time"

	resaleentryv1connect "buf.build/gen/go/liverty-music/schema/connectrpc/go/v2/liverty_music/rpc/resale_entry/v1/resale_entryv1connect"
	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	resaleentryv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/resale_entry/v1"
	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	handler "github.com/liverty-music/backend/internal/adapter/rpc"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/usecase"
	ucmocks "github.com/liverty-music/backend/internal/usecase/mocks"
)

// resaleEntryFixture serves a ResaleEntryHandler behind the validation
// interceptor. The usecase mocks fail the test on any call not expected.
type resaleEntryHandlerFixture struct {
	resale *ucmocks.MockResaleEntryUseCase
	users  *ucmocks.MockUserUseCase
	client resaleentryv1connect.ResaleEntryServiceClient
}

func newResaleEntryHandlerFixture(t *testing.T, decorate func(context.Context) context.Context) *resaleEntryHandlerFixture {
	t.Helper()
	f := &resaleEntryHandlerFixture{resale: ucmocks.NewMockResaleEntryUseCase(t), users: ucmocks.NewMockUserUseCase(t)}
	logger, err := logging.New()
	require.NoError(t, err)
	h := handler.NewResaleEntryHandler(f.resale, f.users, logger)
	srv := serveValidated(t, func(s *connect.Server) {
		resaleentryv1connect.RegisterResaleEntryServiceHandler(s, h)
	}, decorate)
	f.client = resaleentryv1connect.NewResaleEntryServiceClient(connect.NewClient(connecthttp.NewTransport(srv.Client(), srv.URL)))
	return f
}

func TestResaleEntryHandler(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	const entryID = "019a0000-0000-7000-8000-0000000000d1"
	authorizeReq := func(count int32) *resaleentryv1.AuthorizeCardRequest {
		return &resaleentryv1.AuthorizeCardRequest{
			TicketStockId: &entityv1.TicketStockId{Value: tkStockID},
			TicketCount:   count,
			FullName:      &entityv1.FullName{Value: "山田太郎"},
			PhoneNumber:   &entityv1.PhoneNumber{Value: "+819012345678"},
		}
	}
	nextDraw := time.Date(2026, 11, 6, 3, 0, 0, 0, time.UTC)
	entered := &entity.ResaleEntry{
		ID: entryID, OrderID: tkOrderID, State: entity.ResaleEntryStateEntered, CreateTime: nextDraw.Add(-24 * time.Hour),
		Order: &entity.Order{
			ID: tkOrderID, UserID: tkFanUserID, TicketStockID: tkStockID, TicketCount: 2, ResaleEntryID: entryID,
			Status: entity.OrderStatusPending, Amount: 6000, Currency: "JPY",
		},
	}

	t.Run("zero tickets", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/fan/api/rpc/resale-entry "Zero tickets"
		f := newResaleEntryHandlerFixture(t, signedInAs(tkFanSub))

		_, err := f.client.AuthorizeCard(ctx, authorizeReq(0))

		assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err), "no card authorization is opened")
	})

	t.Run("valid application", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/fan/api/rpc/resale-entry "Valid application"
		f := newResaleEntryHandlerFixture(t, signedInAs(tkFanSub))
		expectFan(f.users)
		f.resale.EXPECT().AuthorizeCard(mock.Anything, entity.TicketStockID(tkStockID), entity.UserID(tkFanUserID), 2,
			"山田太郎", "+819012345678", (*entity.ReferralCode)(nil)).
			Return(&entity.CardAuthorization{ID: "019a0000-0000-7000-8000-000000000001", Secret: "pi_1_secret"}, nil)

		resp, err := f.client.AuthorizeCard(ctx, authorizeReq(2))

		require.NoError(t, err)
		assert.Equal(t, "pi_1_secret", resp.GetCardAuthorizationSecret().GetValue())
	})

	t.Run("guest", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/fan/api/rpc/resale-entry "Guest"
		f := newResaleEntryHandlerFixture(t, func(c context.Context) context.Context { return c })

		_, err := f.client.List(ctx, &resaleentryv1.ListRequest{})

		assert.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
	})

	t.Run("signed-in fan", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/fan/api/rpc/resale-entry "Signed-in fan"
		f := newResaleEntryHandlerFixture(t, signedInAs(tkFanSub))
		expectFan(f.users)
		stock := &entity.TicketStock{ID: tkStockID, EventID: "019a0000-0000-7000-8000-0000000000e1", SeatTypeID: "019a0000-0000-7000-8000-0000000000e2",
			SeatType: &entity.SeatType{ID: "019a0000-0000-7000-8000-0000000000e2", SeriesID: "019a0000-0000-7000-8000-0000000000e3", Name: "S席", Price: 3000}, Quantity: 10, SoldCount: 10}
		f.resale.EXPECT().List(mock.Anything, entity.UserID(tkFanUserID)).Return([]*usecase.ResaleEntryView{{Entry: entered, Stock: stock, NextDrawTime: &nextDraw}}, nil)

		resp, err := f.client.List(ctx, &resaleentryv1.ListRequest{})

		require.NoError(t, err)
		require.Len(t, resp.GetResaleEntries(), 1)
		assert.Equal(t, entryID, resp.GetResaleEntries()[0].GetId().GetValue())
		assert.True(t, resp.GetResaleEntries()[0].GetNextDrawTime().AsTime().Equal(nextDraw))
		require.Len(t, resp.GetOrders(), 1)
		require.Len(t, resp.GetTicketStocks(), 1)
		require.Len(t, resp.GetSeatTypes(), 1)
		assert.Equal(t, "S席", resp.GetSeatTypes()[0].GetName().GetValue())
	})

	t.Run("fan withdraws an entry", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/fan/api/rpc/resale-entry "Fan withdraws an entry"
		f := newResaleEntryHandlerFixture(t, signedInAs(tkFanSub))
		expectFan(f.users)
		withdrawn := *entered
		withdrawn.State = entity.ResaleEntryStateWithdrawn
		at := nextDraw.Add(-time.Hour)
		withdrawn.WithdrawTime = &at
		f.resale.EXPECT().Withdraw(mock.Anything, entity.UserID(tkFanUserID), entity.ResaleEntryID(entryID)).Return(&withdrawn, nil)

		resp, err := f.client.Withdraw(ctx, &resaleentryv1.WithdrawRequest{ResaleEntryId: &entityv1.ResaleEntryId{Value: entryID}})

		require.NoError(t, err)
		assert.Equal(t, entityv1.ResaleEntryState_RESALE_ENTRY_STATE_WITHDRAWN, resp.GetResaleEntry().GetState())
	})
}
