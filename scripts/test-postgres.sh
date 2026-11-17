#!/usr/bin/env bash
# Manages the integration-test PostgreSQL that every checkout of this
# repository shares (compose.yml: project liverty-music-backend, container
# "postgres", port 15432).
#
#   test-postgres.sh up     start the server if it is not running, never
#                           recreate it, and fail when its settings differ
#                           from compose.yml
#   test-postgres.sh clean  drop the test databases nobody has used for a day
#
# With TEST_DB_PORT set, the tests use the server on that port, which the
# caller runs; `up` does nothing then and `clean` works on that server.
set -euo pipefail

cd "$(dirname "$0")/.."

readonly USER_NAME=test-user
# The advisory lock of testutil.LockMigrations (migrationLockKey): holding it
# keeps `clean` from dropping a template while a test builds one.
readonly MIGRATION_LOCK_KEY=7105336001

run_psql() {
  if [[ -n "${TEST_DB_PORT:-}" ]]; then
    psql -X -h localhost -p "$TEST_DB_PORT" -U "$USER_NAME" -d postgres "$@"
  else
    docker compose exec -T postgres psql -X -p 15432 -U "$USER_NAME" -d postgres "$@"
  fi
}

up() {
  if [[ -n "${TEST_DB_PORT:-}" ]]; then
    echo "TEST_DB_PORT=$TEST_DB_PORT is set: using the PostgreSQL you run there; compose is not started."
    return
  fi
  # --no-recreate: another checkout may be running tests on this container,
  # and recreating it (which compose does when the options differ) kills them.
  docker compose up -d --no-recreate postgres --wait

  # Every `-c name=value` of the server's command in compose.yml must be what
  # the running server reports.
  local mismatch=0 setting name want got
  while read -r setting; do
    name=${setting%%=*}
    want=${setting#*=}
    got=$(run_psql -Atc "SHOW $name" </dev/null)
    if [[ "$got" != "$want" ]]; then
      echo "test-postgres: the running server has $name=$got, compose.yml says $want." >&2
      mismatch=1
    fi
  done < <(grep -oE '"-c", "[a-z_]+=[^"]+"' compose.yml | sed -E 's/"-c", "(.*)"/\1/')
  if ((mismatch)); then
    cat >&2 <<'MSG'
test-postgres: the shared test PostgreSQL was created with other settings than this checkout's compose.yml,
and make test does not recreate it because other checkouts may be running tests on it.
Either update this checkout (git merge origin/main) if it is behind, or, when no test is running anywhere, recreate it:
  docker compose up -d --force-recreate postgres --wait
MSG
    return 1
  fi
}

clean() {
  # DRY_RUN=1 prints the statements instead of running them.
  local EXEC_META='\gexec'
  [[ -z "${DRY_RUN:-}" ]] || EXEC_META=';'
  # Databases are dropped only when they have no session. Templates and the
  # copies of failed tests (kept for inspection) are named testdb_tpl_<hash>
  # and testdb_tpl_<hash>_inst_<id>; the creation time is the modification
  # time of the database's PG_VERSION file. A template is stale when it and
  # all its copies are older than a day; it is rebuilt by the next test.
  # The advisory lock is held for the whole session, so a template that a
  # test is building right now is never dropped.
  run_psql -q -v ON_ERROR_STOP=1 -At <<SQL
SELECT pg_advisory_lock($MIGRATION_LOCK_KEY);
CREATE TEMP VIEW test_dbs AS
  SELECT d.datname,
         (pg_stat_file('base/' || d.oid || '/PG_VERSION')).modification AS created,
         (SELECT count(*) FROM pg_stat_activity a WHERE a.datname = d.datname) AS sessions
  FROM pg_database d
  WHERE d.datname LIKE 'testdb\_tpl\_%';
SELECT stmt FROM (
  -- copies of failed tests
  SELECT 1 AS step, datname, format('DROP DATABASE %I', datname) AS stmt FROM test_dbs c
    WHERE c.datname LIKE '%\_inst\_%' AND c.created < now() - interval '1 day' AND c.sessions = 0
  UNION ALL
  -- stale templates: a template is flagged datistemplate, which must be
  -- cleared before it can be dropped
  SELECT 2, datname, format('ALTER DATABASE %I WITH is_template false', datname) FROM test_dbs t
    WHERE t.datname NOT LIKE '%\_inst\_%' AND t.sessions = 0 AND t.created < now() - interval '1 day'
      AND NOT EXISTS (SELECT 1 FROM test_dbs c WHERE c.datname LIKE t.datname || '\_inst\_%'
                      AND (c.sessions > 0 OR c.created >= now() - interval '1 day'))
  UNION ALL
  SELECT 3, datname, format('DROP DATABASE %I', datname) FROM test_dbs t
    WHERE t.datname NOT LIKE '%\_inst\_%' AND t.sessions = 0 AND t.created < now() - interval '1 day'
      AND NOT EXISTS (SELECT 1 FROM test_dbs c WHERE c.datname LIKE t.datname || '\_inst\_%'
                      AND (c.sessions > 0 OR c.created >= now() - interval '1 day'))
) s ORDER BY step, datname
${EXEC_META}
SQL
}

case "${1:-}" in
  up) up ;;
  clean) clean ;;
  *) echo "usage: $0 up|clean" >&2; exit 2 ;;
esac
