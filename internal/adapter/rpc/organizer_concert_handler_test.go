package rpc_test

import (
	"context"
	"testing"

	organizerconcertv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/organizer/concert/v1"
	"connectrpc.com/connect"
	"github.com/liverty-music/backend/internal/adapter/rpc"
	"github.com/liverty-music/backend/internal/adapter/rpc/mapper"
	"github.com/liverty-music/backend/internal/entity"
	ucmocks "github.com/liverty-music/backend/internal/usecase/mocks"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// organizerConcertHandlerDeps bundles the mocked collaborators of
// OrganizerConcertHandler. None has an expectation unless a test sets one, so
// an unexpected call fails the test.
type organizerConcertHandlerDeps struct {
	authoringUC *ucmocks.MockConcertAuthoringUseCase
	organizerUC *ucmocks.MockOrganizerUseCase
	mediaUC     *ucmocks.MockMediaUseCase
	handler     *rpc.OrganizerConcertHandler
}

func newOrganizerConcertHandlerDeps(t *testing.T) *organizerConcertHandlerDeps {
	t.Helper()
	logger, err := logging.New()
	require.NoError(t, err)

	d := &organizerConcertHandlerDeps{
		authoringUC: ucmocks.NewMockConcertAuthoringUseCase(t),
		organizerUC: ucmocks.NewMockOrganizerUseCase(t),
		mediaUC:     ucmocks.NewMockMediaUseCase(t),
	}
	d.handler = rpc.NewOrganizerConcertHandler(
		d.authoringUC,
		d.organizerUC,
		d.mediaUC,
		mapper.NewMediaURLBuilder("https://cdn.example.com"),
		logger,
	)
	return d
}

// TestOrganizerConcertHandler_List verifies that an operator of an active
// Organizer lists that Organizer's own concerts.
func TestOrganizerConcertHandler_List(t *testing.T) {
	t.Parallel()

	// @spec components/adapter/organizer/api/rpc/concert "Operator of an active Organizer lists concerts"
	t.Run("return the concerts authored by the caller's own organizer", func(t *testing.T) {
		t.Parallel()
		d := newOrganizerConcertHandlerDeps(t)

		d.organizerUC.EXPECT().
			ResolveCaller(mock.Anything, testZitadelOrgID).
			Return(activeOrganizer(), nil).
			Once()
		events := []*entity.Event{{ID: "event-1"}}
		artists := []*entity.Artist{{ID: "artist-1", Name: "Artist One"}}
		d.authoringUC.EXPECT().
			ListOwn(mock.Anything, testOrganizerID).
			Return(
				[]*entity.Series{{ID: "series-1", Title: "Tour"}, {ID: "series-2", Title: "Festival"}},
				[]*[]*entity.Event{&events, nil},
				[]*[]*entity.Artist{&artists, nil},
				nil,
			).
			Once()

		resp, err := d.handler.List(orgCtx(testZitadelOrgID), connect.NewRequest(&organizerconcertv1.ListRequest{}))

		require.NoError(t, err)
		require.NotNil(t, resp)
		require.Len(t, resp.Msg.Concerts, 2)
		assert.Equal(t, "series-1", resp.Msg.Concerts[0].GetSeries().GetId().GetValue())
		assert.Equal(t, "series-2", resp.Msg.Concerts[1].GetSeries().GetId().GetValue())
	})
}

// TestOrganizerConcertHandler_ResolveCallerFailure verifies that every
// authoring call returns a failure of OrganizerUseCase.ResolveCaller unchanged
// and reaches neither the authoring nor the media usecase, so nothing is
// stored.
func TestOrganizerConcertHandler_ResolveCallerFailure(t *testing.T) {
	t.Parallel()

	calls := []struct {
		name string
		call func(ctx context.Context, h *rpc.OrganizerConcertHandler) error
	}{
		{"Create", func(ctx context.Context, h *rpc.OrganizerConcertHandler) error {
			_, err := h.Create(ctx, connect.NewRequest(&organizerconcertv1.CreateRequest{}))
			return err
		}},
		{"Update", func(ctx context.Context, h *rpc.OrganizerConcertHandler) error {
			_, err := h.Update(ctx, connect.NewRequest(&organizerconcertv1.UpdateRequest{}))
			return err
		}},
		{"Publish", func(ctx context.Context, h *rpc.OrganizerConcertHandler) error {
			_, err := h.Publish(ctx, connect.NewRequest(&organizerconcertv1.PublishRequest{}))
			return err
		}},
		{"Cancel", func(ctx context.Context, h *rpc.OrganizerConcertHandler) error {
			_, err := h.Cancel(ctx, connect.NewRequest(&organizerconcertv1.CancelRequest{}))
			return err
		}},
		{"List", func(ctx context.Context, h *rpc.OrganizerConcertHandler) error {
			_, err := h.List(ctx, connect.NewRequest(&organizerconcertv1.ListRequest{}))
			return err
		}},
		{"RegenerateToken", func(ctx context.Context, h *rpc.OrganizerConcertHandler) error {
			_, err := h.RegenerateToken(ctx, connect.NewRequest(&organizerconcertv1.RegenerateTokenRequest{}))
			return err
		}},
		{"CreateMediaUploadURL", func(ctx context.Context, h *rpc.OrganizerConcertHandler) error {
			_, err := h.CreateMediaUploadURL(ctx, connect.NewRequest(&organizerconcertv1.CreateMediaUploadURLRequest{}))
			return err
		}},
		{"AttachMedia", func(ctx context.Context, h *rpc.OrganizerConcertHandler) error {
			_, err := h.AttachMedia(ctx, connect.NewRequest(&organizerconcertv1.AttachMediaRequest{}))
			return err
		}},
	}

	type dep struct {
		resolveCallerErr error
	}
	tests := []struct {
		name     string
		dep      dep
		wantCode connect.Code
	}{
		{
			// @spec components/adapter/organizer/api/rpc/concert "Provisioning Organizer"
			name:     "return PERMISSION_DENIED unchanged when the organizer is provisioning",
			dep:      dep{resolveCallerErr: apperr.New(apperr.ErrPermissionDenied.Code, "permission denied")},
			wantCode: connect.CodePermissionDenied,
		},
		{
			// @spec components/adapter/organizer/api/rpc/concert "Deactivated Organizer"
			name:     "return FAILED_PRECONDITION unchanged when the organizer is deactivated",
			dep:      dep{resolveCallerErr: apperr.New(apperr.ErrFailedPrecondition.Code, "organizer is deactivated")},
			wantCode: connect.CodeFailedPrecondition,
		},
	}

	for _, tt := range tests {
		for _, c := range calls {
			t.Run(tt.name+"/"+c.name, func(t *testing.T) {
				t.Parallel()
				d := newOrganizerConcertHandlerDeps(t)

				// The authoring and media usecases have no expectations: any
				// call to them fails the test.
				d.organizerUC.EXPECT().
					ResolveCaller(mock.Anything, testZitadelOrgID).
					Return(nil, tt.dep.resolveCallerErr).
					Once()

				err := c.call(orgCtx(testZitadelOrgID), d.handler)

				require.Error(t, err)
				assert.ErrorIs(t, err, tt.dep.resolveCallerErr)
				assert.Equal(t, tt.wantCode, connectCodeOf(err))
			})
		}
	}
}
