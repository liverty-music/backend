package usecase_test

import (
	"context"
	"errors"
	"testing"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/entity/mocks"
	"github.com/liverty-music/backend/internal/usecase"
	ucmocks "github.com/liverty-music/backend/internal/usecase/mocks"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// organizerTestDeps holds all dependencies for OrganizerUseCase tests.
type organizerTestDeps struct {
	orgRepo     *mocks.MockOrganizerRepository
	artistRepo  *mocks.MockArtistRepository
	provisioner *ucmocks.MockOrganizerProvisioner
	mediaRepo   *mocks.MockMediaRepository
	imageStorer *ucmocks.MockImageStorer
	publisher   *ucmocks.MockEventPublisher
	metrics     *ucmocks.MockOrganizerMetrics
	uc          usecase.OrganizerUseCase
}

func newOrganizerTestDeps(t *testing.T) *organizerTestDeps {
	t.Helper()
	d := &organizerTestDeps{
		orgRepo:     mocks.NewMockOrganizerRepository(t),
		artistRepo:  mocks.NewMockArtistRepository(t),
		provisioner: ucmocks.NewMockOrganizerProvisioner(t),
		mediaRepo:   mocks.NewMockMediaRepository(t),
		imageStorer: ucmocks.NewMockImageStorer(t),
		publisher:   ucmocks.NewMockEventPublisher(t),
		metrics:     ucmocks.NewMockOrganizerMetrics(t),
	}
	d.uc = usecase.NewOrganizerUseCase(
		d.orgRepo,
		d.artistRepo,
		d.provisioner,
		d.mediaRepo,
		d.imageStorer,
		usecase.OrganizerMediaBuckets{Internal: testInternalBucket, Served: testServedBucket},
		d.publisher,
		d.metrics,
		newTestLogger(t),
	)
	return d
}

const (
	testInternalBucket = "organizer-media-internal"
	testServedBucket   = "organizer-media"
)

func TestOrganizerUseCase_Create(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	const (
		orgName = "Acme Music"
		email   = "operator@acme.com"
	)
	errEmailTaken := apperr.New(codes.AlreadyExists, "operator email is already used by another account")

	// expectInsert expects the operator-email check and the provisioning row
	// insert, returning the inserted row.
	expectInsert := func(d *organizerTestDeps) {
		d.provisioner.EXPECT().CheckOperatorEmailAvailable(ctx, email).Return(nil).Once()
		d.orgRepo.EXPECT().
			Create(ctx, mock.AnythingOfType("*entity.Organizer")).
			Return(&entity.Organizer{ID: "org-1", Name: orgName, OperatorEmail: email, Status: entity.OrganizerStatusProvisioning}, nil).
			Once()
	}
	// expectTenantOrg expects the org creation and the immediate recording of
	// its id.
	expectTenantOrg := func(d *organizerTestDeps, calls *[]string) {
		d.provisioner.EXPECT().EnsureTenantOrg(mock.Anything, "org-1").Run(func(context.Context, string) {
			*calls = append(*calls, "ensure-org")
		}).Return("zitadel-org-1", nil).Once()
		d.orgRepo.EXPECT().SetZitadelOrgID(mock.Anything, "org-1", "zitadel-org-1").Run(func(context.Context, string, string) {
			*calls = append(*calls, "store-org-id")
		}).Return(nil).Once()
	}
	// expectDiscard expects the compensation of a permanent failure: Deactivate
	// then Delete of the row and its tenant org. deleteErr fails the final
	// record deletion.
	expectDiscard := func(d *organizerTestDeps, calls *[]string, deleteErr error) {
		withOrg := &entity.Organizer{ID: "org-1", ZitadelOrgID: "zitadel-org-1", Status: entity.OrganizerStatusProvisioning}
		deactivated := &entity.Organizer{ID: "org-1", ZitadelOrgID: "zitadel-org-1", Status: entity.OrganizerStatusDeactivated}
		d.orgRepo.EXPECT().Get(mock.Anything, "org-1").Return(withOrg, nil).Once()
		d.provisioner.EXPECT().DeactivateOperators(mock.Anything, "zitadel-org-1").Return(nil).Once()
		d.orgRepo.EXPECT().FreeArtists(mock.Anything, "org-1").Return(nil).Once()
		d.orgRepo.EXPECT().SetStatus(mock.Anything, "org-1", entity.OrganizerStatusDeactivated).Run(func(context.Context, string, entity.OrganizerStatus) {
			*calls = append(*calls, "deactivate")
		}).Return(nil).Once()
		d.orgRepo.EXPECT().Get(mock.Anything, "org-1").Return(deactivated, nil).Once()
		d.orgRepo.EXPECT().Delete(mock.Anything, "org-1", true).Return(nil).Once()
		d.mediaRepo.EXPECT().ListMediaByOrganizer(mock.Anything, "org-1").Return(nil, nil).Once()
		d.provisioner.EXPECT().DeleteTenant(mock.Anything, "zitadel-org-1").Run(func(context.Context, string) {
			*calls = append(*calls, "delete-tenant")
		}).Return(nil).Once()
		d.orgRepo.EXPECT().Delete(mock.Anything, "org-1", false).Run(func(context.Context, string, bool) {
			*calls = append(*calls, "delete-row")
		}).Return(deleteErr).Once()
	}

	tests := []struct {
		name      string
		setup     func(d *organizerTestDeps, calls *[]string)
		want      func(t *testing.T, got *entity.Organizer)
		wantCalls []string
		wantErr   error
	}{
		{
			// @spec components/usecase/organizer/create "Organizer is created"
			name: "return active organizer with ZitadelOrgID set when all steps succeed, recording the org id before the remaining steps",
			setup: func(d *organizerTestDeps, calls *[]string) {
				expectInsert(d)
				expectTenantOrg(d, calls)
				d.provisioner.EXPECT().ProvisionTenant(mock.Anything, "org-1", "zitadel-org-1", email).Run(func(context.Context, string, string, string) {
					*calls = append(*calls, "provision")
				}).Return(nil).Once()
				d.orgRepo.EXPECT().
					CompareAndSetStatus(mock.Anything, "org-1", entity.OrganizerStatusProvisioning, entity.OrganizerStatusActive).
					Return(true, nil).
					Once()
				d.metrics.EXPECT().RecordOrganizerProvisioning(mock.Anything, "success").Return().Once()
				d.publisher.EXPECT().
					PublishEvent(mock.Anything, entity.SubjectOrganizerCreated, entity.OrganizerCreatedData{OrganizerID: "org-1"}).
					Return(nil).
					Once()
			},
			want: func(t *testing.T, got *entity.Organizer) {
				t.Helper()
				assert.Equal(t, entity.OrganizerStatusActive, got.Status)
				assert.Equal(t, "zitadel-org-1", got.ZitadelOrgID)
				assert.Equal(t, "org-1", got.ID)
			},
			wantCalls: []string{"ensure-org", "store-org-id", "provision"},
		},
		{
			// @spec components/usecase/organizer/create "Operator email used by another account"
			name: "fail with AlreadyExists and create nothing when the operator email is used by another account",
			setup: func(d *organizerTestDeps, _ *[]string) {
				// No row, no org: the check runs before anything is created.
				d.provisioner.EXPECT().CheckOperatorEmailAvailable(ctx, email).Return(errEmailTaken).Once()
			},
			wantErr: apperr.ErrAlreadyExists,
		},
		{
			// @spec components/usecase/organizer/create "Operator email taken during provisioning"
			name: "remove the tenant org and the row when provisioning finds the email used by a user in another org",
			setup: func(d *organizerTestDeps, calls *[]string) {
				expectInsert(d)
				expectTenantOrg(d, calls)
				d.provisioner.EXPECT().ProvisionTenant(mock.Anything, "org-1", "zitadel-org-1", email).Return(errEmailTaken).Once()
				d.metrics.EXPECT().RecordOrganizerProvisioning(mock.Anything, "failed").Return().Once()
				expectDiscard(d, calls, nil)
			},
			wantCalls: []string{"ensure-org", "store-org-id", "deactivate", "delete-tenant", "delete-row"},
			wantErr:   apperr.ErrAlreadyExists,
		},
		{
			name: "still return AlreadyExists, leaving a deactivated row, when the compensation cannot delete the row",
			setup: func(d *organizerTestDeps, calls *[]string) {
				expectInsert(d)
				expectTenantOrg(d, calls)
				d.provisioner.EXPECT().ProvisionTenant(mock.Anything, "org-1", "zitadel-org-1", email).Return(errEmailTaken).Once()
				d.metrics.EXPECT().RecordOrganizerProvisioning(mock.Anything, "failed").Return().Once()
				expectDiscard(d, calls, apperr.New(codes.Internal, "db down"))
			},
			wantCalls: []string{"ensure-org", "store-org-id", "deactivate", "delete-tenant", "delete-row"},
			wantErr:   apperr.ErrAlreadyExists,
		},
		{
			// @spec components/usecase/organizer/create "Provisioning fails"
			name: "leave the row provisioning with its org id recorded when a step after org creation fails transiently",
			setup: func(d *organizerTestDeps, calls *[]string) {
				expectInsert(d)
				expectTenantOrg(d, calls)
				d.provisioner.EXPECT().ProvisionTenant(mock.Anything, "org-1", "zitadel-org-1", email).Return(apperr.ErrInternal).Once()
				d.metrics.EXPECT().RecordOrganizerProvisioning(mock.Anything, "failed").Return().Once()
				// No compensation: the reconciler retries with the recorded id.
			},
			wantCalls: []string{"ensure-org", "store-org-id"},
			wantErr:   apperr.ErrInternal,
		},
		{
			name: "return error and record failed metric when the tenant org cannot be created",
			setup: func(d *organizerTestDeps, _ *[]string) {
				expectInsert(d)
				d.provisioner.EXPECT().EnsureTenantOrg(mock.Anything, "org-1").Return("", apperr.ErrInternal).Once()
				d.metrics.EXPECT().RecordOrganizerProvisioning(mock.Anything, "failed").Return().Once()
			},
			wantErr: apperr.ErrInternal,
		},
		{
			// @spec components/usecase/organizer/create "Deactivated meanwhile"
			name: "do not clobber a concurrent deactivation when activation is superseded",
			setup: func(d *organizerTestDeps, calls *[]string) {
				expectInsert(d)
				expectTenantOrg(d, calls)
				d.provisioner.EXPECT().ProvisionTenant(mock.Anything, "org-1", "zitadel-org-1", email).Return(nil).Once()
				// A concurrent Deactivate already moved the row out of provisioning,
				// so the CAS does not apply. The saga must NOT record success, emit
				// organizer.created, or force the row back to active.
				d.orgRepo.EXPECT().
					CompareAndSetStatus(mock.Anything, "org-1", entity.OrganizerStatusProvisioning, entity.OrganizerStatusActive).
					Return(false, nil).
					Once()
			},
			want: func(t *testing.T, got *entity.Organizer) {
				t.Helper()
				assert.Equal(t, entity.OrganizerStatusProvisioning, got.Status)
			},
			wantCalls: []string{"ensure-org", "store-org-id"},
		},
		{
			name: "return error when repository Create fails",
			setup: func(d *organizerTestDeps, _ *[]string) {
				d.provisioner.EXPECT().CheckOperatorEmailAvailable(ctx, email).Return(nil).Once()
				d.orgRepo.EXPECT().
					Create(ctx, mock.AnythingOfType("*entity.Organizer")).
					Return(nil, apperr.ErrInternal).
					Once()
			},
			wantErr: apperr.ErrInternal,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			d := newOrganizerTestDeps(t)
			var calls []string
			tt.setup(d, &calls)

			got, err := d.uc.Create(ctx, orgName, email)

			assert.Equal(t, tt.wantCalls, calls)
			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				assert.Nil(t, got)
				return
			}
			assert.NoError(t, err)
			if tt.want != nil {
				tt.want(t, got)
			}
		})
	}
}

// @spec components/usecase/organizer/create "Same name and operator email again"
func TestOrganizerUseCase_Create_sameNameAndEmailTwice(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	const (
		orgName = "Acme Music"
		email   = "operator@acme.com"
	)
	d := newOrganizerTestDeps(t)

	// Create never looks up an existing Organizer: each call inserts its own row
	// and creates its own tenant org.
	d.provisioner.EXPECT().CheckOperatorEmailAvailable(ctx, email).Return(nil).Times(2)
	for _, id := range []string{"org-1", "org-2"} {
		d.orgRepo.EXPECT().
			Create(ctx, mock.AnythingOfType("*entity.Organizer")).
			Return(&entity.Organizer{ID: id, Name: orgName, OperatorEmail: email, Status: entity.OrganizerStatusProvisioning}, nil).
			Once()
		d.provisioner.EXPECT().EnsureTenantOrg(mock.Anything, id).Return("zitadel-"+id, nil).Once()
		d.orgRepo.EXPECT().SetZitadelOrgID(mock.Anything, id, "zitadel-"+id).Return(nil).Once()
		d.provisioner.EXPECT().ProvisionTenant(mock.Anything, id, "zitadel-"+id, email).Return(nil).Once()
		d.orgRepo.EXPECT().
			CompareAndSetStatus(mock.Anything, id, entity.OrganizerStatusProvisioning, entity.OrganizerStatusActive).
			Return(true, nil).
			Once()
		d.publisher.EXPECT().
			PublishEvent(mock.Anything, entity.SubjectOrganizerCreated, entity.OrganizerCreatedData{OrganizerID: id}).
			Return(nil).
			Once()
	}
	d.metrics.EXPECT().RecordOrganizerProvisioning(mock.Anything, "success").Return().Times(2)

	first, err := d.uc.Create(ctx, orgName, email)
	require.NoError(t, err)
	second, err := d.uc.Create(ctx, orgName, email)
	require.NoError(t, err)

	assert.NotEqual(t, first.ID, second.ID, "two Organizers exist")
	assert.NotEqual(t, first.ZitadelOrgID, second.ZitadelOrgID, "each Organizer has its own tenant")
	assert.Equal(t, entity.OrganizerStatusActive, first.Status)
	assert.Equal(t, entity.OrganizerStatusActive, second.Status)
}

func TestOrganizerUseCase_Get(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	tests := []struct {
		name    string
		id      string
		setup   func(t *testing.T, d *organizerTestDeps)
		want    *entity.Organizer
		wantErr error
	}{
		{
			name: "return organizer when found",
			id:   "org-1",
			setup: func(t *testing.T, d *organizerTestDeps) {
				t.Helper()
				d.orgRepo.EXPECT().
					Get(ctx, "org-1").
					Return(&entity.Organizer{ID: "org-1", Name: "Acme"}, nil).
					Once()
			},
			want: &entity.Organizer{ID: "org-1", Name: "Acme"},
		},
		{
			name: "return NotFound error when organizer does not exist",
			id:   "missing-org",
			setup: func(t *testing.T, d *organizerTestDeps) {
				t.Helper()
				d.orgRepo.EXPECT().
					Get(ctx, "missing-org").
					Return(nil, apperr.New(codes.NotFound, "not found")).
					Once()
			},
			wantErr: apperr.ErrNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			d := newOrganizerTestDeps(t)
			if tt.setup != nil {
				tt.setup(t, d)
			}

			got, err := d.uc.Get(ctx, tt.id)

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				return
			}
			assert.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestOrganizerUseCase_List(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	t.Run("return all organizers from repository", func(t *testing.T) {
		t.Parallel()
		d := newOrganizerTestDeps(t)
		want := []*entity.Organizer{
			{ID: "org-1", Name: "Acme"},
			{ID: "org-2", Name: "Beta"},
		}
		d.orgRepo.EXPECT().List(ctx).Return(want, nil).Once()

		got, err := d.uc.List(ctx)

		assert.NoError(t, err)
		assert.Equal(t, want, got)
	})
}

func TestOrganizerUseCase_ListArtists(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	tests := []struct {
		name        string
		organizerID string
		setup       func(t *testing.T, d *organizerTestDeps)
		want        []*entity.Artist
		wantErr     error
	}{
		{
			name:        "return artists for existing organizer",
			organizerID: "org-1",
			setup: func(t *testing.T, d *organizerTestDeps) {
				t.Helper()
				d.orgRepo.EXPECT().
					Get(ctx, "org-1").
					Return(&entity.Organizer{ID: "org-1", Status: entity.OrganizerStatusActive}, nil).
					Once()
				want := []*entity.Artist{{ID: "artist-1"}, {ID: "artist-2"}}
				d.orgRepo.EXPECT().
					ListArtists(ctx, "org-1").
					Return(want, nil).
					Once()
			},
			want: []*entity.Artist{{ID: "artist-1"}, {ID: "artist-2"}},
		},
		{
			name:        "return NotFound error when organizer does not exist",
			organizerID: "missing-org",
			setup: func(t *testing.T, d *organizerTestDeps) {
				t.Helper()
				d.orgRepo.EXPECT().
					Get(ctx, "missing-org").
					Return(nil, apperr.New(codes.NotFound, "not found")).
					Once()
			},
			wantErr: apperr.ErrNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			d := newOrganizerTestDeps(t)
			if tt.setup != nil {
				tt.setup(t, d)
			}

			got, err := d.uc.ListArtists(ctx, tt.organizerID)

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				return
			}
			assert.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestOrganizerUseCase_ListOwnArtists(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	tests := []struct {
		name              string
		callerOrganizerID string
		reqOrganizerID    string
		setup             func(t *testing.T, d *organizerTestDeps)
		want              []*entity.Artist
		wantErr           error
	}{
		{
			// @spec components/usecase/organizer/list-own-artists "Own roster"
			name:              "return own roster when reqOrganizerID matches the caller",
			callerOrganizerID: "org-1",
			reqOrganizerID:    "org-1",
			setup: func(t *testing.T, d *organizerTestDeps) {
				t.Helper()
				d.orgRepo.EXPECT().
					Get(ctx, "org-1").
					Return(&entity.Organizer{ID: "org-1", Status: entity.OrganizerStatusActive}, nil).
					Once()
				want := []*entity.Artist{{ID: "artist-1"}, {ID: "artist-2"}}
				d.orgRepo.EXPECT().
					ListArtists(ctx, "org-1").
					Return(want, nil).
					Once()
			},
			want: []*entity.Artist{{ID: "artist-1"}, {ID: "artist-2"}},
		},
		{
			// @spec components/usecase/organizer/list-own-artists "Another Organizer's roster"
			name:              "PermissionDenied when reqOrganizerID does not match the caller",
			callerOrganizerID: "org-1",
			reqOrganizerID:    "org-999",
			// Neither Get nor ListArtists must be called — the ownership
			// check runs before touching the repository.
			wantErr: apperr.ErrPermissionDenied,
		},
		{
			// @spec components/usecase/organizer/list-own-artists "Own Organizer gone"
			name:              "NotFound when the caller's own organizer no longer exists",
			callerOrganizerID: "org-1",
			reqOrganizerID:    "org-1",
			setup: func(t *testing.T, d *organizerTestDeps) {
				t.Helper()
				d.orgRepo.EXPECT().
					Get(ctx, "org-1").
					Return(nil, apperr.New(codes.NotFound, "not found")).
					Once()
			},
			wantErr: apperr.ErrNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			d := newOrganizerTestDeps(t)
			if tt.setup != nil {
				tt.setup(t, d)
			}

			got, err := d.uc.ListOwnArtists(ctx, tt.callerOrganizerID, tt.reqOrganizerID)

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				return
			}
			assert.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestOrganizerUseCase_ResolveCaller(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	tests := []struct {
		name         string
		zitadelOrgID string
		setup        func(t *testing.T, d *organizerTestDeps)
		want         *entity.Organizer
		wantErr      error
	}{
		{
			// @spec components/usecase/organizer/resolve-caller "Active Organizer"
			name:         "return the organizer when active",
			zitadelOrgID: "zitadel-org-1",
			setup: func(t *testing.T, d *organizerTestDeps) {
				t.Helper()
				d.orgRepo.EXPECT().
					GetByZitadelOrgID(ctx, "zitadel-org-1").
					Return(&entity.Organizer{ID: "org-1", ZitadelOrgID: "zitadel-org-1", Status: entity.OrganizerStatusActive}, nil).
					Once()
			},
			want: &entity.Organizer{ID: "org-1", ZitadelOrgID: "zitadel-org-1", Status: entity.OrganizerStatusActive},
		},
		{
			// @spec components/usecase/organizer/resolve-caller "Deactivated Organizer"
			name:         "FailedPrecondition when the organizer is deactivated",
			zitadelOrgID: "zitadel-org-deactivated",
			setup: func(t *testing.T, d *organizerTestDeps) {
				t.Helper()
				d.orgRepo.EXPECT().
					GetByZitadelOrgID(ctx, "zitadel-org-deactivated").
					Return(&entity.Organizer{ID: "org-1", Status: entity.OrganizerStatusDeactivated}, nil).
					Once()
			},
			wantErr: apperr.ErrFailedPrecondition,
		},
		{
			// Non-revealing per spec D3: any non-Active, non-Deactivated
			// status (e.g. still provisioning) collapses into the same
			// PermissionDenied as "no such organizer."
			// @spec components/usecase/organizer/resolve-caller "Provisioning Organizer"
			name:         "PermissionDenied when the organizer is still provisioning",
			zitadelOrgID: "zitadel-org-provisioning",
			setup: func(t *testing.T, d *organizerTestDeps) {
				t.Helper()
				d.orgRepo.EXPECT().
					GetByZitadelOrgID(ctx, "zitadel-org-provisioning").
					Return(&entity.Organizer{ID: "org-1", Status: entity.OrganizerStatusProvisioning}, nil).
					Once()
			},
			wantErr: apperr.ErrPermissionDenied,
		},
		{
			// @spec components/usecase/organizer/resolve-caller "Tenant with no Organizer"
			name:         "PermissionDenied (non-revealing) when no organizer is linked",
			zitadelOrgID: "zitadel-org-unknown",
			setup: func(t *testing.T, d *organizerTestDeps) {
				t.Helper()
				d.orgRepo.EXPECT().
					GetByZitadelOrgID(ctx, "zitadel-org-unknown").
					Return(nil, apperr.New(codes.NotFound, "not found")).
					Once()
			},
			wantErr: apperr.ErrPermissionDenied,
		},
		{
			// @spec components/usecase/organizer/resolve-caller "Organizer unreadable"
			name:         "propagates a non-NotFound repository failure unchanged",
			zitadelOrgID: "zitadel-org-broken",
			setup: func(t *testing.T, d *organizerTestDeps) {
				t.Helper()
				d.orgRepo.EXPECT().
					GetByZitadelOrgID(ctx, "zitadel-org-broken").
					Return(nil, apperr.New(codes.Internal, "db down")).
					Once()
			},
			wantErr: apperr.ErrInternal,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			d := newOrganizerTestDeps(t)
			if tt.setup != nil {
				tt.setup(t, d)
			}

			got, err := d.uc.ResolveCaller(ctx, tt.zitadelOrgID)

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				assert.Nil(t, got)
				return
			}
			assert.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestOrganizerUseCase_AssociateArtist(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	type args struct {
		organizerID string
		artistID    string
	}

	tests := []struct {
		name    string
		args    args
		setup   func(t *testing.T, d *organizerTestDeps)
		wantErr error
	}{
		{
			name: "success when organizer is active and artist exists",
			args: args{organizerID: "org-1", artistID: "artist-1"},
			setup: func(t *testing.T, d *organizerTestDeps) {
				t.Helper()
				d.orgRepo.EXPECT().
					Get(ctx, "org-1").
					Return(&entity.Organizer{ID: "org-1", Status: entity.OrganizerStatusActive}, nil).
					Once()
				d.artistRepo.EXPECT().
					Get(ctx, "artist-1").
					Return(&entity.Artist{ID: "artist-1"}, nil).
					Once()
				d.orgRepo.EXPECT().
					AssociateArtist(ctx, "org-1", "artist-1").
					Return(nil).
					Once()
				d.publisher.EXPECT().
					PublishEvent(mock.Anything, entity.SubjectOrganizerArtistAssociated, entity.OrganizerArtistAssociatedData{OrganizerID: "org-1", ArtistID: "artist-1"}).
					Return(nil).
					Once()
			},
		},
		{
			name: "return NotFound error when organizer does not exist",
			args: args{organizerID: "missing-org", artistID: "artist-1"},
			setup: func(t *testing.T, d *organizerTestDeps) {
				t.Helper()
				d.orgRepo.EXPECT().
					Get(ctx, "missing-org").
					Return(nil, apperr.New(codes.NotFound, "not found")).
					Once()
				// artistRepo.Get must NOT be called.
			},
			wantErr: apperr.ErrNotFound,
		},
		{
			name: "return FailedPrecondition when organizer is deactivated without calling artistRepo",
			args: args{organizerID: "org-1", artistID: "artist-1"},
			setup: func(t *testing.T, d *organizerTestDeps) {
				t.Helper()
				d.orgRepo.EXPECT().
					Get(ctx, "org-1").
					Return(&entity.Organizer{ID: "org-1", Status: entity.OrganizerStatusDeactivated}, nil).
					Once()
				// artistRepo.Get must NOT be called.
			},
			wantErr: apperr.ErrFailedPrecondition,
		},
		{
			name: "return NotFound error when artist does not exist",
			args: args{organizerID: "org-1", artistID: "missing-artist"},
			setup: func(t *testing.T, d *organizerTestDeps) {
				t.Helper()
				d.orgRepo.EXPECT().
					Get(ctx, "org-1").
					Return(&entity.Organizer{ID: "org-1", Status: entity.OrganizerStatusActive}, nil).
					Once()
				d.artistRepo.EXPECT().
					Get(ctx, "missing-artist").
					Return(nil, apperr.New(codes.NotFound, "artist not found")).
					Once()
			},
			wantErr: apperr.ErrNotFound,
		},
		{
			name: "return AlreadyExists when artist is already associated with an organizer",
			args: args{organizerID: "org-1", artistID: "artist-1"},
			setup: func(t *testing.T, d *organizerTestDeps) {
				t.Helper()
				d.orgRepo.EXPECT().
					Get(ctx, "org-1").
					Return(&entity.Organizer{ID: "org-1", Status: entity.OrganizerStatusActive}, nil).
					Once()
				d.artistRepo.EXPECT().
					Get(ctx, "artist-1").
					Return(&entity.Artist{ID: "artist-1"}, nil).
					Once()
				d.orgRepo.EXPECT().
					AssociateArtist(ctx, "org-1", "artist-1").
					Return(apperr.New(codes.AlreadyExists, "artist already associated")).
					Once()
			},
			wantErr: apperr.ErrAlreadyExists,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			d := newOrganizerTestDeps(t)
			if tt.setup != nil {
				tt.setup(t, d)
			}

			err := d.uc.AssociateArtist(ctx, tt.args.organizerID, tt.args.artistID)

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				return
			}
			assert.NoError(t, err)
		})
	}
}

func TestOrganizerUseCase_DisassociateArtist(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	type args struct {
		organizerID string
		artistID    string
	}

	tests := []struct {
		name    string
		args    args
		setup   func(t *testing.T, d *organizerTestDeps)
		wantErr error
	}{
		{
			name: "success when organizer is active",
			args: args{organizerID: "org-1", artistID: "artist-1"},
			setup: func(t *testing.T, d *organizerTestDeps) {
				t.Helper()
				d.orgRepo.EXPECT().
					Get(ctx, "org-1").
					Return(&entity.Organizer{ID: "org-1", Status: entity.OrganizerStatusActive}, nil).
					Once()
				d.orgRepo.EXPECT().
					DisassociateArtist(ctx, "org-1", "artist-1").
					Return(nil).
					Once()
			},
		},
		{
			name: "return FailedPrecondition when organizer is deactivated",
			args: args{organizerID: "org-1", artistID: "artist-1"},
			setup: func(t *testing.T, d *organizerTestDeps) {
				t.Helper()
				d.orgRepo.EXPECT().
					Get(ctx, "org-1").
					Return(&entity.Organizer{ID: "org-1", Status: entity.OrganizerStatusDeactivated}, nil).
					Once()
			},
			wantErr: apperr.ErrFailedPrecondition,
		},
		{
			name: "return NotFound error when organizer does not exist",
			args: args{organizerID: "missing-org", artistID: "artist-1"},
			setup: func(t *testing.T, d *organizerTestDeps) {
				t.Helper()
				d.orgRepo.EXPECT().
					Get(ctx, "missing-org").
					Return(nil, apperr.New(codes.NotFound, "not found")).
					Once()
			},
			wantErr: apperr.ErrNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			d := newOrganizerTestDeps(t)
			if tt.setup != nil {
				tt.setup(t, d)
			}

			err := d.uc.DisassociateArtist(ctx, tt.args.organizerID, tt.args.artistID)

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				return
			}
			assert.NoError(t, err)
		})
	}
}

func TestOrganizerUseCase_Deactivate(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	tests := []struct {
		name        string
		organizerID string
		setup       func(t *testing.T, d *organizerTestDeps)
		wantErr     error
	}{
		{
			// @spec components/usecase/organizer/deactivate "Unknown Organizer"
			name:        "return NotFound error when organizer does not exist",
			organizerID: "missing-org",
			setup: func(t *testing.T, d *organizerTestDeps) {
				t.Helper()
				d.orgRepo.EXPECT().
					Get(ctx, "missing-org").
					Return(nil, apperr.New(codes.NotFound, "not found")).
					Once()
			},
			wantErr: apperr.ErrNotFound,
		},
		{
			name:        "return nil without writes when organizer is already deactivated",
			organizerID: "org-1",
			setup: func(t *testing.T, d *organizerTestDeps) {
				t.Helper()
				d.orgRepo.EXPECT().
					Get(ctx, "org-1").
					Return(&entity.Organizer{
						ID:           "org-1",
						Status:       entity.OrganizerStatusDeactivated,
						ZitadelOrgID: "zitadel-org-1",
					}, nil).
					Once()
				// provisioner.DeactivateOperators, orgRepo.FreeArtists, and
				// orgRepo.SetStatus must NOT be called (idempotent path).
			},
		},
		{
			// @spec components/usecase/organizer/deactivate "Active Organizer is deactivated"
			name:        "deactivate provisioner operators, free artists, and set status when organizer is active with ZitadelOrgID",
			organizerID: "org-1",
			setup: func(t *testing.T, d *organizerTestDeps) {
				t.Helper()
				d.orgRepo.EXPECT().
					Get(ctx, "org-1").
					Return(&entity.Organizer{
						ID:           "org-1",
						Status:       entity.OrganizerStatusActive,
						ZitadelOrgID: "zitadel-org-1",
					}, nil).
					Once()
				d.provisioner.EXPECT().
					DeactivateOperators(ctx, "zitadel-org-1").
					Return(nil).
					Once()
				d.orgRepo.EXPECT().
					FreeArtists(ctx, "org-1").
					Return(nil).
					Once()
				d.orgRepo.EXPECT().
					SetStatus(ctx, "org-1", entity.OrganizerStatusDeactivated).
					Return(nil).
					Once()
			},
		},
		{
			// @spec components/usecase/organizer/deactivate "Unlinked tenant"
			name:        "deactivate the operators of a provisioning organizer whose unrecorded tenant org is found by name",
			organizerID: "org-3",
			setup: func(t *testing.T, d *organizerTestDeps) {
				t.Helper()
				d.orgRepo.EXPECT().
					Get(ctx, "org-3").
					Return(&entity.Organizer{ID: "org-3", Status: entity.OrganizerStatusProvisioning}, nil).
					Once()
				d.provisioner.EXPECT().FindTenantOrg(ctx, "org-3").Return("zitadel-org-3", nil).Once()
				d.provisioner.EXPECT().DeactivateOperators(ctx, "zitadel-org-3").Return(nil).Once()
				d.orgRepo.EXPECT().FreeArtists(ctx, "org-3").Return(nil).Once()
				d.orgRepo.EXPECT().SetStatus(ctx, "org-3", entity.OrganizerStatusDeactivated).Return(nil).Once()
			},
		},
		{
			name:        "return the error and keep the status when the unrecorded tenant org cannot be searched",
			organizerID: "org-3",
			setup: func(t *testing.T, d *organizerTestDeps) {
				t.Helper()
				d.orgRepo.EXPECT().
					Get(ctx, "org-3").
					Return(&entity.Organizer{ID: "org-3", Status: entity.OrganizerStatusProvisioning}, nil).
					Once()
				d.provisioner.EXPECT().FindTenantOrg(ctx, "org-3").Return("", apperr.ErrInternal).Once()
			},
			wantErr: apperr.ErrInternal,
		},
		{
			// @spec components/usecase/organizer/deactivate "Provisioning Organizer without a tenant"
			name:        "skip DeactivateOperators when the organizer has no tenant org but still free artists and set status",
			organizerID: "org-2",
			setup: func(t *testing.T, d *organizerTestDeps) {
				t.Helper()
				d.orgRepo.EXPECT().
					Get(ctx, "org-2").
					Return(&entity.Organizer{
						ID:           "org-2",
						Status:       entity.OrganizerStatusProvisioning,
						ZitadelOrgID: "", // not yet provisioned
					}, nil).
					Once()
				d.provisioner.EXPECT().FindTenantOrg(ctx, "org-2").Return("", apperr.New(codes.NotFound, "tenant org not found")).Once()
				// provisioner.DeactivateOperators must NOT be called.
				d.orgRepo.EXPECT().
					FreeArtists(ctx, "org-2").
					Return(nil).
					Once()
				d.orgRepo.EXPECT().
					SetStatus(ctx, "org-2", entity.OrganizerStatusDeactivated).
					Return(nil).
					Once()
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			d := newOrganizerTestDeps(t)
			if tt.setup != nil {
				tt.setup(t, d)
			}

			err := d.uc.Deactivate(ctx, tt.organizerID)

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				return
			}
			assert.NoError(t, err)
		})
	}
}

// TestOrganizerUseCase_Deactivate_IdempotentIsDistinct verifies via errors.Is
// that the already-deactivated path truly returns nil (not a code-equal error).
func TestOrganizerUseCase_Deactivate_IdempotentIsDistinct(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	d := newOrganizerTestDeps(t)

	d.orgRepo.EXPECT().
		Get(ctx, "org-1").
		Return(&entity.Organizer{
			ID:     "org-1",
			Status: entity.OrganizerStatusDeactivated,
		}, nil).
		Once()

	err := d.uc.Deactivate(ctx, "org-1")

	// Must be exactly nil — not an error whose code is "success".
	assert.True(t, err == nil, "expected nil error for already-deactivated organizer")
	assert.False(t, errors.Is(err, apperr.ErrNotFound))
}

func TestOrganizerUseCase_ReconcileProvisioning(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// expectActivation expects the tail of a successful completion.
	expectActivation := func(d *organizerTestDeps, id string) {
		d.orgRepo.EXPECT().CompareAndSetStatus(mock.Anything, id, entity.OrganizerStatusProvisioning, entity.OrganizerStatusActive).Return(true, nil).Once()
		d.metrics.EXPECT().RecordOrganizerProvisioning(mock.Anything, "success").Return().Once()
		d.publisher.EXPECT().PublishEvent(mock.Anything, entity.SubjectOrganizerCreated, entity.OrganizerCreatedData{OrganizerID: id}).Return(nil).Once()
	}

	// @spec components/usecase/organizer/reconcile-provisioning "Tenant already recorded"
	// @spec components/usecase/organizer/reconcile-provisioning "Organizer left provisioning"
	t.Run("completes every organizer stuck in provisioning, reusing a recorded org id and resolving a missing one", func(t *testing.T) {
		t.Parallel()
		d := newOrganizerTestDeps(t)
		stuck := []*entity.Organizer{
			// Failed after the org id was recorded: the retry reuses the id and
			// never creates or searches for the org.
			{ID: "org-1", Name: "Acme", OperatorEmail: "a@acme.com", ZitadelOrgID: "z-org-1", Status: entity.OrganizerStatusProvisioning},
			// Left by an older release that never recorded the id: the org is
			// resolved (EnsureTenantOrg finds it by name) and recorded.
			{ID: "org-2", Name: "Beta", OperatorEmail: "b@beta.com", Status: entity.OrganizerStatusProvisioning},
		}
		d.orgRepo.EXPECT().ListByStatus(ctx, entity.OrganizerStatusProvisioning).Return(stuck, nil).Once()

		d.provisioner.EXPECT().ProvisionTenant(mock.Anything, "org-1", "z-org-1", "a@acme.com").Return(nil).Once()
		expectActivation(d, "org-1")

		d.provisioner.EXPECT().EnsureTenantOrg(mock.Anything, "org-2").Return("z-org-2", nil).Once()
		d.orgRepo.EXPECT().SetZitadelOrgID(mock.Anything, "org-2", "z-org-2").Return(nil).Once()
		d.provisioner.EXPECT().ProvisionTenant(mock.Anything, "org-2", "z-org-2", "b@beta.com").Return(nil).Once()
		expectActivation(d, "org-2")

		assert.NoError(t, d.uc.ReconcileProvisioning(ctx))
		// EnsureTenantOrg must not run for org-1 (mockery fails on unexpected calls).
	})

	// @spec components/usecase/organizer/reconcile-provisioning "One Organizer fails"
	t.Run("skips a transiently failing organizer, leaving it provisioning, and continues the sweep", func(t *testing.T) {
		t.Parallel()
		d := newOrganizerTestDeps(t)
		stuck := []*entity.Organizer{
			{ID: "org-bad", Name: "Bad", OperatorEmail: "x@bad.com", ZitadelOrgID: "z-bad", Status: entity.OrganizerStatusProvisioning},
			{ID: "org-ok", Name: "Ok", OperatorEmail: "y@ok.com", ZitadelOrgID: "z-ok", Status: entity.OrganizerStatusProvisioning},
		}
		d.orgRepo.EXPECT().ListByStatus(ctx, entity.OrganizerStatusProvisioning).Return(stuck, nil).Once()

		// org-bad: still failing transiently -> failed metric, no status change.
		d.provisioner.EXPECT().ProvisionTenant(mock.Anything, "org-bad", "z-bad", "x@bad.com").Return(apperr.ErrInternal).Once()
		d.metrics.EXPECT().RecordOrganizerProvisioning(mock.Anything, "failed").Return().Once()

		d.provisioner.EXPECT().ProvisionTenant(mock.Anything, "org-ok", "z-ok", "y@ok.com").Return(nil).Once()
		expectActivation(d, "org-ok")

		assert.NoError(t, d.uc.ReconcileProvisioning(ctx))
	})

	// @spec components/usecase/organizer/reconcile-provisioning "Operator email taken"
	t.Run("deactivates an organizer whose operator email is used by another account so it is not retried forever", func(t *testing.T) {
		t.Parallel()
		d := newOrganizerTestDeps(t)
		// The prod shape of #564: the row never recorded its org id.
		stuck := &entity.Organizer{ID: "org-taken", Name: "Taken", OperatorEmail: "admin@example.com", Status: entity.OrganizerStatusProvisioning}
		d.orgRepo.EXPECT().ListByStatus(ctx, entity.OrganizerStatusProvisioning).Return([]*entity.Organizer{stuck}, nil).Once()

		d.provisioner.EXPECT().EnsureTenantOrg(mock.Anything, "org-taken").Return("394277002850336802", nil).Once()
		d.orgRepo.EXPECT().SetZitadelOrgID(mock.Anything, "org-taken", "394277002850336802").Return(nil).Once()
		d.provisioner.EXPECT().ProvisionTenant(mock.Anything, "org-taken", "394277002850336802", "admin@example.com").
			Return(apperr.New(codes.AlreadyExists, "operator email is already used by another account")).Once()
		d.metrics.EXPECT().RecordOrganizerProvisioning(mock.Anything, "failed").Return().Once()

		// Terminal: Deactivate moves it out of provisioning; nothing is deleted.
		d.orgRepo.EXPECT().Get(ctx, "org-taken").Return(&entity.Organizer{ID: "org-taken", ZitadelOrgID: "394277002850336802", Status: entity.OrganizerStatusProvisioning}, nil).Once()
		d.provisioner.EXPECT().DeactivateOperators(ctx, "394277002850336802").Return(nil).Once()
		d.orgRepo.EXPECT().FreeArtists(ctx, "org-taken").Return(nil).Once()
		d.orgRepo.EXPECT().SetStatus(ctx, "org-taken", entity.OrganizerStatusDeactivated).Return(nil).Once()

		assert.NoError(t, d.uc.ReconcileProvisioning(ctx))
	})

	// @spec components/usecase/organizer/reconcile-provisioning "Listing fails"
	t.Run("returns error when listing stuck organizers fails", func(t *testing.T) {
		t.Parallel()
		d := newOrganizerTestDeps(t)
		d.orgRepo.EXPECT().ListByStatus(ctx, entity.OrganizerStatusProvisioning).Return(nil, apperr.ErrInternal).Once()
		assert.ErrorIs(t, d.uc.ReconcileProvisioning(ctx), apperr.ErrInternal)
	})
}

func TestOrganizerUseCase_Delete(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	const orgID = "org-delete"
	deactivated := &entity.Organizer{ID: orgID, ZitadelOrgID: "zitadel-tenant", Status: entity.OrganizerStatusDeactivated}
	media := []*entity.Media{{ID: "media-cover", OrganizerID: orgID}, {ID: "media-unused", OrganizerID: orgID}}
	errTenant := apperr.New(codes.Internal, "remove tenant org")

	// expectRemoval expects every removal step of a deactivated organizer and
	// appends each step to calls as it runs.
	expectRemoval := func(d *organizerTestDeps, org *entity.Organizer, calls *[]string, tenantErr error) {
		d.orgRepo.EXPECT().Get(ctx, orgID).Return(org, nil).Once()
		d.orgRepo.EXPECT().Delete(ctx, orgID, true).Run(func(context.Context, string, bool) {
			*calls = append(*calls, "check")
		}).Return(nil).Once()
		d.mediaRepo.EXPECT().ListMediaByOrganizer(ctx, orgID).Return(media, nil).Once()
		for _, m := range media {
			d.imageStorer.EXPECT().DeleteOriginal(ctx, testInternalBucket, orgID, m.ID).Run(func(context.Context, string, string, string) {
				*calls = append(*calls, "original:"+m.ID)
			}).Return(nil).Once()
			d.imageStorer.EXPECT().DeleteVariants(ctx, testServedBucket, orgID, m.ID).Run(func(context.Context, string, string, string) {
				*calls = append(*calls, "variants:"+m.ID)
			}).Return(nil).Once()
		}
		if org.ZitadelOrgID != "" {
			d.provisioner.EXPECT().DeleteTenant(ctx, org.ZitadelOrgID).Run(func(context.Context, string) {
				*calls = append(*calls, "tenant")
			}).Return(tenantErr).Once()
		} else {
			// No recorded link: the tenant org is searched by name and absent.
			d.provisioner.EXPECT().FindTenantOrg(ctx, orgID).Return("", apperr.New(codes.NotFound, "tenant org not found")).Once()
		}
		if tenantErr == nil {
			d.orgRepo.EXPECT().Delete(ctx, orgID, false).Run(func(context.Context, string, bool) {
				*calls = append(*calls, "records")
			}).Return(nil).Once()
		}
	}
	filesThen := func(tail ...string) []string {
		return append([]string{"check",
			"original:media-cover", "variants:media-cover",
			"original:media-unused", "variants:media-unused",
		}, tail...)
	}

	tests := []struct {
		name string
		// noStorage builds the usecase without an image storer or buckets.
		noStorage bool
		setup     func(d *organizerTestDeps, calls *[]string)
		wantCalls []string
		wantErr   error
	}{
		{
			// @spec components/usecase/organizer/delete "Deactivated test Organizer"
			name: "removes the media files, then the tenant, then the records",
			setup: func(d *organizerTestDeps, calls *[]string) {
				expectRemoval(d, deactivated, calls, nil)
			},
			wantCalls: filesThen("tenant", "records"),
		},
		{
			// @spec components/usecase/organizer/delete "Refunded order"
			name: "removes an organizer whose only order is refunded, as the record check passes",
			setup: func(d *organizerTestDeps, calls *[]string) {
				expectRemoval(d, deactivated, calls, nil)
			},
			wantCalls: filesThen("tenant", "records"),
		},
		{
			// @spec components/usecase/organizer/delete "Organizer without a tenant"
			name: "skips the tenant when the organizer has no tenant link",
			setup: func(d *organizerTestDeps, calls *[]string) {
				noTenant := *deactivated
				noTenant.ZitadelOrgID = ""
				expectRemoval(d, &noTenant, calls, nil)
			},
			wantCalls: filesThen("records"),
		},
		{
			name: "removes a tenant org whose link was never recorded, found by its name",
			setup: func(d *organizerTestDeps, calls *[]string) {
				// A provisioning Organizer from before the org id was recorded
				// early, deactivated by the admin: its org must not be orphaned.
				unlinked := *deactivated
				unlinked.ZitadelOrgID = ""
				d.orgRepo.EXPECT().Get(ctx, orgID).Return(&unlinked, nil).Once()
				d.orgRepo.EXPECT().Delete(ctx, orgID, true).Return(nil).Once()
				d.mediaRepo.EXPECT().ListMediaByOrganizer(ctx, orgID).Return(nil, nil).Once()
				d.provisioner.EXPECT().FindTenantOrg(ctx, orgID).Return("394277002850336802", nil).Once()
				d.provisioner.EXPECT().DeleteTenant(ctx, "394277002850336802").Run(func(context.Context, string) {
					*calls = append(*calls, "tenant")
				}).Return(nil).Once()
				d.orgRepo.EXPECT().Delete(ctx, orgID, false).Run(func(context.Context, string, bool) {
					*calls = append(*calls, "records")
				}).Return(nil).Once()
			},
			wantCalls: []string{"tenant", "records"},
		},
		{
			name: "keeps the records when the unrecorded tenant org cannot be searched",
			setup: func(d *organizerTestDeps, _ *[]string) {
				unlinked := *deactivated
				unlinked.ZitadelOrgID = ""
				d.orgRepo.EXPECT().Get(ctx, orgID).Return(&unlinked, nil).Once()
				d.orgRepo.EXPECT().Delete(ctx, orgID, true).Return(nil).Once()
				d.mediaRepo.EXPECT().ListMediaByOrganizer(ctx, orgID).Return(nil, nil).Once()
				d.provisioner.EXPECT().FindTenantOrg(ctx, orgID).Return("", apperr.ErrInternal).Once()
			},
			wantErr: apperr.ErrInternal,
		},
		{
			// @spec components/usecase/organizer/delete "Active Organizer"
			name: "refuses an active organizer and removes nothing",
			setup: func(d *organizerTestDeps, _ *[]string) {
				d.orgRepo.EXPECT().Get(ctx, orgID).Return(&entity.Organizer{ID: orgID, ZitadelOrgID: "zitadel-tenant", Status: entity.OrganizerStatusActive}, nil).Once()
			},
			wantErr: apperr.ErrFailedPrecondition,
		},
		{
			// @spec components/usecase/organizer/delete "Unknown Organizer"
			name: "returns NotFound for an unknown organizer",
			setup: func(d *organizerTestDeps, _ *[]string) {
				d.orgRepo.EXPECT().Get(ctx, orgID).Return(nil, apperr.New(codes.NotFound, "organizer not found")).Once()
			},
			wantErr: apperr.ErrNotFound,
		},
		{
			// @spec components/usecase/organizer/delete "Paid order"
			name: "refuses before any file or the tenant is removed when the record check is blocked",
			setup: func(d *organizerTestDeps, _ *[]string) {
				d.orgRepo.EXPECT().Get(ctx, orgID).Return(deactivated, nil).Once()
				d.orgRepo.EXPECT().Delete(ctx, orgID, true).Return(apperr.New(codes.FailedPrecondition, "order not refunded")).Once()
			},
			wantErr: apperr.ErrFailedPrecondition,
		},
		{
			name:      "returns Internal before removing anything when media storage is not configured",
			noStorage: true,
			setup: func(d *organizerTestDeps, _ *[]string) {
				d.orgRepo.EXPECT().Get(ctx, orgID).Return(deactivated, nil).Once()
				d.orgRepo.EXPECT().Delete(ctx, orgID, true).Return(nil).Once()
				d.mediaRepo.EXPECT().ListMediaByOrganizer(ctx, orgID).Return(media, nil).Once()
			},
			wantErr: apperr.ErrInternal,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			d := newOrganizerTestDeps(t)
			if tt.noStorage {
				d.uc = usecase.NewOrganizerUseCase(d.orgRepo, d.artistRepo, d.provisioner, d.mediaRepo, nil,
					usecase.OrganizerMediaBuckets{}, d.publisher, d.metrics, newTestLogger(t))
			}
			var calls []string
			tt.setup(d, &calls)

			err := d.uc.Delete(ctx, orgID)

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				return
			}
			assert.NoError(t, err)
			assert.Equal(t, tt.wantCalls, calls)
		})
	}

	// @spec components/usecase/organizer/delete "Tenant removal fails"
	t.Run("a failed tenant removal leaves the records and a second call completes the deletion", func(t *testing.T) {
		t.Parallel()
		d := newOrganizerTestDeps(t)
		var calls []string
		expectRemoval(d, deactivated, &calls, errTenant)

		err := d.uc.Delete(ctx, orgID)

		assert.ErrorIs(t, err, apperr.ErrInternal)
		assert.Equal(t, filesThen("tenant"), calls, "the records must remain after the tenant fails")

		calls = nil
		expectRemoval(d, deactivated, &calls, nil)

		assert.NoError(t, d.uc.Delete(ctx, orgID))
		assert.Equal(t, filesThen("tenant", "records"), calls)
	})
}

// TestOrganizerUseCase_CleanUpStuckProvisioningOrganizer walks the admin
// cleanup of an Organizer stuck in provisioning without a recorded tenant link
// (the prod shape of #564): Deactivate accepts it, and Delete then removes the
// tenant org found by name, so nothing is orphaned in Zitadel.
func TestOrganizerUseCase_CleanUpStuckProvisioningOrganizer(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	const (
		orgID        = "01a11dec-968d-762a-a64d-17f4ce185b92"
		zitadelOrgID = "394277002850336802"
	)
	d := newOrganizerTestDeps(t)

	// Deactivate: the provisioning row is accepted and its tenant is resolved.
	d.orgRepo.EXPECT().Get(ctx, orgID).Return(&entity.Organizer{ID: orgID, Status: entity.OrganizerStatusProvisioning}, nil).Once()
	d.provisioner.EXPECT().FindTenantOrg(ctx, orgID).Return(zitadelOrgID, nil).Once()
	d.provisioner.EXPECT().DeactivateOperators(ctx, zitadelOrgID).Return(nil).Once()
	d.orgRepo.EXPECT().FreeArtists(ctx, orgID).Return(nil).Once()
	d.orgRepo.EXPECT().SetStatus(ctx, orgID, entity.OrganizerStatusDeactivated).Return(nil).Once()

	assert.NoError(t, d.uc.Deactivate(ctx, orgID))

	// Delete: the blocker check runs first, then the tenant org found by name
	// is removed, then the records.
	var calls []string
	d.orgRepo.EXPECT().Get(ctx, orgID).Return(&entity.Organizer{ID: orgID, Status: entity.OrganizerStatusDeactivated}, nil).Once()
	d.orgRepo.EXPECT().Delete(ctx, orgID, true).Run(func(context.Context, string, bool) {
		calls = append(calls, "check")
	}).Return(nil).Once()
	d.mediaRepo.EXPECT().ListMediaByOrganizer(ctx, orgID).Return(nil, nil).Once()
	d.provisioner.EXPECT().FindTenantOrg(ctx, orgID).Return(zitadelOrgID, nil).Once()
	d.provisioner.EXPECT().DeleteTenant(ctx, zitadelOrgID).Run(func(context.Context, string) {
		calls = append(calls, "tenant")
	}).Return(nil).Once()
	d.orgRepo.EXPECT().Delete(ctx, orgID, false).Run(func(context.Context, string, bool) {
		calls = append(calls, "records")
	}).Return(nil).Once()

	assert.NoError(t, d.uc.Delete(ctx, orgID))
	assert.Equal(t, []string{"check", "tenant", "records"}, calls)
}
