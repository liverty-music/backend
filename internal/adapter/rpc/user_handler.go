package rpc

import (
	"context"
	"errors"
	"sync"
	"time"

	userv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/user/v1"
	"connectrpc.com/connect"
	"github.com/liverty-music/backend/internal/adapter/rpc/mapper"
	"github.com/liverty-music/backend/internal/entity"
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

// Get retrieves the authenticated user's profile.
//
// The request-supplied user_id is verified against the JWT-derived userID;
// mismatches are rejected with PERMISSION_DENIED per the rpc-auth-scoping
// convention.
func (h *UserHandler) Get(ctx context.Context, req *connect.Request[userv1.GetRequest]) (*connect.Response[userv1.GetResponse], error) {
	externalID, err := mapper.GetExternalUserID(ctx)
	if err != nil {
		return nil, err
	}

	user, err := h.userUseCase.GetByExternalID(ctx, externalID)
	if err != nil {
		return nil, err
	}

	if err := mapper.RequireUserIDMatch(user.ID, req.Msg.GetUserId().GetValue()); err != nil {
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
	// Extract JWT claims from authenticated context (set by auth interceptor).
	// This is critical for security — we extract all identity fields from validated JWT claims
	// (external_id, email, name) and never trust client-provided identity data.
	claims, err := mapper.GetClaimsFromContext(ctx)
	if err != nil {
		return nil, err
	}

	// TODO(persist-user-language): replace placeholder with req.GetPreferredLanguage() after BSR gen.
	// Once the proto schema package publishes CreateRequest.preferred_language (field 3),
	// change the third argument to: req.Msg.GetPreferredLanguage()
	var preferredLanguage string // placeholder: will be req.Msg.GetPreferredLanguage() after BSR gen

	// Convert JWT claims, optional home, and preferred language to domain DTO.
	newUser := mapper.NewUserFromCreateRequest(claims, req.Msg.Home, preferredLanguage)

	// Use the use case layer for business logic.
	createdUser, err := h.userUseCase.Create(ctx, newUser)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&userv1.CreateResponse{
		User: mapper.UserToProto(createdUser),
	}), nil
}

// UpdatePreferredLanguageParams holds the parsed parameters for the
// UpdatePreferredLanguage operation. This struct exists only until BSR gen lands
// and the proto-generated request type becomes available.
//
// TODO(persist-user-language): remove this struct and replace call sites with
// *connect.Request[userv1.UpdatePreferredLanguageRequest] after BSR gen.
type UpdatePreferredLanguageParams struct {
	// UserID is the caller's user identifier from the request body.
	UserID string
	// PreferredLanguage is the new ISO 639-1 two-letter language code.
	PreferredLanguage string
}

// UpdatePreferredLanguage sets or changes the authenticated user's preferred display language.
//
// It follows the rpc-auth-scoping convention: the userID embedded in the request
// is verified against the caller's JWT-derived identity before any DB write.
//
// TODO(persist-user-language): replace this method with the generated Connect handler
// after BSR gen. The eventual signature (mirroring UpdateHome) will be:
//
//	func (h *UserHandler) UpdatePreferredLanguage(
//	    ctx context.Context,
//	    req *connect.Request[userv1.UpdatePreferredLanguageRequest],
//	) (*connect.Response[userv1.UpdatePreferredLanguageResponse], error)
//
// After BSR gen, inline the body of this method there and delete
// UpdatePreferredLanguageParams. The only change required is reading
// req.Msg.GetUserId().GetValue() and req.Msg.GetPreferredLanguage() from
// the generated proto type instead of the params struct.
func (h *UserHandler) UpdatePreferredLanguage(ctx context.Context, params UpdatePreferredLanguageParams) (*entity.User, error) {
	externalID, err := mapper.GetExternalUserID(ctx)
	if err != nil {
		return nil, err
	}

	caller, err := h.userUseCase.GetByExternalID(ctx, externalID)
	if err != nil {
		return nil, err
	}

	if err := mapper.RequireUserIDMatch(caller.ID, params.UserID); err != nil {
		return nil, err
	}

	user, err := h.userUseCase.UpdatePreferredLanguage(ctx, caller.ID, params.PreferredLanguage)
	if err != nil {
		return nil, err
	}

	return user, nil
}

// UpdateHome sets or changes the authenticated user's home area.
//
// The request-supplied user_id is verified against the JWT-derived userID;
// mismatches are rejected with PERMISSION_DENIED per the rpc-auth-scoping
// convention.
func (h *UserHandler) UpdateHome(ctx context.Context, req *connect.Request[userv1.UpdateHomeRequest]) (*connect.Response[userv1.UpdateHomeResponse], error) {
	externalID, err := mapper.GetExternalUserID(ctx)
	if err != nil {
		return nil, err
	}

	user, err := h.userUseCase.GetByExternalID(ctx, externalID)
	if err != nil {
		return nil, err
	}

	if err := mapper.RequireUserIDMatch(user.ID, req.Msg.GetUserId().GetValue()); err != nil {
		return nil, err
	}

	home := mapper.ProtoHomeToEntity(req.Msg.GetHome())
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
//
// The request-supplied user_id is verified against the JWT-derived userID;
// mismatches are rejected with PERMISSION_DENIED before any Zitadel API call
// is made, per the rpc-auth-scoping convention.
func (h *UserHandler) ResendEmailVerification(ctx context.Context, req *connect.Request[userv1.ResendEmailVerificationRequest]) (*connect.Response[userv1.ResendEmailVerificationResponse], error) {
	if h.emailVerifier == nil {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("email verification service is not configured"))
	}

	claims, err := mapper.GetClaimsFromContext(ctx)
	if err != nil {
		return nil, err
	}

	user, err := h.userUseCase.GetByExternalID(ctx, claims.Sub)
	if err != nil {
		return nil, err
	}

	if err := mapper.RequireUserIDMatch(user.ID, req.Msg.GetUserId().GetValue()); err != nil {
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
