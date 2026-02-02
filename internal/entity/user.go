package entity

import (
	"context"
	"time"
)

// User represents a user who registers for concert notifications.
//
// Corresponds to liverty_music.entity.v1.User.
type User struct {
	// ID is the unique identifier for the user (UUID).
	ID                string
	// Email is the user's email address.
	Email             string
	// Name is the user's display name.
	Name              string
	// PreferredLanguage is the user's preferred language code (e.g., "en", "ja").
	PreferredLanguage string
	// Country is the user's country code.
	Country           string
	// TimeZone is the user's preferred time zone.
	TimeZone          string
	// IsActive indicates if the user account is active.
	IsActive          bool
	// CreateTime is the timestamp when the user was created.
	CreateTime        time.Time
	// UpdateTime is the timestamp when the user was last updated.
	UpdateTime        time.Time
}

// NewUser represents data for creating a new user.
type NewUser struct {
	// Email is the user's email address.
	Email             string
	// Name is the user's display name.
	Name              string
	// PreferredLanguage is the user's preferred language code.
	PreferredLanguage string
	// Country is the user's country code.
	Country           string
	// TimeZone is the user's preferred time zone.
	TimeZone          string
}

// UserArtistSubscription represents a user's subscription to an artist.
//
// Corresponds to liverty_music.entity.v1.UserArtistSubscription.
type UserArtistSubscription struct {
	// ID is the unique identifier for the subscription.
	ID         string
	// UserID is the subscriber's ID.
	UserID     string
	// ArtistID is the subscribed artist's ID.
	ArtistID   string
	// IsActive indicates if the subscription is currently active.
	IsActive   bool
	// CreateTime is the timestamp when the subscription was created.
	CreateTime time.Time
	// UpdateTime is the timestamp when the subscription was last updated.
	UpdateTime time.Time
}

// NewUserArtistSubscription represents data for creating a new subscription.
type NewUserArtistSubscription struct {
	// UserID is the subscriber's ID.
	UserID   string
	// ArtistID is the subscribed artist's ID.
	ArtistID string
}

// UserRepository defines the interface for user data access.
type UserRepository interface {
	// Create creates a new user.
	//
	// # Possible errors
	//
	//  - InvalidArgument: If email or name is invalid.
	//  - AlreadyExists: If a user with the same email already exists.
	Create(ctx context.Context, params *NewUser) (*User, error)

	// Get retrieves a user by ID.
	//
	// # Possible errors
	//
	//  - NotFound: If the user does not exist.
	Get(ctx context.Context, id string) (*User, error)

	// GetByEmail retrieves a user by email address.
	//
	// # Possible errors
	//
	//  - NotFound: If the user does not exist.
	GetByEmail(ctx context.Context, email string) (*User, error)

	// Update updates user information.
	//
	// # Possible errors
	//
	//  - NotFound: If the user does not exist.
	Update(ctx context.Context, id string, params *NewUser) (*User, error)

	// Delete removes a user.
	//
	// # Possible errors
	//
	//  - NotFound: If the user does not exist.
	Delete(ctx context.Context, id string) error

	// List retrieves users with pagination.
	List(ctx context.Context, limit, offset int) ([]*User, error)
}

// UserArtistSubscriptionRepository defines the interface for user-artist subscription data access.
type UserArtistSubscriptionRepository interface {
	// Create creates a new user-artist subscription.
	//
	// # Possible errors
	//
	//  - InvalidArgument: If inputs are invalid.
	//  - AlreadyExists: If subscription already exists.
	Create(ctx context.Context, params *NewUserArtistSubscription) (*UserArtistSubscription, error)

	// Get retrieves a subscription by ID.
	//
	// # Possible errors
	//
	//  - NotFound: If not found.
	Get(ctx context.Context, id string) (*UserArtistSubscription, error)

	// GetByUserAndArtist retrieves a subscription for a specific user and artist.
	//
	// # Possible errors
	//
	//  - NotFound: If not found.
	GetByUserAndArtist(ctx context.Context, userID, artistID string) (*UserArtistSubscription, error)

	// GetByUser retrieves all subscriptions for a user with pagination.
	GetByUser(ctx context.Context, userID string, limit, offset int) ([]*UserArtistSubscription, error)

	// GetByArtist retrieves all subscriptions for an artist with pagination.
	GetByArtist(ctx context.Context, artistID string, limit, offset int) ([]*UserArtistSubscription, error)

	// Activate reactivates a subscription.
	//
	// # Possible errors
	//
	//  - NotFound: If not found.
	Activate(ctx context.Context, id string) error

	// Deactivate deactivates a subscription.
	//
	// # Possible errors
	//
	//  - NotFound: If not found.
	Deactivate(ctx context.Context, id string) error

	// Delete removes a subscription.
	//
	// # Possible errors
	//
	//  - NotFound: If not found.
	Delete(ctx context.Context, id string) error
}
