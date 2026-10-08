// Package usecase contains business logic implementations for the application.
package usecase

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/pannpers/go-logging/logging"
)

// resendRateLimit and resendRateWindow bound how often a user may request a
// verification-email resend: at most resendRateLimit requests within any
// rolling resendRateWindow.
const (
	resendRateLimit  = 3
	resendRateWindow = 10 * time.Minute
)

// UserUseCase defines the interface for user-related business logic.
type UserUseCase interface {
	// Create registers a new user, or returns the existing user when the
	// caller's external_id is already provisioned.
	//
	// Idempotent behavior: when the underlying repository reports a unique
	// violation and a user already exists for the supplied external_id, the
	// existing entity is returned with no error and no UserCreated event is
	// published. The existing row's email, name, and home fields are NOT
	// overwritten — the duplicate call is a read, not an upsert.
	//
	// A unique violation on email by a different external_id is NOT
	// idempotent and is surfaced as AlreadyExists.
	//
	// # Possible errors
	//
	//  - InvalidArgument: If email or name is invalid, or home is malformed.
	//  - AlreadyExists: If the email is already claimed by a different identity.
	Create(ctx context.Context, params *entity.NewUser) (*entity.User, error)

	// Get retrieves a user by their unique ID.
	//
	// # Possible errors
	//
	//  - NotFound: If the user does not exist.
	Get(ctx context.Context, id string) (*entity.User, error)

	// GetByExternalID retrieves a user by identity provider ID (Zitadel sub claim).
	//
	// # Possible errors
	//
	//  - NotFound: If the user does not exist.
	GetByExternalID(ctx context.Context, externalID string) (*entity.User, error)

	// UpdatePreferredLanguage sets or changes the user's preferred display language.
	//
	// # Possible errors
	//
	//  - NotFound: If the user does not exist.
	UpdatePreferredLanguage(ctx context.Context, id, lang string) (*entity.User, error)

	// UpdateHome sets or changes the user's home area.
	//
	// # Possible errors
	//
	//  - InvalidArgument: If the home value is malformed.
	//  - NotFound: If the user does not exist.
	UpdateHome(ctx context.Context, id string, home *entity.Home) (*entity.User, error)

	// Delete permanently removes a fan user at an admin's request: first the
	// user's sign-in identity, then the user record with everything it owns.
	// The user's Orders, Tickets and ticket applications are kept. Nothing is
	// removed while the user holds an Issued Ticket or has an Order that is not
	// Refunded. An identity that is already gone counts as removed, so calling
	// Delete again completes a deletion whose record removal failed.
	//
	// # Possible errors
	//
	//  - NotFound: If the user does not exist.
	//  - FailedPrecondition: If the user holds an Issued Ticket or has an Order
	//    that is not Refunded.
	//  - Internal: If the identity could not be removed, or identity removal
	//    is not configured.
	Delete(ctx context.Context, id string) error

	// ResolveCaller returns the user identified by externalID (the Zitadel
	// subject claim), after verifying that reqUserID — the user_id supplied
	// by the client in the request body — matches the caller's own id.
	//
	// Handlers for per-user RPCs that expose an explicit user_id field in
	// their request message MUST call this instead of GetByExternalID so
	// that cross-user requests are rejected before any business logic runs.
	//
	// # Possible errors
	//
	//  - InvalidArgument: reqUserID is empty.
	//  - PermissionDenied: reqUserID does not match the caller's user id.
	//  - NotFound: no user exists for externalID.
	ResolveCaller(ctx context.Context, externalID, reqUserID string) (*entity.User, error)

	// ResendEmailVerification triggers a new email verification message via
	// the identity provider for the user identified by externalID (the
	// Zitadel subject claim), after verifying reqUserID matches the
	// caller's own user id and the resend rate limit has not been
	// exceeded.
	//
	// The rate limit (3 resends per rolling 10-minute window) is tracked in
	// memory, scoped to this process. It is NOT shared across replicas, so
	// the effective limit is 3-per-replica and resets whenever the process
	// restarts. Make it shared (e.g. via Redis) if the limit must hold
	// cluster-wide.
	//
	// # Possible errors
	//
	//  - InvalidArgument: reqUserID is empty.
	//  - PermissionDenied: reqUserID does not match the caller's user id.
	//  - ResourceExhausted: the resend rate limit has been exceeded.
	//  - FailedPrecondition: the user's email is already verified.
	//  - Unavailable: the email verification service is not configured.
	//  - NotFound: no user exists for externalID.
	ResendEmailVerification(ctx context.Context, externalID, reqUserID string) error
}

// userUseCase implements the UserUseCase interface.
type userUseCase struct {
	userRepo        entity.UserRepository
	ticketRepo      entity.TicketRepository
	orderRepo       entity.OrderRepository
	publisher       EventPublisher
	emailVerifier   entity.EmailVerifier
	identityRemover entity.IdentityRemover
	logger          *logging.Logger

	// resendMu protects resendLog for concurrent access.
	resendMu  sync.Mutex
	resendLog map[string][]time.Time
}

// Compile-time interface compliance check
var _ UserUseCase = (*userUseCase)(nil)

// NewUserUseCase creates a new user use case.
// It requires a user repository for data persistence, the ticket and order
// repositories that deletion checks for live purchases, a publisher for domain
// events, an email verifier for triggering verification emails and an
// identity remover for deletion (both nil when the Zitadel API client is not
// configured, e.g. local dev), and a logger.
func NewUserUseCase(
	userRepo entity.UserRepository,
	ticketRepo entity.TicketRepository,
	orderRepo entity.OrderRepository,
	publisher EventPublisher,
	emailVerifier entity.EmailVerifier,
	identityRemover entity.IdentityRemover,
	logger *logging.Logger,
) UserUseCase {
	return &userUseCase{
		userRepo:        userRepo,
		ticketRepo:      ticketRepo,
		orderRepo:       orderRepo,
		publisher:       publisher,
		emailVerifier:   emailVerifier,
		identityRemover: identityRemover,
		logger:          logger,
		resendLog:       make(map[string][]time.Time),
	}
}

// Create creates a new user, or returns the existing user on duplicate
// external_id (idempotent).
func (uc *userUseCase) Create(ctx context.Context, params *entity.NewUser) (*entity.User, error) {
	if params.Home != nil {
		if err := params.Home.Validate(); err != nil {
			return nil, apperr.Wrap(err, codes.InvalidArgument, err.Error())
		}
	}
	// preferred_language is optional at Create (old clients omit it, which
	// is allowed — the row is created NULL and the client backfills on
	// next hydration). When present, it MUST match the ISO 639-1 pattern;
	// otherwise a value like "EN" or "english" would round-trip into the
	// DB unchanged and the frontend's i18n.setLocale would silently
	// fall back to the fallbackLng.
	if params.PreferredLanguage != "" && !entity.IsValidLanguageCode(params.PreferredLanguage) {
		return nil, apperr.New(codes.InvalidArgument,
			"preferred_language must match ISO 639-1 (^[a-z]{2}$)",
			slog.String("preferred_language", params.PreferredLanguage),
		)
	}

	user, err := uc.userRepo.Create(ctx, params)
	if err != nil {
		// On AlreadyExists, distinguish:
		//   (a) Same caller retrying — duplicate external_id. GetByExternalID
		//       returns the existing row; treat as idempotent success.
		//   (b) Different identity claiming the same email — GetByExternalID
		//       returns NotFound. Propagate the original AlreadyExists so the
		//       caller sees the email-collision signal.
		//   (c) GetByExternalID itself errored for a non-NotFound reason
		//       (Internal scan failure, Unavailable, transient pool error,
		//       etc.). Return the retry error so the operator sees the real
		//       failure instead of a masquerading AlreadyExists — masking
		//       caused a multi-hour incident on 2026-05-23 when scanUser
		//       failed on NULL columns and surfaced to clients as
		//       AlreadyExists with no log trail (the bug ate its own
		//       evidence). The original AlreadyExists is logged so the full
		//       chain is preserved for forensic review.
		if errors.Is(err, apperr.ErrAlreadyExists) {
			existing, getErr := uc.userRepo.GetByExternalID(ctx, params.ExternalID)
			if getErr == nil {
				uc.logger.Info(ctx, "Create returned existing user (idempotent on duplicate external_id)",
					slog.String("user_id", existing.ID),
					slog.String("external_id", existing.ExternalID),
				)
				return existing, nil
			}
			if errors.Is(getErr, apperr.ErrNotFound) {
				// Case (b): email-collision case.
				return nil, err
			}
			// Case (c): retry failure other than NotFound — return the
			// truthful error class.
			uc.logger.Warn(ctx, "Create retry GetByExternalID failed with non-NotFound error; returning retry error to surface real failure",
				slog.String("external_id", params.ExternalID),
				slog.String("original_error", err.Error()),
				slog.String("retry_error", getErr.Error()),
			)
			return nil, getErr
		}
		return nil, err
	}

	if user == nil {
		return nil, apperr.New(codes.Internal, "repository returned nil user without error")
	}

	uc.logger.Info(ctx, "User created successfully", slog.String("user_id", user.ID))

	if err := uc.publishEvent(ctx, entity.SubjectUserCreated, entity.UserCreatedData{
		UserID:     user.ID,
		ExternalID: user.ExternalID,
		Email:      user.Email,
	}); err != nil {
		uc.logger.Error(ctx, "failed to publish user.created event", err,
			slog.String("user_id", user.ID),
		)
		// Non-fatal: user is already persisted.
	}

	return user, nil
}

// publishEvent publishes data as a CloudEvent to the given subject.
func (uc *userUseCase) publishEvent(ctx context.Context, subject string, data any) error {
	return uc.publisher.PublishEvent(ctx, subject, data)
}

// Get retrieves a user by ID.
func (uc *userUseCase) Get(ctx context.Context, id string) (*entity.User, error) {
	user, err := uc.userRepo.Get(ctx, id)
	if err != nil {
		// Add context without overriding the repository's code: a missing
		// user is NotFound, but a database outage must stay Unavailable/
		// Internal rather than masquerade as NotFound.
		return nil, fmt.Errorf("failed to get user %q: %w", id, err)
	}

	return user, nil
}

// GetByExternalID retrieves a user by identity provider ID.
func (uc *userUseCase) GetByExternalID(ctx context.Context, externalID string) (*entity.User, error) {
	user, err := uc.userRepo.GetByExternalID(ctx, externalID)
	if err != nil {
		// Preserve the repository's error code (see Get above).
		return nil, fmt.Errorf("failed to get user by external ID %q: %w", externalID, err)
	}

	return user, nil
}

// UpdatePreferredLanguage sets the user's preferred display language.
//
// Validates `lang` against the ISO 639-1 two-letter pattern before reaching
// the repository — mirrors UpdateHome's `home.Validate()` posture so that
// non-RPC callers (integration tests, future internal handlers, scripts)
// don't bypass the wire-layer protovalidate constraint.
func (uc *userUseCase) UpdatePreferredLanguage(ctx context.Context, id, lang string) (*entity.User, error) {
	if id == "" {
		return nil, apperr.New(codes.InvalidArgument, "user id is required")
	}
	if !entity.IsValidLanguageCode(lang) {
		return nil, apperr.New(codes.InvalidArgument,
			"preferred_language must match ISO 639-1 (^[a-z]{2}$)",
			slog.String("preferred_language", lang),
		)
	}

	user, err := uc.userRepo.UpdatePreferredLanguage(ctx, id, lang)
	if err != nil {
		return nil, err
	}

	uc.logger.Info(ctx, "User preferred language updated",
		slog.String("user_id", id),
		slog.String("preferred_language", lang),
	)

	return user, nil
}

// UpdateHome sets or changes the user's home area after validating the structured Home.
func (uc *userUseCase) UpdateHome(ctx context.Context, id string, home *entity.Home) (*entity.User, error) {
	if err := home.Validate(); err != nil {
		return nil, apperr.Wrap(err, codes.InvalidArgument, err.Error())
	}

	user, err := uc.userRepo.UpdateHome(ctx, id, home)
	if err != nil {
		return nil, err
	}

	uc.logger.Info(ctx, "User home updated",
		slog.String("user_id", id),
		slog.String("country_code", home.CountryCode),
		slog.String("level_1", home.Level1),
	)

	return user, nil
}

// Delete removes the user's identity and then the user, after checking that
// the user holds no Issued Ticket and has no Order that is not Refunded.
func (uc *userUseCase) Delete(ctx context.Context, id string) error {
	user, err := uc.userRepo.Get(ctx, id)
	if err != nil {
		return err
	}

	tickets, err := uc.ticketRepo.ListByHolder(ctx, entity.UserID(id))
	if err != nil {
		return err
	}
	for _, t := range tickets {
		if t.Status == entity.TicketStatusIssued {
			return apperr.New(codes.FailedPrecondition, "user holds an issued ticket", slog.String("user_id", id))
		}
	}
	orders, err := uc.orderRepo.ListByBuyer(ctx, entity.UserID(id))
	if err != nil {
		return err
	}
	for _, o := range orders {
		if o.Status != entity.OrderStatusRefunded {
			return apperr.New(codes.FailedPrecondition, "user has an order that is not refunded", slog.String("user_id", id))
		}
	}

	if uc.identityRemover == nil {
		return apperr.New(codes.Internal, "identity removal is not configured")
	}
	if err := uc.identityRemover.DeleteIdentity(ctx, user.ExternalID); err != nil {
		return err
	}
	if err := uc.userRepo.Delete(ctx, id); err != nil {
		return err
	}

	uc.logger.Info(ctx, "User deleted successfully",
		slog.String("user_id", id),
		slog.String("external_id", user.ExternalID),
	)

	return nil
}

// ResolveCaller returns the user identified by externalID, verifying that
// reqUserID matches the caller's own id.
func (uc *userUseCase) ResolveCaller(ctx context.Context, externalID, reqUserID string) (*entity.User, error) {
	user, err := uc.GetByExternalID(ctx, externalID)
	if err != nil {
		return nil, err
	}
	if reqUserID == "" {
		return nil, apperr.New(codes.InvalidArgument, "user_id is required")
	}
	if reqUserID != user.ID {
		return nil, apperr.New(codes.PermissionDenied, "user_id does not match authenticated user")
	}
	return user, nil
}

// ResendEmailVerification triggers a verification-email resend for the
// caller, enforcing the user_id ownership check and the in-memory resend
// rate limit before calling the identity provider.
func (uc *userUseCase) ResendEmailVerification(ctx context.Context, externalID, reqUserID string) error {
	if uc.emailVerifier == nil {
		return apperr.New(codes.Unavailable, "email verification service is not configured")
	}

	user, err := uc.ResolveCaller(ctx, externalID, reqUserID)
	if err != nil {
		return err
	}

	if !uc.allowResend(user.ExternalID) {
		return apperr.New(codes.ResourceExhausted, "resend rate limit exceeded")
	}

	return uc.emailVerifier.ResendVerification(ctx, user.ExternalID)
}

// allowResend reports whether externalID has not exceeded the resend rate
// limit (resendRateLimit requests per rolling resendRateWindow), recording
// this attempt when it allows it. Returns false when the caller must wait.
func (uc *userUseCase) allowResend(externalID string) bool {
	now := time.Now()
	cutoff := now.Add(-resendRateWindow)

	uc.resendMu.Lock()
	defer uc.resendMu.Unlock()

	// Filter out expired entries.
	var recent []time.Time
	for _, t := range uc.resendLog[externalID] {
		if t.After(cutoff) {
			recent = append(recent, t)
		}
	}

	// Reclaim memory for users with no recent activity.
	if len(recent) == 0 {
		delete(uc.resendLog, externalID)
	}

	if len(recent) >= resendRateLimit {
		uc.resendLog[externalID] = recent
		return false
	}

	uc.resendLog[externalID] = append(recent, now)
	return true
}
