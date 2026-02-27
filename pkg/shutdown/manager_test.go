package shutdown_test

import (
	"context"
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/liverty-music/backend/pkg/shutdown"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
)

type stubCloser struct {
	closed atomic.Bool
	err    error
	order  *[]string
	mu     *sync.Mutex
	name   string
}

func (s *stubCloser) Close() error {
	s.closed.Store(true)
	if s.order != nil {
		s.mu.Lock()
		*s.order = append(*s.order, s.name)
		s.mu.Unlock()
	}
	return s.err
}

func newStub(name string, err error) *stubCloser {
	return &stubCloser{name: name, err: err}
}

func newOrderedStub(name string, order *[]string, mu *sync.Mutex) *stubCloser {
	return &stubCloser{name: name, order: order, mu: mu}
}

func initShutdown(t *testing.T) {
	t.Helper()
	t.Cleanup(shutdown.Reset)
	logger, err := logging.New()
	assert.NoError(t, err)
	shutdown.Init(logger)
}

func TestShutdown(t *testing.T) {
	type args struct {
		setupFunc func() []*stubCloser
		ctx       func() context.Context
	}

	tests := []struct {
		name       string
		args       args
		wantErr    error
		wantClosed []bool
	}{
		{
			name: "all five phases execute and close all resources",
			args: args{
				setupFunc: func() []*stubCloser {
					drain := newStub("drain", nil)
					flush := newStub("flush", nil)
					ext := newStub("external", nil)
					obs := newStub("observe", nil)
					ds := newStub("datastore", nil)
					shutdown.AddDrainPhase(drain)
					shutdown.AddFlushPhase(flush)
					shutdown.AddExternalPhase(ext)
					shutdown.AddObservePhase(obs)
					shutdown.AddDatastorePhase(ds)
					return []*stubCloser{drain, flush, ext, obs, ds}
				},
				ctx: func() context.Context { return context.Background() },
			},
			wantErr:    nil,
			wantClosed: []bool{true, true, true, true, true},
		},
		{
			name: "empty phases are skipped",
			args: args{
				setupFunc: func() []*stubCloser {
					flush := newStub("flush", nil)
					ds := newStub("datastore", nil)
					shutdown.AddFlushPhase(flush)
					shutdown.AddDatastorePhase(ds)
					return []*stubCloser{flush, ds}
				},
				ctx: func() context.Context { return context.Background() },
			},
			wantErr:    nil,
			wantClosed: []bool{true, true},
		},
		{
			name: "errors are aggregated across phases",
			args: args{
				setupFunc: func() []*stubCloser {
					flush := newStub("flush", errors.New("flush failed"))
					ds := newStub("datastore", errors.New("db failed"))
					shutdown.AddFlushPhase(flush)
					shutdown.AddDatastorePhase(ds)
					return []*stubCloser{flush, ds}
				},
				ctx: func() context.Context { return context.Background() },
			},
			wantErr:    errors.New("aggregated"),
			wantClosed: []bool{true, true},
		},
		{
			name: "multiple errors within same phase are aggregated",
			args: args{
				setupFunc: func() []*stubCloser {
					c1 := newStub("ext1", errors.New("ext1 failed"))
					c2 := newStub("ext2", errors.New("ext2 failed"))
					shutdown.AddExternalPhase(c1, c2)
					return []*stubCloser{c1, c2}
				},
				ctx: func() context.Context { return context.Background() },
			},
			wantErr:    errors.New("aggregated"),
			wantClosed: []bool{true, true},
		},
		{
			name: "cancelled context skips all phases",
			args: args{
				setupFunc: func() []*stubCloser {
					flush := newStub("flush", nil)
					ds := newStub("datastore", nil)
					shutdown.AddFlushPhase(flush)
					shutdown.AddDatastorePhase(ds)
					return []*stubCloser{flush, ds}
				},
				ctx: func() context.Context {
					ctx, cancel := context.WithCancel(context.Background())
					cancel()
					return ctx
				},
			},
			wantErr:    context.Canceled,
			wantClosed: []bool{false, false},
		},
		{
			name: "empty registry succeeds",
			args: args{
				setupFunc: func() []*stubCloser { return nil },
				ctx:       func() context.Context { return context.Background() },
			},
			wantErr: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			initShutdown(t)
			closers := tt.args.setupFunc()

			err := shutdown.Shutdown(tt.args.ctx())

			if tt.wantErr != nil {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}

			for i, c := range closers {
				if i < len(tt.wantClosed) {
					assert.Equal(t, tt.wantClosed[i], c.closed.Load(), "closer %q closed state", c.name)
				}
			}
		})
	}
}

func TestShutdown_PhaseOrder(t *testing.T) {
	initShutdown(t)

	mu := &sync.Mutex{}
	order := &[]string{}

	// Register in reverse order to prove execution follows phase order.
	shutdown.AddDatastorePhase(newOrderedStub("datastore", order, mu))
	shutdown.AddObservePhase(newOrderedStub("observe", order, mu))
	shutdown.AddExternalPhase(newOrderedStub("external", order, mu))
	shutdown.AddFlushPhase(newOrderedStub("flush", order, mu))
	shutdown.AddDrainPhase(newOrderedStub("drain", order, mu))

	err := shutdown.Shutdown(context.Background())

	assert.NoError(t, err)
	assert.Equal(t, []string{"drain", "flush", "external", "observe", "datastore"}, *order)
}

func TestShutdown_ConcurrentClosersInSamePhase(t *testing.T) {
	initShutdown(t)

	const n = 10
	stubs := make([]*stubCloser, n)
	for i := range n {
		stubs[i] = newStub("ext", nil)
		shutdown.AddExternalPhase(stubs[i])
	}

	err := shutdown.Shutdown(context.Background())

	assert.NoError(t, err)
	for i, c := range stubs {
		assert.True(t, c.closed.Load(), "closer %d should be closed", i)
	}
}

func TestShutdown_OnlyExecutesOnce(t *testing.T) {
	initShutdown(t)

	var count atomic.Int32
	c := &countCloser{count: &count}
	shutdown.AddFlushPhase(c)

	err1 := shutdown.Shutdown(context.Background())
	err2 := shutdown.Shutdown(context.Background())

	assert.NoError(t, err1)
	assert.NoError(t, err2)
	assert.Equal(t, int32(1), count.Load(), "Close should be called exactly once")
}

func TestShutdown_EmptyPhaseRegistration(t *testing.T) {
	initShutdown(t)

	var nilClosers []io.Closer
	shutdown.AddFlushPhase(nilClosers...)

	err := shutdown.Shutdown(context.Background())
	assert.NoError(t, err)
}

func TestShutdown_InitIgnoresSecondCall(t *testing.T) {
	t.Cleanup(shutdown.Reset)

	logger1, err := logging.New()
	assert.NoError(t, err)
	logger2, err := logging.New()
	assert.NoError(t, err)

	shutdown.Init(logger1)
	shutdown.Init(logger2) // should be silently ignored

	// Verify the package still works (uses first logger).
	stub := newStub("drain", nil)
	shutdown.AddDrainPhase(stub)

	err = shutdown.Shutdown(context.Background())
	assert.NoError(t, err)
	assert.True(t, stub.closed.Load())
}

func TestShutdown_WithoutInitReturnsError(t *testing.T) {
	t.Cleanup(shutdown.Reset)
	// Do NOT call shutdown.Init — logger is nil.

	err := shutdown.Shutdown(context.Background())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "Init() must be called before Shutdown()")
}

type countCloser struct {
	count *atomic.Int32
}

func (c *countCloser) Close() error {
	c.count.Add(1)
	return nil
}
