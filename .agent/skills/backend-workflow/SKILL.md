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

### Manual Deployment (Development/Testing Only)

For testing changes before merging to main:

```bash
# Build and push test image
docker build -t asia-northeast2-docker.pkg.dev/liverty-music-dev/backend/server:test .
gcloud auth configure-docker asia-northeast2-docker.pkg.dev
docker push asia-northeast2-docker.pkg.dev/liverty-music-dev/backend/server:test

# Update Kustomize in cloud-provisioning repo
# k8s/namespaces/backend/overlays/dev/server/kustomization.yaml
#   images:
#   - name: server
#     newTag: test
```

**Note**: Standard workflow is to merge to `main` and let CI/CD handle deployment.

## 2. Database Migrations (Atlas)

Manage PostgreSQL schema migrations using Atlas.

```bash
# Generate migration from schema definition (schema.sql -> DB state diff)
# Note: Ensure local DB is running
atlas migrate diff --env local <migration_name>

# Validate migrations
atlas migrate validate --env local

# Apply migrations (Local development only)
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
