package rpc_test

import (
	"context"
	"testing"
	"time"

	resalelistingv1connect "buf.build/gen/go/liverty-music/schema/connectrpc/go/v2/liverty_music/rpc/resale_listing/v1/resale_listingv1connect"
	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	resalelistingv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/resale_listing/v1"
	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	handler "github.com/liverty-music/backend/internal/adapter/rpc"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/usecase"
	ucmocks "github.com/liverty-music/backend/internal/usecase/mocks"
)

const (
	rlTicketID  = "019a0000-0000-7000-8000-0000000000d1"
	rlListingID = "019a0000-0000-7000-8000-0000000000d2"
)

// resaleListingFixture serves a ResaleListingHandler behind the validation
// interceptor. The usecase mocks fail the test on any call not expected.
type resaleListingFixture struct {
	listings *ucmocks.MockResaleListingUseCase
	users    *ucmocks.MockUserUseCase
	client   resalelistingv1connect.ResaleListingServiceClient
}

func newResaleListingFixture(t *testing.T, decorate func(context.Context) context.Context) *resaleListingFixture {
	t.Helper()
	f := &resaleListingFixture{
		listings: ucmocks.NewMockResaleListingUseCase(t),
		users:    ucmocks.NewMockUserUseCase(t),
	}
	logger, err := logging.New()
	require.NoError(t, err)
	h := handler.NewResaleListingHandler(f.listings, f.users, logger)
	srv := serveValidated(t, func(s *connect.Server) {
		resalelistingv1connect.RegisterResaleListingServiceHandler(s, h)
	}, decorate)
	f.client = resalelistingv1connect.NewResaleListingServiceClient(connect.NewClient(connecthttp.NewTransport(srv.Client(), srv.URL)))
	return f
}

func TestResaleListingHandler(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	created := time.Date(2026, 11, 13, 3, 0, 0, 0, time.UTC)
	deadline := time.Date(2026, 11, 20, 7, 0, 0, 0, time.UTC)
	listed := func() *usecase.ResaleListingAt {
		return &usecase.ResaleListingAt{
			Listing: &entity.ResaleListing{
				ID: rlListingID, TicketID: rlTicketID, OrderID: tkOrderID, UserID: tkFanUserID, EventID: "event-1",
				TicketTypeID: tkTicketTypeID, Price: 5000, Status: entity.ResaleListingStatusListed,
				CheckoutID: tkCheckoutID, CreateTime: created,
			},
			Deadline: deadline,
			Status:   entity.ResaleListingStatusListed,
		}
	}
	createReq := func() *resalelistingv1.CreateRequest {
		return &resalelistingv1.CreateRequest{TicketId: &entityv1.TicketId{Value: rlTicketID}}
	}

	t.Run("fan lists a ticket", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/fan/api/rpc/resale-listing "Fan lists a ticket"
		f := newResaleListingFixture(t, signedInAs(tkFanSub))
		expectFan(f.users)
		f.listings.EXPECT().Create(mock.Anything, entity.UserID(tkFanUserID), entity.TicketID(rlTicketID)).Return(listed(), nil)

		resp, err := f.client.Create(ctx, createReq())

		require.NoError(t, err)
		got := resp.GetResaleListing()
		assert.Equal(t, rlListingID, got.GetId().GetValue())
		assert.Equal(t, rlTicketID, got.GetTicketId().GetValue())
		assert.Equal(t, int64(5000), got.GetPrice().GetValue())
		assert.Equal(t, int64(500), got.GetFee())
		assert.Equal(t, int64(4500), got.GetRefundAmount())
		assert.Equal(t, entityv1.ResaleListingStatus_RESALE_LISTING_STATUS_LISTED, got.GetStatus())
		assert.True(t, got.GetDeadlineTime().AsTime().Equal(deadline))
		assert.True(t, got.GetCreateTime().AsTime().Equal(created))
	})

	t.Run("guest calls", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/fan/api/rpc/resale-listing "Guest calls"
		f := newResaleListingFixture(t, func(c context.Context) context.Context { return c })

		_, err := f.client.Create(ctx, createReq())

		assert.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
	})

	t.Run("caller without an account", func(t *testing.T) {
		t.Parallel()
		f := newResaleListingFixture(t, signedInAs("ext-new"))
		expectNoAccount(f.users, "ext-new")

		_, err := f.client.List(ctx, &resalelistingv1.ListRequest{})

		assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
	})

	t.Run("missing ticket", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/fan/api/rpc/resale-listing "Missing ticket"
		f := newResaleListingFixture(t, signedInAs(tkFanSub))

		_, err := f.client.Create(ctx, &resalelistingv1.CreateRequest{})

		assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
	})

	t.Run("malformed listing", func(t *testing.T) {
		t.Parallel()
		f := newResaleListingFixture(t, signedInAs(tkFanSub))

		_, err := f.client.Withdraw(ctx, &resalelistingv1.WithdrawRequest{
			ResaleListingId: &entityv1.ResaleListingId{Value: "not-a-uuid"},
		})

		assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
	})

	t.Run("usecase error returned unchanged", func(t *testing.T) {
		t.Parallel()
		f := newResaleListingFixture(t, signedInAs(tkFanSub))
		expectFan(f.users)
		f.listings.EXPECT().Withdraw(mock.Anything, entity.UserID(tkFanUserID), entity.ResaleListingID(rlListingID)).
			Return(nil, apperr.New(codes.FailedPrecondition, "held"))

		_, err := f.client.Withdraw(ctx, &resalelistingv1.WithdrawRequest{
			ResaleListingId: &entityv1.ResaleListingId{Value: rlListingID},
		})

		assert.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err))
	})

	t.Run("sold listing", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/fan/api/rpc/resale-listing "Sold listing"
		f := newResaleListingFixture(t, signedInAs(tkFanSub))
		expectFan(f.users)
		sold := listed()
		soldAt, refundAt := created.Add(time.Hour), created.Add(time.Hour+time.Minute)
		sold.Listing.Status, sold.Status = entity.ResaleListingStatusSold, entity.ResaleListingStatusSold
		sold.Listing.SoldTime, sold.Listing.RefundTime = &soldAt, &refundAt
		f.listings.EXPECT().List(mock.Anything, entity.UserID(tkFanUserID)).Return([]*usecase.ResaleListingAt{sold}, nil)

		resp, err := f.client.List(ctx, &resalelistingv1.ListRequest{})

		require.NoError(t, err)
		require.Len(t, resp.GetResaleListings(), 1)
		got := resp.GetResaleListings()[0]
		assert.Equal(t, entityv1.ResaleListingStatus_RESALE_LISTING_STATUS_SOLD, got.GetStatus())
		assert.Equal(t, int64(5000), got.GetPrice().GetValue())
		assert.Equal(t, int64(500), got.GetFee())
		assert.Equal(t, int64(4500), got.GetRefundAmount())
		assert.True(t, got.GetSoldTime().AsTime().Equal(soldAt))
		assert.True(t, got.GetRefundTime().AsTime().Equal(refundAt))
		// The message has no field for the checkout or the purchasing fan, so
		// nothing about them can reach the fan.
		assert.Nil(t, got.ProtoReflect().Descriptor().Fields().ByName("checkout_id"))
	})

	t.Run("expired listing", func(t *testing.T) {
		t.Parallel()
		f := newResaleListingFixture(t, signedInAs(tkFanSub))
		expectFan(f.users)
		expired := listed()
		expired.Status = entity.ResaleListingStatusExpired
		f.listings.EXPECT().List(mock.Anything, entity.UserID(tkFanUserID)).Return([]*usecase.ResaleListingAt{expired}, nil)

		resp, err := f.client.List(ctx, &resalelistingv1.ListRequest{})

		require.NoError(t, err)
		assert.Equal(t, entityv1.ResaleListingStatus_RESALE_LISTING_STATUS_EXPIRED, resp.GetResaleListings()[0].GetStatus())
	})
}
