package usecase

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"

	"github.com/google/uuid"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/pannpers/go-logging/logging"
)

// ethAddressRe matches a valid Ethereum address (0x-prefixed, 40 hex chars).
var ethAddressRe = regexp.MustCompile(`^0x[0-9a-fA-F]{40}$`)

// TicketUseCase defines the interface for ticket-related business logic.
type TicketUseCase interface {
	// MintTicket mints a soulbound ticket for a user at an event.
	// The operation is idempotent: if a ticket already exists for the given
	// event and user (in the database), it is returned without re-minting.
	//
	// # Possible errors
	//
	//  - InvalidArgument: If eventID, userID, recipientAddress, or tokenID are invalid.
	//  - Internal: If the on-chain mint transaction fails after retries.
	MintTicket(ctx context.Context, params *MintTicketParams) (*entity.Ticket, error)

	// GetTicket retrieves a ticket by its ID.
	//
	// # Possible errors
	//
	//  - NotFound: If the ticket does not exist.
	GetTicket(ctx context.Context, id string) (*entity.Ticket, error)

	// ListTicketsForUser retrieves all tickets for a given user.
	ListTicketsForUser(ctx context.Context, userID string) ([]*entity.Ticket, error)
}

// MintTicketParams holds the inputs required to mint a ticket.
type MintTicketParams struct {
	// EventID is the ID of the event for which the ticket is being minted.
	EventID string
	// UserID is the internal users.id of the ticket recipient.
	UserID string
	// RecipientAddress is the EVM address (Safe or EOA) that receives the SBT.
	RecipientAddress string
	// TokenID is no longer accepted from callers. It is generated internally
	// by generateTokenID to prevent client-controlled token ID assignment.
}

// ticketUseCase implements the TicketUseCase interface.
type ticketUseCase struct {
	ticketRepo entity.TicketRepository
	minter     entity.TicketMinter
	logger     *logging.Logger
}

// Compile-time interface compliance check.
var _ TicketUseCase = (*ticketUseCase)(nil)

// NewTicketUseCase creates a new ticket use case.
func NewTicketUseCase(
	ticketRepo entity.TicketRepository,
	minter entity.TicketMinter,
	logger *logging.Logger,
) TicketUseCase {
	return &ticketUseCase{
		ticketRepo: ticketRepo,
		minter:     minter,
		logger:     logger,
	}
}

// MintTicket mints a soulbound ticket, with idempotency via DB + on-chain checks.
func (uc *ticketUseCase) MintTicket(ctx context.Context, params *MintTicketParams) (*entity.Ticket, error) {
	if params == nil {
		return nil, apperr.New(codes.InvalidArgument, "params cannot be nil")
	}

	if params.EventID == "" {
		return nil, apperr.New(codes.InvalidArgument, "event_id is required")
	}

	if params.UserID == "" {
		return nil, apperr.New(codes.InvalidArgument, "user_id is required")
	}

	if params.RecipientAddress == "" {
		return nil, apperr.New(codes.InvalidArgument, "recipient_address is required")
	}

	if !ethAddressRe.MatchString(params.RecipientAddress) {
		return nil, apperr.New(codes.InvalidArgument, "recipient_address must be a valid Ethereum address (0x followed by 40 hex characters)")
	}

	// Idempotency check 1: check the database for an existing ticket record.
	existing, err := uc.ticketRepo.GetByEventAndUser(ctx, params.EventID, params.UserID)
	if err == nil {
		uc.logger.Info(ctx, "ticket already exists in database, returning existing record",
			slog.String("ticket_id", existing.ID),
			slog.String("event_id", params.EventID),
			slog.String("user_id", params.UserID),
		)
		return existing, nil
	}

	if !errors.Is(err, apperr.ErrNotFound) {
		return nil, err
	}

	// Generate a backend-controlled token ID from a UUIDv7.
	// Using the high 64 bits (timestamp + random) gives a monotonically increasing,
	// collision-resistant value without accepting client input.
	tokenID, err := generateTokenID()
	if err != nil {
		return nil, apperr.Wrap(err, codes.Internal, "failed to generate token ID")
	}

	// Idempotency check 2: check on-chain whether tokenID is already minted.
	alreadyMinted, err := uc.minter.IsTokenMinted(ctx, tokenID)
	if err != nil {
		return nil, apperr.Wrap(err, codes.Internal, "failed to check on-chain token status",
			slog.Uint64("token_id", tokenID),
		)
	}

	var txHash string

	if alreadyMinted {
		// Token exists on-chain but no DB record — verify ownership before reconciling.
		owner, err := uc.minter.OwnerOf(ctx, tokenID)
		if err != nil {
			return nil, apperr.Wrap(err, codes.Internal, "failed to fetch on-chain owner for reconciliation",
				slog.Uint64("token_id", tokenID),
			)
		}

		if !strings.EqualFold(owner, params.RecipientAddress) {
			return nil, apperr.New(codes.PermissionDenied,
				fmt.Sprintf("token %d is already owned by %s, not %s", tokenID, owner, params.RecipientAddress),
			)
		}

		uc.logger.Warn(ctx, "token already minted on-chain but missing DB record, reconciling",
			slog.Uint64("token_id", tokenID),
			slog.String("event_id", params.EventID),
			slog.String("user_id", params.UserID),
		)
		txHash = "0x0000000000000000000000000000000000000000000000000000000000000000"
	} else {
		// Submit the mint transaction. Retry logic is inside the minter implementation.
		txHash, err = uc.minter.Mint(ctx, params.RecipientAddress, tokenID)
		if err != nil {
			return nil, apperr.Wrap(err, codes.Internal, "failed to mint ticket on-chain",
				slog.String("event_id", params.EventID),
				slog.String("user_id", params.UserID),
				slog.Uint64("token_id", tokenID),
			)
		}

		uc.logger.Info(ctx, "ticket minted on-chain",
			slog.String("tx_hash", txHash),
			slog.Uint64("token_id", tokenID),
		)
	}

	// Store the minted ticket in the database.
	ticket, err := uc.ticketRepo.Create(ctx, &entity.NewTicket{
		EventID: params.EventID,
		UserID:  params.UserID,
		TokenID: tokenID,
		TxHash:  txHash,
	})
	if err != nil {
		// On unique constraint violation another concurrent mint succeeded — fetch and return it.
		if errors.Is(err, apperr.ErrAlreadyExists) {
			return uc.ticketRepo.GetByEventAndUser(ctx, params.EventID, params.UserID)
		}

		return nil, err
	}

	uc.logger.Info(ctx, "ticket record saved",
		slog.String("ticket_id", ticket.ID),
		slog.String("event_id", params.EventID),
		slog.String("user_id", params.UserID),
	)

	return ticket, nil
}

// generateTokenID produces a backend-controlled ERC-721 token ID from a UUIDv7.
// The high 64 bits of the UUID (48-bit ms timestamp + 12-bit sequence + 4 version bits)
// form a monotonically increasing, collision-resistant uint64 that is safe to use as a
// token ID without any client input.
func generateTokenID() (uint64, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return 0, fmt.Errorf("failed to generate UUIDv7: %w", err)
	}
	b := id[:]
	// Use the high 8 bytes of the UUID for the token ID.
	return binary.BigEndian.Uint64(b[:8]), nil
}

// GetTicket retrieves a ticket by ID.
func (uc *ticketUseCase) GetTicket(ctx context.Context, id string) (*entity.Ticket, error) {
	if id == "" {
		return nil, apperr.New(codes.InvalidArgument, "ticket ID cannot be empty")
	}

	return uc.ticketRepo.Get(ctx, id)
}

// ListTicketsForUser retrieves all tickets for a given user.
func (uc *ticketUseCase) ListTicketsForUser(ctx context.Context, userID string) ([]*entity.Ticket, error) {
	if userID == "" {
		return nil, apperr.New(codes.InvalidArgument, "user ID cannot be empty")
	}

	return uc.ticketRepo.ListByUser(ctx, userID)
}
