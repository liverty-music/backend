
## 1. Domain Model Refactoring

- [ ] 1.1 Create `internal/entity/event.go`: Define generic `Event` struct.
- [ ] 1.2 Refactor `internal/entity/concert.go`: Embed `Event` struct and remove duplicate fields.

## 2. Database Migration

- [ ] 2.1 Generate migration file: `create_events_table`.
- [ ] 2.2 Implement migration up: Create `events` table, migrate distinct data from `concerts`, add FK constraint.
- [ ] 2.3 Implement migration down: Reverse the changes (if possible/needed for dev).

## 3. Infrastructure Implementation

- [x] 3.1 Update `internal/infrastructure/database/rdb/concert_repository.go`: Implement `Create` with transaction (insert into `events` then `concerts`).
- [x] 3.2 Update `internal/infrastructure/database/rdb/concert_repository.go`: Implement `ListByArtist` with `JOIN` query.

## 4. Verification

- [x] 4.1 Run existing tests for `ConcertRepository` to ensure backward compatibility in behavior.
- [x] 4.2 Verify database schema correctness after migration.
