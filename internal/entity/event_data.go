package entity

// Event subject constants for domain events published via messaging.
//
// Subjects follow the UPPERCASE two-segment convention enforced by the
// pre-existing JetStream stream config (CONCERT.*, ARTIST.*, USER.*,
// VENUE.*, POISON.*). The analytics-consumer maps each subject to a
// lowercase catalogue event name (see specification/docs/analytics/
// event-catalog.md) at the Handle method that subscribes to it.
const (
	SubjectConcertDiscovered        = "CONCERT.discovered"
	SubjectConcertCreated           = "CONCERT.created"
	SubjectArtistCreated            = "ARTIST.created"
	SubjectArtistFollowed           = "ARTIST.followed"
	SubjectArtistUnfollowed         = "ARTIST.unfollowed"
	SubjectUserCreated              = "USER.created"
	SubjectNotificationSubscribed   = "NOTIFICATION.subscribed"
	SubjectNotificationUnsubscribed = "NOTIFICATION.unsubscribed"
	// SubjectNotificationDelivered is published by NotificationUseCase once per
	// notification whose channel send reached the delivered state. It drives the
	// notification.delivered analytics event so push-delivery reach can be
	// tracked in PostHog, keyed by notification_id. Matches the existing
	// NOTIFICATION.* JetStream stream — no new stream required.
	SubjectNotificationDelivered = "NOTIFICATION.delivered"
	// SubjectSalesPhaseDiscovered is published when a brand-new sales phase row
	// is inserted. Re-discovery of an existing phase (UpsertOutcomeUpdated)
	// must NOT publish this event.
	SubjectSalesPhaseDiscovered = "SALES_PHASE.discovered"
	// SubjectSalesPhaseReminderDue is published by the reminder scan for each
	// (user, phase, stage) triple that became due and has not yet been sent.
	// Two tokens (reminder_due, not reminder.due) so the SALES_PHASE stream
	// filter can be a plain `SALES_PHASE.*`.
	SubjectSalesPhaseReminderDue = "SALES_PHASE.reminder_due"
	// SubjectTicketJourneyStatusChanged is published by SetStatus after a
	// successful upsert when the new status differs from the prior one (or
	// when no prior journey existed). It drives the
	// ticket.journey.status.changed analytics event.
	SubjectTicketJourneyStatusChanged = "TICKET_JOURNEY.status_changed"
	// SubjectTicketEmailParsed is published by TicketEmailUseCase.Create on
	// both parse-success and parse-failure paths. It drives the
	// ticket.email.parsed analytics event (email-ingestion data quality,
	// parser robustness).
	SubjectTicketEmailParsed = "TICKET_EMAIL.parsed"
	// SubjectUserLoggedIn is published by the login-event webhook handler once
	// per user-initiated login, bound to the Zitadel session.user.checked
	// event. It drives the account.signin analytics event (returning /
	// active-user retention cohorts). The OIDC refresh_token grant touches only
	// the oidc_session aggregate and never emits session.user.checked, so this
	// subject is never published on a silent token refresh — login-specific by
	// construction. Modelled as a USER-domain event (USER.* stream), not a
	// separate ACCOUNT stream.
	SubjectUserLoggedIn = "USER.logged_in"
	// SubjectOrganizerCreated is published by OrganizerUseCase.Create after the
	// Zitadel tenant is provisioned and the organizer row is flipped to active.
	// It drives the organizer.created analytics event (admin-actor / group event
	// keyed on organizer_id; no fan distinct_id). Two tokens (created, not
	// org.created) so the ORGANIZER.* stream filter matches with a plain
	// single-token wildcard — same rationale as SubjectSalesPhaseReminderDue.
	SubjectOrganizerCreated = "ORGANIZER.created"
	// SubjectOrganizerArtistAssociated is published by OrganizerUseCase.AssociateArtist
	// after the repository persists the artist link. It drives the
	// organizer.artist.associated analytics event (admin-actor / group event
	// keyed on organizer_id; no fan distinct_id). The event token uses an
	// underscore (artist_associated, not artist.associated) so the subject stays
	// two tokens and the plain ORGANIZER.* filter on the JetStream stream
	// captures it without a deeper wildcard.
	SubjectOrganizerArtistAssociated = "ORGANIZER.artist_associated"
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
	SubjectNotificationSubscribed,
	SubjectNotificationUnsubscribed,
	SubjectNotificationDelivered,
	SubjectSalesPhaseDiscovered,
	SubjectSalesPhaseReminderDue,
	SubjectTicketJourneyStatusChanged,
	SubjectTicketEmailParsed,
	SubjectUserLoggedIn,
	SubjectOrganizerCreated,
	SubjectOrganizerArtistAssociated,
}

// ConcertDiscoveredData is the payload for concert.discovered.v1 events.
// It carries the newly discovered concerts for one artist (post-deduplication),
// grouped by series so a tour's dates persist under one series by construction.
// Published by SearchNewConcerts after the external API call and dedup.
type ConcertDiscoveredData struct {
	// ArtistID is the internal UUID of the artist.
	ArtistID string `json:"artist_id"`
	// ArtistName is the display name of the artist (for notification context).
	ArtistName string `json:"artist_name"`
	// Series is the list of newly discovered, deduplicated series, each carrying
	// its member events. A <tour> block is one TOUR series with many events; a
	// standalone show is one SINGLE series with a single event.
	Series []*DiscoveredSeries `json:"series"`
}

// EventCount returns the total number of member events across all series in the
// payload. Used for logging/metrics where the flat event count is meaningful.
func (d ConcertDiscoveredData) EventCount() int {
	n := 0
	for _, s := range d.Series {
		if s == nil {
			continue
		}
		n += len(s.Events)
	}
	return n
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

// UserLoggedInData is the payload for USER.logged_in events.
// Mapped to the catalogue event account.signin by the analytics-consumer
// (the NATS transport subject is USER.logged_in). Published by the
// login-event webhook handler once per user-initiated sign-in, after the
// Zitadel sub has been resolved to the platform UserID.
type UserLoggedInData struct {
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

// OrganizerCreatedData is the payload for ORGANIZER.created.
// Mapped to the catalogue event organizer.created by the analytics-consumer.
// Published by OrganizerUseCase.completeProvisioning after the organizer row is
// flipped to active. Keyed by organizer_id in PostHog (admin-actor group event;
// no fan distinct_id is present).
type OrganizerCreatedData struct {
	// OrganizerID is the platform-internal UUID of the newly provisioned organizer.
	OrganizerID string `json:"organizer_id"`
}

// OrganizerArtistAssociatedData is the payload for ORGANIZER.artist_associated.
// Mapped to the catalogue event organizer.artist.associated by the
// analytics-consumer. Published by OrganizerUseCase.AssociateArtist after the
// repository persists the artist link. Keyed by organizer_id in PostHog
// (admin-actor group event; no fan distinct_id is present).
type OrganizerArtistAssociatedData struct {
	// OrganizerID is the platform-internal UUID of the organizer.
	OrganizerID string `json:"organizer_id"`
	// ArtistID is the internal UUID of the associated artist.
	ArtistID string `json:"artist_id"`
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
