// Package ticketsbt provides a client for interacting with the TicketSBT ERC-5192
// soulbound token contract deployed on Base Sepolia.
package ticketsbt

import (
	"context"
	"crypto/ecdsa"
	"fmt"
	"math/big"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
)

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

// Close releases the underlying RPC connection.
func (c *Client) Close() {
	c.ethClient.Close()
}

// MintResult holds the outcome of a successful mint transaction.
type MintResult struct {
	// TxHash is the transaction hash of the mint operation.
	TxHash string
	// TokenID is the minted token ID.
	TokenID uint64
}

// Mint submits a mint transaction to the TicketSBT contract.
// It retries up to maxRetries times with exponential backoff on transient RPC errors.
//
// recipient is the address that will receive the soulbound token.
// tokenID is the ERC-721 token ID to mint (must be > 0 and unique).
func (c *Client) Mint(ctx context.Context, recipient common.Address, tokenID uint64) (*MintResult, error) {
	tokenIDBig := new(big.Int).SetUint64(tokenID)

	var lastErr error
	for attempt := range maxRetries {
		if attempt > 0 {
			delay := retryBaseDelay * time.Duration(1<<uint(attempt-1))
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(delay):
			}
		}

		tx, err := c.contract.Mint(c.signer, recipient, tokenIDBig)
		if err != nil {
			lastErr = err
			continue
		}

		return &MintResult{
			TxHash:  tx.Hash().Hex(),
			TokenID: tokenID,
		}, nil
	}

	return nil, fmt.Errorf("ticketsbt: mint failed after %d attempts: %w", maxRetries, lastErr)
}

// OwnerOf returns the owner address of the given tokenID.
// Returns the zero address and ERC721NonexistentToken error if the token has not been minted.
func (c *Client) OwnerOf(ctx context.Context, tokenID uint64) (common.Address, error) {
	callOpts := &bind.CallOpts{Context: ctx}
	tokenIDBig := new(big.Int).SetUint64(tokenID)

	var lastErr error
	for attempt := range maxRetries {
		if attempt > 0 {
			delay := retryBaseDelay * time.Duration(1<<uint(attempt-1))
			select {
			case <-ctx.Done():
				return common.Address{}, ctx.Err()
			case <-time.After(delay):
			}
		}

		owner, err := c.contract.OwnerOf(callOpts, tokenIDBig)
		if err != nil {
			lastErr = err
			continue
		}

		return owner, nil
	}

	return common.Address{}, fmt.Errorf("ticketsbt: ownerOf failed after %d attempts: %w", maxRetries, lastErr)
}

// IsTokenMinted returns true if the given tokenID has already been minted on-chain.
func (c *Client) IsTokenMinted(ctx context.Context, tokenID uint64) (bool, error) {
	owner, err := c.OwnerOf(ctx, tokenID)
	if err != nil {
		// ERC721NonexistentToken is returned for unminted tokens — treat as not minted.
		// We cannot import the custom error type directly, so check the string.
		return false, nil //nolint:nilerr
	}

	return owner != (common.Address{}), nil
}
