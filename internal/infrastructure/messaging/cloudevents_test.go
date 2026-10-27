package messaging_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/google/uuid"
	"github.com/liverty-music/backend/internal/infrastructure/messaging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type testPayload struct {
	Name  string `json:"name"`
	Value int    `json:"value"`
}

func TestNewEvent_MetadataFields(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	data := testPayload{Name: "test", Value: 42}

	msg, err := messaging.NewEvent(ctx, data)

	require.NoError(t, err)
	require.NotNil(t, msg)

	t.Run("specversion is set to 1.0", func(t *testing.T) {
		t.Parallel()

		assert.Equal(t, "1.0", msg.Metadata.Get("ce_specversion"))
	})

	t.Run("source is set to liverty-music/backend", func(t *testing.T) {
		t.Parallel()

		assert.Equal(t, "liverty-music/backend", msg.Metadata.Get("ce_source"))
	})

	t.Run("ce_id is a non-empty UUID", func(t *testing.T) {
		t.Parallel()

		id := msg.Metadata.Get("ce_id")
		assert.NotEmpty(t, id)
		// A UUID is 36 chars including hyphens
		assert.Len(t, id, 36)
	})

	t.Run("message UUID matches ce_id", func(t *testing.T) {
		t.Parallel()

		assert.Equal(t, msg.Metadata.Get("ce_id"), msg.UUID)
	})

	t.Run("ce_time is a valid RFC3339 timestamp", func(t *testing.T) {
		t.Parallel()

		ceTime := msg.Metadata.Get("ce_time")
		require.NotEmpty(t, ceTime)

		parsed, parseErr := time.Parse(time.RFC3339, ceTime)
		require.NoError(t, parseErr)
		// The timestamp should be recent (within 5 seconds of now).
		assert.WithinDuration(t, time.Now().UTC(), parsed, 5*time.Second)
	})

	t.Run("datacontenttype is application/json", func(t *testing.T) {
		t.Parallel()

		assert.Equal(t, "application/json", msg.Metadata.Get("ce_datacontenttype"))
	})

	t.Run("payload is valid JSON matching the input data", func(t *testing.T) {
		t.Parallel()

		var got testPayload
		require.NoError(t, json.Unmarshal(msg.Payload, &got))
		assert.Equal(t, data, got)
	})
}

func TestNewEvent_UniqueIDs(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	msg1, err1 := messaging.NewEvent(ctx, testPayload{Name: "a"})
	msg2, err2 := messaging.NewEvent(ctx, testPayload{Name: "b"})

	require.NoError(t, err1)
	require.NoError(t, err2)

	assert.NotEqual(t, msg1.UUID, msg2.UUID, "each event must have a unique ID")
}

func TestNewEvent_ContextPropagation(t *testing.T) {
	t.Parallel()

	type ctxKey string
	const key ctxKey = "trace-id"

	ctx := context.WithValue(context.Background(), key, "trace-abc")

	msg, err := messaging.NewEvent(ctx, testPayload{})

	require.NoError(t, err)
	assert.Equal(t, "trace-abc", msg.Context().Value(key))
}

func TestNewEvent_UnmarshalablePayloadReturnsError(t *testing.T) {
	t.Parallel()

	// Channels cannot be JSON-marshalled.
	_, err := messaging.NewEvent(context.Background(), make(chan int))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "marshal event data")
}

func TestParseCloudEventData_ValidPayload(t *testing.T) {
	t.Parallel()

	original := testPayload{Name: "hello", Value: 99}
	payload, err := json.Marshal(original)
	require.NoError(t, err)

	msg := message.NewMessage("id-1", payload)

	var got testPayload
	require.NoError(t, messaging.ParseCloudEventData(msg, &got))
	assert.Equal(t, original, got)
}

func TestParseCloudEventData_InvalidJSON(t *testing.T) {
	t.Parallel()

	msg := message.NewMessage("id-bad", []byte("not json at all"))

	var got testPayload
	err := messaging.ParseCloudEventData(msg, &got)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "unmarshal event data")
}

func TestNewEvent_ID(t *testing.T) {
	t.Parallel()

	// eventIDs publishes once under each context and returns the CloudEvent ids.
	eventIDs := func(t *testing.T, ctxs ...context.Context) []string {
		t.Helper()
		var ids []string
		for _, ctx := range ctxs {
			msg, err := messaging.NewEvent(ctx, testPayload{Name: "x"})
			require.NoError(t, err)
			ids = append(ids, msg.Metadata.Get("ce_id"))
		}
		return ids
	}

	tests := []struct {
		name      string
		ctxs      func() []context.Context
		wantEqual bool
	}{
		{
			name: "the same step run again publishes with the same id",
			ctxs: func() []context.Context {
				return []context.Context{
					messaging.WithPublishOrigin(context.Background(), "wf-1/3"),
					messaging.WithPublishOrigin(context.Background(), "wf-1/3"),
				}
			},
			wantEqual: true,
		},
		{
			name: "two passes of a loop are two steps and publish with two ids",
			ctxs: func() []context.Context {
				return []context.Context{
					messaging.WithPublishOrigin(context.Background(), "wf-1/3"),
					messaging.WithPublishOrigin(context.Background(), "wf-1/4"),
				}
			},
			wantEqual: false,
		},
		{
			name: "two publishes in one step get two ids",
			ctxs: func() []context.Context {
				ctx := messaging.WithPublishOrigin(context.Background(), "wf-1/3")
				return []context.Context{ctx, ctx}
			},
			wantEqual: false,
		},
		{
			name: "two publishes outside a workflow get two ids",
			ctxs: func() []context.Context {
				return []context.Context{context.Background(), context.Background()}
			},
			wantEqual: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ids := eventIDs(t, tt.ctxs()...)
			for _, id := range ids {
				parsed, err := uuid.Parse(id)
				require.NoError(t, err)
				assert.Equal(t, uuid.Version(5), parsed.Version(), "every event id is a UUIDv5")
			}
			if tt.wantEqual {
				assert.Equal(t, ids[0], ids[1])
			} else {
				assert.NotEqual(t, ids[0], ids[1])
			}
		})
	}
}
