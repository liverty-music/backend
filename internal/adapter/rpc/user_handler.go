package rpc

import (
	"context"
	"errors"
	"sync"
	"time"

	userv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/user/v1"
	"connectrpc.com/connect"
	"github.com/liverty-music/backend/internal/adapter/rpc/mapper"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-logging/logging"
)

const (
	resendRateLimit  = 3
	resendRateWindow = 10 * time.Minute
)

// UserHandler implements the UserService Connect interface.
type UserHandler struct {
	userUseCase   usecase.UserUseCase
	emailVerifier usecase.EmailVerifier
	logger        *logging.Logger

	// resendMu protects resendLog for concurrent access.
	resendMu  sync.Mutex
	resendLog map[string][]time.Time
}

// NewUserHandler creates a new user handler.
// emailVerifier may be nil when the Zitadel API client is not configured (local dev).
func NewUserHandler(userUseCase usecase.UserUseCase, emailVerifier usecase.EmailVerifier, logger *logging.Logger) *UserHandler {
	return &UserHandler{
		userUseCase:   userUseCase,
		emailVerifier: emailVerifier,
		logger:        logger,
		resendLog:     make(map[string][]time.Time),
	}
}

// Get retrieves the authenticated user's profile using their JWT identity.
func (h *UserHandler) Get(ctx context.Context, req *connect.Request[userv1.GetRequest]) (*connect.Response[userv1.GetResponse], error) {
	if req == nil || req.Msg == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("request cannot be nil"))
	}

	// Extract user identity from JWT context.
	claims, err := mapper.GetClaimsFromContext(ctx)
	if err != nil {
		return nil, err
	}

	// Resolve the user by their external identity provider ID (JWT sub claim).
	user, err := h.userUseCase.GetByExternalID(ctx, claims.Sub)
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
	user, err := h.userUseCase.GetByExternalID(ctx, claims.Sub)
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

// ResendEmailVerification triggers a new email verification message for the
// authenticated user via the Zitadel API.
func (h *UserHandler) ResendEmailVerification(ctx context.Context, req *connect.Request[userv1.ResendEmailVerificationRequest]) (*connect.Response[userv1.ResendEmailVerificationResponse], error) {
	if h.emailVerifier == nil {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("email verification service is not configured"))
	}

	claims, err := mapper.GetClaimsFromContext(ctx)
	if err != nil {
		return nil, err
	}

	if !h.allowResend(claims.Sub) {
		return nil, connect.NewError(connect.CodeResourceExhausted, errors.New("resend rate limit exceeded"))
	}

	if err := h.emailVerifier.ResendVerification(ctx, claims.Sub); err != nil {
		return nil, err
	}

	return connect.NewResponse(&userv1.ResendEmailVerificationResponse{}), nil
}

// allowResend checks whether the user has not exceeded the resend rate limit.
// Returns true if the request is allowed, false if rate-limited.
func (h *UserHandler) allowResend(userID string) bool {
	now := time.Now()
	cutoff := now.Add(-resendRateWindow)

	h.resendMu.Lock()
	defer h.resendMu.Unlock()

	// Filter out expired entries.
	var recent []time.Time
	for _, t := range h.resendLog[userID] {
		if t.After(cutoff) {
			recent = append(recent, t)
		}
	}

	// Reclaim memory for users with no recent activity.
	if len(recent) == 0 {
		delete(h.resendLog, userID)
	}

	if len(recent) >= resendRateLimit {
		h.resendLog[userID] = recent
		return false
	}

	h.resendLog[userID] = append(recent, now)
	return true
}
