package entity

import "context"

// Hype represents the user's enthusiasm tier for a followed artist.
// Values are ordered by ascending enthusiasm: Watch (lowest) to Away (highest).
type Hype string

const (
	// HypeWatch indicates dashboard-only display, no push notifications (default).
	HypeWatch Hype = "watch"
	// HypeHome indicates notifications only for concerts in the user's home area.
	HypeHome Hype = "home"
	// HypeNearby is reserved for Phase 2 (physical distance based proximity).
	HypeNearby Hype = "nearby"
	// HypeAway indicates notifications for all concerts nationwide.
	HypeAway Hype = "away"
)

// IsValid reports whether h is a recognized Hype value.
func (h Hype) IsValid() bool {
	switch h {
	case HypeWatch, HypeHome, HypeNearby, HypeAway:
		return true
	default:
		return false
	}
}

// ShouldNotify reports whether a follower with hype level h should receive a
// push notification for a newly discovered concert, given the follower's home
// area, the venue's admin areas, and the list of concerts.
//
// Decision rules (evaluated in order):
//  1. HypeWatch → false (dashboard-only, no push).
//  2. HypeHome → true only when home is non-nil, home.Level1 is non-empty, and
//     home.Level1 exists in venueAreas.
//  3. HypeNearby → true only when home is non-nil and at least one concert in
//     concerts has proximity ProximityHome or ProximityNearby relative to home.
//  4. HypeAway → true (all concerts nationwide).
//  5. Anything else → false.
func (h Hype) ShouldNotify(home *Home, venueAreas map[string]struct{}, concerts []*Concert) bool {
	switch h {
	case HypeWatch:
		return false

	case HypeHome:
		if home == nil || home.Level1 == "" {
			return false
		}
		_, ok := venueAreas[home.Level1]
		return ok

	case HypeNearby:
		if home == nil {
			return false
		}
		for _, c := range concerts {
			p := c.ProximityTo(home)
			if p == ProximityHome || p == ProximityNearby {
				return true
			}
		}
		return false

	case HypeAway:
		return true

	default:
		return false
	}
}

// Follow represents the write model for a user-artist follow relationship.
type Follow struct {
	// UserID is the internal UUID of the follower.
	UserID string
	// ArtistID is the internal UUID of the followed artist.
	ArtistID string
	// Hype is the user's enthusiasm tier for this artist.
	Hype Hype
}

// FollowedArtist is the user-perspective read model for a followed artist.
// Used by ListByUser to return the user's followed artists with hype metadata.
type FollowedArtist struct {
	// UserID is the internal UUID of the follower.
	UserID string
	// Artist is the followed artist entity.
	Artist *Artist
	// Hype is the user's enthusiasm tier for this artist.
	Hype Hype
}

// Follower is the artist-perspective read model for a user following an artist.
// Used by ListFollowers to return followers with hype for notification filtering.
type Follower struct {
	// ArtistID is the internal UUID of the followed artist.
	ArtistID string
	// User is the follower's user entity (may be partially populated).
	User *User
	// Hype is the follower's enthusiasm tier for the artist.
	Hype Hype
}

// FollowRepository defines the persistence layer operations for follow relationships.
type FollowRepository interface {
	// Follow records a user's interest in an artist for notification purposes.
	//
	// # Possible errors:
	//
	//   - AlreadyExists: the user is already following this artist.
	//   - Internal: database execution failure.
	Follow(ctx context.Context, userID, artistID string) error

	// Unfollow removes the subscription between a user and an artist.
	//
	// # Possible errors:
	//
	//   - NotFound: the follow relationship does not exist.
	//   - Internal: database execution failure.
	Unfollow(ctx context.Context, userID, artistID string) error

	// SetHype updates the enthusiasm tier for a followed artist.
	//
	// # Possible errors:
	//
	//   - NotFound: the user is not following the specified artist.
	//   - Internal: database execution failure.
	SetHype(ctx context.Context, userID, artistID string, hype Hype) error

	// ListByUser retrieves all artists followed by a specific user,
	// enriched with per-user hype metadata.
	//
	// # Possible errors:
	//
	//   - Internal: database query failure.
	ListByUser(ctx context.Context, userID string) ([]*FollowedArtist, error)

	// ListAll retrieves all distinct artists followed by any user.
	//
	// # Possible errors:
	//
	//   - Internal: database query failure.
	ListAll(ctx context.Context) ([]*Artist, error)

	// ListFollowers retrieves all users following the given artist along with
	// their hype level. User entities are partially populated with ID and Home
	// for notification filtering. Returns an empty slice when no users follow
	// the artist.
	//
	// # Possible errors:
	//
	//   - Internal: database query failure.
	ListFollowers(ctx context.Context, artistID string) ([]*Follower, error)
}
