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

This repository carries no planning of its own. `openspec/config.yaml` declares `store: openspec-store` (the `liverty-music/specification` repository), so every `openspec` command run here resolves to that store; the `Using OpenSpec root: openspec-store` banner confirms it.

- **Before implementing**, read the change's artifacts and the affected specs in the store: `openspec show <change>`, `openspec instructions apply --change <change>`, and `openspec show <spec-id> --type spec`. Implement against the spec, not against memory.
- **Run `openspec doctor` first** on a fresh machine and at the start of every cloud thread. If the store is not registered, register a `specification` checkout: the sibling clone when one exists (`openspec store register "$(git rev-parse --show-toplevel)/../specification" --id openspec-store`, the layout of a multi-repo Claude Project thread), otherwise clone the public repo first (`git clone --depth 1 https://github.com/liverty-music/specification.git /tmp/openspec-store`) and register that path. Then continue.
- **Isolate local work with `claude --worktree <change>`** (it lands in `.claude/worktrees/<change>`, and `.worktreeinclude` copies the gitignored files a worktree needs). The full cross-repo workflow, local and cloud, is in the `specification` repository's README ("Development workflow").
- **Never write to the store from this repository.** Task progress and archiving are recorded in the `specification` repository once this repository's PR merges.
- **Every PR must cite its change**: fill the `OpenSpec-Change` (or `OpenSpec-Spec`) field and the store commit SHA in the PR template so reviewers can see which contract version the implementation follows.

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

## Development Workflows

### Consuming New Proto Types (after BSR gen)

Upgrade and placeholder-swap procedure: see the `consume-proto-release` skill.

### Database Migrations

Production migrations are handled by the **Atlas Kubernetes Operator** — the backend application does NOT run migrations at startup.

Migration workflow (local generation, operator deployment, kustomization update): see the `db-migration-workflow` skill.

### Gemini A/B Evaluation Harness

See `internal/infrastructure/gcp/gemini/CLAUDE.md`.

### Cloud SQL Access (dev and prod, via port-forward)

For integration tests use `docker compose up -d postgres` instead.

See [docs/dev-db-access.md](docs/dev-db-access.md), which points to the cloud-provisioning runbook (ephemeral `db-proxy` Pod, personal IAM login, read-only).

## Review criteria (flag violations; quote the rule + link the existing code compared against)

- Handlers in `internal/adapter/rpc/` only map Proto↔Entity (via `mapper/`) and call a UseCase — no business logic or repo access (cf. `follow_handler.go`).
- Errors crossing a layer carry an apperr code: repos via `toAppErr` (`rdb/errors.go`), external via `api.FromHTTP`. A bare `fmt.Errorf` across a boundary is a violation.
- A new handler MUST be registered in `internal/di/provider.go` in the correct list (admin vs consumer).
- Interfaces are defined where consumed (`internal/entity/`, `internal/usecase/`); impls in `infrastructure/`/`adapter/`.
- Nullable columns scan into `sql.Null*` and are `.Valid`-checked before assignment (cf. `user_repo.go scanUser`).

</agent-rules>
