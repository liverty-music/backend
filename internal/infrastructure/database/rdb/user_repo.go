package rdb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/liverty-music/backend/internal/adapter/pb"
	"github.com/liverty-music/backend/internal/entity"
	infrageo "github.com/liverty-music/backend/internal/infrastructure/geo"
	"github.com/liverty-music/backend/internal/infrastructure/messaging"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
)

// UserRepository implements entity.UserRepository interface.
type UserRepository struct {
	db     *Database
	outbox Announcer
}

const (
	userColumns = `u.id, u.provider_user_id, u.email, u.name, u.preferred_language, u.time_zone, u.full_name, u.phone_number, u.last_login_at`

	homeColumns = `h.id, h.country_code, h.level_1, h.level_2, h.centroid_latitude, h.centroid_longitude`

	getUserQuery = `
		SELECT ` + userColumns + `, ` + homeColumns + `
		FROM users u
		LEFT JOIN homes h ON u.home_id = h.id
		WHERE u.id = $1
	`

	getUserByProviderUserIDQuery = `
		SELECT ` + userColumns + `, ` + homeColumns + `
		FROM users u
		LEFT JOIN homes h ON u.home_id = h.id
		WHERE u.provider_user_id = $1
	`

	// getUsersByEmailQuery matches the address with letter case ignored
	// (served by idx_users_email_lower). UNIQUE (email) is case-sensitive, so
	// two rows can match; LIMIT 2 is enough to tell one match from several.
	getUsersByEmailQuery = `
		SELECT ` + userColumns + `, ` + homeColumns + `
		FROM users u
		LEFT JOIN homes h ON u.home_id = h.id
		WHERE lower(u.email) = lower($1)
		LIMIT 2
	`

	// Atomic UPDATE + SELECT in a single statement so the read-after-write
	// can't race with a concurrent DELETE on the same user. The CTE
	// returns 0 rows if no row matches the WHERE, which scanUser surfaces
	// as ErrNoRows and the caller maps to NotFound.
	updatePreferredLanguageQuery = `
		WITH updated AS (
			UPDATE users SET preferred_language = $2
			WHERE id = $1
			RETURNING *
		)
		SELECT ` + userColumns + `, ` + homeColumns + `
		FROM updated u
		LEFT JOIN homes h ON u.home_id = h.id
	`

	// updatePersonalDetailsQuery sets both personal details columns in one
	// atomic UPDATE + SELECT, like updatePreferredLanguageQuery. A NULL
	// parameter stores the value as absent.
	updatePersonalDetailsQuery = `
		WITH updated AS (
			UPDATE users SET full_name = $2, phone_number = $3
			WHERE id = $1
			RETURNING *
		)
		SELECT ` + userColumns + `, ` + homeColumns + `
		FROM updated u
		LEFT JOIN homes h ON u.home_id = h.id
	`

	insertHomeQuery = `
		INSERT INTO homes (id, country_code, level_1, level_2, centroid_latitude, centroid_longitude)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id
	`

	updateHomeQuery = `
		UPDATE homes SET
			country_code = $2,
			level_1 = $3,
			level_2 = $4,
			centroid_latitude = $5,
			centroid_longitude = $6
		WHERE id = $1
		RETURNING id
	`

	setUserHomeIDQuery = `
		UPDATE users SET home_id = $2 WHERE id = $1
	`

	insertUserQuery = `
		INSERT INTO users (id, provider_user_id, email, name, preferred_language, time_zone)
		VALUES ($1, $2, $3, $4, $5, $6)
	`

	deleteUserQuery = `
		DELETE FROM users WHERE id = $1
	`

	// userDeletionLockQuery and userCreateLockQuery take the user's
	// transaction-scoped advisory lock, exclusive for a deletion and shared
	// for a create of a record that blocks one; both are released at commit
	// or rollback, and when the session ends.
	userDeletionLockQuery = `SELECT pg_advisory_xact_lock(hashtextextended('user:' || $1, 0))`
	userCreateLockQuery   = `SELECT pg_advisory_xact_lock_shared(hashtextextended('user:' || $1, 0))`
	userExistsQuery       = `SELECT EXISTS (SELECT 1 FROM users WHERE id = $1)`
	// recordLoginQuery moves the last login time forward only.
	recordLoginQuery = `
		UPDATE users SET last_login_at = $2
		WHERE id = $1 AND (last_login_at IS NULL OR last_login_at < $2)
	`
)

// NewUserRepository creates a new user repository instance.
func NewUserRepository(db *Database, outbox Announcer) *UserRepository {
	return &UserRepository{db: db, outbox: outbox}
}

// nullStringFromEmpty maps Go's zero value (empty string) to a NULL
// SQL value, and any non-empty string to a present value. Used at the
// write boundary so columns whose semantics distinguish "absent" from
// "explicitly empty" (e.g. users.preferred_language) preserve that
// distinction in the database.
func nullStringFromEmpty(s string) sql.NullString {
	if s == "" {
		return sql.NullString{Valid: false}
	}
	return sql.NullString{String: s, Valid: true}
}

// scanUser scans a user row with optional home columns from a LEFT JOIN.
//
// Every nullable users column (preferred_language, time_zone)
// is scanned into a sql.NullString intermediate, then assigned
// to the entity field via .String when .Valid. pgx errors out when scanning
// SQL NULL into a non-nullable Go destination (a bare string); the three
// columns above are nullable per schema and at least one
// (preferred_language) is regularly NULL on legacy rows after the
// persist-user-language migration, so the intermediate-local pattern
// protects every read path that shares this scanner (Get, GetByProviderUserID,
// GetByEmail, Update, UpdatePreferredLanguage, UpdateHome's re-fetch, List).
func scanUser(scanner interface{ Scan(dest ...any) error }) (*entity.User, error) {
	user := &entity.User{}
	var preferredLanguage, timeZone sql.NullString
	var fullName, phoneNumber sql.NullString
	var lastLoginAt sql.NullTime
	var homeID, countryCode, level1, level2 sql.NullString
	var centroidLat, centroidLng sql.NullFloat64

	err := scanner.Scan(
		&user.ID, &user.ProviderUserID, &user.Email, &user.Name,
		&preferredLanguage, &timeZone,
		&fullName, &phoneNumber, &lastLoginAt,
		&homeID, &countryCode, &level1, &level2, &centroidLat, &centroidLng,
	)
	if err != nil {
		return nil, err
	}
	if preferredLanguage.Valid {
		user.PreferredLanguage = preferredLanguage.String
	}
	if timeZone.Valid {
		user.TimeZone = timeZone.String
	}
	if fullName.Valid {
		user.FullName = fullName.String
	}
	if phoneNumber.Valid {
		user.PhoneNumber = phoneNumber.String
	}
	if lastLoginAt.Valid {
		t := lastLoginAt.Time
		user.LastLoginTime = &t
	}

	if homeID.Valid {
		user.Home = &entity.Home{
			ID:          homeID.String,
			CountryCode: countryCode.String,
			Level1:      level1.String,
		}
		if centroidLat.Valid && centroidLng.Valid {
			user.Home.Centroid = &entity.Coordinates{
				Latitude:  centroidLat.Float64,
				Longitude: centroidLng.Float64,
			}
		}
		if level2.Valid {
			user.Home.Level2 = &level2.String
		}
	}

	return user, nil
}

// Create creates a new user in the database.
// If params.Home is non-nil, the home record is inserted atomically.
//
// # Announces
//
// USER.created with the User without its full name, phone number and display
// name, in the transaction of the insert. Nothing on AlreadyExists.
func (r *UserRepository) Create(ctx context.Context, params *entity.NewUser) (*entity.User, error) {
	if params == nil {
		return nil, apperr.New(codes.InvalidArgument, "params cannot be nil")
	}

	// Create returns the in-memory entity built from params (not a row
	// re-fetched after INSERT). This is intentional and currently safe:
	// every column we write here round-trips identically through pgx (no
	// triggers, generated columns, or server-side normalization on
	// users.* writes). If that ever changes — e.g., a DB-side normalizer
	// is added to preferred_language, or a trigger rewrites name — switch
	// Create to the CTE RETURNING + scanUser pattern that Update,
	// UpdateHome, and UpdatePreferredLanguage already use, so the caller
	// observes DB truth instead of a stale snapshot of params.
	user := entity.CreateUser(params)

	tx, err := r.db.Pool.Begin(ctx)
	if err != nil {
		return nil, toAppErr(err, "failed to begin transaction")
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	// Preserve the NULL = "client has not yet asserted a value" invariant
	// for every nullable users column. Callers (handlers, mappers, the
	// guest-bootstrap path) carry these fields as Go strings, so an
	// unfilled field arrives here as the empty string. Mapping "" to
	// sql.NullString{Valid:false} keeps the distinction between "absent"
	// and "explicitly empty" in the database, which the scanUser pattern
	// preserves on read. preferred_language uses this distinction to feed
	// the hydration backfill path; time_zone uses it for
	// downstream observability (a NULL row is a "never asserted" signal
	// while an empty string would be a contract violation).
	_, err = tx.Exec(ctx, insertUserQuery,
		user.ID, params.ProviderUserID, params.Email, params.Name,
		nullStringFromEmpty(params.PreferredLanguage),
		nullStringFromEmpty(params.TimeZone),
	)
	if err != nil {
		if IsUniqueViolation(err) {
			r.db.logger.Warn(ctx, "duplicate user",
				slog.String("entityType", "user"),
				slog.String("email", params.Email),
			)
		}
		return nil, toAppErr(err, "failed to create user", slog.String("email", params.Email))
	}

	var home *entity.Home
	if params.Home != nil {
		home = entity.NewHome(params.Home.CountryCode, params.Home.Level1, params.Home.Level2)
		var centroidLat, centroidLng *float64
		if c, ok := infrageo.ResolveCentroid(params.Home.Level1); ok {
			centroidLat = &c.Latitude
			centroidLng = &c.Longitude
			home.Centroid = &entity.Coordinates{
				Latitude:  c.Latitude,
				Longitude: c.Longitude,
			}
		}

		var homeID string
		err = tx.QueryRow(ctx, insertHomeQuery,
			home.ID, home.CountryCode, home.Level1, home.Level2,
			centroidLat, centroidLng,
		).Scan(&homeID)
		if err != nil {
			return nil, toAppErr(err, "failed to create home", slog.String("user_id", user.ID))
		}
		home.ID = homeID

		_, err = tx.Exec(ctx, setUserHomeIDQuery, user.ID, homeID)
		if err != nil {
			return nil, toAppErr(err, "failed to set user home_id", slog.String("user_id", user.ID))
		}
	}

	user.Home = home
	if err := announce(ctx, r.outbox, tx, entity.SubjectUserCreated, messaging.Announcement{Payload: pb.FromUserForAnnouncement(user)}); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, toAppErr(err, "failed to commit transaction")
	}

	r.db.logger.Info(ctx, "user created",
		slog.String("entityType", "user"),
		slog.String("userID", user.ID),
	)

	user.Home = home
	return user, nil
}

// RecordLogin sets the user's last login time to at when it has none or an
// earlier one.
//
// # Announces
//
// USER.logged_in with the User and its new last login time, without its full
// name, phone number and display name, in the transaction of the change.
// Nothing when the stored time is the same or later.
func (r *UserRepository) RecordLogin(ctx context.Context, id string, at time.Time) error {
	attrs := []slog.Attr{slog.String("user_id", id)}
	tx, err := r.db.Pool.Begin(ctx)
	if err != nil {
		return toAppErr(err, "failed to begin login record", attrs...)
	}
	// Rollback is a no-op after a successful Commit.
	defer func() { _ = tx.Rollback(ctx) }()

	tag, err := tx.Exec(ctx, recordLoginQuery, id, at)
	if err != nil {
		return toAppErr(err, "failed to record login", attrs...)
	}
	if tag.RowsAffected() == 0 {
		var exists bool
		if err := tx.QueryRow(ctx, userExistsQuery, id).Scan(&exists); err != nil {
			return toAppErr(err, "failed to read the user", attrs...)
		}
		if !exists {
			return apperr.New(codes.NotFound, "user not found", attrs...)
		}
		return nil
	}
	user, err := scanUser(tx.QueryRow(ctx, getUserQuery, id))
	if err != nil {
		return toAppErr(err, "failed to read the signed-in user", attrs...)
	}
	if err := announce(ctx, r.outbox, tx, entity.SubjectUserLoggedIn, messaging.Announcement{Payload: pb.FromUserForAnnouncement(user)}); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return toAppErr(err, "failed to commit login record", attrs...)
	}
	return nil
}

// Get retrieves a user by ID from the database.
func (r *UserRepository) Get(ctx context.Context, id string) (*entity.User, error) {
	if id == "" {
		return nil, apperr.New(codes.InvalidArgument, "user ID cannot be empty")
	}

	user, err := scanUser(r.db.Pool.QueryRow(ctx, getUserQuery, id))
	if err != nil {
		return nil, toAppErr(err, "failed to get user", slog.String("user_id", id))
	}

	return user, nil
}

// GetByProviderUserID retrieves a user by identity provider ID from the database.
func (r *UserRepository) GetByProviderUserID(ctx context.Context, providerUserID string) (*entity.User, error) {
	if providerUserID == "" {
		return nil, apperr.New(codes.InvalidArgument, "provider user ID cannot be empty")
	}

	user, err := scanUser(r.db.Pool.QueryRow(ctx, getUserByProviderUserIDQuery, providerUserID))
	if err != nil {
		return nil, toAppErr(err, "failed to get user by provider user ID", slog.String("provider_user_id", providerUserID))
	}

	return user, nil
}

// GetByEmail retrieves the user whose email equals email when letter case is
// ignored. More than one match fails with Internal rather than picking one.
func (r *UserRepository) GetByEmail(ctx context.Context, email string) (*entity.User, error) {
	if email == "" {
		return nil, apperr.New(codes.InvalidArgument, "email cannot be empty")
	}

	rows, err := r.db.Pool.Query(ctx, getUsersByEmailQuery, email)
	if err != nil {
		return nil, toAppErr(err, "failed to get user by email")
	}
	defer rows.Close()

	var users []*entity.User
	for rows.Next() {
		user, err := scanUser(rows)
		if err != nil {
			return nil, toAppErr(err, "failed to scan user by email")
		}
		users = append(users, user)
	}
	if err := rows.Err(); err != nil {
		return nil, toAppErr(err, "failed to get user by email")
	}

	switch len(users) {
	case 0:
		return nil, apperr.New(codes.NotFound, "no user has the email")
	case 1:
		return users[0], nil
	default:
		return nil, apperr.New(codes.Internal, "more than one user has the email when letter case is ignored",
			slog.String("user_id", users[0].ID), slog.String("other_user_id", users[1].ID))
	}
}

// UpdatePersonalDetails sets the user's full name and phone number, storing
// an empty value as absent, and returns the refreshed user.
func (r *UserRepository) UpdatePersonalDetails(ctx context.Context, id string, fullName, phoneNumber string) (*entity.User, error) {
	if err := entity.ValidatePersonalDetails(fullName, phoneNumber); err != nil {
		return nil, apperr.Wrap(err, codes.InvalidArgument, "invalid personal details")
	}
	user, err := scanUser(r.db.Pool.QueryRow(ctx, updatePersonalDetailsQuery, id,
		nullStringFromEmpty(fullName), nullStringFromEmpty(phoneNumber)))
	if err != nil {
		return nil, toAppErr(err, "failed to update personal details", slog.String("user_id", id))
	}
	return user, nil
}

// UpdatePreferredLanguage sets the user's preferred display language.
// It performs a focused UPDATE on the preferred_language column and returns
// the refreshed user entity via the standard SELECT query.
func (r *UserRepository) UpdatePreferredLanguage(ctx context.Context, id, lang string) (*entity.User, error) {
	if id == "" {
		return nil, apperr.New(codes.InvalidArgument, "user ID cannot be empty")
	}
	// UpdatePreferredLanguage's RPC contract requires a non-empty
	// ISO 639-1 code (protovalidate enforces min_len: 2 + pattern). An
	// empty value reaching the repository is a programmer error; reject
	// loudly rather than write a NULL that downstream code would
	// misinterpret as "client has not yet asserted a preference".
	if lang == "" {
		return nil, apperr.New(codes.InvalidArgument, "preferred language cannot be empty")
	}

	// Atomic UPDATE + SELECT via CTE — no race window with a concurrent
	// DELETE on this user. If no row matches, scanUser returns ErrNoRows
	// which we wrap as NotFound.
	//
	// Belt-and-suspenders: pass via nullStringFromEmpty so that IF the
	// `lang == ""` guard above ever gets removed by a future refactor,
	// the query writes SQL NULL (preserving the "client has not yet
	// asserted" invariant and inviting a backfill on next hydration)
	// rather than an empty string (which would silently escape the
	// IS NULL invariant). The guard returning InvalidArgument is the
	// intended contract — this is the failure-mode safety net.
	user, err := scanUser(r.db.Pool.QueryRow(ctx, updatePreferredLanguageQuery, id, nullStringFromEmpty(lang)))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, apperr.Wrap(apperr.ErrNotFound, codes.NotFound, fmt.Sprintf("user with ID %s not found", id))
		}
		return nil, toAppErr(err, "failed to update preferred language", slog.String("user_id", id))
	}

	r.db.logger.Info(ctx, "user updated",
		slog.String("entityType", "user"),
		slog.String("userID", id),
		slog.String("field", "preferredLanguage"),
	)

	return user, nil
}

// UpdateHome sets or changes the user's home area.
// If the user already has a home record it is updated in place; otherwise a new
// home record is inserted and linked to the user via users.home_id.
func (r *UserRepository) UpdateHome(ctx context.Context, id string, home *entity.Home) (*entity.User, error) {
	if id == "" {
		return nil, apperr.New(codes.InvalidArgument, "user ID cannot be empty")
	}

	tx, err := r.db.Pool.Begin(ctx)
	if err != nil {
		return nil, toAppErr(err, "failed to begin transaction")
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	// Fetch current user to determine whether a home record already exists.
	current, err := scanUser(tx.QueryRow(ctx, getUserQuery, id))
	if err != nil {
		return nil, toAppErr(err, "failed to get user for home update", slog.String("user_id", id))
	}

	var centroidLat, centroidLng *float64
	if c, ok := infrageo.ResolveCentroid(home.Level1); ok {
		centroidLat = &c.Latitude
		centroidLng = &c.Longitude
	}

	if current.Home != nil {
		// Update the existing home record.
		_, err = tx.Exec(ctx, updateHomeQuery,
			current.Home.ID, home.CountryCode, home.Level1, home.Level2,
			centroidLat, centroidLng,
		)
		if err != nil {
			return nil, toAppErr(err, "failed to update home", slog.String("user_id", id))
		}
	} else {
		// Insert a new home record and link it to the user.
		newHome := entity.NewHome(home.CountryCode, home.Level1, home.Level2)
		var homeID string
		err = tx.QueryRow(ctx, insertHomeQuery,
			newHome.ID, newHome.CountryCode, newHome.Level1, newHome.Level2,
			centroidLat, centroidLng,
		).Scan(&homeID)
		if err != nil {
			return nil, toAppErr(err, "failed to insert home", slog.String("user_id", id))
		}

		_, err = tx.Exec(ctx, setUserHomeIDQuery, id, homeID)
		if err != nil {
			return nil, toAppErr(err, "failed to set user home_id", slog.String("user_id", id))
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, toAppErr(err, "failed to commit transaction")
	}

	// Re-fetch the user with home JOIN to return the complete entity.
	user, err := scanUser(r.db.Pool.QueryRow(ctx, getUserQuery, id))
	if err != nil {
		return nil, toAppErr(err, "failed to get user after home update", slog.String("user_id", id))
	}

	r.db.logger.Info(ctx, "user updated",
		slog.String("entityType", "user"),
		slog.String("userID", id),
		slog.String("field", "home"),
	)

	return user, nil
}

// SerializeDeletion runs fn while holding the user's deletion lock
// exclusively, taken in a transaction on its own connection. fn's own reads
// and writes run on other connections, so they never wait for this lock.
func (r *UserRepository) SerializeDeletion(ctx context.Context, id string, fn func(ctx context.Context) error) error {
	attrs := slog.String("user_id", id)
	tx, err := r.db.Pool.Begin(ctx)
	if err != nil {
		return toAppErr(err, "failed to begin user deletion lock", attrs)
	}
	// Rollback releases the lock; it is the only way the transaction ends.
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	if _, err := tx.Exec(ctx, userDeletionLockQuery, id); err != nil {
		return toAppErr(err, "failed to lock user deletion", attrs)
	}
	return fn(ctx)
}

// lockUserForCreate holds the user's deletion lock shared for the rest of tx
// and confirms the user still exists, so a record created in tx never
// outlives a deletion of its user (UserRepository.SerializeDeletion).
func lockUserForCreate(ctx context.Context, tx pgx.Tx, userID entity.UserID) error {
	attrs := slog.String("user_id", string(userID))
	if _, err := tx.Exec(ctx, userCreateLockQuery, string(userID)); err != nil {
		return toAppErr(err, "failed to lock the user against deletion", attrs)
	}
	var exists bool
	if err := tx.QueryRow(ctx, userExistsQuery, string(userID)).Scan(&exists); err != nil {
		return toAppErr(err, "failed to check the user exists", attrs)
	}
	if !exists {
		return apperr.New(codes.NotFound, "the user does not exist", attrs)
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
