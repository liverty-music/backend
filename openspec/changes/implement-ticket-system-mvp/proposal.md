## Why

Ticket scalping and fraud remain major issues in the ticketing industry. While regulatory frameworks exist in Japan, technical enforcement is lagging. We aim to solve this by introducing a next-generation ticket system that leverages **Soulbound Tokens (SBT)** for resale prevention and **Zero-Knowledge Proofs (ZKP)** for privacy-preserving entry, combined with a **Web2-like UX** using Passkeys and PWA.

The initial Browser-Only approach was rejected due to UX and performance concerns. **This Hybrid MVP** introduces a Go backend to handle complex logic (auth, minting, paymaster) and adopts a **PWA-First** strategy to ensure mobile-native UX without the cost of native app development.

## What Changes

We will implement a Hybrid MVP architecture:
- **Backend (Go)**: A new service using Connect RPC and deployed on GKE Autopilot. It handles:
    - **Authentication**: FIDO2 Passkey validation for Smart Account owners.
    - **SBT Minting**: Direct interaction with the `TicketSBT` contract.
    - **Paymaster**: Verifying Paymaster logic to sponsor gas fees for users.
    - **Proof Verification**: Off-chain ZKP verification for high-speed entry.
- **Frontend (Aurelia PWA)**: A PWA built with Aurelia 2, ensuring installability (A2HS) and offline capabilities.
    - **Passkey Login**: Seamless login using platform authenticators.
    - **Offline ZKP**: Client-side proof generation using cached WASM circuits via Service Worker.
- **Smart Contracts (Solidity)**:
    - **TicketSBT**: ERC-5192 Soulbound Token.
    - **Smart Accounts**: ERC-4337 accounts for users (Base Sepolia).

## Capabilities

### New Capabilities
- `ticket-management`: SBT contract logic (ERC-5192), Minting flow, and Metadata management.
- `user-auth`: Passkey (WebAuthn) registration/login and Smart Account mapping.
- `pwa-foundation`: Aurelia 2 setup, Service Worker (Offline support), and Web Manifest (A2HS).
- `zkp-entry`: Client-side proof generation (Groth16), Circuit management, and Verification logic (Go).

### Modified Capabilities
- `continuous-delivery`: Update CI/CD pipelines to support GKE deployment and PWA build artifacts.

## Impact

- **Infrastructure**: Requires GKE Autopilot (Osaka) and Cloud SQL setup in `cloud-provisioning`.
- **Database**: New tables for `users`, `events`, `tickets`, `merkle_tree`, `nullifiers`.
- **Dependencies**: New Go modules (`connectrpc`, `go-ethereum`, `gnark`/`rapidsnark`) and JS packages (`simplewebauthn`, `snarkjs`).
