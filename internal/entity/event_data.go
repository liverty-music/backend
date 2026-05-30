package entity

// Event subject constants for domain events published via messaging.
//
// Subjects follow the UPPERCASE two-segment convention enforced by the
// pre-existing JetStream stream config (CONCERT.*, ARTIST.*, USER.*,
// VENUE.*, POISON.*). The analytics-consumer maps each subject to a
// lowercase catalogue event name (see specification/docs/analytics/
// event-catalog.md) at the Handle method that subscribes to it.
const (
	SubjectConcertDiscovered            = "CONCERT.discovered"
	SubjectConcertCreated               = "CONCERT.created"
	SubjectArtistCreated                = "ARTIST.created"
	SubjectArtistFollowed               = "ARTIST.followed"
	SubjectArtistUnfollowed             = "ARTIST.unfollowed"
	SubjectUserCreated                  = "USER.created"
	SubjectUserPreferredLanguageUpdated = "USER.preferred_language_updated"
	SubjectPushSubscriptionCompleted    = "PUSH.subscription_completed"
)

// ConcertDiscoveredData is the payload for concert.discovered.v1 events.
// It carries the full batch of scraped concerts for one artist (post-deduplication).
// Published by SearchNewConcerts after external API call and dedup.
type ConcertDiscoveredData struct {
	// ArtistID is the internal UUID of the artist.
	ArtistID string `json:"artist_id"`
	// ArtistName is the display name of the artist (for notification context).
	ArtistName string `json:"artist_name"`
	// Concerts is the list of newly discovered, deduplicated scraped concerts.
	Concerts ScrapedConcerts `json:"concerts"`
}

// UserCreatedData is the payload for user.created events.
// Published by UserUseCase.Create after persisting a new user.
type UserCreatedData struct {
	// UserID is the platform-internal user identifier (UUID). Used as
	// the PostHog `distinct_id` by the analytics-consumer per the
	// introduce-analytics-tool OpenSpec change (Decision 3).
	UserID string `json:"user_id"`
	// ExternalID is the Zitadel user ID (JWT sub claim). Used by the
	// email-verification consumer to address Zitadel APIs.
	ExternalID string `json:"external_id"`
	// Email is the user's email address.
	Email string `json:"email"`
}

// ArtistCreatedData is the payload for artist.created events.
// Published by persistArtists when new artists are inserted into the database.
type ArtistCreatedData struct {
	// ArtistID is the internal UUID of the artist.
	ArtistID string `json:"artist_id"`
	// ArtistName is the display name of the artist.
	ArtistName string `json:"artist_name"`
	// MBID is the MusicBrainz identifier for canonical identity.
	MBID string `json:"mbid"`
}

// UserPreferredLanguageUpdatedData is the payload for USER.preferred_language_updated.
// Mapped to the catalogue event account.preferred_language.updated by the
// analytics-consumer. Published by UserUseCase.UpdatePreferredLanguage after
// the repository confirms the change.
type UserPreferredLanguageUpdatedData struct {
	// UserID is the platform-internal user identifier (UUID). Used as the
	// PostHog distinct_id.
	UserID string `json:"user_id"`
	// FromLocale is the ISO 639-1 language code before the change. Empty
	// when the user had no preferred language set previously (legacy rows
	// pending backfill — see entity.User.PreferredLanguage docstring).
	FromLocale string `json:"from_locale"`
	// ToLocale is the ISO 639-1 language code after the change.
	ToLocale string `json:"to_locale"`
}

// ArtistFollowedData is the payload for ARTIST.followed.
// Mapped to the catalogue event artist.follow.completed by the
// analytics-consumer. Published by FollowUseCase.Follow after the
// repository persists the relationship.
type ArtistFollowedData struct {
	// UserID is the platform-internal user identifier of the follower.
	// Used as the PostHog distinct_id.
	UserID string `json:"user_id"`
	// ArtistID is the internal UUID of the followed artist.
	ArtistID string `json:"artist_id"`
}

// ArtistUnfollowedData is the payload for ARTIST.unfollowed.
// Mapped to the catalogue event artist.unfollow.completed by the
// analytics-consumer. Published by FollowUseCase.Unfollow after the
// repository removes the relationship.
type ArtistUnfollowedData struct {
	// UserID is the platform-internal user identifier of the user who
	// stopped following. Used as the PostHog distinct_id.
	UserID string `json:"user_id"`
	// ArtistID is the internal UUID of the unfollowed artist.
	ArtistID string `json:"artist_id"`
}

// PushSubscriptionCompletedData is the payload for PUSH.subscription_completed.
// Mapped to the catalogue event push.subscription.completed by the
// analytics-consumer. Published by PushNotificationUseCase.Create after the
// repository persists the subscription record.
type PushSubscriptionCompletedData struct {
	// UserID is the platform-internal user identifier of the subscriber.
	// Used as the PostHog distinct_id.
	UserID string `json:"user_id"`
	// DeviceType is the browser/OS family derived from the push endpoint
	// host. Values: "android" (FCM), "apple" (Web Push for Safari), "firefox"
	// (Mozilla autopush), "windows" (WNS), "other". The endpoint URL itself
	// is sensitive and is NOT included in the payload — the host classifier
	// is the only signal forwarded to PostHog.
	DeviceType string `json:"device_type"`
}
