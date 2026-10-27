package workflow_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/dbos-inc/dbos-transact-golang/dbos"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const migrationsDir = "../../../k8s/atlas/base/migrations"

var (
	beginMarker   = regexp.MustCompile(`^-- BEGIN dbos migrate --print-migrations (\d+)$`)
	migrationLine = regexp.MustCompile(`^-- Migration (\d+)$`)
)

// dbosMigration is the DBOS SQL one Atlas migration file carries.
type dbosMigration struct {
	file string
	from int
	body []string
}

// readDBOSMigrations returns the DBOS SQL of every Atlas migration, in the
// order Atlas applies them.
func readDBOSMigrations(t *testing.T) []dbosMigration {
	t.Helper()

	files, err := filepath.Glob(filepath.Join(migrationsDir, "*.sql"))
	require.NoError(t, err)

	var migrations []dbosMigration
	for _, file := range files {
		content, err := os.ReadFile(file)
		require.NoError(t, err)

		var m *dbosMigration
		for line := range strings.SplitSeq(string(content), "\n") {
			if m == nil {
				if match := beginMarker.FindStringSubmatch(line); match != nil {
					from, err := strconv.Atoi(match[1])
					require.NoError(t, err)
					m = &dbosMigration{file: filepath.Base(file), from: from}
				}
				continue
			}
			if line == "-- END dbos migrate --print-migrations "+strconv.Itoa(m.from) {
				migrations = append(migrations, *m)
				m = nil
				break
			}
			m.body = append(m.body, line)
		}
		require.Nil(t, m, "%s: BEGIN marker without its END marker", filepath.Base(file))
	}
	return migrations
}

// versions returns the DBOS migration numbers named in lines.
func versions(lines []string) []int {
	var vs []int
	for _, line := range lines {
		if match := migrationLine.FindStringSubmatch(line); match != nil {
			v, _ := strconv.Atoi(match[1])
			vs = append(vs, v)
		}
	}
	return vs
}

// TestDBOSMigrationsMatchModule fails when the pinned DBOS module and the
// committed Atlas migrations disagree, most often because the module was
// bumped without the migration its new system tables need (OpenSpec change
// introduce-durable-workflows, design "Placement, migrations and connections").
func TestDBOSMigrationsMatchModule(t *testing.T) {
	t.Parallel()

	migrations := readDBOSMigrations(t)
	require.NotEmpty(t, migrations, "no Atlas migration carries the DBOS system tables")
	assert.Equal(t, 1, migrations[0].from, "%s: the first DBOS migration must hold --print-migrations 1", migrations[0].file)

	var committed []string
	for _, m := range migrations {
		committed = append(committed, m.body...)
	}

	want, err := dbos.MigrationStatements("dbos", 1)
	require.NoError(t, err)
	wantLines := strings.Split(strings.Join(want, "\n"), "\n")

	if !assert.Equal(t, wantLines, committed) {
		have := map[int]bool{}
		for _, v := range versions(committed) {
			have[v] = true
		}
		for _, v := range versions(wantLines) {
			if !have[v] {
				t.Logf("the module has DBOS migration %d and later, which no Atlas migration holds: add one with the output of "+
					"`go run github.com/dbos-inc/dbos-transact-golang/cmd/dbos@<version in go.mod> migrate --print-migrations %d` "+
					"between the BEGIN and END markers (backend/AGENTS.md, \"Durable workflows\")", v, v)
				break
			}
		}
	}
}

// TestDBOSGrantsMatchModule checks that the first DBOS migration grants every
// Cloud SQL IAM user what `dbos migrate --print-user-role` grants.
func TestDBOSGrantsMatchModule(t *testing.T) {
	t.Parallel()

	migrations := readDBOSMigrations(t)
	require.NotEmpty(t, migrations)
	content, err := os.ReadFile(filepath.Join(migrationsDir, migrations[0].file))
	require.NoError(t, err)

	for _, stmt := range dbos.PermissionStatements("dbos", "ROLE") {
		granted := "EXECUTE format('" + strings.Replace(stmt, `"ROLE"`, "%I", 1) + "', iam_role);"
		assert.Contains(t, string(content), granted)
	}
}
