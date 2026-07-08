package usecase

// AnalyticsEventName identifies a product-analytics event recorded against a
// user's behavioral profile in PostHog.
//
// Every event emitted from the backend MUST use a constant defined here.
// Event names follow the convention domain.action[.outcome] in dot.case;
// see specification/docs/analytics/event-catalog.md for the catalogue and
// per-event property contracts.
//
// Frontend-only events are NOT listed here; they live in
// frontend/src/services/analytics-events.ts. Trust-critical events whose
// accuracy must survive client tampering (ticket purchases, ZK proof
// verification, notification delivery, account state changes) are emitted
// only from the backend through these constants.
type AnalyticsEventName string

// Account lifecycle events emitted from the backend.
const (
	// EventAccountSignin is recorded once per user-initiated sign-in, sourced
	// from the Zitadel session.user.checked Actions v2 event webhook (NATS
	// transport subject ACCOUNT.login). It is never recorded on a silent token
	// refresh (which touches only the oidc_session aggregate), so it is a
	// returning/active-user retention signal. Signup is represented by
	// EventUserCreated, not a separate account.signup.completed event.
	//
	// Named account.signin (renamed from account.login while it had zero
	// production history) for vocabulary consistency with the account.* namespace.
	EventAccountSignin AnalyticsEventName = "account.signin"

	// EventUserCreated is recorded when the backend creates a User record
	// for the first time, either through self-signup or admin provisioning.
	EventUserCreated AnalyticsEventName = "user.created"
)

// Artist engagement events emitted from the backend after persistence.
const (
	// EventArtistFollowCompleted is recorded after a follow relationship
	// has been persisted. The frontend emits a paired artist.follow.requested
	// event at the moment the user submits the action.
	EventArtistFollowCompleted AnalyticsEventName = "artist.follow.completed"

	// EventArtistUnfollowCompleted is recorded after the corresponding
	// follow relationship has been removed.
	EventArtistUnfollowCompleted AnalyticsEventName = "artist.unfollow.completed"
)

// Ticket journey and purchase events emitted from the backend.
const (
	// EventTicketJourneyStatusChanged is recorded after a fan's ticket
	// journey status is successfully updated via SetStatus. It is
	// suppressed when the incoming status equals the stored status
	// (no-op upsert) to avoid noise in downstream funnels.
	// Properties: event_id, from_status, to_status.
	EventTicketJourneyStatusChanged AnalyticsEventName = "ticket.journey.status.changed"

	// EventTicketMintCompleted is recorded after a soulbound ticket is
	// successfully minted on-chain and persisted in the database. Feeds the
	// SBT issuance / ticket-activation funnel in PostHog.
	// Properties: event_id.
	EventTicketMintCompleted AnalyticsEventName = "ticket.mint.completed"

	// EventTicketEmailParsed is recorded by TicketEmailUseCase.Create on
	// both parse-success and parse-failure paths. Feeds the email-ingestion
	// data quality and parser robustness dashboards in PostHog.
	// Properties: email_type, parse_status, field_count.
	EventTicketEmailParsed AnalyticsEventName = "ticket.email.parsed"
)

// Entry verification events emitted from the backend, including the
// zero-knowledge-proof check at venue gates.
const (
	// EventEntryZkProofVerified is recorded when a fan's Groth16 proof
	// passes verification against the published Merkle root for the event.
	EventEntryZkProofVerified AnalyticsEventName = "entry.zk_proof.verified"

	// EventEntryZkProofRejected is recorded when proof verification fails
	// for any reason (invalid proof, wrong event, expired commitment).
	EventEntryZkProofRejected AnalyticsEventName = "entry.zk_proof.rejected"
)

// Notification lifecycle events emitted from the backend. The underlying
// transport is the W3C Push API, but the analytics surface is scoped under
// the notification domain to align with the user-facing concept — see
// specification/docs/analytics/event-catalog.md.
const (
	// EventNotificationSubscribed is recorded after a Web Push
	// subscription has been persisted against the user record.
	EventNotificationSubscribed AnalyticsEventName = "notification.subscribed"

	// EventNotificationUnsubscribed is recorded after a user-initiated
	// Web Push subscription has been removed via PushNotificationUseCase.Delete.
	// Auto-cleanup on 410 Gone (send-loop expiry) intentionally does NOT
	// emit this event — that is subscription expiry, not user churn.
	EventNotificationUnsubscribed AnalyticsEventName = "notification.unsubscribed"

	// EventNotificationDelivered is recorded when the push provider
	// accepts a notification for delivery. The frontend records the
	// downstream open/dismiss separately.
	EventNotificationDelivered AnalyticsEventName = "notification.delivered"
)

// knownBackendEvents is the allowlist of AnalyticsEventName constants
// emitted from the backend. AnalyticsClient implementations consult this
// via IsKnownEvent to reject typos at the call site, satisfying the
// "unknown eventName" contract on Enqueue.
//
// The map is populated lazily-once at package init via the const groups
// above; every new constant MUST be added here in the same commit.
var knownBackendEvents = map[AnalyticsEventName]struct{}{
	EventAccountSignin:              {},
	EventUserCreated:                {},
	EventArtistFollowCompleted:      {},
	EventArtistUnfollowCompleted:    {},
	EventTicketJourneyStatusChanged: {},
	EventTicketMintCompleted:        {},
	EventTicketEmailParsed:          {},
	EventEntryZkProofVerified:       {},
	EventEntryZkProofRejected:       {},
	EventNotificationSubscribed:     {},
	EventNotificationUnsubscribed:   {},
	EventNotificationDelivered:      {},
}

// IsKnownEvent reports whether name is registered in the backend event
// catalogue. AnalyticsClient implementations call this on Enqueue to
// fail fast on typos like AnalyticsEventName("ticket.purcase.completed")
// that would otherwise silently fragment downstream dashboards.
func IsKnownEvent(name AnalyticsEventName) bool {
	_, ok := knownBackendEvents[name]
	return ok
}
