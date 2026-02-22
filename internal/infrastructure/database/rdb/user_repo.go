package rdb

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
)

// UserRepository implements entity.UserRepository interface.
type UserRepository struct {
	db *Database
}

const (
	getUserQuery = `
		SELECT id, external_id, email, name, preferred_language, country, time_zone, COALESCE(safe_address, ''), is_active, created_at, updated_at
		FROM users
		WHERE id = $1
	`

	getUserByExternalIDQuery = `
		SELECT id, external_id, email, name, preferred_language, country, time_zone, COALESCE(safe_address, ''), is_active, created_at, updated_at
		FROM users
		WHERE external_id = $1
	`

	updateSafeAddressQuery = `
		UPDATE users SET safe_address = $2, updated_at = NOW() WHERE id = $1
	`

	insertUserQuery = `
		INSERT INTO users (external_id, email, name, preferred_language, country, time_zone, is_active)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id, created_at, updated_at
	`
	deleteUserQuery = `
		DELETE FROM users WHERE id = $1
	`
)

// NewUserRepository creates a new user repository instance.
func NewUserRepository(db *Database) *UserRepository {
	return &UserRepository{db: db}
}

// Create creates a new user in the database.
func (r *UserRepository) Create(ctx context.Context, params *entity.NewUser) (*entity.User, error) {
	if params == nil {
		return nil, apperr.New(codes.InvalidArgument, "params cannot be nil")
	}

	user := &entity.User{
		ExternalID:        params.ExternalID,
		Email:             params.Email,
		Name:              params.Name,
		PreferredLanguage: params.PreferredLanguage,
		Country:           params.Country,
		TimeZone:          params.TimeZone,
		IsActive:          true,
	}

	err := r.db.Pool.QueryRow(ctx, insertUserQuery,
		user.ExternalID, user.Email, user.Name, user.PreferredLanguage, user.Country, user.TimeZone, user.IsActive,
	).Scan(&user.ID, &user.CreateTime, &user.UpdateTime)
	if err != nil {
		return nil, toAppErr(err, "failed to create user", slog.String("email", user.Email))
	}

	return user, nil
}

// Get retrieves a user by ID from the database.
func (r *UserRepository) Get(ctx context.Context, id string) (*entity.User, error) {
	if id == "" {
		return nil, apperr.New(codes.InvalidArgument, "user ID cannot be empty")
	}

	user := &entity.User{}
	err := r.db.Pool.QueryRow(ctx, getUserQuery, id).Scan(
		&user.ID, &user.ExternalID, &user.Email, &user.Name, &user.PreferredLanguage, &user.Country, &user.TimeZone, &user.SafeAddress, &user.IsActive, &user.CreateTime, &user.UpdateTime,
	)
	if err != nil {
		return nil, toAppErr(err, "failed to get user", slog.String("user_id", id))
	}

	return user, nil
}

// GetByExternalID retrieves a user by identity provider ID from the database.
func (r *UserRepository) GetByExternalID(ctx context.Context, externalID string) (*entity.User, error) {
	if externalID == "" {
		return nil, apperr.New(codes.InvalidArgument, "external ID cannot be empty")
	}

	user := &entity.User{}
	err := r.db.Pool.QueryRow(ctx, getUserByExternalIDQuery, externalID).Scan(
		&user.ID, &user.ExternalID, &user.Email, &user.Name, &user.PreferredLanguage, &user.Country, &user.TimeZone, &user.SafeAddress, &user.IsActive, &user.CreateTime, &user.UpdateTime,
	)
	if err != nil {
		return nil, toAppErr(err, "failed to get user by external ID", slog.String("external_id", externalID))
	}

	return user, nil
}

// UpdateSafeAddress sets the predicted Safe address for a user.
func (r *UserRepository) UpdateSafeAddress(ctx context.Context, id, safeAddress string) error {
	if id == "" {
		return apperr.New(codes.InvalidArgument, "user ID cannot be empty")
	}

	result, err := r.db.Pool.Exec(ctx, updateSafeAddressQuery, id, safeAddress)
	if err != nil {
		return toAppErr(err, "failed to update safe address", slog.String("user_id", id))
	}

	if result.RowsAffected() == 0 {
		return apperr.Wrap(apperr.ErrNotFound, codes.NotFound, fmt.Sprintf("user with ID %s not found", id))
	}

	return nil
}

// Delete removes a user from the database.
func (r *UserRepository) Delete(ctx context.Context, id string) error {
	if id == "" {
		return apperr.New(codes.InvalidArgument, "user ID cannot be empty")
	}

	result, err := r.db.Pool.Exec(ctx, deleteUserQuery, id)
	if err != nil {
		return toAppErr(err, "failed to delete user", slog.String("user_id", id))
	}

	if result.RowsAffected() == 0 {
		return apperr.Wrap(apperr.ErrNotFound, codes.NotFound, fmt.Sprintf("user with ID %s not found", id))
	}

	return nil
}
