package rpc

import (
	"context"

	seattypev1connect "buf.build/gen/go/liverty-music/schema/connectrpc/go/v2/liverty_music/rpc/organizer/seat_type/v1/seat_typev1connect"
	seattypev1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/organizer/seat_type/v1"
	"github.com/pannpers/go-logging/logging"

	"github.com/liverty-music/backend/internal/adapter/rpc/mapper"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/usecase"
)

// Compile-time assertion that OrganizerSeatTypeHandler satisfies the
// generated interface.
var _ seattypev1connect.SeatTypeServiceHandler = (*OrganizerSeatTypeHandler)(nil)

// OrganizerSeatTypeHandler implements the organizer-facing SeatTypeService: a
// member defines the seat types of their own Organizer's Series. The request
// is validated by the protovalidate interceptor before this handler runs; the
// handler resolves the caller's own Organizer and delegates to the use case.
type OrganizerSeatTypeHandler struct {
	seatTypeUC  usecase.SeatTypeUseCase
	organizerUC usecase.OrganizerUseCase
	logger      *logging.Logger
}

// NewOrganizerSeatTypeHandler creates a new OrganizerSeatTypeHandler.
func NewOrganizerSeatTypeHandler(
	seatTypeUC usecase.SeatTypeUseCase,
	organizerUC usecase.OrganizerUseCase,
	logger *logging.Logger,
) *OrganizerSeatTypeHandler {
	return &OrganizerSeatTypeHandler{seatTypeUC: seatTypeUC, organizerUC: organizerUC, logger: logger}
}

// Create adds a seat type to one of the caller's Series.
func (h *OrganizerSeatTypeHandler) Create(
	ctx context.Context,
	req *seattypev1.CreateRequest,
) (*seattypev1.CreateResponse, error) {
	organizer, err := resolveCallerOrganizer(ctx, h.organizerUC)
	if err != nil {
		return nil, err
	}

	seatType, err := h.seatTypeUC.Create(ctx, organizer.ID, req.GetSeriesId().GetValue(),
		req.GetName().GetValue(), req.GetPrice().GetValue())
	if err != nil {
		return nil, err
	}
	return &seattypev1.CreateResponse{SeatType: mapper.SeatTypeToProto(seatType)}, nil
}

// Delete removes one of the caller's seat types.
func (h *OrganizerSeatTypeHandler) Delete(
	ctx context.Context,
	req *seattypev1.DeleteRequest,
) (*seattypev1.DeleteResponse, error) {
	organizer, err := resolveCallerOrganizer(ctx, h.organizerUC)
	if err != nil {
		return nil, err
	}

	if err := h.seatTypeUC.Delete(ctx, organizer.ID, entity.SeatTypeID(req.GetSeatTypeId().GetValue())); err != nil {
		return nil, err
	}
	return &seattypev1.DeleteResponse{}, nil
}

// ListBySeries returns the seat types of one of the caller's Series.
func (h *OrganizerSeatTypeHandler) ListBySeries(
	ctx context.Context,
	req *seattypev1.ListBySeriesRequest,
) (*seattypev1.ListBySeriesResponse, error) {
	organizer, err := resolveCallerOrganizer(ctx, h.organizerUC)
	if err != nil {
		return nil, err
	}

	types, err := h.seatTypeUC.ListBySeries(ctx, organizer.ID, req.GetSeriesId().GetValue())
	if err != nil {
		return nil, err
	}
	return &seattypev1.ListBySeriesResponse{SeatTypes: mapper.SeatTypesToProto(types)}, nil
}
