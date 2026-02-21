package entity

import (
	"context"
	"time"
)

// Ticket represents a soulbound ticket (ERC-5192) issued to a user for an event.
//
// Corresponds to liverty_music.entity.v1.Ticket.
type Ticket struct {
	// ID is the unique identifier for the ticket (UUID).
	ID string
	// EventID is the ID of the event for which this ticket grants admission.
	EventID string
	// UserID is the ID of the fan who holds this ticket.
	UserID string
	// TokenID is the on-chain ERC-721 token identifier assigned at mint time.
	TokenID uint64
	// TxHash is the blockchain transaction hash recorded when this ticket was minted.
	TxHash string
	// MintTime is the timestamp at which this ticket was minted on the blockchain.
	MintTime time.Time
}

// NewTicket represents data required to create a ticket record.
type NewTicket struct {
	// EventID is the ID of the event.
	EventID string
	// UserID is the ID of the fan.
	UserID string
	// TokenID is the on-chain token ID.
	TokenID uint64
	// TxHash is the mint transaction hash.
	TxHash string
}

// TicketMinter defines the interface for on-chain ticket minting operations.
// This abstraction allows the use case layer to depend on an interface rather
// than the concrete blockchain client, enabling unit testing with mocks.
type TicketMinter interface {
	// Mint submits a mint transaction for a soulbound token.
	Mint(ctx context.Context, recipient string, tokenID uint64) (txHash string, err error)
	// IsTokenMinted returns true if the given tokenID has already been minted on-chain.
	IsTokenMinted(ctx context.Context, tokenID uint64) (bool, error)
	// OwnerOf returns the owner address of the given tokenID as a lowercase hex string.
	// Returns an error if the token does not exist or the RPC call fails.
	OwnerOf(ctx context.Context, tokenID uint64) (address string, err error)
}

// TicketRepository defines the interface for ticket data access.
type TicketRepository interface {
	// Create persists a newly minted ticket record.
	//
	// # Possible errors
	//
	//  - AlreadyExists: If a ticket for the same event and user already exists.
	Create(ctx context.Context, params *NewTicket) (*Ticket, error)

	// Get retrieves a ticket by its ID.
	//
	// # Possible errors
	//
	//  - NotFound: If the ticket does not exist.
	Get(ctx context.Context, id string) (*Ticket, error)

	// GetByEventAndUser retrieves a ticket by event ID and user ID.
	// Used for idempotency check before minting.
	//
	// # Possible errors
	//
	//  - NotFound: If no ticket exists for the given event and user.
	GetByEventAndUser(ctx context.Context, eventID, userID string) (*Ticket, error)

	// ListByUser retrieves all tickets for a given user, ordered by mint time descending.
	ListByUser(ctx context.Context, userID string) ([]*Ticket, error)
}
