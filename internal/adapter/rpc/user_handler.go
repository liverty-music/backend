package rpc

import (
	"context"
	"errors"
	"sync"
	"time"

	userv1connect "buf.build/gen/go/liverty-music/schema/connectrpc/go/liverty_music/rpc/user/v1/userv1connect"
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

// Compile-time assertion: UserHandler satisfies the generated
// UserServiceHandler interface. Matches the same pattern in
// TicketHandler / EntryHandler and surfaces interface drift (added or
// renamed RPC methods) at build time instead of waiting for the DI
// wiring to fail at startup.
var _ userv1connect.UserServiceHandler = (*UserHandler)(nil)

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

	// Defense-in-depth format check for preferred_language. The wire-layer
	// protovalidate constraint and the use case both validate this; the
	// handler also rejects malformed values at the seam so all three
	// layers stay symmetric with UpdatePreferredLanguage. preferred_language
	// is optional at Create (old clients omit it → stored as NULL), so
	// only the non-empty case is checked here.
	lang := req.Msg.GetPreferredLanguage()
	if lang != "" && !entity.IsValidLanguageCode(lang) {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("preferred_language must match ISO 639-1 (^[a-z]{2}$) when present"))
	}

	// Convert JWT claims and request to domain DTO. preferred_language is optional;
	// GetPreferredLanguage() returns "" when the field is absent, which is stored
	// as NULL in the database.
	newUser := mapper.NewUserFromCreateRequest(claims, req.Msg)

	// Use the use case layer for business logic.
	createdUser, err := h.userUseCase.Create(ctx, newUser)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&userv1.CreateResponse{
		User: mapper.UserToProto(createdUser),
	}), nil
}

// UpdatePreferredLanguage sets or changes the authenticated user's preferred display language.
//
// The request-supplied user_id is verified against the JWT-derived userID;
// mismatches are rejected with PERMISSION_DENIED per the rpc-auth-scoping
// convention.
func (h *UserHandler) UpdatePreferredLanguage(ctx context.Context, req *connect.Request[userv1.UpdatePreferredLanguageRequest]) (*connect.Response[userv1.UpdatePreferredLanguageResponse], error) {
	// Defense in depth: protovalidate enforces the same regex at the wire
	// layer and the use case has the authoritative guard. Re-checking
	// here at the RPC seam keeps the contract explicit and consistent so
	// all three layers reject the same shape if any one is bypassed
	// (interceptor misconfigured, internal callers, test harnesses).
	//
	// Order: format validation runs BEFORE GetByExternalID so a malformed
	// payload doesn't trigger a DB round-trip and doesn't leak
	// user-existence information on the sad path (a deleted user with a
	// malformed lang would otherwise see NotFound instead of
	// InvalidArgument). This intentionally diverges from UpdateHome's
	// auth-first posture — UpdateHome has no handler-layer payload check;
	// here we do, so payload first is the right ordering.
	//
	// `entity.IsValidLanguageCode` is the single source of truth (also
	// used by the use case), so a future format change is one edit.
	lang := req.Msg.GetPreferredLanguage()
	if !entity.IsValidLanguageCode(lang) {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("preferred_language must match ISO 639-1 (^[a-z]{2}$)"))
	}

	externalID, err := mapper.GetExternalUserID(ctx)
	if err != nil {
		return nil, err
	}

	caller, err := h.userUseCase.GetByExternalID(ctx, externalID)
	if err != nil {
		return nil, err
	}

	if err := mapper.RequireUserIDMatch(caller.ID, req.Msg.GetUserId().GetValue()); err != nil {
		return nil, err
	}

	user, err := h.userUseCase.UpdatePreferredLanguage(ctx, caller.ID, lang)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&userv1.UpdatePreferredLanguageResponse{
		User: mapper.UserToProto(user),
	}), nil
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
