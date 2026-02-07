## 1. Specification & Schema

- [x] 1.1 Create `proto/liverty_music/rpc/v1/artist_service.proto` with simplified RPC names (`List`, `Create`, `Search`, `Follow`, etc.)
- [x] 1.2 Refactor `proto/liverty_music/rpc/v1/concert_service.proto` to remove artist management RPCs
- [x] 1.3 Add `followed_artists` table migration (PostgreSQL)

## 2. Backend - ArtistService Implementation

- [ ] 2.1 Implement `ArtistService.List` and `ArtistService.Create` (moved from ConcertService)
- [ ] 2.2 Implement `ArtistService.Search` with incremental search support
- [ ] 2.3 Implement `ArtistService.Follow` and `ArtistService.Unfollow` logic (updates `followed_artists` table)
- [ ] 2.4 Implement Last.fm API client for `geo.getTopArtists` and `artist.getSimilar`
- [ ] 2.5 Implement `ArtistService.ListTop` and `ArtistService.ListSimilar` using Last.fm client

## 3. Frontend - Discovery UI

- [ ] 3.1 Create `OnboardingDiscovery` Aurelia 2 component
- [ ] 3.2 Implement initial view fetching `ListTop` artists
- [ ] 3.3 Implement search form with real-time `Search` calls
- [ ] 3.4 Implement chain-follow logic (click Follow -> trigger `ListSimilar` -> refresh list)
- [ ] 3.5 Implement "Reset" button to return to `ListTop` view
- [ ] 3.6 Add smooth transitions for list updates and focus states

## 4. Integration & Routing

- [ ] 4.1 Update `MyApp` routes to include `/onboarding`
- [ ] 4.2 Ensure `AuthCallback` redirects to `/onboarding` for new users (if needed)

## 5. Verification

- [ ] 5.1 Verify `ArtistService` RPCs via manual calls or unit tests
- [ ] 5.2 Verify full onboarding flow: Search -> Follow -> Similar list -> Follow -> Reset
- [ ] 5.3 Verify database persistence for followed artists
