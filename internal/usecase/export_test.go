package usecase

import (
	"context"
	"encoding/json"

	"github.com/liverty-music/backend/internal/entity"
)

// ExportedBuildNewTicketEmail exposes buildNewTicketEmail for black-box tests.
var ExportedBuildNewTicketEmail = func(uc TicketEmailUseCase, userID string, emailType entity.TicketEmailType, rawBody string, parsedJSON json.RawMessage, parsed *entity.ParsedEmailData) *entity.NewTicketEmail {
	return uc.(*ticketEmailUseCase).buildNewTicketEmail(userID, emailType, rawBody, parsedJSON, parsed)
}

// ExportedDetermineJourneyStatus exposes determineJourneyStatus for black-box tests.
var ExportedDetermineJourneyStatus = func(uc TicketEmailUseCase, te *entity.TicketEmail) entity.TicketJourneyStatus {
	return uc.(*ticketEmailUseCase).determineJourneyStatus(te)
}

// ExportedProfileLogoColor exposes profileLogoColor for black-box tests.
var ExportedProfileLogoColor = func(uc ArtistImageSyncUseCase, ctx context.Context, fanart *entity.Fanart, artistID string) {
	uc.(*artistImageSyncUseCase).profileLogoColor(ctx, fanart, artistID)
}

// ExportedValidateMintParams exposes validateMintParams for black-box tests.
var ExportedValidateMintParams = func(uc TicketUseCase, params *MintTicketParams) error {
	return uc.(*ticketUseCase).validateMintParams(params)
}

// ExportedCheckExistingTicket exposes checkExistingTicket for black-box tests.
var ExportedCheckExistingTicket = func(uc TicketUseCase, ctx context.Context, eventID, userID string) (*entity.Ticket, bool, error) {
	return uc.(*ticketUseCase).checkExistingTicket(ctx, eventID, userID)
}

// ExportedMintOrReconcile exposes mintOrReconcile for black-box tests.
// Returns only txHash and error; tokenID is an internal implementation detail.
var ExportedMintOrReconcile = func(uc TicketUseCase, ctx context.Context, params *MintTicketParams) (string, error) {
	txHash, _, err := uc.(*ticketUseCase).mintOrReconcile(ctx, params)
	return txHash, err
}

// ExportedPersistTicket exposes persistTicket for black-box tests.
var ExportedPersistTicket = func(uc TicketUseCase, ctx context.Context, params *MintTicketParams, tokenID uint64, txHash string) (*entity.Ticket, error) {
	return uc.(*ticketUseCase).persistTicket(ctx, params, tokenID, txHash)
}
