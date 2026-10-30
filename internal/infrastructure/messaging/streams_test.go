package messaging_test

import (
	"bufio"
	"os"
	"strings"
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

// TestStreamForSubject verifies that StreamForSubject returns the correct stream
// name for covered subjects and ("", false) for uncovered ones. This is the
// function the pull subscriber uses to resolve the stream name for js.Consumer.
func TestStreamForSubject(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		subject    string
		wantStream string
		wantOK     bool
	}{
		{
			name:       "SERIES_IMAGE.uploaded resolves to SERIES_IMAGE stream",
			subject:    "SERIES_IMAGE.uploaded",
			wantStream: "SERIES_IMAGE",
			wantOK:     true,
		},
		{
			name:       "CONCERT.created resolves to CONCERT stream",
			subject:    "CONCERT.created",
			wantStream: "CONCERT",
			wantOK:     true,
		},
		{
			name:       "SALES_PHASE.reminder_due resolves to SALES_PHASE stream",
			subject:    "SALES_PHASE.reminder_due",
			wantStream: "SALES_PHASE",
			wantOK:     true,
		},
		{
			name:       "POISON.queue resolves to POISON stream",
			subject:    "POISON.queue",
			wantStream: "POISON",
			wantOK:     true,
		},
		{
			name:       "unknown domain returns empty and false",
			subject:    "UNKNOWN.event",
			wantStream: "",
			wantOK:     false,
		},
		{
			name:       "nested subject not covered by single-token filter returns false",
			subject:    "CONCERT.created.extra",
			wantStream: "",
			wantOK:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, ok := messaging.StreamForSubject(tt.subject)
			assert.Equal(t, tt.wantOK, ok)
			assert.Equal(t, tt.wantStream, got)
		})
	}
}

// TestStreamForSubject_AllSubjects verifies every domain-event subject in the
// entity catalogue resolves to a non-empty stream name. This closes the same
// gap as TestAllSubjectsCoveredByStream but from the StreamForSubject angle —
// the pull subscriber calls StreamForSubject at Subscribe time and the bind
// will fail if the stream is wrong.
func TestStreamForSubject_AllSubjects(t *testing.T) {
	t.Parallel()

	for _, subject := range entity.AllSubjects {
		t.Run(subject, func(t *testing.T) {
			t.Parallel()

			stream, ok := messaging.StreamForSubject(subject)
			assert.Truef(t, ok,
				"StreamForSubject(%q) returned false; add the stream to streams.go", subject)
			assert.NotEmptyf(t, stream,
				"StreamForSubject(%q) returned an empty stream name", subject)
		})
	}
}

// TestSubjectCoveredByStream exercises the NATS token-matching semantics
// directly, including the '*' (single token) vs '>' (trailing tokens) nuance.
// Both SALES_PHASE subjects are now two-token (reminder_due, not
// reminder.due), so the stream filter is a plain SALES_PHASE.*.
func TestSubjectCoveredByStream(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		subject string
		want    bool
	}{
		{
			name:    "single-token subject matched by <domain>.* stream",
			subject: "TICKET_JOURNEY.status_changed",
			want:    true,
		},
		{
			name:    "two-token SALES_PHASE.reminder_due matched by <domain>.* stream",
			subject: "SALES_PHASE.reminder_due",
			want:    true,
		},
		{
			name:    "two-token SALES_PHASE.discovered matched by <domain>.* stream",
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
			subject: "TICKET_JOURNEY.status.changed",
			want:    false,
		},
		{
			name:    "domain prefix alone (no event token) is not covered",
			subject: "TICKET_JOURNEY",
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

// TestAllSubjectsCapturedByDeclaredStreams checks every subject in the entity
// catalogue against the streams cloud-provisioning declares (the checked-in
// list testdata/jetstream_stream_subjects.txt), so no announcement is
// recorded for a subject no stream in the cluster captures: the forwarder
// would retry it forever.
func TestAllSubjectsCapturedByDeclaredStreams(t *testing.T) {
	t.Parallel()

	f, err := os.Open("testdata/jetstream_stream_subjects.txt")
	if err != nil {
		t.Fatalf("open the declared stream subjects: %v", err)
	}
	defer func() { _ = f.Close() }()
	var filters []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" && !strings.HasPrefix(line, "#") {
			filters = append(filters, line)
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("read the declared stream subjects: %v", err)
	}

	for _, subject := range entity.AllSubjects {
		captured := false
		for _, filter := range filters {
			if messaging.SubjectMatches(filter, subject) {
				captured = true
				break
			}
		}
		assert.Truef(t, captured, "no stream declared in cloud-provisioning captures %q", subject)
	}
}
