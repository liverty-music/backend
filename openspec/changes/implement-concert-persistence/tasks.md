## 1. Domain & Infrastructure Updates

- [ ] 1.1 Update `VenueRepository` interface in `internal/entity/venue.go` to include `GetByName`.
- [ ] 1.2 Implement `GetByName` in `internal/infrastructure/database/rdb/venue_repo.go`.

## 2. UseCase Implementation

- [ ] 2.1 Update `ConcertUseCase` struct and `NewConcertUseCase` constructor to accept `VenueRepository`.
- [ ] 2.2 Refactor `SearchNewConcerts` to implement the "Search -> Get/Create Venue -> Persist Concert" loop.

## 3. Verification

- [ ] 3.1 Run unit tests for `ConcertUseCase` (fixing breaking changes in mocks).
- [ ] 3.2 Verify persistence manually via CLI or test script.
