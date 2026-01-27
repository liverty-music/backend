---
name: backend-workflow
description: Standard development workflows for Liverty Music Backend (Build, Test, Migrate, Gen).
---

# Backend Development Workflows

Reference guide for common development tasks in the `liverty-music/backend` repository.

## 1. Daily Development

Standard commands for building and verifying code.

```bash
# Run the application (HTTP :8080, gRPC :9090)
go run cmd/api/main.go

# Run all tests
go test ./...

# Run tests for a specific package
go test ./pkg/config

# Run static analysis (Linting & Vetting)
go vet ./...
golangci-lint run ./...
```

## 2. Code Generation

Run these commands when modifying source definitions.

```bash
# Generate Wire dependency injection code (Run when wire.go modified)
wire internal/di/

# Generate protobuf code (Run when .proto files in schema repo change)
buf generate
```

## 3. Database Operations (Atlas)

Manage PostgreSQL schema migrations using Atlas.

```bash
# Generate migration from schema definition (schema.sql -> DB state diff)
# Note: Ensure local DB is running.
atlas migrate diff --env local <migration_name>

# Validate migrations
atlas migrate validate --env local

# Apply migrations (Local development only)
atlas migrate apply --env local
```

## 4. API Verification (buf curl)

Test Connect-RPC endpoints directly.

```bash
# GetUser Example
buf curl --schema buf.build/liverty-music/schema --protocol connect \
  -d '{"user_id": {"value": "123"}}' \
  http://localhost:9090/liverty.api.v1.UserService/GetUser

# Health Check
buf curl --schema buf.build/grpc/health --protocol connect \
  -d '{"service": ""}' \
  http://localhost:9090/grpc.health.v1.Health/Check
```

## 5. Directory Reference

- `migrations/`: `internal/infrastructure/database/rdb/migrations/`
- `handlers/`: `internal/adapter/rpc/`
- `di/`: `internal/di/`
