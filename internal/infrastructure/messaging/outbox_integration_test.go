package messaging_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	"github.com/ThreeDotsLabs/watermill"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"github.com/liverty-music/backend/internal/infrastructure/messaging"
)

// insertCounter counts the INSERT statements a connection sends to the
// outbox table.
type insertCounter struct{ n atomic.Int64 }

func (c *insertCounter) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	if strings.Contains(data.SQL, "INSERT INTO") && strings.Contains(data.SQL, "watermill_events_outbox") {
		c.n.Add(1)
	}
	return ctx
}

func (c *insertCounter) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

// connectTestDB connects to the integration-test PostgreSQL (the compose
// service, or TEST_DB_PORT / TEST_DB_NAME) with the app search path.
func connectTestDB(t *testing.T, tracer pgx.QueryTracer) *pgx.Conn {
	t.Helper()
	port, name := os.Getenv("TEST_DB_PORT"), os.Getenv("TEST_DB_NAME")
	if port == "" {
		port = "15432"
	}
	if name == "" {
		name = "test-db"
	}
	cfg, err := pgx.ParseConfig(fmt.Sprintf("postgres://test-user@localhost:%s/%s?sslmode=disable&search_path=app,public", port, name))
	require.NoError(t, err)
	cfg.Tracer = tracer
	conn, err := pgx.ConnectConfig(context.Background(), cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close(context.Background()) })
	return conn
}

// envelope is the part of the forwarder envelope a row holds that the test
// reads.
type envelope struct {
	DestinationTopic string            `json:"destination_topic"`
	UUID             string            `json:"uuid"`
	Payload          []byte            `json:"payload"`
	Metadata         map[string]string `json:"metadata"`
}

// recordedRows returns the envelopes of subject visible to q, oldest first.
// Each test uses its own subject, so rows other tests record do not count.
func recordedRows(t *testing.T, q interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}, subject string) []envelope {
	t.Helper()
	rows, err := q.Query(context.Background(), `SELECT payload FROM watermill_events_outbox WHERE payload->>'destination_topic' = $1 ORDER BY "offset"`, subject)
	require.NoError(t, err)
	raws, err := pgx.CollectRows(rows, pgx.RowTo[[]byte])
	require.NoError(t, err)
	out := make([]envelope, 0, len(raws))
	for _, raw := range raws {
		var e envelope
		require.NoError(t, json.Unmarshal(raw, &e))
		out = append(out, e)
	}
	return out
}

func follow(user string) messaging.Announcement {
	return messaging.Announcement{Payload: &entityv1.Follow{
		UserId:   &entityv1.UserId{Value: user},
		ArtistId: &entityv1.ArtistId{Value: "a"},
		Hype:     entityv1.HypeType_HYPE_TYPE_WATCH,
	}}
}

func TestOutbox_Record(t *testing.T) {
	ctx := context.Background()
	outbox := messaging.NewOutbox(watermill.NopLogger{})

	t.Run("one row per call with its id as ce_id", func(t *testing.T) {
		subject := "TEST." + uuid.NewString()
		tx, err := connectTestDB(t, nil).Begin(ctx)
		require.NoError(t, err)
		defer func() { _ = tx.Rollback(ctx) }()

		require.NoError(t, outbox.Record(ctx, tx, subject, messaging.Announcement{
			Payload: follow("u1").Payload,
			Subject: "a",
		}))
		require.NoError(t, outbox.Record(ctx, tx, subject, follow("u2")))

		rows := recordedRows(t, tx, subject)
		require.Len(t, rows, 2)
		first := rows[0]
		assert.Equal(t, subject, first.DestinationTopic)
		assert.Equal(t, first.UUID, first.Metadata["ce_id"])
		id, err := uuid.Parse(first.UUID)
		require.NoError(t, err)
		assert.Equal(t, uuid.Version(7), id.Version())
		assert.Equal(t, subject, first.Metadata["ce_type"])
		assert.Equal(t, messaging.ContentTypeProtobuf, first.Metadata["ce_datacontenttype"])
		assert.Equal(t, "a", first.Metadata["ce_subject"])
		assert.NotContains(t, rows[1].Metadata, "ce_subject")
		assert.NotEqual(t, first.UUID, rows[1].UUID)

		var got entityv1.Follow
		require.NoError(t, proto.Unmarshal(first.Payload, &got))
		assert.Equal(t, "u1", got.GetUserId().GetValue())
	})

	t.Run("a list call writes one multi-row insert", func(t *testing.T) {
		subject := "TEST." + uuid.NewString()
		counter := &insertCounter{}
		tx, err := connectTestDB(t, counter).Begin(ctx)
		require.NoError(t, err)
		defer func() { _ = tx.Rollback(ctx) }()

		require.NoError(t, outbox.RecordAll(ctx, tx, subject, []messaging.Announcement{follow("u1"), follow("u2"), follow("u3")}))

		assert.Equal(t, int64(1), counter.n.Load())
		assert.Len(t, recordedRows(t, tx, subject), 3)
	})

	t.Run("nothing is recorded when the transaction rolls back", func(t *testing.T) {
		subject := "TEST." + uuid.NewString()
		conn := connectTestDB(t, nil)
		tx, err := conn.Begin(ctx)
		require.NoError(t, err)
		require.NoError(t, outbox.Record(ctx, tx, subject, follow("u1")))
		require.NoError(t, tx.Rollback(ctx))

		assert.Empty(t, recordedRows(t, conn, subject))
	})
}
