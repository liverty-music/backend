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

	user, err := uc.userRepo.Create(ctx, params)
	if err != nil {
		// On AlreadyExists, distinguish between (a) same caller retrying the
		// Create (duplicate external_id) — treat as idempotent success — and
		// (b) a different identity claiming the same email — surface the
		// original error. GetByExternalID resolves which case we are in.
		if errors.Is(err, apperr.ErrAlreadyExists) {
			existing, getErr := uc.userRepo.GetByExternalID(ctx, params.ExternalID)
			if getErr != nil {
				// external_id was NOT the conflicting column — propagate the
				// original AlreadyExists to signal the email collision.
				return nil, err
			}
			uc.logger.Info(ctx, "Create returned existing user (idempotent on duplicate external_id)",
				slog.String("user_id", existing.ID),
				slog.String("external_id", existing.ExternalID),
			)
			return existing, nil
		}
		return nil, err
	}

	if user == nil {
		return nil, apperr.New(codes.Internal, "repository returned nil user without error")
	}

	uc.logger.Info(ctx, "User created successfully", slog.String("user_id", user.ID))

	if err := uc.publishEvent(ctx, entity.SubjectUserCreated, entity.UserCreatedData{
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
