package rpc_test

import (
	"context"
	"testing"

	notificationv1connect "buf.build/gen/go/liverty-music/schema/connectrpc/go/v2/liverty_music/rpc/notification/v1/notificationv1connect"
	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	notificationv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/notification/v1"
	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
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

func notificationAuthedCtx(sub string) context.Context {
	return auth.WithClaims(context.Background(), &auth.Claims{Sub: sub})
}

func newTestNotificationHandler(t *testing.T) (*handler.NotificationHandler, *ucmocks.MockNotificationUseCase, *entitymocks.MockUserRepository) {
	t.Helper()
	uc := ucmocks.NewMockNotificationUseCase(t)
	ur := entitymocks.NewMockUserRepository(t)
	logger, err := logging.New()
	require.NoError(t, err)
	return handler.NewNotificationHandler(uc, ur, logger), uc, ur
}

func TestNotificationHandler_MarkReadAndDismiss(t *testing.T) {
	t.Parallel()

	const (
		extID   = "ext-user-1"
		userID  = "user-uuid-1"
		notifID = "01890000-0000-7000-8000-000000000abc"
	)
	expectCaller := func(ur *entitymocks.MockUserRepository) {
		ur.EXPECT().GetByProviderUserID(mock.Anything, extID).Return(&entity.User{ID: userID, ProviderUserID: extID}, nil).Once()
	}

	t.Run("fan marks their notification read", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/fan/api/rpc/notification "Fan marks their notification read"
		h, uc, ur := newTestNotificationHandler(t)
		expectCaller(ur)
		uc.EXPECT().MarkRead(mock.Anything, userID, notifID).Return(nil).Once()

		_, err := h.MarkRead(notificationAuthedCtx(extID), &notificationv1.MarkReadRequest{
			NotificationId: &entityv1.NotificationId{Value: notifID},
		})

		require.NoError(t, err)
	})

	t.Run("fan dismisses their notification", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/fan/api/rpc/notification "Fan dismisses their notification"
		h, uc, ur := newTestNotificationHandler(t)
		expectCaller(ur)
		uc.EXPECT().Dismiss(mock.Anything, userID, notifID).Return(nil).Once()

		_, err := h.Dismiss(notificationAuthedCtx(extID), &notificationv1.DismissRequest{
			NotificationId: &entityv1.NotificationId{Value: notifID},
		})

		require.NoError(t, err)
	})

	t.Run("not signed in", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/fan/api/rpc/notification "Not signed in"
		h, _, _ := newTestNotificationHandler(t)

		_, readErr := h.MarkRead(context.Background(), &notificationv1.MarkReadRequest{
			NotificationId: &entityv1.NotificationId{Value: notifID},
		})
		_, dismissErr := h.Dismiss(context.Background(), &notificationv1.DismissRequest{
			NotificationId: &entityv1.NotificationId{Value: notifID},
		})

		assert.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(readErr))
		assert.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(dismissErr))
	})

	t.Run("missing notification", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/fan/api/rpc/notification "Missing notification"
		// The mocks fail the test if a Notification changes.
		h, _, _ := newTestNotificationHandler(t)
		srv := newFanBoundaryServer(t, func(s *connect.Server) {
			notificationv1connect.RegisterNotificationServiceHandler(s, h)
		})
		client := notificationv1connect.NewNotificationServiceClient(connect.NewClient(connecthttp.NewTransport(srv.Client(), srv.URL)))

		_, readErr := client.MarkRead(withFanToken(&notificationv1.MarkReadRequest{}))
		_, dismissErr := client.Dismiss(withFanToken(&notificationv1.DismissRequest{}))

		assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(readErr))
		assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(dismissErr))
	})

	t.Run("return NOT_FOUND when the caller has no account", func(t *testing.T) {
		t.Parallel()
		h, _, ur := newTestNotificationHandler(t)
		ur.EXPECT().GetByProviderUserID(mock.Anything, extID).Return(nil, apperr.ErrNotFound).Once()

		_, err := h.Dismiss(notificationAuthedCtx(extID), &notificationv1.DismissRequest{
			NotificationId: &entityv1.NotificationId{Value: notifID},
		})

		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})
}
