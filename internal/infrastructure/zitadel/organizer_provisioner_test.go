package zitadel

import (
	"context"
	"testing"

	mgmtpb "github.com/zitadel/zitadel-go/v3/pkg/client/zitadel/management"
	orgpb "github.com/zitadel/zitadel-go/v3/pkg/client/zitadel/org"
	userpb "github.com/zitadel/zitadel-go/v3/pkg/client/zitadel/user"
	"google.golang.org/grpc"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubMgmt is a test double for ManagementServiceClient. It embeds the
// interface so that only the methods exercised by OrganizerProvisioner need to
// be overridden. All un-overridden methods panic if called, which surfaces any
// unexpected dependency during the test.
type stubMgmt struct {
	mgmtpb.ManagementServiceClient

	// addOrgResp/addOrgErr controls AddOrg.
	addOrgResp *mgmtpb.AddOrgResponse
	addOrgErr  error

	// getOrgByDomainResp/getOrgByDomainErr controls GetOrgByDomainGlobal.
	getOrgByDomainResp *mgmtpb.GetOrgByDomainGlobalResponse
	getOrgByDomainErr  error

	// addCustomLoginPolicyErr controls AddCustomLoginPolicy.
	addCustomLoginPolicyErr error

	// addProjectGrantReqs records all AddProjectGrant calls for inspection.
	addProjectGrantReqs []*mgmtpb.AddProjectGrantRequest
	addProjectGrantErr  error

	// addHumanUserResp/addHumanUserErr controls AddHumanUser.
	addHumanUserResp *mgmtpb.AddHumanUserResponse
	addHumanUserErr  error

	// sendPasswordlessRegistrationErr controls SendPasswordlessRegistration.
	sendPasswordlessRegistrationErr error

	// addUserGrantReqs records all AddUserGrant calls for inspection.
	addUserGrantReqs []*mgmtpb.AddUserGrantRequest
	addUserGrantErr  error

	// listUsersResp/listUsersErr controls ListUsers.
	listUsersResp *mgmtpb.ListUsersResponse
	listUsersErr  error

	// deactivateUserCallIDs records the user ids passed to DeactivateUser.
	deactivateUserCallIDs []string
	// deactivateUserErrs maps user id → error; nil entry means success.
	deactivateUserErrs map[string]error
}

func (s *stubMgmt) AddOrg(_ context.Context, in *mgmtpb.AddOrgRequest, _ ...grpc.CallOption) (*mgmtpb.AddOrgResponse, error) {
	return s.addOrgResp, s.addOrgErr
}

func (s *stubMgmt) GetOrgByDomainGlobal(_ context.Context, _ *mgmtpb.GetOrgByDomainGlobalRequest, _ ...grpc.CallOption) (*mgmtpb.GetOrgByDomainGlobalResponse, error) {
	return s.getOrgByDomainResp, s.getOrgByDomainErr
}

func (s *stubMgmt) AddCustomLoginPolicy(_ context.Context, _ *mgmtpb.AddCustomLoginPolicyRequest, _ ...grpc.CallOption) (*mgmtpb.AddCustomLoginPolicyResponse, error) {
	return &mgmtpb.AddCustomLoginPolicyResponse{}, s.addCustomLoginPolicyErr
}

func (s *stubMgmt) AddProjectGrant(_ context.Context, in *mgmtpb.AddProjectGrantRequest, _ ...grpc.CallOption) (*mgmtpb.AddProjectGrantResponse, error) {
	s.addProjectGrantReqs = append(s.addProjectGrantReqs, in)
	return &mgmtpb.AddProjectGrantResponse{}, s.addProjectGrantErr
}

func (s *stubMgmt) AddHumanUser(_ context.Context, _ *mgmtpb.AddHumanUserRequest, _ ...grpc.CallOption) (*mgmtpb.AddHumanUserResponse, error) {
	return s.addHumanUserResp, s.addHumanUserErr
}

func (s *stubMgmt) SendPasswordlessRegistration(_ context.Context, _ *mgmtpb.SendPasswordlessRegistrationRequest, _ ...grpc.CallOption) (*mgmtpb.SendPasswordlessRegistrationResponse, error) {
	return &mgmtpb.SendPasswordlessRegistrationResponse{}, s.sendPasswordlessRegistrationErr
}

func (s *stubMgmt) AddUserGrant(_ context.Context, in *mgmtpb.AddUserGrantRequest, _ ...grpc.CallOption) (*mgmtpb.AddUserGrantResponse, error) {
	s.addUserGrantReqs = append(s.addUserGrantReqs, in)
	return &mgmtpb.AddUserGrantResponse{}, s.addUserGrantErr
}

func (s *stubMgmt) ListUsers(_ context.Context, _ *mgmtpb.ListUsersRequest, _ ...grpc.CallOption) (*mgmtpb.ListUsersResponse, error) {
	return s.listUsersResp, s.listUsersErr
}

func (s *stubMgmt) DeactivateUser(_ context.Context, in *mgmtpb.DeactivateUserRequest, _ ...grpc.CallOption) (*mgmtpb.DeactivateUserResponse, error) {
	s.deactivateUserCallIDs = append(s.deactivateUserCallIDs, in.GetId())
	if s.deactivateUserErrs != nil {
		if err, ok := s.deactivateUserErrs[in.GetId()]; ok {
			return nil, err
		}
	}
	return &mgmtpb.DeactivateUserResponse{}, nil
}

// newTestProvisioner builds an OrganizerProvisioner wired to the given stub
// instead of a live Zitadel connection.
func newTestProvisioner(t *testing.T, stub *stubMgmt) *OrganizerProvisioner {
	t.Helper()
	logger, err := logging.New()
	require.NoError(t, err)
	return &OrganizerProvisioner{
		mgmt:                      stub,
		organizerConsoleProjectID: "proj-1",
		logger:                    logger,
	}
}

func TestOrganizerProvisioner_ProvisionTenant(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	type args struct {
		organizerID   string
		name          string
		operatorEmail string
	}

	tests := []struct {
		name      string
		args      args
		stub      *stubMgmt
		wantOrgID string
		wantErr   bool
		check     func(t *testing.T, stub *stubMgmt)
	}{
		{
			name: "happy path: all steps succeed, returns org id from AddOrg",
			args: args{
				organizerID:   "org-abc12345",
				name:          "Acme Label",
				operatorEmail: "op@acme.com",
			},
			stub: &stubMgmt{
				addOrgResp:       &mgmtpb.AddOrgResponse{Id: "zitadel-org-xyz"},
				addHumanUserResp: &mgmtpb.AddHumanUserResponse{UserId: "operator-user-1"},
			},
			wantOrgID: "zitadel-org-xyz",
			check: func(t *testing.T, stub *stubMgmt) {
				t.Helper()
				// AddProjectGrant must receive the console project id, the
				// returned org id, and the owner role.
				require.Len(t, stub.addProjectGrantReqs, 1)
				req := stub.addProjectGrantReqs[0]
				assert.Equal(t, "proj-1", req.GetProjectId())
				assert.Equal(t, "zitadel-org-xyz", req.GetGrantedOrgId())
				assert.Equal(t, []string{"owner"}, req.GetRoleKeys())

				// AddUserGrant must reference the operator id and the console project.
				require.Len(t, stub.addUserGrantReqs, 1)
				grant := stub.addUserGrantReqs[0]
				assert.Equal(t, "operator-user-1", grant.GetUserId())
				assert.Equal(t, "proj-1", grant.GetProjectId())
				assert.Equal(t, []string{"owner"}, grant.GetRoleKeys())
			},
		},
		{
			name: "idempotent: AddOrg AlreadyExists falls back to GetOrgByDomainGlobal, duplicate step errors are swallowed",
			args: args{
				organizerID:   "org-abc12345",
				name:          "Acme Label",
				operatorEmail: "op@acme.com",
			},
			stub: &stubMgmt{
				addOrgErr: grpcstatus.Error(grpccodes.AlreadyExists, "org already exists"),
				getOrgByDomainResp: &mgmtpb.GetOrgByDomainGlobalResponse{
					Org: &orgpb.Org{Id: "zitadel-org-existing"},
				},
				// Simulate all create-steps returning AlreadyExists on a retry.
				addCustomLoginPolicyErr: grpcstatus.Error(grpccodes.AlreadyExists, "policy exists"),
				addProjectGrantErr:      grpcstatus.Error(grpccodes.AlreadyExists, "grant exists"),
				// AddHumanUser AlreadyExists triggers lookup by email via ListUsers.
				addHumanUserErr: grpcstatus.Error(grpccodes.AlreadyExists, "user exists"),
				listUsersResp: &mgmtpb.ListUsersResponse{
					Result: []*userpb.User{{Id: "existing-operator-id"}},
				},
				addUserGrantErr: grpcstatus.Error(grpccodes.AlreadyExists, "grant exists"),
			},
			wantOrgID: "zitadel-org-existing",
		},
		{
			name: "FATAL: SendPasswordlessRegistration failure returns error",
			args: args{
				organizerID:   "org-abc12345",
				name:          "Fail Label",
				operatorEmail: "op@fail.com",
			},
			stub: &stubMgmt{
				addOrgResp:                      &mgmtpb.AddOrgResponse{Id: "zitadel-org-xyz"},
				addHumanUserResp:                &mgmtpb.AddHumanUserResponse{UserId: "op-1"},
				sendPasswordlessRegistrationErr: grpcstatus.Error(grpccodes.Internal, "email delivery failed"),
			},
			wantErr: true,
		},
		{
			name: "AddOrg non-AlreadyExists error returns error",
			args: args{
				organizerID:   "org-abc12345",
				name:          "Fail Label",
				operatorEmail: "op@fail.com",
			},
			stub: &stubMgmt{
				addOrgErr: grpcstatus.Error(grpccodes.Internal, "db error"),
			},
			wantErr: true,
		},
		{
			name: "AddHumanUser non-AlreadyExists error returns error",
			args: args{
				organizerID:   "org-abc12345",
				name:          "Fail Label",
				operatorEmail: "op@fail.com",
			},
			stub: &stubMgmt{
				addOrgResp:      &mgmtpb.AddOrgResponse{Id: "zitadel-org-xyz"},
				addHumanUserErr: grpcstatus.Error(grpccodes.Internal, "create user failed"),
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			p := newTestProvisioner(t, tt.stub)

			got, err := p.ProvisionTenant(ctx, tt.args.organizerID, tt.args.name, tt.args.operatorEmail)

			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantOrgID, got)
			if tt.check != nil {
				tt.check(t, tt.stub)
			}
		})
	}
}

func TestOrganizerProvisioner_DeactivateOperators(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	tests := []struct {
		name          string
		zitadelOrgID  string
		stub          *stubMgmt
		wantErr       bool
		wantCallCount int
	}{
		{
			name:         "deactivate all users returned by ListUsers",
			zitadelOrgID: "zitadel-org-1",
			stub: &stubMgmt{
				listUsersResp: &mgmtpb.ListUsersResponse{
					Result: []*userpb.User{
						{Id: "user-a"},
						{Id: "user-b"},
					},
				},
			},
			wantCallCount: 2,
		},
		{
			name:         "FailedPrecondition on DeactivateUser is treated as already-inactive and is swallowed",
			zitadelOrgID: "zitadel-org-1",
			stub: &stubMgmt{
				listUsersResp: &mgmtpb.ListUsersResponse{
					Result: []*userpb.User{
						{Id: "user-already-inactive"},
						{Id: "user-active"},
					},
				},
				deactivateUserErrs: map[string]error{
					"user-already-inactive": grpcstatus.Error(grpccodes.FailedPrecondition, "user already deactivated"),
				},
			},
			wantErr:       false,
			wantCallCount: 2,
		},
		{
			name:         "empty ListUsers result is a no-op",
			zitadelOrgID: "zitadel-org-empty",
			stub: &stubMgmt{
				listUsersResp: &mgmtpb.ListUsersResponse{},
			},
			wantCallCount: 0,
		},
		{
			name:         "ListUsers error returns error",
			zitadelOrgID: "zitadel-org-1",
			stub: &stubMgmt{
				listUsersErr: grpcstatus.Error(grpccodes.Internal, "list failed"),
			},
			wantErr: true,
		},
		{
			name:         "non-FailedPrecondition DeactivateUser error returns error",
			zitadelOrgID: "zitadel-org-1",
			stub: &stubMgmt{
				listUsersResp: &mgmtpb.ListUsersResponse{
					Result: []*userpb.User{
						{Id: "user-a"},
					},
				},
				deactivateUserErrs: map[string]error{
					"user-a": grpcstatus.Error(grpccodes.Internal, "deactivate failed"),
				},
			},
			wantErr:       true,
			wantCallCount: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			p := newTestProvisioner(t, tt.stub)

			err := p.DeactivateOperators(ctx, tt.zitadelOrgID)

			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
			assert.Len(t, tt.stub.deactivateUserCallIDs, tt.wantCallCount)
		})
	}
}
