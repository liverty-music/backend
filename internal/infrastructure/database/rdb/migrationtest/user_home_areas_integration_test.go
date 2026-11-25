package migrationtest

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	// userHomeAreasPrevious is the last migration before
	// 20261125011819_add_user_home_areas.sql.
	userHomeAreasPrevious = "20261125010231"
	// userHomeAreasVersion is 20261125011819_add_user_home_areas.sql.
	userHomeAreasVersion = "20261125011819"
)

// TestMigration_UserHomeAreas loads 11 homes as production had them (7
// referenced by a user, 4 left behind by deleted users) plus a user whose
// home is CN-TW, a code with no Area, applies
// 20261125011819_add_user_home_areas.sql and checks that the 7 referenced
// homes moved, the orphans stayed behind, the CN-TW home was not copied, and
// that deleting a user deletes its home.
func TestMigration_UserHomeAreas(t *testing.T) {
	ctx := context.Background()
	conn, apply := setupScratchDatabase(t, ctx)
	apply(userHomeAreasPrevious)

	mustExec := func(sql string, args ...any) {
		t.Helper()
		_, err := conn.Exec(ctx, sql, args...)
		require.NoError(t, err, sql)
	}
	// The reference rows the copy needs; production has the full seed.
	mustExec(`INSERT INTO areas (code, kind, country_code, name_en) VALUES
		('JP-13', 'subdivision', 'JP', 'Tokyo'), ('JP-27', 'subdivision', 'JP', 'Osaka'), ('JP-40', 'subdivision', 'JP', 'Fukuoka')`)

	levels := []string{"JP-13", "JP-27", "JP-40", "JP-13", "JP-27", "JP-13", "JP-40"}
	var users []string
	for i, level := range levels {
		home, user := id(100+i), id(200+i)
		mustExec(`INSERT INTO homes (id, country_code, level_1) VALUES ($1, 'JP', $2)`, home, level)
		mustExec(`INSERT INTO users (id, provider_user_id, email, name, preferred_language, home_id) VALUES ($1, $2, $3, 'fan', 'ja', $4)`,
			user, fmt.Sprintf("p%d", i), fmt.Sprintf("fan%d@example.com", i), home)
		users = append(users, user)
	}
	for i := range 4 {
		mustExec(`INSERT INTO homes (id, country_code, level_1) VALUES ($1, 'JP', 'JP-13')`, id(300+i))
	}
	taiwanHome, taiwanUser := id(400), id(401)
	mustExec(`INSERT INTO homes (id, country_code, level_1) VALUES ($1, 'CN', 'CN-TW')`, taiwanHome)
	mustExec(`INSERT INTO users (id, provider_user_id, email, name, preferred_language, home_id) VALUES ($1, 'ptw', 'tw@example.com', 'fan', 'ja', $2)`,
		taiwanUser, taiwanHome)

	apply(userHomeAreasVersion)

	t.Run("the referenced homes moved", func(t *testing.T) {
		for i, user := range users {
			var code string
			require.NoError(t, conn.QueryRow(ctx, `SELECT area_code FROM user_home_areas WHERE user_id = $1`, user).Scan(&code))
			assert.Equal(t, levels[i], code)
		}
		var count int
		require.NoError(t, conn.QueryRow(ctx, `SELECT count(*) FROM user_home_areas`).Scan(&count))
		assert.Equal(t, 7, count, "the 4 orphans and the CN-TW home are not copied")
	})
	t.Run("the orphans stay in homes", func(t *testing.T) {
		var count int
		require.NoError(t, conn.QueryRow(ctx, `SELECT count(*) FROM homes`).Scan(&count))
		assert.Equal(t, 12, count)
	})
	t.Run("a home with an omitted code is not copied", func(t *testing.T) {
		var exists bool
		require.NoError(t, conn.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM user_home_areas WHERE user_id = $1)`, taiwanUser).Scan(&exists))
		assert.False(t, exists)
	})
	t.Run("a home goes with its user", func(t *testing.T) {
		mustExec(`DELETE FROM users WHERE id = $1`, users[0])
		var exists bool
		require.NoError(t, conn.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM user_home_areas WHERE user_id = $1)`, users[0]).Scan(&exists))
		assert.False(t, exists)
	})
	t.Run("only a first-level Area can be a home", func(t *testing.T) {
		_, err := conn.Exec(ctx, `INSERT INTO user_home_areas (user_id, area_code) VALUES ($1, 'JP-99')`, taiwanUser)
		assert.Error(t, err)
	})
}
