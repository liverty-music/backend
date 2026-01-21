<!-- OPENSPEC:START -->

# OpenSpec Instructions

These instructions are for AI assistants working in this project.

Always open `@/openspec/AGENTS.md` when the request:

- Mentions planning or proposals (words like proposal, spec, change, plan)
- Introduces new capabilities, breaking changes, architecture shifts, or big performance/security work
- Sounds ambiguous and you need the authoritative spec before coding

Use `@/openspec/AGENTS.md` to learn:

- How to create and apply change proposals
- Spec format and conventions
- Project structure and guidelines

Keep this managed block so 'openspec update' can refresh the instructions.

<!-- OPENSPEC:END -->

## Development Cheatsheet

### Running the Application

```bash
# Start the server (HTTP on :8080, gRPC/Connect on :9090)
go run cmd/api/main.go
```

### Testing

```bash
# Run all tests
go test ./...

# Run tests for a specific package
# Run tests for a specific package
go test ./pkg/config
```

### Linting

```bash
# Run linters
golangci-lint run ./...
```

### Code Generation

```bash
# Generate Wire dependency injection code (when wire.go is modified)
wire internal/di/

# Generate protobuf code (if working with proto files)
buf generate

# Generate database schema from Bun models
go run internal/infrastructure/database/rdb/migrations/generate_schema.go
```

### Database Migrations (Atlas)

```bash
# Generate migration from schema changes
atlas migrate diff --env local

# Validate migrations
atlas migrate validate --env local

# Apply migrations (for local development only)
atlas migrate apply --env local
```

### API Testing (buf curl)

```bash
# Test GetUser endpoint
buf curl --schema buf.build/liverty-music/schema --protocol connect \
  -d '{"user_id": {"value": "123"}}' \
  http://localhost:9090/liverty.api.v1.UserService/GetUser

# Health Check
buf curl --schema buf.build/grpc/health --protocol connect \
  -d '{"service": ""}' \
  http://localhost:9090/grpc.health.v1.Health/Check
```

## Service Implementation Reference

### Connect-RPC Handlers (`internal/adapter/rpc/`)

- Implement generated service interfaces.
- **User Service**: `user_handler.go`
- **Artist Service**: `artist_handler.go`
- **Concert Service**: `concert_handler.go`
- **Notification Service**: `notification_handler.go`
- **Health Check**: `health_handler.go` (`/grpc.health.v1.Health/`)

### Database Integration (`internal/infrastructure/database/rdb/`)

- **Bun ORM** with PostgreSQL.
- **Migrations**: `internal/infrastructure/database/rdb/migrations/`.
- **Command**: `mise run migrate <migration_name>` (Recommended over raw Atlas commands for creating migrations).

### Telemetry & Logging

- **Tracing**: Automatic for Connect-RPC.
- **Logging**: Use `pkg/logging` with context.
  ```go
  logger.Info(ctx, "User logged in", slog.String("user_id", "123"))
  ```
- **Configuration**:
  - `APP_TELEMETRY_OTLP_ENDPOINT`: Exporter URL.
  - `APP_TELEMETRY_SERVICE_NAME`: Service name.
