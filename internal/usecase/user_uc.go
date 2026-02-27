// Package usecase contains business logic implementations for the application.
package usecase

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/pannpers/go-logging/logging"
)

// countryCodeRe validates ISO 3166-1 alpha-2 country codes (e.g., "JP", "US").
var countryCodeRe = regexp.MustCompile(`^[A-Z]{2}$`)

// iso31662Re validates ISO 3166-2 subdivision codes (e.g., "JP-13", "US-CA").
var iso31662Re = regexp.MustCompile(`^[A-Z]{2}-[A-Z0-9]{1,3}$`)

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

// validateHome checks that a Home value has valid country_code, level_1, and optional level_2.
func validateHome(home *entity.Home) error {
	if !countryCodeRe.MatchString(home.CountryCode) {
		return apperr.New(codes.InvalidArgument, "country_code must be a valid ISO 3166-1 alpha-2 code (e.g., JP)")
	}
	if !iso31662Re.MatchString(home.Level1) {
		return apperr.New(codes.InvalidArgument, "level_1 must be a valid ISO 3166-2 code (e.g., JP-13)")
	}
	// Ensure level_1 prefix matches country_code.
	if home.Level1[:2] != home.CountryCode {
		return apperr.New(codes.InvalidArgument,
			fmt.Sprintf("level_1 prefix %q does not match country_code %q", home.Level1[:2], home.CountryCode))
	}
	if home.Level2 != nil && (len(*home.Level2) == 0 || len(*home.Level2) > 20) {
		return apperr.New(codes.InvalidArgument, "level_2 must be between 1 and 20 characters when provided")
	}
	return nil
}

// Create creates a new user.
func (uc *userUseCase) Create(ctx context.Context, params *entity.NewUser) (*entity.User, error) {
	if params.Home != nil {
		if err := validateHome(params.Home); err != nil {
			return nil, err
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

// UpdateHome sets or changes the user's home area after validating the structured Home.
func (uc *userUseCase) UpdateHome(ctx context.Context, id string, home *entity.Home) (*entity.User, error) {
	if id == "" {
		return nil, apperr.New(codes.InvalidArgument, "user ID cannot be empty")
	}
	if home == nil {
		return nil, apperr.New(codes.InvalidArgument, "home cannot be nil")
	}
	if err := validateHome(home); err != nil {
		return nil, err
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
