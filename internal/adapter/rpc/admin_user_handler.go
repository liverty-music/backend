package rpc

import (
	"context"

	adminuserv1connect "buf.build/gen/go/liverty-music/schema/connectrpc/go/v2/liverty_music/rpc/admin/user/v1/userv1connect"
	adminuserv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/admin/user/v1"

	"github.com/liverty-music/backend/internal/adapter/rpc/mapper"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-logging/logging"
)

// Compile-time assertion that AdminUserHandler satisfies the generated interface.
var _ adminuserv1connect.UserServiceHandler = (*AdminUserHandler)(nil)

// AdminUserHandler implements the admin-facing UserService Connect interface.
// Admin authorization is enforced at the admin server boundary by its
// server-wide RequireRoleInterceptor (admin role), and the UserId and Email
// are validated by the server's protovalidate interceptor, so the handler only
// delegates to the use case.
type AdminUserHandler struct {
	userUseCase usecase.UserUseCase
	logger      *logging.Logger
}

// NewAdminUserHandler creates a new AdminUserHandler.
func NewAdminUserHandler(userUseCase usecase.UserUseCase, logger *logging.Logger) *AdminUserHandler {
	return &AdminUserHandler{userUseCase: userUseCase, logger: logger}
}

// GetByEmail returns the fan User registered with the address and what
// currently stops its deletion. Returns NotFound when no user has the
// address.
func (h *AdminUserHandler) GetByEmail(
	ctx context.Context,
	req *adminuserv1.GetByEmailRequest,
) (*adminuserv1.GetByEmailResponse, error) {
	user, err := h.userUseCase.GetByEmail(ctx, req.GetEmail().GetValue())
	if err != nil {
		return nil, err
	}
	blockers, err := h.userUseCase.ListDeletionBlockers(ctx, user.ID)
	if err != nil {
		return nil, err
	}
	return &adminuserv1.GetByEmailResponse{
		User:     mapper.UserToProto(user),
		Blockers: mapper.DeletionBlockersToProto(blockers),
	}, nil
}

// Delete permanently removes a fan User and its sign-in identity. Returns
// NotFound when no user with the given id exists and FailedPrecondition while
// the user has a deletion blocker.
func (h *AdminUserHandler) Delete(
	ctx context.Context,
	req *adminuserv1.DeleteRequest,
) (*adminuserv1.DeleteResponse, error) {
	if err := h.userUseCase.Delete(ctx, req.GetUserId().GetValue()); err != nil {
		return nil, err
	}
	return &adminuserv1.DeleteResponse{}, nil
}
