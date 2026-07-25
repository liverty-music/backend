package messaging

import (
	"context"
	"log/slog"
	"testing"

	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/assert"
)

// fakeConsumerLister is a test double for jsConsumerLister. It serves a fixed
// set of consumers per stream and records every DeleteConsumer call.
type fakeConsumerLister struct {
	byStream map[string][]*nats.ConsumerInfo
	deleted  []string // "<stream>/<consumer>"
}

func (f *fakeConsumerLister) ConsumersInfo(stream string, _ ...nats.JSOpt) <-chan *nats.ConsumerInfo {
	ch := make(chan *nats.ConsumerInfo)
	go func() {
		defer close(ch)
		for _, ci := range f.byStream[stream] {
			ch <- ci
		}
	}()
	return ch
}

func (f *fakeConsumerLister) DeleteConsumer(stream, consumer string, _ ...nats.JSOpt) error {
	f.deleted = append(f.deleted, stream+"/"+consumer)
	return nil
}

// consumer builds a *nats.ConsumerInfo with the given durable name, deliver
// group, and delivery policy.
func consumer(name, group string, policy nats.DeliverPolicy) *nats.ConsumerInfo {
	return &nats.ConsumerInfo{
		Name: name,
		Config: nats.ConsumerConfig{
			Durable:       name,
			DeliverGroup:  group,
			DeliverPolicy: policy,
		},
	}
}

func TestReconcileConsumers_DeletesStaleKeepsCorrect(t *testing.T) {
	t.Parallel()

	// All fixtures live on the CONCERT stream (which is in the `streams` list
	// reconcileConsumers iterates). Desired behaviors for this stream:
	desired := []string{"ingest-concert", "notify-concert"}

	js := &fakeConsumerLister{
		byStream: map[string][]*nats.ConsumerInfo{
			"CONCERT": {
				// Correct: behavior-named, self-grouped, DeliverNew → KEEP.
				consumer("notify-concert", "notify-concert", nats.DeliverNewPolicy),
				// Desired name but the old shared "consumer" group → DELETE
				// (this is the exact incident misbind config).
				consumer("ingest-concert", "consumer", nats.DeliverNewPolicy),
				// Old subject-named orphan, not in desired → DELETE.
				consumer("CONCERT_created", "consumer", nats.DeliverNewPolicy),
				// Old prefixed orphan, not in desired → DELETE.
				consumer("consumer_CONCERT_created", "consumer_CONCERT_created", nats.DeliverNewPolicy),
				// Desired + self-grouped but wrong delivery policy → DELETE.
				consumer("notify-concert-x", "notify-concert-x", nats.DeliverAllPolicy),
			},
		},
	}

	err := reconcileConsumers(context.Background(), js, desired, slog.New(slog.DiscardHandler))

	assert.NoError(t, err)
	assert.ElementsMatch(t, []string{
		"CONCERT/ingest-concert",           // shared deliver group
		"CONCERT/CONCERT_created",          // not desired
		"CONCERT/consumer_CONCERT_created", // not desired (prefixed)
		"CONCERT/notify-concert-x",         // not desired + wrong policy
	}, js.deleted)
	assert.NotContains(t, js.deleted, "CONCERT/notify-concert", "a correct behavior durable must be kept")
}

func TestClassifyConsumer(t *testing.T) {
	t.Parallel()

	desired := map[string]struct{}{"notify-concert": {}}

	tests := []struct {
		name       string
		info       *nats.ConsumerInfo
		wantStale  bool
		wantReason reconcileReason
	}{
		{
			name:      "correct behavior durable is kept",
			info:      consumer("notify-concert", "notify-concert", nats.DeliverNewPolicy),
			wantStale: false,
		},
		{
			name:       "name not desired is stale",
			info:       consumer("CONCERT_created", "CONCERT_created", nats.DeliverNewPolicy),
			wantStale:  true,
			wantReason: reasonNotDesired,
		},
		{
			name:       "shared deliver group is stale",
			info:       consumer("notify-concert", "consumer", nats.DeliverNewPolicy),
			wantStale:  true,
			wantReason: reasonSharedDeliverGroup,
		},
		{
			name:       "wrong delivery policy is stale",
			info:       consumer("notify-concert", "notify-concert", nats.DeliverAllPolicy),
			wantStale:  true,
			wantReason: reasonWrongDeliverPolicy,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			reason, stale := classifyConsumer(tt.info, desired)
			assert.Equal(t, tt.wantStale, stale)
			if tt.wantStale {
				assert.Equal(t, tt.wantReason, reason)
			}
		})
	}
}
