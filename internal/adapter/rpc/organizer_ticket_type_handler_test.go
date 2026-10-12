package rpc_test

import (
	"context"
	"net/http"
	"testing"

	tickettypev1connect "buf.build/gen/go/liverty-music/schema/connectrpc/go/liverty_music/rpc/organizer/ticket_type/v1/ticket_typev1connect"
	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	tickettypev1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/organizer/ticket_type/v1"
	"connectrpc.com/connect"
	handler "github.com/liverty-music/backend/internal/adapter/rpc"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/usecase"
	ucmocks "github.com/liverty-music/backend/internal/usecase/mocks"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func newOrganizerTicketTypeClient(t *testing.T) (tickettypev1connect.TicketTypeServiceClient, *ucmocks.MockTicketTypeUseCase, *ucmocks.MockOrganizerUseCase) {
	t.Helper()
	types := ucmocks.NewMockTicketTypeUseCase(t)
	orgs := ucmocks.NewMockOrganizerUseCase(t)
	logger, err := logging.New()
	require.NoError(t, err)
	h := handler.NewOrganizerTicketTypeHandler(types, orgs, logger)
	srv := serveValidated(t, func(opts ...connect.HandlerOption) (string, http.Handler) {
		return tickettypev1connect.NewTicketTypeServiceHandler(h, opts...)
	}, asOperator(testProviderOrganizerID))
	return tickettypev1connect.NewTicketTypeServiceClient(srv.Client(), srv.URL), types, orgs
}

func TestOrganizerTicketTypeHandler_Update(t *testing.T) {
	t.Parallel()

	updateReq := func() *tickettypev1.UpdateRequest {
		return &tickettypev1.UpdateRequest{
			TicketTypeId:    &entityv1.TicketTypeId{Value: tkTicketTypeID},
			Price:           &entityv1.TicketTypePrice{Value: 3000},
			Quantity:        180,
			PerAccountLimit: 4,
		}
	}

	t.Run("operator raises the quantity", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/organizer/api/rpc/ticket-type "Operator raises the quantity"
		client, types, orgs := newOrganizerTicketTypeClient(t)
		orgs.EXPECT().ResolveCaller(mock.Anything, testProviderOrganizerID).Return(&entity.Organizer{ID: "org-1"}, nil)
		types.EXPECT().Update(mock.Anything, "org-1", usecase.UpdateTicketTypeInput{
			TicketTypeID: tkTicketTypeID, Price: 3000, Quantity: 180, PerAccountLimit: 4,
		}).Return(&usecase.TicketTypeView{
			TicketType: &entity.TicketType{ID: tkTicketTypeID, TicketSaleID: tkSaleID, EventID: testEventID,
				Price: 3000, Quantity: 180, PerAccountLimit: 4, SoldCount: 150},
			State: entity.TicketTypeStateOnSale,
		}, nil)

		resp, err := client.Update(context.Background(), connect.NewRequest(updateReq()))

		require.NoError(t, err)
		assert.Equal(t, int32(180), resp.Msg.GetTicketType().GetQuantity())
		assert.Equal(t, int32(150), resp.Msg.GetTicketType().GetSoldCount())
	})

	t.Run("deactivated organizer", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/organizer/api/rpc/ticket-type "Deactivated Organizer"
		client, _, orgs := newOrganizerTicketTypeClient(t)
		orgs.EXPECT().ResolveCaller(mock.Anything, testProviderOrganizerID).Return(nil, apperr.New(codes.FailedPrecondition, "organizer is deactivated"))

		_, err := client.Update(context.Background(), connect.NewRequest(updateReq()))

		assert.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err))
	})

	invalid := []struct {
		name   string
		mutate func(*tickettypev1.UpdateRequest)
	}{
		// @spec components/adapter/organizer/api/rpc/ticket-type "Missing quantity"
		{name: "missing quantity", mutate: func(r *tickettypev1.UpdateRequest) { r.Quantity = 0 }},
		{name: "missing ticket type", mutate: func(r *tickettypev1.UpdateRequest) { r.TicketTypeId = nil }},
		{name: "missing price", mutate: func(r *tickettypev1.UpdateRequest) { r.Price = nil }},
		{name: "missing per-account limit", mutate: func(r *tickettypev1.UpdateRequest) { r.PerAccountLimit = 0 }},
		{name: "per-account limit over 10", mutate: func(r *tickettypev1.UpdateRequest) { r.PerAccountLimit = 11 }},
	}
	for _, tt := range invalid {
		t.Run(tt.name+" fails with InvalidArgument", func(t *testing.T) {
			t.Parallel()
			// No expectations: neither usecase may run.
			client, _, _ := newOrganizerTicketTypeClient(t)
			req := updateReq()
			tt.mutate(req)

			_, err := client.Update(context.Background(), connect.NewRequest(req))

			assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
		})
	}
}
