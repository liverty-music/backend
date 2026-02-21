// Package ticketsbt provides a client for interacting with the TicketSBT ERC-5192
// soulbound token contract deployed on Base Sepolia.
package ticketsbt

import (
	"context"
	"crypto/ecdsa"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/liverty-music/backend/internal/entity"
)

// erc721NonexistentTokenSelector is the 4-byte ABI selector for ERC721NonexistentToken(uint256).
// keccak256("ERC721NonexistentToken(uint256)")[0:4] = 0x7e273289
// Used to distinguish "token does not exist" reverts from other RPC failures.
const erc721NonexistentTokenSelector = "7e273289"

// Compile-time check that Client implements entity.TicketMinter.
var _ entity.TicketMinter = (*Client)(nil)

const (
	// baseSepolia is the chain ID for Base Sepolia testnet.
	baseSepolia = 84532

	// maxRetries is the maximum number of RPC call attempts.
	maxRetries = 3
	// retryBaseDelay is the initial backoff delay between RPC retries.
	retryBaseDelay = 500 * time.Millisecond
)

// Client wraps the TicketSBT contract caller and transactor.
type Client struct {
	ethClient   *ethclient.Client
	contract    *TicketSBT
	signer      *bind.TransactOpts
	privateKey  *ecdsa.PrivateKey
	fromAddress common.Address
}

// NewClient creates a new TicketSBT contract client.
//
// rpcURL is the Base Sepolia JSON-RPC endpoint.
// privateKeyHex is the hex-encoded EOA private key that holds MINTER_ROLE.
// contractAddr is the deployed TicketSBT contract address.
func NewClient(ctx context.Context, rpcURL, privateKeyHex, contractAddr string) (*Client, error) {
	if rpcURL == "" || privateKeyHex == "" || contractAddr == "" {
		return nil, fmt.Errorf("ticketsbt: rpcURL, privateKeyHex, and contractAddr are required")
	}

	ethClient, err := ethclient.DialContext(ctx, rpcURL)
	if err != nil {
		return nil, fmt.Errorf("ticketsbt: failed to connect to RPC: %w", err)
	}

	privateKey, err := crypto.HexToECDSA(privateKeyHex)
	if err != nil {
		return nil, fmt.Errorf("ticketsbt: invalid deployer private key: %w", err)
	}

	fromAddress := crypto.PubkeyToAddress(privateKey.PublicKey)

	contract, err := NewTicketSBT(common.HexToAddress(contractAddr), ethClient)
	if err != nil {
		return nil, fmt.Errorf("ticketsbt: failed to bind contract: %w", err)
	}

	signer, err := bind.NewKeyedTransactorWithChainID(privateKey, big.NewInt(baseSepolia))
	if err != nil {
		return nil, fmt.Errorf("ticketsbt: failed to create transactor: %w", err)
	}

	return &Client{
		ethClient:   ethClient,
		contract:    contract,
		signer:      signer,
		privateKey:  privateKey,
		fromAddress: fromAddress,
	}, nil
}

// Close releases the underlying RPC connection. Implements io.Closer.
func (c *Client) Close() error {
	c.ethClient.Close()
	return nil
}

// Mint submits a mint transaction to the TicketSBT contract.
// It retries up to maxRetries times with exponential backoff on transient RPC errors.
//
// recipientAddr is the hex-encoded Ethereum address that will receive the soulbound token.
// tokenID is the ERC-721 token ID to mint (must be > 0 and unique).
func (c *Client) Mint(ctx context.Context, recipientAddr string, tokenID uint64) (string, error) {
	recipient := common.HexToAddress(recipientAddr)
	tokenIDBig := new(big.Int).SetUint64(tokenID)

	// Copy the signer to set per-call context for cancellation/timeout propagation.
	opts := *c.signer
	opts.Context = ctx

	var lastErr error
	for attempt := range maxRetries {
		if attempt > 0 {
			delay := retryBaseDelay * time.Duration(1<<uint(attempt-1))
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(delay):
			}
		}

		tx, err := c.contract.Mint(&opts, recipient, tokenIDBig)
		if err != nil {
			lastErr = err
			continue
		}

		return tx.Hash().Hex(), nil
	}

	return "", fmt.Errorf("ticketsbt: mint failed after %d attempts: %w", maxRetries, lastErr)
}

// OwnerOf returns the owner address of the given tokenID as a lowercase hex string.
// Returns an error if the token does not exist or the RPC call fails.
func (c *Client) OwnerOf(ctx context.Context, tokenID uint64) (string, error) {
	callOpts := &bind.CallOpts{Context: ctx}
	tokenIDBig := new(big.Int).SetUint64(tokenID)

	var lastErr error
	for attempt := range maxRetries {
		if attempt > 0 {
			delay := retryBaseDelay * time.Duration(1<<uint(attempt-1))
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(delay):
			}
		}

		owner, err := c.contract.OwnerOf(callOpts, tokenIDBig)
		if err != nil {
			lastErr = err
			continue
		}

		return strings.ToLower(owner.Hex()), nil
	}

	return "", fmt.Errorf("ticketsbt: ownerOf failed after %d attempts: %w", maxRetries, lastErr)
}

// IsTokenMinted returns true if the given tokenID has already been minted on-chain.
// It distinguishes ERC721NonexistentToken reverts (unminted) from real RPC errors
// by checking for the specific 4-byte ABI selector, not a broad string match.
func (c *Client) IsTokenMinted(ctx context.Context, tokenID uint64) (bool, error) {
	_, err := c.OwnerOf(ctx, tokenID)
	if err != nil {
		// Only treat the specific ERC721NonexistentToken revert as "not minted".
		// All other errors (out-of-gas, wrong contract, network failure) are propagated.
		if strings.Contains(err.Error(), erc721NonexistentTokenSelector) ||
			strings.Contains(err.Error(), "ERC721NonexistentToken") {
			return false, nil
		}
		return false, fmt.Errorf("ticketsbt: failed to check token ownership: %w", err)
	}

	return true, nil
}
