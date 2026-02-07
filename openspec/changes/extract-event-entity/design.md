
## Context

The current `Concert` entity in `internal/entity/concert.go` monolithically stores all event-related data. As per the product roadmap, we anticipate supporting non-musical events. To facilitate this, we need a normalized data model where generic event attributes are separated from concert-specific attributes.

## Goals / Non-Goals

**Goals:**
- Extract generic fields (`Title`, `VenueID`, `LocalEventDate`, `StartTime`, `OpenTime`, `SourceURL`) into a reusable `Event` entity.
- Refactor `Concert` to embed `Event`, adding `ArtistID`.
- Normalize the database schema into `events` and `concerts` tables with a 1:1 relationship.
- Ensure `ConcertRepository` transparently handles the split data model using transactions for writes and JOINs for reads.

**Non-Goals:**
- Implementing specific logic for other event types (e.g., Sports, Theater) in this change.
- Changing the external Protobuf API contract (Client-facing APIs remain unchanged).

## Implementation Details

- **Go Struct Embedding**: The `Concert` struct embeds the `Event` struct to inherit common fields. This provides ergonomic access (e.g., `concert.ID`, `concert.Title`) while maintaining type separation. The embedded fields are promoted, allowing direct access.
- **Naming Convention**:
    - **Database**: Use `_at` suffix (e.g., `start_at`, `created_at`) and `TIMESTAMPTZ` type.
    - **Go Entity**: Use `Time` suffix (e.g., `StartTime`, `CreateTime`) and `time.Time` type.
    - **Repository**: Handles mapping between DB columns and Go structs.
- **Database Schema**:
    - `events` table: Stores generic data (ID, VenueID, Title, LocalEventDate, StartAt, OpenAt, etc.).
    - `concerts` table: Stores specific data (EventID [PK/FK], ArtistID).
    - Relationship: 1:1, enforced by FK on `concerts.event_id`.
- **Repository Pattern**:
    - `Create`: Uses a transaction to insert into both `events` and `concerts`.
    - `List`: Uses `JOIN` to retrieve aggregated data.

## Risks / Trade-offs

- **Risk: Migration Complexity**: Existing `concerts` table has data.
  - **Mitigation**: Use a documented migration strategy (Create `events` -> Backfill from `concerts` -> Alter `concerts`).
- **Trade-off: Write Cost**: Inserts now require 2 queries instead of 1.
  - **Acceptance**: The volume of concert creation is low compared to reads, so the overhead is negligible.
- **Trade-off: Read Cost**: Reads now require a JOIN.
  - **Acceptance**: Join on indexed PK is highly efficient.

## Migration Plan

1. Create `events` table.
2. Insert distinct event data from `concerts` into `events`.
3. Drop generic columns from `concerts` and add FK constraint to `events`. (Or create new `concerts_new` and swap).
   *Decision*: Altering existing table is riskier. We will create `events` schema, then a migration script will handle the data transfer.
