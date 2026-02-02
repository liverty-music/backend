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
	//
	// # Possible errors
	//
	//  - InvalidArgument: If email or name is invalid.
	//  - AlreadyExists: If a user with the same email already exists.
	Create(ctx context.Context, params *entity.NewUser) (*entity.User, error)

	// Get retrieves a user by their unique ID.
	//
	// # Possible errors
	//
	//  - NotFound: If the user does not exist.
	Get(ctx context.Context, id string) (*entity.User, error)

	// Delete removes a user from the system.
	//
	// # Possible errors
	//
	//  - NotFound: If the user does not exist.
	Delete(ctx context.Context, id string) error
}

// userUseCase implements the UserUseCase interface.
type userUseCase struct {
	userRepo entity.UserRepository
	logger   *logging.Logger
}

// Compile-time interface compliance check
var _ UserUseCase = (*userUseCase)(nil)

// NewUserUseCase creates a new user use case.
// It requires a user repository for data persistence and a logger.
func NewUserUseCase(userRepo entity.UserRepository, logger *logging.Logger) UserUseCase {
	return &userUseCase{
		userRepo: userRepo,
		logger:   logger,
	}
}

// Create creates a new user.
func (uc *userUseCase) Create(ctx context.Context, params *entity.NewUser) (*entity.User, error) {
	user, err := uc.userRepo.Create(ctx, params)
	if err != nil {
		return nil, err
	}

	uc.logger.Info(ctx, "User created successfully", slog.String("user_id", user.ID))

	return user, nil
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
