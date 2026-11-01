<poly-repo-context repo="backend">
  <responsibilities>Go API server following Clean Architecture. Connect-RPC services,
  pgx for PostgreSQL, manual factory functions for DI, Atlas for DB migrations.</responsibilities>
  <essential-commands>
    atlas migrate diff --env local &lt;name&gt;  # Generate DB migration
    atlas migrate apply --env local            # Apply migrations locally
  </essential-commands>
</poly-repo-context>

<agent-rules>

## OpenSpec (planning lives in the store)

This repository carries no planning of its own. `openspec/config.yaml` declares `store: openspec-store` (the `liverty-music/specification` repository), so every `openspec` command run here resolves to that store; the `Using OpenSpec root: openspec-store` banner confirms it. The full workflow, local and cloud, is in the `specification` README ("Development workflow"); the rules that bind a session here are:

- **Before implementing**, read the change's artifacts and the affected specs in the store: `openspec show <change>`, `openspec instructions apply --change <change>`, and `openspec show <spec-id> --type spec`. Implement against the spec, not against memory.
- **Run `openspec doctor` first** on a fresh machine and at the start of every cloud thread. If the store is not registered, register a `specification` checkout: the sibling clone when one exists (`openspec store register "$(git rev-parse --show-toplevel)/../specification" --id openspec-store`, the layout of a multi-repo Claude Project thread), otherwise clone the public repo first (`git clone --depth 1 https://github.com/liverty-music/specification.git /tmp/openspec-store`) and register that path. Keep that checkout on `main` and pull it before implementing: OpenSpec never pulls.
- **Isolate local work with `claude --worktree <change>`** (`.claude/worktrees/<change>`, branch `worktree-<change>`). In `specification`, the main checkout is the shared store and stays on `main`; branch work there (plan PR, proto PR, archive) goes through its own worktrees (`specification` README "Branch work in this repository").
- **Record progress in the store, never commit there.** `/opsx:apply` checks off `tasks.md` and updates `design.md` in the `specification` checkout's working tree on `main`, uncommitted. Several changes share that working tree, so never run `git stash`, `git reset --hard`, `git checkout -- .` / `git restore .` or `git clean` there, never `git add -A` / `git add .`, and never switch its branch.
- **Every PR must cite its change**: fill the `OpenSpec-Change` (or `OpenSpec-Spec`) field and the store commit SHA in the PR template's OpenSpec Traceability section. One PR per repository.
- **Close-out happens in `specification`**, after every implementing PR has merged and the release is confirmed in production: `/opsx:verify`, then archive in a `<change>-archive` worktree of `specification`, staging only that change's paths (README "Lifecycle of a change", step 5). Do not archive from this repository.
- **Cloud threads run no repository hooks**: run `make check` before committing; CI is the gate.

## Cross-repo workflow (poly-repo)

This repo is one of four under `liverty-music/`: `specification` (proto schema + OpenSpec store), `backend`, `frontend`, `cloud-provisioning`. The full release process lives in the specification repo's AGENTS.md; the rules that bind work here are:

- **Dependency order**: specification PR merge → GitHub Release (`vX.Y.Z`) → BSR remote generation → this repo can build with the new types. Never generate protobuf code locally; consume it from BSR (see the `consume-proto-release` skill).
- **Do not open a PR, even a draft, before BSR gen completes.** CI fails on the missing types and creates review noise. Prepare the branch locally and push only after the generated package is upgraded and placeholder types are swapped. Exception: the user explicitly asks for parallel review; then annotate the PR with "Depends on BSR gen for vX.Y.Z".
- **Start downstream work early.** As soon as the proto surface is agreed (approved OpenSpec change or open specification PR), write handlers, use cases, repositories and tests against the planned type shape, with local placeholders marked `TODO: swap to generated type after BSR gen`.
- Monitor BSR gen with `gh run list --repo liverty-music/specification --workflow buf-release.yml --limit 3`.

## Entity Tags

Entity types in `internal/entity/` are pure structs without struct tags, with one exception. Event payloads published through `EventPublisher.PublishEvent` (`internal/entity/event_data.go` and the types they embed, e.g. `DiscoveredSeries`) and the Web Push `NotificationPayload` are wire contracts, so they carry `json` tags in `internal/entity`. The tags pin the field names on the wire so renaming a Go field cannot silently break in-flight messages or the service worker. Do not add tags to other entity types, and do not strip these tags without a wire-compatibility plan.

## Key Technical Decisions

### Naming Conventions

- **Timestamps**:
    - **Database**: Use `_at` suffix (e.g., `start_at`, `created_at`). Type: `TIMESTAMPTZ`.
    - **Go Entity**: Use `Time` suffix (e.g., `StartTime`, `CreateTime`). Type: `time.Time`.
    - **Reasoning**: Adheres to SQL standards for columns and Google AIP/Protobuf standards for code. Mappings should be handled in the Repository layer.
    - **When a timestamp exists**: only when an entity field in the spec needs it; no audit metadata (`updated_at`, `deleted_at` are rejected by `scripts/lint-schema.sh`). When the field means "when the record was created", name it `created_at` / `CreateTime` / proto `create_time` (AIP-148), not a synonym such as `configured_at` or `recorded_at`.

### Go conventions

General Go guidance comes from the `go` plugin skills (go-errors, go-doc-comments, go-layout, go-testing, go-postgres, go-connect-rpc). This repository's choices on top of them:

- **Layers**: `internal/entity` (types and the ports they need), `internal/usecase`, `internal/adapter` (RPC handlers, event consumers), `internal/infrastructure` (database, external APIs), wired in `internal/di`. Dependencies point inward; see the review criteria below for the rules a review checks.
- **Errors** crossing a layer are `apperr` errors (`github.com/pannpers/go-apperr`) with a code; the RPC layer maps the code to a Connect code.
- **Doc comments** on interface methods list the apperr codes they return under a `# Possible errors` heading (cf. `internal/entity/identity_verification.go`). The list items are indented (`//   - NotFound: ...`); an unindented `- ` line is not rendered as a list.
- **Tests** live in the external `_test` package, assert with testify (`require` for preconditions, `assert` for the rest), and stub ports with the mockery v3 mocks (`make mocks` regenerates them from `.mockery.yml`).
- **IDs** are UUIDv7 strings from `entity.NewID()` (`internal/entity/id.go`).

### Database schema

`schema.sql` (`internal/infrastructure/database/rdb/schema/schema.sql`) is the desired state, and `make lint-schema` (`scripts/lint-schema.sh`) enforces its policies: no `SERIAL`, `TIMESTAMPTZ` only, no `updated_at` / `deleted_at`, `TEXT` with a `CHECK` instead of `VARCHAR`, and a `COMMENT ON` for every table and column. General PostgreSQL guidance comes from the `postgres` plugin skills (postgres-schema, postgres-migration).

## Development Workflows

### Consuming New Proto Types (after BSR gen)

Upgrade and placeholder-swap procedure: see the `consume-proto-release` skill.

### Database Migrations

Production migrations are handled by the **Atlas Kubernetes Operator** — the backend application does NOT run migrations at startup.

Migration workflow (local generation, operator deployment, kustomization update): see the `db-migration-workflow` skill.

### Durable workflows

A process that must survive a stopped process is a workflow, not a sweeper: a usecase method `func(ctx context.Context, f usecase.Flow, in In) (Out, error)` whose ordered steps run through `f.Step` (or `usecase.Step`), registered by a stable name with `workflow.Register` in the workflow worker (`cmd/worker`). Other workloads only start, signal and await workflows through `usecase.Workflows`. The engine is DBOS Transact (`internal/infrastructure/workflow`); usecases never import it. A resumed run replays the recorded results of completed steps, so it must take the same path as the first run:

- Read every value that can change between runs (the clock, new ids, random numbers, rows) inside a step, never in the workflow body.
- Keep step names stable; a step is matched by its position and name.
- Change the steps of a registered workflow only under `if patched, err := f.Patched(ctx, "<change>")`. Once no run started before the patch is left, replace that call with `f.DeprecatePatch(ctx, "<change>")`, and remove it in a later change. Never change the application version for it.
- Return a business outcome (a card that can no longer be charged) as the workflow's result. An error ends the workflow and pages an admin (`workflow.needs_admin`).
- Return errors from `Flow` and `Workflows` as they are. A conflict (another execution owns the workflow, `dbos.ErrConflictingWorkflowID`) or a cancellation must reach DBOS, never be swallowed or retried in the usecase.
- A child workflow that must outlive its parent is started with `context.WithoutCancel(ctx)`.
- A step that fails with `Unavailable` or `DeadlineExceeded` is retried up to the workflow's deadline (pass `usecase.WithDeadline` when the work must end by a time); any other error is not retried. An external call's idempotency key names the business operation (`refund:<order>`), not the workflow run.
- A queue's settings live only in code, where the queue is registered; never change them at runtime, because the next start of the worker overwrites them.

**Upgrading `github.com/dbos-inc/dbos-transact-golang`**: the workloads never migrate the `dbos` schema themselves (`SkipMigrations`), and a worker whose schema is behind refuses to start. Before bumping the module, add an Atlas migration holding the output of `go run github.com/dbos-inc/dbos-transact-golang/cmd/dbos@<new version> migrate --print-migrations <first new number>` between the `-- BEGIN/END dbos migrate --print-migrations <n>` markers (see `20261027120000_create_dbos_schema.sql`), with `-- atlas:txmode none` at the top. `TestDBOSMigrationsMatchModule` fails until the committed SQL matches the module, and names the number to print. These migrations have no counterpart in `schema.sql`; `atlas.hcl` keeps `migrate diff` from dropping the `dbos` schema.

A workflow that needs an admin, and the `workflow` subcommands that resolve it, are in the cloud-provisioning runbook `docs/runbooks/workflow-needs-admin.md`.

### Gemini A/B Evaluation Harness

See `internal/infrastructure/gcp/gemini/AGENTS.md`.

### Cloud SQL Access (dev and prod, via port-forward)

For integration tests use `docker compose up -d postgres` instead.

See [docs/dev-db-access.md](docs/dev-db-access.md), which points to the cloud-provisioning runbook (ephemeral `db-proxy` Pod, personal IAM login, read-only).

## Review criteria (flag violations; quote the rule + link the existing code compared against)

- Handlers in `internal/adapter/rpc/` only map Proto↔Entity (via `internal/adapter/pb`) and call a UseCase — no business logic or repo access (cf. `follow_handler.go`).
- Errors crossing a layer carry an apperr code: repos via `toAppErr` (`rdb/errors.go`), external via `api.FromHTTP`. A bare `fmt.Errorf` across a boundary is a violation.
- A new handler MUST be registered in `internal/di/provider.go` in the correct list (admin vs consumer).
- Interfaces are defined where consumed (`internal/entity/`, `internal/usecase/`); impls in `infrastructure/`/`adapter/`.
- Nullable columns scan into `sql.Null*` and are `.Valid`-checked before assignment (cf. `user_repo.go scanUser`).
- Every new column (timestamps included) maps to an entity field the spec defines; a column no spec requirement uses is a violation, and a creation-time column is named `created_at`, not a synonym.
- A write that checks a condition and then acts (a delete refused while a record exists, a create limited by a count) runs the check and the write in one transaction and locks the rows that the paths creating those records also lock (cf. `checkout_repo.go` `checkoutLockTicketTypeQuery`). A delete of dependent rows filters by the states the check allowed, so a row created after the check makes a RESTRICT foreign key fail instead of vanishing.
- A different filter is a different method (`ListByArtist`), not an optional parameter on an existing one; an optional value is a pointer (cf. `VenueRepository.GetByListedName` `adminArea *string`), never `""` or `0` meaning "absent".
- A usecase test stubs its ports with the mockery mocks in `mocks/`; a hand-written fake does not grow behaviour the real implementation lacks.

</agent-rules>
