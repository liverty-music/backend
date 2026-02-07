
## ADDED Requirements

### Requirement: Concert-Event Association
Every Concert entity SHALL be securely linked to a distinct generic Event entity.

#### Scenario: Concert Data Integrity
- **WHEN** a Concert is persisted or retrieved
- **THEN** it MUST include all fields defined in the `Event` entity (Title, Date, Venue, etc.)
- **AND** data consistency between the Concert specific fields (ArtistID) and Event generic fields MUST be maintained
