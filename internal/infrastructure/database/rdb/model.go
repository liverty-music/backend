package rdb

import (
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/uptrace/bun"
)

// User represents the database model for the users table.
type User struct {
	bun.BaseModel `bun:"table:users,alias:u"`

	ID                string    `bun:",pk,type:uuid,default:uuid_generate_v4()"`
	Email             string    `bun:",notnull,unique,type:varchar(255)"`
	Name              string    `bun:",notnull,type:varchar(255)"`
	PreferredLanguage string    `bun:",type:varchar(10),default:'en'"`
	Country           string    `bun:",type:varchar(3)"`
	TimeZone          string    `bun:",type:varchar(50)"`
	IsActive          bool      `bun:",notnull,default:true"`
	CreatedAt         time.Time `bun:",nullzero,notnull,default:current_timestamp"`
	UpdatedAt         time.Time `bun:",nullzero,notnull,default:current_timestamp"`
}

// ToEntity converts database model to domain entity.
func (u *User) ToEntity() *entity.User {
	return &entity.User{
		ID:                u.ID,
		Email:             u.Email,
		Name:              u.Name,
		PreferredLanguage: u.PreferredLanguage,
		Country:           u.Country,
		TimeZone:          u.TimeZone,
		IsActive:          u.IsActive,
		CreatedAt:         u.CreatedAt,
		UpdatedAt:         u.UpdatedAt,
	}
}

// FromEntity converts domain entity to database model.
func (u *User) FromEntity(user *entity.User) {
	u.ID = user.ID
	u.Email = user.Email
	u.Name = user.Name
	u.PreferredLanguage = user.PreferredLanguage
	u.Country = user.Country
	u.TimeZone = user.TimeZone
	u.IsActive = user.IsActive
	u.CreatedAt = user.CreatedAt
	u.UpdatedAt = user.UpdatedAt
}

// FromNewUser converts NewUser domain object to database model for creation.
func FromNewUser(newUser *entity.NewUser) *User {
	u := &User{}
	u.Email = newUser.Email
	u.Name = newUser.Name
	u.PreferredLanguage = newUser.PreferredLanguage
	u.Country = newUser.Country
	u.TimeZone = newUser.TimeZone
	return u
}

// Artist represents the database model for the artists table.
type Artist struct {
	bun.BaseModel `bun:"table:artists,alias:a"`

	ID            string    `bun:",pk,type:uuid,default:uuid_generate_v4()"`
	Name          string    `bun:",notnull,type:varchar(255)"`
	SpotifyID     string    `bun:",type:varchar(100),unique"`
	MusicBrainzID string    `bun:",type:varchar(100),unique"`
	Genres        []string  `bun:",array,type:text[]"`
	Country       string    `bun:",type:varchar(3)"`
	ImageURL      string    `bun:",type:text"`
	CreatedAt     time.Time `bun:",nullzero,notnull,default:current_timestamp"`
	UpdatedAt     time.Time `bun:",nullzero,notnull,default:current_timestamp"`
}

// ToEntity converts database model to domain entity.
func (a *Artist) ToEntity() *entity.Artist {
	return &entity.Artist{
		ID:            a.ID,
		Name:          a.Name,
		SpotifyID:     a.SpotifyID,
		MusicBrainzID: a.MusicBrainzID,
		Genres:        a.Genres,
		Country:       a.Country,
		ImageURL:      a.ImageURL,
		CreatedAt:     a.CreatedAt,
		UpdatedAt:     a.UpdatedAt,
	}
}

// FromEntity converts domain entity to database model.
func (a *Artist) FromEntity(artist *entity.Artist) {
	a.ID = artist.ID
	a.Name = artist.Name
	a.SpotifyID = artist.SpotifyID
	a.MusicBrainzID = artist.MusicBrainzID
	a.Genres = artist.Genres
	a.Country = artist.Country
	a.ImageURL = artist.ImageURL
	a.CreatedAt = artist.CreatedAt
	a.UpdatedAt = artist.UpdatedAt
}

// FromNewArtist converts NewArtist domain object to database model for creation.
func FromNewArtist(newArtist *entity.NewArtist) *Artist {
	a := &Artist{}
	a.Name = newArtist.Name
	a.SpotifyID = newArtist.SpotifyID
	a.MusicBrainzID = newArtist.MusicBrainzID
	a.Genres = newArtist.Genres
	a.Country = newArtist.Country
	a.ImageURL = newArtist.ImageURL
	return a
}

// Concert represents the database model for the concerts table.
type Concert struct {
	bun.BaseModel `bun:"table:concerts,alias:c"`

	ID           string    `bun:",pk,type:uuid,default:uuid_generate_v4()"`
	Title        string    `bun:",notnull,type:varchar(500)"`
	ArtistID     string    `bun:",notnull,type:uuid"`
	VenueName    string    `bun:",notnull,type:varchar(255)"`
	VenueCity    string    `bun:",notnull,type:varchar(100)"`
	VenueCountry string    `bun:",notnull,type:varchar(3)"`
	VenueAddress string    `bun:",type:text"`
	EventDate    time.Time `bun:",notnull"`
	TicketURL    string    `bun:",type:text"`
	Price        float64   `bun:",type:decimal(10,2)"`
	Currency     string    `bun:",type:varchar(3),default:'USD'"`
	Status       string    `bun:",notnull,type:varchar(20),default:'scheduled'"`
	CreatedAt    time.Time `bun:",nullzero,notnull,default:current_timestamp"`
	UpdatedAt    time.Time `bun:",nullzero,notnull,default:current_timestamp"`

	// Relations
	Artist *Artist `bun:"rel:belongs-to,join:artist_id=id,on_delete:CASCADE"`
}

// ToEntity converts database model to domain entity.
func (c *Concert) ToEntity() *entity.Concert {
	return &entity.Concert{
		ID:           c.ID,
		Title:        c.Title,
		ArtistID:     c.ArtistID,
		VenueName:    c.VenueName,
		VenueCity:    c.VenueCity,
		VenueCountry: c.VenueCountry,
		VenueAddress: c.VenueAddress,
		EventDate:    c.EventDate,
		TicketURL:    c.TicketURL,
		Price:        c.Price,
		Currency:     c.Currency,
		Status:       entity.ConcertStatus(c.Status),
		CreatedAt:    c.CreatedAt,
		UpdatedAt:    c.UpdatedAt,
	}
}

// FromEntity converts domain entity to database model.
func (c *Concert) FromEntity(concert *entity.Concert) {
	c.ID = concert.ID
	c.Title = concert.Title
	c.ArtistID = concert.ArtistID
	c.VenueName = concert.VenueName
	c.VenueCity = concert.VenueCity
	c.VenueCountry = concert.VenueCountry
	c.VenueAddress = concert.VenueAddress
	c.EventDate = concert.EventDate
	c.TicketURL = concert.TicketURL
	c.Price = concert.Price
	c.Currency = concert.Currency
	c.Status = string(concert.Status)
	c.CreatedAt = concert.CreatedAt
	c.UpdatedAt = concert.UpdatedAt
}

// FromNewConcert converts NewConcert domain object to database model for creation.
func FromNewConcert(newConcert *entity.NewConcert) *Concert {
	c := &Concert{}
	c.Title = newConcert.Title
	c.ArtistID = newConcert.ArtistID
	c.VenueName = newConcert.VenueName
	c.VenueCity = newConcert.VenueCity
	c.VenueCountry = newConcert.VenueCountry
	c.VenueAddress = newConcert.VenueAddress
	c.EventDate = newConcert.EventDate
	c.TicketURL = newConcert.TicketURL
	c.Price = newConcert.Price
	c.Currency = newConcert.Currency
	c.Status = string(newConcert.Status)
	return c
}

// Notification represents the database model for the notifications table.
type Notification struct {
	bun.BaseModel `bun:"table:notifications,alias:n"`

	ID          string     `bun:",pk,type:uuid,default:uuid_generate_v4()"`
	UserID      string     `bun:",notnull,type:uuid"`
	ArtistID    string     `bun:",notnull,type:uuid"`
	ConcertID   string     `bun:",type:uuid"`
	Type        string     `bun:",notnull,type:varchar(50)"`
	Title       string     `bun:",notnull,type:varchar(255)"`
	Message     string     `bun:",notnull,type:text"`
	Language    string     `bun:",notnull,type:varchar(10),default:'en'"`
	Status      string     `bun:",notnull,type:varchar(20),default:'pending'"`
	ScheduledAt time.Time  `bun:",notnull"`
	SentAt      *time.Time `bun:","`
	CreatedAt   time.Time  `bun:",nullzero,notnull,default:current_timestamp"`
	UpdatedAt   time.Time  `bun:",nullzero,notnull,default:current_timestamp"`

	// Relations
	User    *User    `bun:"rel:belongs-to,join:user_id=id,on_delete:CASCADE"`
	Artist  *Artist  `bun:"rel:belongs-to,join:artist_id=id,on_delete:CASCADE"`
	Concert *Concert `bun:"rel:belongs-to,join:concert_id=id,on_delete:SET NULL"`
}

// ToEntity converts database model to domain entity.
func (n *Notification) ToEntity() *entity.Notification {
	return &entity.Notification{
		ID:          n.ID,
		UserID:      n.UserID,
		ArtistID:    n.ArtistID,
		ConcertID:   n.ConcertID,
		Type:        entity.NotificationType(n.Type),
		Title:       n.Title,
		Message:     n.Message,
		Language:    n.Language,
		Status:      entity.NotificationStatus(n.Status),
		ScheduledAt: n.ScheduledAt,
		SentAt:      n.SentAt,
		CreatedAt:   n.CreatedAt,
		UpdatedAt:   n.UpdatedAt,
	}
}

// FromEntity converts domain entity to database model.
func (n *Notification) FromEntity(notification *entity.Notification) {
	n.ID = notification.ID
	n.UserID = notification.UserID
	n.ArtistID = notification.ArtistID
	n.ConcertID = notification.ConcertID
	n.Type = string(notification.Type)
	n.Title = notification.Title
	n.Message = notification.Message
	n.Language = notification.Language
	n.Status = string(notification.Status)
	n.ScheduledAt = notification.ScheduledAt
	n.SentAt = notification.SentAt
	n.CreatedAt = notification.CreatedAt
	n.UpdatedAt = notification.UpdatedAt
}

// FromNewNotification converts NewNotification domain object to database model for creation.
func FromNewNotification(newNotification *entity.NewNotification) *Notification {
	n := &Notification{}
	n.UserID = newNotification.UserID
	n.ArtistID = newNotification.ArtistID
	n.ConcertID = newNotification.ConcertID
	n.Type = string(newNotification.Type)
	n.Title = newNotification.Title
	n.Message = newNotification.Message
	n.Language = newNotification.Language
	n.ScheduledAt = newNotification.ScheduledAt
	return n
}

// UserArtistSubscription represents the database model for the user_artist_subscriptions table.
type UserArtistSubscription struct {
	bun.BaseModel `bun:"table:user_artist_subscriptions,alias:uas"`

	ID        string    `bun:",pk,type:uuid,default:uuid_generate_v4()"`
	UserID    string    `bun:",notnull,type:uuid"`
	ArtistID  string    `bun:",notnull,type:uuid"`
	IsActive  bool      `bun:",notnull,default:true"`
	CreatedAt time.Time `bun:",nullzero,notnull,default:current_timestamp"`
	UpdatedAt time.Time `bun:",nullzero,notnull,default:current_timestamp"`

	// Relations
	User   *User   `bun:"rel:belongs-to,join:user_id=id,on_delete:CASCADE"`
	Artist *Artist `bun:"rel:belongs-to,join:artist_id=id,on_delete:CASCADE"`
}

// ToEntity converts database model to domain entity.
func (uas *UserArtistSubscription) ToEntity() *entity.UserArtistSubscription {
	return &entity.UserArtistSubscription{
		ID:        uas.ID,
		UserID:    uas.UserID,
		ArtistID:  uas.ArtistID,
		IsActive:  uas.IsActive,
		CreatedAt: uas.CreatedAt,
		UpdatedAt: uas.UpdatedAt,
	}
}

// FromEntity converts domain entity to database model.
func (uas *UserArtistSubscription) FromEntity(subscription *entity.UserArtistSubscription) {
	uas.ID = subscription.ID
	uas.UserID = subscription.UserID
	uas.ArtistID = subscription.ArtistID
	uas.IsActive = subscription.IsActive
	uas.CreatedAt = subscription.CreatedAt
	uas.UpdatedAt = subscription.UpdatedAt
}

// FromNewUserArtistSubscription converts NewUserArtistSubscription domain object to database model for creation.
func FromNewUserArtistSubscription(newSubscription *entity.NewUserArtistSubscription) *UserArtistSubscription {
	uas := &UserArtistSubscription{}
	uas.UserID = newSubscription.UserID
	uas.ArtistID = newSubscription.ArtistID
	return uas
}
