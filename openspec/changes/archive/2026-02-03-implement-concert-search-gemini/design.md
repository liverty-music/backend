# Design: Concert Search with Gemini

## Context

The system currently manages concert data stored in a PostgreSQL database. To enhance user experience, we need to allow users to search for upcoming concerts validation external sources (artist websites) using Generative AI.

## Goals / Non-Goals

**Goals:**

- Enable searching for concerts by artist name using Vertex AI (Gemini) with Grounding.
- Integrate the search capability into the existing backend architecture.
- Follow Clean Architecture principles while adhering to the specific request of modifying `ConcertRepository`.

**Non-Goals:**

- Storing the search results automatically (this is a separate "import" capability).
- Advanced filtering beyond "future events" and "exclude existing".

## Decisions

### 1. ConcertRepository Interface Update

- **Decision**: Update `entity.ConcertRepository` to include a filter for upcoming events.
  ```go
  ListByArtist(ctx context.Context, artistID string, upcomingOnly bool) ([]*Concert, error)
  ```
- **Rationale**: The UseCase needs to retrieve only planned future events to distinguish them from newly discovered ones.

### 2. ConcertSearcher Interface (New)

- **Decision**: Create a new domain interface `entity.ConcertSearcher` for external discovery.
  ```go
  Search(ctx context.Context, artist *Artist, officialSite *OfficialSite, from time.Time, excluded []*Concert) ([]*Concert, error)
  ```
- **Rationale**: Decouples external web search logic from the Repository pattern. Passing `OfficialSite` explicitly ensures the searcher has the grounding URL it needs.

### 3. Infrastructure Implementation (`infrastructure/gcp/gemini`)

- **Decision**: Create a new package `infrastructure/gcp/gemini` implementing `ConcertSearcher`.
- **Details**:
  - The `GeminiConcertSearcher` struct will implement `Search` using the `google.golang.org/genai` library with Vertex AI Grounding.
- **Configuration**: Project ID, Location, Model Name, and Data Store ID will be configured via environment variables.

### 4. Logic & Filtering (ConcertUseCase)

- **Decision**: The `ConcertUseCase.SearchNewConcerts(artistID)` method will coordinate the discovery flow.
- **Flow**:
  1. Retrieve the `Artist` entity using `ArtistRepository.Get(artistID)`.
  2. Retrieve the `OfficialSite` entity using `ArtistRepository.GetOfficialSite(artistID)`.
  3. Retrieve existing upcoming concerts via `ConcertRepository.ListByArtist(artistID, true)`.
  4. Call `ConcertSearcher.Search` passing the artist, official site, current time, and existing concerts to exclude.
  5. Return the list of newly discovered concerts.

### 5. Domain Entity Cleanup

- **Decision**: Cleanup `Artist` and `Concert` entities to match Protobuf definitions.
- **Rationale**: Keeps the domain model lean and consistent with the external API spec.
- **Changes**:
  - Remove fields from `Artist`: `SpotifyID`, `MusicBrainzID`, `Genres`, `Country`, `ImageURL`, `CreatedAt`, `UpdatedAt`.
  - Remove fields from `Concert`: `VenueName`, `VenueCity`, `VenueCountry`, `VenueAddress`, `EventDate`, `TicketURL`, `Price`, `Currency`, `Status`, `CreatedAt`, `UpdatedAt`.
  - Remove helper functions like `NewArtist`.

### 5. Artist Metadata Simplification

- **Decision**: Replace `artist_media` table/entity with `artist_official_site`.
- **Rationale**: The system primarily needs the official website to perform concert search via Gemini Grounding. Storing other social media links in a dedicated table is unnecessary complexity for the current scope.
- **Schema Changes**:
  - DROP `artist_media` table.
  - CREATE `artist_official_site` table (1:1 relationship with `artists`).
- **Entity Changes**:
  - Remove `Media` and `MediaType` from `Artist`.
  - Add `OfficialSite` entity linked to `Artist`.
  - Update `ArtistRepository` to use `CreateOfficialSite` and `GetOfficialSite`.

## Risks / Trade-offs

- **Interface Segregation**: Adding `Search` to `ConcertRepository` forces all implementations (DB) to implement it, even if irrelevant.
  - **Mitigation**: Use stub implementations. In the future, we should split `ConcertRepository` into `ConcertReader`, `ConcertWriter`, and `ConcertSearcher`.
- **Gemini Cost/Latency**: External search is slower and costlier than DB queries.
  - **Mitigation**: UseCase should cache results or use this feature sparingly (user-triggered).
- **Breaking Change**: Removing `artist_media` removes support for social media links (Twitter, Instagram).
  - **Mitigation**: Accepting this as a requirement to simplify metadata for search grounding.
