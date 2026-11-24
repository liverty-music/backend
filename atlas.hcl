// Define the "local" environment for development.
env "local" {
  // The actual database to apply migrations to, matching compose.yml. Its
  // search_path is the operator's (k8s/atlas/base/atlas-migration.yaml), so
  // every table lands in app as in production, and Atlas keeps its revision
  // table in app.atlas_schema_revisions.
  url = "postgres://test-user@localhost:15432/test-db?sslmode=disable&search_path=app"

  // A temporary database for schema analysis (diffing, validating), scoped to
  // schema app with search_path=app, so the replay puts every table in app as
  // production does and Atlas writes unqualified names. The schema must exist
  // before Atlas reads it, which its docker:// driver cannot arrange without
  // `atlas login`; scripts/atlas-dev.sh starts a fresh container, creates
  // schema app and sets ATLAS_DEV_URL. Run `migrate diff` and `migrate
  // validate` through it:
  //
  //   scripts/atlas-dev.sh atlas migrate diff --env local <name>
  //
  // `migrate apply`, `migrate hash` and `migrate new` need no dev database.
  dev = getenv("ATLAS_DEV_URL")

  // This is the "desired state" of the database, generated from models.
  schema {
    src = "file://internal/infrastructure/database/rdb/schema/schema.sql"
  }

  // This represents the "current state" of the database, built from versioned files.
  migration {
    dir = "file://k8s/atlas/base/migrations"
  }

  // Schema `dbos` holds the DBOS Transact system tables. Its migrations are
  // the SQL DBOS prints (k8s/atlas/base/migrations/*_dbos_*.sql), not a diff
  // of schema.sql, so schema.sql leaves it out; without this, every
  // `migrate diff` would end with DROP SCHEMA "dbos" CASCADE.
  diff {
    skip {
      drop_schema = true
    }
  }
}

// Define the "ci" environment for continuous integration.
env "ci" {
  // In CI, the dev database is the service container defined in the workflow.
  dev = env("DATABASE_URL")

  // This is the "desired state" of the database, generated from models.
  schema {
    src = "file://internal/infrastructure/database/rdb/schema/schema.sql"
  }
}
