package rpc

import (
	"context"

	adminv1connect "buf.build/gen/go/liverty-music/schema/connectrpc/go/liverty_music/rpc/admin/v1/adminv1connect"
	adminv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/admin/v1"
	"connectrpc.com/connect"
	"github.com/liverty-music/backend/internal/adapter/rpc/mapper"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/auth"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-logging/logging"
)

// Compile-time check that ConcertModerationHandler implements the generated service interface.
var _ adminv1connect.ConcertModerationServiceHandler = (*ConcertModerationHandler)(nil)

// ConcertModerationHandler implements the ConcertModerationService Connect interface.
// All methods require the caller to hold the "admin" Zitadel project role.
type ConcertModerationHandler struct {
	stagedConcertRepo      entity.StagedConcertRepository
	artistRepo             entity.ArtistRepository
	concertApprovalUseCase usecase.ConcertApprovalUseCase
	logger                 *logging.Logger
}

// NewConcertModerationHandler creates a new concert moderation handler.
func NewConcertModerationHandler(
	stagedConcertRepo entity.StagedConcertRepository,
	artistRepo entity.ArtistRepository,
	concertApprovalUseCase usecase.ConcertApprovalUseCase,
	logger *logging.Logger,
) *ConcertModerationHandler {
	return &ConcertModerationHandler{
		stagedConcertRepo:      stagedConcertRepo,
		artistRepo:             artistRepo,
		concertApprovalUseCase: concertApprovalUseCase,
		logger:                 logger,
	}
}

// ListPendingConcerts returns every staged concert currently awaiting review.
// The performer artist is resolved per row so reviewers see full artist detail.
func (h *ConcertModerationHandler) ListPendingConcerts(
	ctx context.Context,
	_ *connect.Request[adminv1.ListPendingConcertsRequest],
) (*connect.Response[adminv1.ListPendingConcertsResponse], error) {
	if err := auth.RequireRole(ctx, "admin"); err != nil {
		return nil, err
	}

	staged, err := h.stagedConcertRepo.ListPending(ctx)
	if err != nil {
		return nil, err
	}

	pending := make([]*adminv1.PendingConcert, 0, len(staged))
	for _, sc := range staged {
		artist, err := h.artistRepo.Get(ctx, sc.ArtistID)
		if err != nil {
			return nil, err
		}
		pending = append(pending, mapper.PendingConcertToProto(sc, artist))
	}

	return connect.NewResponse(&adminv1.ListPendingConcertsResponse{
		PendingConcerts: pending,
	}), nil
}

// ApproveConcert promotes a pending staged concert to a published event.
// The operation is idempotent: if the staged row is already gone the method
// returns success without creating a duplicate.
func (h *ConcertModerationHandler) ApproveConcert(
	ctx context.Context,
	req *connect.Request[adminv1.ApproveConcertRequest],
) (*connect.Response[adminv1.ApproveConcertResponse], error) {
	if err := auth.RequireRole(ctx, "admin"); err != nil {
		return nil, err
	}

	stagedID := req.Msg.GetStagedId().GetValue()
	if err := h.concertApprovalUseCase.Approve(ctx, stagedID); err != nil {
		return nil, err
	}

	return connect.NewResponse(&adminv1.ApproveConcertResponse{}), nil
}

// RejectConcert drops a pending staged concert and records the rejection with
// the reviewer's identity and reason for search-quality analysis.
func (h *ConcertModerationHandler) RejectConcert(
	ctx context.Context,
	req *connect.Request[adminv1.RejectConcertRequest],
) (*connect.Response[adminv1.RejectConcertResponse], error) {
	if err := auth.RequireRole(ctx, "admin"); err != nil {
		return nil, err
	}

	claims, ok := auth.GetClaims(ctx)
	reviewerSub := ""
	if ok && claims != nil {
		reviewerSub = claims.Sub
	}

	stagedID := req.Msg.GetStagedId().GetValue()
	reason := req.Msg.GetReason()
	if err := h.concertApprovalUseCase.Reject(ctx, stagedID, reason, reviewerSub); err != nil {
		return nil, err
	}

	return connect.NewResponse(&adminv1.RejectConcertResponse{}), nil
}
