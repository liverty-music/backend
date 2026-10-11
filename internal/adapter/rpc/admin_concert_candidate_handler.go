package rpc

import (
	"context"

	concertcandidatev1connect "buf.build/gen/go/liverty-music/schema/connectrpc/go/liverty_music/rpc/admin/concert_candidate/v1/concert_candidatev1connect"
	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	concertcandidatev1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/admin/concert_candidate/v1"
	"connectrpc.com/connect"
	"github.com/liverty-music/backend/internal/adapter/rpc/mapper"
	"github.com/liverty-music/backend/internal/infrastructure/auth"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-logging/logging"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Compile-time check that AdminConcertCandidateHandler implements the generated
// service interface.
var _ concertcandidatev1connect.ConcertCandidateServiceHandler = (*AdminConcertCandidateHandler)(nil)

// AdminConcertCandidateHandler implements the admin ConcertCandidateService Connect
// interface: listing, approving and rejecting the ConcertCandidates awaiting
// review. Admin authorization is enforced at the admin server boundary by its
// server-wide RequireRoleInterceptor (admin role); the handler is pure
// proto<->entity mapping.
type AdminConcertCandidateHandler struct {
	concertUseCase usecase.AdminConcertUseCase
	logger         *logging.Logger
}

// NewAdminConcertCandidateHandler creates a new admin concert candidate handler.
func NewAdminConcertCandidateHandler(
	concertUseCase usecase.AdminConcertUseCase,
	logger *logging.Logger,
) *AdminConcertCandidateHandler {
	return &AdminConcertCandidateHandler{
		concertUseCase: concertUseCase,
		logger:         logger,
	}
}

// List returns every concert candidate currently awaiting review.
// Performer resolution is delegated to the use case.
func (h *AdminConcertCandidateHandler) List(
	ctx context.Context,
	_ *connect.Request[concertcandidatev1.ListRequest],
) (*connect.Response[concertcandidatev1.ListResponse], error) {
	reviews, err := h.concertUseCase.ListPending(ctx)
	if err != nil {
		return nil, err
	}

	pending := make([]*concertcandidatev1.ConcertCandidate, 0, len(reviews))
	for _, r := range reviews {
		pending = append(pending, mapper.ConcertCandidateToProto(r.Staged, r.Performer))
	}

	return connect.NewResponse(&concertcandidatev1.ListResponse{
		ConcertCandidates: pending,
	}), nil
}

// Approve promotes a pending concert candidate to a published event. When the staged
// concert duplicates an existing event and no resolution was chosen, the response
// carries a DuplicateConflict and no state is mutated; the console then re-calls
// Approve with a resolution. The operation is idempotent on a missing staged row.
func (h *AdminConcertCandidateHandler) Approve(
	ctx context.Context,
	req *connect.Request[concertcandidatev1.ApproveRequest],
) (*connect.Response[concertcandidatev1.ApproveResponse], error) {
	// Reviewer identity is captured from the admin JWT (as in Reject) so a
	// keep-existing reconciliation can attribute the rejection-log entry.
	claims, ok := auth.GetClaims(ctx)
	reviewerSub := ""
	if ok && claims != nil {
		reviewerSub = claims.Sub
	}

	stagedID := req.Msg.GetConcertCandidateId().GetValue()
	resolution := approveResolutionFromProto(req.Msg.GetResolution())

	result, err := h.concertUseCase.Approve(ctx, stagedID, resolution, reviewerSub)
	if err != nil {
		return nil, err
	}

	resp := &concertcandidatev1.ApproveResponse{}
	if result != nil && result.Conflict != nil {
		resp.Conflict = duplicateConflictToProto(result.Conflict)
	}
	return connect.NewResponse(resp), nil
}

// approveResolutionFromProto maps the generated Resolution enum to the usecase's
// resolution selector, defaulting to Unspecified for unknown values.
func approveResolutionFromProto(r concertcandidatev1.Resolution) usecase.ApproveResolution {
	switch r {
	case concertcandidatev1.Resolution_RESOLUTION_KEEP_EXISTING:
		return usecase.ApproveResolutionKeepExisting
	case concertcandidatev1.Resolution_RESOLUTION_ADOPT_STAGED:
		return usecase.ApproveResolutionAdoptStaged
	default:
		return usecase.ApproveResolutionUnspecified
	}
}

// duplicateConflictToProto maps a usecase DuplicateConflict to the wire message:
// the existing event's display fields plus the concert candidate preview.
func duplicateConflictToProto(dc *usecase.DuplicateConflict) *concertcandidatev1.DuplicateConflict {
	return &concertcandidatev1.DuplicateConflict{
		Existing:         existingEventToProto(dc.Existing),
		ConcertCandidate: mapper.ConcertCandidateToProto(dc.Staged, dc.StagedPerformer),
	}
}

// existingEventToProto maps the existing-event display DTO to its wire message.
// Optional start/open times are omitted when unset.
func existingEventToProto(e *usecase.ExistingEventDisplay) *concertcandidatev1.ExistingEvent {
	proto := &concertcandidatev1.ExistingEvent{
		EventId:         &entityv1.EventId{Value: e.EventID},
		Title:           &entityv1.Title{Value: e.Title},
		ListedVenueName: &entityv1.ListedVenueName{Value: e.ListedVenueName},
		LocalDate:       &entityv1.LocalDate{Value: mapper.TimeToDate(e.LocalDate)},
	}
	if e.StartTime != nil {
		proto.StartTime = &entityv1.StartTime{Value: timestamppb.New(*e.StartTime)}
	}
	if e.OpenTime != nil {
		proto.OpenTime = &entityv1.OpenTime{Value: timestamppb.New(*e.OpenTime)}
	}
	return proto
}

// Reject drops a pending concert candidate and records the rejection with the
// reviewer's identity and reason for search-quality analysis.
func (h *AdminConcertCandidateHandler) Reject(
	ctx context.Context,
	req *connect.Request[concertcandidatev1.RejectRequest],
) (*connect.Response[concertcandidatev1.RejectResponse], error) {
	claims, ok := auth.GetClaims(ctx)
	reviewerSub := ""
	if ok && claims != nil {
		reviewerSub = claims.Sub
	}

	stagedID := req.Msg.GetConcertCandidateId().GetValue()
	reason := req.Msg.GetReason()
	if err := h.concertUseCase.Reject(ctx, stagedID, reason, reviewerSub); err != nil {
		return nil, err
	}

	return connect.NewResponse(&concertcandidatev1.RejectResponse{}), nil
}
