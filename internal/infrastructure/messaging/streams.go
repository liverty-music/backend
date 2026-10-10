package messaging

import (
	"strings"
	"time"

	"github.com/nats-io/nats.go"
)

// PoisonQueueSubject is the NATS subject for messages that exceeded max retries.
const PoisonQueueSubject = "POISON.queue"

// streams is the registry of JetStream streams and their subject filters.
// NACK (the external operator) owns stream lifecycle — the application only
// reads this registry to validate subject coverage and to look up which stream
// a given subject belongs to (used by the pull subscriber to bind a durable).
var streams = []nats.StreamConfig{
	{
		Name:       "CONCERT",
		Subjects:   []string{"CONCERT.*"},
		Retention:  nats.LimitsPolicy,
		MaxAge:     7 * 24 * time.Hour,
		Storage:    nats.FileStorage,
		Discard:    nats.DiscardOld,
		Replicas:   1, // overridden per environment
		Duplicates: 2 * time.Minute,
	},
	{
		Name:       "VENUE",
		Subjects:   []string{"VENUE.*"},
		Retention:  nats.LimitsPolicy,
		MaxAge:     7 * 24 * time.Hour,
		Storage:    nats.FileStorage,
		Discard:    nats.DiscardOld,
		Replicas:   1,
		Duplicates: 2 * time.Minute,
	},
	{
		Name:       "ARTIST",
		Subjects:   []string{"ARTIST.*"},
		Retention:  nats.LimitsPolicy,
		MaxAge:     7 * 24 * time.Hour,
		Storage:    nats.FileStorage,
		Discard:    nats.DiscardOld,
		Replicas:   1,
		Duplicates: 2 * time.Minute,
	},
	{
		Name:       "USER",
		Subjects:   []string{"USER.*"},
		Retention:  nats.LimitsPolicy,
		MaxAge:     7 * 24 * time.Hour,
		Storage:    nats.FileStorage,
		Discard:    nats.DiscardOld,
		Replicas:   1,
		Duplicates: 2 * time.Minute,
	},
	{
		Name:       "NOTIFICATION",
		Subjects:   []string{"NOTIFICATION.*"},
		Retention:  nats.LimitsPolicy,
		MaxAge:     7 * 24 * time.Hour,
		Storage:    nats.FileStorage,
		Discard:    nats.DiscardOld,
		Replicas:   1,
		Duplicates: 2 * time.Minute,
	},
	{
		Name: "SALES_PHASE",
		// Both subjects are two-token (SALES_PHASE.discovered,
		// SALES_PHASE.reminder_due), so a single-token `SALES_PHASE.*` matches
		// them all.
		Subjects:   []string{"SALES_PHASE.*"},
		Retention:  nats.LimitsPolicy,
		MaxAge:     7 * 24 * time.Hour,
		Storage:    nats.FileStorage,
		Discard:    nats.DiscardOld,
		Replicas:   1,
		Duplicates: 2 * time.Minute,
	},
	{
		// Carries TICKET_JOURNEY.status_changed (fan interest-tier
		// transitions), consumed by the analytics consumer.
		Name:       "TICKET_JOURNEY",
		Subjects:   []string{"TICKET_JOURNEY.*"},
		Retention:  nats.LimitsPolicy,
		MaxAge:     7 * 24 * time.Hour,
		Storage:    nats.FileStorage,
		Discard:    nats.DiscardOld,
		Replicas:   1,
		Duplicates: 2 * time.Minute,
	},
	{
		// Carries ORGANIZER.created and ORGANIZER.artist_associated (both two-token
		// subjects), consumed by the analytics consumer. A plain ORGANIZER.*
		// filter matches both subjects because each uses a single underscore token
		// for the event name (artist_associated, not artist.associated) — same
		// convention as SALES_PHASE.reminder_due.
		Name:       "ORGANIZER",
		Subjects:   []string{"ORGANIZER.*"},
		Retention:  nats.LimitsPolicy,
		MaxAge:     7 * 24 * time.Hour,
		Storage:    nats.FileStorage,
		Discard:    nats.DiscardOld,
		Replicas:   1,
		Duplicates: 2 * time.Minute,
	},
	{
		// Carries MEDIA.uploaded (organizer media upload events), consumed by
		// the media-consumer to generate WebP variants and cut over
		// series_media. A plain MEDIA.* filter matches all two-token subjects
		// — same convention as the ORGANIZER and SALES_PHASE streams.
		Name:       "MEDIA",
		Subjects:   []string{"MEDIA.*"},
		Retention:  nats.LimitsPolicy,
		MaxAge:     7 * 24 * time.Hour,
		Storage:    nats.FileStorage,
		Discard:    nats.DiscardOld,
		Replicas:   1,
		Duplicates: 2 * time.Minute,
	},
	{
		Name:       "POISON",
		Subjects:   []string{"POISON.*"},
		Retention:  nats.LimitsPolicy,
		MaxAge:     30 * 24 * time.Hour,
		Storage:    nats.FileStorage,
		Discard:    nats.DiscardOld,
		Replicas:   1,
		Duplicates: 2 * time.Minute,
	},
}

// SubjectCoveredByStream reports whether the given concrete NATS subject is
// captured by at least one configured JetStream stream. A JetStream consumer
// can only bind to a subject that some stream's subject filter matches; a
// subscription to an uncovered subject fails at startup with "no stream
// matches subject" and crashloops the consumer. This is the recurring class of
// bug that occurs when a new publisher/subscription is added for a fresh event
// domain without adding its paired stream to the streams list above.
//
// Matching follows NATS token semantics: subjects are '.'-delimited; a '*'
// token matches exactly one token, and a trailing '>' matches one or more
// remaining tokens.
func SubjectCoveredByStream(subject string) bool {
	for _, s := range streams {
		for _, filter := range s.Subjects {
			if subjectMatches(filter, subject) {
				return true
			}
		}
	}
	return false
}

// StreamForSubject returns the name of the JetStream stream whose subject
// filters cover the given concrete subject, and true. If no stream covers the
// subject it returns ("", false). The pull subscriber uses this to resolve the
// stream name needed by js.Consumer(ctx, stream, durable) when binding a
// pre-existing durable.
func StreamForSubject(subject string) (string, bool) {
	for _, s := range streams {
		for _, filter := range s.Subjects {
			if subjectMatches(filter, subject) {
				return s.Name, true
			}
		}
	}
	return "", false
}

// subjectMatches implements NATS subject-filter matching for a single filter
// against a concrete subject.
func subjectMatches(filter, subject string) bool {
	filterTokens := strings.Split(filter, ".")
	subjectTokens := strings.Split(subject, ".")

	for i, ft := range filterTokens {
		// A trailing '>' matches all remaining subject tokens (at least one).
		if ft == ">" {
			return i < len(subjectTokens)
		}
		if i >= len(subjectTokens) {
			return false
		}
		// '*' matches exactly one token; otherwise require an exact match.
		if ft != "*" && ft != subjectTokens[i] {
			return false
		}
	}

	// No '>' wildcard consumed the tail, so token counts must match exactly.
	return len(filterTokens) == len(subjectTokens)
}
