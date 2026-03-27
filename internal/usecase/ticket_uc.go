package usecase

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/liverty-music/backend/internal/entity"
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

// validateMintParams checks domain invariants for a mint request.
func (uc *ticketUseCase) validateMintParams(params *MintTicketParams) error {
	if err := entity.ValidateEthereumAddress(params.RecipientAddress); err != nil {
		return apperr.New(codes.InvalidArgument, err.Error())
	}

	return nil
}

// checkExistingTicket queries the database for an existing ticket by event and user,
// then verifies the event exists before allowing a mint.
// Returns (ticket, true, nil) when a ticket is already present,
// (nil, false, nil) when not found and the event exists,
// and (nil, false, err) on any error.
func (uc *ticketUseCase) checkExistingTicket(ctx context.Context, eventID, userID string) (*entity.Ticket, bool, error) {
	ticket, err := uc.ticketRepo.GetByEventAndUser(ctx, eventID, userID)
	if err == nil {
		return ticket, true, nil
	}
	if !errors.Is(err, apperr.ErrNotFound) {
		return nil, false, err
	}

	// Validate that the event exists before triggering an irreversible on-chain mint.
	eventExists, err := uc.ticketRepo.EventExists(ctx, eventID)
	if err != nil {
		return nil, false, apperr.Wrap(err, codes.Internal, "failed to check event existence")
	}
	if !eventExists {
		return nil, false, apperr.New(codes.NotFound, fmt.Sprintf("event %s does not exist", eventID))
	}

	return nil, false, nil
}

// mintOrReconcile generates a token ID, checks on-chain state, and either mints
// a new token or reconciles an existing one.
func (uc *ticketUseCase) mintOrReconcile(ctx context.Context, params *MintTicketParams) (string, uint64, error) {
	// Generate a backend-controlled token ID from a UUIDv7.
	// Using the high 64 bits (timestamp + random) gives a monotonically increasing,
	// collision-resistant value without accepting client input.
	tokenID, err := entity.GenerateTokenID()
	if err != nil {
		return "", 0, apperr.Wrap(err, codes.Internal, "failed to generate token ID")
	}

	// Idempotency check 2: check on-chain whether tokenID is already minted.
	alreadyMinted, err := uc.minter.IsTokenMinted(ctx, tokenID)
	if err != nil {
		return "", 0, apperr.Wrap(err, codes.Internal, "failed to check on-chain token status",
			slog.Uint64("token_id", tokenID),
		)
	}

	if alreadyMinted {
		// Token exists on-chain but no DB record — verify ownership before reconciling.
		owner, err := uc.minter.OwnerOf(ctx, tokenID)
		if err != nil {
			return "", 0, apperr.Wrap(err, codes.Internal, "failed to fetch on-chain owner for reconciliation",
				slog.Uint64("token_id", tokenID),
			)
		}

		if !strings.EqualFold(owner, params.RecipientAddress) {
			return "", 0, apperr.New(codes.PermissionDenied,
				fmt.Sprintf("token %d is already owned by %s, not %s", tokenID, owner, params.RecipientAddress),
			)
		}

		uc.logger.Warn(ctx, "token already minted on-chain but missing DB record, reconciling",
			slog.Uint64("token_id", tokenID),
			slog.String("event_id", params.EventID),
			slog.String("user_id", params.UserID),
		)
		return "0x0000000000000000000000000000000000000000000000000000000000000000", tokenID, nil
	}

	// Submit the mint transaction. Retry logic is inside the minter implementation.
	txHash, err := uc.minter.Mint(ctx, params.RecipientAddress, tokenID)
	if err != nil {
		return "", 0, apperr.Wrap(err, codes.Internal, "failed to mint ticket on-chain",
			slog.String("event_id", params.EventID),
			slog.String("user_id", params.UserID),
			slog.Uint64("token_id", tokenID),
		)
	}

	uc.logger.Info(ctx, "ticket minted on-chain",
		slog.String("tx_hash", txHash),
		slog.Uint64("token_id", tokenID),
	)
	return txHash, tokenID, nil
}

// persistTicket inserts a minted ticket into the database.
// On concurrent duplicate (AlreadyExists), fetches and returns the winning record.
func (uc *ticketUseCase) persistTicket(ctx context.Context, params *MintTicketParams, tokenID uint64, txHash string) (*entity.Ticket, error) {
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

// MintTicket mints a soulbound ticket, with idempotency via DB + on-chain checks.
func (uc *ticketUseCase) MintTicket(ctx context.Context, params *MintTicketParams) (*entity.Ticket, error) {
	if err := uc.validateMintParams(params); err != nil {
		return nil, err
	}

	// Idempotency check 1: check the database for an existing ticket record.
	existing, found, err := uc.checkExistingTicket(ctx, params.EventID, params.UserID)
	if err != nil {
		return nil, err
	}
	if found {
		uc.logger.Info(ctx, "ticket already exists in database, returning existing record",
			slog.String("ticket_id", existing.ID),
			slog.String("event_id", params.EventID),
			slog.String("user_id", params.UserID),
		)
		return existing, nil
	}

	txHash, tokenID, err := uc.mintOrReconcile(ctx, params)
	if err != nil {
		return nil, err
	}

	return uc.persistTicket(ctx, params, tokenID, txHash)
}

// GetTicket retrieves a ticket by ID.
func (uc *ticketUseCase) GetTicket(ctx context.Context, id string) (*entity.Ticket, error) {
	return uc.ticketRepo.Get(ctx, id)
}

// ListTicketsForUser retrieves all tickets for a given user.
func (uc *ticketUseCase) ListTicketsForUser(ctx context.Context, userID string) ([]*entity.Ticket, error) {
	return uc.ticketRepo.ListByUser(ctx, userID)
}
