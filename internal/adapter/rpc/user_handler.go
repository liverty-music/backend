package rpc

import (
	"context"
	"errors"

	userv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/user/v1"
	"connectrpc.com/connect"
	"github.com/liverty-music/backend/internal/adapter/rpc/mapper"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-logging/logging"
)

// UserHandler implements the UserService Connect interface.
type UserHandler struct {
	userUseCase usecase.UserUseCase
	userRepo    entity.UserRepository
	logger      *logging.Logger
}

// NewUserHandler creates a new user handler.
func NewUserHandler(userUseCase usecase.UserUseCase, userRepo entity.UserRepository, logger *logging.Logger) *UserHandler {
	return &UserHandler{
		userUseCase: userUseCase,
		userRepo:    userRepo,
		logger:      logger,
	}
}

// Get retrieves a user by ID.
func (h *UserHandler) Get(ctx context.Context, req *connect.Request[userv1.GetRequest]) (*connect.Response[userv1.GetResponse], error) {
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

	return connect.NewResponse(&userv1.GetResponse{
		User: mapper.UserToProto(user),
	}), nil
}

// Create creates a new user.
// The optional home field allows persisting the user's home area atomically
// with account creation (selected during onboarding before sign-up).
func (h *UserHandler) Create(ctx context.Context, req *connect.Request[userv1.CreateRequest]) (*connect.Response[userv1.CreateResponse], error) {
	if req == nil || req.Msg == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("request cannot be nil"))
	}

	// Extract JWT claims from authenticated context (set by auth interceptor)
	// This is critical for security - we extract all identity fields from validated JWT claims
	// (external_id, email, name) and never trust client-provided identity data
	claims, err := mapper.GetClaimsFromContext(ctx)
	if err != nil {
		return nil, err
	}

	// Convert JWT claims and optional home to domain DTO
	newUser := mapper.NewUserFromCreateRequest(claims, req.Msg.Home)

	// Use the use case layer for business logic
	createdUser, err := h.userUseCase.Create(ctx, newUser)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&userv1.CreateResponse{
		User: mapper.UserToProto(createdUser),
	}), nil
}

// UpdateHome sets or changes the authenticated user's home area.
func (h *UserHandler) UpdateHome(ctx context.Context, req *connect.Request[userv1.UpdateHomeRequest]) (*connect.Response[userv1.UpdateHomeResponse], error) {
	if req == nil || req.Msg == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("request cannot be nil"))
	}

	if req.Msg.Home == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("home is required"))
	}

	// Extract user identity from JWT context
	claims, err := mapper.GetClaimsFromContext(ctx)
	if err != nil {
		return nil, err
	}

	// Resolve the internal users.id from the JWT sub claim (Zitadel external_id).
	user, err := h.userRepo.GetByExternalID(ctx, claims.Sub)
	if err != nil {
		return nil, err
	}
	if user == nil {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("user not found"))
	}

	home := mapper.ProtoHomeToEntity(req.Msg.Home)
	updatedUser, err := h.userUseCase.UpdateHome(ctx, user.ID, home)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&userv1.UpdateHomeResponse{
		User: mapper.UserToProto(updatedUser),
	}), nil
}
