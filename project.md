# Project Context

## Overview

Liverty Music is a concert notification platform that transforms concert discovery from active search to personalized, automated alerts. It specifically targets passionate music fans who attend 10+ concerts annually.
The system provides a "passive experience where the information you want 'finds you'".

## Architecture

- **Style**: Clean Architecture with Domain-Driven Design (DDD).
- **Layers**:
  - `cmd/`: Application entry points.
  - `internal/adapter/`: Connect-RPC handlers (controllers).
  - `internal/usecase/`: Business logic.
  - `internal/entity/`: Domain entities.
  - `internal/infrastructure/`: Database and server implementations.
- **Communication**: gRPC/Connect-RPC using Protocol Buffers.

## Tech Stack

- **Language**: Go (1.24+)
- **API Framework**: Connect-RPC (`connectrpc.com/connect`)
  - Supports HTTP/1.1 and gRPC.
  - Schema: `buf.build/liverty-music/schema` (BSR).
- **Database**: PostgreSQL
  - ORM: Bun (`github.com/uptrace/bun`)
  - Migrations: Atlas (`atlasgo.sh`)
- **Dependency Injection**: Google Wire (`github.com/google/wire`)
- **Observability**: OpenTelemetry (Tracing), Structured Logging (`pkg/logging`).

## Conventions

- **Directory Structure**: Standard Go project layout.
- **Dependency Injection**: Re-run `wire internal/di/` when `wire.go` changes.
- **Migrations**:
  - Use `atlas migrate diff --env local <name>` to generate migrations.
  - Migrations are versioned in `internal/infrastructure/database/rdb/migrations/versions/`.
- **Testing**: `go test ./...`
- **Linting**: `golangci-lint run ./...`.

## Core Features

1.  **Artist Registration**: User-artist subscriptions (Spotify/MusicBrainz identifiers).
2.  **Concert Data**: Management of venues, dates, tickets, and status.
3.  **Notifications**: Multi-channel (push, email), multi-lingual (En/Ja).
4.  **Matching**: Intelligent user-artist-concert matching.
