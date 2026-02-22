package rpc

import (
	"context"
	"errors"

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
func (h *EntryHandler) VerifyEntry(
	ctx context.Context,
	req *connect.Request[entryv1.VerifyEntryRequest],
) (*connect.Response[entryv1.VerifyEntryResponse], error) {
	if req == nil || req.Msg == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("request cannot be nil"))
	}

	msg := req.Msg
	if msg.GetEventId() == nil || msg.GetEventId().GetValue() == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("event_id is required"))
	}

	if msg.GetProofJson() == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("proof_json is required"))
	}

	if msg.GetPublicSignalsJson() == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("public_signals_json is required"))
	}

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
func (h *EntryHandler) GetMerklePath(
	ctx context.Context,
	req *connect.Request[entryv1.GetMerklePathRequest],
) (*connect.Response[entryv1.GetMerklePathResponse], error) {
	if req == nil || req.Msg == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("request cannot be nil"))
	}

	msg := req.Msg
	if msg.GetEventId() == nil || msg.GetEventId().GetValue() == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("event_id is required"))
	}

	// Resolve user ID from JWT claims for authorization safety.
	// The request body user_id is intentionally ignored to prevent users from
	// querying other users' Merkle paths.
	claims, err := mapper.GetClaimsFromContext(ctx)
	if err != nil {
		return nil, err
	}

	user, err := h.userRepo.GetByExternalID(ctx, claims.Sub)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("failed to resolve user"))
	}
	if user == nil {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("user not found"))
	}

	result, err := h.entryUseCase.GetMerklePath(ctx, msg.GetEventId().GetValue(), user.ID)
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
