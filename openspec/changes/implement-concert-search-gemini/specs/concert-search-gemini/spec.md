# Spec: Concert Search with Gemini

## ADDED Requirements

### Requirement: Search Concerts by Artist

System must provide a way to search for future concerts of a specific artist using generative AI grounding.

#### Scenario: Successful Search

- **WHEN** a user searches for concerts for an existing artist
- **THEN** the system returns a list of upcoming concerts found on the web
- **AND** each concert includes title, venue, date, and start time
- **AND** results exclude concerts that are already stored in the database

#### Scenario: Filter Past Events

- **WHEN** the search results include events with dates in the past
- **THEN** the system must filter them out and only return future events

#### Scenario: No Results

- **WHEN** no upcoming concerts are found for the artist
- **THEN** the system returns an empty list without error
