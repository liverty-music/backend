package server_test

import (
	"context"
	"strings"
	"testing"

	adminorganizerv1connect "buf.build/gen/go/liverty-music/schema/connectrpc/go/liverty_music/rpc/admin/organizer/v1/organizerv1connect"
	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	adminorganizerv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/admin/organizer/v1"
	"connectrpc.com/connect"

	"github.com/liverty-music/backend/internal/entity"
	usecasemocks "github.com/liverty-music/backend/internal/usecase/mocks"
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
	return adminorganizerv1connect.NewOrganizerServiceClient(ts.Client(), ts.URL)
}

// asAdmin wraps msg in a request carrying the admin bearer token.
func asAdmin[T any](msg *T) *connect.Request[T] {
	req := connect.NewRequest(msg)
	req.Header().Set("Authorization", "Bearer "+deleteAdminToken)
	return req
}

func TestAdminServer_OrganizerCreateValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		req  *adminorganizerv1.CreateRequest
	}{
		{
			// @spec components/adapter/admin/api/rpc/organizer "Name too long"
			name: "returns InvalidArgument for a 201-character name and creates nothing",
			req: &adminorganizerv1.CreateRequest{
				Name:          &entityv1.OrganizerName{Value: strings.Repeat("a", 201)},
				OperatorEmail: &entityv1.UserEmail{Value: "operator@example.com"},
			},
		},
		{
			// @spec components/adapter/admin/api/rpc/organizer "Malformed operator email"
			name: "returns InvalidArgument for an operator email that is not an email address",
			req: &adminorganizerv1.CreateRequest{
				Name:          &entityv1.OrganizerName{Value: "Acme Music"},
				OperatorEmail: &entityv1.UserEmail{Value: "not-an-email"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			// The strict mock fails the test if OrganizerUseCase.Create runs.
			client := newAdminOrganizerClient(t, usecasemocks.NewMockOrganizerUseCase(t))

			_, err := client.Create(context.Background(), asAdmin(tt.req))

			assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
		})
	}
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
			{ID: organizerID, Name: "Acme Music", Status: entity.OrganizerStatusActive},
			{ID: otherID, Name: "Old Label", Status: entity.OrganizerStatusDeactivated},
		}, nil).Once()
		organizerUC.EXPECT().ListArtists(mock.Anything, organizerID).
			Return([]*entity.Artist{{ID: artistID, Name: "The Band"}}, nil).Once()
		client := newAdminOrganizerClient(t, organizerUC)

		list, err := client.List(context.Background(), asAdmin(&adminorganizerv1.ListRequest{}))
		require.NoError(t, err)
		ids := make([]string, 0, len(list.Msg.GetOrganizers()))
		for _, o := range list.Msg.GetOrganizers() {
			ids = append(ids, o.GetId().GetValue())
		}
		assert.Equal(t, []string{organizerID, otherID}, ids)

		roster, err := client.ListArtists(context.Background(), asAdmin(&adminorganizerv1.ListArtistsRequest{
			OrganizerId: &entityv1.OrganizerId{Value: organizerID},
		}))
		require.NoError(t, err)
		require.Len(t, roster.Msg.GetArtists(), 1)
		assert.Equal(t, artistID, roster.Msg.GetArtists()[0].GetId().GetValue())
	})

	// @spec components/adapter/admin/api/rpc/organizer "Admin reads any Organizer"
	t.Run("returns a deactivated organizer with its id and name only", func(t *testing.T) {
		t.Parallel()
		organizerUC := usecasemocks.NewMockOrganizerUseCase(t)
		organizerUC.EXPECT().Get(mock.Anything, otherID).Return(&entity.Organizer{
			ID:            otherID,
			Name:          "Old Label",
			OperatorEmail: "operator@example.com",
			ZitadelOrgID:  "zitadel-org-1",
			Status:        entity.OrganizerStatusDeactivated,
		}, nil).Once()
		client := newAdminOrganizerClient(t, organizerUC)

		got, err := client.Get(context.Background(), asAdmin(&adminorganizerv1.GetRequest{
			OrganizerId: &entityv1.OrganizerId{Value: otherID},
		}))

		require.NoError(t, err)
		want := &entityv1.Organizer{
			Id:   &entityv1.OrganizerId{Value: otherID},
			Name: &entityv1.OrganizerName{Value: "Old Label"},
		}
		assert.True(t, proto.Equal(want, got.Msg.GetOrganizer()), "got %v", got.Msg.GetOrganizer())
	})
}
