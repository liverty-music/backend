package rdb_test

import (
	"context"
	"fmt"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
	"github.com/liverty-music/backend/internal/testutil"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"uuid"
)

// Fake Cloud SQL IAM service-account roles. Cloud SQL names them
// `<name>@<project>.iam`, so they match the role patterns the grant
// migrations loop over. Roles belong to the whole server, and a role that
// another run left with privileges in some database can never be dropped;
// the project part is new on every run, so such a role never blocks this
// test.
var (
	grantsRoleProject   = "test-" + uuid.NewV7().String()[24:] + ".iam"
	grantsOrganizerRole = "organizer-console-api@" + grantsRoleProject
	grantsZitadelRole   = "zitadel@" + grantsRoleProject
	grantsReceptionRole = "reception-api@" + grantsRoleProject

	// grantsRetiredMediaConsumerRole is the IAM user of the retired
	// media-consumer worker. Its grants were revoked so Cloud SQL can drop it.
	grantsRetiredMediaConsumerRole = "media-consumer@" + grantsRoleProject

	// grantsAdminRole, grantsBackendRole and grantsFanRole keep full CRUD on
	// the app schema and reach the workflow tables.
	grantsAdminRole   = "admin-console-api@" + grantsRoleProject
	grantsBackendRole = "backend-app@" + grantsRoleProject
	grantsFanRole     = "fan-api@" + grantsRoleProject
)

const (
	// grantsProbeTable is created after every migration has run, to check the
	// default privileges a future table inherits.
	grantsProbeTable = "grants_probe"

	// grantsTestDSNFormat targets the integration-test PostgreSQL as the
	// superuser test-user. search_path=app matches the Atlas Operator
	// connection (k8s/atlas/base/atlas-migration.yaml), so every table lands in
	// schema app as in production.
	grantsTestDSNFormat = "postgres://test-user@localhost:%d/%s?sslmode=disable&search_path=app"
)

// grantsReadWriteRoles keep full CRUD on the app schema.
var grantsReadWriteRoles = []string{
	grantsAdminRole,
	grantsBackendRole,
	grantsFanRole,
	"series-image-consumer@" + grantsRoleProject,
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
// 20261008020000_restrict_organizer_console_api_and_zitadel_app_grants.sql,
// 20261009120000_grant_reception_api_and_restrict_organizer_console_api.sql,
// 20261010010000_grant_organizer_console_api_ticket_sales.sql,
// 20261011000000_unify_ticket_sales.sql and
// 20261013000100_grant_organizer_console_api_event_lifecycle.sql;
// 20261012000000_one_name_per_concept.sql and
// 20261012220000_rename_reception_links_to_scanners.sql rename the tables and
// columns, and the privileges follow them; 20261013000000_event_publish_state.sql
// drops the draft tables and the series state columns with their privileges;
// 20261029015006_remodel_ticket_sales.sql drops ticket_types with its
// privileges and grants the seat type, ticket stock and sale event writes).
// Any other app table is SELECT-only for the role.
var organizerWrites = map[string]organizerTableWrites{
	"series": {insert: true, delete: true, updateColumns: []string{
		"title", "type", "source_url", "description", "visibility", "unlisted_token",
	}},
	"venues": {insert: true},
	"events": {insert: true, delete: true, updateColumns: []string{
		"series_id", "publish_state", "venue_id", "listed_venue_name", "local_event_date", "start_at", "open_at", "per_account_limit",
	}},
	"concert_artists":    {insert: true, delete: true},
	"ticket_journeys":    {insert: true},
	"concerts":           {insert: true},
	"concert_candidates": {delete: true},
	"series_images":      {insert: true},
	"payout_accounts":    {insert: true, updateColumns: []string{"provider_account_id", "status", "status_synced_at"}},
	"scanners":           {insert: true, updateColumns: []string{"status", "link_token", "revoked_at"}},
	"ticket_sales":       {insert: true, updateColumns: []string{"sale_start_at", "sale_end_at", "verification_requirement"}},
	"seat_types":         {insert: true, delete: true},
	"ticket_stocks":      {insert: true},
	"ticket_sale_events": {insert: true},
	// 20261030130100_grant_events_outbox_inserts.sql: announcements of
	// SeriesImage.Create, Series.PublishEvents and Organizer.AssociateArtist.
	"watermill_events_outbox": {insert: true},
}

// receptionTableAccess is the access of the reception-api role to one app
// table. updateColumns lists the only columns it may UPDATE; table-level
// UPDATE is never granted.
type receptionTableAccess struct {
	selects       bool
	insert        bool
	updateColumns []string
}

// receptionAccess is every read and write the reception server performs (see
// 20261009120000_grant_reception_api_and_restrict_organizer_console_api.sql;
// 20261012220000_rename_reception_links_to_scanners.sql renames reception_links
// to scanners and token to link_token, and the privileges follow them).
// The role has no privilege on any other app table.
var receptionAccess = map[string]receptionTableAccess{
	"scanners":           {selects: true, updateColumns: []string{"status", "bound_public_key", "bound_at", "link_token"}},
	"events":             {selects: true},
	"wallet_public_keys": {selects: true},
	"tickets":            {selects: true, updateColumns: []string{"admit_at"}},
	"admissions":         {selects: true, insert: true},
	// 20261030130100_grant_events_outbox_inserts.sql: the announcement of Ticket.Admit.
	"watermill_events_outbox": {insert: true},
	"rejected_scans":          {insert: true},
}

// TestMigrationGrants_IAMRoles applies every Atlas migration to a scratch
// database in which the IAM service-account roles already exist, then checks
// the privileges each role ends up with on schema app. It fails when a
// migration (for example a generic `%@%.iam` grant loop) widens the
// organizer-console-api write set or the reception-api access, or gives
// zitadel or the retired media-consumer any app privilege.
func TestMigrationGrants_IAMRoles(t *testing.T) {
	// Parallel with the other tests: its roles and its database are named
	// for this run, and it holds the migration lock while its roles exist.
	t.Parallel()
	db := newTestDB(t)
	ctx := context.Background()
	conn := setupGrantsDatabase(t, db, ctx)

	tables := listAppTables(t, ctx, conn)
	require.Contains(t, tables, grantsProbeTable)
	for table := range organizerWrites {
		require.Contains(t, tables, table, "organizer write table missing from schema app")
	}
	for table := range receptionAccess {
		require.Contains(t, tables, table, "reception table missing from schema app")
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
		for _, table := range []string{"admissions", "rejected_scans", "orders", "users", "identity_verifications"} {
			assert.False(t, hasAnyColumnPrivilege(t, ctx, conn, grantsOrganizerRole, table, "UPDATE"), "UPDATE on %s", table)
			assert.False(t, hasTablePrivilege(t, ctx, conn, grantsOrganizerRole, table, "DELETE"), "DELETE on %s", table)
		}
	})

	t.Run("only organizer-console-api executes delete_ticket_stock", func(t *testing.T) {
		assert.True(t, canExecute(t, ctx, conn, grantsOrganizerRole, "app.delete_ticket_stock(uuid)"))
		for _, role := range append([]string{grantsZitadelRole, grantsReceptionRole}, grantsReadWriteRoles...) {
			assert.False(t, canExecute(t, ctx, conn, role, "app.delete_ticket_stock(uuid)"), "EXECUTE by %s", role)
		}
	})

	t.Run("reception-api holds only the reception reads and writes", func(t *testing.T) {
		assert.True(t, hasSchemaUsage(t, ctx, conn, grantsReceptionRole))
		for _, table := range tables {
			want := receptionAccess[table]
			assert.Equal(t, want.selects, hasTablePrivilege(t, ctx, conn, grantsReceptionRole, table, "SELECT"), "SELECT on %s", table)
			assert.Equal(t, want.insert, hasTablePrivilege(t, ctx, conn, grantsReceptionRole, table, "INSERT"), "INSERT on %s", table)
			for _, privilege := range []string{"UPDATE", "DELETE", "TRUNCATE", "REFERENCES", "TRIGGER"} {
				assert.False(t, hasTablePrivilege(t, ctx, conn, grantsReceptionRole, table, privilege), "table-level %s on %s", privilege, table)
			}
			for _, column := range listColumns(t, ctx, conn, table) {
				assert.Equal(t, slices.Contains(want.updateColumns, column),
					hasColumnPrivilege(t, ctx, conn, grantsReceptionRole, table, column, "UPDATE"),
					"UPDATE on %s.%s", table, column)
			}
		}
		assert.False(t, hasAnySequencePrivilege(t, ctx, conn, grantsReceptionRole), "sequence privilege")
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

	t.Run("retired media-consumer holds nothing that blocks dropping it", func(t *testing.T) {
		role := grantsRetiredMediaConsumerRole
		assert.False(t, hasSchemaUsage(t, ctx, conn, role), "USAGE on schema app")
		for _, table := range tables {
			for _, privilege := range []string{"SELECT", "INSERT", "UPDATE", "DELETE", "TRUNCATE", "REFERENCES", "TRIGGER"} {
				assert.False(t, hasTablePrivilege(t, ctx, conn, role, table, privilege), "%s on %s", privilege, table)
			}
		}
		assert.False(t, hasAnySequencePrivilege(t, ctx, conn, role), "sequence privilege")
		// DROP ROLE fails while pg_shdepend records any privilege, default
		// privilege or ownership of the role in a database.
		var dependents int
		require.NoError(t, conn.QueryRow(ctx, `
			SELECT count(*) FROM pg_shdepend
			WHERE refclassid = 'pg_authid'::regclass
			  AND refobjid = (SELECT oid FROM pg_roles WHERE rolname = $1)
			  AND dbid = (SELECT oid FROM pg_database WHERE datname = current_database())`, role).Scan(&dependents))
		assert.Zero(t, dependents, "objects in the database depend on %s", role)
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

	// fan-api runs and starts workflows; backend-app (the event consumer and
	// the jobs) and admin-console-api start them
	// (20261031090000_grant_dbos_to_backend_app_and_admin_console_api.sql).
	t.Run("only fan-api, backend-app and admin-console-api reach the workflow tables in schema dbos", func(t *testing.T) {
		for _, role := range append([]string{grantsOrganizerRole, grantsZitadelRole, grantsReceptionRole, grantsRetiredMediaConsumerRole}, grantsReadWriteRoles...) {
			want := role == grantsFanRole || role == grantsBackendRole || role == grantsAdminRole
			assert.Equal(t, want, hasSchemaPrivilege(t, ctx, conn, role, "dbos", "USAGE"), "USAGE on schema dbos for %s", role)
			assert.Equal(t, want, hasTablePrivilegeIn(t, ctx, conn, role, "dbos", "workflow_status", "SELECT"), "SELECT on dbos.workflow_status for %s", role)
		}
	})
}

// setupGrantsDatabase creates the fake IAM roles and an empty database of the
// test's own, applies every migration to it with atlas as the Atlas Operator
// does, creates the probe table, and returns a connection to it. Cleanup
// drops the database and then the roles: dropping the database removes every
// privilege the roles hold, and no other migration runs while they exist
// (see [testutil.LockMigrations]), so DROP ROLE cannot fail on dependent
// objects.
//
// The database is not a copy of the template: the grant migrations must run
// while the roles exist, as they do in production.
func setupGrantsDatabase(t *testing.T, db *rdb.Database, ctx context.Context) *pgx.Conn {
	t.Helper()

	atlasPath, err := exec.LookPath("atlas")
	require.NoError(t, err, "atlas CLI is required to apply migrations")
	migrationsDir, err := filepath.Abs("../../../../k8s/atlas/base/migrations")
	require.NoError(t, err)

	// Released after the roles are dropped: cleanups run last first.
	testutil.LockMigrations(t)
	name := "grants-" + uuid.NewV7().String()[24:]
	_, err = db.Pool.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize())
	require.NoError(t, err)
	dsn := fmt.Sprintf(grantsTestDSNFormat, testutil.DatabasePort(), name)
	conn, err := pgx.Connect(ctx, dsn)
	require.NoError(t, err)

	roles := append([]string{grantsOrganizerRole, grantsZitadelRole, grantsReceptionRole, grantsRetiredMediaConsumerRole}, grantsReadWriteRoles...)
	t.Cleanup(func() {
		ctx := context.Background()
		assert.NoError(t, conn.Close(ctx))
		_, err := db.Pool.Exec(ctx, "DROP DATABASE "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)")
		assert.NoError(t, err)
		for _, role := range roles {
			_, err := db.Pool.Exec(ctx, "DROP ROLE IF EXISTS "+pgx.Identifier{role}.Sanitize())
			assert.NoError(t, err)
		}
	})

	for _, role := range roles {
		_, err := db.Pool.Exec(ctx, "CREATE ROLE "+pgx.Identifier{role}.Sanitize()+" NOLOGIN")
		require.NoError(t, err)
	}

	out, err := exec.CommandContext(ctx, atlasPath, "migrate", "apply",
		"--url", dsn, "--dir", "file://"+migrationsDir).CombinedOutput()
	require.NoError(t, err, "atlas migrate apply: %s", out)

	_, err = conn.Exec(ctx, "CREATE TABLE app."+grantsProbeTable+" (id UUID PRIMARY KEY, note TEXT)")
	require.NoError(t, err)
	return conn
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

func hasSchemaPrivilege(t *testing.T, ctx context.Context, conn *pgx.Conn, role, schema, privilege string) bool {
	t.Helper()
	var ok bool
	require.NoError(t, conn.QueryRow(ctx, `SELECT has_schema_privilege($1, $2, $3)`, role, schema, privilege).Scan(&ok))
	return ok
}

func hasTablePrivilegeIn(t *testing.T, ctx context.Context, conn *pgx.Conn, role, schema, table, privilege string) bool {
	t.Helper()
	var ok bool
	require.NoError(t, conn.QueryRow(ctx, `SELECT has_table_privilege($1, $2 || '.' || $3, $4)`, role, schema, table, privilege).Scan(&ok))
	return ok
}

func canExecute(t *testing.T, ctx context.Context, conn *pgx.Conn, role, function string) bool {
	t.Helper()
	var ok bool
	require.NoError(t, conn.QueryRow(ctx, `SELECT has_function_privilege($1, $2, 'EXECUTE')`, role, function).Scan(&ok))
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
