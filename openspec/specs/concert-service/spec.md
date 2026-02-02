# concert-service Specification

## Purpose

TBD - created by archiving change implement-concert-service. Update Purpose after archive.

## Requirements

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

### Requirement: Search Concerts by Artist

System must provide a way to search for future concerts of a specific artist using generative AI grounding.

#### Scenario: Successful Search

- **WHEN** `SearchNewConcerts` is called for an existing artist
- **THEN** the system returns a list of upcoming concerts found on the web
- **AND** each concert includes title, venue, date, and start time
- **AND** results exclude concerts that are already stored in the database

#### Scenario: Filter Past Events

- **WHEN** the search results include events with dates in the past
- **THEN** the system must filter them out and only return future events

#### Scenario: No Results

- **WHEN** no upcoming concerts are found for the artist
- **THEN** the system returns an empty list without error
