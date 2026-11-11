package messaging_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	"github.com/ThreeDotsLabs/watermill"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"github.com/liverty-music/backend/internal/infrastructure/messaging"
	"github.com/liverty-music/backend/internal/infrastructure/messaging/mocks"
	"github.com/liverty-music/backend/internal/testutil"
	"github.com/liverty-music/backend/pkg/eventmsg"
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

// connectTestDB connects to a database of the test's own (see
// [testutil.NewDatabase]) with the app search path, so the rows the test
// reads are only the rows it recorded.
func connectTestDB(t *testing.T, tracer pgx.QueryTracer) *pgx.Conn {
	t.Helper()
	cfg, err := pgx.ParseConfig(testutil.NewDatabase(t).URL("app,public"))
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

// protoEncoder returns an Encoder that passes through payloads that are
// already Protobuf messages, as the tests announce them.
func protoEncoder(t *testing.T) *mocks.MockEncoder {
	t.Helper()
	enc := mocks.NewMockEncoder(t)
	enc.EXPECT().Encode(mock.Anything, mock.Anything).RunAndReturn(func(_ string, p any) (proto.Message, error) {
		return p.(proto.Message), nil
	}).Maybe()
	return enc
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
	outbox := messaging.NewOutbox(watermill.NopLogger{}, protoEncoder(t))

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
		assert.Equal(t, eventmsg.ContentTypeProtobuf, first.Metadata["ce_datacontenttype"])
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

	t.Run("a payload the encoder rejects is Internal and records nothing", func(t *testing.T) {
		subject := "TEST." + uuid.NewString()
		tx, err := connectTestDB(t, nil).Begin(ctx)
		require.NoError(t, err)
		defer func() { _ = tx.Rollback(ctx) }()
		enc := mocks.NewMockEncoder(t)
		enc.EXPECT().Encode(subject, "not an entity").Return(nil, errors.New("unknown type"))

		err = messaging.NewOutbox(watermill.NopLogger{}, enc).Record(ctx, tx, subject, messaging.Announcement{Payload: "not an entity"})

		assert.ErrorIs(t, err, apperr.ErrInternal)
		assert.Empty(t, recordedRows(t, tx, subject))
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
