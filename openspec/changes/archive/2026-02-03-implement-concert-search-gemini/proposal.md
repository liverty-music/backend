# Proposal: Implement Concert Search with Gemini

## Why

Users need to find concert information for their favorite artists easily. The current system lacks a comprehensive search capability. This change aims to integrate Vertex AI Agent (Gemini) to search and extract concert information from artist websites, providing accurate and up-to-date event details.

## What Changes

- Define a `Search` method in the `ConcertRepository` interface in `internal/entity/concert.go`.
- Implement the `Search` method in a new `infrastructure/gcp/gemini` package.
- The implementation will be based on the prototype CLI located at `cmd/prototype-cli/main.go`, utilizing the `google.golang.org/genai` library and Vertex AI Search grounding.

## Capabilities

### New Capabilities

- `concert-search-gemini`: Enables searching for live concert information by artist name using Gemini with grounding.

### Modified Capabilities

<!-- No existing capabilities are modified by this change -->

## Impact

- **Backend**: Adds a new method to the `ConcertRepository` interface and provides a GCP-based implementation.
- **Dependencies**: Uses `google.golang.org/genai`.
