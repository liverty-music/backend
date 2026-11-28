package rpc

import (
	"context"

	admincatalogartistv1connect "buf.build/gen/go/liverty-music/schema/connectrpc/go/v2/liverty_music/rpc/admin/catalog_artist/v1/catalog_artistv1connect"
	admincatalogartistv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/admin/catalog_artist/v1"
	"github.com/liverty-music/backend/internal/adapter/pb"
	"github.com/liverty-music/backend/internal/usecase"
)

// Compile-time assertion that AdminCatalogArtistHandler satisfies the
// generated interface.
var _ admincatalogartistv1connect.CatalogArtistServiceHandler = (*AdminCatalogArtistHandler)(nil)

// AdminCatalogArtistHandler implements the admin CatalogArtistService Connect
// interface: the music catalog's namesakes of a registered Artist, for the
// admin console's Artist MBIDs screen. Admin authorization is enforced at the
// admin server boundary by its server-wide RequireRoleInterceptor (admin
// role), and requests are validated by the server's protovalidate interceptor
// before this runs.
type AdminCatalogArtistHandler struct {
	catalogArtistUC usecase.CatalogArtistUseCase
}

// NewAdminCatalogArtistHandler creates a new AdminCatalogArtistHandler.
func NewAdminCatalogArtistHandler(catalogArtistUC usecase.CatalogArtistUseCase) *AdminCatalogArtistHandler {
	return &AdminCatalogArtistHandler{catalogArtistUC: catalogArtistUC}
}

// ListByArtist returns the Artist's namesake candidates in the usecase's
// order. Usecase errors (NotFound, Unavailable) are returned unchanged.
func (h *AdminCatalogArtistHandler) ListByArtist(
	ctx context.Context,
	req *admincatalogartistv1.ListByArtistRequest,
) (*admincatalogartistv1.ListByArtistResponse, error) {
	candidates, err := h.catalogArtistUC.ListByArtist(ctx, req.GetArtistId().GetValue())
	if err != nil {
		return nil, err
	}
	return &admincatalogartistv1.ListByArtistResponse{CatalogArtists: pb.FromCatalogArtists(candidates)}, nil
}
