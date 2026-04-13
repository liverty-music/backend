<poly-repo-context repo="backend">
  <responsibilities>Go API server following Clean Architecture. Connect-RPC services,
  pgx for PostgreSQL, Google Wire for DI, Atlas for DB migrations.</responsibilities>
  <essential-commands>
    atlas migrate diff --env local &lt;name&gt;  # Generate DB migration
    atlas migrate apply --env local            # Apply migrations locally
    mockery                                    # Generate mocks from interfaces
    docker compose up -d postgres              # Start local DB for integration tests
  </essential-commands>
</poly-repo-context>

<agent-rules>

## Core Architecture

| Layer              | Path                       | Responsibility                                                                  |
| ------------------ | -------------------------- | ------------------------------------------------------------------------------- |
| **Entity**         | `internal/entity/`         | Core business objects (User, Artist). Pure structs, no tags (unless necessary). |
| **Use Case**       | `internal/usecase/`        | Business logic & Application rules. Interfaces defined here.                    |
| **Adapter**        | `internal/adapter/`        | Interface adapters. RPC handlers (`ipc/`) convert Proto <-> Entity.             |
| **Infrastructure** | `internal/infrastructure/` | Frameworks & Drivers. DB (`database/rdb`), Server (`server/`).                  |
| **DI**             | `internal/di/`             | Dependency Injection wiring using manual factory functions.                      |

## Key Technical Decisions

### 1. RPC & Communication

- **Framework**: Connect-RPC (`connectrpc.com/connect`).
- **Schema**: Managed via BSR (`buf.build/liverty-music/schema`).
- **Pattern**: Handlers should strictly map Proto messages to Domain Entities and delegate logic to UseCases.

### 2. Naming Conventions

- **Timestamps**:
    - **Database**: Use `_at` suffix (e.g., `start_at`, `created_at`). Type: `TIMESTAMPTZ`.
    - **Go Entity**: Use `Time` suffix (e.g., `StartTime`, `CreateTime`). Type: `time.Time`.
    - **Reasoning**: Adheres to SQL standards for columns and Google AIP/Protobuf standards for code. Mappings should be handled in the Repository layer.

### 3. Testing & Mocking

- **Mocking**: Configured via `.mockery.yml`. Run `mockery` to generate mocks from interfaces.
- **Pattern**: Define interfaces where consumed. Accept interfaces, return concrete types.

## Development Workflows

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


### Development Commands

```bash
make lint              # Format check + golangci-lint (matches CI)
make fix               # Auto-fix formatting (gofmt -w)
make test              # Unit tests with local DB (docker compose + atlas migrate)
make test-integration  # Integration tests (DB must already be running, used by CI)
make check             # Full pre-commit check (lint + test)
```

`make check` is automatically enforced before `git commit` by the Claude Code PreToolUse hook in `.claude/settings.json`.

### Integration Tests

Integration tests under `internal/infrastructure/database/rdb/` require a local PostgreSQL instance. `make test` handles DB startup automatically via `docker compose up -d postgres --wait`.

### Dev DB Access (Cloud SQL via port-forward)

To connect directly to the **dev Cloud SQL instance** (e.g., for ad-hoc queries, schema inspection, or data debugging), use the Cloud SQL Auth Proxy Pod deployed in the dev GKE cluster.

> **This is for the dev Cloud SQL instance only.** For integration tests and local development, use the Docker Compose PostgreSQL instance (`docker compose up -d postgres`).

**Step 1 — Forward the proxy port:**

```bash
kubectl port-forward deployment/cloud-sql-proxy 5432:5432 -n backend
```

Keep this terminal open. The tunnel closes when you exit.

**Step 2 — Connect with psql:**

```bash
psql "host=localhost port=5432 user=backend-app@liverty-music-dev.iam dbname=liverty-music sslmode=disable options='-c search_path=app'"
```

No password is required — authentication is handled by IAM via the proxy.

**Connection parameters:**

| Parameter | Value |
|-----------|-------|
| Host | `localhost` |
| Port | `5432` |
| User | `backend-app@liverty-music-dev.iam` |
| Database | `liverty-music` |
| Schema | `app` |
| SSL Mode | `disable` (proxy handles encryption) |

</agent-rules>
