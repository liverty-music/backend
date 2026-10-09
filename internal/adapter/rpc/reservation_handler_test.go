package rpc_test

import (
	"context"
	"testing"
	"time"

	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	reservationv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/reservation/v1"
	"connectrpc.com/connect"
	handler "github.com/liverty-music/backend/internal/adapter/rpc"
	"github.com/liverty-music/backend/internal/entity"
	entitymocks "github.com/liverty-music/backend/internal/entity/mocks"
	"github.com/liverty-music/backend/internal/usecase"
	ucmocks "github.com/liverty-music/backend/internal/usecase/mocks"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testSaleID        = "019a0000-0000-7000-8000-0000000000f1"
	testReservationID = "019a0000-0000-7000-8000-0000000000f2"
)

type reservationHandlerMocks struct {
	reservations *ucmocks.MockReservationUseCase
	issuance     *ucmocks.MockIssuanceUseCase
	users        *entitymocks.MockUserRepository
}

func newReservationHandler(t *testing.T) (*handler.ReservationHandler, reservationHandlerMocks) {
	t.Helper()
	m := reservationHandlerMocks{
		reservations: ucmocks.NewMockReservationUseCase(t),
		issuance:     ucmocks.NewMockIssuanceUseCase(t),
		users:        entitymocks.NewMockUserRepository(t),
	}
	logger, err := logging.New()
	require.NoError(t, err)
	return handler.NewReservationHandler(m.reservations, m.issuance, m.users, logger), m
}

func (m reservationHandlerMocks) signedIn(ctx context.Context) {
	m.users.EXPECT().GetByExternalID(ctx, "ext-1").Return(&entity.User{ID: "fan-1", ExternalID: "ext-1"}, nil)
}

func TestReservationHandler(t *testing.T) {
	t.Parallel()
	expiry := time.Date(2026, 11, 5, 9, 15, 0, 0, time.UTC)

	t.Run("fan starts a checkout", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/fan/api/rpc/reservation "Fan starts a checkout"
		ctx := ticketAuthedCtx("ext-1")
		h, m := newReservationHandler(t)
		m.signedIn(ctx)
		m.reservations.EXPECT().Start(ctx, entity.UserID("fan-1"), entity.TicketSaleID(testSaleID), 2).Return(&usecase.StartedReservation{
			Reservation: &entity.Reservation{ID: testReservationID, TicketSaleID: testSaleID, UserID: "fan-1", TicketCount: 2, Amount: 6000,
				Status: entity.ReservationStatusHeld, HoldExpireTime: expiry, CreateTime: expiry.Add(-15 * time.Minute)},
			TicketPrice:   3000,
			SavedIdentity: &entity.HolderIdentity{FullName: "山田 花子", PhoneNumber: "+819012345678"},
		}, nil)

		resp, err := h.Start(ctx, connect.NewRequest(&reservationv1.StartRequest{
			TicketSaleId: &entityv1.TicketSaleId{Value: testSaleID}, TicketCount: 2,
		}))

		require.NoError(t, err)
		assert.Equal(t, testReservationID, resp.Msg.Reservation.Id.Value)
		assert.Equal(t, int64(6000), resp.Msg.Reservation.Amount)
		assert.True(t, resp.Msg.Reservation.HoldExpireTime.AsTime().Equal(expiry))
		assert.Equal(t, int64(3000), resp.Msg.TicketPrice)
		assert.Equal(t, "山田 花子", resp.Msg.SavedHolderIdentity.FullName)
	})

	t.Run("guest tries to buy", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/fan/api/rpc/reservation "Guest tries to buy"
		h, _ := newReservationHandler(t)

		_, err := h.Start(context.Background(), connect.NewRequest(&reservationv1.StartRequest{
			TicketSaleId: &entityv1.TicketSaleId{Value: testSaleID}, TicketCount: 2,
		}))

		assert.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
	})

	t.Run("caller without an account", func(t *testing.T) {
		t.Parallel()
		ctx := ticketAuthedCtx("ext-1")
		h, m := newReservationHandler(t)
		m.users.EXPECT().GetByExternalID(ctx, "ext-1").Return(nil, apperr.ErrNotFound)

		_, err := h.Get(ctx, connect.NewRequest(&reservationv1.GetRequest{ReservationId: &entityv1.ReservationId{Value: testReservationID}}))

		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})

	t.Run("fan confirms", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/fan/api/rpc/reservation "Fan confirms"
		ctx := ticketAuthedCtx("ext-1")
		h, m := newReservationHandler(t)
		m.signedIn(ctx)
		fan := entity.UserID("fan-1")
		m.issuance.EXPECT().IssueFromReservation(ctx, entity.ReservationID(testReservationID), &fan).Return(&entity.Order{
			ID: "019a0000-0000-7000-8000-0000000000f3", BuyerID: "fan-1", ReservationID: testReservationID,
			Status: entity.OrderStatusPaid, Amount: 6000, Currency: "JPY", PaidTime: expiry,
			Payment: entity.Payment{Provider: entity.PaymentProviderStripe, PaymentIntentRef: "pi_1"},
		}, nil)

		resp, err := h.Confirm(ctx, connect.NewRequest(&reservationv1.ConfirmRequest{ReservationId: &entityv1.ReservationId{Value: testReservationID}}))

		require.NoError(t, err)
		assert.Equal(t, testReservationID, resp.Msg.Order.GetReservationId().GetValue())
		assert.Nil(t, resp.Msg.Order.GetApplicationId())
	})

	t.Run("missing name", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/fan/api/rpc/reservation "Missing name"
		ctx := ticketAuthedCtx("ext-1")
		h, m := newReservationHandler(t)
		m.signedIn(ctx)

		_, err := h.Authorize(ctx, connect.NewRequest(&reservationv1.AuthorizeRequest{
			ReservationId:  &entityv1.ReservationId{Value: testReservationID},
			HolderIdentity: &entityv1.HolderIdentity{PhoneNumber: "+819012345678"},
		}))

		assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
	})

	t.Run("fan authorizes", func(t *testing.T) {
		t.Parallel()
		ctx := ticketAuthedCtx("ext-1")
		h, m := newReservationHandler(t)
		m.signedIn(ctx)
		m.reservations.EXPECT().Authorize(ctx, entity.UserID("fan-1"), entity.ReservationID(testReservationID),
			entity.HolderIdentity{FullName: "山田 花子", PhoneNumber: "+819012345678"}).Return("secret_1", nil)

		resp, err := h.Authorize(ctx, connect.NewRequest(&reservationv1.AuthorizeRequest{
			ReservationId:  &entityv1.ReservationId{Value: testReservationID},
			HolderIdentity: &entityv1.HolderIdentity{FullName: "山田 花子", PhoneNumber: "+819012345678"},
		}))

		require.NoError(t, err)
		assert.Equal(t, "secret_1", resp.Msg.ClientSecret)
	})

	t.Run("get maps the outcome", func(t *testing.T) {
		t.Parallel()
		ctx := ticketAuthedCtx("ext-1")
		h, m := newReservationHandler(t)
		m.signedIn(ctx)
		committed := expiry.Add(-5 * time.Minute)
		m.reservations.EXPECT().Get(ctx, entity.UserID("fan-1"), entity.ReservationID(testReservationID)).Return(&usecase.ReservationView{
			Reservation: &entity.Reservation{ID: testReservationID, TicketSaleID: testSaleID, UserID: "fan-1", TicketCount: 2, Amount: 6000,
				Status: entity.ReservationStatusCompleted, HoldExpireTime: expiry, CommitTime: &committed, CaptureTime: &committed},
			Authorized: true,
			OrderID:    "019a0000-0000-7000-8000-0000000000f3",
		}, nil)

		resp, err := h.Get(ctx, connect.NewRequest(&reservationv1.GetRequest{ReservationId: &entityv1.ReservationId{Value: testReservationID}}))

		require.NoError(t, err)
		assert.Equal(t, entityv1.ReservationStatus_RESERVATION_STATUS_COMPLETED, resp.Msg.Reservation.Status)
		assert.True(t, resp.Msg.Authorized)
		assert.False(t, resp.Msg.Holding)
		assert.Equal(t, "019a0000-0000-7000-8000-0000000000f3", resp.Msg.OrderId.Value)
		assert.NotNil(t, resp.Msg.Reservation.CaptureTime)
	})

	t.Run("malformed reservation", func(t *testing.T) {
		t.Parallel()
		ctx := ticketAuthedCtx("ext-1")
		h, m := newReservationHandler(t)
		m.signedIn(ctx)

		_, err := h.Confirm(ctx, connect.NewRequest(&reservationv1.ConfirmRequest{ReservationId: &entityv1.ReservationId{Value: "nope"}}))

		assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
	})

	t.Run("usecase errors are returned unchanged", func(t *testing.T) {
		t.Parallel()
		ctx := ticketAuthedCtx("ext-1")
		h, m := newReservationHandler(t)
		m.signedIn(ctx)
		m.reservations.EXPECT().Start(ctx, entity.UserID("fan-1"), entity.TicketSaleID(testSaleID), 1).
			Return(nil, apperr.New(apperr.ErrResourceExhausted.Code, "not enough tickets remain"))

		_, err := h.Start(ctx, connect.NewRequest(&reservationv1.StartRequest{
			TicketSaleId: &entityv1.TicketSaleId{Value: testSaleID}, TicketCount: 1,
		}))

		assert.ErrorIs(t, err, apperr.ErrResourceExhausted)
	})
}
