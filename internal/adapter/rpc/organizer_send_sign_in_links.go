package rpc

import (
	"context"

	organizerv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/organizer/v1"
	"connectrpc.com/connect"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
)

// SendSignInLinks is not served yet: the change
// reuse-fan-email-for-organizer-sign-in (tasks 4.1-4.4) implements it. The
// generated OrganizerServiceHandler requires the method since schema v0.86.0.
func (h *OrganizerHandler) SendSignInLinks(
	_ context.Context,
	_ *connect.Request[organizerv1.SendSignInLinksRequest],
) (*connect.Response[organizerv1.SendSignInLinksResponse], error) {
	return nil, apperr.New(codes.Unimplemented, "sending sign-in links is not available yet")
}
