// Package usecase contains business logic implementations for the application.
package usecase

import (
	"context"
	"log/slog"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/pkg/apperr"
	"github.com/liverty-music/backend/pkg/apperr/codes"
	"github.com/liverty-music/backend/pkg/logging"
)

// UserUseCase handles user business logic.
type UserUseCase struct {
	userRepo entity.UserRepository
	logger   *logging.Logger
}

// NewUserUseCase creates a new user use case.
func NewUserUseCase(userRepo entity.UserRepository, logger *logging.Logger) *UserUseCase {
	return &UserUseCase{
		userRepo: userRepo,
		logger:   logger,
	}
}

// CreateUser creates a new user.
func (uc *UserUseCase) CreateUser(ctx context.Context, params *entity.NewUser) (*entity.User, error) {
	user, err := uc.userRepo.Create(ctx, params)
	if err != nil {
		return nil, apperr.Wrap(err, codes.Internal, "failed to create user", 
			slog.String("name", params.Name),
			slog.String("email", params.Email),
		)
	}

	uc.logger.Info(ctx, "User created successfully", slog.String("user_id", user.ID))

	return user, nil
}

// GetUser retrieves a user by ID.
func (uc *UserUseCase) GetUser(ctx context.Context, id string) (*entity.User, error) {
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

// DeleteUser deletes a user by ID.
func (uc *UserUseCase) DeleteUser(ctx context.Context, id string) error {
	if id == "" {
		return apperr.New(codes.InvalidArgument, "user ID cannot be empty")
	}

	err := uc.userRepo.Delete(ctx, id)
	if err != nil {
		return apperr.Wrap(err, codes.Internal, "failed to delete user", 
			slog.String("user_id", id),
		)
	}

	uc.logger.Info(ctx, "User deleted successfully", slog.String("user_id", id))

	return nil
}
