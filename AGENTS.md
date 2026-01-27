<!-- OPENSPEC:START -->

# OpenSpec Instructions

These instructions are for AI assistants working in this project.

Always open `@/openspec/AGENTS.md` when the request:

- Mentions planning or proposals (words like proposal, spec, change, plan)
- Introduces new capabilities, breaking changes, architecture shifts, or big performance/security work
- Sounds ambiguous and you need the authoritative spec before coding

Use `@/openspec/AGENTS.md` to learn:

- How to create and apply change proposals
- Spec format and conventions
- Project structure and guidelines

Keep this managed block so 'openspec update' can refresh the instructions.

<!-- OPENSPEC:END -->

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

## Development Workflows

For procedural commands (Build, Test, Migrate, Gen), **load the `backend-workflow` skill**.

- Skill Path: `.agent/skills/backend-workflow/SKILL.md`

> [!TIP]
> If the user asks "How do I run this?" or "Test the API", refer to the `backend-workflow` skill.
