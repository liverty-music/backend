package server_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	adminorganizerv1connect "buf.build/gen/go/liverty-music/schema/connectrpc/go/liverty_music/rpc/admin/organizer/v1/organizerv1connect"
	adminuserv1connect "buf.build/gen/go/liverty-music/schema/connectrpc/go/liverty_music/rpc/admin/user/v1/userv1connect"
	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	adminorganizerv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/admin/organizer/v1"
	adminuserv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/admin/user/v1"
	"connectrpc.com/connect"

	"github.com/liverty-music/backend/internal/adapter/rpc"
	"github.com/liverty-music/backend/internal/infrastructure/auth"
	authmocks "github.com/liverty-music/backend/internal/infrastructure/auth/mocks"
	"github.com/liverty-music/backend/internal/infrastructure/server"
	"github.com/liverty-music/backend/internal/infrastructure/server/ratelimit"
	usecasemocks "github.com/liverty-music/backend/internal/usecase/mocks"
	"github.com/liverty-music/backend/pkg/config"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

const (
	deleteAdminToken    = "delete-admin-token"
	deleteNonAdminToken = "delete-viewer-token"
	deleteOrganizerID   = "019a0000-0000-7000-8000-0000000000d1"
	deleteUserID        = "019a0000-0000-7000-8000-0000000000d2"
)

// newTestAdminDeleteServer builds the admin Connect server as provider.go does
// (no public procedures, server-wide admin-role interceptor, the server's own
// validation interceptor) with the admin OrganizerService and UserService
// mounted on the given usecase mocks.
func newTestAdminDeleteServer(t *testing.T, organizerUC *usecasemocks.MockOrganizerUseCase, userUC *usecasemocks.MockUserUseCase) *httptest.Server {
	t.Helper()
	logger, err := logging.New()
	require.NoError(t, err)

	validator := authmocks.NewMockTokenValidator(t)
	validator.EXPECT().ValidateToken(mock.Anything, deleteAdminToken).
		Return(&auth.Claims{Sub: "admin-user", Roles: []string{"admin"}}, nil).Maybe()
	validator.EXPECT().ValidateToken(mock.Anything, deleteNonAdminToken).
		Return(&auth.Claims{Sub: "regular-user", Roles: []string{"viewer"}}, nil).Maybe()

	rateLimiter := ratelimit.NewLimiter(ratelimit.Config{AuthRPS: 100, AuthBurst: 100, AnonRPS: 100, AnonBurst: 100}, time.Minute)
	t.Cleanup(func() { _ = rateLimiter.Close() })
	healthHandler := func(_ ...connect.HandlerOption) (string, http.Handler) {
		return "/unused-health-check/", http.NotFoundHandler()
	}
	handlers := []server.RPCHandlerFunc{
		func(opts ...connect.HandlerOption) (string, http.Handler) {
			return adminorganizerv1connect.NewOrganizerServiceHandler(rpc.NewAdminOrganizerHandler(organizerUC, logger), opts...)
		},
		func(opts ...connect.HandlerOption) (string, http.Handler) {
			return adminuserv1connect.NewUserServiceHandler(rpc.NewAdminUserHandler(userUC, logger), opts...)
		},
	}
	cfg := config.ServerSettings{
		Host: "127.0.0.1", HandlerTimeout: 5 * time.Second, ReadHeaderTimeout: 2 * time.Second,
		ReadTimeout: 2 * time.Second, IdleTimeout: 5 * time.Second,
	}
	srv := server.NewConnectServer(cfg, logger, auth.NewAuthFunc(validator, nil), rateLimiter, healthHandler,
		[]connect.Interceptor{auth.NewRequireRoleInterceptor("admin")}, nil, handlers...)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts
}

func TestAdminServer_OrganizerDelete(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		organizerID *entityv1.OrganizerId
		setup       func(uc *usecasemocks.MockOrganizerUseCase)
		wantCode    connect.Code
	}{
		{
			// @spec components/adapter/admin/api/rpc/organizer "Admin deletes a deactivated Organizer"
			name:        "runs OrganizerUseCase.Delete and returns an empty response",
			organizerID: &entityv1.OrganizerId{Value: deleteOrganizerID},
			setup: func(uc *usecasemocks.MockOrganizerUseCase) {
				uc.EXPECT().Delete(mock.Anything, deleteOrganizerID).Return(nil).Once()
			},
		},
		{
			// @spec components/adapter/admin/api/rpc/organizer "Delete refused"
			name:        "returns FailedPrecondition when the usecase refuses",
			organizerID: &entityv1.OrganizerId{Value: deleteOrganizerID},
			setup: func(uc *usecasemocks.MockOrganizerUseCase) {
				uc.EXPECT().Delete(mock.Anything, deleteOrganizerID).
					Return(apperr.New(codes.FailedPrecondition, "organizer is not deactivated")).Once()
			},
			wantCode: connect.CodeFailedPrecondition,
		},
		{
			// @spec components/adapter/admin/api/rpc/organizer "Missing OrganizerId"
			name:     "returns InvalidArgument without an OrganizerId and runs no usecase",
			setup:    func(*usecasemocks.MockOrganizerUseCase) {},
			wantCode: connect.CodeInvalidArgument,
		},
		{
			name:        "returns InvalidArgument for a malformed OrganizerId and runs no usecase",
			organizerID: &entityv1.OrganizerId{Value: "not-a-uuid"},
			setup:       func(*usecasemocks.MockOrganizerUseCase) {},
			wantCode:    connect.CodeInvalidArgument,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			organizerUC := usecasemocks.NewMockOrganizerUseCase(t)
			tt.setup(organizerUC)
			ts := newTestAdminDeleteServer(t, organizerUC, usecasemocks.NewMockUserUseCase(t))

			req := connect.NewRequest(&adminorganizerv1.DeleteRequest{OrganizerId: tt.organizerID})
			req.Header().Set("Authorization", "Bearer "+deleteAdminToken)
			_, err := adminorganizerv1connect.NewOrganizerServiceClient(ts.Client(), ts.URL).Delete(context.Background(), req)

			if tt.wantCode == 0 {
				assert.NoError(t, err)
				return
			}
			assert.Equal(t, tt.wantCode, connect.CodeOf(err))
		})
	}
}

func TestAdminServer_UserDelete(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		token    string
		userID   *entityv1.UserId
		setup    func(uc *usecasemocks.MockUserUseCase)
		wantCode connect.Code
	}{
		{
			// @spec components/adapter/admin/api/rpc/user "Not signed in"
			name:     "returns Unauthenticated without a sign-in and runs no usecase",
			userID:   &entityv1.UserId{Value: deleteUserID},
			setup:    func(*usecasemocks.MockUserUseCase) {},
			wantCode: connect.CodeUnauthenticated,
		},
		{
			// @spec components/adapter/admin/api/rpc/user "Non-admin"
			name:     "returns PermissionDenied for a non-admin and runs no usecase",
			token:    deleteNonAdminToken,
			userID:   &entityv1.UserId{Value: deleteUserID},
			setup:    func(*usecasemocks.MockUserUseCase) {},
			wantCode: connect.CodePermissionDenied,
		},
		{
			// @spec components/adapter/admin/api/rpc/user "Admin removes a test user"
			name:   "runs UserUseCase.Delete and returns an empty response",
			token:  deleteAdminToken,
			userID: &entityv1.UserId{Value: deleteUserID},
			setup: func(uc *usecasemocks.MockUserUseCase) {
				uc.EXPECT().Delete(mock.Anything, deleteUserID).Return(nil).Once()
			},
		},
		{
			// @spec components/adapter/admin/api/rpc/user "Missing UserId"
			name:     "returns InvalidArgument without a UserId and runs no usecase",
			token:    deleteAdminToken,
			setup:    func(*usecasemocks.MockUserUseCase) {},
			wantCode: connect.CodeInvalidArgument,
		},
		{
			name:     "returns InvalidArgument for a UserId that is not a UUID and runs no usecase",
			token:    deleteAdminToken,
			userID:   &entityv1.UserId{Value: "not-a-uuid"},
			setup:    func(*usecasemocks.MockUserUseCase) {},
			wantCode: connect.CodeInvalidArgument,
		},
		{
			// @spec components/adapter/admin/api/rpc/user "User holds a ticket"
			name:   "returns FailedPrecondition when the usecase refuses",
			token:  deleteAdminToken,
			userID: &entityv1.UserId{Value: deleteUserID},
			setup: func(uc *usecasemocks.MockUserUseCase) {
				uc.EXPECT().Delete(mock.Anything, deleteUserID).
					Return(apperr.New(codes.FailedPrecondition, "user holds an issued ticket")).Once()
			},
			wantCode: connect.CodeFailedPrecondition,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			userUC := usecasemocks.NewMockUserUseCase(t)
			tt.setup(userUC)
			ts := newTestAdminDeleteServer(t, usecasemocks.NewMockOrganizerUseCase(t), userUC)

			req := connect.NewRequest(&adminuserv1.DeleteRequest{UserId: tt.userID})
			if tt.token != "" {
				req.Header().Set("Authorization", "Bearer "+tt.token)
			}
			_, err := adminuserv1connect.NewUserServiceClient(ts.Client(), ts.URL).Delete(context.Background(), req)

			if tt.wantCode == 0 {
				assert.NoError(t, err)
				return
			}
			assert.Equal(t, tt.wantCode, connect.CodeOf(err))
		})
	}
}
