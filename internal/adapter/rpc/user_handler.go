package rpc

import (
	"context"
	"errors"

	userv1connect "buf.build/gen/go/liverty-music/schema/connectrpc/go/liverty_music/rpc/user/v1/userv1connect"
	userv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/user/v1"
	"connectrpc.com/connect"
	"github.com/liverty-music/backend/internal/adapter/rpc/mapper"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-logging/logging"
)

// Compile-time assertion: UserHandler satisfies the generated
// UserServiceHandler interface. Matches the same pattern in
// TicketHandler / EntryHandler and surfaces interface drift (added or
// renamed RPC methods) at build time instead of waiting for the DI
// wiring to fail at startup.
var _ userv1connect.UserServiceHandler = (*UserHandler)(nil)

// UserHandler implements the UserService Connect interface.
//
// Every call acts on the signed-in caller's own User: no request names a
// User. The handler only resolves the caller's identity from the
// authenticated context and maps proto to/from domain entities; resolving the
// caller's User and the resend rate limit live in UserUseCase.
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

// Get returns the signed-in caller's own User.
func (h *UserHandler) Get(ctx context.Context, _ *connect.Request[userv1.GetRequest]) (*connect.Response[userv1.GetResponse], error) {
	providerUserID, err := mapper.GetExternalUserID(ctx)
	if err != nil {
		return nil, err
	}

	user, err := h.userUseCase.ResolveCaller(ctx, providerUserID)
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
	// Extract JWT claims from authenticated context (set by auth interceptor).
	// This is critical for security — we extract all identity fields from validated JWT claims
	// (provider_user_id, email, name) and never trust client-provided identity data.
	claims, err := mapper.GetClaimsFromContext(ctx)
	if err != nil {
		return nil, err
	}

	// Defense-in-depth format check for preferred_language. The wire-layer
	// protovalidate constraint and the use case both validate this; the
	// handler also rejects malformed values at the seam so all three
	// layers stay symmetric with SetPreferredLanguage. preferred_language
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

// Update changes the signed-in caller's full name, phone number or both and
// returns the User; a value not given keeps the current one. The request is
// checked before the usecase runs: at least one value must be given, and each
// given value must follow the User's personal details rule (a full name of 1
// to 200 characters, a phone number in E.164 form).
func (h *UserHandler) Update(ctx context.Context, req *connect.Request[userv1.UpdateRequest]) (*connect.Response[userv1.UpdateResponse], error) {
	fullName, phoneNumber, err := mapper.PersonalDetailsFromUpdateRequest(req.Msg)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	providerUserID, err := mapper.GetExternalUserID(ctx)
	if err != nil {
		return nil, err
	}

	// UserUseCase.Update resolves the caller's User from providerUserID and fails
	// with NotFound when the caller has none.
	user, err := h.userUseCase.Update(ctx, providerUserID, fullName, phoneNumber)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&userv1.UpdateResponse{
		User: mapper.UserToProto(user),
	}), nil
}

// SetPreferredLanguage sets or changes the signed-in caller's preferred
// display language.
func (h *UserHandler) SetPreferredLanguage(ctx context.Context, req *connect.Request[userv1.SetPreferredLanguageRequest]) (*connect.Response[userv1.SetPreferredLanguageResponse], error) {
	// Defense in depth: protovalidate enforces the same regex at the wire
	// layer and the use case has the authoritative guard. Re-checking
	// here at the RPC seam keeps the contract explicit and consistent so
	// all three layers reject the same shape if any one is bypassed
	// (interceptor misconfigured, internal callers, test harnesses).
	//
	// Order: format validation runs BEFORE resolving the caller so a
	// malformed payload doesn't trigger a DB round-trip and doesn't leak
	// user-existence information on the sad path (a deleted user with a
	// malformed lang would otherwise see NotFound instead of
	// InvalidArgument). This intentionally diverges from SetHome's
	// auth-first posture — SetHome has no handler-layer payload check;
	// here we do, so payload first is the right ordering.
	//
	// `entity.IsValidLanguageCode` is the single source of truth (also
	// used by the use case), so a future format change is one edit.
	lang := req.Msg.GetPreferredLanguage()
	if !entity.IsValidLanguageCode(lang) {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("preferred_language must match ISO 639-1 (^[a-z]{2}$)"))
	}

	providerUserID, err := mapper.GetExternalUserID(ctx)
	if err != nil {
		return nil, err
	}

	caller, err := h.userUseCase.ResolveCaller(ctx, providerUserID)
	if err != nil {
		return nil, err
	}

	user, err := h.userUseCase.SetPreferredLanguage(ctx, caller.ID, lang)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&userv1.SetPreferredLanguageResponse{
		User: mapper.UserToProto(user),
	}), nil
}

// SetHome sets or changes the signed-in caller's home area.
func (h *UserHandler) SetHome(ctx context.Context, req *connect.Request[userv1.SetHomeRequest]) (*connect.Response[userv1.SetHomeResponse], error) {
	providerUserID, err := mapper.GetExternalUserID(ctx)
	if err != nil {
		return nil, err
	}

	user, err := h.userUseCase.ResolveCaller(ctx, providerUserID)
	if err != nil {
		return nil, err
	}

	home := mapper.ProtoHomeToEntity(req.Msg.GetHome())
	updatedUser, err := h.userUseCase.SetHome(ctx, user.ID, home)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&userv1.SetHomeResponse{
		User: mapper.UserToProto(updatedUser),
	}), nil
}

// ResendEmailVerification triggers a new email verification message for the
// authenticated user via the Zitadel API.
//
// The handler only resolves the caller's provider user ID from the JWT and
// delegates to the use case, which resolves the caller and enforces the resend
// rate limit before calling the identity provider.
func (h *UserHandler) ResendEmailVerification(ctx context.Context, _ *connect.Request[userv1.ResendEmailVerificationRequest]) (*connect.Response[userv1.ResendEmailVerificationResponse], error) {
	claims, err := mapper.GetClaimsFromContext(ctx)
	if err != nil {
		return nil, err
	}

	if err := h.userUseCase.ResendEmailVerification(ctx, claims.Sub); err != nil {
		return nil, err
	}

	return connect.NewResponse(&userv1.ResendEmailVerificationResponse{}), nil
}
