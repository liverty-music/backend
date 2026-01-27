# Design: Implement ConcertService Backend

## Context

We are implementing the backend for `ConcertService` based on the `liverty_music.rpc.v1` proto definition. The goal is to provide a functional API for the frontend and other clients. Needs to adhere to the existing `pgx` based architecture.

## Goals / Non-Goals

- **Goals**:
  - Functional implementation of 5 core RPCs.
  - Robust error handling (mapping domain errors to gRPC status codes).
  - Unit and integration testing.
- **Non-Goals**:
  - `UserService` implementation.
  - Advanced filtering or sorting beyond what is specified.
  - Authentication/Authorization (creating artists is currently open to all).
  - Pagination (postponed).

## Decisions

- **Decision: No Pagination**
  - **Why**: Explicit user instruction to keep implementation simple for now.
  - **Trade-off**: Potential performance issues with large datasets, but acceptable for MVP.
- **Decision: No Authorization**
  - **Why**: User instruction.
  - **Risk**: Security vulnerability; anyone can create artists.
  - **Mitigation**: Will be addressed in a future "Security & Auth" change.
- **Decision: Filter by Artist ID only**
  - **Why**: User instruction to strictly follow current proto definition.

## Database Schema

We need to persist `Artist`, `Concert`, and `Venue` entities. Media links are part of the `Artist` aggregate but likely stored in a separate table for normalization.

```sql
CREATE TABLE venues (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name TEXT NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

CREATE TABLE artists (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name TEXT NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

-- Index for searching/listing artists (though effectively full scan without pagination)
CREATE INDEX idx_artists_name ON artists(name);

CREATE TABLE artist_media (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    artist_id UUID NOT NULL REFERENCES artists(id) ON DELETE CASCADE,
    type TEXT NOT NULL, -- "WEB", "TWITTER", "INSTAGRAM" mapped from enum
    url TEXT NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

CREATE INDEX idx_artist_media_artist_id ON artist_media(artist_id);

CREATE TABLE concerts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    artist_id UUID NOT NULL REFERENCES artists(id) ON DELETE CASCADE,
    venue_id UUID NOT NULL REFERENCES venues(id),
    title TEXT NOT NULL,
    date DATE NOT NULL,
    start_time TIME NOT NULL,
    open_time TIME,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

CREATE INDEX idx_concerts_artist_id ON concerts(artist_id);
CREATE INDEX idx_concerts_venue_id ON concerts(venue_id);
```

## Go Packages & Interfaces

We will adhere to the **Clean Architecture** layout defined in the `go-architecture` skill.

### Packages

- `internal/entity`: Business entities (`Artist`, `Concert`, `Venue`).
- `internal/usecase`: Application business logic (`ArtistUseCase`, `ConcertUseCase`).
- `internal/infrastructure/postgres`: Database implementation of repository interfaces.
- `internal/handler/rpc`: gRPC/Connect server implementation (Adapter layer).
- `cmd/server`: Main entry point with **Manual DI**.

### Interfaces

**Repository Interfaces (defined in `internal/entity`)**

```go
package entity

import "context"

type ArtistRepository interface {
	Create(ctx context.Context, artist *Artist) error
	List(ctx context.Context) ([]*Artist, error)
	Get(ctx context.Context, id string) (*Artist, error)

	// Media operations belong to Artist aggregate
	AddMedia(ctx context.Context, media *Media) error
	DeleteMedia(ctx context.Context, mediaID string) error
}

type ConcertRepository interface {
	ListByArtist(ctx context.Context, artistID string) ([]*Concert, error)
}

type VenueRepository interface {
	Create(ctx context.Context, venue *Venue) error
	Get(ctx context.Context, id string) (*Venue, error)
}
```

**UseCase Interfaces (defined in `internal/usecase`)**

```go
package usecase

import (
	"context"
	"github.com/liverty-music/backend/internal/entity"
)

type ArtistUseCase interface {
	List(ctx context.Context) ([]*entity.Artist, error)
	Create(ctx context.Context, name string, media []*entity.Media) (*entity.Artist, error)
	AddMedia(ctx context.Context, artistID string, media *entity.Media) error
	RemoveMedia(ctx context.Context, mediaID string) error
}

type ConcertUseCase interface {
	List(ctx context.Context, artistID string) ([]*entity.Concert, error)
}
```

## Boundary Interfaces

### Dependency Injection

- **Manual DI**: Dependencies will be injected manually in `cmd/main.go`.
- `wire` is **NOT** used for this feature. We will manually construct `ArtistUseCase` and `ConcertUseCase` and inject them into the RPC handler.

### External Dependencies

- **PostgreSQL**: Managed via `pgx/v5`.
- **Venue Service**: This logic is now internalized with `VenueRepository`. We ensure valid `venue_id` via foreign keys.

## Open Questions

- Database Schema: Do the `artists`, `concerts`, `artist_media` tables exist and match the entity definitions? (Action: Check schema during implementation task 1.1)
