package server_test

import (
	"context"
	"testing"

	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	adminartistv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/admin/artist/v1"
	adminorganizerv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/admin/organizer/v1"
	"connectrpc.com/connect"

	"github.com/liverty-music/backend/internal/entity"
	usecasemocks "github.com/liverty-music/backend/internal/usecase/mocks"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// asNonAdmin wraps msg in a request carrying a signed-in non-admin token.
func asNonAdmin[T any](msg *T) *connect.Request[T] {
	req := connect.NewRequest(msg)
	req.Header().Set("Authorization", "Bearer "+deleteNonAdminToken)
	return req
}

func TestAdminServer_ArtistListByOrganizer(t *testing.T) {
	t.Parallel()

	const organizerID = "019a0000-0000-7000-8000-0000000000e1"

	t.Run("admin inspects a roster", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/admin/api/rpc/artist "Admin inspects a roster"
		uc := usecasemocks.NewMockOrganizerUseCase(t)
		uc.EXPECT().ListArtists(mock.Anything, organizerID).Return([]*entity.Artist{
			{ID: "artist-1", Name: "Band One"},
			{ID: "artist-2", Name: "Band Two"},
		}, nil).Once()

		resp, err := newAdminArtistClient(t, uc).ListByOrganizer(context.Background(), asAdmin(&adminartistv1.ListByOrganizerRequest{
			OrganizerId: &entityv1.OrganizerId{Value: organizerID},
		}))

		require.NoError(t, err)
		require.Len(t, resp.Msg.GetArtists(), 2)
		assert.Equal(t, "artist-1", resp.Msg.GetArtists()[0].GetId().GetValue())
		assert.Equal(t, "artist-2", resp.Msg.GetArtists()[1].GetId().GetValue())
	})

	t.Run("missing OrganizerId", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/admin/api/rpc/artist "Missing OrganizerId"
		// The strict mock fails the test if the usecase runs.
		_, err := newAdminArtistClient(t, usecasemocks.NewMockOrganizerUseCase(t)).ListByOrganizer(context.Background(),
			asAdmin(&adminartistv1.ListByOrganizerRequest{}))

		assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
	})

	t.Run("unknown Organizer", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/admin/api/rpc/artist "Unknown Organizer"
		uc := usecasemocks.NewMockOrganizerUseCase(t)
		uc.EXPECT().ListArtists(mock.Anything, organizerID).Return(nil, apperr.New(codes.NotFound, "organizer not found")).Once()

		_, err := newAdminArtistClient(t, uc).ListByOrganizer(context.Background(), asAdmin(&adminartistv1.ListByOrganizerRequest{
			OrganizerId: &entityv1.OrganizerId{Value: organizerID},
		}))

		assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
	})

	t.Run("non-admin reads a roster", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/admin/api/rpc/artist "Non-admin reads a roster"
		_, err := newAdminArtistClient(t, usecasemocks.NewMockOrganizerUseCase(t)).ListByOrganizer(context.Background(),
			asNonAdmin(&adminartistv1.ListByOrganizerRequest{OrganizerId: &entityv1.OrganizerId{Value: organizerID}}))

		assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
	})
}

func TestAdminServer_OrganizerBusinessDetailsAndFeeRate(t *testing.T) {
	t.Parallel()

	rate := int32(500)
	details := entity.BusinessDetails{
		LegalName: "株式会社リバティ", RepresentativeName: "代表 太郎", BusinessAddress: "東京都渋谷区1-2-3",
		BusinessPhoneNumber: "+81312345678", ContactEmail: "contact@example.com",
	}
	setDetailsRequest := func(organizerID *entityv1.OrganizerId) *adminorganizerv1.SetBusinessDetailsRequest {
		return &adminorganizerv1.SetBusinessDetailsRequest{
			OrganizerId:         organizerID,
			LegalName:           &entityv1.LegalName{Value: details.LegalName},
			RepresentativeName:  &entityv1.RepresentativeName{Value: details.RepresentativeName},
			BusinessAddress:     &entityv1.BusinessAddress{Value: details.BusinessAddress},
			BusinessPhoneNumber: &entityv1.PhoneNumber{Value: details.BusinessPhoneNumber},
			ContactEmail:        &entityv1.ContactEmail{Value: details.ContactEmail},
		}
	}

	t.Run("non-admin sets a rate", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/admin/api/rpc/organizer "Non-admin sets a rate"
		_, err := newAdminOrganizerClient(t, usecasemocks.NewMockOrganizerUseCase(t)).SetPlatformFeeRate(context.Background(),
			asNonAdmin(&adminorganizerv1.SetPlatformFeeRateRequest{OrganizerId: &entityv1.OrganizerId{Value: deleteOrganizerID}, PlatformFeeRateBps: &rate}))

		assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
	})

	t.Run("missing OrganizerId", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/admin/api/rpc/organizer "Missing OrganizerId"
		client := newAdminOrganizerClient(t, usecasemocks.NewMockOrganizerUseCase(t))

		_, rateErr := client.SetPlatformFeeRate(context.Background(), asAdmin(&adminorganizerv1.SetPlatformFeeRateRequest{PlatformFeeRateBps: &rate}))
		_, detailsErr := client.SetBusinessDetails(context.Background(), asAdmin(setDetailsRequest(nil)))

		assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(rateErr))
		assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(detailsErr))
	})

	t.Run("missing rate", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/admin/api/rpc/organizer "Missing rate"
		_, err := newAdminOrganizerClient(t, usecasemocks.NewMockOrganizerUseCase(t)).SetPlatformFeeRate(context.Background(),
			asAdmin(&adminorganizerv1.SetPlatformFeeRateRequest{OrganizerId: &entityv1.OrganizerId{Value: deleteOrganizerID}}))

		assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
	})

	t.Run("rate over the bound", func(t *testing.T) {
		t.Parallel()
		over := int32(3100)

		_, err := newAdminOrganizerClient(t, usecasemocks.NewMockOrganizerUseCase(t)).SetPlatformFeeRate(context.Background(),
			asAdmin(&adminorganizerv1.SetPlatformFeeRateRequest{OrganizerId: &entityv1.OrganizerId{Value: deleteOrganizerID}, PlatformFeeRateBps: &over}))

		assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
	})

	t.Run("admin sets a zero rate", func(t *testing.T) {
		t.Parallel()
		uc := usecasemocks.NewMockOrganizerUseCase(t)
		uc.EXPECT().SetPlatformFeeRate(mock.Anything, deleteOrganizerID, 0).
			Return(&entity.Organizer{ID: deleteOrganizerID, Name: "Label", PlatformFeeRateBps: 0}, nil).Once()
		zero := int32(0)

		_, err := newAdminOrganizerClient(t, uc).SetPlatformFeeRate(context.Background(),
			asAdmin(&adminorganizerv1.SetPlatformFeeRateRequest{OrganizerId: &entityv1.OrganizerId{Value: deleteOrganizerID}, PlatformFeeRateBps: &zero}))

		assert.NoError(t, err)
	})

	t.Run("admin records seller details", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/admin/api/rpc/organizer "Admin records seller details"
		uc := usecasemocks.NewMockOrganizerUseCase(t)
		stored := details
		uc.EXPECT().SetBusinessDetails(mock.Anything, deleteOrganizerID, details).
			Return(&entity.Organizer{ID: deleteOrganizerID, Name: "Label", PlatformFeeRateBps: 800, BusinessDetails: &stored}, nil).Once()

		resp, err := newAdminOrganizerClient(t, uc).SetBusinessDetails(context.Background(),
			asAdmin(setDetailsRequest(&entityv1.OrganizerId{Value: deleteOrganizerID})))

		require.NoError(t, err)
		o := resp.Msg.GetOrganizer()
		assert.Equal(t, "株式会社リバティ", o.GetLegalName().GetValue())
		assert.Equal(t, "代表 太郎", o.GetRepresentativeName().GetValue())
		assert.Equal(t, "東京都渋谷区1-2-3", o.GetBusinessAddress().GetValue())
		assert.Equal(t, "+81312345678", o.GetBusinessPhoneNumber().GetValue())
		assert.Equal(t, "contact@example.com", o.GetContactEmail().GetValue())
		assert.Equal(t, int32(800), o.GetPlatformFeeRateBps())
	})

	t.Run("missing business detail", func(t *testing.T) {
		t.Parallel()
		req := setDetailsRequest(&entityv1.OrganizerId{Value: deleteOrganizerID})
		req.ContactEmail = nil

		_, err := newAdminOrganizerClient(t, usecasemocks.NewMockOrganizerUseCase(t)).SetBusinessDetails(context.Background(), asAdmin(req))

		assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
	})
}
