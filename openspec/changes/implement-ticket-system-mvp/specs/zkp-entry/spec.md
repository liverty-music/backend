## ADDED Requirements

### Requirement: Client-Side Proof Generation
The frontend application SHALL generate Zero-Knowledge Proofs (Groth16) entirely on the client side without exposing the private key.

#### Scenario: Proof Generation
- **WHEN** a user requests to generate an entry code
- **THEN** the application SHALL accept user input (Identity Trapdoor) and public inputs (Merkle Root)
- **AND** generate a valid ZK Proof using the `TicketCheck` circuit (WASM)

### Requirement: Off-Chain Verification (Hybrid MVP)
The backend SHALL act as the verifier for entry proofs to ensure low latency and zero gas costs for check-ins.

#### Scenario: Entry Verification
- **WHEN** a user presents the generated proof (QR Code)
- **THEN** the backend SHALL verify the proof against the verification key
- **AND** ensure the `nullifierHash` has not been used previously (Double Entry Check)
