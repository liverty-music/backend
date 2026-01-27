## ADDED Requirements

### Requirement: Concert Service

The system SHALL provide a gRPC service to manage concerts and artists.

#### Scenario: List Concerts by Artist

- **WHEN** `List` is called with a valid `artist_id`
- **THEN** it returns a list of concerts associated with that artist
- **AND** returns an empty list if no concerts are found (not an error)

#### Scenario: List All Artists

- **WHEN** `ListArtists` is called
- **THEN** it returns a list of all artists in the system

#### Scenario: Create Artist

- **WHEN** `CreateArtist` is called with a valid name
- **THEN** a new artist is created and returned with a generated ID
- **AND** the artist is persistable

#### Scenario: Create Artist with Invalid Name

- **WHEN** `CreateArtist` is called with an empty name
- **THEN** it returns an `INVALID_ARGUMENT` error

#### Scenario: Add Media to Artist

- **WHEN** `CreateArtistMedia` is called with valid `artist_id`, `type`, and `url`
- **THEN** the media link is associated with the artist

#### Scenario: Remove Media from Artist

- **WHEN** `DeleteArtistMedia` is called with a valid `media_id`
- **THEN** the media link is removed
