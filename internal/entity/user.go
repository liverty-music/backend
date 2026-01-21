package entity

import (
	"context"
	"time"
)

// User represents a user who registers for concert notifications.
type User struct {
	ID                string
	Email             string
	Name              string
	PreferredLanguage string
	Country           string
	TimeZone          string
	IsActive          bool
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// NewUser represents data for creating a new user.
type NewUser struct {
	Email             string
	Name              string
	PreferredLanguage string
	Country           string
	TimeZone          string
}

// UserArtistSubscription represents a user's subscription to an artist.
type UserArtistSubscription struct {
	ID        string
	UserID    string
	ArtistID  string
	IsActive  bool
	CreatedAt time.Time
	UpdatedAt time.Time
}

// NewUserArtistSubscription represents data for creating a new subscription.
type NewUserArtistSubscription struct {
	UserID   string
	ArtistID string
}

// UserRepository defines the interface for user data access.
type UserRepository interface {
	Create(ctx context.Context, params *NewUser) (*User, error)
	Get(ctx context.Context, id string) (*User, error)
	GetByEmail(ctx context.Context, email string) (*User, error)
	Update(ctx context.Context, id string, params *NewUser) (*User, error)
	Delete(ctx context.Context, id string) error
	List(ctx context.Context, limit, offset int) ([]*User, error)
}

// UserArtistSubscriptionRepository defines the interface for user-artist subscription data access.
type UserArtistSubscriptionRepository interface {
	Create(ctx context.Context, params *NewUserArtistSubscription) (*UserArtistSubscription, error)
	Get(ctx context.Context, id string) (*UserArtistSubscription, error)
	GetByUserAndArtist(ctx context.Context, userID, artistID string) (*UserArtistSubscription, error)
	GetByUser(ctx context.Context, userID string, limit, offset int) ([]*UserArtistSubscription, error)
	GetByArtist(ctx context.Context, artistID string, limit, offset int) ([]*UserArtistSubscription, error)
	Activate(ctx context.Context, id string) error
	Deactivate(ctx context.Context, id string) error
	Delete(ctx context.Context, id string) error
}
