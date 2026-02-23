// Package ticketsbt provides a client for interacting with the TicketSBT ERC-5192
// soulbound token contract.
package ticketsbt

import (
	"context"
	"crypto/ecdsa"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"strings"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/pannpers/go-logging/logging"
)

// erc721NonexistentTokenSelector is the 4-byte ABI selector for ERC721NonexistentToken(uint256).
// keccak256("ERC721NonexistentToken(uint256)")[0:4] = 0x7e273289
// Used to distinguish "token does not exist" reverts from other RPC failures.
const erc721NonexistentTokenSelector = "7e273289"

// Compile-time check that Client implements entity.TicketMinter.
var _ entity.TicketMinter = (*Client)(nil)

const (
	// maxRetries is the maximum number of RPC call attempts.
	maxRetries = 3
	// retryBaseDelay is the initial backoff delay between RPC retries.
	retryBaseDelay = 500 * time.Millisecond
)

// Client wraps the TicketSBT contract caller and transactor.
type Client struct {
	mu          sync.Mutex
	ethClient   *ethclient.Client
	contract    *TicketSBT
	signer      *bind.TransactOpts
	privateKey  *ecdsa.PrivateKey
	fromAddress common.Address
	logger      *logging.Logger
}

// NewClient creates a new TicketSBT contract client.
//
// rpcURL is the JSON-RPC endpoint for the target EVM chain.
// privateKeyHex is the hex-encoded EOA private key that holds MINTER_ROLE.
// contractAddr is the deployed TicketSBT contract address.
// chainID is the EIP-155 chain ID used for transaction signing (e.g., 84532 for Base Sepolia).
func NewClient(ctx context.Context, rpcURL, privateKeyHex, contractAddr string, chainID int64, logger *logging.Logger) (*Client, error) {
	if rpcURL == "" || privateKeyHex == "" || contractAddr == "" {
		return nil, fmt.Errorf("ticketsbt: rpcURL, privateKeyHex, and contractAddr are required")
	}
	if chainID <= 0 {
		return nil, fmt.Errorf("ticketsbt: chainID must be positive")
	}

	l := logger.With(slog.String("component", "ticketsbt"))

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

	signer, err := bind.NewKeyedTransactorWithChainID(privateKey, big.NewInt(chainID))
	if err != nil {
		return nil, fmt.Errorf("ticketsbt: failed to create transactor: %w", err)
	}

	l.Info(ctx, "blockchain client initialized",
		slog.String("contractAddr", contractAddr),
		slog.Int64("chainID", chainID),
	)

	return &Client{
		ethClient:   ethClient,
		contract:    contract,
		signer:      signer,
		privateKey:  privateKey,
		fromAddress: fromAddress,
		logger:      l,
	}, nil
}

// Close releases the underlying RPC connection. Implements io.Closer.
func (c *Client) Close() error {
	c.ethClient.Close()
	return nil
}

// isTransientError reports whether err represents a transient (retryable) RPC failure.
// Permanent errors such as execution reverts, insufficient funds, nonce conflicts, and
// gas estimation failures are not retryable and return false.
func isTransientError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	permanentSubstrings := []string{
		"execution reverted",
		"insufficient funds",
		"nonce too low",
		"gas required exceeds allowance",
	}
	for _, s := range permanentSubstrings {
		if strings.Contains(msg, s) {
			return false
		}
	}
	return true
}

// Mint submits a mint transaction to the TicketSBT contract and waits for on-chain
// confirmation. It retries up to maxRetries times with exponential backoff on transient
// RPC errors. Permanent errors (execution reverts, insufficient funds, etc.) are
// returned immediately without retrying.
//
// recipientAddr is the hex-encoded Ethereum address that will receive the soulbound token.
// tokenID is the ERC-721 token ID to mint (must be > 0 and unique).
func (c *Client) Mint(ctx context.Context, recipientAddr string, tokenID uint64) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	recipient := common.HexToAddress(recipientAddr)
	tokenIDBig := new(big.Int).SetUint64(tokenID)

	// Copy the signer to set per-call context for cancellation/timeout propagation.
	opts := *c.signer
	opts.Context = ctx

	var lastErr error
	for attempt := range maxRetries {
		if attempt > 0 {
			delay := retryBaseDelay * time.Duration(1<<uint(attempt-1))
			c.logger.Debug(ctx, "mint retry backoff",
				slog.Uint64("tokenID", tokenID),
				slog.Int("attempt", attempt+1),
				slog.Int("maxAttempts", maxRetries),
				slog.String("error", lastErr.Error()),
			)
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(delay):
			}
		}

		// Fetch a fresh nonce on each attempt to avoid stale nonce after retries.
		nonce, err := c.ethClient.PendingNonceAt(ctx, c.fromAddress)
		if err != nil {
			if !isTransientError(err) {
				return "", fmt.Errorf("ticketsbt: permanent error fetching nonce: %w", err)
			}
			lastErr = fmt.Errorf("ticketsbt: failed to fetch pending nonce: %w", err)
			continue
		}
		opts.Nonce = new(big.Int).SetUint64(nonce)

		tx, err := c.contract.Mint(&opts, recipient, tokenIDBig)
		if err != nil {
			if !isTransientError(err) {
				return "", fmt.Errorf("ticketsbt: permanent mint error: %w", err)
			}
			lastErr = err
			continue
		}

		// Wait for the transaction to be mined and verify on-chain success.
		receipt, err := bind.WaitMined(ctx, c.ethClient, tx)
		if err != nil {
			if !isTransientError(err) {
				return "", fmt.Errorf("ticketsbt: permanent error waiting for receipt: %w", err)
			}
			lastErr = err
			continue
		}
		if receipt.Status != types.ReceiptStatusSuccessful {
			return "", fmt.Errorf("ticketsbt: mint transaction reverted on-chain (tx=%s)", tx.Hash().Hex())
		}

		txHash := tx.Hash().Hex()
		c.logger.Info(ctx, "ticket minted",
			slog.Uint64("tokenID", tokenID),
			slog.String("recipient", recipientAddr),
			slog.String("txHash", txHash),
		)
		return txHash, nil
	}

	c.logger.Error(ctx, "mint failed after retries", lastErr,
		slog.Uint64("tokenID", tokenID),
		slog.String("recipient", recipientAddr),
		slog.Int("attempts", maxRetries),
	)
	return "", fmt.Errorf("ticketsbt: mint failed after %d attempts: %w", maxRetries, lastErr)
}

// OwnerOf returns the owner address of the given tokenID as a lowercase hex string.
// Returns an error if the token does not exist or the RPC call fails.
func (c *Client) OwnerOf(ctx context.Context, tokenID uint64) (string, error) {
	callOpts := &bind.CallOpts{Context: ctx}
	tokenIDBig := new(big.Int).SetUint64(tokenID)

	c.logger.Debug(ctx, "querying token owner", slog.Uint64("tokenID", tokenID))

	var lastErr error
	for attempt := range maxRetries {
		if attempt > 0 {
			delay := retryBaseDelay * time.Duration(1<<uint(attempt-1))
			c.logger.Debug(ctx, "ownerOf retry backoff",
				slog.Uint64("tokenID", tokenID),
				slog.Int("attempt", attempt+1),
				slog.Int("maxAttempts", maxRetries),
				slog.String("error", lastErr.Error()),
			)
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

	c.logger.Error(ctx, "ownerOf failed after retries", lastErr,
		slog.Uint64("tokenID", tokenID),
		slog.Int("attempts", maxRetries),
	)
	return "", fmt.Errorf("ticketsbt: ownerOf failed after %d attempts: %w", maxRetries, lastErr)
}

// IsTokenMinted returns true if the given tokenID has already been minted on-chain.
// It distinguishes ERC721NonexistentToken reverts (unminted) from real RPC errors by
// extracting the 4-byte ABI error selector from the RPC error data, which is more
// robust than string matching against error messages that may change across go-ethereum
// versions.
func (c *Client) IsTokenMinted(ctx context.Context, tokenID uint64) (bool, error) {
	_, err := c.OwnerOf(ctx, tokenID)
	if err != nil {
		if isERC721NonexistentTokenError(err) {
			return false, nil
		}
		c.logger.Warn(ctx, "unexpected error checking token existence",
			slog.Uint64("tokenID", tokenID),
			slog.String("error", err.Error()),
		)
		return false, fmt.Errorf("ticketsbt: failed to check token ownership: %w", err)
	}

	return true, nil
}

// isERC721NonexistentTokenError checks whether err is an ERC721NonexistentToken revert.
// It first attempts to extract the 4-byte ABI error selector from the RPC error data
// (via rpc.DataError interface), falling back to string matching as a last resort.
func isERC721NonexistentTokenError(err error) bool {
	// Try structured error data first (robust across go-ethereum versions).
	var dataErr rpc.DataError
	if errors.As(err, &dataErr) {
		if data, ok := dataErr.ErrorData().(string); ok {
			// RPC returns error data as a hex string (e.g., "0x7e273289...").
			data = strings.TrimPrefix(data, "0x")
			if len(data) >= 8 && data[:8] == erc721NonexistentTokenSelector {
				return true
			}
		}
	}

	// Try extracting selector from raw hex in the error chain.
	// Some wrapped errors embed the revert data as hex bytes in the message.
	var rawErr interface{ ErrorData() interface{} }
	if errors.As(err, &rawErr) {
		if rawData, ok := rawErr.ErrorData().([]byte); ok && len(rawData) >= 4 {
			selector := hex.EncodeToString(rawData[:4])
			if selector == erc721NonexistentTokenSelector {
				return true
			}
		}
	}

	// Fallback: string matching for compatibility with error formats that don't
	// implement DataError (e.g., wrapped errors from middleware).
	msg := err.Error()
	return strings.Contains(msg, erc721NonexistentTokenSelector) ||
		strings.Contains(msg, "ERC721NonexistentToken")
}
