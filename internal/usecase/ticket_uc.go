package usecase

import (
	"context"
	"errors"
	"log/slog"

	"github.com/ethereum/go-ethereum/common"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/blockchain/ticketsbt"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/pannpers/go-logging/logging"
)

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
	// TokenID is the ERC-721 token ID to assign. Must be > 0 and unique on-chain.
	TokenID uint64
}

// ticketUseCase implements the TicketUseCase interface.
type ticketUseCase struct {
	ticketRepo entity.TicketRepository
	sbtClient  *ticketsbt.Client
	logger     *logging.Logger
}

// Compile-time interface compliance check.
var _ TicketUseCase = (*ticketUseCase)(nil)

// NewTicketUseCase creates a new ticket use case.
func NewTicketUseCase(
	ticketRepo entity.TicketRepository,
	sbtClient *ticketsbt.Client,
	logger *logging.Logger,
) TicketUseCase {
	return &ticketUseCase{
		ticketRepo: ticketRepo,
		sbtClient:  sbtClient,
		logger:     logger,
	}
}

// MintTicket mints a soulbound ticket, with idempotency via DB + on-chain checks.
// Task 7.2 + 7.3 + 7.4 (retry is in ticketsbt.Client.Mint) + 7.5 (store in DB).
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

	if params.TokenID == 0 {
		return nil, apperr.New(codes.InvalidArgument, "token_id must be greater than 0")
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

	// Idempotency check 2: check on-chain whether tokenID is already minted.
	alreadyMinted, err := uc.sbtClient.IsTokenMinted(ctx, params.TokenID)
	if err != nil {
		return nil, apperr.Wrap(err, codes.Internal, "failed to check on-chain token status",
			slog.Uint64("token_id", params.TokenID),
		)
	}

	var mintResult *ticketsbt.MintResult

	if alreadyMinted {
		// Token exists on-chain but no DB record — create the record to reconcile state.
		uc.logger.Warn(ctx, "token already minted on-chain but missing DB record, reconciling",
			slog.Uint64("token_id", params.TokenID),
			slog.String("event_id", params.EventID),
			slog.String("user_id", params.UserID),
		)
		// Use a zero-value tx hash as placeholder for reconciled records.
		// The token exists on-chain but we have no tx hash — use the null hash.
		mintResult = &ticketsbt.MintResult{
			TxHash:  "0x0000000000000000000000000000000000000000000000000000000000000000",
			TokenID: params.TokenID,
		}
	} else {
		// Submit the mint transaction. Retry logic is inside Client.Mint.
		recipient := common.HexToAddress(params.RecipientAddress)
		mintResult, err = uc.sbtClient.Mint(ctx, recipient, params.TokenID)
		if err != nil {
			return nil, apperr.Wrap(err, codes.Internal, "failed to mint ticket on-chain",
				slog.String("event_id", params.EventID),
				slog.String("user_id", params.UserID),
				slog.Uint64("token_id", params.TokenID),
			)
		}

		uc.logger.Info(ctx, "ticket minted on-chain",
			slog.String("tx_hash", mintResult.TxHash),
			slog.Uint64("token_id", mintResult.TokenID),
		)
	}

	// Task 7.5: store the minted ticket in the database.
	ticket, err := uc.ticketRepo.Create(ctx, &entity.NewTicket{
		EventID: params.EventID,
		UserID:  params.UserID,
		TokenID: mintResult.TokenID,
		TxHash:  mintResult.TxHash,
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
