package rpc

import (
	"context"

	userv1connect "buf.build/gen/go/liverty-music/schema/connectrpc/go/v2/liverty_music/rpc/user/v1/userv1connect"
	userv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/user/v1"
	"connectrpc.com/connect/v2"
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
// Every call except RequestPasskeyRecovery and VerifyEmail acts on the
// signed-in caller's own User: no request names a User. RequestPasskeyRecovery
// and VerifyEmail need no sign-in and use no sign-in identity. The handler only resolves the caller's identity from the
// authenticated context and maps proto to/from domain entities; resolving the
// caller's User and the resend rate limit live in UserUseCase.
type UserHandler struct {
	userUseCase usecase.UserUseCase
	freshSignIn FreshSignInVerifier
	logger      *logging.Logger
}

// FreshSignInVerifier checks the proof that the caller signed in within the
// last 5 minutes, which Delete requires.
type FreshSignInVerifier interface {
	// VerifyFreshSignIn checks that idToken is a validly signed ID token the
	// sign-in service issued to the fan app for a sign-in of the identity
	// providerUserID within the last 5 minutes.
	//
	// # Possible errors
	//
	//  - PermissionDenied: any of the checks fails.
	//  - Unavailable: the sign-in service's keys cannot be fetched.
	VerifyFreshSignIn(ctx context.Context, idToken, providerUserID string) error
}

// NewUserHandler creates a new user handler. freshSignIn checks Delete's
// proof of a fresh sign-in.
func NewUserHandler(userUseCase usecase.UserUseCase, freshSignIn FreshSignInVerifier, logger *logging.Logger) *UserHandler {
	return &UserHandler{
		userUseCase: userUseCase,
		freshSignIn: freshSignIn,
		logger:      logger,
	}
}

// Get returns the signed-in caller's own User.
func (h *UserHandler) Get(ctx context.Context, _ *userv1.GetRequest) (*userv1.GetResponse, error) {
	providerUserID, err := mapper.GetExternalUserID(ctx)
	if err != nil {
		return nil, err
	}

	user, err := h.userUseCase.ResolveCaller(ctx, providerUserID)
	if err != nil {
		return nil, err
	}

	return &userv1.GetResponse{
		User: mapper.UserToProto(user),
	}, nil
}

// Create creates a new user.
// The optional home field allows persisting the user's home area atomically
// with account creation (selected during onboarding before sign-up).
func (h *UserHandler) Create(ctx context.Context, req *userv1.CreateRequest) (*userv1.CreateResponse, error) {
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
	lang := req.GetPreferredLanguage()
	if lang != "" && !entity.IsValidLanguageCode(lang) {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			"preferred_language must match ISO 639-1 (^[a-z]{2}$) when present")
	}

	// Convert JWT claims and request to domain DTO. preferred_language is optional;
	// GetPreferredLanguage() returns "" when the field is absent, which is stored
	// as NULL in the database.
	newUser := mapper.NewUserFromCreateRequest(claims, req)

	// Use the use case layer for business logic.
	createdUser, err := h.userUseCase.Create(ctx, newUser)
	if err != nil {
		return nil, err
	}

	return &userv1.CreateResponse{
		User: mapper.UserToProto(createdUser),
	}, nil
}

// Update changes the signed-in caller's full name, phone number or both and
// returns the User; a value not given keeps the current one. The request is
// checked before the usecase runs: at least one value must be given, and each
// given value must follow the User's personal details rule (a full name of 1
// to 200 characters, a phone number in E.164 form).
func (h *UserHandler) Update(ctx context.Context, req *userv1.UpdateRequest) (*userv1.UpdateResponse, error) {
	fullName, phoneNumber, err := mapper.PersonalDetailsFromUpdateRequest(req)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err.Error()).WithCause(err)
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

	return &userv1.UpdateResponse{
		User: mapper.UserToProto(user),
	}, nil
}

// SetPreferredLanguage sets or changes the signed-in caller's preferred
// display language.
func (h *UserHandler) SetPreferredLanguage(ctx context.Context, req *userv1.SetPreferredLanguageRequest) (*userv1.SetPreferredLanguageResponse, error) {
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
	lang := req.GetPreferredLanguage()
	if !entity.IsValidLanguageCode(lang) {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			"preferred_language must match ISO 639-1 (^[a-z]{2}$)")
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

	return &userv1.SetPreferredLanguageResponse{
		User: mapper.UserToProto(user),
	}, nil
}

// SetHome sets or changes the signed-in caller's home area.
func (h *UserHandler) SetHome(ctx context.Context, req *userv1.SetHomeRequest) (*userv1.SetHomeResponse, error) {
	providerUserID, err := mapper.GetExternalUserID(ctx)
	if err != nil {
		return nil, err
	}

	user, err := h.userUseCase.ResolveCaller(ctx, providerUserID)
	if err != nil {
		return nil, err
	}

	home := mapper.ProtoHomeToEntity(req.GetHome())
	updatedUser, err := h.userUseCase.SetHome(ctx, user.ID, home)
	if err != nil {
		return nil, err
	}

	return &userv1.SetHomeResponse{
		User: mapper.UserToProto(updatedUser),
	}, nil
}

// ResendEmailVerification requests a new email verification message for the
// authenticated user.
//
// The handler only resolves the caller's provider user ID from the JWT and
// delegates to the use case, which resolves the caller, enforces the resend
// rate limit and announces the request; the event consumer sends the code.
func (h *UserHandler) ResendEmailVerification(ctx context.Context, _ *userv1.ResendEmailVerificationRequest) (*userv1.ResendEmailVerificationResponse, error) {
	claims, err := mapper.GetClaimsFromContext(ctx)
	if err != nil {
		return nil, err
	}

	if err := h.userUseCase.ResendEmailVerification(ctx, claims.Sub); err != nil {
		return nil, err
	}

	return &userv1.ResendEmailVerificationResponse{}, nil
}

// RequestPasskeyRecovery asks for a passkey registration link for the
// account of the given address, for a person who cannot sign in. It needs no
// sign-in and uses no sign-in identity even when one is presented. The address
// is checked by protovalidate before the handler runs; the per-client limit is
// applied by the fan server's interceptor, whose ResourceExhausted reaches
// the caller unchanged. The answer is the same empty success whatever the
// address.
func (h *UserHandler) RequestPasskeyRecovery(ctx context.Context, req *userv1.RequestPasskeyRecoveryRequest) (*userv1.RequestPasskeyRecoveryResponse, error) {
	h.userUseCase.RequestPasskeyRecovery(ctx, req.GetEmail().GetValue())
	return &userv1.RequestPasskeyRecoveryResponse{}, nil
}

// VerifyEmail confirms a User's email address with the User id and code from
// the link in the User's verification email. It needs no sign-in and uses no
// sign-in identity even when one is presented. The User id and the code are
// checked by protovalidate before the handler runs; the per-client limit is
// applied by the fan server's interceptor. The code is never logged.
func (h *UserHandler) VerifyEmail(ctx context.Context, req *userv1.VerifyEmailRequest) (*userv1.VerifyEmailResponse, error) {
	if err := h.userUseCase.VerifyEmail(ctx, req.GetUserId().GetValue(), req.GetCode().GetValue()); err != nil {
		return nil, err
	}
	return &userv1.VerifyEmailResponse{}, nil
}

// ListDeletionBlockers returns what currently stops the caller's own User
// from being deleted.
func (h *UserHandler) ListDeletionBlockers(ctx context.Context, _ *userv1.ListDeletionBlockersRequest) (*userv1.ListDeletionBlockersResponse, error) {
	providerUserID, err := mapper.GetExternalUserID(ctx)
	if err != nil {
		return nil, err
	}
	user, err := h.userUseCase.ResolveCaller(ctx, providerUserID)
	if err != nil {
		return nil, err
	}
	blockers, err := h.userUseCase.ListDeletionBlockers(ctx, user.ID)
	if err != nil {
		return nil, err
	}
	return &userv1.ListDeletionBlockersResponse{Blockers: mapper.DeletionBlockersToProto(blockers)}, nil
}

// Delete removes the caller's own User once the request proves a sign-in by
// the caller within the last 5 minutes. A missing proof is rejected by the
// server's protovalidate interceptor before the handler runs.
func (h *UserHandler) Delete(ctx context.Context, req *userv1.DeleteRequest) (*userv1.DeleteResponse, error) {
	providerUserID, err := mapper.GetExternalUserID(ctx)
	if err != nil {
		return nil, err
	}
	user, err := h.userUseCase.ResolveCaller(ctx, providerUserID)
	if err != nil {
		return nil, err
	}
	if err := h.freshSignIn.VerifyFreshSignIn(ctx, req.GetIdToken(), providerUserID); err != nil {
		return nil, err
	}
	if err := h.userUseCase.Delete(ctx, user.ID); err != nil {
		return nil, err
	}
	return &userv1.DeleteResponse{}, nil
}
