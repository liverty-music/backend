# Change: Implement Concert Service Backend

## Why

The `liverty_music.rpc.v1` package defines the API contract for managing concerts and artists, but the backend implementation is currently missing. Implementing `ConcertService` is critical for enabling the core functionality of the Liverty Music platform, allowing clients to list concerts and manage artist data.

## What Changes

- Enable the `concert-service` capability by implementing the defined RPCs.
- Provide `List` and `ListArtists` functionality for retrieving live event data.
- Provide `CreateArtist`, `CreateArtistMedia`, and `DeleteArtistMedia` functionality for managing artist data.

## Impact

- **Affected Specs**: Adds `concert-service` capability.
- **Affected Code**: Backend services and repositories (`internal/`).
- **Breaking Changes**: None.
