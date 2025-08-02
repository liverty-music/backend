package rpc

import (
	"context"
	"errors"

	"connectrpc.com/connect"
	"github.com/pannpers/go-backend-scaffold/internal/adapter/rpc/mapper"
	"github.com/pannpers/go-backend-scaffold/internal/usecase"
	"github.com/pannpers/go-backend-scaffold/pkg/logging"
	api "buf.build/gen/go/pannpers/scaffold/protocolbuffers/go/pannpers/api/v1"
)

// UserHandler implements the UserService Connect interface.
type UserHandler struct {
	userUseCase *usecase.UserUseCase
	logger      *logging.Logger
}

// NewUserHandler creates a new user handler.
func NewUserHandler(userUseCase *usecase.UserUseCase, logger *logging.Logger) *UserHandler {
	return &UserHandler{
		userUseCase: userUseCase,
		logger:      logger,
	}
}

// GetUser retrieves a user by ID.
func (h *UserHandler) GetUser(ctx context.Context, req *connect.Request[api.GetUserRequest]) (*connect.Response[api.GetUserResponse], error) {
	if req == nil || req.Msg == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("request cannot be nil"))
	}

	if req.Msg.UserId == nil || req.Msg.UserId.GetValue() == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("user_id is required"))
	}

	// Use the use case layer for business logic
	user, err := h.userUseCase.GetUser(ctx, req.Msg.UserId.GetValue())
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&api.GetUserResponse{
		User: mapper.UserToProto(user),
	}), nil
}

// CreateUser creates a new user.
func (h *UserHandler) CreateUser(ctx context.Context, req *connect.Request[api.CreateUserRequest]) (*connect.Response[api.CreateUserResponse], error) {
	if req == nil || req.Msg == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("request cannot be nil"))
	}

	if req.Msg.User == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("user is required"))
	}

	// Convert protobuf to domain DTO
	newUser := mapper.NewUserFromProto(req.Msg.User)

	// Use the use case layer for business logic
	createdUser, err := h.userUseCase.CreateUser(ctx, newUser)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&api.CreateUserResponse{
		User: mapper.UserToProto(createdUser),
	}), nil
}
