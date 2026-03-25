// Package usecase contains business logic implementations for the application.
package usecase

import (
	"context"
	"log/slog"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/pannpers/go-logging/logging"
)

// UserUseCase defines the interface for user-related business logic.
type UserUseCase interface {
	// Create registers a new user.
	// If params.Home is non-nil, the home area is validated and persisted atomically.
	//
	// # Possible errors
	//
	//  - InvalidArgument: If email or name is invalid, or home is malformed.
	//  - AlreadyExists: If a user with the same email already exists.
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

// Create creates a new user.
func (uc *userUseCase) Create(ctx context.Context, params *entity.NewUser) (*entity.User, error) {
	if params.Home != nil {
		if err := params.Home.Validate(); err != nil {
			return nil, apperr.Wrap(err, codes.InvalidArgument, err.Error())
		}
	}

	user, err := uc.userRepo.Create(ctx, params)
	if err != nil {
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
	if id == "" {
		return nil, apperr.New(codes.InvalidArgument, "user ID cannot be empty")
	}

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
	if externalID == "" {
		return nil, apperr.New(codes.InvalidArgument, "external ID cannot be empty")
	}

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
	if id == "" {
		return nil, apperr.New(codes.InvalidArgument, "user ID cannot be empty")
	}
	if home == nil {
		return nil, apperr.New(codes.InvalidArgument, "home cannot be nil")
	}
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
	if id == "" {
		return apperr.New(codes.InvalidArgument, "user ID cannot be empty")
	}

	err := uc.userRepo.Delete(ctx, id)
	if err != nil {
		return err
	}

	uc.logger.Info(ctx, "User deleted successfully", slog.String("user_id", id))

	return nil
}
