package rpc

import (
	"context"

	entryconnect "buf.build/gen/go/liverty-music/schema/connectrpc/go/liverty_music/rpc/entry/v1/entryv1connect"
	entryv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/entry/v1"
	"connectrpc.com/connect"
	"github.com/liverty-music/backend/internal/adapter/rpc/mapper"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-logging/logging"
)

// Compile-time check that EntryHandler implements the generated service interface.
var _ entryconnect.EntryServiceHandler = (*EntryHandler)(nil)

// EntryHandler implements the EntryService Connect interface.
type EntryHandler struct {
	entryUseCase usecase.EntryUseCase
	userRepo     entity.UserRepository
	logger       *logging.Logger
}

// NewEntryHandler creates a new entry handler.
func NewEntryHandler(entryUseCase usecase.EntryUseCase, userRepo entity.UserRepository, logger *logging.Logger) *EntryHandler {
	return &EntryHandler{
		entryUseCase: entryUseCase,
		userRepo:     userRepo,
		logger:       logger,
	}
}

// VerifyEntry verifies a ZKP for event entry.
//
// The QR code payload includes an `exp` (expiration) timestamp added by the frontend
// to limit the replay window for photographed QR codes. Expiry validation is enforced
// by the scanning client before calling this RPC, not server-side, because the `exp`
// field is part of the outer QR wrapper and not included in the proof or public signals.
// The server-side guard against replay is the nullifier uniqueness constraint: each
// nullifier can only be used once per event, so even an unexpired replayed QR will fail
// if the original has already been verified.
func (h *EntryHandler) VerifyEntry(
	ctx context.Context,
	req *connect.Request[entryv1.VerifyEntryRequest],
) (*connect.Response[entryv1.VerifyEntryResponse], error) {
	msg := req.Msg

	result, err := h.entryUseCase.VerifyEntry(ctx, &usecase.VerifyEntryParams{
		EventID:           msg.GetEventId().GetValue(),
		ProofJSON:         msg.GetProofJson(),
		PublicSignalsJSON: msg.GetPublicSignalsJson(),
	})
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&entryv1.VerifyEntryResponse{
		Verified: result.Verified,
		Message:  result.Message,
	}), nil
}

// GetMerklePath retrieves the Merkle path for a user at an event.
//
// The request-supplied user_id is verified against the JWT-derived userID;
// mismatches are rejected with PERMISSION_DENIED per the rpc-auth-scoping
// convention.
func (h *EntryHandler) GetMerklePath(
	ctx context.Context,
	req *connect.Request[entryv1.GetMerklePathRequest],
) (*connect.Response[entryv1.GetMerklePathResponse], error) {
	externalID, err := mapper.GetExternalUserID(ctx)
	if err != nil {
		return nil, err
	}

	user, err := h.userRepo.GetByExternalID(ctx, externalID)
	if err != nil {
		return nil, err
	}

	if err := mapper.RequireUserIDMatch(user.ID, req.Msg.GetUserId().GetValue()); err != nil {
		return nil, err
	}

	result, err := h.entryUseCase.GetMerklePath(ctx, req.Msg.GetEventId().GetValue(), user.ID)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&entryv1.GetMerklePathResponse{
		MerkleRoot:   result.MerkleRoot,
		PathElements: result.PathElements,
		PathIndices:  result.PathIndices,
		Leaf:         result.Leaf,
	}), nil
}
