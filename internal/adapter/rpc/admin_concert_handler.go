package rpc

import (
	"context"

	adminv1connect "buf.build/gen/go/liverty-music/schema/connectrpc/go/liverty_music/rpc/admin/v1/adminv1connect"
	adminv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/admin/v1"
	"connectrpc.com/connect"
	"github.com/liverty-music/backend/internal/adapter/rpc/mapper"
	"github.com/liverty-music/backend/internal/infrastructure/auth"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-logging/logging"
)

// Compile-time check that AdminConcertHandler implements the generated service interface.
var _ adminv1connect.ConcertServiceHandler = (*AdminConcertHandler)(nil)

// AdminConcertHandler implements the admin ConcertService Connect interface.
//
// Admin authorization is enforced at the admin server boundary by its server-wide
// RequireRoleInterceptor (admin role), not by per-method checks here; the handler
// is pure proto<->entity mapping. See internal/infrastructure/auth/authz.go.
type AdminConcertHandler struct {
	concertUseCase usecase.AdminConcertUseCase
	logger         *logging.Logger
}

// NewAdminConcertHandler creates a new admin concert handler.
func NewAdminConcertHandler(
	concertUseCase usecase.AdminConcertUseCase,
	logger *logging.Logger,
) *AdminConcertHandler {
	return &AdminConcertHandler{
		concertUseCase: concertUseCase,
		logger:         logger,
	}
}

// List returns every published concert for admin catalog management. The admin
// console groups the flat result by performing artist client-side.
func (h *AdminConcertHandler) List(
	ctx context.Context,
	_ *connect.Request[adminv1.ListRequest],
) (*connect.Response[adminv1.ListResponse], error) {
	concerts, err := h.concertUseCase.List(ctx)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&adminv1.ListResponse{
		Concerts: mapper.ConcertsToProto(concerts),
	}), nil
}

// ListPending returns every staged concert currently awaiting review.
// Performer resolution is delegated to the use case.
func (h *AdminConcertHandler) ListPending(
	ctx context.Context,
	_ *connect.Request[adminv1.ListPendingRequest],
) (*connect.Response[adminv1.ListPendingResponse], error) {
	reviews, err := h.concertUseCase.ListPending(ctx)
	if err != nil {
		return nil, err
	}

	pending := make([]*adminv1.PendingConcert, 0, len(reviews))
	for _, r := range reviews {
		pending = append(pending, mapper.PendingConcertToProto(r.Staged, r.Performer))
	}

	return connect.NewResponse(&adminv1.ListPendingResponse{
		PendingConcerts: pending,
	}), nil
}

// Approve promotes a pending staged concert to a published event.
// The operation is idempotent: if the staged row is already gone the method
// returns success without creating a duplicate.
func (h *AdminConcertHandler) Approve(
	ctx context.Context,
	req *connect.Request[adminv1.ApproveRequest],
) (*connect.Response[adminv1.ApproveResponse], error) {
	stagedID := req.Msg.GetStagedId().GetValue()
	if err := h.concertUseCase.Approve(ctx, stagedID); err != nil {
		return nil, err
	}

	return connect.NewResponse(&adminv1.ApproveResponse{}), nil
}

// Reject drops a pending staged concert and records the rejection with the
// reviewer's identity and reason for search-quality analysis.
func (h *AdminConcertHandler) Reject(
	ctx context.Context,
	req *connect.Request[adminv1.RejectRequest],
) (*connect.Response[adminv1.RejectResponse], error) {
	claims, ok := auth.GetClaims(ctx)
	reviewerSub := ""
	if ok && claims != nil {
		reviewerSub = claims.Sub
	}

	stagedID := req.Msg.GetStagedId().GetValue()
	reason := req.Msg.GetReason()
	if err := h.concertUseCase.Reject(ctx, stagedID, reason, reviewerSub); err != nil {
		return nil, err
	}

	return connect.NewResponse(&adminv1.RejectResponse{}), nil
}

// Delete permanently removes a published concert by its event id. The delete
// cascades to all referencing rows and is idempotent on a missing id.
func (h *AdminConcertHandler) Delete(
	ctx context.Context,
	req *connect.Request[adminv1.DeleteRequest],
) (*connect.Response[adminv1.DeleteResponse], error) {
	eventID := req.Msg.GetEventId().GetValue()
	if err := h.concertUseCase.Delete(ctx, eventID); err != nil {
		return nil, err
	}

	return connect.NewResponse(&adminv1.DeleteResponse{}), nil
}
