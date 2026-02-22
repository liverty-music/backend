# contracts/

Solidity smart contracts for the Liverty Music ticket system, managed with [Foundry](https://book.getfoundry.sh/).

## Structure

```
contracts/
├─ foundry.toml          Foundry project config (remappings, optimizer, etc.)
├─ lib/                  Dependencies installed via forge install
│   └─ openzeppelin-contracts/
├─ src/
│   └─ TicketSBT.sol    ERC-721 + ERC-5192 Soulbound Ticket contract
├─ test/
│   └─ TicketSBT.t.sol  Foundry tests
└─ script/
    └─ Deploy.s.sol     Deployment script (Base Sepolia)
```

The compiled ABI is consumed by the Go backend via `abigen`:

```
contracts/out/TicketSBT.sol/TicketSBT.json   ← forge build output
        │
        └─▶  go generate ./internal/infrastructure/blockchain/ticketsbt/...
                    │
                    └─▶  internal/infrastructure/blockchain/ticketsbt/TicketSBT.go
```

## Contracts

### TicketSBT

ERC-721 token with ERC-5192 Soulbound semantics. Tokens are non-transferable by default.

| Feature  | Detail                                                      |
|----------|-------------------------------------------------------------|
| Standard | ERC-721 + ERC-5192                                          |
| Transfer | Reverts with `"SBT: Ticket transfer is prohibited"`         |
| Minting  | Restricted to `MINTER_ROLE` (backend service EOA)           |
| Events   | `Locked(tokenId)` emitted on every mint                     |
| Network  | Base Sepolia (testnet), Base Mainnet (production)           |

---

## Prerequisites

### Install Foundry

[Foundry](https://book.getfoundry.sh/) is required for building, testing, and deploying contracts.

```bash
curl -L https://foundry.paradigm.xyz | bash
foundryup
```

Verify:

```bash
cast --version   # cast 0.x.x (...)
forge --version  # forge 0.x.x (...)
```

### Install `abigen` (for Go bindings)

`abigen` generates Go contract bindings from the compiled ABI:

```bash
go install github.com/ethereum/go-ethereum/cmd/abigen@latest
```

Verify:

```bash
abigen --version  # abigen version 1.x.x-...
```

Ensure `$GOPATH/bin` is in your `PATH`:

```bash
export PATH="$PATH:$(go env GOPATH)/bin"
```

---

## Quick Start

```bash
cd contracts

# Install Solidity dependencies
forge install

# Compile
forge build

# Run tests
forge test -vv
```

---

## Generate Go Bindings

Run from the **repository root** after `forge build`:

```bash
go generate ./internal/infrastructure/blockchain/ticketsbt/...
```

This is equivalent to:

```bash
abigen \
  --abi contracts/out/TicketSBT.sol/TicketSBT.json \
  --pkg ticketsbt \
  --out internal/infrastructure/blockchain/ticketsbt/TicketSBT.go
```

The `//go:generate` directive lives in `internal/infrastructure/blockchain/ticketsbt/generate.go`.

---

## Deploy to Base Sepolia

Before deploying, ensure you have:
- A funded deployer EOA (see [cloud-provisioning/docs/TICKET_SYSTEM_SETUP.md](../../cloud-provisioning/docs/TICKET_SYSTEM_SETUP.md) Steps 1–2)
- A Base Sepolia RPC URL (see Step 3 of the same guide)

```bash
cd contracts
forge script script/Deploy.s.sol \
  --rpc-url <BASE_SEPOLIA_RPC_URL> \
  --private-key <DEPLOYER_PRIVATE_KEY> \
  --broadcast
```

The script output includes the deployed contract address:

```
== Logs ==
TicketSBT deployed at: 0x1234...abcd
```

Record this address — it is required by the Go backend (`TICKET_SBT_CONTRACT_ADDRESS` env var or config).

### Grant MINTER_ROLE

After deployment, grant `MINTER_ROLE` to the deployer EOA so it can mint tickets:

```bash
cast send <CONTRACT_ADDRESS> \
  "grantRole(bytes32,address)" \
  "$(cast keccak "MINTER_ROLE")" \
  <DEPLOYER_ADDRESS> \
  --rpc-url <BASE_SEPOLIA_RPC_URL> \
  --private-key <DEPLOYER_PRIVATE_KEY>
```

---

## Troubleshooting

### `cast` / `forge: command not found`

```bash
foundryup
exec $SHELL
```

### `abigen: command not found`

```bash
go install github.com/ethereum/go-ethereum/cmd/abigen@latest
export PATH="$PATH:$(go env GOPATH)/bin"
```

### `forge build` fails: `lib/openzeppelin-contracts not found`

```bash
cd contracts
forge install OpenZeppelin/openzeppelin-contracts
```
