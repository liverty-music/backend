package rdb

import (
	"time"

	"github.com/pannpers/go-backend-scaffold/internal/entity"
	"github.com/uptrace/bun"
)

// User represents the database model for the users table.
type User struct {
	bun.BaseModel `bun:"table:users,alias:u"`

	ID        string    `bun:",pk,type:uuid,default:uuid_generate_v4()"`
	Name      string    `bun:",notnull,type:varchar(255)"`
	Email     string    `bun:",notnull,unique,type:varchar(255)"`
	CreatedAt time.Time `bun:",nullzero,notnull,default:current_timestamp"`
	UpdatedAt time.Time `bun:",nullzero,notnull,default:current_timestamp"`
}

// ToEntity converts database model to domain entity.
func (u *User) ToEntity() *entity.User {
	return &entity.User{
		ID:        u.ID,
		Name:      u.Name,
		Email:     u.Email,
		CreatedAt: u.CreatedAt,
		UpdatedAt: u.UpdatedAt,
	}
}

// FromEntity converts domain entity to database model.
func (u *User) FromEntity(user *entity.User) {
	u.ID = user.ID
	u.Name = user.Name
	u.Email = user.Email
	u.CreatedAt = user.CreatedAt
	u.UpdatedAt = user.UpdatedAt
}

// FromNewUser converts NewUser domain object to database model for creation.
func FromNewUser(newUser *entity.NewUser) *User {
	u := &User{}
	u.Name = newUser.Name
	u.Email = newUser.Email
	return u
}

// Post represents the database model for the posts table.
type Post struct {
	bun.BaseModel `bun:"table:posts,alias:p"`

	ID        string    `bun:",pk,type:uuid,default:uuid_generate_v4()"`
	Title     string    `bun:",notnull,type:varchar(500)"`
	UserID    string    `bun:",notnull,type:uuid"`
	CreatedAt time.Time `bun:",nullzero,notnull,default:current_timestamp"`
	UpdatedAt time.Time `bun:",nullzero,notnull,default:current_timestamp"`

	// Relations
	User *User `bun:"rel:belongs-to,join:user_id=id,on_delete:CASCADE"`
}

// ToEntity converts database model to domain entity.
func (p *Post) ToEntity() *entity.Post {
	return &entity.Post{
		ID:        p.ID,
		Title:     p.Title,
		UserID:    p.UserID,
		CreatedAt: p.CreatedAt,
		UpdatedAt: p.UpdatedAt,
	}
}

// FromEntity converts domain entity to database model.
func (p *Post) FromEntity(post *entity.Post) {
	p.ID = post.ID
	p.Title = post.Title
	p.UserID = post.UserID
	p.CreatedAt = post.CreatedAt
	p.UpdatedAt = post.UpdatedAt
}

// FromNewPost converts NewPost domain object to database model for creation.
func FromNewPost(newPost *entity.NewPost) *Post {
	p := &Post{}
	p.Title = newPost.Title
	p.UserID = newPost.UserID
	return p
}
