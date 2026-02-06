## ADDED Requirements

### Requirement: Soulbound Token Properties
The `TicketSBT` contract SHALL implement ERC-721 and ERC-5192 standards to ensure tokens are non-transferable (Soulbound) by default, while allowing authorized burning for resale.

#### Scenario: Transfer restricted
- **WHEN** a user attempts to transfer a TicketSBT to another address
- **THEN** the transaction SHALL revert with a "SBT: Ticket transfer is prohibited" error

#### Scenario: Locked Event Emission
- **WHEN** a token is minted or its status is locked
- **THEN** a `Locked` event SHALL be emitted as per ERC-5192

### Requirement: Minting Logic
The contract SHALL allow only authorized minters (Backend Service) to mint tickets required for the Hybrid MVP architecture.

#### Scenario: Authorized Minting
- **WHEN** the authorized backend service calls the mint function
- **THEN** a new TicketSBT SHALL be assigned to the recipient's Smart Account address
