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
1. Edit `schema.sql` to the desired state first; never write a schema migration by hand (functions are the exception, see "Function migrations"). Lock-safety rules for the generated SQL (concurrent indexes, `NOT VALID` constraints, expand and contract) are in the `postgres-migration` skill.
2. Generate the file with `atlas migrate diff --env local <descriptive_name>` (needs Docker for the `dev` database). An empty diff means no migration is needed. An unexpected `DROP` + `CREATE` pair means `schema.sql` drifted from the migrations; fix `schema.sql` instead of the generated file.
3. To add data changes (a backfill) or `-- atlas:txmode none` (required for `CREATE INDEX CONCURRENTLY`), edit the generated file and run `atlas migrate hash --env local`.
   Atlas writes names qualified with `"app".`; remove the qualifier and start the file with `SET search_path TO app, public;`, as the other migrations do, then rehash. Production applies migrations with `search_path: app` (`atlas-migration.yaml`), but the local database, CI and the `dev` replay apply them without one, so tables created by a migration without `SET search_path` sit in `public` there and a qualified `"app".<table>` fails.
4. Add the new file to `k8s/atlas/base/kustomization.yaml` under `configMapGenerator.files`.
5. Run `scripts/check-migration-drift.sh` (kustomization listing, `schema.sql` changed with the migrations, `atlas migrate validate`, timestamp order against `origin/main`) and `make lint-schema`.
6. All of these changes go in the same PR.

#### Function migrations

Atlas Community (the CLI without `atlas login`, which this repository uses) cannot read functions, procedures, triggers or views from the desired state: a `CREATE FUNCTION` in `schema.sql` makes `migrate diff` fail with `functions and procedures are available to logged-in users only`. It also skips them when it inspects a database, so a diff never drops one. Keep them out of `schema.sql`:

- Write the function by hand in a migration (`atlas migrate new <name>`, then `atlas migrate hash --env local`), with its `COMMENT ON FUNCTION` and grants. A change to it is a new migration with `CREATE OR REPLACE FUNCTION`.
- Leave a comment in `schema.sql` where the function would be, naming the latest migration that defines it (cf. `delete_ticket_stock`).
- A migration can hold both: generate the schema part with `migrate diff`, then append the function by hand and rehash.

#### Grant migrations (Cloud SQL IAM users)

Privileges are not part of `schema.sql`, so grant migrations are written by hand (`atlas migrate new <name>`, then `atlas migrate hash`). Rules:

- Grant a new workload's IAM user by its own name pattern (e.g. `rolname LIKE 'fan-api@%.iam'`), never with a generic `rolname LIKE '%@%.iam'` loop.
- If a generic loop is unavoidable, it MUST exclude the narrow-privilege roles: `AND rolname NOT LIKE 'organizer-console-api@%' AND rolname NOT LIKE 'zitadel@%'`. `ALTER DEFAULT PRIVILEGES` in such a loop reaches every table created later, because migrations run as `postgres`.
- When the organizer server gains a new write, add the exact grant (column-level `UPDATE` where possible) in a new migration and extend `organizerWrites` in `internal/infrastructure/database/rdb/migration_grants_integration_test.go`. That test applies every migration with fake IAM roles present and fails on any privilege outside the list.
