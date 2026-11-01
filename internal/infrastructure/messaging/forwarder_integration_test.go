package messaging_test

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ThreeDotsLabs/watermill"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	natsserver "github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/liverty-music/backend/internal/infrastructure/messaging"
	"github.com/liverty-music/backend/pkg/config"
)

// startOutboxJetStream starts an embedded NATS server with JetStream and a
// stream on TEST.*, with the 2-minute duplicate window of the production
// streams, and returns its URL and a JetStream context to read the stream.
func startOutboxJetStream(t *testing.T) (string, nats.JetStreamContext) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := ln.Addr().(*net.TCPAddr).Port
	require.NoError(t, ln.Close())

	s, err := natsserver.NewServer(&natsserver.Options{
		Host: "127.0.0.1", Port: port, NoLog: true, NoSigs: true,
		JetStream: true, StoreDir: t.TempDir(),
	})
	require.NoError(t, err)
	s.Start()
	t.Cleanup(s.Shutdown)
	require.True(t, s.ReadyForConnections(10*time.Second))

	url := "nats://127.0.0.1:" + strconv.Itoa(port)
	nc, err := nats.Connect(url)
	require.NoError(t, err)
	t.Cleanup(nc.Close)
	js, err := nc.JetStream()
	require.NoError(t, err)
	_, err = js.AddStream(&nats.StreamConfig{
		Name: "TEST", Subjects: []string{"TEST.*"}, Storage: nats.MemoryStorage, Duplicates: 2 * time.Minute,
	})
	require.NoError(t, err)
	return url, js
}

// isolatedOutboxPool returns a pool whose search path is a schema of its own
// holding an empty copy of the outbox table, so the forwarder under test
// reads only the rows this test records.
func isolatedOutboxPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()
	schema := "outbox_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	admin := connectTestDB(t, nil)
	_, err := admin.Exec(ctx, fmt.Sprintf(`CREATE SCHEMA %s; CREATE TABLE %s.watermill_events_outbox (LIKE app.watermill_events_outbox INCLUDING ALL)`, schema, schema))
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = connectTestDB(t, nil).Exec(context.Background(), fmt.Sprintf(`DROP SCHEMA %s CASCADE`, schema))
	})

	port, name := testDBPortAndName()
	pool, err := pgxpool.New(ctx, fmt.Sprintf("postgres://test-user@localhost:%s/%s?sslmode=disable&search_path=%s", port, name, schema))
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	return pool
}

// startForwarder runs a forwarder from pool's outbox to the NATS at url
// until the returned stop is called.
func startForwarder(t *testing.T, pool *pgxpool.Pool, url string) (stop func()) {
	t.Helper()
	logger, err := logging.New()
	require.NoError(t, err)
	pub, err := messaging.NewPublisher(config.NATSConfig{URL: url}, watermill.NopLogger{}, nil, logger)
	require.NoError(t, err)
	fwd, err := messaging.NewForwarder(pool, pub, watermill.NopLogger{})
	require.NoError(t, err)

	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = fwd.Run(context.Background())
	}()
	<-fwd.Running()
	return func() {
		require.NoError(t, fwd.Close())
		<-done
		require.NoError(t, pub.Close())
	}
}

// recordAndCommit records one announcement of subject in its own committed
// transaction, as an entity operation does with its change.
func recordAndCommit(t *testing.T, pool *pgxpool.Pool, subject string) {
	t.Helper()
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	require.NoError(t, messaging.NewOutbox(watermill.NopLogger{}).Record(ctx, tx, subject, follow("u1")))
	require.NoError(t, tx.Commit(ctx))
}

func outboxRows(t *testing.T, pool *pgxpool.Pool) int {
	t.Helper()
	var n int
	require.NoError(t, pool.QueryRow(context.Background(), `SELECT count(*) FROM watermill_events_outbox`).Scan(&n))
	return n
}

// streamMessages returns the number of messages the TEST stream holds.
func streamMessages(t *testing.T, js nats.JetStreamContext) uint64 {
	t.Helper()
	info, err := js.StreamInfo("TEST")
	require.NoError(t, err)
	return info.State.Msgs
}

func TestForwarder(t *testing.T) {
	t.Run("delivers a committed announcement and deletes its row", func(t *testing.T) {
		// @spec components/infrastructure/backend/process/event-delivery "Change and announcement together"
		url, js := startOutboxJetStream(t)
		pool := isolatedOutboxPool(t)
		stop := startForwarder(t, pool, url)
		defer stop()

		recordAndCommit(t, pool, "TEST.followed")

		require.Eventually(t, func() bool { return streamMessages(t, js) == 1 }, 10*time.Second, 50*time.Millisecond)
		require.Eventually(t, func() bool { return outboxRows(t, pool) == 0 }, 10*time.Second, 50*time.Millisecond)
		msg, err := js.GetLastMsg("TEST", "TEST.followed")
		require.NoError(t, err)
		assert.Equal(t, messaging.ContentTypeProtobuf, msg.Header.Get("ce_datacontenttype"))
		assert.Equal(t, msg.Header.Get("ce_id"), msg.Header.Get(nats.MsgIdHdr))
	})

	t.Run("delivers an announcement recorded while it was stopped", func(t *testing.T) {
		// @spec components/infrastructure/backend/process/event-delivery "Process stops before delivery"
		url, js := startOutboxJetStream(t)
		pool := isolatedOutboxPool(t)

		recordAndCommit(t, pool, "TEST.followed")
		assert.Equal(t, 1, outboxRows(t, pool), "nothing delivers while the forwarder is stopped")
		assert.Zero(t, streamMessages(t, js))

		stop := startForwarder(t, pool, url)
		defer stop()

		require.Eventually(t, func() bool { return streamMessages(t, js) == 1 }, 10*time.Second, 50*time.Millisecond)
		require.Eventually(t, func() bool { return outboxRows(t, pool) == 0 }, 10*time.Second, 50*time.Millisecond)
	})

	t.Run("a redelivery under the same id is stored once", func(t *testing.T) {
		// @spec components/infrastructure/backend/process/event-delivery "Delivered twice"
		url, js := startOutboxJetStream(t)
		pool := isolatedOutboxPool(t)
		ctx := context.Background()

		// The same row twice stands for a row published once and published
		// again because its delete was lost.
		recordAndCommit(t, pool, "TEST.followed")
		_, err := pool.Exec(ctx, `INSERT INTO watermill_events_outbox (uuid, payload, metadata) SELECT 'redelivery', payload, metadata FROM watermill_events_outbox`)
		require.NoError(t, err)
		require.Equal(t, 2, outboxRows(t, pool))

		stop := startForwarder(t, pool, url)
		defer stop()

		require.Eventually(t, func() bool { return outboxRows(t, pool) == 0 }, 10*time.Second, 50*time.Millisecond)
		assert.Equal(t, uint64(1), streamMessages(t, js), "JetStream keeps one message: both deliveries had one id")
	})
}
