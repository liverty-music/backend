## ADDED Requirements

### Requirement: Passkey Registration
The system SHALL allowing users to register a FIDO2 Passkey (WebAuthn) to create and access their account.

#### Scenario: Registration Request
- **WHEN** a user initiates registration
- **THEN** the backend SHALL return `PublicKeyCredentialCreationOptions`
- **AND** the frontend SHALL prompt the user to create a passkey

#### Scenario: Registration Verification
- **WHEN** the user submits the signed credential
- **THEN** the backend SHALL verify the signature against the challenge
- **AND** create a User record if valid

### Requirement: Smart Account Mapping
The system SHALL strictly map a Passkey credential to a Smart Account (Wallet) address.

#### Scenario: Account Derivation / Creation
- **WHEN** a user registers successfully
- **THEN** a predicted Safe (ERC-4337) address SHALL be associated with the user
- **AND** this address SHALL be used for all future blockchain interactions
