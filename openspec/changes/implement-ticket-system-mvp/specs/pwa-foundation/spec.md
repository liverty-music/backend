## ADDED Requirements

### Requirement: Installability (A2HS)
The frontend application SHALL satisfy PWA criteria to allow installation on user devices (iOS/Android/Desktop).

#### Scenario: Manifest Configuration
- **WHEN** the browser loads the application
- **THEN** it SHALL detect a valid `manifest.json` with correct icons, display mode (standalone), and start URL
- **AND** the browser built-in install prompt SHOULD be triggerable (where supported)

### Requirement: Offline Capability
The application SHALL function in offline environments, particularly for critical flows like ticket display and entry code generation.

#### Scenario: Service Worker Caching
- **WHEN** the application is loaded for the first time
- **THEN** core assets (HTML, CSS, JS) and ZK Circuit files (WASM, ZKey) SHALL be cached by a Service Worker
- **AND** subsequent loads SHALL utilize the cache, functioning without network connectivity
