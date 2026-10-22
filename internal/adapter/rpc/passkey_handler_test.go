package rpc_test

import (
	"context"
	"testing"
	"time"

	passkeyv1connect "buf.build/gen/go/liverty-music/schema/connectrpc/go/v2/liverty_music/rpc/passkey/v1/passkeyv1connect"
	passkeyv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/passkey/v1"
	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/liverty-music/backend/internal/adapter/rpc"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/usecase/mocks"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// newPasskeyBoundaryClient serves PasskeyService behind the fan server's authn
// middleware and validation interceptor, over mocked usecases.
func newPasskeyBoundaryClient(t *testing.T) (passkeyv1connect.PasskeyServiceClient, *mocks.MockPasskeyUseCase, *mocks.MockUserUseCase) {
	t.Helper()
	logger, err := logging.New()
	require.NoError(t, err)
	passkeyUC := mocks.NewMockPasskeyUseCase(t)
	userUC := mocks.NewMockUserUseCase(t)
	h := rpc.NewPasskeyHandler(passkeyUC, userUC, logger)
	srv := newFanBoundaryServer(t, func(s *connect.Server) {
		passkeyv1connect.RegisterPasskeyServiceHandler(s, h)
	})
	return passkeyv1connect.NewPasskeyServiceClient(connect.NewClient(connecthttp.NewTransport(srv.Client(), srv.URL))), passkeyUC, userUC
}

func TestPasskeyHandler_List(t *testing.T) {
	t.Parallel()

	// @spec components/adapter/fan/api/rpc/passkey "Fan lists their passkeys"
	t.Run("returns the caller's recorded passkeys with id, provider name when known and create time", func(t *testing.T) {
		t.Parallel()
		client, passkeyUC, userUC := newPasskeyBoundaryClient(t)
		created := time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)
		userUC.EXPECT().ResolveCaller(mock.Anything, testCallerExtID).Return(callerUser(), nil).Once()
		passkeyUC.EXPECT().List(mock.Anything, testCallerUserID).Return([]*entity.Passkey{
			{ProviderPasskeyID: "token-2", UserID: testCallerUserID, AAGUID: "ea9b8d66-4d01-1d21-3ce4-b6b48cb575d4", CreateTime: created},
			{ProviderPasskeyID: "token-1", UserID: testCallerUserID, AAGUID: entity.ZeroAAGUID, CreateTime: created.Add(-time.Hour)},
		}, nil).Once()

		resp, err := client.List(withFanToken(&passkeyv1.ListRequest{}))

		require.NoError(t, err)
		got := resp.GetPasskeys()
		require.Len(t, got, 2)
		assert.Equal(t, "token-2", got[0].GetId().GetValue())
		assert.Equal(t, "Google Password Manager", got[0].GetProviderName().GetValue())
		assert.True(t, created.Equal(got[0].GetCreateTime().AsTime()))
		assert.Equal(t, "token-1", got[1].GetId().GetValue())
		assert.False(t, got[1].HasProviderName(), "no provider name for an all-zero AAGUID")
	})

	// @spec components/adapter/fan/api/rpc/passkey "Not signed in"
	t.Run("fails with Unauthenticated without a sign-in token", func(t *testing.T) {
		t.Parallel()
		client, _, _ := newPasskeyBoundaryClient(t)

		_, err := client.List(context.Background(), &passkeyv1.ListRequest{})

		assert.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
	})

	// @spec components/adapter/fan/api/rpc/passkey "No account"
	t.Run("fails with NotFound when the signed-in caller has no User", func(t *testing.T) {
		t.Parallel()
		logger, err := logging.New()
		require.NoError(t, err)
		userUC := mocks.NewMockUserUseCase(t)
		h := rpc.NewPasskeyHandler(mocks.NewMockPasskeyUseCase(t), userUC, logger)
		userUC.EXPECT().ResolveCaller(mock.Anything, testCallerExtID).Return(nil, apperr.New(codes.NotFound, "no user")).Once()

		_, err = h.List(authedCtx(testCallerExtID), &passkeyv1.ListRequest{})

		// The server's error interceptor maps the apperr code to the wire code.
		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})
}
