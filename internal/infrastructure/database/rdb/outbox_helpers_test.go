package rdb_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/ThreeDotsLabs/watermill"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"github.com/liverty-music/backend/internal/infrastructure/messaging"
)

// testOutbox records announcements in the test database's outbox table, as
// the repositories do in production.
var testOutbox = messaging.NewOutbox(watermill.NopLogger{})

// failingOutbox fails every recording, standing for an outbox insert that
// fails inside the operation's transaction.
type failingOutbox struct{}

func (failingOutbox) Record(context.Context, pgx.Tx, string, messaging.Announcement) error {
	return errors.New("outbox unavailable")
}

func (failingOutbox) RecordAll(context.Context, pgx.Tx, string, []messaging.Announcement) error {
	return errors.New("outbox unavailable")
}

// outboxMark returns the newest outbox offset, so a test reads only the rows
// recorded after it.
func outboxMark(t *testing.T) int64 {
	t.Helper()
	var mark int64
	require.NoError(t, testDB.Pool.QueryRow(context.Background(),
		`SELECT COALESCE(MAX("offset"), 0) FROM watermill_events_outbox`).Scan(&mark))
	return mark
}

// recordedAnnouncement is one outbox row: its subject, its CloudEvents
// metadata and its protobuf payload.
type recordedAnnouncement struct {
	Subject  string            `json:"destination_topic"`
	ID       string            `json:"uuid"`
	Payload  []byte            `json:"payload"`
	Metadata map[string]string `json:"metadata"`
}

// announcedSince returns the announcements of subject recorded after mark
// whose payload, decoded into a new T, satisfies match; tests running at the
// same time record their own rows, so match picks this test's entities.
func announcedSince[T proto.Message](t *testing.T, mark int64, subject string, newT func() T, match func(T) bool) []T {
	t.Helper()
	rows, err := testDB.Pool.Query(context.Background(),
		`SELECT payload FROM watermill_events_outbox WHERE "offset" > $1 AND payload->>'destination_topic' = $2 ORDER BY "offset"`,
		mark, subject)
	require.NoError(t, err)
	raws, err := pgx.CollectRows(rows, pgx.RowTo[[]byte])
	require.NoError(t, err)

	var out []T
	for _, raw := range raws {
		var a recordedAnnouncement
		require.NoError(t, json.Unmarshal(raw, &a))
		require.Equal(t, a.ID, a.Metadata["ce_id"], "the announcement id is its ce_id")
		msg := newT()
		require.NoError(t, proto.Unmarshal(a.Payload, msg))
		if match(msg) {
			out = append(out, msg)
		}
	}
	return out
}
