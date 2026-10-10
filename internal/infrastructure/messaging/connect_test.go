package messaging_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/ThreeDotsLabs/watermill"
	natsserver "github.com/nats-io/nats-server/v2/server"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/liverty-music/backend/internal/infrastructure/messaging"
	"github.com/liverty-music/backend/pkg/config"
)

// syncBuffer is a bytes.Buffer safe for the NATS callback goroutine to write
// while the test reads.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

// entries parses the buffered JSON log lines.
func (b *syncBuffer) entries(t *testing.T) []map[string]any {
	t.Helper()
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []map[string]any
	sc := bufio.NewScanner(bytes.NewReader(b.buf.Bytes()))
	for sc.Scan() {
		var e map[string]any
		require.NoError(t, json.Unmarshal(sc.Bytes(), &e), sc.Text())
		out = append(out, e)
	}
	return out
}

func newJSONLogger(t *testing.T) (*logging.Logger, *syncBuffer) {
	t.Helper()
	buf := &syncBuffer{}
	logger, err := logging.New(logging.WithFormat(logging.FormatJSON), logging.WithWriter(buf))
	require.NoError(t, err)
	return logger, buf
}

// freePort returns a local TCP port nothing listens on.
func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := ln.Addr().(*net.TCPAddr).Port
	require.NoError(t, ln.Close())
	return port
}

func natsURL(port int) string {
	return "nats://127.0.0.1:" + strconv.Itoa(port)
}

// newNATS returns an embedded NATS server for port, not yet started. The
// caller starts it with Start and stops it with Shutdown.
func newNATS(t *testing.T, port int) *natsserver.Server {
	t.Helper()
	s, err := natsserver.NewServer(&natsserver.Options{Host: "127.0.0.1", Port: port, NoLog: true, NoSigs: true})
	require.NoError(t, err)
	t.Cleanup(s.Shutdown)
	return s
}

// The API server's only startup step that touches the broker is creating the
// publisher. It must succeed without the broker, so DI completes and the
// server's readiness (which checks only the database) and read calls are
// unaffected.
//
// @spec components/infrastructure/backend/process/startup-dependencies "Start while the broker is down"
func TestNewPublisher_BrokerUnreachable(t *testing.T) {
	t.Parallel()
	logger, _ := newJSONLogger(t)
	cfg := config.NATSConfig{URL: natsURL(freePort(t))}

	start := time.Now()
	pub, err := messaging.NewPublisher(cfg, watermill.NopLogger{}, nil, logger)

	require.NoError(t, err)
	require.NotNil(t, pub)
	t.Cleanup(func() { _ = pub.Close() })
	assert.Less(t, time.Since(start), 2*time.Second, "NewPublisher must not wait for the broker")
}

// @spec components/infrastructure/backend/process/startup-dependencies "Broker back within the wait"
// @spec components/infrastructure/backend/process/structured-logging "Waiting for the broker"
func TestConnectWithRetry_BrokerBackWithinWait(t *testing.T) {
	t.Parallel()
	logger, buf := newJSONLogger(t)
	port := freePort(t)

	srv := newNATS(t, port)
	time.AfterFunc(2*time.Second, srv.Start)

	nc, err := messaging.ConnectWithRetry(context.Background(), natsURL(port), 30*time.Second, logger)

	require.NoError(t, err)
	t.Cleanup(nc.Close)
	assert.True(t, nc.IsConnected())

	var retries int
	for _, e := range buf.entries(t) {
		if e["msg"] == "NATS connection failed, retrying" {
			retries++
			assert.Equal(t, "WARN", e["level"])
		}
		assert.NotEqual(t, "ERROR", e["level"], "no retry is an error: %v", e)
	}
	assert.Positive(t, retries, "each retry is logged")
}

func TestConnectWithRetry_GivesUpAfterBudget(t *testing.T) {
	t.Parallel()
	logger, _ := newJSONLogger(t)

	start := time.Now()
	nc, err := messaging.ConnectWithRetry(context.Background(), natsURL(freePort(t)), 2*time.Second, logger)

	require.Error(t, err)
	assert.Nil(t, nc)
	assert.Contains(t, err.Error(), "NATS unreachable for 2s")
	assert.GreaterOrEqual(t, time.Since(start), 2*time.Second)
}

func TestConnectWithRetry_StopsWhenContextEnds(t *testing.T) {
	t.Parallel()
	logger, _ := newJSONLogger(t)
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	nc, err := messaging.ConnectWithRetry(ctx, natsURL(freePort(t)), time.Minute, logger)

	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Nil(t, nc)
}

func TestNATSReconnectDelay(t *testing.T) {
	t.Parallel()

	tests := []struct {
		attempts int
		base     time.Duration
	}{
		{attempts: 1, base: 1 * time.Second},
		{attempts: 2, base: 2 * time.Second},
		{attempts: 3, base: 4 * time.Second},
		{attempts: 4, base: 8 * time.Second},
		{attempts: 5, base: 15 * time.Second},
		{attempts: 100, base: 15 * time.Second},
	}
	for _, tt := range tests {
		t.Run(strconv.Itoa(tt.attempts), func(t *testing.T) {
			t.Parallel()
			got := messaging.NATSReconnectDelay(tt.attempts)
			assert.GreaterOrEqual(t, got, tt.base)
			assert.Less(t, got, tt.base+tt.base/5)
		})
	}
}
