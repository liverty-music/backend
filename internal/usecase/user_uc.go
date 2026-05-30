// Package usecase contains business logic implementations for the application.
package usecase

import (
	"context"
	"errors"
	"log/slog"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/pannpers/go-logging/logging"
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

	// Delete removes a user from the system.
	//
	// # Possible errors
	//
	//  - NotFound: If the user does not exist.
	Delete(ctx context.Context, id string) error
}

// userUseCase implements the UserUseCase interface.
type userUseCase struct {
	userRepo  entity.UserRepository
	publisher EventPublisher
	logger    *logging.Logger
}

// Compile-time interface compliance check
var _ UserUseCase = (*userUseCase)(nil)

// NewUserUseCase creates a new user use case.
// It requires a user repository for data persistence, a publisher for domain
// events, and a logger.
func NewUserUseCase(userRepo entity.UserRepository, publisher EventPublisher, logger *logging.Logger) UserUseCase {
	return &userUseCase{
		userRepo:  userRepo,
		publisher: publisher,
		logger:    logger,
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
		return nil, apperr.Wrap(err, codes.NotFound, "failed to get user",
			slog.String("user_id", id),
		)
	}

	return user, nil
}

// GetByExternalID retrieves a user by identity provider ID.
func (uc *userUseCase) GetByExternalID(ctx context.Context, externalID string) (*entity.User, error) {
	user, err := uc.userRepo.GetByExternalID(ctx, externalID)
	if err != nil {
		return nil, apperr.Wrap(err, codes.NotFound, "failed to get user by external ID",
			slog.String("external_id", externalID),
		)
	}

	return user, nil
}

// UpdatePreferredLanguage sets the user's preferred display language.
//
// Validates `lang` against the ISO 639-1 two-letter pattern before reaching
// the repository — mirrors UpdateHome's `home.Validate()` posture so that
// non-RPC callers (integration tests, future internal handlers, scripts)
// don't bypass the wire-layer protovalidate constraint.
//
// Captures the prior PreferredLanguage value via a pre-update Get so the
// USER.preferred_language_updated event can carry both from_locale and
// to_locale. The extra read is acceptable: this operation is rare and the
// event's analytical value depends on the locale-transition pair.
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

	prior, err := uc.userRepo.Get(ctx, id)
	if err != nil {
		return nil, apperr.Wrap(err, codes.NotFound, "failed to load user for preferred-language update",
			slog.String("user_id", id),
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

	if err := uc.publishEvent(ctx, entity.SubjectUserPreferredLanguageUpdated, entity.UserPreferredLanguageUpdatedData{
		UserID:     user.ID,
		FromLocale: prior.PreferredLanguage,
		ToLocale:   user.PreferredLanguage,
	}); err != nil {
		uc.logger.Error(ctx, "failed to publish USER.preferred_language_updated event", err,
			slog.String("user_id", user.ID),
		)
		// Non-fatal: the language change is already persisted.
	}

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

// Delete deletes a user by ID.
func (uc *userUseCase) Delete(ctx context.Context, id string) error {
	err := uc.userRepo.Delete(ctx, id)
	if err != nil {
		return err
	}

	uc.logger.Info(ctx, "User deleted successfully", slog.String("user_id", id))

	return nil
}
