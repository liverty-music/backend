# Tasks: Cloud SQL Connector

## 1. Dependencies and Configuration

- [x] 1.1 Add `cloud.google.com/go/cloudsqlconn` dependency.
- [x] 1.2 Update `pkg/config/config.go`: Add `InstanceConnectionName` (string) to `DatabaseConfig`.
- [x] 1.3 Update `pkg/config/config.go`: Add `local` as valid environment default.
- [x] 1.3 Update `pkg/config/config.go`: Remove `Password` field entirely.

## 2. Core Implementation

- [x] 2.1 Update `internal/infrastructure/database/rdb/postgres.go`: Modify `New` to accept `context.Context` (already does).
- [x] 2.2 Update `internal/infrastructure/database/rdb/postgres.go`: Implement `cloudsqlconn` with `WithPSC` and remove password usage.
- [x] 2.3 Update `internal/infrastructure/database/rdb/postgres.go`: Configure `pgxpool` to use the dialer.
- [x] 2.4 Update `internal/infrastructure/database/rdb/postgres.go`: Ensure dialer is closed on shutdown.

## 3. Cleanup and Verification

- [x] 3.1 Verify local connection using `Environment=local` (standard TCP).
- [x] 3.2 Verify configuration validation.
