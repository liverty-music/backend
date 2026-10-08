package rdb_test

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Fake Cloud SQL IAM service-account roles. Cloud SQL names them
// `<name>@<project>.iam`, so they match the role patterns the grant
// migrations loop over.
const (
	grantsOrganizerRole = "organizer-console-api@test.iam"
	grantsZitadelRole   = "zitadel@test.iam"

	// grantsProbeTable is created after every migration has run, to check the
	// default privileges a future table inherits.
	grantsProbeTable = "grants_probe"

	// grantsTestDSNFormat targets the integration-test PostgreSQL as the
	// superuser test-user. search_path=app matches the Atlas Operator
	// connection (k8s/atlas/base/atlas-migration.yaml), so every table lands in
	// schema app as in production.
	grantsTestDSNFormat = "postgres://test-user@localhost:15432/%s?sslmode=disable&search_path=app"

	// grantsTestDatabase is the scratch database the migrations are applied
	// to, next to the shared test-db.
	grantsTestDatabase = "grants-test-db"
)

// grantsReadWriteRoles keep full CRUD on the app schema.
var grantsReadWriteRoles = []string{
	"admin-console-api@test.iam",
	"backend-app@test.iam",
	"fan-api@test.iam",
	"media-consumer@test.iam",
}

// organizerTableWrites is the write set of one app table for the
// organizer-console-api role. updateColumns lists the only columns it may
// UPDATE; table-level UPDATE is never granted.
type organizerTableWrites struct {
	insert        bool
	delete        bool
	updateColumns []string
}

// organizerWrites is every write the organizer server performs (see
// 20261008020000_restrict_organizer_console_api_and_zitadel_app_grants.sql).
// Any other app table is SELECT-only for the role.
var organizerWrites = map[string]organizerTableWrites{
	"series": {insert: true, updateColumns: []string{
		"title", "type", "source_url", "description", "visibility",
		"unlisted_token", "publish_state", "published_at", "cancelled_at",
	}},
	"draft_events":                 {insert: true, delete: true},
	"draft_series_performers":      {insert: true, delete: true},
	"venues":                       {insert: true},
	"events":                       {insert: true, updateColumns: []string{"series_id"}},
	"event_performers":             {insert: true},
	"concerts":                     {insert: true},
	"staged_concerts":              {delete: true},
	"media":                        {insert: true},
	"lottery_sales_phases":         {insert: true, updateColumns: []string{"verification_requirement"}},
	"organizer_connected_accounts": {insert: true, updateColumns: []string{"account_ref", "status", "status_synced_at"}},
	"reception_links":              {insert: true, updateColumns: []string{"status", "bound_public_key", "bound_at", "token", "revoked_at"}},
	"tickets":                      {updateColumns: []string{"admitted_at"}},
	"admissions":                   {insert: true},
	"rejected_scans":               {insert: true},
}

// TestMigrationGrants_IAMRoles applies every Atlas migration to a scratch
// database in which the IAM service-account roles already exist, then checks
// the privileges each role ends up with on schema app. It fails when a
// migration (for example a generic `%@%.iam` grant loop) widens the
// organizer-console-api write set or gives zitadel any app privilege.
func TestMigrationGrants_IAMRoles(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	ctx := context.Background()
	conn := setupGrantsDatabase(t, ctx)

	tables := listAppTables(t, ctx, conn)
	require.Contains(t, tables, grantsProbeTable)
	for table := range organizerWrites {
		require.Contains(t, tables, table, "organizer write table missing from schema app")
	}

	t.Run("organizer-console-api reads every table and writes only its write set", func(t *testing.T) {
		assert.True(t, hasSchemaUsage(t, ctx, conn, grantsOrganizerRole))
		for _, table := range tables {
			want := organizerWrites[table]
			assert.True(t, hasTablePrivilege(t, ctx, conn, grantsOrganizerRole, table, "SELECT"), "SELECT on %s", table)
			assert.Equal(t, want.insert, hasTablePrivilege(t, ctx, conn, grantsOrganizerRole, table, "INSERT"), "INSERT on %s", table)
			assert.Equal(t, want.delete, hasTablePrivilege(t, ctx, conn, grantsOrganizerRole, table, "DELETE"), "DELETE on %s", table)
			assert.False(t, hasTablePrivilege(t, ctx, conn, grantsOrganizerRole, table, "UPDATE"), "table-level UPDATE on %s", table)
			for _, privilege := range []string{"TRUNCATE", "REFERENCES", "TRIGGER"} {
				assert.False(t, hasTablePrivilege(t, ctx, conn, grantsOrganizerRole, table, privilege), "%s on %s", privilege, table)
			}
			for _, column := range listColumns(t, ctx, conn, table) {
				assert.Equal(t, slices.Contains(want.updateColumns, column),
					hasColumnPrivilege(t, ctx, conn, grantsOrganizerRole, table, column, "UPDATE"),
					"UPDATE on %s.%s", table, column)
			}
		}
		assert.False(t, hasAnySequencePrivilege(t, ctx, conn, grantsOrganizerRole), "sequence privilege")
	})

	t.Run("organizer-console-api cannot alter evidence or fan records", func(t *testing.T) {
		for _, table := range []string{"admissions", "rejected_scans", "orders", "users", "verified_identities"} {
			assert.False(t, hasAnyColumnPrivilege(t, ctx, conn, grantsOrganizerRole, table, "UPDATE"), "UPDATE on %s", table)
			assert.False(t, hasTablePrivilege(t, ctx, conn, grantsOrganizerRole, table, "DELETE"), "DELETE on %s", table)
		}
	})

	t.Run("zitadel holds no app privileges", func(t *testing.T) {
		assert.False(t, hasSchemaUsage(t, ctx, conn, grantsZitadelRole), "USAGE on schema app")
		for _, table := range tables {
			for _, privilege := range []string{"SELECT", "INSERT", "UPDATE", "DELETE", "TRUNCATE", "REFERENCES", "TRIGGER"} {
				assert.False(t, hasTablePrivilege(t, ctx, conn, grantsZitadelRole, table, privilege), "%s on %s", privilege, table)
			}
			// Column privileges exist only for these kinds.
			for _, privilege := range []string{"SELECT", "INSERT", "UPDATE", "REFERENCES"} {
				assert.False(t, hasAnyColumnPrivilege(t, ctx, conn, grantsZitadelRole, table, privilege), "column %s on %s", privilege, table)
			}
		}
		assert.False(t, hasAnySequencePrivilege(t, ctx, conn, grantsZitadelRole), "sequence privilege")
	})

	t.Run("other service accounts keep full CRUD", func(t *testing.T) {
		for _, role := range grantsReadWriteRoles {
			for _, table := range tables {
				for _, privilege := range []string{"SELECT", "INSERT", "UPDATE", "DELETE"} {
					assert.True(t, hasTablePrivilege(t, ctx, conn, role, table, privilege), "%s: %s on %s", role, privilege, table)
				}
			}
		}
	})
}

// setupGrantsDatabase creates the fake IAM roles, empties the scratch
// database, applies every migration to it with atlas as the Atlas Operator
// does, creates the probe table, and returns a connection to it. Cleanup
// empties the scratch database again and drops the roles.
//
// The scratch database is reused rather than dropped: DROP DATABASE forces an
// immediate checkpoint, which can take minutes on a test cluster whose other
// tests TRUNCATE tables between cases. Dropping its schemas is cheap.
func setupGrantsDatabase(t *testing.T, ctx context.Context) *pgx.Conn {
	t.Helper()

	atlasPath, err := exec.LookPath("atlas")
	require.NoError(t, err, "atlas CLI is required to apply migrations")
	migrationsDir, err := filepath.Abs("../../../../k8s/atlas/base/migrations")
	require.NoError(t, err)

	var dbExists bool
	require.NoError(t, testDB.Pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname = $1)`, grantsTestDatabase).Scan(&dbExists))
	if !dbExists {
		_, err := testDB.Pool.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{grantsTestDatabase}.Sanitize())
		require.NoError(t, err)
	}
	dsn := fmt.Sprintf(grantsTestDSNFormat, grantsTestDatabase)
	conn, err := pgx.Connect(ctx, dsn)
	require.NoError(t, err)

	roles := append([]string{grantsOrganizerRole, grantsZitadelRole}, grantsReadWriteRoles...)
	t.Cleanup(func() {
		ctx := context.Background()
		resetGrantsDatabase(t, ctx, conn, roles)
		assert.NoError(t, conn.Close(ctx))
		for _, role := range roles {
			var exists bool
			require.NoError(t, testDB.Pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = $1)`, role).Scan(&exists))
			if !exists {
				continue
			}
			// DROP OWNED clears any privilege the role holds in the shared test
			// database, so DROP ROLE cannot fail on dependent objects.
			_, err := testDB.Pool.Exec(ctx, "DROP OWNED BY "+pgx.Identifier{role}.Sanitize())
			assert.NoError(t, err)
			_, err = testDB.Pool.Exec(ctx, "DROP ROLE "+pgx.Identifier{role}.Sanitize())
			assert.NoError(t, err)
		}
	})

	for _, role := range roles {
		var exists bool
		require.NoError(t, testDB.Pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = $1)`, role).Scan(&exists))
		if !exists {
			_, err := testDB.Pool.Exec(ctx, "CREATE ROLE "+pgx.Identifier{role}.Sanitize()+" NOLOGIN")
			require.NoError(t, err)
		}
	}
	// Start from an empty database even if an earlier run did not clean up.
	resetGrantsDatabase(t, ctx, conn, roles)

	out, err := exec.CommandContext(ctx, atlasPath, "migrate", "apply",
		"--url", dsn, "--dir", "file://"+migrationsDir).CombinedOutput()
	require.NoError(t, err, "atlas migrate apply: %s", out)

	_, err = conn.Exec(ctx, "CREATE TABLE app."+grantsProbeTable+" (id UUID PRIMARY KEY, note TEXT)")
	require.NoError(t, err)
	return conn
}

// resetGrantsDatabase revokes everything the roles hold in the scratch
// database and drops every schema the migrations create.
func resetGrantsDatabase(t *testing.T, ctx context.Context, conn *pgx.Conn, roles []string) {
	t.Helper()
	for _, role := range roles {
		var exists bool
		require.NoError(t, conn.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = $1)`, role).Scan(&exists))
		if exists {
			_, err := conn.Exec(ctx, "DROP OWNED BY "+pgx.Identifier{role}.Sanitize())
			require.NoError(t, err)
		}
	}
	_, err := conn.Exec(ctx, `
		DROP SCHEMA IF EXISTS app CASCADE;
		DROP SCHEMA IF EXISTS atlas_schema_revisions CASCADE;
		DROP SCHEMA IF EXISTS public CASCADE;
		CREATE SCHEMA public;`)
	require.NoError(t, err)
}

func listAppTables(t *testing.T, ctx context.Context, conn *pgx.Conn) []string {
	t.Helper()
	rows, err := conn.Query(ctx, `SELECT tablename FROM pg_tables WHERE schemaname = 'app' ORDER BY tablename`)
	require.NoError(t, err)
	tables, err := pgx.CollectRows(rows, pgx.RowTo[string])
	require.NoError(t, err)
	return tables
}

func listColumns(t *testing.T, ctx context.Context, conn *pgx.Conn, table string) []string {
	t.Helper()
	rows, err := conn.Query(ctx,
		`SELECT column_name FROM information_schema.columns WHERE table_schema = 'app' AND table_name = $1 ORDER BY ordinal_position`,
		table)
	require.NoError(t, err)
	columns, err := pgx.CollectRows(rows, pgx.RowTo[string])
	require.NoError(t, err)
	return columns
}

func hasSchemaUsage(t *testing.T, ctx context.Context, conn *pgx.Conn, role string) bool {
	t.Helper()
	var ok bool
	require.NoError(t, conn.QueryRow(ctx, `SELECT has_schema_privilege($1, 'app', 'USAGE')`, role).Scan(&ok))
	return ok
}

func hasTablePrivilege(t *testing.T, ctx context.Context, conn *pgx.Conn, role, table, privilege string) bool {
	t.Helper()
	var ok bool
	require.NoError(t, conn.QueryRow(ctx, `SELECT has_table_privilege($1, 'app.' || $2, $3)`, role, table, privilege).Scan(&ok))
	return ok
}

func hasAnyColumnPrivilege(t *testing.T, ctx context.Context, conn *pgx.Conn, role, table, privilege string) bool {
	t.Helper()
	var ok bool
	require.NoError(t, conn.QueryRow(ctx, `SELECT has_any_column_privilege($1, 'app.' || $2, $3)`, role, table, privilege).Scan(&ok))
	return ok
}

func hasColumnPrivilege(t *testing.T, ctx context.Context, conn *pgx.Conn, role, table, column, privilege string) bool {
	t.Helper()
	var ok bool
	require.NoError(t, conn.QueryRow(ctx, `SELECT has_column_privilege($1, 'app.' || $2, $3, $4)`, role, table, column, privilege).Scan(&ok))
	return ok
}

// hasAnySequencePrivilege reports whether role holds any privilege on any
// sequence in schema app, including sequences created after the migrations
// ran (none exist today, so the probe sequence checks the default privileges).
func hasAnySequencePrivilege(t *testing.T, ctx context.Context, conn *pgx.Conn, role string) bool {
	t.Helper()
	_, err := conn.Exec(ctx, `CREATE SEQUENCE IF NOT EXISTS app.grants_probe_seq`)
	require.NoError(t, err)
	var ok bool
	require.NoError(t, conn.QueryRow(ctx, `
		SELECT COALESCE(bool_or(
			has_sequence_privilege($1, 'app.' || sequencename, 'USAGE')
			OR has_sequence_privilege($1, 'app.' || sequencename, 'SELECT')
			OR has_sequence_privilege($1, 'app.' || sequencename, 'UPDATE')), false)
		FROM pg_sequences WHERE schemaname = 'app'`, role).Scan(&ok))
	return ok
}
