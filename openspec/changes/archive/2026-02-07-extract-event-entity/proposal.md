
## Why

To support the future expansion of the ticket system to non-musical events (e.g., sports, theater) as outlined in the [Product Roadmap](../specification/docs/product-roadmap.md), the current `Concert` entity is too specific. By extracting generic event properties into a separate `Event` entity, we can increase the versatility of the system while maintaining type safety and data integrity for music-specific events.

## What Changes

- **Create `Event` Entity**: Extract generic fields (ID, Title, VenueID, Date, Times, URL) from `Concert` to a new `Event` struct.
- **Refactor `Concert` Entity**: Modify `Concert` to embed `Event` (Go composition) and retain only music-specific fields (ArtistID).
- **Database Schema Update**: Split the single `concerts` table into `events` (generic) and `concerts` (specific, with 1:1 relation to events).
- **Repository Update**: Update `ConcertRepository` to handle the joined data structure (reading from both tables, writing to both tables transactionally).

## Capabilities

### New Capabilities
- `event-management`: Generic event management capabilities, serving as the foundation for specific event types like Concerts.

### Modified Capabilities
- `concert-service`: The underlying data model for concerts will be refactored to use the new `Event` entity.

## Impact

- **Backend**:
  - `internal/entity/concert.go` (Refactor)
  - `internal/entity/event.go` (New)
  - `internal/infrastructure/database/rdb/concert_repository.go` (Update to use joins/transactions)
- **Database**:
  - New `events` table.
  - Migration to move data from `concerts` to `events` and link them.
