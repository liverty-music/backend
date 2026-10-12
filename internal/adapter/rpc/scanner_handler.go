package rpc

import (
	"context"
	"errors"
	"time"

	scannerv1connect "buf.build/gen/go/liverty-music/schema/connectrpc/go/liverty_music/rpc/scanner/v1/scannerv1connect"
	scannerv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/scanner/v1"
	"connectrpc.com/connect"
	"github.com/liverty-music/backend/internal/adapter/rpc/mapper"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-logging/logging"
)

// Compile-time assertion that ScannerHandler satisfies the generated
// interface.
var _ scannerv1connect.ScannerServiceHandler = (*ScannerHandler)(nil)

// ScannerHandler implements the device-facing ScannerService
// (rpc.scanner.v1) a scanner device calls. It is served only by the reception
// server, without a sign-in: the caller is a link token plus the bound
// device's signature, checked by the use cases.
// The handler only checks that the call carries them and maps Proto↔Entity;
// clients that guess link tokens are throttled by the unknown-token interceptor
// (ratelimit.NewUnknownTokenInterceptor) registered for these procedures.
type ScannerHandler struct {
	scannerUC usecase.ScannerUseCase
	ticketUC  usecase.TicketUseCase
	logger    *logging.Logger
}

// NewScannerHandler creates a new ScannerHandler.
func NewScannerHandler(
	scannerUC usecase.ScannerUseCase,
	ticketUC usecase.TicketUseCase,
	logger *logging.Logger,
) *ScannerHandler {
	return &ScannerHandler{
		scannerUC: scannerUC,
		ticketUC:  ticketUC,
		logger:    logger,
	}
}

// requireCallProof fails with InvalidArgument when the call lacks its link
// token, sign time or signature.
func requireCallProof(token string, hasSignTime bool, signature []byte) error {
	switch {
	case token == "":
		return connect.NewError(connect.CodeInvalidArgument, errors.New("link_token is required"))
	case !hasSignTime:
		return connect.NewError(connect.CodeInvalidArgument, errors.New("sign_time is required"))
	case len(signature) != entity.SignatureLen:
		return connect.NewError(connect.CodeInvalidArgument, errors.New("signature must be a 64-byte P1363 signature"))
	}
	return nil
}

// Open binds the Scanner to the calling device on first use and returns the
// admission window.
func (h *ScannerHandler) Open(
	ctx context.Context,
	req *connect.Request[scannerv1.OpenRequest],
) (*connect.Response[scannerv1.OpenResponse], error) {
	msg := req.Msg
	if err := requireCallProof(msg.GetLinkToken().GetValue(), msg.GetSignTime() != nil, msg.GetSignature().GetValue()); err != nil {
		return nil, err
	}
	if len(msg.GetPublicKey().GetValue()) == 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("public_key is required"))
	}

	result, err := h.scannerUC.Open(ctx, usecase.OpenScannerInput{
		Procedure: scannerv1connect.ScannerServiceOpenProcedure,
		LinkToken: msg.GetLinkToken().GetValue(),
		PublicKey: entity.PublicKey(msg.GetPublicKey().GetValue()),
		SignTime:  msg.GetSignTime().AsTime(),
		Signature: entity.Signature(msg.GetSignature().GetValue()),
		Now:       time.Now(),
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&scannerv1.OpenResponse{
		Scanner:         mapper.ScannerToProto(result.Scanner),
		AdmissionWindow: mapper.AdmissionWindowToProto(result.Window),
		InsideWindow:    result.InsideWindow,
	}), nil
}

// Admit decides one scan and returns the head count and reasons, never any
// personal data.
func (h *ScannerHandler) Admit(
	ctx context.Context,
	req *connect.Request[scannerv1.AdmitRequest],
) (*connect.Response[scannerv1.AdmitResponse], error) {
	msg := req.Msg
	if err := requireCallProof(msg.GetLinkToken().GetValue(), msg.GetSignTime() != nil, msg.GetSignature().GetValue()); err != nil {
		return nil, err
	}
	if msg.GetScannedText().GetValue() == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("scanned_text is required"))
	}

	result, err := h.ticketUC.Admit(ctx, usecase.AdmitInput{
		Procedure:   scannerv1connect.ScannerServiceAdmitProcedure,
		LinkToken:   msg.GetLinkToken().GetValue(),
		SignTime:    msg.GetSignTime().AsTime(),
		Signature:   entity.Signature(msg.GetSignature().GetValue()),
		ScannedText: msg.GetScannedText().GetValue(),
		Now:         time.Now(),
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(mapper.AdmitResultToProto(result)), nil
}
