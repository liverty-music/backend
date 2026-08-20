package zitadel

import (
	"context"
	"testing"

	mgmtpb "github.com/zitadel/zitadel-go/v3/pkg/client/zitadel/management"
	orgpb "github.com/zitadel/zitadel-go/v3/pkg/client/zitadel/org"
	userpb "github.com/zitadel/zitadel-go/v3/pkg/client/zitadel/user"
	userv2pb "github.com/zitadel/zitadel-go/v3/pkg/client/zitadel/user/v2"
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

	// removeUserCallIDs records the user ids passed to RemoveUser.
	removeUserCallIDs []string
	// removeUserErrs maps user id → error; nil entry means success.
	removeUserErrs map[string]error
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

func (s *stubMgmt) RemoveUser(_ context.Context, in *mgmtpb.RemoveUserRequest, _ ...grpc.CallOption) (*mgmtpb.RemoveUserResponse, error) {
	s.removeUserCallIDs = append(s.removeUserCallIDs, in.GetId())
	if s.removeUserErrs != nil {
		if err, ok := s.removeUserErrs[in.GetId()]; ok {
			return nil, err
		}
	}
	return &mgmtpb.RemoveUserResponse{}, nil
}

// stubUserV2 is a test double for the User v2 UserServiceClient. It embeds the
// interface so only the methods OrganizerProvisioner uses need overriding;
// un-overridden methods panic if called, surfacing unexpected dependencies.
type stubUserV2 struct {
	userv2pb.UserServiceClient

	// addHumanUserResp/addHumanUserErr controls AddHumanUser (v2).
	addHumanUserResp *userv2pb.AddHumanUserResponse
	addHumanUserErr  error

	// createPasskeyLinkErr controls CreatePasskeyRegistrationLink.
	createPasskeyLinkErr error
}

func (s *stubUserV2) AddHumanUser(_ context.Context, _ *userv2pb.AddHumanUserRequest, _ ...grpc.CallOption) (*userv2pb.AddHumanUserResponse, error) {
	return s.addHumanUserResp, s.addHumanUserErr
}

func (s *stubUserV2) CreatePasskeyRegistrationLink(_ context.Context, _ *userv2pb.CreatePasskeyRegistrationLinkRequest, _ ...grpc.CallOption) (*userv2pb.CreatePasskeyRegistrationLinkResponse, error) {
	return &userv2pb.CreatePasskeyRegistrationLinkResponse{}, s.createPasskeyLinkErr
}

// newTestProvisioner builds an OrganizerProvisioner wired to the given stubs
// instead of a live Zitadel connection.
func newTestProvisioner(t *testing.T, stub *stubMgmt, userV2 *stubUserV2) *OrganizerProvisioner {
	t.Helper()
	logger, err := logging.New()
	require.NoError(t, err)
	if userV2 == nil {
		userV2 = &stubUserV2{}
	}
	return &OrganizerProvisioner{
		mgmt:                      stub,
		userV2:                    userV2,
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
		userV2    *stubUserV2
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
				addOrgResp: &mgmtpb.AddOrgResponse{Id: "zitadel-org-xyz"},
			},
			userV2: &stubUserV2{
				addHumanUserResp: &userv2pb.AddHumanUserResponse{UserId: "operator-user-1"},
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
				// AddHumanUser AlreadyExists (v2) triggers lookup by email via ListUsers (v1).
				listUsersResp: &mgmtpb.ListUsersResponse{
					Result: []*userpb.User{{Id: "existing-operator-id"}},
				},
				addUserGrantErr: grpcstatus.Error(grpccodes.AlreadyExists, "grant exists"),
			},
			userV2: &stubUserV2{
				addHumanUserErr: grpcstatus.Error(grpccodes.AlreadyExists, "user exists"),
			},
			wantOrgID: "zitadel-org-existing",
		},
		{
			name: "FATAL: CreatePasskeyRegistrationLink failure returns error",
			args: args{
				organizerID:   "org-abc12345",
				name:          "Fail Label",
				operatorEmail: "op@fail.com",
			},
			stub: &stubMgmt{
				addOrgResp: &mgmtpb.AddOrgResponse{Id: "zitadel-org-xyz"},
			},
			userV2: &stubUserV2{
				addHumanUserResp:     &userv2pb.AddHumanUserResponse{UserId: "op-1"},
				createPasskeyLinkErr: grpcstatus.Error(grpccodes.Internal, "email delivery failed"),
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
				addOrgResp: &mgmtpb.AddOrgResponse{Id: "zitadel-org-xyz"},
			},
			userV2: &stubUserV2{
				addHumanUserErr: grpcstatus.Error(grpccodes.Internal, "create user failed"),
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			p := newTestProvisioner(t, tt.stub, tt.userV2)

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
		name            string
		zitadelOrgID    string
		stub            *stubMgmt
		wantErr         bool
		wantCallCount   int
		wantRemoveCount int
	}{
		{
			name:         "initial-state operator is removed, active operator is deactivated",
			zitadelOrgID: "zitadel-org-mixed",
			stub: &stubMgmt{
				listUsersResp: &mgmtpb.ListUsersResponse{
					Result: []*userpb.User{
						{Id: "user-initial", State: userpb.UserState_USER_STATE_INITIAL},
						{Id: "user-active", State: userpb.UserState_USER_STATE_ACTIVE},
					},
				},
			},
			wantCallCount:   1, // only the active user hits DeactivateUser
			wantRemoveCount: 1, // the initial user is deleted instead
		},
		{
			name:         "RemoveUser NotFound on an initial operator is idempotent",
			zitadelOrgID: "zitadel-org-1",
			stub: &stubMgmt{
				listUsersResp: &mgmtpb.ListUsersResponse{
					Result: []*userpb.User{
						{Id: "user-gone", State: userpb.UserState_USER_STATE_INITIAL},
					},
				},
				removeUserErrs: map[string]error{
					"user-gone": grpcstatus.Error(grpccodes.NotFound, "user not found"),
				},
			},
			wantErr:         false,
			wantRemoveCount: 1,
		},
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
			p := newTestProvisioner(t, tt.stub, nil)

			err := p.DeactivateOperators(ctx, tt.zitadelOrgID)

			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
			assert.Len(t, tt.stub.deactivateUserCallIDs, tt.wantCallCount)
			assert.Len(t, tt.stub.removeUserCallIDs, tt.wantRemoveCount)
		})
	}
}
