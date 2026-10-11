package rpc_test

import (
	"context"
	"testing"

	entitypb "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	rpcv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/push_subscription/v1"
	"connectrpc.com/connect"
	handler "github.com/liverty-music/backend/internal/adapter/rpc"
	"github.com/liverty-music/backend/internal/entity"
	entitymocks "github.com/liverty-music/backend/internal/entity/mocks"
	"github.com/liverty-music/backend/internal/infrastructure/auth"
	ucmocks "github.com/liverty-music/backend/internal/usecase/mocks"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func authedCtx(externalID string) context.Context {
	return auth.WithClaims(context.Background(), &auth.Claims{Sub: externalID})
}

const (
	pushTestExtID    = "ext-user-1"
	pushTestUserID   = "user-uuid-1"
	pushTestEndpoint = "https://push.example.com/sub"
)

// newPushSubscriptionHandler wires a PushSubscriptionHandler with fresh mocks.
func newPushSubscriptionHandler(t *testing.T) (*handler.PushSubscriptionHandler, *ucmocks.MockPushSubscriptionUseCase, *entitymocks.MockUserRepository) {
	t.Helper()
	logger, err := logging.New()
	require.NoError(t, err)
	uc := ucmocks.NewMockPushSubscriptionUseCase(t)
	ur := entitymocks.NewMockUserRepository(t)
	return handler.NewPushSubscriptionHandler(uc, ur, logger), uc, ur
}

// expectPushCaller makes the user repository resolve the signed-in caller.
func expectPushCaller(ur *entitymocks.MockUserRepository) {
	ur.EXPECT().GetByProviderUserID(mock.Anything, pushTestExtID).
		Return(&entity.User{ID: pushTestUserID, ProviderUserID: pushTestExtID}, nil).Once()
}

func TestPushSubscriptionHandler_Create(t *testing.T) {
	t.Parallel()

	t.Run("fan registers their browser", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/fan/api/rpc/push-subscription "Fan registers their browser"
		h, uc, ur := newPushSubscriptionHandler(t)
		expectPushCaller(ur)
		uc.EXPECT().Create(mock.Anything, pushTestUserID, pushTestEndpoint, "key", "authSecret", "other").
			Return(&entity.PushSubscription{
				ID: "sub-uuid-1", UserID: pushTestUserID, Endpoint: pushTestEndpoint, P256dh: "key", Auth: "authSecret",
			}, nil).Once()

		resp, err := h.Create(authedCtx(pushTestExtID), connect.NewRequest(&rpcv1.CreateRequest{
			Endpoint: &entitypb.PushEndpoint{Value: pushTestEndpoint},
			Keys:     &entitypb.PushKeys{P256Dh: "key", Auth: "authSecret"},
		}))

		require.NoError(t, err)
		assert.Equal(t, "sub-uuid-1", resp.Msg.GetSubscription().GetId().GetValue())
		assert.Equal(t, pushTestUserID, resp.Msg.GetSubscription().GetUserId().GetValue())
	})

	t.Run("missing keys", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/fan/api/rpc/push-subscription "Missing keys"
		h, _, ur := newPushSubscriptionHandler(t)
		expectPushCaller(ur)

		_, err := h.Create(authedCtx(pushTestExtID), connect.NewRequest(&rpcv1.CreateRequest{
			Endpoint: &entitypb.PushEndpoint{Value: pushTestEndpoint},
		}))

		assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
	})

	t.Run("not signed in", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/fan/api/rpc/push-subscription "Not signed in"
		h, _, _ := newPushSubscriptionHandler(t)

		_, err := h.Create(context.Background(), connect.NewRequest(&rpcv1.CreateRequest{
			Endpoint: &entitypb.PushEndpoint{Value: pushTestEndpoint},
			Keys:     &entitypb.PushKeys{P256Dh: "k", Auth: "a"},
		}))

		assert.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
	})

	t.Run("return NOT_FOUND when the caller has no account", func(t *testing.T) {
		t.Parallel()
		h, _, ur := newPushSubscriptionHandler(t)
		ur.EXPECT().GetByProviderUserID(mock.Anything, pushTestExtID).Return(nil, apperr.ErrNotFound).Once()

		_, err := h.Create(authedCtx(pushTestExtID), connect.NewRequest(&rpcv1.CreateRequest{
			Endpoint: &entitypb.PushEndpoint{Value: pushTestEndpoint},
			Keys:     &entitypb.PushKeys{P256Dh: "k", Auth: "a"},
		}))

		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})
}

func TestPushSubscriptionHandler_GetAndDelete(t *testing.T) {
	t.Parallel()

	t.Run("fan checks their browser", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/fan/api/rpc/push-subscription "Fan checks their browser"
		h, uc, ur := newPushSubscriptionHandler(t)
		expectPushCaller(ur)
		uc.EXPECT().Get(mock.Anything, pushTestUserID, pushTestEndpoint).
			Return(&entity.PushSubscription{ID: "sub-uuid-1", UserID: pushTestUserID, Endpoint: pushTestEndpoint}, nil).Once()

		resp, err := h.Get(authedCtx(pushTestExtID), connect.NewRequest(&rpcv1.GetRequest{
			Endpoint: &entitypb.PushEndpoint{Value: pushTestEndpoint},
		}))

		require.NoError(t, err)
		assert.Equal(t, "sub-uuid-1", resp.Msg.GetSubscription().GetId().GetValue())
	})

	t.Run("push address of another fan", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/fan/api/rpc/push-subscription "Push address of another fan"
		const othersEndpoint = "https://push.example.com/someone-else"
		h, uc, ur := newPushSubscriptionHandler(t)
		ur.EXPECT().GetByProviderUserID(mock.Anything, pushTestExtID).
			Return(&entity.User{ID: pushTestUserID, ProviderUserID: pushTestExtID}, nil).Twice()
		// Both calls are scoped to the caller's own User, so another fan's
		// subscription is neither found nor removed.
		uc.EXPECT().Get(mock.Anything, pushTestUserID, othersEndpoint).Return(nil, apperr.ErrNotFound).Once()
		uc.EXPECT().Delete(mock.Anything, pushTestUserID, othersEndpoint, "other").Return(nil).Once()

		_, getErr := h.Get(authedCtx(pushTestExtID), connect.NewRequest(&rpcv1.GetRequest{
			Endpoint: &entitypb.PushEndpoint{Value: othersEndpoint},
		}))
		_, delErr := h.Delete(authedCtx(pushTestExtID), connect.NewRequest(&rpcv1.DeleteRequest{
			Endpoint: &entitypb.PushEndpoint{Value: othersEndpoint},
		}))

		assert.Equal(t, connect.CodeNotFound, connect.CodeOf(getErr))
		assert.NoError(t, delErr)
	})

	t.Run("missing push address", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/fan/api/rpc/push-subscription "Missing push address"
		h, _, ur := newPushSubscriptionHandler(t)
		expectPushCaller(ur)

		_, err := h.Delete(authedCtx(pushTestExtID), connect.NewRequest(&rpcv1.DeleteRequest{}))

		assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
	})

	t.Run("return UNAUTHENTICATED without a sign-in", func(t *testing.T) {
		t.Parallel()
		h, _, _ := newPushSubscriptionHandler(t)

		_, getErr := h.Get(context.Background(), connect.NewRequest(&rpcv1.GetRequest{
			Endpoint: &entitypb.PushEndpoint{Value: pushTestEndpoint},
		}))
		_, delErr := h.Delete(context.Background(), connect.NewRequest(&rpcv1.DeleteRequest{
			Endpoint: &entitypb.PushEndpoint{Value: pushTestEndpoint},
		}))

		assert.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(getErr))
		assert.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(delErr))
	})

	t.Run("remove the caller's own subscription", func(t *testing.T) {
		t.Parallel()
		h, uc, ur := newPushSubscriptionHandler(t)
		expectPushCaller(ur)
		uc.EXPECT().Delete(mock.Anything, pushTestUserID, pushTestEndpoint, "other").Return(nil).Once()

		resp, err := h.Delete(authedCtx(pushTestExtID), connect.NewRequest(&rpcv1.DeleteRequest{
			Endpoint: &entitypb.PushEndpoint{Value: pushTestEndpoint},
		}))

		require.NoError(t, err)
		assert.NotNil(t, resp)
	})
}
