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
)

// organizerTestDeps holds all dependencies for OrganizerUseCase tests.
type organizerTestDeps struct {
	orgRepo     *mocks.MockOrganizerRepository
	artistRepo  *mocks.MockArtistRepository
	provisioner *mocks.MockOrganizerProvisioner
	publisher   *ucmocks.MockEventPublisher
	metrics     *ucmocks.MockOrganizerMetrics
	uc          usecase.OrganizerUseCase
}

func newOrganizerTestDeps(t *testing.T) *organizerTestDeps {
	t.Helper()
	d := &organizerTestDeps{
		orgRepo:     mocks.NewMockOrganizerRepository(t),
		artistRepo:  mocks.NewMockArtistRepository(t),
		provisioner: mocks.NewMockOrganizerProvisioner(t),
		publisher:   ucmocks.NewMockEventPublisher(t),
		metrics:     ucmocks.NewMockOrganizerMetrics(t),
	}
	d.uc = usecase.NewOrganizerUseCase(
		d.orgRepo,
		d.artistRepo,
		d.provisioner,
		d.publisher,
		d.metrics,
		newTestLogger(t),
	)
	return d
}

func TestOrganizerUseCase_Create(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	type args struct {
		name          string
		operatorEmail string
	}

	tests := []struct {
		name    string
		args    args
		setup   func(t *testing.T, d *organizerTestDeps)
		want    func(t *testing.T, got *entity.Organizer)
		wantErr error
	}{
		{
			name: "return active organizer with ZitadelOrgID set when all steps succeed",
			args: args{
				name:          "Acme Music",
				operatorEmail: "operator@acme.com",
			},
			setup: func(t *testing.T, d *organizerTestDeps) {
				t.Helper()
				provisioning := &entity.Organizer{
					ID:            "org-1",
					Name:          "Acme Music",
					OperatorEmail: "operator@acme.com",
					Status:        entity.OrganizerStatusProvisioning,
				}
				d.orgRepo.EXPECT().
					Create(ctx, mock.AnythingOfType("*entity.Organizer")).
					Return(provisioning, nil).
					Once()
				d.provisioner.EXPECT().
					ProvisionTenant(mock.Anything, "org-1", "Acme Music", "operator@acme.com").
					Return("zitadel-org-1", nil).
					Once()
				d.metrics.EXPECT().
					RecordOrganizerProvisioning(mock.Anything, "success").
					Return().
					Once()
				d.orgRepo.EXPECT().
					SetZitadelOrgID(mock.Anything, "org-1", "zitadel-org-1").
					Return(nil).
					Once()
				d.orgRepo.EXPECT().
					SetStatus(mock.Anything, "org-1", entity.OrganizerStatusActive).
					Return(nil).
					Once()
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
		},
		{
			name: "return error and record failed metric when provisioner fails",
			args: args{
				name:          "Acme Music",
				operatorEmail: "operator@acme.com",
			},
			setup: func(t *testing.T, d *organizerTestDeps) {
				t.Helper()
				provisioning := &entity.Organizer{
					ID:            "org-1",
					Name:          "Acme Music",
					OperatorEmail: "operator@acme.com",
					Status:        entity.OrganizerStatusProvisioning,
				}
				d.orgRepo.EXPECT().
					Create(ctx, mock.AnythingOfType("*entity.Organizer")).
					Return(provisioning, nil).
					Once()
				d.provisioner.EXPECT().
					ProvisionTenant(mock.Anything, "org-1", "Acme Music", "operator@acme.com").
					Return("", apperr.ErrInternal).
					Once()
				d.metrics.EXPECT().
					RecordOrganizerProvisioning(mock.Anything, "failed").
					Return().
					Once()
				// SetZitadelOrgID and SetStatus(active) must NOT be called.
			},
			wantErr: apperr.ErrInternal,
		},
		{
			name: "return error when repository Create fails",
			args: args{
				name:          "Fail Corp",
				operatorEmail: "op@fail.com",
			},
			setup: func(t *testing.T, d *organizerTestDeps) {
				t.Helper()
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
			if tt.setup != nil {
				tt.setup(t, d)
			}

			got, err := d.uc.Create(ctx, tt.args.name, tt.args.operatorEmail)

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
			name:        "skip DeactivateOperators when ZitadelOrgID is empty but still free artists and set status",
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

	t.Run("completes every organizer stuck in provisioning", func(t *testing.T) {
		t.Parallel()
		d := newOrganizerTestDeps(t)
		stuck := []*entity.Organizer{
			{ID: "org-1", Name: "Acme", OperatorEmail: "a@acme.com", Status: entity.OrganizerStatusProvisioning},
			{ID: "org-2", Name: "Beta", OperatorEmail: "b@beta.com", Status: entity.OrganizerStatusProvisioning},
		}
		d.orgRepo.EXPECT().ListByStatus(ctx, entity.OrganizerStatusProvisioning).Return(stuck, nil).Once()
		for _, o := range stuck {
			d.provisioner.EXPECT().ProvisionTenant(mock.Anything, o.ID, o.Name, o.OperatorEmail).Return("z-"+o.ID, nil).Once()
			d.metrics.EXPECT().RecordOrganizerProvisioning(mock.Anything, "success").Return().Once()
			d.orgRepo.EXPECT().SetZitadelOrgID(mock.Anything, o.ID, "z-"+o.ID).Return(nil).Once()
			d.orgRepo.EXPECT().SetStatus(mock.Anything, o.ID, entity.OrganizerStatusActive).Return(nil).Once()
			d.publisher.EXPECT().PublishEvent(mock.Anything, entity.SubjectOrganizerCreated, entity.OrganizerCreatedData{OrganizerID: o.ID}).Return(nil).Once()
		}

		assert.NoError(t, d.uc.ReconcileProvisioning(ctx))
	})

	t.Run("skips a failing organizer and continues the sweep", func(t *testing.T) {
		t.Parallel()
		d := newOrganizerTestDeps(t)
		stuck := []*entity.Organizer{
			{ID: "org-bad", Name: "Bad", OperatorEmail: "x@bad.com", Status: entity.OrganizerStatusProvisioning},
			{ID: "org-ok", Name: "Ok", OperatorEmail: "y@ok.com", Status: entity.OrganizerStatusProvisioning},
		}
		d.orgRepo.EXPECT().ListByStatus(ctx, entity.OrganizerStatusProvisioning).Return(stuck, nil).Once()

		// org-bad: provisioner still failing -> failed metric, no set calls, sweep continues.
		d.provisioner.EXPECT().ProvisionTenant(mock.Anything, "org-bad", "Bad", "x@bad.com").Return("", apperr.ErrInternal).Once()
		d.metrics.EXPECT().RecordOrganizerProvisioning(mock.Anything, "failed").Return().Once()

		// org-ok: completes.
		d.provisioner.EXPECT().ProvisionTenant(mock.Anything, "org-ok", "Ok", "y@ok.com").Return("z-ok", nil).Once()
		d.metrics.EXPECT().RecordOrganizerProvisioning(mock.Anything, "success").Return().Once()
		d.orgRepo.EXPECT().SetZitadelOrgID(mock.Anything, "org-ok", "z-ok").Return(nil).Once()
		d.orgRepo.EXPECT().SetStatus(mock.Anything, "org-ok", entity.OrganizerStatusActive).Return(nil).Once()
		d.publisher.EXPECT().PublishEvent(mock.Anything, entity.SubjectOrganizerCreated, entity.OrganizerCreatedData{OrganizerID: "org-ok"}).Return(nil).Once()

		// Per-organizer failures are logged and skipped, not returned.
		assert.NoError(t, d.uc.ReconcileProvisioning(ctx))
	})

	t.Run("returns error when listing stuck organizers fails", func(t *testing.T) {
		t.Parallel()
		d := newOrganizerTestDeps(t)
		d.orgRepo.EXPECT().ListByStatus(ctx, entity.OrganizerStatusProvisioning).Return(nil, apperr.ErrInternal).Once()
		assert.ErrorIs(t, d.uc.ReconcileProvisioning(ctx), apperr.ErrInternal)
	})
}
