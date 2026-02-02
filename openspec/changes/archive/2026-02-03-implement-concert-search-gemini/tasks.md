# Tasks: Implement Concert Search with Gemini

Issue: #18

## 1. Schema Migration

- [x] 1.1 Create migration to drop `artist_media` and create `artist_official_site`
- [x] 1.2 Update `internal/infrastructure/database/rdb/migrations/schema.sql` to match new schema

## 2. Domain Layer Updates

- [x] 2.1 Remove unused fields from `internal/entity/artist.go` (match proto)
- [x] 2.2 Remove unused fields from `internal/entity/concert.go` (match proto)
- [x] 2.3 Remove unused helper functions (`NewArtist`, etc.)
- [x] 2.4 Add `OfficialSite` entity to `internal/entity/artist.go`
- [x] 2.5 Update `ArtistRepository` interface with `CreateOfficialSite` and `GetOfficialSite`
- [x] 2.6 Update `ConcertRepository` interface with `upcomingOnly` filter
- [x] 2.7 Add `ConcertSearcher` domain interface

## 3. Infrastructure Layer (RDB)

- [x] 3.1 Update `ArtistRepository` to implement `CreateOfficialSite` and `GetOfficialSite`
- [x] 3.2 Update `ArtistRepository.Create` and `Get` to remove media handling
- [x] 3.3 Update `ConcertRepository` (RDB) to support `upcomingOnly` flag
- [x] 3.4 Update `setup_test.go` for database testing

## 4. Infrastructure Layer (Gemini)

- [x] 4.1 Create `internal/infrastructure/gcp/gemini` package
- [x] 4.2 Implement `GeminiConcertSearcher` with `Search` using Vertex AI grounding

## 5. UseCase Layer

- [x] 5.1 Add `SearchNewConcerts` method to `ConcertUseCase`
- [x] 5.2 Implement discovery logic flow (Get Artist -> Get OfficialSite -> Get Upcoming -> Search New)

## 6. Verification

- [x] 6.1 Unit tests for `ConcertUseCase` filtering logic
- [x] 6.2 Integration tests for `ArtistRepository` schema changes
- [x] 6.3 Manual verification of Gemini search results via CLI
