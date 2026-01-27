## 1. Preparation

- [x] 1.1 Verify database schema exists for Artist and Concert entities <!-- id: 0 -->
- [x] 1.2 Generate Go code from proto files (if not already done/automated) <!-- id: 1 -->

## 2. Data Access Layer (Repository)

- [x] 2.1 Define `ArtistRepository` interface in `internal/entity` and implement in `infrastructure/postgres` <!-- id: 2 -->
- [x] 2.2 Define `ConcertRepository` interface in `internal/entity` and implement in `infrastructure/postgres` <!-- id: 3 -->
- [x] 2.3 Define `VenueRepository` interface in `internal/entity` and implement in `infrastructure/postgres` <!-- id: 4 -->

## 3. UseCase Layer (Business Logic)

- [x] 3.1 Implement `ArtistUseCase` struct and constructor <!-- id: 5 -->
- [x] 3.2 Implement `ConcertUseCase` struct and constructor <!-- id: 6 -->
- [x] 3.3 Implement `ArtistUseCase.Create` (TDD: Write test -> Implement entity logic) <!-- id: 7 -->
- [x] 3.4 Implement `ArtistUseCase.List` (TDD: Write test -> Implement logic) <!-- id: 8 -->
- [x] 3.5 Implement `ArtistUseCase.AddMedia` / `RemoveMedia` (TDD: Write test -> Implement logic) <!-- id: 9 -->
- [x] 3.6 Implement `ConcertUseCase.List` (TDD: Write test -> Implement logic) <!-- id: 10 -->

## 4. Handler & Wiring (Manual DI)

- [x] 4.1 Implement `ConcertService` (gRPC Handler) in `internal/handler/rpc` <!-- id: 12 -->
- [x] 4.2 Inject `ArtistUseCase` and `ConcertUseCase` into Handler <!-- id: 13 -->
- [x] 4.3 Map Proto <-> Entity in Handler <!-- id: 14 -->
- [x] 4.4 Wire up dependencies in `cmd/server/main.go` (Manual DI) <!-- id: 15 -->
- [x] 4.5 Verify gRPC endpoint connectivity <!-- id: 16 -->
