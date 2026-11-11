package rpc_test

import (
	"context"
	"testing"

	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	adminorganizerv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/admin/organizer/v1"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	handler "github.com/liverty-music/backend/internal/adapter/rpc"
	"github.com/liverty-music/backend/internal/entity"
	ucmocks "github.com/liverty-music/backend/internal/usecase/mocks"
)

func newAdminOrganizerHandler(t *testing.T) (*handler.AdminOrganizerHandler, *ucmocks.MockOrganizerUseCase) {
	t.Helper()
	logger, err := logging.New()
	require.NoError(t, err)
	uc := ucmocks.NewMockOrganizerUseCase(t)
	return handler.NewAdminOrganizerHandler(uc, logger), uc
}

// adminOrganizerDetails are the business details of the Organizers in these
// tests.
var adminOrganizerDetails = entity.BusinessDetails{
	LegalName:           "株式会社ライブ",
	RepresentativeName:  "山田 太郎",
	BusinessAddress:     "東京都渋谷区1-2-3",
	BusinessPhoneNumber: "+81312345678",
	ContactEmail:        "contact@example.com",
}

// wantAdminOrganizer is the message the admin service returns for an
// Organizer org-1 "Live Inc." (live-inc) in the status, with
// adminOrganizerDetails and a platform fee rate of 800 bps.
func wantAdminOrganizer(status entityv1.OrganizerStatus) *entityv1.Organizer {
	return &entityv1.Organizer{
		Id:                  &entityv1.OrganizerId{Value: "org-1"},
		Name:                &entityv1.OrganizerName{Value: "Live Inc."},
		ShortName:           &entityv1.OrganizerShortName{Value: "live-inc"},
		Status:              status,
		LegalName:           &entityv1.LegalName{Value: adminOrganizerDetails.LegalName},
		RepresentativeName:  &entityv1.RepresentativeName{Value: adminOrganizerDetails.RepresentativeName},
		BusinessAddress:     &entityv1.BusinessAddress{Value: adminOrganizerDetails.BusinessAddress},
		BusinessPhoneNumber: &entityv1.PhoneNumber{Value: adminOrganizerDetails.BusinessPhoneNumber},
		ContactEmail:        &entityv1.Email{Value: adminOrganizerDetails.ContactEmail},
		PlatformFeeRateBps:  800,
	}
}

func adminOrganizer(status entity.OrganizerStatus) *entity.Organizer {
	details := adminOrganizerDetails
	return &entity.Organizer{
		ID: "org-1", Name: "Live Inc.", ShortName: "live-inc", OwnerEmail: "owner@example.com",
		ProviderOrganizerID: "zitadel-org-1", Status: status, BusinessDetails: &details, PlatformFeeRateBps: 800,
	}
}

func assertProtoEqual(t *testing.T, want, got proto.Message) {
	t.Helper()
	assert.True(t, proto.Equal(want, got), "want %v, got %v", want, got)
}

func TestAdminOrganizerHandler_Create(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	req := &adminorganizerv1.CreateRequest{
		Name:       &entityv1.OrganizerName{Value: "Live Inc."},
		ShortName:  &entityv1.OrganizerShortName{Value: "live-inc"},
		OwnerEmail: &entityv1.Email{Value: "owner@example.com"},
	}

	t.Run("runs Create with the name, the short name and the owner email", func(t *testing.T) {
		t.Parallel()
		h, uc := newAdminOrganizerHandler(t)
		uc.EXPECT().Create(ctx, "Live Inc.", "live-inc", "owner@example.com").Return(adminOrganizer(entity.OrganizerStatusProvisioning), nil).Once()

		resp, err := h.Create(ctx, req)

		require.NoError(t, err)
		assertProtoEqual(t, wantAdminOrganizer(entityv1.OrganizerStatus_ORGANIZER_STATUS_PROVISIONING), resp.GetOrganizer())
	})

	// @spec components/adapter/admin/api/rpc/organizer "Short name already used"
	t.Run("a short name already used fails with AlreadyExists", func(t *testing.T) {
		t.Parallel()
		h, uc := newAdminOrganizerHandler(t)
		ucErr := apperr.New(codes.AlreadyExists, "the short name is already used")
		uc.EXPECT().Create(ctx, "Live Inc.", "live-inc", "owner@example.com").Return(nil, ucErr).Once()

		resp, err := h.Create(ctx, req)

		assert.Nil(t, resp)
		assert.Same(t, ucErr, err, "the usecase error is returned unchanged")
	})
}

func TestAdminOrganizerHandler_List(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// @spec components/adapter/admin/api/rpc/organizer "Admin lists Organizers and inspects a roster"
	// @spec components/adapter/admin/api/rpc/organizer "Admin lists Organizers in every status"
	//
	// The roster half of the first scenario holds by construction: the
	// service has no method that returns Artists (the compile-time assertion
	// in admin_organizer_handler.go pins its method set), and
	// AdminArtistHandler.ListByOrganizer returns them.
	t.Run("returns every Organizer with its short name and its own status", func(t *testing.T) {
		t.Parallel()
		h, uc := newAdminOrganizerHandler(t)
		uc.EXPECT().List(ctx).Return([]*entity.Organizer{
			{ID: "org-1", Name: "Provisioning", ShortName: "provisioning", Status: entity.OrganizerStatusProvisioning},
			{ID: "org-2", Name: "Active", ShortName: "active", Status: entity.OrganizerStatusActive},
			{ID: "org-3", Name: "Deactivated", ShortName: "deactivated", Status: entity.OrganizerStatusDeactivated},
		}, nil).Once()

		resp, err := h.List(ctx, &adminorganizerv1.ListRequest{})

		require.NoError(t, err)
		require.Len(t, resp.GetOrganizers(), 3)
		wantStatuses := []entityv1.OrganizerStatus{
			entityv1.OrganizerStatus_ORGANIZER_STATUS_PROVISIONING,
			entityv1.OrganizerStatus_ORGANIZER_STATUS_ACTIVE,
			entityv1.OrganizerStatus_ORGANIZER_STATUS_DEACTIVATED,
		}
		wantShortNames := []string{"provisioning", "active", "deactivated"}
		for i, o := range resp.GetOrganizers() {
			assert.Equal(t, wantStatuses[i], o.GetStatus())
			assert.Equal(t, wantShortNames[i], o.GetShortName().GetValue())
		}
	})

	t.Run("a usecase error is returned unchanged", func(t *testing.T) {
		t.Parallel()
		h, uc := newAdminOrganizerHandler(t)
		ucErr := apperr.New(codes.Unavailable, "db down")
		uc.EXPECT().List(ctx).Return(nil, ucErr).Once()

		resp, err := h.List(ctx, &adminorganizerv1.ListRequest{})

		assert.Nil(t, resp)
		assert.Same(t, ucErr, err)
	})
}

func TestAdminOrganizerHandler_Get(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	req := &adminorganizerv1.GetRequest{OrganizerId: &entityv1.OrganizerId{Value: "org-1"}}

	// @spec components/adapter/admin/api/rpc/organizer "Admin reads any Organizer"
	t.Run("returns a deactivated Organizer with its details and rate", func(t *testing.T) {
		t.Parallel()
		h, uc := newAdminOrganizerHandler(t)
		uc.EXPECT().Get(ctx, "org-1").Return(adminOrganizer(entity.OrganizerStatusDeactivated), nil).Once()

		resp, err := h.Get(ctx, req)

		require.NoError(t, err)
		assertProtoEqual(t, wantAdminOrganizer(entityv1.OrganizerStatus_ORGANIZER_STATUS_DEACTIVATED), resp.GetOrganizer())
	})

	t.Run("a usecase error is returned unchanged", func(t *testing.T) {
		t.Parallel()
		h, uc := newAdminOrganizerHandler(t)
		ucErr := apperr.New(codes.NotFound, "no such organizer")
		uc.EXPECT().Get(ctx, "org-1").Return(nil, ucErr).Once()

		resp, err := h.Get(ctx, req)

		assert.Nil(t, resp)
		assert.Same(t, ucErr, err)
	})
}

// TestAdminOrganizerHandler_Commands covers the calls that return an empty
// response: each runs the usecase method of the same name with the request's
// ids, and returns its error unchanged.
func TestAdminOrganizerHandler_Commands(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	orgID := &entityv1.OrganizerId{Value: "org-1"}
	artistID := &entityv1.ArtistId{Value: "artist-1"}

	tests := []struct {
		name   string
		expect func(uc *ucmocks.MockOrganizerUseCase, err error)
		call   func(h *handler.AdminOrganizerHandler) (proto.Message, error)
	}{
		{
			name: "AssociateArtist",
			expect: func(uc *ucmocks.MockOrganizerUseCase, err error) {
				uc.EXPECT().AssociateArtist(ctx, "org-1", "artist-1").Return(err).Once()
			},
			call: func(h *handler.AdminOrganizerHandler) (proto.Message, error) {
				resp, err := h.AssociateArtist(ctx, &adminorganizerv1.AssociateArtistRequest{OrganizerId: orgID, ArtistId: artistID})
				return orNil(resp), err
			},
		},
		{
			name: "DisassociateArtist",
			expect: func(uc *ucmocks.MockOrganizerUseCase, err error) {
				uc.EXPECT().DisassociateArtist(ctx, "org-1", "artist-1").Return(err).Once()
			},
			call: func(h *handler.AdminOrganizerHandler) (proto.Message, error) {
				resp, err := h.DisassociateArtist(ctx, &adminorganizerv1.DisassociateArtistRequest{OrganizerId: orgID, ArtistId: artistID})
				return orNil(resp), err
			},
		},
		{
			name: "Deactivate",
			expect: func(uc *ucmocks.MockOrganizerUseCase, err error) {
				uc.EXPECT().Deactivate(ctx, "org-1").Return(err).Once()
			},
			call: func(h *handler.AdminOrganizerHandler) (proto.Message, error) {
				resp, err := h.Deactivate(ctx, &adminorganizerv1.DeactivateRequest{OrganizerId: orgID})
				return orNil(resp), err
			},
		},
		{
			// @spec components/adapter/admin/api/rpc/organizer "Admin deletes a deactivated Organizer"
			// @spec components/adapter/admin/api/rpc/organizer "Delete refused"
			name: "Delete",
			expect: func(uc *ucmocks.MockOrganizerUseCase, err error) {
				uc.EXPECT().Delete(ctx, "org-1").Return(err).Once()
			},
			call: func(h *handler.AdminOrganizerHandler) (proto.Message, error) {
				resp, err := h.Delete(ctx, &adminorganizerv1.DeleteRequest{OrganizerId: orgID})
				return orNil(resp), err
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name+" runs its usecase method and returns an empty response", func(t *testing.T) {
			t.Parallel()
			h, uc := newAdminOrganizerHandler(t)
			tt.expect(uc, nil)

			resp, err := tt.call(h)

			require.NoError(t, err)
			require.NotNil(t, resp)
			assert.Zero(t, proto.Size(resp), "the response carries nothing")
		})

		t.Run(tt.name+" returns a usecase error unchanged", func(t *testing.T) {
			t.Parallel()
			h, uc := newAdminOrganizerHandler(t)
			ucErr := apperr.New(codes.FailedPrecondition, "refused")
			tt.expect(uc, ucErr)

			resp, err := tt.call(h)

			assert.Nil(t, resp)
			assert.Same(t, ucErr, err)
		})
	}
}

// orNil returns a nil proto.Message for a nil response, so a typed nil
// pointer does not hide behind the interface.
func orNil[T any, P interface {
	*T
	proto.Message
}](resp P) proto.Message {
	if resp == nil {
		return nil
	}
	return resp
}

func TestAdminOrganizerHandler_SetBusinessDetails(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	req := &adminorganizerv1.SetBusinessDetailsRequest{
		OrganizerId:         &entityv1.OrganizerId{Value: "org-1"},
		LegalName:           &entityv1.LegalName{Value: adminOrganizerDetails.LegalName},
		RepresentativeName:  &entityv1.RepresentativeName{Value: adminOrganizerDetails.RepresentativeName},
		BusinessAddress:     &entityv1.BusinessAddress{Value: adminOrganizerDetails.BusinessAddress},
		BusinessPhoneNumber: &entityv1.PhoneNumber{Value: adminOrganizerDetails.BusinessPhoneNumber},
		ContactEmail:        &entityv1.Email{Value: adminOrganizerDetails.ContactEmail},
	}

	// @spec components/adapter/admin/api/rpc/organizer "Admin records business details"
	t.Run("runs SetBusinessDetails with all five details and returns the Organizer with them", func(t *testing.T) {
		t.Parallel()
		h, uc := newAdminOrganizerHandler(t)
		uc.EXPECT().SetBusinessDetails(ctx, "org-1", adminOrganizerDetails).Return(adminOrganizer(entity.OrganizerStatusActive), nil).Once()

		resp, err := h.SetBusinessDetails(ctx, req)

		require.NoError(t, err)
		assertProtoEqual(t, wantAdminOrganizer(entityv1.OrganizerStatus_ORGANIZER_STATUS_ACTIVE), resp.GetOrganizer())
	})

	t.Run("a usecase error is returned unchanged", func(t *testing.T) {
		t.Parallel()
		h, uc := newAdminOrganizerHandler(t)
		ucErr := apperr.New(codes.InvalidArgument, "the legal name is too long")
		uc.EXPECT().SetBusinessDetails(ctx, "org-1", adminOrganizerDetails).Return(nil, ucErr).Once()

		resp, err := h.SetBusinessDetails(ctx, req)

		assert.Nil(t, resp)
		assert.Same(t, ucErr, err)
	})
}

func TestAdminOrganizerHandler_SetPlatformFeeRate(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	req := &adminorganizerv1.SetPlatformFeeRateRequest{
		OrganizerId:        &entityv1.OrganizerId{Value: "org-1"},
		PlatformFeeRateBps: proto.Int32(800),
	}

	t.Run("runs SetPlatformFeeRate with the rate and returns the Organizer with it", func(t *testing.T) {
		t.Parallel()
		h, uc := newAdminOrganizerHandler(t)
		uc.EXPECT().SetPlatformFeeRate(ctx, "org-1", 800).Return(adminOrganizer(entity.OrganizerStatusActive), nil).Once()

		resp, err := h.SetPlatformFeeRate(ctx, req)

		require.NoError(t, err)
		assertProtoEqual(t, wantAdminOrganizer(entityv1.OrganizerStatus_ORGANIZER_STATUS_ACTIVE), resp.GetOrganizer())
	})

	t.Run("a usecase error is returned unchanged", func(t *testing.T) {
		t.Parallel()
		h, uc := newAdminOrganizerHandler(t)
		ucErr := apperr.New(codes.NotFound, "no such organizer")
		uc.EXPECT().SetPlatformFeeRate(ctx, "org-1", 800).Return(nil, ucErr).Once()

		resp, err := h.SetPlatformFeeRate(ctx, req)

		assert.Nil(t, resp)
		assert.Same(t, ucErr, err)
	})
}
