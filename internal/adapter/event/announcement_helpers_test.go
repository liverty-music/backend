package event_test

import (
	"testing"
	"time"

	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"github.com/liverty-music/backend/internal/infrastructure/messaging"
)

// makeAnnouncementMsg builds an announcement as the outbox records it: the
// binary protobuf payload, the announcement id as its message UUID and
// ce_id, and about as ce_subject when it is not empty.
func makeAnnouncementMsg(t *testing.T, id, about string, payload proto.Message) *message.Message {
	t.Helper()
	raw, err := proto.Marshal(payload)
	require.NoError(t, err)
	msg := message.NewMessage(id, raw)
	msg.Metadata.Set("ce_id", id)
	msg.Metadata.Set("ce_time", time.Date(2026, 10, 31, 3, 0, 0, 0, time.UTC).Format(time.RFC3339Nano))
	msg.Metadata.Set("ce_datacontenttype", messaging.ContentTypeProtobuf)
	if about != "" {
		msg.Metadata.Set("ce_subject", about)
	}
	return msg
}
