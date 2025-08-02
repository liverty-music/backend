package entity

import (
	"context"
	"time"
)

// Post represents a post domain entity.
type Post struct {
	ID        string
	Title     string
	UserID    string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// NewPost represents data for creating a new post.
type NewPost struct {
	Title  string
	UserID string
}

// PostRepository defines the interface for post data access.
type PostRepository interface {
	Create(ctx context.Context, params *NewPost) (*Post, error)
	Get(ctx context.Context, id string) (*Post, error)
	Delete(ctx context.Context, id string) error
}