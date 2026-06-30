package rpc_test

import (
	"context"
	"testing"

	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	notificationv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/notification/v1"
	"connectrpc.com/connect"
	handler "github.com/liverty-music/backend/internal/adapter/rpc"
	"github.com/liverty-music/backend/internal/entity"
	entitymocks "github.com/liverty-music/backend/internal/entity/mocks"
	"github.com/liverty-music/backend/internal/infrastructure/auth"
	ucmocks "github.com/liverty-music/backend/internal/usecase/mocks"
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

func TestNotificationHandler_MarkRead(t *testing.T) {
	t.Parallel()

	const (
		extID   = "ext-user-1"
		userID  = "user-uuid-1"
		notifID = "01890000-0000-7000-8000-000000000abc"
	)

	tests := []struct {
		name     string
		ctx      context.Context
		req      *notificationv1.MarkReadRequest
		setup    func(uc *ucmocks.MockNotificationUseCase, ur *entitymocks.MockUserRepository)
		wantCode connect.Code
		wantErr  bool
	}{
		{
			name: "success delegates to usecase",
			ctx:  notificationAuthedCtx(extID),
			req: &notificationv1.MarkReadRequest{
				UserId:         &entityv1.UserId{Value: userID},
				NotificationId: &entityv1.NotificationId{Value: notifID},
			},
			setup: func(uc *ucmocks.MockNotificationUseCase, ur *entitymocks.MockUserRepository) {
				ur.EXPECT().GetByExternalID(mock.Anything, extID).Return(&entity.User{ID: userID, ExternalID: extID}, nil)
				uc.EXPECT().MarkRead(mock.Anything, userID, notifID).Return(nil).Once()
			},
		},
		{
			name:     "unauthenticated when no claims",
			ctx:      context.Background(),
			req:      &notificationv1.MarkReadRequest{UserId: &entityv1.UserId{Value: userID}, NotificationId: &entityv1.NotificationId{Value: notifID}},
			setup:    func(_ *ucmocks.MockNotificationUseCase, _ *entitymocks.MockUserRepository) {},
			wantCode: connect.CodeUnauthenticated,
			wantErr:  true,
		},
		{
			name: "permission denied when user_id does not match caller",
			ctx:  notificationAuthedCtx(extID),
			req: &notificationv1.MarkReadRequest{
				UserId:         &entityv1.UserId{Value: "someone-else"},
				NotificationId: &entityv1.NotificationId{Value: notifID},
			},
			setup: func(_ *ucmocks.MockNotificationUseCase, ur *entitymocks.MockUserRepository) {
				ur.EXPECT().GetByExternalID(mock.Anything, extID).Return(&entity.User{ID: userID, ExternalID: extID}, nil)
			},
			wantCode: connect.CodePermissionDenied,
			wantErr:  true,
		},
		{
			name: "invalid argument when user_id is empty",
			ctx:  notificationAuthedCtx(extID),
			req: &notificationv1.MarkReadRequest{
				UserId:         &entityv1.UserId{Value: ""},
				NotificationId: &entityv1.NotificationId{Value: notifID},
			},
			setup: func(_ *ucmocks.MockNotificationUseCase, ur *entitymocks.MockUserRepository) {
				ur.EXPECT().GetByExternalID(mock.Anything, extID).Return(&entity.User{ID: userID, ExternalID: extID}, nil)
			},
			wantCode: connect.CodeInvalidArgument,
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			h, uc, ur := newTestNotificationHandler(t)
			tt.setup(uc, ur)

			_, err := h.MarkRead(tt.ctx, connect.NewRequest(tt.req))

			if tt.wantErr {
				require.Error(t, err)
				assert.Equal(t, tt.wantCode, connect.CodeOf(err))
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestNotificationHandler_MarkDismissed_SuccessAndCrossUser(t *testing.T) {
	t.Parallel()

	const (
		extID   = "ext-user-2"
		userID  = "user-uuid-2"
		notifID = "01890000-0000-7000-8000-000000000def"
	)

	t.Run("success delegates to usecase", func(t *testing.T) {
		t.Parallel()
		h, uc, ur := newTestNotificationHandler(t)
		ur.EXPECT().GetByExternalID(mock.Anything, extID).Return(&entity.User{ID: userID, ExternalID: extID}, nil)
		uc.EXPECT().MarkDismissed(mock.Anything, userID, notifID).Return(nil).Once()

		_, err := h.MarkDismissed(notificationAuthedCtx(extID), connect.NewRequest(&notificationv1.MarkDismissedRequest{
			UserId:         &entityv1.UserId{Value: userID},
			NotificationId: &entityv1.NotificationId{Value: notifID},
		}))
		require.NoError(t, err)
	})

	t.Run("cross-user rejected before usecase", func(t *testing.T) {
		t.Parallel()
		h, _, ur := newTestNotificationHandler(t)
		ur.EXPECT().GetByExternalID(mock.Anything, extID).Return(&entity.User{ID: userID, ExternalID: extID}, nil)

		_, err := h.MarkDismissed(notificationAuthedCtx(extID), connect.NewRequest(&notificationv1.MarkDismissedRequest{
			UserId:         &entityv1.UserId{Value: "attacker"},
			NotificationId: &entityv1.NotificationId{Value: notifID},
		}))
		require.Error(t, err)
		assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
	})
}
