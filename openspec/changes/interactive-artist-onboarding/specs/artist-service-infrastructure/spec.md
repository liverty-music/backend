## ADDED Requirements

### Requirement: Standalone Artist Service
The system SHALL provide a dedicated `ArtistService` that is independent of the `ConcertService` for managing artist-related operations.

#### Scenario: Service initialization
- **WHEN** the backend application starts
- **THEN** the `ArtistService` SHALL be registered as a separate RPC handler with its own set of dependencies (repositories, external clients)

### Requirement: Unified Music Discovery Interface
The system SHALL provide a unified way to search for artists across multiple providers (local database, Last.fm, MusicBrainz).

#### Scenario: Multi-provider search
- **WHEN** a search request is received
- **THEN** the system SHALL combine and normalize results from multiple sources into a consistent `Artist` entity
