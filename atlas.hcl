// Define the "local" environment for development.
env "local" {
  // The actual database to apply migrations to, matching compose.yml.
  url = "postgres://test-user@localhost:5432/test-db?sslmode=disable"

  // A temporary database for schema analysis (diffing, linting).
  // Use a temporary database container for schema analysis to ensure a clean environment.
  dev = "docker://postgres/18/postgres?search_path=public"

  // This is the "desired state" of the database, generated from models.
  schema {
    src = "file://internal/infrastructure/database/rdb/migrations/schema.sql"
  }

  // This represents the "current state" of the database, built from versioned files.
  migration {
    dir = "file://internal/infrastructure/database/rdb/migrations/versions"
  }
}

// Define the "ci" environment for continuous integration.
env "ci" {
  // In CI, the dev database is the service container defined in the workflow.
  dev = env("DATABASE_URL")

  // This is the "desired state" of the database, generated from models.
  schema {
    src = "file://internal/infrastructure/database/rdb/migrations/schema.sql"
  }
}
