## 1. Domain & Infrastructure Updates

- [x] 1.1 Update `VenueRepository` interface in `internal/entity/venue.go` to include `GetByName`.
- [x] 1.2 Implement `GetByName` in `internal/infrastructure/database/rdb/venue_repo.go`.

## 2. UseCase Implementation

- [x] 2.1 Update `ConcertUseCase` struct and `NewConcertUseCase` constructor to accept `VenueRepository`.
- [x] 2.2 Refactor `SearchNewConcerts` to implement the "Search -> Get/Create Venue -> Persist Concert" loop.

## 3. Verification

- [x] 3.1 Run unit tests for `ConcertUseCase` (fixing breaking changes in mocks).
- [x] 3.2 Verify persistence manually via CLI or test script.

## 4. Refactoring

- [x] 4.1 Rename usecase files to `*_uc.go` suffix.
