package rpc

import (
	"context"

	adminuserv1connect "buf.build/gen/go/liverty-music/schema/connectrpc/go/liverty_music/rpc/admin/user/v1/userv1connect"
	adminuserv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/admin/user/v1"
	"connectrpc.com/connect"

	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-logging/logging"
)

// Compile-time assertion that AdminUserHandler satisfies the generated interface.
var _ adminuserv1connect.UserServiceHandler = (*AdminUserHandler)(nil)

// AdminUserHandler implements the admin-facing UserService Connect interface.
// Admin authorization is enforced at the admin server boundary by its
// server-wide RequireRoleInterceptor (admin role), and the UserId is validated
// by the server's protovalidate interceptor, so the handler only delegates to
// the use case.
type AdminUserHandler struct {
	userUseCase usecase.UserUseCase
	logger      *logging.Logger
}

// NewAdminUserHandler creates a new AdminUserHandler.
func NewAdminUserHandler(userUseCase usecase.UserUseCase, logger *logging.Logger) *AdminUserHandler {
	return &AdminUserHandler{userUseCase: userUseCase, logger: logger}
}

// Delete permanently removes a fan User and its sign-in identity. Returns
// NotFound when no user with the given id exists and FailedPrecondition while
// the user holds an issued ticket or has an order that is not refunded.
func (h *AdminUserHandler) Delete(
	ctx context.Context,
	req *connect.Request[adminuserv1.DeleteRequest],
) (*connect.Response[adminuserv1.DeleteResponse], error) {
	if err := h.userUseCase.Delete(ctx, req.Msg.GetUserId().GetValue()); err != nil {
		return nil, err
	}
	return connect.NewResponse(&adminuserv1.DeleteResponse{}), nil
}
