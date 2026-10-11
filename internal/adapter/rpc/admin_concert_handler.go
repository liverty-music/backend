package rpc

import (
	"context"

	adminconcertv1connect "buf.build/gen/go/liverty-music/schema/connectrpc/go/liverty_music/rpc/admin/concert/v1/concertv1connect"
	adminconcertv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/admin/concert/v1"
	"connectrpc.com/connect"
	"github.com/liverty-music/backend/internal/adapter/rpc/mapper"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-logging/logging"
)

// Compile-time check that AdminConcertHandler implements the generated service interface.
var _ adminconcertv1connect.ConcertServiceHandler = (*AdminConcertHandler)(nil)

// AdminConcertHandler implements the admin ConcertService Connect interface:
// List and Delete of published Concerts. The review of ConcertCandidates is the
// admin ConcertCandidateService ([AdminConcertCandidateHandler]).
//
// Admin authorization is enforced at the admin server boundary by its server-wide
// RequireRoleInterceptor (admin role), not by per-method checks here; the handler
// is pure proto<->entity mapping. See internal/infrastructure/auth/authz.go.
type AdminConcertHandler struct {
	concertUseCase        usecase.AdminConcertUseCase
	seriesImageURLBuilder *mapper.SeriesImageURLBuilder
	logger                *logging.Logger
}

// NewAdminConcertHandler creates a new admin concert handler. mediaURLBuilder
// composes the cover image URLs of the Series List returns, so they carry the
// same fields as on the fan concert service.
func NewAdminConcertHandler(
	concertUseCase usecase.AdminConcertUseCase,
	seriesImageURLBuilder *mapper.SeriesImageURLBuilder,
	logger *logging.Logger,
) *AdminConcertHandler {
	return &AdminConcertHandler{
		concertUseCase:        concertUseCase,
		seriesImageURLBuilder: seriesImageURLBuilder,
		logger:                logger,
	}
}

// List returns every published concert for admin catalog management, with
// each Series and Artist they refer to once. The admin console groups the
// flat result by performing artist client-side.
func (h *AdminConcertHandler) List(
	ctx context.Context,
	_ *connect.Request[adminconcertv1.ListRequest],
) (*connect.Response[adminconcertv1.ListResponse], error) {
	concerts, err := h.concertUseCase.List(ctx)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&adminconcertv1.ListResponse{
		Concerts: mapper.ConcertsToProto(concerts),
		Series:   h.seriesImageURLBuilder.ReferencedSeries(concerts),
		Artists:  mapper.ReferencedArtists(concerts),
	}), nil
}

// Delete permanently removes a published concert by its event id. The delete
// cascades to all referencing rows and is idempotent on a missing id.
func (h *AdminConcertHandler) Delete(
	ctx context.Context,
	req *connect.Request[adminconcertv1.DeleteRequest],
) (*connect.Response[adminconcertv1.DeleteResponse], error) {
	eventID := req.Msg.GetEventId().GetValue()
	if err := h.concertUseCase.Delete(ctx, eventID); err != nil {
		return nil, err
	}

	return connect.NewResponse(&adminconcertv1.DeleteResponse{}), nil
}
