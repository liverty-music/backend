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

## Development Workflows

For procedural commands (Build, Test, Migrate, Gen), **load the `backend-workflow` skill**.

- Skill Path: `.agent/skills/backend-workflow/SKILL.md`

### Protobuf Code Generation

Local Protobuf code generation is FORBIDDEN. To generate/update Go code from schema changes:

1.  **Specification Repo**: Create a Pull Request with your `.proto` changes.
2.  **GitHub Release**: Once merged to `main`, create a GitHub Release in the `specification` repository.
3.  **Remote Generation**: This triggers the BSR (Buf Schema Registry) remote generation.
4.  **Local Consumption**: The `backend` repo consumes these types via `buf.build/gen/go/...`. You may need to run `go get -u` or similar if your local environment doesn't pick up the latest BSR build immediately.

> [!TIP]
> If the user asks "How do I run this?" or "Test the API", refer to the `backend-workflow` skill.
