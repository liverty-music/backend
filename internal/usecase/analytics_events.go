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
	// EventAccountLogin is recorded when an existing user successfully
	// completes the OIDC authentication callback.
	EventAccountLogin AnalyticsEventName = "account.login"

	// EventAccountPreferredLanguageUpdated is recorded when a user's
	// preferred display language is changed via UpdatePreferredLanguage.
	EventAccountPreferredLanguageUpdated AnalyticsEventName = "account.preferred_language.updated"

	// EventUserCreated is recorded when the backend creates a User record
	// for the first time, either through self-signup or admin provisioning.
	EventUserCreated AnalyticsEventName = "user.created"

	// EventUserDeleted is recorded when a User record is permanently
	// removed from the platform.
	EventUserDeleted AnalyticsEventName = "user.deleted"
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
	// EventTicketLotteryEntryAccepted is recorded after a lottery entry
	// passes validation and is persisted. Paired with the frontend
	// ticket.lottery.entry.submitted to measure the intent-to-acceptance gap.
	EventTicketLotteryEntryAccepted AnalyticsEventName = "ticket.lottery.entry.accepted"

	// EventTicketLotteryEntryRejected is recorded when a lottery entry
	// fails validation (duplicate, closed window, etc.). Paired with
	// ticket.lottery.entry.submitted.
	EventTicketLotteryEntryRejected AnalyticsEventName = "ticket.lottery.entry.rejected"

	// EventTicketLotteryResultAssigned is recorded when the lottery draw
	// completes and a result (WON or LOST) is recorded against the entry.
	EventTicketLotteryResultAssigned AnalyticsEventName = "ticket.lottery.result.assigned"

	// EventTicketPurchaseCompleted is the trust-critical event recorded
	// when a payment provider confirms a successful ticket purchase. The
	// frontend emits a paired ticket.purchase.initiated at the moment the
	// user starts the purchase flow.
	EventTicketPurchaseCompleted AnalyticsEventName = "ticket.purchase.completed"

	// EventTicketPurchaseFailed is recorded when a purchase attempt is
	// rejected by the payment provider or backend validation.
	EventTicketPurchaseFailed AnalyticsEventName = "ticket.purchase.failed"

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

// Sales reminder delivery events emitted from the backend.
const (
	// EventSalesReminderDelivered is recorded at every terminal delivery
	// outcome of a sales-phase push reminder. It feeds reach and failure-rate
	// dashboards per phase stage in PostHog.
	// Properties: phase_stage, delivery_status.
	EventSalesReminderDelivered AnalyticsEventName = "sales_reminder.delivered"
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
	EventAccountLogin:                    {},
	EventAccountPreferredLanguageUpdated: {},
	EventUserCreated:                     {},
	EventUserDeleted:                     {},
	EventArtistFollowCompleted:           {},
	EventArtistUnfollowCompleted:         {},
	EventTicketLotteryEntryAccepted:      {},
	EventTicketLotteryEntryRejected:      {},
	EventTicketLotteryResultAssigned:     {},
	EventTicketPurchaseCompleted:         {},
	EventTicketPurchaseFailed:            {},
	EventTicketJourneyStatusChanged:      {},
	EventTicketMintCompleted:             {},
	EventTicketEmailParsed:               {},
	EventEntryZkProofVerified:            {},
	EventEntryZkProofRejected:            {},
	EventNotificationSubscribed:          {},
	EventNotificationUnsubscribed:        {},
	EventNotificationDelivered:           {},
	EventSalesReminderDelivered:          {},
}

// IsKnownEvent reports whether name is registered in the backend event
// catalogue. AnalyticsClient implementations call this on Enqueue to
// fail fast on typos like AnalyticsEventName("ticket.purcase.completed")
// that would otherwise silently fragment downstream dashboards.
func IsKnownEvent(name AnalyticsEventName) bool {
	_, ok := knownBackendEvents[name]
	return ok
}
