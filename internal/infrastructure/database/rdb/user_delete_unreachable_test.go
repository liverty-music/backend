package rdb_test

import (
	"context"
	"fmt"
	"io"
	"net"
	"strconv"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
	"github.com/liverty-music/backend/internal/testutil"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stoppableProxy forwards connections to the test database until stop, which
// closes the open connections and refuses new ones, as a stopped database
// does. The shared test server itself cannot be stopped.
type stoppableProxy struct {
	listener net.Listener
	mu       sync.Mutex
	conns    []net.Conn
	stopped  bool
}

func startProxy(t *testing.T, target int) *stoppableProxy {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	p := &stoppableProxy{listener: l}
	t.Cleanup(p.stop)
	go func() {
		for {
			client, err := l.Accept()
			if err != nil {
				return
			}
			server, err := net.Dial("tcp", net.JoinHostPort("localhost", strconv.Itoa(target)))
			if err != nil || !p.track(client, server) {
				_ = client.Close()
				continue
			}
			go func() { _, _ = io.Copy(server, client); _ = server.Close() }()
			go func() { _, _ = io.Copy(client, server); _ = client.Close() }()
		}
	}()
	return p
}

func (p *stoppableProxy) track(conns ...net.Conn) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.stopped {
		return false
	}
	p.conns = append(p.conns, conns...)
	return true
}

func (p *stoppableProxy) port() int { return p.listener.Addr().(*net.TCPAddr).Port }

func (p *stoppableProxy) stop() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.stopped = true
	_ = p.listener.Close()
	for _, c := range p.conns {
		_ = c.Close()
	}
}

// @spec components/entity/user/delete "Store unreachable"
func TestUserRepository_Delete_StoreUnreachable(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	clone := testutil.NewDatabase(t)
	direct := openTestDB(t, clone, 2)
	user, err := rdb.NewUserRepository(direct, testOutbox).Create(ctx, newTestUser("ext-unreachable", "unreachable@example.com", "Unreachable"))
	require.NoError(t, err)

	proxy := startProxy(t, clone.Port)
	through := clone
	through.Port = proxy.port()
	repo := rdb.NewUserRepository(openTestDB(t, through, 2), testOutbox)
	_, err = repo.Get(ctx, user.ID)
	require.NoError(t, err, "the store is reached through the proxy before it stops")

	proxy.stop()

	// The first calls use the connections the stop broke; later ones fail to
	// connect.
	for range 4 {
		assert.ErrorIs(t, repo.Delete(ctx, user.ID), apperr.ErrUnavailable)
		assert.ErrorIs(t, repo.SerializeDeletion(ctx, user.ID, func(context.Context) error { return nil }), apperr.ErrUnavailable)
		_, err = repo.Get(ctx, user.ID)
		assert.ErrorIs(t, err, apperr.ErrUnavailable)
	}
	_, err = rdb.NewUserRepository(direct, testOutbox).Get(ctx, user.ID)
	assert.NoError(t, err, "nothing was removed")
}

// @spec components/entity/user/forget-removed-identity "Store unreachable"
func TestUserRepository_ForgetRemovedIdentity_StoreUnreachable(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	clone := testutil.NewDatabase(t)
	direct := openTestDB(t, clone, 2)
	user, err := rdb.NewUserRepository(direct, testOutbox).Create(ctx, newTestUser("ext-forget-unreachable", "forget-unreachable@example.com", "Unreachable"))
	require.NoError(t, err)
	require.NoError(t, rdb.NewUserRepository(direct, testOutbox).Delete(ctx, user.ID))

	proxy := startProxy(t, clone.Port)
	through := clone
	through.Port = proxy.port()
	repo := rdb.NewUserRepository(openTestDB(t, through, 2), testOutbox)
	_, err = repo.Get(ctx, "019e3029-8044-75fc-a662-2ddfce51178e")
	require.ErrorIs(t, err, apperr.ErrNotFound, "the store is reached through the proxy before it stops")

	proxy.stop()

	for range 4 {
		assert.ErrorIs(t, repo.ForgetRemovedIdentity(ctx, "ext-forget-unreachable"), apperr.ErrUnavailable)
	}
	_, err = rdb.NewUserRepository(direct, testOutbox).Create(ctx, newTestUser("ext-forget-unreachable", "forget-unreachable-2@example.com", "Again"))
	assert.ErrorIs(t, err, apperr.ErrFailedPrecondition, "the record remains")
}

// A database that accepts the connection and never answers: the connection
// attempt times out while the caller's context goes on.
//
// @spec components/entity/user/delete "Store unreachable"
func TestUserRepository_Delete_StoreNotAnswering(t *testing.T) {
	t.Parallel()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })
	cfg, err := pgxpool.ParseConfig(fmt.Sprintf("postgres://test-user@%s/db?sslmode=disable&connect_timeout=1", listener.Addr()))
	require.NoError(t, err)
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	repo := rdb.NewUserRepository(&rdb.Database{Pool: rdb.NewTracedPool(pool, "db", "127.0.0.1")}, testOutbox)

	err = repo.Delete(context.Background(), "019e3029-8044-75fc-a662-2ddfce51178e")

	assert.ErrorIs(t, err, apperr.ErrUnavailable)
}
