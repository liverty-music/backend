## Context

The system currently has a `SearchNewConcerts` method that interacts with an external AI service (Gemini) to find concert information. However, this method currently returns transient entity objects that are discarded after the request. To make this information persistent and searchable, we need to save these objects to the database.

A key challenge is that the external source provides venue _names_ but not our internal venue IDs. We need a way to map these names to existing venues or create new ones on the fly.

## Goals / Non-Goals

**Goals:**

- Persist concerts discovered by `SearchNewConcerts`.
- Automatically resolve venue identities based on name.
- Reuse existing venues when names match to avoid duplication.
- Create new venues when no match is found.

**Non-Goals:**

- Complex venue deduplication (e.g., fuzzy matching, address verification). We will stick to exact name matching for this iteration.
- UI flow for resolving ambiguous venues manually (this is a backend-only change).

## Decisions

### 1. Synchronous Persistence

We will implement the persistence logic directly within the `SearchNewConcerts` method (or a new `SyncConcerts` method wrapping it).
**Rationale:** Simplicity. The operation is already long-running (due to external API calls), so adding a few DB writes is acceptable. It ensures the client gets the final, persisted state.

### 2. "Find or Create" Venue Strategy

We will look up venues by exact name. If not found, a new venue created with just the name.
**Rationale:** Minimal friction. We want to capture the data even if the venue is new.
**Alternative Considered:** Rejecting unknown venues. Rejected because it would require manual intervention for every new venue, defeating the purpose of automation.

## Risks / Trade-offs

- **Risk: Duplicate Venues**: Minor spelling variations ("Tokyo Dome" vs "Tokyo Dome City") will create duplicate venue records.
  - **Mitigation**: Future work can add an admin tool to merge venues or improve the searcher to normalize names.
