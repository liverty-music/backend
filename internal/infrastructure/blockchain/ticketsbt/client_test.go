package ticketsbt_test

import (
	"context"
	"crypto/ecdsa"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/liverty-music/backend/internal/infrastructure/blockchain/ticketsbt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testPrivateKey is a throwaway private key for unit testing (never used on-chain).
const testPrivateKey = "ac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80"

// testContractAddr is a dummy contract address for unit testing.
const testContractAddr = "0x5FbDB2315678afecb367f032d93F642f64180aa3"

// jsonRPCRequest represents a JSON-RPC 2.0 request.
type jsonRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
	ID      json.RawMessage `json:"id"`
}

// jsonRPCResponse represents a JSON-RPC 2.0 response.
type jsonRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  interface{}     `json:"result,omitempty"`
	Error   *jsonRPCError   `json:"error,omitempty"`
}

type jsonRPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

// newTestRPCServer creates an httptest server that responds to JSON-RPC calls.
// The handler func receives the method and params and returns (result, error).
func newTestRPCServer(t *testing.T, handler func(method string, params json.RawMessage) (interface{}, *jsonRPCError)) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req jsonRPCRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}

		result, rpcErr := handler(req.Method, req.Params)
		resp := jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  result,
			Error:   rpcErr,
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
}

// abiEncodeAddress ABI-encodes an address as a 32-byte hex string (0x-prefixed).
func abiEncodeAddress(addr common.Address) string {
	addrType, _ := abi.NewType("address", "", nil)
	args := abi.Arguments{{Type: addrType}}
	encoded, _ := args.Pack(addr)
	return "0x" + common.Bytes2Hex(encoded)
}

func TestNewClient_InvalidInputs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		rpcURL     string
		privateKey string
		contract   string
		wantErr    string
	}{
		{
			name:       "empty rpcURL",
			rpcURL:     "",
			privateKey: testPrivateKey,
			contract:   testContractAddr,
			wantErr:    "rpcURL, privateKeyHex, and contractAddr are required",
		},
		{
			name:       "empty privateKey",
			rpcURL:     "http://localhost:8545",
			privateKey: "",
			contract:   testContractAddr,
			wantErr:    "rpcURL, privateKeyHex, and contractAddr are required",
		},
		{
			name:       "empty contractAddr",
			rpcURL:     "http://localhost:8545",
			privateKey: testPrivateKey,
			contract:   "",
			wantErr:    "rpcURL, privateKeyHex, and contractAddr are required",
		},
		{
			name:       "invalid private key hex",
			rpcURL:     "http://localhost:8545",
			privateKey: "not-hex",
			contract:   testContractAddr,
			wantErr:    "invalid deployer private key",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := ticketsbt.NewClient(context.Background(), tc.rpcURL, tc.privateKey, tc.contract)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantErr)
		})
	}
}

func TestNewClient_Success(t *testing.T) {
	t.Parallel()

	srv := newTestRPCServer(t, func(method string, _ json.RawMessage) (interface{}, *jsonRPCError) {
		// NewClient dials but doesn't make RPC calls during construction.
		return nil, nil
	})
	defer srv.Close()

	client, err := ticketsbt.NewClient(context.Background(), srv.URL, testPrivateKey, testContractAddr)
	require.NoError(t, err)
	assert.NotNil(t, client)
	require.NoError(t, client.Close())
}

func TestIsTokenMinted_NotMinted(t *testing.T) {
	t.Parallel()

	// Simulate ERC721NonexistentToken revert for ownerOf call.
	// go-ethereum extracts the revert reason from the "data" field and includes it
	// in the error message. We include the selector 0x7e273289 in the message
	// to match what a real node returns after go-ethereum error parsing.
	srv := newTestRPCServer(t, func(method string, _ json.RawMessage) (interface{}, *jsonRPCError) {
		if method == "eth_call" {
			revertData := fmt.Sprintf("0x7e273289%064x", 42)
			return nil, &jsonRPCError{
				Code:    3,
				Message: "execution reverted: ERC721NonexistentToken(42) " + revertData,
			}
		}
		return "0x1", nil
	})
	defer srv.Close()

	client, err := ticketsbt.NewClient(context.Background(), srv.URL, testPrivateKey, testContractAddr)
	require.NoError(t, err)
	defer client.Close()

	minted, err := client.IsTokenMinted(context.Background(), 42)
	require.NoError(t, err)
	assert.False(t, minted)
}

func TestIsTokenMinted_AlreadyMinted(t *testing.T) {
	t.Parallel()

	ownerAddr := crypto.PubkeyToAddress(mustParseKey(t).PublicKey)

	srv := newTestRPCServer(t, func(method string, _ json.RawMessage) (interface{}, *jsonRPCError) {
		if method == "eth_call" {
			return abiEncodeAddress(ownerAddr), nil
		}
		return "0x1", nil
	})
	defer srv.Close()

	client, err := ticketsbt.NewClient(context.Background(), srv.URL, testPrivateKey, testContractAddr)
	require.NoError(t, err)
	defer client.Close()

	minted, err := client.IsTokenMinted(context.Background(), 42)
	require.NoError(t, err)
	assert.True(t, minted)
}

func TestIsTokenMinted_RPCError(t *testing.T) {
	t.Parallel()

	// Return a non-ERC721NonexistentToken error — should propagate as an error.
	srv := newTestRPCServer(t, func(method string, _ json.RawMessage) (interface{}, *jsonRPCError) {
		if method == "eth_call" {
			return nil, &jsonRPCError{
				Code:    -32000,
				Message: "node is not synced",
			}
		}
		return "0x1", nil
	})
	defer srv.Close()

	client, err := ticketsbt.NewClient(context.Background(), srv.URL, testPrivateKey, testContractAddr)
	require.NoError(t, err)
	defer client.Close()

	_, err = client.IsTokenMinted(context.Background(), 42)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to check token ownership")
}

func TestOwnerOf_Success(t *testing.T) {
	t.Parallel()

	ownerAddr := crypto.PubkeyToAddress(mustParseKey(t).PublicKey)

	srv := newTestRPCServer(t, func(method string, _ json.RawMessage) (interface{}, *jsonRPCError) {
		if method == "eth_call" {
			return abiEncodeAddress(ownerAddr), nil
		}
		return "0x1", nil
	})
	defer srv.Close()

	client, err := ticketsbt.NewClient(context.Background(), srv.URL, testPrivateKey, testContractAddr)
	require.NoError(t, err)
	defer client.Close()

	owner, err := client.OwnerOf(context.Background(), 42)
	require.NoError(t, err)
	assert.Equal(t, strings.ToLower(ownerAddr.Hex()), owner)
}

func TestOwnerOf_NonexistentToken(t *testing.T) {
	t.Parallel()

	srv := newTestRPCServer(t, func(method string, _ json.RawMessage) (interface{}, *jsonRPCError) {
		if method == "eth_call" {
			tokenID := new(big.Int).SetUint64(999)
			return nil, &jsonRPCError{
				Code:    3,
				Message: "execution reverted",
				Data:    json.RawMessage(fmt.Sprintf(`"0x7e273289%064s"`, tokenID.Text(16))),
			}
		}
		return "0x1", nil
	})
	defer srv.Close()

	client, err := ticketsbt.NewClient(context.Background(), srv.URL, testPrivateKey, testContractAddr)
	require.NoError(t, err)
	defer client.Close()

	_, err = client.OwnerOf(context.Background(), 999)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ownerOf failed")
}

func TestMint_ContextCancelled(t *testing.T) {
	t.Parallel()

	srv := newTestRPCServer(t, func(method string, _ json.RawMessage) (interface{}, *jsonRPCError) {
		return nil, &jsonRPCError{Code: -32000, Message: "timeout"}
	})
	defer srv.Close()

	client, err := ticketsbt.NewClient(context.Background(), srv.URL, testPrivateKey, testContractAddr)
	require.NoError(t, err)
	defer client.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately.

	_, err = client.Mint(ctx, "0xaAbBcCdDeEfF0011223344556677889900aAbBcC", 42)
	require.Error(t, err)
}

// mustParseKey parses the test private key.
func mustParseKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	key, err := crypto.HexToECDSA(testPrivateKey)
	require.NoError(t, err)
	return key
}
