---
name: backend-workflow
description: Development workflows for backend - Docker build/deploy, CI/CD (GitHub Actions to GAR to ArgoCD), Go build/test, database migrations (Atlas), code generation (Wire, Buf). MUST READ before any build/deploy/test/migration tasks. Keywords - docker, build, deploy, deployment, CI/CD, GAR, push, image, go build, go test, atlas, migrate, migration, wire, buf, generate, proto, protobuf.
---

# Backend Development Workflows

**IMPORTANT**: This guide covers all development workflows. Read the appropriate section before executing commands.

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

## 5. Deployment Workflow

Backend deployment is **fully automated via GitHub Actions**.

### How It Works

1. **Merge to `main`**: Changes merged to the main branch trigger the deploy workflow
2. **Auto-build**: GitHub Actions builds a Docker image using the Dockerfile
3. **Auto-push**: Image is pushed to Google Artifact Registry (GAR) with tags:
   - `latest`
   - `<commit-sha>`
   - `main`
4. **Auto-deploy**: ArgoCD detects the new image and deploys to GKE

### Workflow File

`.github/workflows/deploy.yml` triggers on:
- Push to `main` branch
- Changes to `**.go`, `go.mod`, `go.sum`, `Dockerfile`, or the workflow file itself

### Manual Deployment (Development Only)

For testing changes before merging to main:

```bash
# Build image locally
docker build -t asia-northeast2-docker.pkg.dev/liverty-music-dev/backend/server:test .

# Authenticate to GAR (if needed)
gcloud auth configure-docker asia-northeast2-docker.pkg.dev

# Push to GAR
docker push asia-northeast2-docker.pkg.dev/liverty-music-dev/backend/server:test

# Update Kustomize to use test tag (in cloud-provisioning repo)
# k8s/namespaces/backend/overlays/dev/server/kustomization.yaml
#   images:
#   - name: server
#     newTag: test
```

**Note**: Standard workflow is to merge to `main` and let CI/CD handle deployment automatically.

## 6. Directory Reference

- `migrations/`: `internal/infrastructure/database/rdb/migrations/`
- `handlers/`: `internal/adapter/rpc/`
- `di/`: `internal/di/`
