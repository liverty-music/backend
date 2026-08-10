package entity

import "context"

// Hype represents the user's enthusiasm tier for a followed artist.
// Values are ordered by ascending enthusiasm: Watch (lowest) to Away (highest).
type Hype string

const (
	// HypeWatch indicates dashboard-only display, no push notifications. Lowest tier.
	HypeWatch Hype = "watch"
	// HypeHome indicates notifications only for concerts in the user's home area.
	HypeHome Hype = "home"
	// HypeNearby indicates notifications for concerts within physical proximity
	// (~200km) of the user's home area.
	HypeNearby Hype = "nearby"
	// HypeAway indicates notifications for all concerts nationwide.
	HypeAway Hype = "away"
)

// DefaultHype is the tier assigned to a fan when they follow an artist without
// specifying enthusiasm. This expresses the product decision that following an
// artist implicitly means "notify me about concerts within reach" — the
// non-dismissable signup banner makes the signup prerequisite explicit.
//
// The followed_artists.hype DB column DEFAULT mirrors this value so that
// INSERT statements omitting the hype column produce consistent state, but
// the constant here is the canonical domain-layer source: Go code creating
// follows (bulk operations, background jobs, test fixtures) SHALL reference
// this constant rather than re-deciding the default.
const DefaultHype Hype = HypeNearby

// IsValid reports whether h is a recognized Hype value.
func (h Hype) IsValid() bool {
	switch h {
	case HypeWatch, HypeHome, HypeNearby, HypeAway:
		return true
	default:
		return false
	}
}

// MatchingConcerts narrows a batch of newly created concerts to the subset that
// satisfies this hype level's predicate for a follower with the given home area.
// The subset drives both the notification body count and the deep-link target,
// so it must reflect exactly what the recipient's hype tier promised them.
//
// Selection rules:
//  1. HypeWatch → empty (dashboard-only, no push).
//  2. HypeHome → concerts whose venue admin area matches the recipient's home
//     area (ProximityHome); empty when home is nil or home.Level1 is empty.
//  3. HypeNearby → concerts within range of the home centroid (ProximityHome or
//     ProximityNearby); empty when home is nil.
//  4. HypeAway → all concerts nationwide.
//  5. Anything else → empty.
//
// The returned slice preserves the input order and shares no backing array with
// concerts, so callers may sort or filter it freely. Returns nil (not a zero
// slice) when nothing matches.
func (h Hype) MatchingConcerts(home *Home, concerts []*Concert) []*Concert {
	switch h {
	case HypeWatch:
		return nil

	case HypeHome:
		if home == nil || home.Level1 == "" {
			return nil
		}
		var matched []*Concert
		for _, c := range concerts {
			if c.ProximityTo(home) == ProximityHome {
				matched = append(matched, c)
			}
		}
		return matched

	case HypeNearby:
		if home == nil {
			return nil
		}
		var matched []*Concert
		for _, c := range concerts {
			if p := c.ProximityTo(home); p == ProximityHome || p == ProximityNearby {
				matched = append(matched, c)
			}
		}
		return matched

	case HypeAway:
		if len(concerts) == 0 {
			return nil
		}
		matched := make([]*Concert, len(concerts))
		copy(matched, concerts)
		return matched

	default:
		return nil
	}
}

// ShouldNotify reports whether a follower with hype level h should receive a
// push notification for the given newly created concerts, given the follower's
// home area. It is defined as "the hype-matched subset is non-empty" — see
// [Hype.MatchingConcerts] for the per-tier predicate.
func (h Hype) ShouldNotify(home *Home, concerts []*Concert) bool {
	return len(h.MatchingConcerts(home, concerts)) > 0
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
	// their hype level. User entities are partially populated with ID, Home, and
	// PreferredLanguage for notification filtering and copy localization. Returns
	// an empty slice when no users follow the artist.
	//
	// # Possible errors:
	//
	//   - Internal: database query failure.
	ListFollowers(ctx context.Context, artistID string) ([]*Follower, error)
}
