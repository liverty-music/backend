package rpc

import (
	"context"
	"errors"
	"time"

	receptionv1connect "buf.build/gen/go/liverty-music/schema/connectrpc/go/liverty_music/rpc/organizer/reception/v1/receptionv1connect"
	receptionv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/organizer/reception/v1"
	"connectrpc.com/connect"
	"github.com/liverty-music/backend/internal/adapter/rpc/mapper"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-logging/logging"
)

// Compile-time assertion that ReceptionHandler satisfies the generated
// interface.
var _ receptionv1connect.ReceptionServiceHandler = (*ReceptionHandler)(nil)

// ReceptionHandler implements the ReceptionService a reception device calls.
// It is served only by the reception server, without a sign-in: the caller is
// a reception link token plus the bound device's signature, checked by the
// use cases.
// The handler only checks that the call carries them and maps Proto↔Entity;
// clients that guess tokens are throttled by the unknown-token interceptor
// (ratelimit.NewUnknownTokenInterceptor) registered for these procedures.
type ReceptionHandler struct {
	receptionLinkUC usecase.ReceptionLinkUseCase
	ticketUC        usecase.TicketUseCase
	logger          *logging.Logger
}

// NewReceptionHandler creates a new ReceptionHandler.
func NewReceptionHandler(
	receptionLinkUC usecase.ReceptionLinkUseCase,
	ticketUC usecase.TicketUseCase,
	logger *logging.Logger,
) *ReceptionHandler {
	return &ReceptionHandler{
		receptionLinkUC: receptionLinkUC,
		ticketUC:        ticketUC,
		logger:          logger,
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

// Open binds the link to the calling device on first use and returns its
// reception window.
func (h *ReceptionHandler) Open(
	ctx context.Context,
	req *connect.Request[receptionv1.OpenRequest],
) (*connect.Response[receptionv1.OpenResponse], error) {
	msg := req.Msg
	if err := requireCallProof(msg.GetLinkToken().GetValue(), msg.GetSignTime() != nil, msg.GetSignature().GetValue()); err != nil {
		return nil, err
	}
	if len(msg.GetPublicKey().GetValue()) == 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("public_key is required"))
	}

	result, err := h.receptionLinkUC.Open(ctx, usecase.OpenReceptionLinkInput{
		Procedure: receptionv1connect.ReceptionServiceOpenProcedure,
		LinkToken: msg.GetLinkToken().GetValue(),
		PublicKey: entity.PublicKey(msg.GetPublicKey().GetValue()),
		SignTime:  msg.GetSignTime().AsTime(),
		Signature: entity.Signature(msg.GetSignature().GetValue()),
		Now:       time.Now(),
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&receptionv1.OpenResponse{
		ReceptionLink:   mapper.ReceptionLinkToProto(result.Link),
		ReceptionWindow: mapper.ReceptionWindowToProto(result.Window),
		InsideWindow:    result.InsideWindow,
	}), nil
}

// Admit decides one scan and returns the head count and reasons, never any
// personal data.
func (h *ReceptionHandler) Admit(
	ctx context.Context,
	req *connect.Request[receptionv1.AdmitRequest],
) (*connect.Response[receptionv1.AdmitResponse], error) {
	msg := req.Msg
	if err := requireCallProof(msg.GetLinkToken().GetValue(), msg.GetSignTime() != nil, msg.GetSignature().GetValue()); err != nil {
		return nil, err
	}
	if msg.GetScannedText().GetValue() == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("scanned_text is required"))
	}

	result, err := h.ticketUC.Admit(ctx, usecase.AdmitInput{
		Procedure:   receptionv1connect.ReceptionServiceAdmitProcedure,
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
