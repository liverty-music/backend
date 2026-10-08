package rpc_test

import (
	"context"
	"testing"
	"time"

	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	ticketv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/ticket/v1"
	"connectrpc.com/connect"
	handler "github.com/liverty-music/backend/internal/adapter/rpc"
	"github.com/liverty-music/backend/internal/entity"
	entitymocks "github.com/liverty-music/backend/internal/entity/mocks"
	"github.com/liverty-music/backend/internal/testutil"
	"github.com/liverty-music/backend/internal/usecase"
	ucmocks "github.com/liverty-music/backend/internal/usecase/mocks"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestTicketHandler_RegisterWalletPublicKey(t *testing.T) {
	t.Parallel()

	newHandler := func(t *testing.T) (*handler.TicketHandler, *ucmocks.MockWalletPublicKeyUseCase, *entitymocks.MockUserRepository) {
		t.Helper()
		wallet := ucmocks.NewMockWalletPublicKeyUseCase(t)
		users := entitymocks.NewMockUserRepository(t)
		logger, err := logging.New()
		require.NoError(t, err)
		return handler.NewTicketHandler(ucmocks.NewMockTicketUseCase(t), wallet, users, logger), wallet, users
	}

	t.Run("fan's phone registers its key", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/fan/api/rpc/ticket "Fan's phone registers its key"
		ctx := ticketAuthedCtx("ext-1")
		h, wallet, users := newHandler(t)
		key := testutil.NewDeviceKey(t).PublicKey(t)
		registered := time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)
		users.EXPECT().GetByExternalID(ctx, "ext-1").Return(&entity.User{ID: "user-1", ExternalID: "ext-1"}, nil)
		wallet.EXPECT().Register(ctx, entity.UserID("user-1"), key, mock.AnythingOfType("time.Time")).
			Return(&usecase.RegisterWalletPublicKeyResult{
				Key:              &entity.WalletPublicKey{UserID: "user-1", PublicKey: key, RegisteredTime: registered},
				ReplacedOtherKey: true,
			}, nil)

		resp, err := h.RegisterWalletPublicKey(ctx, connect.NewRequest(&ticketv1.RegisterWalletPublicKeyRequest{
			PublicKey: &entityv1.PublicKey{Value: key},
		}))
		require.NoError(t, err)
		assert.Equal(t, "user-1", resp.Msg.WalletPublicKey.UserId.Value)
		assert.Equal(t, []byte(key), resp.Msg.WalletPublicKey.PublicKey.Value)
		assert.True(t, resp.Msg.WalletPublicKey.RegisterTime.AsTime().Equal(registered))
		assert.True(t, resp.Msg.ReplacedOtherKey)
	})

	t.Run("not signed in", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/fan/api/rpc/ticket "Not signed in"
		h, _, _ := newHandler(t)
		_, err := h.RegisterWalletPublicKey(context.Background(), connect.NewRequest(&ticketv1.RegisterWalletPublicKeyRequest{
			PublicKey: &entityv1.PublicKey{Value: testutil.NewDeviceKey(t).PublicKey(t)},
		}))
		assert.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
	})

	t.Run("missing key", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/fan/api/rpc/ticket "Missing key"
		h, _, _ := newHandler(t)
		_, err := h.RegisterWalletPublicKey(ticketAuthedCtx("ext-1"), connect.NewRequest(&ticketv1.RegisterWalletPublicKeyRequest{}))
		assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
	})

	t.Run("caller without an account", func(t *testing.T) {
		t.Parallel()
		ctx := ticketAuthedCtx("ext-1")
		h, _, users := newHandler(t)
		users.EXPECT().GetByExternalID(ctx, "ext-1").Return(nil, apperr.New(codes.NotFound, "no user"))
		_, err := h.RegisterWalletPublicKey(ctx, connect.NewRequest(&ticketv1.RegisterWalletPublicKeyRequest{
			PublicKey: &entityv1.PublicKey{Value: testutil.NewDeviceKey(t).PublicKey(t)},
		}))
		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})

	t.Run("invalid key is returned unchanged", func(t *testing.T) {
		t.Parallel()
		ctx := ticketAuthedCtx("ext-1")
		h, wallet, users := newHandler(t)
		users.EXPECT().GetByExternalID(ctx, "ext-1").Return(&entity.User{ID: "user-1", ExternalID: "ext-1"}, nil)
		wallet.EXPECT().Register(ctx, entity.UserID("user-1"), mock.Anything, mock.Anything).
			Return(nil, apperr.New(codes.InvalidArgument, "not a P-256 point"))
		_, err := h.RegisterWalletPublicKey(ctx, connect.NewRequest(&ticketv1.RegisterWalletPublicKeyRequest{
			PublicKey: &entityv1.PublicKey{Value: append([]byte{0x04}, make([]byte, 64)...)},
		}))
		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
	})
}
