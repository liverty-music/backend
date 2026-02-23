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
		SELECT id, external_id, email, name, preferred_language, country, time_zone, COALESCE(safe_address, ''), is_active
		FROM users
		WHERE id = $1
	`

	getUserByExternalIDQuery = `
		SELECT id, external_id, email, name, preferred_language, country, time_zone, COALESCE(safe_address, ''), is_active
		FROM users
		WHERE external_id = $1
	`

	getUserByEmailQuery = `
		SELECT id, external_id, email, name, preferred_language, country, time_zone, COALESCE(safe_address, ''), is_active
		FROM users
		WHERE email = $1
	`

	updateUserQuery = `
		UPDATE users SET external_id = $2, email = $3, name = $4, preferred_language = $5, country = $6, time_zone = $7
		WHERE id = $1
		RETURNING id, external_id, email, name, preferred_language, country, time_zone, COALESCE(safe_address, ''), is_active
	`

	listUsersQuery = `
		SELECT id, external_id, email, name, preferred_language, country, time_zone, COALESCE(safe_address, ''), is_active
		FROM users
		ORDER BY id
		LIMIT $1 OFFSET $2
	`

	updateSafeAddressQuery = `
		UPDATE users SET safe_address = $2 WHERE id = $1
	`

	insertUserQuery = `
		INSERT INTO users (external_id, email, name, preferred_language, country, time_zone, is_active)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id
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
	).Scan(&user.ID)
	if err != nil {
		if IsUniqueViolation(err) {
			r.db.logger.Warn(ctx, "duplicate user",
				slog.String("entityType", "user"),
				slog.String("email", user.Email),
			)
		}
		return nil, toAppErr(err, "failed to create user", slog.String("email", user.Email))
	}

	r.db.logger.Info(ctx, "user created",
		slog.String("entityType", "user"),
		slog.String("userID", user.ID),
	)

	return user, nil
}

// Get retrieves a user by ID from the database.
func (r *UserRepository) Get(ctx context.Context, id string) (*entity.User, error) {
	if id == "" {
		return nil, apperr.New(codes.InvalidArgument, "user ID cannot be empty")
	}

	user := &entity.User{}
	err := r.db.Pool.QueryRow(ctx, getUserQuery, id).Scan(
		&user.ID, &user.ExternalID, &user.Email, &user.Name, &user.PreferredLanguage, &user.Country, &user.TimeZone, &user.SafeAddress, &user.IsActive,
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
		&user.ID, &user.ExternalID, &user.Email, &user.Name, &user.PreferredLanguage, &user.Country, &user.TimeZone, &user.SafeAddress, &user.IsActive,
	)
	if err != nil {
		return nil, toAppErr(err, "failed to get user by external ID", slog.String("external_id", externalID))
	}

	return user, nil
}

// GetByEmail retrieves a user by email address from the database.
func (r *UserRepository) GetByEmail(ctx context.Context, email string) (*entity.User, error) {
	if email == "" {
		return nil, apperr.New(codes.InvalidArgument, "email cannot be empty")
	}

	user := &entity.User{}
	err := r.db.Pool.QueryRow(ctx, getUserByEmailQuery, email).Scan(
		&user.ID, &user.ExternalID, &user.Email, &user.Name, &user.PreferredLanguage, &user.Country, &user.TimeZone, &user.SafeAddress, &user.IsActive,
	)
	if err != nil {
		return nil, toAppErr(err, "failed to get user by email", slog.String("email", email))
	}

	return user, nil
}

// Update updates user information in the database.
func (r *UserRepository) Update(ctx context.Context, id string, params *entity.NewUser) (*entity.User, error) {
	if id == "" {
		return nil, apperr.New(codes.InvalidArgument, "user ID cannot be empty")
	}
	if params == nil {
		return nil, apperr.New(codes.InvalidArgument, "params cannot be nil")
	}

	user := &entity.User{}
	err := r.db.Pool.QueryRow(ctx, updateUserQuery,
		id, params.ExternalID, params.Email, params.Name, params.PreferredLanguage, params.Country, params.TimeZone,
	).Scan(
		&user.ID, &user.ExternalID, &user.Email, &user.Name, &user.PreferredLanguage, &user.Country, &user.TimeZone, &user.SafeAddress, &user.IsActive,
	)
	if err != nil {
		return nil, toAppErr(err, "failed to update user", slog.String("user_id", id))
	}

	r.db.logger.Info(ctx, "user updated",
		slog.String("entityType", "user"),
		slog.String("userID", user.ID),
	)

	return user, nil
}

// List retrieves users with pagination from the database.
func (r *UserRepository) List(ctx context.Context, limit, offset int) ([]*entity.User, error) {
	rows, err := r.db.Pool.Query(ctx, listUsersQuery, limit, offset)
	if err != nil {
		return nil, toAppErr(err, "failed to list users")
	}
	defer rows.Close()

	var users []*entity.User
	for rows.Next() {
		user := &entity.User{}
		if err := rows.Scan(
			&user.ID, &user.ExternalID, &user.Email, &user.Name, &user.PreferredLanguage, &user.Country, &user.TimeZone, &user.SafeAddress, &user.IsActive,
		); err != nil {
			return nil, toAppErr(err, "failed to scan user row")
		}
		users = append(users, user)
	}

	if err := rows.Err(); err != nil {
		return nil, toAppErr(err, "failed to iterate user rows")
	}

	return users, nil
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

	r.db.logger.Info(ctx, "user updated",
		slog.String("entityType", "user"),
		slog.String("userID", id),
		slog.String("field", "safeAddress"),
	)

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
