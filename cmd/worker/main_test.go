// The tests are in package main because a main package cannot be imported.
package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dbos-inc/dbos-transact-golang/dbos"
	"github.com/jackc/pgx/v5"
	"github.com/liverty-music/backend/internal/di"
	"github.com/liverty-music/backend/internal/infrastructure/server"
	"github.com/liverty-music/backend/internal/infrastructure/workflow"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	// scratchDatabase holds the dbos schema the worker verifies at start.
	scratchDatabase = "worker-test-db"

	adminDSNFormat   = "postgres://test-user@localhost:%d/test-db?sslmode=disable"
	scratchDSNFormat = "postgres://test-user@localhost:%d/%s?sslmode=disable&search_path=app"
)

func testDBPort() int {
	if port, err := strconv.Atoi(os.Getenv("TEST_DB_PORT")); err == nil {
		return port
	}
	return 15432
}

// setupScratchDatabase gives the scratch database empty DBOS system tables.
func setupScratchDatabase(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, fmt.Sprintf(adminDSNFormat, testDBPort()))
	if err != nil {
		t.Skipf("no local database available: %v", err)
	}
	defer func() { assert.NoError(t, admin.Close(ctx)) }()
	var exists bool
	require.NoError(t, admin.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname = $1)`, scratchDatabase).Scan(&exists))
	if !exists {
		_, err := admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{scratchDatabase}.Sanitize())
		require.NoError(t, err)
	}

	dsn := fmt.Sprintf(scratchDSNFormat, testDBPort(), scratchDatabase)
	conn, err := pgx.Connect(ctx, dsn)
	require.NoError(t, err)
	_, err = conn.Exec(ctx, `
		DROP SCHEMA IF EXISTS app CASCADE;
		DROP SCHEMA IF EXISTS dbos CASCADE;
		DROP SCHEMA IF EXISTS atlas_schema_revisions CASCADE;
		DROP SCHEMA IF EXISTS public CASCADE;
		CREATE SCHEMA public;`)
	require.NoError(t, err)
	require.NoError(t, conn.Close(ctx))

	// DBOS creates its tables with the SQL TestDBOSMigrationsMatchModule keeps
	// equal to the Atlas migration. The Atlas apply itself is not repeated here:
	// its IAM grant loops would grant the grants test's fake roles, which are
	// cluster-wide, privileges in this database while that test runs, and its
	// DROP ROLE would fail (rdb.TestMigrationGrants_IAMRoles covers the apply).
	require.NoError(t, dbos.Migrate(ctx, dsn, dbos.WithMigrateSchema(workflow.Schema)))
}

// startDatabaseProxy listens on a local port and refuses every connection
// until open is set; then it forwards connections to the test database.
func startDatabaseProxy(t *testing.T, open *atomic.Bool) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			client, err := ln.Accept()
			if err != nil {
				return
			}
			if !open.Load() {
				_ = client.Close()
				continue
			}
			upstream, err := net.Dial("tcp", "127.0.0.1:"+strconv.Itoa(testDBPort()))
			if err != nil {
				_ = client.Close()
				continue
			}
			go func() { _, _ = io.Copy(upstream, client); _ = upstream.Close() }()
			go func() { _, _ = io.Copy(client, upstream); _ = client.Close() }()
		}
	}()
	return ln.Addr().(*net.TCPAddr).Port
}

// probe returns the status code of the health server's path.
func probe(health *server.HealthServer, path string) int {
	rec := httptest.NewRecorder()
	health.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec.Code
}

// The 3 minutes are scaled down to 8 seconds; the worker keeps trying for 5.
//
// @spec components/infrastructure/backend/process/durable-workflows "Database unreachable at start"
func TestRun_DatabaseUnreachableAtStart(t *testing.T) {
	setupScratchDatabase(t)
	var open atomic.Bool
	port := startDatabaseProxy(t, &open)
	// The worker reads its configuration from the environment, as in its Pod.
	t.Setenv("ENVIRONMENT", "local")
	t.Setenv("DATABASE_HOST", "127.0.0.1")
	t.Setenv("DATABASE_PORT", strconv.Itoa(port))
	t.Setenv("DATABASE_NAME", scratchDatabase)
	t.Setenv("DATABASE_USER", "test-user")
	t.Setenv("LOGGING_LEVEL", "error")

	logger, err := logging.New(logging.WithWriter(io.Discard))
	require.NoError(t, err)
	health := server.NewHealthServer("127.0.0.1:0")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- run(ctx, logger, health, di.InitializeWorkerApp) }()

	unreachableUntil := time.Now().Add(8 * time.Second)
	for time.Now().Before(unreachableUntil) {
		assert.Equal(t, http.StatusServiceUnavailable, probe(health, "/readyz"), "not ready while the database is unreachable")
		assert.Equal(t, http.StatusOK, probe(health, "/healthz"), "alive, so the Pod is never restarted")
		select {
		case err := <-done:
			t.Fatalf("the worker gave up while the database was unreachable: %v", err)
		case <-time.After(500 * time.Millisecond):
		}
	}

	open.Store(true)
	require.Eventually(t, func() bool { return probe(health, "/readyz") == http.StatusOK }, 90*time.Second, 200*time.Millisecond,
		"ready once it runs its queues and schedules")
	assert.Equal(t, http.StatusOK, probe(health, "/healthz"))

	cancel()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(60 * time.Second):
		t.Fatal("the worker did not stop")
	}
}
