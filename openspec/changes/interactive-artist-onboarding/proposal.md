## Why

To improve the user experience of getting started with the service, we will implement an interactive onboarding flow. This involves separating artist management into its own service, integrating with the Last.fm API for artist discovery, and implementing a follow mechanism for users.

## What Changes

- **New ArtistService**: Separation of artist management from `ConcertService` for better responsibility isolation.
- **Last.fm Integration**: Support for fetching popular and similar artists to enable discovery.
- **Database Schema**: Addition of a `followed_artists` table to persist user preferences.
- **Frontend Components**: Implementation of the onboarding UI in Aurelia 2.

## Capabilities

### New Capabilities
- `artist-following`: Implementation of the follow/unfollow logic and persistence.
- `artist-service-infrastructure`: The architectural setup of the new standalone service.

### Modified Capabilities
- `identity`: Integration with YouTube OAuth scopes if needed for future refinement (referenced from specification).

## Impact

- **Backend**: New Go service and handlers. Database migrations for follow state.
- **Frontend**: New onboarding components and integration with `ArtistService`.
- **Infrastructure**: Additional API keys for Last.fm in configuration.
