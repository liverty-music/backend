---
name: backend-workflow
description: Project-specific development workflows - Deployment via GitHub Actions to GAR to ArgoCD, Atlas database migrations. Use when deploying, building Docker images, or managing database schema.
---

# Backend Workflows

**Use this skill for**: Deployment, Docker builds, Database migrations

## 1. Deployment Workflow

Backend deployment is **fully automated via GitHub Actions**.

### Standard Workflow

1. **Merge to `main`**: Changes merged to main trigger the deploy workflow
2. **Auto-build**: GitHub Actions builds Docker image using Dockerfile
3. **Auto-push**: Image pushed to GAR with tags: `latest`, `<commit-sha>`, `main`
4. **Auto-deploy**: ArgoCD detects new image and deploys to GKE

**Workflow file**: `.github/workflows/deploy.yml`

**Triggers**:
- Push to `main` branch
- Changes to `**.go`, `go.mod`, `go.sum`, `Dockerfile`, or workflow file


## 2. Database Migrations (Atlas)

Manage PostgreSQL schema migrations using Atlas.

**Initial Setup**: After starting the database (`podman compose up -d postgres`), run `atlas migrate apply --env local` to initialize the schema.

```bash
# Generate migration from schema definition (schema.sql -> DB state diff)
atlas migrate diff --env local <migration_name>

# Validate migrations
atlas migrate validate --env local

# Apply migrations (both initial setup and updates)
atlas migrate apply --env local
```

**Migration directory**: `internal/infrastructure/database/rdb/migrations/`

## 3. API Health Check

Test the server is running:

```bash
buf curl --schema buf.build/grpc/health --protocol connect \
  -d '{"service": ""}' \
  http://localhost:9090/grpc.health.v1.Health/Check
```

## 4. Directory Reference

- **Migrations**: `internal/infrastructure/database/rdb/migrations/`
- **Handlers**: `internal/adapter/rpc/`
- **DI**: `internal/di/`
