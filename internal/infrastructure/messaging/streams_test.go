package messaging_test

import (
	"testing"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/messaging"
	"github.com/stretchr/testify/assert"
)

// TestAllSubjectsCoveredByStream is the regression guard for the recurring
// "added a publisher/subscription without its paired JetStream stream" bug:
// every domain-event subject in the entity catalogue MUST be captured by a
// configured stream, otherwise a JetStream consumer that subscribes to it
// fails at startup with "no stream matches subject" and crashloops in
// production. This ran red for TICKET/TICKET_JOURNEY/TICKET_EMAIL/SALES_REMINDER
// (and earlier SALES_PHASE) before their streams were added.
func TestAllSubjectsCoveredByStream(t *testing.T) {
	t.Parallel()

	for _, subject := range entity.AllSubjects {
		t.Run(subject, func(t *testing.T) {
			t.Parallel()

			assert.Truef(t, messaging.SubjectCoveredByStream(subject),
				"subject %q is not covered by any JetStream stream; add a stream "+
					"whose Subjects match it to the streams list in streams.go, "+
					"otherwise a consumer subscribing to it crashloops with "+
					"\"no stream matches subject\"", subject)
		})
	}
}

// TestSubjectCoveredByStream exercises the NATS token-matching semantics
// directly, including the '*' (single token) vs '>' (trailing tokens) nuance
// that made SALES_PHASE.reminder.due require a '>' filter.
func TestSubjectCoveredByStream(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		subject string
		want    bool
	}{
		{
			name:    "single-token subject matched by <domain>.* stream",
			subject: "TICKET.mint_completed",
			want:    true,
		},
		{
			name:    "nested subject matched by <domain>.> stream",
			subject: "SALES_PHASE.reminder.due",
			want:    true,
		},
		{
			name:    "single-token subject matched by <domain>.> stream",
			subject: "SALES_PHASE.discovered",
			want:    true,
		},
		{
			name:    "unknown domain is not covered",
			subject: "UNKNOWN.event",
			want:    false,
		},
		{
			name:    "nested subject is NOT covered by a single-token .* filter",
			subject: "TICKET.mint.completed",
			want:    false,
		},
		{
			name:    "domain prefix alone (no event token) is not covered",
			subject: "TICKET",
			want:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, messaging.SubjectCoveredByStream(tt.subject))
		})
	}
}
