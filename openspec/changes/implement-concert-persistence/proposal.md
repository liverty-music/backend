# Proposal: Implement Concert Persistence

## Why

Currently, the `SearchNewConcerts` method discovers concerts using external sources (Gemini) and returns them as entity objects, but it does not save them to the database. This means the discovered data is lost immediately after the request. To make this feature useful, we need to persist the discovered concerts and their associated venues so they can be queried later (e.g., via `ListByArtist`).

## What Changes

- Update `ConcertUseCase.SearchNewConcerts` to persist discovered concerts.
- Implement "Find or Create" logic for Venues during the persistence process, as the search results only provide venue names.
- Update `VenueRepository` to support lookups by name.
- Ensure that `SearchNewConcerts` returns the _persisted_ entities (with valid IDs).

## Capabilities

### Modified Capabilities

- `concert-service`: Update the Search requirement to explicitly state that discovered concerts are persisted to the database.

## Impact

- **Code**: `ConcertUseCase`, `VenueRepository`.
- **Database**: New rows in `concerts` and `venues` tables when `SearchNewConcerts` is called.
