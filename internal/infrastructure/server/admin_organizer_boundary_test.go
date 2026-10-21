package server_test

import (
	"context"
	"strings"
	"testing"

	adminartistv1connect "buf.build/gen/go/liverty-music/schema/connectrpc/go/v2/liverty_music/rpc/admin/artist/v1/artistv1connect"
	adminorganizerv1connect "buf.build/gen/go/liverty-music/schema/connectrpc/go/v2/liverty_music/rpc/admin/organizer/v1/organizerv1connect"
	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	adminartistv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/admin/artist/v1"
	adminorganizerv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/admin/organizer/v1"
	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/liverty-music/backend/internal/entity"
	usecasemocks "github.com/liverty-music/backend/internal/usecase/mocks"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

// newAdminOrganizerClient builds an admin OrganizerService client against the
// admin server of admin_delete_test.go (real authn, admin-role and validation
// interceptors) mounted on the given usecase mock.
func newAdminOrganizerClient(t *testing.T, organizerUC *usecasemocks.MockOrganizerUseCase) adminorganizerv1connect.OrganizerServiceClient {
	t.Helper()
	ts := newTestAdminDeleteServer(t, organizerUC, usecasemocks.NewMockUserUseCase(t))
	return adminorganizerv1connect.NewOrganizerServiceClient(connect.NewClient(connecthttp.NewTransport(ts.Client(), ts.URL)))
}

// newAdminArtistClient builds an admin ArtistService client against the same
// admin server, its ListByOrganizer served by the given OrganizerUseCase mock.
func newAdminArtistClient(t *testing.T, artistUC *usecasemocks.MockArtistUseCase) adminartistv1connect.ArtistServiceClient {
	t.Helper()
	ts := newTestAdminDeleteServer(t, usecasemocks.NewMockOrganizerUseCase(t), usecasemocks.NewMockUserUseCase(t), artistUC)
	return adminartistv1connect.NewArtistServiceClient(connect.NewClient(connecthttp.NewTransport(ts.Client(), ts.URL)))
}

// asAdmin returns the context and request of a call carrying the admin bearer
// token.
func asAdmin[T any](msg *T) (context.Context, *T) {
	return withBearer(context.Background(), deleteAdminToken), msg
}

func TestAdminServer_OrganizerCreateValidation(t *testing.T) {
	t.Parallel()

	// valid returns a well-formed request that mutate breaks.
	valid := func(mutate func(r *adminorganizerv1.CreateRequest)) *adminorganizerv1.CreateRequest {
		r := &adminorganizerv1.CreateRequest{
			Name:       &entityv1.OrganizerName{Value: "XX Records"},
			ShortName:  &entityv1.OrganizerShortName{Value: "xx-records"},
			OwnerEmail: &entityv1.Email{Value: "band@example.com"},
		}
		mutate(r)
		return r
	}

	tests := []struct {
		name string
		req  *adminorganizerv1.CreateRequest
	}{
		{
			// @spec components/adapter/admin/api/rpc/organizer "Name too long"
			name: "returns InvalidArgument for a 201-character name and creates nothing",
			req: valid(func(r *adminorganizerv1.CreateRequest) {
				r.Name = &entityv1.OrganizerName{Value: strings.Repeat("a", 201)}
			}),
		},
		{
			// @spec components/adapter/admin/api/rpc/organizer "Malformed short name"
			name: "returns InvalidArgument for the short name XX Records and creates nothing",
			req: valid(func(r *adminorganizerv1.CreateRequest) {
				r.ShortName = &entityv1.OrganizerShortName{Value: "XX Records"}
			}),
		},
		{
			name: "returns InvalidArgument for a reserved short name",
			req: valid(func(r *adminorganizerv1.CreateRequest) {
				r.ShortName = &entityv1.OrganizerShortName{Value: "admin"}
			}),
		},
		{
			name: "returns InvalidArgument without a short name",
			req:  valid(func(r *adminorganizerv1.CreateRequest) { r.ShortName = nil }),
		},
		{
			// @spec components/adapter/admin/api/rpc/organizer "Malformed owner email"
			name: "returns InvalidArgument for an owner email that is not an email address",
			req: valid(func(r *adminorganizerv1.CreateRequest) {
				r.OwnerEmail = &entityv1.Email{Value: "not-an-email"}
			}),
		},
		{
			name: "returns InvalidArgument for a 166-character owner email",
			req: valid(func(r *adminorganizerv1.CreateRequest) {
				r.OwnerEmail = &entityv1.Email{Value: strings.Repeat("a", 166-len("@example.com")) + "@example.com"}
			}),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			// The strict mock fails the test if OrganizerUseCase.Create runs.
			client := newAdminOrganizerClient(t, usecasemocks.NewMockOrganizerUseCase(t))

			_, err := client.Create(asAdmin(tt.req))

			assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
		})
	}
}

func TestAdminServer_OrganizerCreate(t *testing.T) {
	t.Parallel()

	req := &adminorganizerv1.CreateRequest{
		Name:       &entityv1.OrganizerName{Value: "XX Records"},
		ShortName:  &entityv1.OrganizerShortName{Value: "xx-records"},
		OwnerEmail: &entityv1.Email{Value: "band@example.com"},
	}

	t.Run("runs OrganizerUseCase.Create with the name, short name and owner email and returns the Organizer with its short name", func(t *testing.T) {
		t.Parallel()
		organizerUC := usecasemocks.NewMockOrganizerUseCase(t)
		organizerUC.EXPECT().Create(mock.Anything, "XX Records", "xx-records", "band@example.com").Return(&entity.Organizer{
			ID: "019a0000-0000-7000-8000-0000000000e4", Name: "XX Records", ShortName: "xx-records",
			OwnerEmail: "band@example.com", Status: entity.OrganizerStatusActive, PlatformFeeRateBps: 800,
		}, nil).Once()

		resp, err := newAdminOrganizerClient(t, organizerUC).Create(asAdmin(req))

		require.NoError(t, err)
		assert.Equal(t, "xx-records", resp.GetOrganizer().GetShortName().GetValue())
	})

	// @spec components/adapter/admin/api/rpc/organizer "Short name already used"
	t.Run("returns AlreadyExists unchanged when the short name is already used", func(t *testing.T) {
		t.Parallel()
		organizerUC := usecasemocks.NewMockOrganizerUseCase(t)
		organizerUC.EXPECT().Create(mock.Anything, "XX Records", "xx-records", "band@example.com").
			Return(nil, apperr.New(codes.AlreadyExists, "short name already used")).Once()

		_, err := newAdminOrganizerClient(t, organizerUC).Create(asAdmin(req))

		assert.Equal(t, connect.CodeAlreadyExists, connect.CodeOf(err))
	})
}

func TestAdminServer_OrganizerReads(t *testing.T) {
	t.Parallel()

	const (
		organizerID = "019a0000-0000-7000-8000-0000000000e1"
		otherID     = "019a0000-0000-7000-8000-0000000000e2"
		artistID    = "019a0000-0000-7000-8000-0000000000e3"
	)

	// @spec components/adapter/admin/api/rpc/organizer "Admin lists Organizers and inspects a roster"
	t.Run("lists every organizer and then one organizer's artists", func(t *testing.T) {
		t.Parallel()
		organizerUC := usecasemocks.NewMockOrganizerUseCase(t)
		organizerUC.EXPECT().List(mock.Anything).Return([]*entity.Organizer{
			{ID: organizerID, Name: "Acme Music", ShortName: "acme-music", Status: entity.OrganizerStatusActive},
			{ID: otherID, Name: "Old Label", ShortName: "old-label", Status: entity.OrganizerStatusDeactivated},
		}, nil).Once()
		artistUC := usecasemocks.NewMockArtistUseCase(t)
		artistUC.EXPECT().ListByOrganizer(mock.Anything, organizerID).
			Return([]*entity.Artist{{ID: artistID, Name: "The Band"}}, nil).Once()
		client := newAdminOrganizerClient(t, organizerUC)

		list, err := client.List(asAdmin(&adminorganizerv1.ListRequest{}))
		require.NoError(t, err)
		ids := make([]string, 0, len(list.GetOrganizers()))
		shortNames := make([]string, 0, len(list.GetOrganizers()))
		for _, o := range list.GetOrganizers() {
			ids = append(ids, o.GetId().GetValue())
			shortNames = append(shortNames, o.GetShortName().GetValue())
		}
		assert.Equal(t, []string{organizerID, otherID}, ids)
		assert.Equal(t, []string{"acme-music", "old-label"}, shortNames)

		// The roster comes from the admin ArtistService, not the Organizer service.
		roster, err := newAdminArtistClient(t, artistUC).ListByOrganizer(asAdmin(&adminartistv1.ListByOrganizerRequest{
			OrganizerId: &entityv1.OrganizerId{Value: organizerID},
		}))
		require.NoError(t, err)
		require.Len(t, roster.GetArtists(), 1)
		assert.Equal(t, artistID, roster.GetArtists()[0].GetId().GetValue())
	})

	// @spec components/adapter/admin/api/rpc/organizer "Admin lists Organizers in every status"
	t.Run("lists a provisioning, an active and a deactivated organizer each with its own status", func(t *testing.T) {
		t.Parallel()
		const provisioningID = "019a0000-0000-7000-8000-0000000000e5"
		organizerUC := usecasemocks.NewMockOrganizerUseCase(t)
		organizerUC.EXPECT().List(mock.Anything).Return([]*entity.Organizer{
			{ID: provisioningID, Name: "New Label", ShortName: "new-label", Status: entity.OrganizerStatusProvisioning},
			{ID: organizerID, Name: "Acme Music", ShortName: "acme-music", Status: entity.OrganizerStatusActive},
			{ID: otherID, Name: "Old Label", ShortName: "old-label", Status: entity.OrganizerStatusDeactivated},
		}, nil).Once()

		list, err := newAdminOrganizerClient(t, organizerUC).List(asAdmin(&adminorganizerv1.ListRequest{}))

		require.NoError(t, err)
		got := make(map[string]entityv1.OrganizerStatus, len(list.GetOrganizers()))
		for _, o := range list.GetOrganizers() {
			got[o.GetId().GetValue()] = o.GetStatus()
		}
		assert.Equal(t, map[string]entityv1.OrganizerStatus{
			provisioningID: entityv1.OrganizerStatus_ORGANIZER_STATUS_PROVISIONING,
			organizerID:    entityv1.OrganizerStatus_ORGANIZER_STATUS_ACTIVE,
			otherID:        entityv1.OrganizerStatus_ORGANIZER_STATUS_DEACTIVATED,
		}, got)
	})

	// @spec components/adapter/admin/api/rpc/organizer "Admin reads any Organizer"
	t.Run("returns a deactivated organizer with its id, name, short name, status, business details and fee rate, never its owner email", func(t *testing.T) {
		t.Parallel()
		organizerUC := usecasemocks.NewMockOrganizerUseCase(t)
		organizerUC.EXPECT().Get(mock.Anything, otherID).Return(&entity.Organizer{
			ID:                  otherID,
			Name:                "Old Label",
			ShortName:           "old-label",
			OwnerEmail:          "owner@example.com",
			ProviderOrganizerID: "zitadel-org-1",
			Status:              entity.OrganizerStatusDeactivated,
			BusinessDetails: &entity.BusinessDetails{
				LegalName:           "株式会社オールドレーベル",
				RepresentativeName:  "代表 太郎",
				BusinessAddress:     "東京都渋谷区1-2-3",
				BusinessPhoneNumber: "+81312345678",
				ContactEmail:        "contact@example.com",
			},
			PlatformFeeRateBps: 500,
		}, nil).Once()
		client := newAdminOrganizerClient(t, organizerUC)

		got, err := client.Get(asAdmin(&adminorganizerv1.GetRequest{
			OrganizerId: &entityv1.OrganizerId{Value: otherID},
		}))

		require.NoError(t, err)
		want := &entityv1.Organizer{
			Id:                  &entityv1.OrganizerId{Value: otherID},
			Name:                &entityv1.OrganizerName{Value: "Old Label"},
			ShortName:           &entityv1.OrganizerShortName{Value: "old-label"},
			Status:              entityv1.OrganizerStatus_ORGANIZER_STATUS_DEACTIVATED,
			LegalName:           &entityv1.LegalName{Value: "株式会社オールドレーベル"},
			RepresentativeName:  &entityv1.RepresentativeName{Value: "代表 太郎"},
			BusinessAddress:     &entityv1.BusinessAddress{Value: "東京都渋谷区1-2-3"},
			BusinessPhoneNumber: &entityv1.PhoneNumber{Value: "+81312345678"},
			ContactEmail:        &entityv1.Email{Value: "contact@example.com"},
			PlatformFeeRateBps:  500,
		}
		assert.True(t, proto.Equal(want, got.GetOrganizer()), "got %v", got.GetOrganizer())
	})
}
