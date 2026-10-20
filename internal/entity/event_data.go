package entity

import "time"

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
	// SubjectTicketSaleDiscovered is published when a new discovered
	// TicketSale is created. Re-discovery of a known one
	// (UpsertOutcomeUpdated) must NOT publish this event. The subject keeps
	// the stream's former name (design "Non-Goals").
	SubjectTicketSaleDiscovered = "SALES_PHASE.discovered"
	// SubjectTicketSaleReminderDue is published by the reminder scan for each
	// (user, ticket sale, stage) that became due and has not yet been sent.
	// Two tokens (reminder_due, not reminder.due) so the SALES_PHASE stream
	// filter can be a plain `SALES_PHASE.*`.
	SubjectTicketSaleReminderDue = "SALES_PHASE.reminder_due"
	// SubjectTicketJourneyStatusChanged is published by SetStatus after a
	// successful upsert when the new status differs from the prior one (or
	// when no prior journey existed). It drives the
	// ticket.journey.status.changed analytics event.
	SubjectTicketJourneyStatusChanged = "TICKET_JOURNEY.status_changed"
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
	// Organizer's Organization is provisioned and the organizer row is flipped to active.
	// It drives the organizer.created analytics event (admin-actor / group event
	// keyed on organizer_id; no fan distinct_id). Two tokens (created, not
	// org.created) so the ORGANIZER.* stream filter matches with a plain
	// single-token wildcard — same rationale as SubjectTicketSaleReminderDue.
	SubjectOrganizerCreated = "ORGANIZER.created"
	// SubjectOrganizerArtistAssociated is published by OrganizerUseCase.AssociateArtist
	// after the repository persists the artist link. It drives the
	// organizer.artist.associated analytics event (admin-actor / group event
	// keyed on organizer_id; no fan distinct_id). The event token uses an
	// underscore (artist_associated, not artist.associated) so the subject stays
	// two tokens and the plain ORGANIZER.* filter on the JetStream stream
	// captures it without a deeper wildcard.
	SubjectOrganizerArtistAssociated = "ORGANIZER.artist_associated"
	// SubjectConcertCancelled is published by SeriesUseCase.Cancel after
	// a PUBLISHED first-party series is transitioned to CANCELLED. It drives the
	// concert.cancelled analytics event and the downstream consumer that notifies
	// affected fans. Two tokens so the CONCERT.* stream filter captures it without
	// a deeper wildcard — consistent with SubjectConcertCreated.
	SubjectConcertCancelled = "CONCERT.cancelled"
	// SubjectOrganizerConcertPublished is published by SeriesUseCase.Publish
	// after a first-party series is successfully published. It drives the
	// organizer.concert.published analytics event (admin-actor / group event keyed
	// on organizer_id; no fan distinct_id). Published as an ORGANIZER.* subject so
	// the JetStream ORGANIZER stream captures it alongside the other organizer events.
	SubjectOrganizerConcertPublished = "ORGANIZER.concert_published"
	// SubjectSeriesImageUploaded is published by the SetCoverImage handler once the series image
	// row is persisted. The series-image-consumer subscribes to this subject to
	// read the original from the originals bucket, generate WebP variants, and
	// cut over series_cover_images to the new series image id. Two tokens (uploaded, not
	// series_image.uploaded) so the SERIES_IMAGE.* stream filter captures it with a plain
	// single-token wildcard — same convention as SubjectTicketSaleReminderDue.
	SubjectSeriesImageUploaded = "SERIES_IMAGE.uploaded"
	// SubjectNotificationRequested is published once per eligible recipient by
	// every producer of a user-facing notification (PushSubscriptionUseCase.
	// NotifyNewConcerts, TicketSaleAnnouncementUseCase.AnnounceDiscoveredSale).
	// The deliver-notification consumer subscribes to this subject and calls
	// NotificationUseCase.Deliver, which records the durable Notification and
	// dispatches the push. Producers publish with a deterministic id (see
	// EventPublisher.PublishEventWithID) derived from stable business keys so
	// an at-least-once retry of the same triggering event republishes the same
	// recipients as the same NATS Msg-Id, which the NOTIFICATION stream's
	// Duplicates window (2 minutes) deduplicates broker-side. Already covered
	// by the existing NOTIFICATION.* stream — see streams.go.
	SubjectNotificationRequested = "NOTIFICATION.requested"
	// SubjectOrderPaid announces that an Order is paid, for a won lottery
	// entry or a completed checkout. OrderUseCase.FulfillPayment
	// publishes it on the payment provider's completed-charge webhook, with the
	// Order id as the message id. Two consumers run from
	// it: the purchase confirmation (NotificationUseCase.SendOrderConfirmation)
	// and the ticket journey (TicketJourneyUseCase.MarkPaid).
	SubjectOrderPaid = "ORDER.paid"
	// SubjectResaleListingRefunded announces that the user of a resold
	// ResaleListing was refunded from the listing's Order's charge.
	// ResaleListingUseCase.RefundSold publishes it with the listing id as the
	// message id, and again on later runs while the sold email is unsent. The
	// sold email consumer (NotificationUseCase.SendResaleSold) runs from it.
	SubjectResaleListingRefunded = "ORDER.resale_listing_refunded"
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
	SubjectConcertCancelled,
	SubjectArtistCreated,
	SubjectArtistFollowed,
	SubjectArtistUnfollowed,
	SubjectUserCreated,
	SubjectNotificationSubscribed,
	SubjectNotificationUnsubscribed,
	SubjectNotificationDelivered,
	SubjectTicketSaleDiscovered,
	SubjectTicketSaleReminderDue,
	SubjectTicketJourneyStatusChanged,
	SubjectUserLoggedIn,
	SubjectOrganizerCreated,
	SubjectOrganizerArtistAssociated,
	SubjectOrganizerConcertPublished,
	SubjectSeriesImageUploaded,
	SubjectNotificationRequested,
	SubjectOrderPaid,
	SubjectResaleListingRefunded,
}

// OrderPaidData is the payload for ORDER.paid events. It carries ids and
// amounts only, never personal data; consumers read the rest by id. Exactly
// one of LotteryEntryID and CheckoutID is set: the Order's source.
type OrderPaidData struct {
	// OrderID is the paid Order; also the event's message id, so a
	// republished announcement is deduplicated and consumers are idempotent by
	// it.
	OrderID string `json:"order_id"`
	// UserID is the platform-internal id of the Order's user.
	UserID string `json:"user_id"`
	// EventID is the event the Order's tickets admit to.
	EventID string `json:"event_id"`
	// TicketCount is the number of Tickets the Order issued.
	TicketCount int `json:"ticket_count"`
	// Amount is the Order's amount in the currency's smallest unit (yen).
	Amount int64 `json:"amount"`
	// Currency is the ISO 4217 code of Amount.
	Currency string `json:"currency"`
	// LotteryEntryID is the won lottery entry, when that is the source.
	LotteryEntryID string `json:"lottery_entry_id,omitempty"`
	// CheckoutID is the completed checkout, when that is the source.
	CheckoutID string `json:"checkout_id,omitempty"`
}

// ResaleListingRefundedData is the payload for ORDER.resale_listing_refunded
// events. It carries ids only, never personal data; consumers read the rest
// by id.
type ResaleListingRefundedData struct {
	// ResaleListingID is the refunded listing; also the event's message id,
	// so a republished announcement is deduplicated and consumers are
	// idempotent by it.
	ResaleListingID string `json:"resale_listing_id"`
	// UserID is the platform-internal id of the listing's user.
	UserID string `json:"user_id"`
	// OrderID is the Order whose charge the user was refunded from.
	OrderID string `json:"order_id"`
	// EventID is the event the listed Ticket admitted to.
	EventID string `json:"event_id"`
}

// ConcertCreatedData is the payload for CONCERT.created events.
// Published once per discovered or organizer-authored series, carrying every
// newly inserted event id so a multi-date tour wakes the notification consumer
// once rather than per event.
type ConcertCreatedData struct {
	// ArtistID is the internal UUID of the primary artist (empty for multi-artist
	// organizer series where the performer list is on the events directly).
	ArtistID string `json:"artist_id"`
	// ConcertIDs are the event IDs of the newly inserted events.
	ConcertIDs []string `json:"concert_ids"`
}

// ConcertCancelledData is the payload for CONCERT.cancelled events.
// Published by SeriesUseCase.Cancel when a PUBLISHED first-party
// series is transitioned to CANCELLED. The consumer uses the SeriesID to
// notify affected fans.
type ConcertCancelledData struct {
	// SeriesID is the internal UUID of the cancelled series.
	SeriesID string `json:"series_id"`
	// ConcertIDs are the event IDs that belong to the cancelled series. These
	// were previously published events; their rows remain in the events table
	// (to keep natural-key slots claimed) but they are excluded from all
	// fan-facing surfaces by the shared visibility guard.
	ConcertIDs []string `json:"concert_ids"`
}

// OrganizerConcertPublishedData is the payload for ORGANIZER.concert_published events.
// Published by SeriesUseCase.Publish after a first-party series is
// successfully published. Drives the organizer.concert.published analytics event.
type OrganizerConcertPublishedData struct {
	// OrganizerID is the platform-internal UUID of the organizer.
	OrganizerID string `json:"organizer_id"`
	// SeriesID is the internal UUID of the published series.
	SeriesID string `json:"series_id"`
	// NewEventCount is the number of events materialized on this publish.
	NewEventCount int `json:"new_event_count"`
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
	return SumSeriesEvents(d.Series)
}

// SumSeriesEvents returns the total number of member events across the given
// discovered series, skipping nil entries. It is the single fold used wherever a
// flat event count over a []*DiscoveredSeries is needed (payload EventCount and
// the discovery use case's filter bookkeeping) so the two cannot drift.
func SumSeriesEvents(series []*DiscoveredSeries) int {
	n := 0
	for _, s := range series {
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
	// ProviderUserID is the Zitadel user ID (JWT sub claim). Used by the
	// email-verification consumer to address Zitadel APIs.
	ProviderUserID string `json:"provider_user_id"`
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
// analytics-consumer. Published by PushSubscriptionUseCase.Create after the
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
// analytics-consumer. Published by PushSubscriptionUseCase.Delete after the
// repository removes a user-initiated Web Push subscription record. The
// endpoint URL itself is sensitive and is NOT included; only the classifier
// output is forwarded to PostHog.
type NotificationUnsubscribedData struct {
	// UserID is the platform-internal user identifier of the subscriber.
	// Used as the PostHog distinct_id.
	UserID string `json:"user_id"`
	// DeviceType is the browser/OS family derived from the push endpoint
	// host. Values: "android" (FCM), "apple" (Web Push for Safari), "firefox"
	// (Mozilla autopush), "windows" (WNS), "other". See
	// mapper.DeviceTypeFromEndpoint (internal/adapter/rpc/mapper), which
	// classifies the endpoint at subscription-registration time.
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

// NotificationRequestedData is the payload for NOTIFICATION.requested.
// Published once per eligible recipient by every notification producer
// (PushSubscriptionUseCase.NotifyNewConcerts, TicketSaleAnnouncementUseCase.
// AnnounceDiscoveredSale); the deliver-notification consumer decodes it and
// calls NotificationUseCase.Deliver(UserID, Type, Payload) — the producer
// never depends on NotificationUseCase directly.
type NotificationRequestedData struct {
	// UserID is the recipient's platform-internal user identifier.
	UserID string `json:"user_id"`
	// Type is the notification kind to record and dispatch.
	Type NotificationType `json:"type"`
	// Payload is the rendered notification content for the push channel.
	Payload *NotificationPayload `json:"payload"`
}

// TicketSaleDiscoveredData is the payload for SALES_PHASE.discovered events.
// Published by the discovery use case only when a new discovered TicketSale is
// created (UpsertOutcomeInserted); an update of a known one must NOT publish
// it.
//
// The JSON names predate the move of discovered sales onto TicketSale and are
// kept with the subject names (design "Non-Goals"). Method carries the
// TicketSaleMethod value (1=FirstCome, 2=Lottery), whose numbers differ from
// the former SalesMethod; no message could be in flight across that change,
// because production stored no sales phase when it shipped.
type TicketSaleDiscoveredData struct {
	// TicketSaleID is the id of the newly created discovered TicketSale.
	TicketSaleID string `json:"phase_id"`
	// SeriesID is the sale's series. The announcement resolves its audience
	// from the Tracking ticket journeys on this series' events.
	SeriesID string `json:"series_id"`
	// Method is the sale's TicketSaleMethod value; the announcement copy
	// depends on it.
	Method int16 `json:"method"`
	// SaleStartTime is when the sale opens; the announcement names it.
	SaleStartTime time.Time `json:"apply_start_time"`
}

// TicketSaleReminderDueData is the payload for SALES_PHASE.reminder_due
// events. Published by the reminder scan for each (user, ticket sale, stage)
// that became due in this scan window. The JSON names are kept with the
// subject names (see [TicketSaleDiscoveredData]).
type TicketSaleReminderDueData struct {
	// UserID is the recipient.
	UserID string `json:"user_id"`
	// TicketSaleID is the id of the TicketSale, discovered or an Organizer's.
	TicketSaleID string `json:"phase_id"`
	// Stage is the reminder stage (ReminderStage int16 value).
	Stage int16 `json:"stage"`
	// Payload is the pre-built notification payload for this recipient.
	// Built per-recipient so times render in the user's timezone and copy
	// is selected by preferred_language.
	Payload *NotificationPayload `json:"payload"`
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

// SeriesImageUploadedData is the payload for SERIES_IMAGE.uploaded events.
// Published by the SetCoverImage handler once the series image row is persisted in the
// database. The series-image-consumer uses SeriesImageID and SeriesID to locate
// the original file in the originals bucket, generate WebP variants (thumb and
// large), and cut over series_cover_images to the new series image id.
type SeriesImageUploadedData struct {
	// SeriesImageID is the unique identifier of the uploaded image object. The
	// processor uses it to compose the originals-bucket key
	// ({org}/{seriesImageID}) and the served-bucket variant keys
	// (cdn/{org}/{seriesImageID}/{variant}.webp).
	SeriesImageID string `json:"series_image_id"`
	// SeriesID is the target series. The processor resolves the organizer id
	// from the series image row (stored on insert by SetCoverImage) so it can compose
	// all bucket paths without an extra DB lookup.
	SeriesID string `json:"series_id"`
}
