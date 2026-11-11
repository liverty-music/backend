package testutil

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib" // the "pgx" driver pgtestdb connects with
	"github.com/peterldowns/pgtestdb"
	"github.com/peterldowns/pgtestdb/migrators/atlasmigrator"
)

// testDatabaseUser is the superuser of the integration-test PostgreSQL
// (compose.yml and the CI service); the server trusts every connection, so
// it has no password.
const testDatabaseUser = "test-user"

// TestDatabase names a database of the integration-test PostgreSQL.
type TestDatabase struct {
	// Port is the server's port: TEST_DB_PORT when set, else the compose
	// service's 15432.
	Port int
	// Name is the database's name.
	Name string
}

// URL returns the connection string of the database for the user that owns
// it. A searchPath that is not empty becomes the connection's search path.
func (d TestDatabase) URL(searchPath string) string {
	url := fmt.Sprintf("postgres://%s@localhost:%d/%s?sslmode=disable", testDatabaseUser, d.Port, d.Name)
	if searchPath != "" {
		url += "&search_path=" + searchPath
	}
	return url
}

// NewDatabase returns a database of the test's own on the integration-test
// PostgreSQL: a copy of a template that holds every migration of
// k8s/atlas/base/migrations, applied with search_path=app as in production.
// No other test reads or writes it, so a test needs no cleanup between runs
// and may run in parallel with any other.
//
// The template is built by the first test that asks, with the atlas CLI
// (which must be on PATH), and is named after a hash of the migration files,
// so a changed migration gets a new template. The copy is dropped when the
// test passes and kept when it fails; `docker compose down -v` removes the
// kept ones.
func NewDatabase(t testing.TB) TestDatabase {
	t.Helper()
	port := DatabasePort()
	conf := pgtestdb.Custom(t, pgtestdb.Config{
		DriverName: "pgx",
		Host:       "localhost",
		Port:       strconv.Itoa(port),
		User:       testDatabaseUser,
		Database:   "postgres",
		Options:    "sslmode=disable&search_path=app",
		// The copies are owned by the server's superuser, as the shared
		// test-db was: the tests create roles and grant them privileges.
		TestRole: &pgtestdb.Role{Username: testDatabaseUser, Capabilities: "SUPERUSER"},
	}, templateMigrator{atlasmigrator.NewDirMigrator(migrationsDir(t))})
	return TestDatabase{Port: port, Name: conf.Database}
}

// DatabasePort returns the port of the integration-test PostgreSQL:
// TEST_DB_PORT when set (to run against another container), else the compose
// service's 15432.
func DatabasePort() int {
	if port, err := strconv.Atoi(os.Getenv("TEST_DB_PORT")); err == nil {
		return port
	}
	return 15432
}

// migrationsDir returns the absolute path of k8s/atlas/base/migrations,
// found from this file so that it does not depend on the package under test.
func migrationsDir(t testing.TB) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatalf("cannot locate the migrations: no caller information")
	}
	return filepath.Join(filepath.Dir(file), "..", "..", "k8s", "atlas", "base", "migrations")
}

// templateMigrator applies the migrations to the template, holding the lock
// of [LockMigrations], and then removes every privilege a fake IAM role holds
// in it.
//
// The grant migrations give privileges to every role named like an IAM
// service account that exists when they run, and roles belong to the whole
// server. The lock keeps this repository's tests from holding such roles
// meanwhile; the removal covers roles that tests of a checkout without the
// lock leave behind, whose privileges would otherwise reach every copy.
type templateMigrator struct {
	*atlasmigrator.DirMigrator
}

// Migrate builds the template; pgtestdb calls it once per migration hash.
func (m templateMigrator) Migrate(ctx context.Context, db *sql.DB, conf pgtestdb.Config) error {
	unlock, err := lockMigrations(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	if err := m.DirMigrator.Migrate(ctx, db, conf); err != nil {
		return err
	}
	rows, err := db.QueryContext(ctx, `SELECT rolname FROM pg_roles WHERE rolname LIKE '%@%.iam'`)
	if err != nil {
		return fmt.Errorf("list IAM roles: %w", err)
	}
	var roles []string
	for rows.Next() {
		var role string
		if err := rows.Scan(&role); err != nil {
			_ = rows.Close()
			return fmt.Errorf("scan IAM role: %w", err)
		}
		roles = append(roles, role)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("list IAM roles: %w", err)
	}
	for _, role := range roles {
		if _, err := db.ExecContext(ctx, `DROP OWNED BY "`+strings.ReplaceAll(role, `"`, `""`)+`"`); err != nil {
			return fmt.Errorf("remove the privileges of %s from the template: %w", role, err)
		}
	}
	return nil
}

// migrationLockKey is the advisory lock that keeps applying migrations and
// holding fake IAM roles apart; see [LockMigrations].
const migrationLockKey = 7_105_336_001

// LockMigrations waits until no other test on the integration-test
// PostgreSQL applies migrations or holds fake IAM roles, and returns the
// function that lets them go on; it also runs at test cleanup.
//
// The grant migrations grant to every role named like an IAM service account
// that exists when they run, and roles belong to the whole server. A
// migration applied in any database while a test holds such a role grants it
// privileges there, and the test can then never drop it. Tests that apply
// migrations hold the lock while atlas runs; tests that create the roles hold
// it from creating them until they are dropped. The lock is taken in the
// postgres database, so it holds across packages and processes.
func LockMigrations(t testing.TB) (unlock func()) {
	t.Helper()
	unlock, err := lockMigrations(context.Background())
	if err != nil {
		t.Fatalf("%v", err)
	}
	t.Cleanup(unlock)
	return unlock
}

// lockMigrations takes the lock of [LockMigrations] and returns the
// function that releases it; calling it again does nothing.
func lockMigrations(ctx context.Context) (unlock func(), err error) {
	db, err := sql.Open("pgx", TestDatabase{Port: DatabasePort(), Name: "postgres"}.URL(""))
	if err != nil {
		return nil, fmt.Errorf("connect to the test server: %w", err)
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("connect to the test server: %w", err)
	}
	if _, err := conn.ExecContext(ctx, "SELECT pg_advisory_lock($1)", migrationLockKey); err != nil {
		_ = conn.Close()
		_ = db.Close()
		return nil, fmt.Errorf("take the migration lock: %w", err)
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			// Closing the session releases its advisory lock.
			_ = conn.Close()
			_ = db.Close()
		})
	}, nil
}
