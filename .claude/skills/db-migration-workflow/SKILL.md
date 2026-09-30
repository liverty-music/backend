---
name: db-migration-workflow
description: Backend Atlas migration workflow - generating, applying and validating migrations locally, how the Atlas Kubernetes Operator applies them in GKE, and the files a new migration PR must touch. Use when adding or changing a database migration in this repository.
---

### Database Migrations

Database migrations are managed by **Atlas** with two distinct workflows:

#### Local Development

```bash
# Generate a new migration from schema changes
atlas migrate diff --env local <migration_name>

# Apply migrations locally
atlas migrate apply --env local

# Validate migration integrity
atlas migrate validate --env local
```

Migration files live in `k8s/atlas/base/migrations/`. The desired-state schema is at `internal/infrastructure/database/rdb/schema/schema.sql`.

#### Production (GKE)

Production migrations are handled by the **Atlas Kubernetes Operator** — the backend application does NOT run migrations at startup.

- **AtlasMigration CRD** + **ConfigMap** are defined in `k8s/atlas/base/`
- ArgoCD syncs from `k8s/atlas/overlays/<env>` via a dedicated `backend-migrations` Application
- The operator connects to Cloud SQL as the `postgres` user (password from K8s Secret synced by ESO)
- All tables reside in the `app` schema (`search_path=app`)
- Sync wave ordering ensures migrations complete before the backend Deployment starts

When adding a new migration:
1. Create the migration file with `atlas migrate diff --env local`
2. Add the new file to `k8s/atlas/base/kustomization.yaml` under `configMapGenerator.files`
3. Both changes go in the same PR
