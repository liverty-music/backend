## ADDED Requirements

### Requirement: Persist User Follow Actions
The system SHALL persist user follow and unfollow actions for specific artists in a relational database.

#### Scenario: Successfully following an artist
- **WHEN** a user with a valid ID requests to follow an artist with a valid MBID
- **THEN** the system SHALL create a record in the `followed_artists` table linking the user to the artist

### Requirement: Idempotent Unfollow Logic
The system SHALL allow users to unfollow artists, ensuring that the operation is idempotent.

#### Scenario: Unfollowing an artist
- **WHEN** a user requests to unfollow an artist they currently follow
- **THEN** the system SHALL remove the corresponding record from the `followed_artists` table

### Requirement: Follow Status Verification
The system SHALL provide a way to verify if a specific user follows a specific artist.

#### Scenario: Checking follow status
- **WHEN** querying the follow status for a user-artist pair
- **THEN** the system SHALL return a boolean indicating whether the follow record exists
