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
	SubjectNotificationSubscribed       = "NOTIFICATION.subscribed"
	SubjectNotificationUnsubscribed     = "NOTIFICATION.unsubscribed"
	// SubjectNotificationDelivered is published by NotificationUseCase once per
	// notification whose channel send reached the delivered state. It drives the
	// notification.delivered analytics event so push-delivery reach can be
	// tracked in PostHog, keyed by notification_id. Matches the existing
	// NOTIFICATION.* JetStream stream — no new stream required.
	SubjectNotificationDelivered = "NOTIFICATION.delivered"
	SubjectEntryZkProofVerified  = "ENTRY.zk_proof_verified"
	SubjectEntryZkProofRejected  = "ENTRY.zk_proof_rejected"
	// SubjectSalesPhaseDiscovered is published when a brand-new sales phase row
	// is inserted. Re-discovery of an existing phase (UpsertOutcomeUpdated)
	// must NOT publish this event.
	SubjectSalesPhaseDiscovered = "SALES_PHASE.discovered"
	// SubjectSalesPhaseReminderDue is published by the reminder scan for each
	// (user, phase, stage) triple that became due and has not yet been sent.
	SubjectSalesPhaseReminderDue = "SALES_PHASE.reminder.due"
	// SubjectTicketJourneyStatusChanged is published by SetStatus after a
	// successful upsert when the new status differs from the prior one (or
	// when no prior journey existed). It drives the
	// ticket.journey.status.changed analytics event.
	SubjectTicketJourneyStatusChanged = "TICKET_JOURNEY.status_changed"
	// SubjectTicketMintCompleted is published by TicketUseCase.MintTicket after
	// a ticket is successfully minted and persisted. It drives the
	// ticket.mint.completed analytics event (SBT issuance / ticket-activation
	// funnel).
	SubjectTicketMintCompleted = "TICKET.mint_completed"
	// SubjectSalesReminderDelivered is published by
	// salesReminderDeliveryUseCase.DeliverReminder at every terminal delivery
	// outcome (delivered, no_subscription, failed). It drives the
	// sales_reminder.delivered analytics event so push-delivery reach and
	// failure rates can be tracked per stage in PostHog.
	SubjectSalesReminderDelivered = "SALES_REMINDER.delivered"
	// SubjectTicketEmailParsed is published by TicketEmailUseCase.Create on
	// both parse-success and parse-failure paths. It drives the
	// ticket.email.parsed analytics event (email-ingestion data quality,
	// parser robustness).
	SubjectTicketEmailParsed = "TICKET_EMAIL.parsed"
	// SubjectAccountLogin is published by the CreateSession webhook handler
	// once per user-initiated login (a Zitadel session creation). It drives
	// the account.login analytics event (returning / active-user retention
	// cohorts). The OIDC refresh_token grant reuses the existing session and
	// never calls CreateSession, so this subject is never published on a
	// silent token refresh — login-specific by construction. Uses a new
	// ACCOUNT.* JetStream stream.
	SubjectAccountLogin = "ACCOUNT.login"
)

// AllSubjects is the canonical catalogue of every domain-event NATS subject
// the system publishes and consumes. It is the single source of truth for
// coverage checks: a subject added above MUST be added here too, and the
// stream-coverage test then guarantees each one is captured by a configured
// JetStream stream (see messaging.SubjectCoveredByStream). This closes the
// recurring "added a publisher/subscription without its paired stream" gap
// that fails a consumer at startup with "no stream matches subject".
var AllSubjects = []string{
	SubjectConcertDiscovered,
	SubjectConcertCreated,
	SubjectArtistCreated,
	SubjectArtistFollowed,
	SubjectArtistUnfollowed,
	SubjectUserCreated,
	SubjectUserPreferredLanguageUpdated,
	SubjectNotificationSubscribed,
	SubjectNotificationUnsubscribed,
	SubjectNotificationDelivered,
	SubjectEntryZkProofVerified,
	SubjectEntryZkProofRejected,
	SubjectSalesPhaseDiscovered,
	SubjectSalesPhaseReminderDue,
	SubjectTicketJourneyStatusChanged,
	SubjectTicketMintCompleted,
	SubjectSalesReminderDelivered,
	SubjectTicketEmailParsed,
	SubjectAccountLogin,
}

// EntryRejectionReason enumerates the legitimate causes for a
// zk-proof entry rejection. Carried on the entry.zk_proof.rejected
// analytics event so operations dashboards can break down
// check-in-failure rate by cause. Parse-error and event-id-mismatch
// paths return errors instead of rejections and intentionally do NOT
// fire the analytics event — those are attacks or upstream bugs, not
// legitimate user attempts.
type EntryRejectionReason string

// Legitimate entry.zk_proof.rejected reasons.
const (
	EntryRejectionMerkleRootMismatch EntryRejectionReason = "merkle_root_mismatch"
	EntryRejectionAlreadyCheckedIn   EntryRejectionReason = "already_checked_in"
	EntryRejectionProofInvalid       EntryRejectionReason = "proof_invalid"
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

// AccountLoginData is the payload for ACCOUNT.login events.
// Mapped to the catalogue event account.login by the analytics-consumer.
// Published by the CreateSession webhook handler once per user-initiated
// login, after the Zitadel sub has been resolved to the platform UserID.
type AccountLoginData struct {
	// UserID is the platform-internal user identifier (UUID). Used as the
	// PostHog distinct_id. Never the Zitadel sub, which Enqueue rejects.
	UserID string `json:"user_id"`
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

// EntryZkProofVerifiedData is the payload for ENTRY.zk_proof_verified.
// Mapped to the catalogue event entry.zk_proof.verified by the
// analytics-consumer. Published by EntryUseCase.VerifyEntry after a
// successful proof verification and nullifier insertion.
//
// NullifierHashHex (hex of the on-wire nullifier) serves as the PostHog
// distinct_id: it is stable per (ticket, event) pair, intentionally
// non-reversible to a user identity by ZK guarantee, and already on the
// public-signals wire so forwarding it leaks no new information.
type EntryZkProofVerifiedData struct {
	// NullifierHashHex is the hex-encoded nullifier hash. Used as
	// PostHog distinct_id.
	NullifierHashHex string `json:"nullifier_hash_hex"`
	// EventID is the internal UUID of the live event.
	EventID string `json:"event_id"`
}

// EntryZkProofRejectedData is the payload for ENTRY.zk_proof_rejected.
// Mapped to the catalogue event entry.zk_proof.rejected. Reason takes
// one of the EntryRejection* constants.
type EntryZkProofRejectedData struct {
	NullifierHashHex string               `json:"nullifier_hash_hex"`
	EventID          string               `json:"event_id"`
	Reason           EntryRejectionReason `json:"reason"`
}

// NotificationSubscribedData is the payload for NOTIFICATION.subscribed.
// Mapped to the catalogue event notification.subscribed by the
// analytics-consumer. Published by PushNotificationUseCase.Create after the
// repository persists the Web Push subscription record. Although the
// underlying transport is the W3C Push API, the analytics surface stays
// scoped under the notification domain — see specification/docs/analytics/
// event-catalog.md.
type NotificationSubscribedData struct {
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

// NotificationUnsubscribedData is the payload for NOTIFICATION.unsubscribed.
// Mapped to the catalogue event notification.unsubscribed by the
// analytics-consumer. Published by PushNotificationUseCase.Delete after the
// repository removes a user-initiated Web Push subscription record. The
// endpoint URL itself is sensitive and is NOT included; only the classifier
// output is forwarded to PostHog.
type NotificationUnsubscribedData struct {
	// UserID is the platform-internal user identifier of the subscriber.
	// Used as the PostHog distinct_id.
	UserID string `json:"user_id"`
	// DeviceType is the browser/OS family derived from the push endpoint
	// host. Values: "android" (FCM), "apple" (Web Push for Safari), "firefox"
	// (Mozilla autopush), "windows" (WNS), "other". See DeviceTypeFromEndpoint.
	DeviceType string `json:"device_type"`
}

// NotificationDeliveredData is the payload for NOTIFICATION.delivered.
// Mapped to the catalogue event notification.delivered by the
// analytics-consumer. Published by NotificationUseCase once per notification
// whose channel send reached the delivered state, so push-delivery reach is
// measurable in PostHog keyed by notification_id. A failed delivery does NOT
// publish this event.
type NotificationDeliveredData struct {
	// UserID is the platform-internal user identifier of the recipient.
	// Used as the PostHog distinct_id.
	UserID string `json:"user_id"`
	// NotificationID is the stored notification record id, the end-to-end
	// correlation key shared with the client open/dismiss events.
	NotificationID string `json:"notification_id"`
	// Type is the string name of the NotificationType (e.g. "new_concerts").
	Type string `json:"type"`
}

// SalesPhaseDiscoveredData is the payload for SALES_PHASE.discovered events.
// Published by the discovery use case only when a brand-new sales_phases row is
// inserted (UpsertOutcomeInserted). Re-discovery of an existing phase
// (UpsertOutcomeUpdated) must NOT publish this event.
type SalesPhaseDiscoveredData struct {
	// PhaseID is the surrogate UUID of the newly inserted SalesPhase row.
	PhaseID string `json:"phase_id"`
	// SeriesID is the parent series of the phase. The announcement consumer
	// resolves its audience from the Tracking ticket journeys on this series'
	// events.
	SeriesID string `json:"series_id"`
}

// SalesPhaseReminderDueData is the payload for SALES_PHASE.reminder.due events.
// Published by the reminder scan for each (user, phase, stage) triple that
// became due in this scan window.
type SalesPhaseReminderDueData struct {
	// UserID is the recipient.
	UserID string `json:"user_id"`
	// PhaseID is the sales phase surrogate id.
	PhaseID string `json:"phase_id"`
	// Stage is the reminder stage (ReminderStage int16 value).
	Stage int16 `json:"stage"`
	// Payload is the pre-built notification payload for this recipient.
	// Built per-recipient so times render in the user's timezone and copy
	// is selected by preferred_language.
	Payload *NotificationPayload `json:"payload"`
}

// TicketMintCompletedData is the payload for TICKET.mint_completed.
// Mapped to the catalogue event ticket.mint.completed by the
// analytics-consumer. Published by TicketUseCase.MintTicket after a ticket is
// successfully minted and persisted. Drives the SBT issuance /
// ticket-activation funnel in PostHog.
type TicketMintCompletedData struct {
	// UserID is the platform-internal user identifier of the ticket recipient.
	// Used as the PostHog distinct_id.
	UserID string `json:"user_id"`
	// EventID is the internal UUID of the live event for which the ticket was minted.
	EventID string `json:"event_id"`
}

// TicketEmailParsedData is the payload for TICKET_EMAIL.parsed.
// Mapped to the catalogue event ticket.email.parsed by the
// analytics-consumer. Published by TicketEmailUseCase.Create on both
// parse-success and parse-failure paths so email-ingestion data quality and
// parser robustness can be measured in PostHog.
type TicketEmailParsedData struct {
	// UserID is the platform-internal user identifier of the fan who imported
	// the email. Used as the PostHog distinct_id.
	UserID string `json:"user_id"`
	// EmailType is the string name of the TicketEmailType enum value (e.g.
	// "LOTTERY_INFO", "LOTTERY_RESULT").
	EmailType string `json:"email_type"`
	// ParseStatus is "success" when the parser returned no error, "failure"
	// otherwise.
	ParseStatus string `json:"parse_status"`
	// FieldCount is the number of non-nil optional fields extracted by the
	// parser on success. Zero on failure.
	FieldCount int `json:"field_count"`
}

// SalesReminderDeliveredData is the payload for SALES_REMINDER.delivered.
// Mapped to the catalogue event sales_reminder.delivered by the
// analytics-consumer. Published by salesReminderDeliveryUseCase.DeliverReminder
// at every terminal delivery outcome so push-delivery reach and failure rates can
// be tracked per stage in PostHog.
type SalesReminderDeliveredData struct {
	// UserID is the platform-internal user identifier of the reminder recipient.
	// Used as the PostHog distinct_id.
	UserID string `json:"user_id"`
	// PhaseStage is the string name of the ReminderStage (e.g. "APPLY_OPEN").
	PhaseStage string `json:"phase_stage"`
	// DeliveryStatus is one of "delivered", "no_subscription", or "failed".
	// "delivered" means the push was accepted by at least one subscription.
	// "no_subscription" means the user had no registered push subscriptions.
	// "failed" means all send attempts were rejected (410 Gone or send error).
	DeliveryStatus string `json:"delivery_status"`
}

// TicketJourneyStatusChangedData is the payload for TICKET_JOURNEY.status_changed.
// Mapped to the catalogue event ticket.journey.status.changed by the
// analytics-consumer. Published by TicketJourneyUseCase.SetStatus after a
// successful upsert when the incoming status differs from the stored one.
// When no prior journey existed, FromStatus is "UNSPECIFIED" (the zero-value
// sentinel of TicketJourneyStatus.String()).
type TicketJourneyStatusChangedData struct {
	// UserID is the platform-internal user identifier of the fan.
	// Used as the PostHog distinct_id.
	UserID string `json:"user_id"`
	// EventID is the internal UUID of the live event being tracked.
	EventID string `json:"event_id"`
	// FromStatus is the TicketJourneyStatus name before the change, or
	// "UNSPECIFIED" when the journey did not exist prior to this call.
	FromStatus string `json:"from_status"`
	// ToStatus is the TicketJourneyStatus name after the successful upsert.
	ToStatus string `json:"to_status"`
}
