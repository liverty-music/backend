package rpc_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	ticketsalev1connect "buf.build/gen/go/liverty-music/schema/connectrpc/go/liverty_music/rpc/organizer/ticket_sale/v1/ticket_salev1connect"
	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	ticketsalev1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/organizer/ticket_sale/v1"
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
	"google.golang.org/protobuf/types/known/timestamppb"
)

// organizerTicketSaleFixture serves an OrganizerTicketSaleHandler behind the
// validation interceptor, signed in as an operator of testProviderOrganizerID.
type organizerTicketSaleFixture struct {
	sales  *ucmocks.MockTicketSaleUseCase
	orgs   *ucmocks.MockOrganizerUseCase
	client ticketsalev1connect.TicketSaleServiceClient
}

func newOrganizerTicketSaleFixture(t *testing.T, decorate func(context.Context) context.Context) *organizerTicketSaleFixture {
	t.Helper()
	f := &organizerTicketSaleFixture{sales: ucmocks.NewMockTicketSaleUseCase(t), orgs: ucmocks.NewMockOrganizerUseCase(t)}
	logger, err := logging.New()
	require.NoError(t, err)
	h := handler.NewOrganizerTicketSaleHandler(f.sales, f.orgs, logger)
	srv := serveValidated(t, func(opts ...connect.HandlerOption) (string, http.Handler) {
		return ticketsalev1connect.NewTicketSaleServiceHandler(h, opts...)
	}, decorate)
	f.client = ticketsalev1connect.NewTicketSaleServiceClient(srv.Client(), srv.URL)
	return f
}

func (f *organizerTicketSaleFixture) resolvesTo(organizerID string) {
	f.orgs.EXPECT().ResolveCaller(mock.Anything, testProviderOrganizerID).Return(&entity.Organizer{ID: organizerID}, nil)
}

func TestOrganizerTicketSaleHandler(t *testing.T) {
	t.Parallel()
	start := time.Date(2026, 11, 1, 1, 0, 0, 0, time.UTC)
	end := start.Add(7 * 24 * time.Hour)

	createReq := func() *ticketsalev1.CreateRequest {
		return &ticketsalev1.CreateRequest{
			SeriesId:      &entityv1.SeriesId{Value: tkSeriesID},
			Name:          &entityv1.TicketSaleName{Value: "一般発売"},
			Method:        entityv1.TicketSaleMethod_TICKET_SALE_METHOD_FIRST_COME,
			SaleStartTime: timestamppb.New(start),
			TicketTypes: []*ticketsalev1.TicketTypeInput{{
				EventId:  &entityv1.EventId{Value: testEventID},
				Price:    &entityv1.TicketTypePrice{Value: 3000},
				Quantity: 150,
			}},
		}
	}
	firstComeView := func(state entity.TicketTypeState) *usecase.TicketSaleView {
		return &usecase.TicketSaleView{
			Sale: &entity.TicketSale{
				ID: tkSaleID, SeriesID: tkSeriesID, Name: "一般発売", Method: entity.TicketSaleMethodFirstCome,
				SaleStartTime: start, SaleEndTime: end,
			},
			TicketTypes: []*usecase.TicketTypeView{{
				TicketType: &entity.TicketType{
					ID: tkTicketTypeID, TicketSaleID: tkSaleID, EventID: testEventID, Price: 3000,
					Quantity: 150, PerAccountLimit: 4, SoldCount: 100, HeldCount: 6,
				},
				State: state,
			}},
		}
	}

	t.Run("operator configures a sale", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/organizer/api/rpc/ticket-sale "Operator configures a sale"
		f := newOrganizerTicketSaleFixture(t, asOperator(testProviderOrganizerID))
		f.resolvesTo("org-1")
		f.sales.EXPECT().Create(mock.Anything, "org-1", usecase.CreateTicketSaleInput{
			SeriesID: tkSeriesID, Name: "一般発売", Method: entity.TicketSaleMethodFirstCome, SaleStart: start,
			TicketTypes: []usecase.TicketTypeInput{{EventID: testEventID, Price: 3000, Quantity: 150}},
		}).Return(firstComeView(entity.TicketTypeStateNotYetOnSale), nil)

		resp, err := f.client.Create(context.Background(), connect.NewRequest(createReq()))

		require.NoError(t, err)
		sale := resp.Msg.GetTicketSale()
		assert.Equal(t, tkSaleID, sale.GetId().GetValue())
		require.Len(t, sale.GetTicketTypes(), 1)
		assert.Equal(t, int32(150), sale.GetTicketTypes()[0].GetQuantity())
		assert.Equal(t, entityv1.TicketTypeState_TICKET_TYPE_STATE_NOT_YET_ON_SALE, sale.GetTicketTypes()[0].GetState())
	})

	t.Run("optional end time, limit and verification are passed on", func(t *testing.T) {
		t.Parallel()
		f := newOrganizerTicketSaleFixture(t, asOperator(testProviderOrganizerID))
		f.resolvesTo("org-1")
		f.sales.EXPECT().Create(mock.Anything, "org-1", mock.MatchedBy(func(in usecase.CreateTicketSaleInput) bool {
			return in.Method == entity.TicketSaleMethodLottery && in.SaleEnd != nil && in.SaleEnd.Equal(end) &&
				in.VerificationRequirement == entity.VerificationRequirementJPKIOnly && in.TicketTypes[0].PerAccountLimit == 2
		})).Return(firstComeView(entity.TicketTypeStateNotYetOnSale), nil)
		req := createReq()
		limit := int32(2)
		req.Method = entityv1.TicketSaleMethod_TICKET_SALE_METHOD_LOTTERY
		req.SaleEndTime = timestamppb.New(end)
		req.VerificationRequirement = entityv1.VerificationRequirement_VERIFICATION_REQUIREMENT_JPKI_ONLY
		req.TicketTypes[0].PerAccountLimit = &limit

		_, err := f.client.Create(context.Background(), connect.NewRequest(req))

		require.NoError(t, err)
	})

	t.Run("operator sees how many sold", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/organizer/api/rpc/ticket-sale "Operator sees how many sold"
		f := newOrganizerTicketSaleFixture(t, asOperator(testProviderOrganizerID))
		f.resolvesTo("org-1")
		lottery := &usecase.TicketSaleView{
			Sale: &entity.TicketSale{ID: "019a0000-0000-7000-8000-0000000000d2", SeriesID: tkSeriesID, Name: "抽選",
				Method: entity.TicketSaleMethodLottery, SaleStartTime: start.Add(-14 * 24 * time.Hour), SaleEndTime: start.Add(-7 * 24 * time.Hour)},
			TicketTypes: []*usecase.TicketTypeView{{
				TicketType: &entity.TicketType{ID: "019a0000-0000-7000-8000-0000000000d3", EventID: testEventID, Price: 3000, Quantity: 50, PerAccountLimit: 2},
				Stats:      &entity.LotteryEntryStats{EntryCount: 80, TicketCount: 140, WonEntryCount: 30, WonTicketCount: 50, LostEntryCount: 50},
			}},
		}
		f.sales.EXPECT().ListOwnByEvent(mock.Anything, "org-1", testEventID).
			Return([]*usecase.TicketSaleView{lottery, firstComeView(entity.TicketTypeStateOnSale)}, nil)

		resp, err := f.client.ListByEvent(context.Background(), connect.NewRequest(&ticketsalev1.ListByEventRequest{
			EventId: &entityv1.EventId{Value: testEventID},
		}))

		require.NoError(t, err)
		require.Len(t, resp.Msg.GetTicketSales(), 2)
		fc := resp.Msg.GetTicketSales()[1].GetTicketTypes()[0]
		assert.Equal(t, int32(150), fc.GetQuantity())
		assert.Equal(t, int32(100), fc.GetSoldCount())
		assert.Equal(t, int32(6), fc.GetHeldCount())
		assert.Equal(t, entityv1.TicketTypeState_TICKET_TYPE_STATE_ON_SALE, fc.GetState())
		lt := resp.Msg.GetTicketSales()[0].GetTicketTypes()[0]
		assert.Equal(t, int32(50), lt.GetQuantity())
		assert.Nil(t, lt.SoldCount, "a lottery ticket type has no sold count")
		require.Len(t, resp.Msg.GetLotteryEntryStats(), 1)
		stats := resp.Msg.GetLotteryEntryStats()[0]
		assert.Equal(t, "019a0000-0000-7000-8000-0000000000d3", stats.GetTicketTypeId().GetValue())
		assert.Equal(t, int32(80), stats.GetEntryCount())
		assert.Equal(t, int32(50), stats.GetWonTicketCount())
	})

	t.Run("operator moves a window", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/organizer/api/rpc/ticket-sale "Operator moves a window"
		f := newOrganizerTicketSaleFixture(t, asOperator(testProviderOrganizerID))
		f.resolvesTo("org-1")
		f.sales.EXPECT().SetWindow(mock.Anything, "org-1", entity.TicketSaleID(tkSaleID), start, end).
			Return(firstComeView(entity.TicketTypeStateNotYetOnSale), nil)

		resp, err := f.client.SetWindow(context.Background(), connect.NewRequest(&ticketsalev1.SetWindowRequest{
			TicketSaleId: &entityv1.TicketSaleId{Value: tkSaleID}, SaleStartTime: timestamppb.New(start), SaleEndTime: timestamppb.New(end),
		}))

		require.NoError(t, err)
		assert.True(t, resp.Msg.GetTicketSale().GetSaleEndTime().AsTime().Equal(end))
	})

	t.Run("no requirement sets none", func(t *testing.T) {
		t.Parallel()
		f := newOrganizerTicketSaleFixture(t, asOperator(testProviderOrganizerID))
		f.resolvesTo("org-1")
		f.sales.EXPECT().SetVerificationRequirement(mock.Anything, "org-1", entity.TicketSaleID(tkSaleID), entity.VerificationRequirementNone).
			Return(firstComeView(entity.TicketTypeStateOnSale), nil)

		_, err := f.client.SetVerificationRequirement(context.Background(), connect.NewRequest(&ticketsalev1.SetVerificationRequirementRequest{
			TicketSaleId: &entityv1.TicketSaleId{Value: tkSaleID},
		}))

		require.NoError(t, err)
	})

	t.Run("tenant with no organizer", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/organizer/api/rpc/ticket-sale "Tenant with no Organizer"
		f := newOrganizerTicketSaleFixture(t, asOperator(testProviderOrganizerID))
		f.orgs.EXPECT().ResolveCaller(mock.Anything, testProviderOrganizerID).Return(nil, apperr.New(codes.PermissionDenied, "no organizer"))

		_, err := f.client.Create(context.Background(), connect.NewRequest(createReq()))

		assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
	})

	t.Run("caller without a tenant", func(t *testing.T) {
		t.Parallel()
		f := newOrganizerTicketSaleFixture(t, signedInAs("op-sub"))

		_, err := f.client.Create(context.Background(), connect.NewRequest(createReq()))

		assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
	})

	t.Run("deactivated organizer", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/organizer/api/rpc/ticket-sale "Deactivated Organizer"
		f := newOrganizerTicketSaleFixture(t, asOperator(testProviderOrganizerID))
		f.orgs.EXPECT().ResolveCaller(mock.Anything, testProviderOrganizerID).
			Return(nil, apperr.New(codes.FailedPrecondition, "organizer is deactivated")).Times(4)
		ctx := context.Background()

		_, err := f.client.Create(ctx, connect.NewRequest(createReq()))
		assert.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err))
		_, err = f.client.ListByEvent(ctx, connect.NewRequest(&ticketsalev1.ListByEventRequest{EventId: &entityv1.EventId{Value: testEventID}}))
		assert.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err))
		_, err = f.client.SetWindow(ctx, connect.NewRequest(&ticketsalev1.SetWindowRequest{
			TicketSaleId: &entityv1.TicketSaleId{Value: tkSaleID}, SaleStartTime: timestamppb.New(start), SaleEndTime: timestamppb.New(end),
		}))
		assert.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err))
		_, err = f.client.SetVerificationRequirement(ctx, connect.NewRequest(&ticketsalev1.SetVerificationRequirementRequest{
			TicketSaleId: &entityv1.TicketSaleId{Value: tkSaleID},
		}))
		assert.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err))
	})

	// The usecase mocks carry no expectations in the cases below: a call that
	// reached them would fail the test.
	invalid := []struct {
		name string
		call func(ctx context.Context, c ticketsalev1connect.TicketSaleServiceClient) error
	}{
		{
			// @spec components/adapter/organizer/api/rpc/ticket-sale "Missing price"
			name: "missing price",
			call: func(ctx context.Context, c ticketsalev1connect.TicketSaleServiceClient) error {
				req := createReq()
				req.TicketTypes[0].Price = nil
				_, err := c.Create(ctx, connect.NewRequest(req))
				return err
			},
		},
		{
			// @spec components/adapter/organizer/api/rpc/ticket-sale "Sale without ticket types"
			name: "sale without ticket types",
			call: func(ctx context.Context, c ticketsalev1connect.TicketSaleServiceClient) error {
				req := createReq()
				req.TicketTypes = nil
				_, err := c.Create(ctx, connect.NewRequest(req))
				return err
			},
		},
		{
			name: "ticket type without a quantity",
			call: func(ctx context.Context, c ticketsalev1connect.TicketSaleServiceClient) error {
				req := createReq()
				req.TicketTypes[0].Quantity = 0
				_, err := c.Create(ctx, connect.NewRequest(req))
				return err
			},
		},
		{
			name: "create without a name",
			call: func(ctx context.Context, c ticketsalev1connect.TicketSaleServiceClient) error {
				req := createReq()
				req.Name = nil
				_, err := c.Create(ctx, connect.NewRequest(req))
				return err
			},
		},
		{
			name: "create without a method",
			call: func(ctx context.Context, c ticketsalev1connect.TicketSaleServiceClient) error {
				req := createReq()
				req.Method = entityv1.TicketSaleMethod_TICKET_SALE_METHOD_UNSPECIFIED
				_, err := c.Create(ctx, connect.NewRequest(req))
				return err
			},
		},
		{
			// @spec components/adapter/organizer/api/rpc/ticket-sale "ListByEvent without an event"
			name: "list by event without an event",
			call: func(ctx context.Context, c ticketsalev1connect.TicketSaleServiceClient) error {
				_, err := c.ListByEvent(ctx, connect.NewRequest(&ticketsalev1.ListByEventRequest{}))
				return err
			},
		},
		{
			// @spec components/adapter/organizer/api/rpc/ticket-sale "SetWindow without an end time"
			name: "set window without an end time",
			call: func(ctx context.Context, c ticketsalev1connect.TicketSaleServiceClient) error {
				_, err := c.SetWindow(ctx, connect.NewRequest(&ticketsalev1.SetWindowRequest{
					TicketSaleId: &entityv1.TicketSaleId{Value: tkSaleID}, SaleStartTime: timestamppb.New(start),
				}))
				return err
			},
		},
		{
			name: "set verification requirement without a sale",
			call: func(ctx context.Context, c ticketsalev1connect.TicketSaleServiceClient) error {
				_, err := c.SetVerificationRequirement(ctx, connect.NewRequest(&ticketsalev1.SetVerificationRequirementRequest{}))
				return err
			},
		},
	}
	for _, tt := range invalid {
		t.Run(tt.name+" fails with InvalidArgument", func(t *testing.T) {
			t.Parallel()
			f := newOrganizerTicketSaleFixture(t, asOperator(testProviderOrganizerID))

			err := tt.call(context.Background(), f.client)

			assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
		})
	}
}
