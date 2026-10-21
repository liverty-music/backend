package rpc

import (
	"context"
	"time"

	"uuid"

	organizerscannerv1connect "buf.build/gen/go/liverty-music/schema/connectrpc/go/v2/liverty_music/rpc/organizer/scanner/v1/scannerv1connect"
	organizerscannerv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/organizer/scanner/v1"
	"connectrpc.com/connect/v2"
	"github.com/liverty-music/backend/internal/adapter/rpc/mapper"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/auth"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-logging/logging"
)

// Compile-time assertion that OrganizerScannerHandler satisfies the
// generated interface.
var _ organizerscannerv1connect.ScannerServiceHandler = (*OrganizerScannerHandler)(nil)

// OrganizerScannerHandler implements the organizer-facing ScannerService
// (rpc.organizer.scanner.v1). Org-scoped authorization is enforced by the
// OrgScopedInterceptor before this handler runs; the handler resolves the
// caller's own Organizer and delegates to the Scanner use case.
type OrganizerScannerHandler struct {
	scannerUC   usecase.ScannerUseCase
	organizerUC usecase.OrganizerUseCase
	logger      *logging.Logger
}

// NewOrganizerScannerHandler creates a new OrganizerScannerHandler.
func NewOrganizerScannerHandler(
	scannerUC usecase.ScannerUseCase,
	organizerUC usecase.OrganizerUseCase,
	logger *logging.Logger,
) *OrganizerScannerHandler {
	return &OrganizerScannerHandler{
		scannerUC:   scannerUC,
		organizerUC: organizerUC,
		logger:      logger,
	}
}

// resolveCallerOrganizer reads the Zitadel org id from context and delegates
// to OrganizerUseCase.ResolveCaller, whose failure is returned unchanged.
func (h *OrganizerScannerHandler) resolveCallerOrganizer(ctx context.Context) (*entity.Organizer, error) {
	callerOrgID, ok := auth.GetCallerOrgID(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodePermissionDenied, "permission denied")
	}
	return h.organizerUC.ResolveCaller(ctx, callerOrgID)
}

// requireUUID fails with InvalidArgument when value is missing or not a UUID.
func requireUUID(field, value string) error {
	if value == "" {
		return connect.NewError(connect.CodeInvalidArgument, field+" is required")
	}
	if _, err := uuid.Parse(value); err != nil {
		return connect.NewError(connect.CodeInvalidArgument, field+" must be a UUID")
	}
	return nil
}

// Create creates a Scanner for one of the caller's published events.
func (h *OrganizerScannerHandler) Create(
	ctx context.Context,
	req *organizerscannerv1.CreateRequest,
) (*organizerscannerv1.CreateResponse, error) {
	eventID := req.GetEventId().GetValue()
	if err := requireUUID("event_id", eventID); err != nil {
		return nil, err
	}
	organizer, err := h.resolveCallerOrganizer(ctx)
	if err != nil {
		return nil, err
	}

	scanner, err := h.scannerUC.Create(ctx, organizer.ID, eventID)
	if err != nil {
		return nil, err
	}
	return &organizerscannerv1.CreateResponse{
		Scanner: mapper.ScannerToProto(scanner),
	}, nil
}

// ListByEvent returns every Scanner of one of the caller's events.
func (h *OrganizerScannerHandler) ListByEvent(
	ctx context.Context,
	req *organizerscannerv1.ListByEventRequest,
) (*organizerscannerv1.ListByEventResponse, error) {
	eventID := req.GetEventId().GetValue()
	if err := requireUUID("event_id", eventID); err != nil {
		return nil, err
	}
	organizer, err := h.resolveCallerOrganizer(ctx)
	if err != nil {
		return nil, err
	}

	scanners, err := h.scannerUC.ListByEvent(ctx, organizer.ID, eventID)
	if err != nil {
		return nil, err
	}
	return &organizerscannerv1.ListByEventResponse{
		Scanners: mapper.ScannersToProto(scanners),
	}, nil
}

// Revoke stops one of the caller's Scanners at once.
func (h *OrganizerScannerHandler) Revoke(
	ctx context.Context,
	req *organizerscannerv1.RevokeRequest,
) (*organizerscannerv1.RevokeResponse, error) {
	scannerID := req.GetScannerId().GetValue()
	if err := requireUUID("scanner_id", scannerID); err != nil {
		return nil, err
	}
	organizer, err := h.resolveCallerOrganizer(ctx)
	if err != nil {
		return nil, err
	}

	scanner, err := h.scannerUC.Revoke(ctx, organizer.ID, entity.ScannerID(scannerID), time.Now())
	if err != nil {
		return nil, err
	}
	return &organizerscannerv1.RevokeResponse{
		Scanner: mapper.ScannerToProto(scanner),
	}, nil
}
