## ADDED Requirements

### Requirement: Concert Persistence

The system SHALL automatically persist any new concerts discovered via the search mechanism.

#### Scenario: Persist New Concerts

- **WHEN** `SearchNewConcerts` is called and finds concerts not currently in the database
- **THEN** the new concerts are saved to the persisted storage
- **AND** returned in the response with valid IDs

#### Scenario: Persist Venues

- **WHEN** a discovered concert has a venue that does not exist in the database
- **THEN** a new venue is created dynamically based on the name provided by the source
- **AND** the new concert is associated with this new venue
