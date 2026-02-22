# Project Context & Architecture

## Core Architecture

This project follows **Clean Architecture** principles.

| Layer              | Path                       | Responsibility                                                                  |
| ------------------ | -------------------------- | ------------------------------------------------------------------------------- |
| **Entity**         | `internal/entity/`         | Core business objects (User, Artist). Pure structs, no tags (unless necessary). |
| **Use Case**       | `internal/usecase/`        | Business logic & Application rules. Interfaces defined here.                    |
| **Adapter**        | `internal/adapter/`        | Interface adapters. RPC handlers (`ipc/`) convert Proto <-> Entity.             |
| **Infrastructure** | `internal/infrastructure/` | Frameworks & Drivers. DB (`database/rdb`), Server (`server/`).                  |
| **DI**             | `internal/di/`             | Dependency Injection wiring using Google Wire.                                  |

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

### Protobuf Code Generation

Local Protobuf code generation is FORBIDDEN. To generate/update Go code from schema changes:

1.  **Specification Repo**: Create a Pull Request with your `.proto` changes.
2.  **GitHub Release**: Once merged to `main`, create a GitHub Release in the `specification` repository.
3.  **Remote Generation**: This triggers the BSR (Buf Schema Registry) remote generation.
4.  **Local Consumption**: The `backend` repo consumes these types via `buf.build/gen/go/...`. You may need to run `go get -u` or similar if your local environment doesn't pick up the latest BSR build immediately.

### Integration Tests

Integration tests under `internal/infrastructure/database/rdb/` require a local PostgreSQL instance.

**Before running integration tests, you MUST ensure the database is running:**

```bash
docker compose up -d postgres
```

The schema is applied automatically via Atlas migrations on container startup. If the container was freshly created, also apply the schema manually:

```bash
docker compose exec postgres psql -U test-user -d test-db < internal/infrastructure/database/rdb/schema/schema.sql
```
