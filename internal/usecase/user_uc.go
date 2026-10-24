// Package usecase contains business logic implementations for the application.
package usecase

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
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
	// caller's provider_user_id is already provisioned.
	//
	// Idempotent behavior: when the underlying repository reports a unique
	// violation and a user already exists for the supplied provider_user_id, the
	// existing entity is returned with no error and no UserCreated event is
	// published. The existing row's email, name, and home fields are NOT
	// overwritten — the duplicate call is a read, not an upsert.
	//
	// A unique violation on email by a different provider_user_id is NOT
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

	// GetByProviderUserID retrieves a user by identity provider ID (Zitadel sub claim).
	//
	// # Possible errors
	//
	//  - NotFound: If the user does not exist.
	GetByProviderUserID(ctx context.Context, providerUserID string) (*entity.User, error)

	// SetPreferredLanguage sets or changes the user's preferred display language.
	//
	// # Possible errors
	//
	//  - NotFound: If the user does not exist.
	SetPreferredLanguage(ctx context.Context, id, lang string) (*entity.User, error)

	// SetHome sets or changes the user's home area.
	//
	// # Possible errors
	//
	//  - InvalidArgument: If the home value is malformed.
	//  - NotFound: If the user does not exist.
	SetHome(ctx context.Context, id string, home *entity.Home) (*entity.User, error)

	// Update changes the calling fan's own full name, phone number or both, the
	// 本人確認 (identity check) details shown on the face of their Tickets, and
	// returns the User. A nil value is not given and keeps the stored one; at
	// least one value must be given.
	//
	// # Possible errors
	//
	//  - InvalidArgument: no value is given, or a given value is empty or
	//    breaks the User's personal details rule; nothing changed.
	//  - NotFound: no user exists for providerUserID.
	Update(ctx context.Context, providerUserID string, fullName, phoneNumber *string) (*entity.User, error)

	// Delete permanently removes a fan user at an admin's request: first the
	// user's sign-in identity, then the user record with everything it owns.
	// The user's Orders, Tickets and lottery entries are kept. Nothing is
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

	// ResolveCaller returns the caller's own User, identified by providerUserID
	// (the Zitadel subject claim). A request never names a User id, so a
	// per-user call always acts on the caller's own User.
	//
	// # Possible errors
	//
	//  - NotFound: no user exists for providerUserID.
	ResolveCaller(ctx context.Context, providerUserID string) (*entity.User, error)

	// ResendEmailVerification announces a verification-email resend request for
	// the caller identified by providerUserID (the Zitadel subject claim), when
	// the resend rate limit has not been exceeded. The fresh code is sent by
	// ReissueEmailVerification in the event consumer, so this process needs no
	// identity-provider credential. The call succeeds for an address that is
	// already verified; ReissueEmailVerification then sends nothing.
	//
	// The rate limit (3 resends per rolling 10-minute window) is tracked in
	// memory, scoped to this process. It is NOT shared across replicas, so
	// the effective limit is 3-per-replica and resets whenever the process
	// restarts. Make it shared (e.g. via Redis) if the limit must hold
	// cluster-wide.
	//
	// # Possible errors
	//
	//  - ResourceExhausted: the resend rate limit has been exceeded.
	//  - Unavailable: the resend request could not be announced; the request
	//    still counts toward the limit.
	//  - NotFound: no user exists for providerUserID.
	ResendEmailVerification(ctx context.Context, providerUserID string) error

	// RequestPasskeyRecovery asks for a passkey registration link for the
	// User whose email is the given address, on behalf of a person who cannot
	// sign in. It succeeds in the same way whether or not the address belongs
	// to a User: after the per-address limit (at most 3 announcements per 60
	// minutes, compared without regard to letter case; a further request
	// returns without announcing) it returns at once, and the lookup with
	// User.GetByEmail and the announcement of a passkey recovery request for
	// the User found run in the background, where failures are logged. The
	// per-client limit is applied before the call, by the server.
	RequestPasskeyRecovery(ctx context.Context, email string)

	// SendPasskeyRecoveryLink sends the passkey registration link for an
	// announced passkey recovery request of the User userID. A User that no
	// longer exists, an identity that no longer exists and an identity whose
	// address is not verified are logged and finish without sending.
	//
	// # Possible errors
	//
	//  - Any other failure of User.Get or User.SendPasskeyRegistrationLink,
	//    unchanged, so the request runs again (and a newer link is sent).
	SendPasskeyRecoveryLink(ctx context.Context, userID string) error

	// NotifyPasskeyChange emails the fan of the identity providerUserID that a
	// passkey was added or removed. When no User has the identity it finishes
	// without sending.
	//
	// # Possible errors
	//
	//  - InvalidArgument: the kind of change is unknown.
	//  - Unavailable: the email cannot be sent now; run again to send it.
	//  - Any other failure of User.GetByProviderUserID or
	//    User.SendPasskeyChangeEmail, unchanged.
	NotifyPasskeyChange(ctx context.Context, providerUserID string, change entity.PasskeyChange) error

	// ReissueEmailVerification sends a fresh verification code to the address
	// of the User identified by userID. It runs for each announced resend
	// request. A User that no longer exists, an address that is already
	// verified, or email verification that is not configured in this process
	// finishes without sending. A run repeated after a code was sent sends a
	// newer code.
	//
	// # Possible errors
	//
	//  - Any other failure of reading the User or sending the code, returned
	//    so the request is run again.
	ReissueEmailVerification(ctx context.Context, userID string) error

	// VerifyEmail confirms the email address of the User userID with the code
	// from that User's verification email, for whoever holds the link: it
	// needs no sign-in. At most 5 attempts per User id in any 60 minutes are
	// accepted, counted before the User is read and whatever their outcome;
	// the per-client limit is applied before the call, by the server. An
	// unknown User and a wrong, expired or used code fail alike, so the
	// outcome never reveals whether a User exists.
	//
	// Like the resend limit, the per-User limit is held in memory per process.
	//
	// # Possible errors
	//
	//  - InvalidArgument: no User has the id, or the code is wrong, older
	//    than 1 hour or already used.
	//  - ResourceExhausted: the per-User limit is reached; nothing is read.
	//  - Unavailable: the identity provider cannot be reached, or email
	//    verification is not configured in this process.
	VerifyEmail(ctx context.Context, userID, code string) error
}

// PasskeyMailers are the senders of the passkey emails: the registration link
// of a recovery request and the passkey change email. Only the event consumer
// sends them; elsewhere they are nil.
type PasskeyMailers struct {
	// RegistrationLinks sends the passkey registration link
	// (User.SendPasskeyRegistrationLink).
	RegistrationLinks entity.PasskeyRegistrationLinkSender
	// ChangeEmails sends the passkey change email
	// (User.SendPasskeyChangeEmail).
	ChangeEmails entity.PasskeyChangeEmailSender
}

// passkeyRecoveryLimit and passkeyRecoveryWindow bound the passkey recovery
// requests announced for one address: at most passkeyRecoveryLimit within any
// passkeyRecoveryWindow.
const (
	passkeyRecoveryLimit  = 3
	passkeyRecoveryWindow = 60 * time.Minute
	// passkeyRecoveryTimeout bounds the background lookup and announcement.
	passkeyRecoveryTimeout = 30 * time.Second
)

// verifyEmailLimit and verifyEmailWindow bound the email verification
// attempts for one User id: at most verifyEmailLimit within any
// verifyEmailWindow.
const (
	verifyEmailLimit  = 5
	verifyEmailWindow = 60 * time.Minute
)

// userUseCase implements the UserUseCase interface.
type userUseCase struct {
	userRepo        entity.UserRepository
	ticketRepo      entity.TicketRepository
	orderRepo       entity.OrderRepository
	publisher       EventPublisher
	emailVerifier   entity.EmailVerifier
	emailCodes      entity.EmailCodeVerifier
	identityRemover entity.IdentityRemover
	passkeyMailers  PasskeyMailers
	logger          *logging.Logger

	// verifyEmailLimiter bounds the email verification attempts for one User
	// id.
	verifyEmailLimiter *slidingWindowLimiter

	// passkeyRecoveryLimiter bounds the recovery requests announced for one
	// address.
	passkeyRecoveryLimiter *slidingWindowLimiter
	// background runs the recovery lookup and announcement after
	// RequestPasskeyRecovery returns. Tests replace it to run the task
	// synchronously.
	background func(task func())

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
// configured, e.g. local dev), the passkey email senders (empty outside the
// event consumer), and a logger. emailCodes confirms addresses with emailed
// codes; it is nil outside the fan API.
func NewUserUseCase(
	userRepo entity.UserRepository,
	ticketRepo entity.TicketRepository,
	orderRepo entity.OrderRepository,
	publisher EventPublisher,
	emailVerifier entity.EmailVerifier,
	emailCodes entity.EmailCodeVerifier,
	identityRemover entity.IdentityRemover,
	passkeyMailers PasskeyMailers,
	logger *logging.Logger,
) UserUseCase {
	return &userUseCase{
		userRepo:               userRepo,
		ticketRepo:             ticketRepo,
		orderRepo:              orderRepo,
		publisher:              publisher,
		emailVerifier:          emailVerifier,
		emailCodes:             emailCodes,
		identityRemover:        identityRemover,
		passkeyMailers:         passkeyMailers,
		logger:                 logger,
		passkeyRecoveryLimiter: newSlidingWindowLimiter(passkeyRecoveryLimit, passkeyRecoveryWindow, time.Now),
		verifyEmailLimiter:     newSlidingWindowLimiter(verifyEmailLimit, verifyEmailWindow, time.Now),
		background:             func(task func()) { go task() },
		resendLog:              make(map[string][]time.Time),
	}
}

// Create creates a new user, or returns the existing user on duplicate
// provider_user_id (idempotent).
func (uc *userUseCase) Create(ctx context.Context, params *entity.NewUser) (*entity.User, error) {
	if err := params.ValidateEmail(); err != nil {
		return nil, apperr.Wrap(err, codes.InvalidArgument, err.Error())
	}
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
		//   (a) Same caller retrying — duplicate provider_user_id. GetByProviderUserID
		//       returns the existing row; treat as idempotent success.
		//   (b) Different identity claiming the same email — GetByProviderUserID
		//       returns NotFound. Propagate the original AlreadyExists so the
		//       caller sees the email-collision signal.
		//   (c) GetByProviderUserID itself errored for a non-NotFound reason
		//       (Internal scan failure, Unavailable, transient pool error,
		//       etc.). Return the retry error so the admin sees the real
		//       failure instead of a masquerading AlreadyExists — masking
		//       caused a multi-hour incident on 2026-05-23 when scanUser
		//       failed on NULL columns and surfaced to clients as
		//       AlreadyExists with no log trail (the bug ate its own
		//       evidence). The original AlreadyExists is logged so the full
		//       chain is preserved for forensic review.
		if errors.Is(err, apperr.ErrAlreadyExists) {
			existing, getErr := uc.userRepo.GetByProviderUserID(ctx, params.ProviderUserID)
			if getErr == nil {
				uc.logger.Info(ctx, "Create returned existing user (idempotent on duplicate provider_user_id)",
					slog.String("user_id", existing.ID),
					slog.String("provider_user_id", existing.ProviderUserID),
				)
				return existing, nil
			}
			if errors.Is(getErr, apperr.ErrNotFound) {
				// Case (b): email-collision case.
				return nil, err
			}
			// Case (c): retry failure other than NotFound — return the
			// truthful error class.
			uc.logger.Warn(ctx, "Create retry GetByProviderUserID failed with non-NotFound error; returning retry error to surface real failure",
				slog.String("provider_user_id", params.ProviderUserID),
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
		UserID:         user.ID,
		ProviderUserID: user.ProviderUserID,
		Email:          user.Email,
		CreateTime:     time.Now(),
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

// GetByProviderUserID retrieves a user by identity provider ID.
func (uc *userUseCase) GetByProviderUserID(ctx context.Context, providerUserID string) (*entity.User, error) {
	user, err := uc.userRepo.GetByProviderUserID(ctx, providerUserID)
	if err != nil {
		// Preserve the repository's error code (see Get above).
		return nil, fmt.Errorf("failed to get user by provider user ID %q: %w", providerUserID, err)
	}

	return user, nil
}

// SetPreferredLanguage sets the user's preferred display language.
//
// Validates `lang` against the ISO 639-1 two-letter pattern before reaching
// the repository — mirrors UpdateHome's `home.Validate()` posture so that
// non-RPC callers (integration tests, future internal handlers, scripts)
// don't bypass the wire-layer protovalidate constraint.
func (uc *userUseCase) SetPreferredLanguage(ctx context.Context, id, lang string) (*entity.User, error) {
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

// SetHome sets or changes the user's home area after validating the structured Home.
func (uc *userUseCase) SetHome(ctx context.Context, id string, home *entity.Home) (*entity.User, error) {
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

	tickets, err := uc.ticketRepo.ListByUser(ctx, entity.UserID(id))
	if err != nil {
		return err
	}
	for _, t := range tickets {
		if t.Status == entity.TicketStatusIssued {
			return apperr.New(codes.FailedPrecondition, "user holds an issued ticket", slog.String("user_id", id))
		}
	}
	orders, err := uc.orderRepo.ListByUser(ctx, entity.UserID(id))
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
	if err := uc.identityRemover.DeleteIdentity(ctx, user.ProviderUserID); err != nil {
		return err
	}
	if err := uc.userRepo.Delete(ctx, id); err != nil {
		return err
	}

	uc.logger.Info(ctx, "User deleted successfully",
		slog.String("user_id", id),
		slog.String("provider_user_id", user.ProviderUserID),
	)

	return nil
}

// Update implements [UserUseCase]. It merges the given values with the
// caller's current ones, so a value not given is kept.
func (uc *userUseCase) Update(ctx context.Context, providerUserID string, fullName, phoneNumber *string) (*entity.User, error) {
	if fullName == nil && phoneNumber == nil {
		return nil, apperr.New(codes.InvalidArgument, "at least one of full name and phone number must be given")
	}
	if (fullName != nil && *fullName == "") || (phoneNumber != nil && *phoneNumber == "") {
		return nil, apperr.New(codes.InvalidArgument, "a given full name or phone number must not be empty")
	}
	user, err := uc.ResolveCaller(ctx, providerUserID)
	if err != nil {
		return nil, err
	}
	name, phone := user.FullName, user.PhoneNumber
	if fullName != nil {
		name = *fullName
	}
	if phoneNumber != nil {
		phone = *phoneNumber
	}
	if err := entity.ValidatePersonalDetails(name, phone); err != nil {
		return nil, apperr.Wrap(err, codes.InvalidArgument, err.Error())
	}
	updated, err := uc.userRepo.UpdatePersonalDetails(ctx, user.ID, name, phone)
	if err != nil {
		return nil, err
	}
	uc.logger.Info(ctx, "User personal details updated", slog.String("user_id", user.ID))
	return updated, nil
}

// ResolveCaller returns the caller's own user, identified by providerUserID.
func (uc *userUseCase) ResolveCaller(ctx context.Context, providerUserID string) (*entity.User, error) {
	return uc.GetByProviderUserID(ctx, providerUserID)
}

// ResendEmailVerification announces a verification-email resend request for
// the caller, enforcing the in-memory resend rate limit first.
func (uc *userUseCase) ResendEmailVerification(ctx context.Context, providerUserID string) error {
	user, err := uc.ResolveCaller(ctx, providerUserID)
	if err != nil {
		return err
	}

	if !uc.allowResend(user.ProviderUserID) {
		return apperr.New(codes.ResourceExhausted, "resend rate limit exceeded")
	}

	if err := uc.publishEvent(ctx, entity.SubjectUserEmailVerificationResendRequested, entity.UserEmailVerificationResendRequestedData{
		UserID: user.ID,
	}); err != nil {
		return apperr.Wrap(err, codes.Unavailable, "announce email verification resend request",
			slog.String("user_id", user.ID))
	}

	return nil
}

// ReissueEmailVerification sends a fresh verification code for an announced
// resend request.
func (uc *userUseCase) ReissueEmailVerification(ctx context.Context, userID string) error {
	if uc.emailVerifier == nil {
		uc.logger.Warn(ctx, "email verifier not configured, skipping verification resend",
			slog.String("user_id", userID))
		return nil
	}

	user, err := uc.userRepo.Get(ctx, userID)
	if errors.Is(err, apperr.ErrNotFound) {
		uc.logger.Info(ctx, "user deleted since the resend request, skipping verification resend",
			slog.String("user_id", userID))
		return nil
	}
	if err != nil {
		return err
	}

	err = uc.emailVerifier.ResendVerification(ctx, user)
	if errors.Is(err, apperr.ErrFailedPrecondition) {
		uc.logger.Info(ctx, "email already verified, skipping verification resend",
			slog.String("user_id", userID))
		return nil
	}
	if err != nil {
		return err
	}

	uc.logger.Info(ctx, "verification email resent", slog.String("user_id", userID))
	return nil
}

// VerifyEmail implements [UserUseCase].
func (uc *userUseCase) VerifyEmail(ctx context.Context, userID, code string) error {
	attrs := slog.String("user_id", userID)
	if !uc.verifyEmailLimiter.allow(userID) {
		return apperr.New(codes.ResourceExhausted, "too many email verification attempts for the user", attrs)
	}
	if uc.emailCodes == nil {
		return apperr.New(codes.Unavailable, "email verification is not configured", attrs)
	}
	user, err := uc.userRepo.Get(ctx, userID)
	if errors.Is(err, apperr.ErrNotFound) {
		return apperr.Wrap(err, codes.InvalidArgument, "email verification link is not valid", attrs)
	}
	if err != nil {
		return err
	}
	err = uc.emailCodes.VerifyEmail(ctx, user.ProviderUserID, code)
	if errors.Is(err, apperr.ErrInvalidArgument) || errors.Is(err, apperr.ErrNotFound) {
		return apperr.Wrap(err, codes.InvalidArgument, "email verification link is not valid", attrs)
	}
	if err != nil {
		return err
	}
	uc.logger.Info(ctx, "email verified", attrs)
	return nil
}

// allowResend reports whether providerUserID has not exceeded the resend rate
// limit (resendRateLimit requests per rolling resendRateWindow), recording
// this attempt when it allows it. Returns false when the caller must wait.
func (uc *userUseCase) allowResend(providerUserID string) bool {
	now := time.Now()
	cutoff := now.Add(-resendRateWindow)

	uc.resendMu.Lock()
	defer uc.resendMu.Unlock()

	// Filter out expired entries.
	var recent []time.Time
	for _, t := range uc.resendLog[providerUserID] {
		if t.After(cutoff) {
			recent = append(recent, t)
		}
	}

	// Reclaim memory for users with no recent activity.
	if len(recent) == 0 {
		delete(uc.resendLog, providerUserID)
	}

	if len(recent) >= resendRateLimit {
		uc.resendLog[providerUserID] = recent
		return false
	}

	uc.resendLog[providerUserID] = append(recent, now)
	return true
}

// RequestPasskeyRecovery implements [UserUseCase]. The per-address check runs
// before it returns; the lookup and the announcement run afterwards on a
// context detached from the request, so neither the answer nor its timing
// depends on whether the address belongs to a User.
func (uc *userUseCase) RequestPasskeyRecovery(ctx context.Context, email string) {
	if !uc.passkeyRecoveryLimiter.allow(strings.ToLower(email)) {
		uc.logger.Info(ctx, "passkey recovery not announced: per-address limit reached")
		return
	}
	bgCtx := context.WithoutCancel(ctx)
	uc.background(func() {
		ctx, cancel := context.WithTimeout(bgCtx, passkeyRecoveryTimeout)
		defer cancel()
		uc.announcePasskeyRecovery(ctx, email)
	})
}

// announcePasskeyRecovery reads the User whose email is the address and, when
// there is one, announces a passkey recovery request for it. The address is
// never logged; failures are.
func (uc *userUseCase) announcePasskeyRecovery(ctx context.Context, email string) {
	user, err := uc.userRepo.GetByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			uc.logger.Info(ctx, "passkey recovery not announced: no user has the address")
			return
		}
		uc.logger.Error(ctx, "failed to read the user for a passkey recovery request", err)
		return
	}
	if err := uc.publishEvent(ctx, entity.SubjectUserPasskeyRecoveryRequested,
		entity.UserPasskeyRecoveryRequestedData{UserID: user.ID}); err != nil {
		uc.logger.Error(ctx, "failed to announce a passkey recovery request", err,
			slog.String("user_id", user.ID))
		return
	}
	uc.logger.Info(ctx, "passkey recovery request announced", slog.String("user_id", user.ID))
}

// SendPasskeyRecoveryLink implements [UserUseCase].
func (uc *userUseCase) SendPasskeyRecoveryLink(ctx context.Context, userID string) error {
	attrs := slog.String("user_id", userID)
	user, err := uc.userRepo.Get(ctx, userID)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			uc.logger.Info(ctx, "passkey recovery link not sent: the user no longer exists", attrs)
			return nil
		}
		return fmt.Errorf("get user %q for a passkey recovery link: %w", userID, err)
	}
	if uc.passkeyMailers.RegistrationLinks == nil {
		return apperr.New(codes.Internal, "passkey registration links are not configured", attrs)
	}
	if err := uc.passkeyMailers.RegistrationLinks.SendPasskeyRegistrationLink(ctx, user); err != nil {
		switch {
		case errors.Is(err, apperr.ErrNotFound):
			uc.logger.Info(ctx, "passkey recovery link not sent: the identity no longer exists", attrs)
			return nil
		case errors.Is(err, apperr.ErrFailedPrecondition):
			uc.logger.Info(ctx, "passkey recovery link not sent: the identity's address is not verified", attrs)
			return nil
		}
		return fmt.Errorf("send passkey registration link to user %q: %w", userID, err)
	}
	uc.logger.Info(ctx, "passkey recovery link sent", attrs)
	return nil
}

// NotifyPasskeyChange implements [UserUseCase].
func (uc *userUseCase) NotifyPasskeyChange(ctx context.Context, providerUserID string, change entity.PasskeyChange) error {
	attrs := []slog.Attr{slog.String("provider_user_id", providerUserID), slog.String("kind", string(change.Kind))}
	if !change.Kind.IsValid() {
		return apperr.New(codes.InvalidArgument, "unknown kind of passkey change", attrs...)
	}
	user, err := uc.userRepo.GetByProviderUserID(ctx, providerUserID)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			uc.logger.Info(ctx, "passkey change not emailed: no user has the identity", attrs...)
			return nil
		}
		return fmt.Errorf("get user by provider user ID %q for a passkey change: %w", providerUserID, err)
	}
	if uc.passkeyMailers.ChangeEmails == nil {
		return apperr.New(codes.Unavailable, "passkey change emails are not configured", attrs...)
	}
	if err := uc.passkeyMailers.ChangeEmails.SendPasskeyChangeEmail(ctx, user, change); err != nil {
		return fmt.Errorf("send passkey change email to user %q: %w", user.ID, err)
	}
	uc.logger.Info(ctx, "passkey change emailed", append(attrs, slog.String("user_id", user.ID))...)
	return nil
}
