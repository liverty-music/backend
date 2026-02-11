# AI Agent Rules - Liverty Music Backend

**CRITICAL: Before ANY action (create file, run command, write code), check Workspace Structure below to determine the correct repository.**

## Workspace Structure and Responsibilities

```
liverty-music/
├── specification/
│   ├── openspec/changes/ ← Ongoing changes (proposals, designs, specs, tasks)
│   ├── openspec/specs/   ← The latest capability specs
│   └── proto/            ← Protobuf entity / RPC schema (no `buf generate`, use BSR)
├── backend/               ← Go implementation (Connect-RPC services)
├── frontend/              ← Aurelia 2 PWA implementation
└── cloud-provisioning/
    ├── src/               ← Pulumi code (GCP, Cloudflare, GitHub resources)
    └── k8s/               ← Kubernetes manifests (Kustomize base/overlays)
```

## Decision Process (Required Before Action)

**For every file creation, code generation, or command execution:**

1. **Identify the artifact type** (OpenSpec change? Protobuf? Go code? Pulumi? K8s manifest?)
2. **Check Workspace Structure** above to find the correct repository
3. **Verify you're in the correct directory** before proceeding
4. **If uncertain, ask** - never guess the repository location

---

## What This Repository Is

Go backend for **Liverty Music** — implements Connect-RPC services defined in `specification/proto`. Uses Clean Architecture with domain-driven design patterns, PostgreSQL with pgx, and deploys to GKE via ArgoCD GitOps.

## Essential Commands

```bash
mise install           # Install toolchain (go, golangci-lint, pre-commit)
pre-commit install     # Install commit hooks

go mod tidy            # Update dependencies
go generate ./...      # Generate mocks and code
go test ./...          # Run all tests
golangci-lint run      # Run linters

make test              # Run tests with coverage
make lint              # Run all linters
make build             # Build binary
```

## Architecture

### Clean Architecture Layers

```
cmd/
  └── server/          # Application entry point
internal/
  ├── domain/          # Core business logic (entities, value objects, interfaces)
  ├── usecase/         # Application logic (orchestrates domain)
  ├── interface/       # Adapters (RPC handlers, repositories)
  │   ├── rpc/         # Connect-RPC service implementations
  │   └── repository/  # PostgreSQL repositories (pgx)
  └── infrastructure/  # External concerns (config, logging, observability)
```

### Key Conventions

- **Dependency Rule**: Dependencies point inward (infrastructure → interface → usecase → domain)
- **Generated Code**: Proto services consumed via BSR (`buf.build/liverty-music/schema`)
- **Database**: PostgreSQL with pgx (never use `database/sql` or ORMs)
- **Testing**: Table-driven tests, mockery for mocks
- **Error Handling**: Wrap errors with context, use connect.Error for RPC responses

## OpenSpec Workflow

This repo uses OpenSpec for implementation tracking. Design artifacts live in `specification/openspec/changes/`. When implementing:

1. Reference the change in `specification/openspec/changes/<change-name>/`
2. Read `tasks.md` for implementation checklist
3. Implement in this repo following Clean Architecture
4. Mark tasks complete in `specification/openspec/changes/<change-name>/tasks.md`

## Poly-repo Context

- **Specification** (`liverty-music/specification`): Proto schemas, OpenSpec design
- **This repo** (`backend`): Go service implementation
- **Cloud Provisioning** (`liverty-music/cloud-provisioning`): Infrastructure as Code

## Pre-implementation Checklist

Before writing Go code, read:
1. `specification/openspec/changes/<change-name>/` — design decisions
2. `.claude/agents/go-backend-specialist.md` — Go coding standards
3. `internal/domain/` — existing domain model
