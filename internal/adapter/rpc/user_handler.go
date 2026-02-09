package rpc

import (
	"context"
	"errors"

	rpc "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/user/v1"
	"connectrpc.com/connect"
	"github.com/liverty-music/backend/internal/adapter/rpc/mapper"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-logging/logging"
)

// UserHandler implements the UserService Connect interface.
type UserHandler struct {
	userUseCase usecase.UserUseCase
	logger      *logging.Logger
}

// NewUserHandler creates a new user handler.
func NewUserHandler(userUseCase usecase.UserUseCase, logger *logging.Logger) *UserHandler {
	return &UserHandler{
		userUseCase: userUseCase,
		logger:      logger,
	}
}

// Get retrieves a user by ID.
func (h *UserHandler) Get(ctx context.Context, req *connect.Request[rpc.GetRequest]) (*connect.Response[rpc.GetResponse], error) {
	if req == nil || req.Msg == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("request cannot be nil"))
	}

	if req.Msg.UserId == nil || req.Msg.UserId.GetValue() == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("user_id is required"))
	}

	// Use the use case layer for business logic
	user, err := h.userUseCase.Get(ctx, req.Msg.UserId.GetValue())
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&rpc.GetResponse{
		User: mapper.UserToProto(user),
	}), nil
}

// Create creates a new user.
func (h *UserHandler) Create(ctx context.Context, req *connect.Request[rpc.CreateRequest]) (*connect.Response[rpc.CreateResponse], error) {
	if req == nil || req.Msg == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("request cannot be nil"))
	}

	if req.Msg.User == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("user is required"))
	}

	// Convert protobuf to domain DTO
	newUser := mapper.NewUserFromProto(req.Msg.User)

	// Use the use case layer for business logic
	createdUser, err := h.userUseCase.Create(ctx, newUser)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&rpc.CreateResponse{
		User: mapper.UserToProto(createdUser),
	}), nil
}
