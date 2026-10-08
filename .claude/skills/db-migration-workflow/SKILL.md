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

#### Grant migrations (Cloud SQL IAM users)

Privileges are not part of `schema.sql`, so grant migrations are written by hand (`atlas migrate new <name>`, then `atlas migrate hash`). Rules:

- Grant a new workload's IAM user by its own name pattern (e.g. `rolname LIKE 'fan-api@%.iam'`), never with a generic `rolname LIKE '%@%.iam'` loop.
- If a generic loop is unavoidable, it MUST exclude the narrow-privilege roles: `AND rolname NOT LIKE 'organizer-console-api@%' AND rolname NOT LIKE 'zitadel@%'`. `ALTER DEFAULT PRIVILEGES` in such a loop reaches every table created later, because migrations run as `postgres`.
- When the organizer server gains a new write, add the exact grant (column-level `UPDATE` where possible) in a new migration and extend `organizerWrites` in `internal/infrastructure/database/rdb/migration_grants_integration_test.go`. That test applies every migration with fake IAM roles present and fails on any privilege outside the list.
