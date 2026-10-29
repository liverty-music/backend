package rpc_test

import (
	"context"
	"testing"
	"time"

	ticketsalev1connect "buf.build/gen/go/liverty-music/schema/connectrpc/go/v2/liverty_music/rpc/organizer/ticket_sale/v1/ticket_salev1connect"
	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	ticketsalev1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/organizer/ticket_sale/v1"
	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
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
// validation interceptor, signed in as a member of testProviderOrganizerID.
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
	srv := serveValidated(t, func(s *connect.Server) {
		ticketsalev1connect.RegisterTicketSaleServiceHandler(s, h)
	}, decorate)
	f.client = ticketsalev1connect.NewTicketSaleServiceClient(connect.NewClient(connecthttp.NewTransport(srv.Client(), srv.URL)))
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
			SaleEndTime:   timestamppb.New(end),
			EventIds:      []*entityv1.EventId{{Value: testEventID}},
		}
	}
	firstCome := func() *entity.TicketSale {
		return &entity.TicketSale{
			ID: tkSaleID, SeriesID: tkSeriesID, Name: "一般発売", Method: entity.TicketSaleMethodFirstCome,
			SaleStartTime: start, SaleEndTime: end, EventIDs: []string{testEventID},
		}
	}

	t.Run("member configures a sale", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/organizer/api/rpc/ticket-sale "Member configures a sale"
		f := newOrganizerTicketSaleFixture(t, asMember(testProviderOrganizerID))
		f.resolvesTo("org-1")
		f.sales.EXPECT().Create(mock.Anything, "org-1", usecase.CreateTicketSaleInput{
			SeriesID: tkSeriesID, Name: "一般発売", Method: entity.TicketSaleMethodFirstCome, SaleStart: start, SaleEnd: end,
			EventIDs: []string{testEventID},
		}).Return(firstCome(), nil)

		resp, err := f.client.Create(context.Background(), createReq())

		require.NoError(t, err)
		sale := resp.GetTicketSale()
		assert.Equal(t, tkSaleID, sale.GetId().GetValue())
		require.Len(t, sale.GetEventIds(), 1)
		assert.Equal(t, testEventID, sale.GetEventIds()[0].GetValue())
	})

	t.Run("lottery method and verification are passed on", func(t *testing.T) {
		t.Parallel()
		f := newOrganizerTicketSaleFixture(t, asMember(testProviderOrganizerID))
		f.resolvesTo("org-1")
		const otherEventID = "019a0000-0000-7000-8000-0000000000e2"
		lottery := firstCome()
		lottery.Method, lottery.EventIDs = entity.TicketSaleMethodLottery, []string{testEventID, otherEventID}
		f.sales.EXPECT().Create(mock.Anything, "org-1", usecase.CreateTicketSaleInput{
			SeriesID: tkSeriesID, Name: "一般発売", Method: entity.TicketSaleMethodLottery, SaleStart: start, SaleEnd: end,
			VerificationRequirement: entity.VerificationRequirementJPKIOnly, EventIDs: []string{testEventID, otherEventID},
		}).Return(lottery, nil)
		req := createReq()
		req.Method = entityv1.TicketSaleMethod_TICKET_SALE_METHOD_LOTTERY
		req.VerificationRequirement = entityv1.VerificationRequirement_VERIFICATION_REQUIREMENT_JPKI_ONLY
		req.EventIds = append(req.EventIds, &entityv1.EventId{Value: otherEventID})

		resp, err := f.client.Create(context.Background(), req)

		require.NoError(t, err)
		assert.Equal(t, entityv1.TicketSaleMethod_TICKET_SALE_METHOD_LOTTERY, resp.GetTicketSale().GetMethod())
		assert.Len(t, resp.GetTicketSale().GetEventIds(), 2)
	})

	t.Run("member sees how many sold", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/organizer/api/rpc/ticket-sale "Member sees how many sold"
		f := newOrganizerTicketSaleFixture(t, asMember(testProviderOrganizerID))
		f.resolvesTo("org-1")
		const lotteryID = "019a0000-0000-7000-8000-0000000000d2"
		lottery := &entity.TicketSale{
			ID: lotteryID, SeriesID: tkSeriesID, Name: "抽選", Method: entity.TicketSaleMethodLottery,
			SaleStartTime: start.Add(-14 * 24 * time.Hour), SaleEndTime: start.Add(-7 * 24 * time.Hour), EventIDs: []string{testEventID},
		}
		stock := &entity.TicketStock{
			ID: tkStockID, EventID: testEventID, SeatTypeID: tkSeatTypeID, Quantity: 50,
			SeatType: &entity.SeatType{ID: tkSeatTypeID, SeriesID: tkSeriesID, Name: "S席", Price: 8000},
		}
		f.sales.EXPECT().ListOwnByEvent(mock.Anything, "org-1", testEventID).Return(&usecase.OwnEventSales{
			Sales: []*entity.TicketSale{lottery, firstCome()},
			LotteryEntryStats: []*usecase.StockEntryStats{{
				SaleID: lotteryID, Stock: stock,
				Stats: entity.LotteryEntryStats{EntryCount: 80, TicketCount: 140, WonEntryCount: 30, WonTicketCount: 50, LostEntryCount: 50},
			}},
		}, nil)

		resp, err := f.client.ListByEvent(context.Background(), &ticketsalev1.ListByEventRequest{
			EventId: &entityv1.EventId{Value: testEventID},
		})

		require.NoError(t, err)
		require.Len(t, resp.GetTicketSales(), 2)
		assert.Equal(t, lotteryID, resp.GetTicketSales()[0].GetId().GetValue())
		require.Len(t, resp.GetLotteryEntryStats(), 1)
		stats := resp.GetLotteryEntryStats()[0]
		assert.Equal(t, lotteryID, stats.GetTicketSaleId().GetValue())
		assert.Equal(t, tkStockID, stats.GetTicketStockId().GetValue())
		assert.Equal(t, tkSeatTypeID, stats.GetSeatTypeId().GetValue())
		assert.Equal(t, int32(80), stats.GetEntryCount())
		assert.Equal(t, int32(140), stats.GetTicketCount())
		assert.Equal(t, int32(30), stats.GetWonEntryCount())
		assert.Equal(t, int32(50), stats.GetWonTicketCount())
		assert.Equal(t, int32(50), stats.GetLostEntryCount())
		assert.Empty(t, resp.GetReferralCodeStats())
	})

	t.Run("purchases per referral code with a direct group", func(t *testing.T) {
		t.Parallel()
		f := newOrganizerTicketSaleFixture(t, asMember(testProviderOrganizerID))
		f.resolvesTo("org-1")
		memberA := entity.ReferralCode("member-a")
		f.sales.EXPECT().ListOwnByEvent(mock.Anything, "org-1", testEventID).Return(&usecase.OwnEventSales{
			Sales: []*entity.TicketSale{firstCome()},
			ReferralCodeStats: []entity.ReferralCodeStats{
				{Code: &memberA, OrderCount: 2, TicketCount: 5, Amount: 16250},
				{OrderCount: 1, TicketCount: 1, Amount: 3250},
			},
		}, nil)

		resp, err := f.client.ListByEvent(context.Background(), &ticketsalev1.ListByEventRequest{
			EventId: &entityv1.EventId{Value: testEventID},
		})

		require.NoError(t, err)
		got := resp.GetReferralCodeStats()
		require.Len(t, got, 2)
		assert.Equal(t, "member-a", got[0].GetCode().GetValue())
		assert.Equal(t, int32(2), got[0].GetOrderCount())
		assert.Equal(t, int32(5), got[0].GetTicketCount())
		assert.Equal(t, int64(16250), got[0].GetAmount())
		assert.Nil(t, got[1].GetCode(), "the direct group has no code")
		assert.Equal(t, int32(1), got[1].GetOrderCount())
		assert.Equal(t, int64(3250), got[1].GetAmount())
	})

	t.Run("member moves a window", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/organizer/api/rpc/ticket-sale "Member moves a window"
		f := newOrganizerTicketSaleFixture(t, asMember(testProviderOrganizerID))
		f.resolvesTo("org-1")
		f.sales.EXPECT().SetWindow(mock.Anything, "org-1", entity.TicketSaleID(tkSaleID), start, end).
			Return(firstCome(), nil)

		resp, err := f.client.SetWindow(context.Background(), &ticketsalev1.SetWindowRequest{
			TicketSaleId: &entityv1.TicketSaleId{Value: tkSaleID}, SaleStartTime: timestamppb.New(start), SaleEndTime: timestamppb.New(end),
		})

		require.NoError(t, err)
		assert.True(t, resp.GetTicketSale().GetSaleEndTime().AsTime().Equal(end))
	})

	t.Run("no requirement sets none", func(t *testing.T) {
		t.Parallel()
		f := newOrganizerTicketSaleFixture(t, asMember(testProviderOrganizerID))
		f.resolvesTo("org-1")
		f.sales.EXPECT().SetVerificationRequirement(mock.Anything, "org-1", entity.TicketSaleID(tkSaleID), entity.VerificationRequirementNone).
			Return(firstCome(), nil)

		_, err := f.client.SetVerificationRequirement(context.Background(), &ticketsalev1.SetVerificationRequirementRequest{
			TicketSaleId: &entityv1.TicketSaleId{Value: tkSaleID},
		})

		require.NoError(t, err)
	})

	t.Run("organization with no organizer", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/organizer/api/rpc/ticket-sale "Organization with no Organizer"
		f := newOrganizerTicketSaleFixture(t, asMember(testProviderOrganizerID))
		f.orgs.EXPECT().ResolveCaller(mock.Anything, testProviderOrganizerID).Return(nil, apperr.New(codes.PermissionDenied, "no organizer"))

		_, err := f.client.Create(context.Background(), createReq())

		assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
	})

	t.Run("caller without an organization", func(t *testing.T) {
		t.Parallel()
		f := newOrganizerTicketSaleFixture(t, signedInAs("op-sub"))

		_, err := f.client.Create(context.Background(), createReq())

		assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
	})

	t.Run("deactivated organizer", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/organizer/api/rpc/ticket-sale "Deactivated Organizer"
		f := newOrganizerTicketSaleFixture(t, asMember(testProviderOrganizerID))
		f.orgs.EXPECT().ResolveCaller(mock.Anything, testProviderOrganizerID).
			Return(nil, apperr.New(codes.FailedPrecondition, "organizer is deactivated")).Times(4)
		ctx := context.Background()

		_, err := f.client.Create(ctx, createReq())
		assert.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err))
		_, err = f.client.ListByEvent(ctx, &ticketsalev1.ListByEventRequest{EventId: &entityv1.EventId{Value: testEventID}})
		assert.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err))
		_, err = f.client.SetWindow(ctx, &ticketsalev1.SetWindowRequest{
			TicketSaleId: &entityv1.TicketSaleId{Value: tkSaleID}, SaleStartTime: timestamppb.New(start), SaleEndTime: timestamppb.New(end),
		})
		assert.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err))
		_, err = f.client.SetVerificationRequirement(ctx, &ticketsalev1.SetVerificationRequirementRequest{
			TicketSaleId: &entityv1.TicketSaleId{Value: tkSaleID},
		})
		assert.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err))
	})

	// The usecase mocks carry no expectations in the cases below: a call that
	// reached them would fail the test.
	invalid := []struct {
		name string
		call func(ctx context.Context, c ticketsalev1connect.TicketSaleServiceClient) error
	}{
		{
			// @spec components/adapter/organizer/api/rpc/ticket-sale "Sale without events"
			name: "sale without events",
			call: func(ctx context.Context, c ticketsalev1connect.TicketSaleServiceClient) error {
				req := createReq()
				req.EventIds = nil
				_, err := c.Create(ctx, req)
				return err
			},
		},
		{
			// @spec components/adapter/organizer/api/rpc/ticket-sale "Create without an end time"
			name: "create without an end time",
			call: func(ctx context.Context, c ticketsalev1connect.TicketSaleServiceClient) error {
				req := createReq()
				req.SaleEndTime = nil
				_, err := c.Create(ctx, req)
				return err
			},
		},
		{
			name: "create with a repeated event",
			call: func(ctx context.Context, c ticketsalev1connect.TicketSaleServiceClient) error {
				req := createReq()
				req.EventIds = append(req.EventIds, &entityv1.EventId{Value: testEventID})
				_, err := c.Create(ctx, req)
				return err
			},
		},
		{
			name: "create without a series",
			call: func(ctx context.Context, c ticketsalev1connect.TicketSaleServiceClient) error {
				req := createReq()
				req.SeriesId = nil
				_, err := c.Create(ctx, req)
				return err
			},
		},
		{
			name: "create without a start time",
			call: func(ctx context.Context, c ticketsalev1connect.TicketSaleServiceClient) error {
				req := createReq()
				req.SaleStartTime = nil
				_, err := c.Create(ctx, req)
				return err
			},
		},
		{
			name: "create without a name",
			call: func(ctx context.Context, c ticketsalev1connect.TicketSaleServiceClient) error {
				req := createReq()
				req.Name = nil
				_, err := c.Create(ctx, req)
				return err
			},
		},
		{
			name: "create without a method",
			call: func(ctx context.Context, c ticketsalev1connect.TicketSaleServiceClient) error {
				req := createReq()
				req.Method = entityv1.TicketSaleMethod_TICKET_SALE_METHOD_UNSPECIFIED
				_, err := c.Create(ctx, req)
				return err
			},
		},
		{
			// @spec components/adapter/organizer/api/rpc/ticket-sale "ListByEvent without an event"
			name: "list by event without an event",
			call: func(ctx context.Context, c ticketsalev1connect.TicketSaleServiceClient) error {
				_, err := c.ListByEvent(ctx, &ticketsalev1.ListByEventRequest{})
				return err
			},
		},
		{
			// @spec components/adapter/organizer/api/rpc/ticket-sale "SetWindow without an end time"
			name: "set window without an end time",
			call: func(ctx context.Context, c ticketsalev1connect.TicketSaleServiceClient) error {
				_, err := c.SetWindow(ctx, &ticketsalev1.SetWindowRequest{
					TicketSaleId: &entityv1.TicketSaleId{Value: tkSaleID}, SaleStartTime: timestamppb.New(start),
				})
				return err
			},
		},
		{
			name: "set verification requirement without a sale",
			call: func(ctx context.Context, c ticketsalev1connect.TicketSaleServiceClient) error {
				_, err := c.SetVerificationRequirement(ctx, &ticketsalev1.SetVerificationRequirementRequest{})
				return err
			},
		},
	}
	for _, tt := range invalid {
		t.Run(tt.name+" fails with InvalidArgument", func(t *testing.T) {
			t.Parallel()
			f := newOrganizerTicketSaleFixture(t, asMember(testProviderOrganizerID))

			err := tt.call(context.Background(), f.client)

			assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
		})
	}
}
