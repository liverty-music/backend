package zitadel

import (
	"context"
	"errors"
	"testing"

	mgmtpb "github.com/zitadel/zitadel-go/v3/pkg/client/zitadel/management"
	objectv2pb "github.com/zitadel/zitadel-go/v3/pkg/client/zitadel/object/v2"
	orgv2pb "github.com/zitadel/zitadel-go/v3/pkg/client/zitadel/org/v2"
	userpb "github.com/zitadel/zitadel-go/v3/pkg/client/zitadel/user"
	userv2pb "github.com/zitadel/zitadel-go/v3/pkg/client/zitadel/user/v2"
	"google.golang.org/grpc"
	grpccodes "google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	grpcstatus "google.golang.org/grpc/status"

	"github.com/pannpers/go-apperr/apperr"
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

	// addOrgCalls counts AddOrg calls.
	addOrgCalls int

	// addCustomLoginPolicyReq captures the last AddCustomLoginPolicy request;
	// addCustomLoginPolicyErr controls its error.
	addCustomLoginPolicyReq *mgmtpb.AddCustomLoginPolicyRequest
	addCustomLoginPolicyErr error

	// updateCustomLoginPolicyReq captures the last UpdateCustomLoginPolicy
	// request; updateCustomLoginPolicyErr controls its error.
	updateCustomLoginPolicyReq *mgmtpb.UpdateCustomLoginPolicyRequest
	updateCustomLoginPolicyErr error

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

	// removeOrgOrgIDs records the x-zitadel-orgid header of each RemoveOrg
	// call; removeOrgErr controls its error.
	removeOrgOrgIDs []string
	removeOrgErr    error
}

func (s *stubMgmt) RemoveOrg(ctx context.Context, _ *mgmtpb.RemoveOrgRequest, _ ...grpc.CallOption) (*mgmtpb.RemoveOrgResponse, error) {
	md, _ := metadata.FromOutgoingContext(ctx)
	s.removeOrgOrgIDs = append(s.removeOrgOrgIDs, md.Get("x-zitadel-orgid")...)
	if s.removeOrgErr != nil {
		return nil, s.removeOrgErr
	}
	return &mgmtpb.RemoveOrgResponse{}, nil
}

func (s *stubMgmt) AddOrg(_ context.Context, in *mgmtpb.AddOrgRequest, _ ...grpc.CallOption) (*mgmtpb.AddOrgResponse, error) {
	s.addOrgCalls++
	return s.addOrgResp, s.addOrgErr
}

func (s *stubMgmt) AddCustomLoginPolicy(_ context.Context, in *mgmtpb.AddCustomLoginPolicyRequest, _ ...grpc.CallOption) (*mgmtpb.AddCustomLoginPolicyResponse, error) {
	s.addCustomLoginPolicyReq = in
	return &mgmtpb.AddCustomLoginPolicyResponse{}, s.addCustomLoginPolicyErr
}

func (s *stubMgmt) UpdateCustomLoginPolicy(_ context.Context, in *mgmtpb.UpdateCustomLoginPolicyRequest, _ ...grpc.CallOption) (*mgmtpb.UpdateCustomLoginPolicyResponse, error) {
	s.updateCustomLoginPolicyReq = in
	return &mgmtpb.UpdateCustomLoginPolicyResponse{}, s.updateCustomLoginPolicyErr
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

	// addHumanUserCalls counts AddHumanUser calls.
	addHumanUserCalls int

	// listUsersResp/listUsersErr controls ListUsers (v2); listUsersReqs
	// records every request.
	listUsersResp *userv2pb.ListUsersResponse
	listUsersErr  error
	listUsersReqs []*userv2pb.ListUsersRequest

	// createInviteErr controls CreateInviteCode.
	createInviteErr error
	// createInviteReq captures the last CreateInviteCode request for assertions.
	createInviteReq *userv2pb.CreateInviteCodeRequest
}

func (s *stubUserV2) AddHumanUser(_ context.Context, _ *userv2pb.AddHumanUserRequest, _ ...grpc.CallOption) (*userv2pb.AddHumanUserResponse, error) {
	s.addHumanUserCalls++
	return s.addHumanUserResp, s.addHumanUserErr
}

func (s *stubUserV2) ListUsers(_ context.Context, req *userv2pb.ListUsersRequest, _ ...grpc.CallOption) (*userv2pb.ListUsersResponse, error) {
	s.listUsersReqs = append(s.listUsersReqs, req)
	if s.listUsersErr != nil {
		return nil, s.listUsersErr
	}
	if s.listUsersResp == nil {
		return &userv2pb.ListUsersResponse{}, nil
	}
	return s.listUsersResp, nil
}

// stubOrgV2 is a test double for the Organization v2 OrganizationServiceClient.
type stubOrgV2 struct {
	orgv2pb.OrganizationServiceClient

	// listOrgsResp/listOrgsErr controls ListOrganizations; listOrgsReqs
	// records every request.
	listOrgsResp *orgv2pb.ListOrganizationsResponse
	listOrgsErr  error
	listOrgsReqs []*orgv2pb.ListOrganizationsRequest
}

func (s *stubOrgV2) ListOrganizations(_ context.Context, req *orgv2pb.ListOrganizationsRequest, _ ...grpc.CallOption) (*orgv2pb.ListOrganizationsResponse, error) {
	s.listOrgsReqs = append(s.listOrgsReqs, req)
	if s.listOrgsErr != nil {
		return nil, s.listOrgsErr
	}
	if s.listOrgsResp == nil {
		return &orgv2pb.ListOrganizationsResponse{}, nil
	}
	return s.listOrgsResp, nil
}

func (s *stubUserV2) CreateInviteCode(_ context.Context, req *userv2pb.CreateInviteCodeRequest, _ ...grpc.CallOption) (*userv2pb.CreateInviteCodeResponse, error) {
	s.createInviteReq = req
	return &userv2pb.CreateInviteCodeResponse{}, s.createInviteErr
}

// newTestProvisioner builds an OrganizerProvisioner wired to the given stubs
// instead of a live Zitadel connection.
func newTestProvisioner(t *testing.T, stub *stubMgmt, userV2 *stubUserV2) *OrganizerProvisioner {
	t.Helper()
	return newTestProvisionerWithOrgs(t, stub, userV2, nil)
}

// newTestProvisionerWithOrgs is newTestProvisioner with an Organization v2
// stub for the org-name lookups.
func newTestProvisionerWithOrgs(t *testing.T, stub *stubMgmt, userV2 *stubUserV2, orgV2 *stubOrgV2) *OrganizerProvisioner {
	t.Helper()
	logger, err := logging.New()
	require.NoError(t, err)
	if stub == nil {
		stub = &stubMgmt{}
	}
	if userV2 == nil {
		userV2 = &stubUserV2{}
	}
	if orgV2 == nil {
		orgV2 = &stubOrgV2{}
	}
	return &OrganizerProvisioner{
		mgmt:                      stub,
		userV2:                    userV2,
		orgV2:                     orgV2,
		organizerConsoleProjectID: "proj-1",
		consoleBaseURL:            "https://organizer.test.local",
		inviteURLTemplate:         inviteVerifyURLTemplate("https://auth.test.local"),
		logger:                    logger,
	}
}

// alreadyExists, internalErr and invalidArg build Zitadel-style gRPC errors.
func alreadyExists(msg string) error { return grpcstatus.Error(grpccodes.AlreadyExists, msg) }
func internalErr(msg string) error   { return grpcstatus.Error(grpccodes.Internal, msg) }
func invalidArg(msg string) error    { return grpcstatus.Error(grpccodes.InvalidArgument, msg) }

// tenantUsers returns a v2 ListUsers response with one user per id.
func tenantUsers(ids ...string) *userv2pb.ListUsersResponse {
	resp := &userv2pb.ListUsersResponse{}
	for _, id := range ids {
		resp.Result = append(resp.Result, &userv2pb.User{UserId: id})
	}
	return resp
}

// orgIDFilter returns the organization id a v2 ListUsers request is limited
// to, or "" when it searches the whole instance.
func orgIDFilter(req *userv2pb.ListUsersRequest) string {
	for _, q := range req.GetQueries() {
		if id := q.GetOrganizationIdQuery().GetOrganizationId(); id != "" {
			return id
		}
	}
	return ""
}

func TestOrganizerProvisioner_ProvisionTenant(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	const (
		organizerID  = "01a11dec-968d-762a-a64d-17f4ce185b92"
		zitadelOrgID = "zitadel-org-xyz"
		email        = "op@acme.com"
	)

	tests := []struct {
		name    string
		stub    *stubMgmt
		userV2  *stubUserV2
		wantErr error
		check   func(t *testing.T, stub *stubMgmt, userV2 *stubUserV2)
	}{
		{
			// @spec components/entity/organizer/provision-tenant "Organizer is provisioned"
			name: "happy path: completes every step on the given tenant org without creating an org",
			stub: &stubMgmt{},
			userV2: &stubUserV2{
				addHumanUserResp: &userv2pb.AddHumanUserResponse{UserId: "operator-user-1"},
			},
			check: func(t *testing.T, stub *stubMgmt, userV2 *stubUserV2) {
				t.Helper()
				assert.Zero(t, stub.addOrgCalls, "ProvisionTenant must not create an org")
				// The login policy enables local auth (passkey-primary) and sets
				// the console as the post-invite default_redirect_uri.
				require.NotNil(t, stub.addCustomLoginPolicyReq)
				assert.True(t, stub.addCustomLoginPolicyReq.GetAllowUsernamePassword(),
					"local authentication must be enabled for the passkey login form")
				assert.Equal(t, "https://organizer.test.local/?org_id=zitadel-org-xyz",
					stub.addCustomLoginPolicyReq.GetDefaultRedirectUri(),
					"invite acceptance must return the operator to the console")

				require.Len(t, stub.addProjectGrantReqs, 1)
				req := stub.addProjectGrantReqs[0]
				assert.Equal(t, "proj-1", req.GetProjectId())
				assert.Equal(t, zitadelOrgID, req.GetGrantedOrgId())
				assert.Equal(t, []string{"owner"}, req.GetRoleKeys())

				require.Len(t, stub.addUserGrantReqs, 1)
				grant := stub.addUserGrantReqs[0]
				assert.Equal(t, "operator-user-1", grant.GetUserId())
				assert.Equal(t, "proj-1", grant.GetProjectId())
				assert.Equal(t, []string{"owner"}, grant.GetRoleKeys())
				assert.Empty(t, userV2.listUsersReqs, "no user search on the happy path")
				require.NotNil(t, userV2.createInviteReq, "the operator is invited to register a passkey")
				assert.Equal(t, "operator-user-1", userV2.createInviteReq.GetUserId())
			},
		},
		{
			// @spec components/entity/organizer/provision-tenant "Repeated call"
			name: "true retry: the operator already exists in the tenant org and is reused; duplicate steps are swallowed",
			stub: &stubMgmt{
				addCustomLoginPolicyErr: alreadyExists("policy exists"),
				addProjectGrantErr:      alreadyExists("grant exists"),
				addUserGrantErr:         alreadyExists("grant exists"),
			},
			userV2: &stubUserV2{
				addHumanUserErr: alreadyExists("user exists"),
				listUsersResp:   tenantUsers("existing-operator-id"),
			},
			check: func(t *testing.T, stub *stubMgmt, userV2 *stubUserV2) {
				t.Helper()
				// A pre-existing login policy must be converged in place.
				require.NotNil(t, stub.updateCustomLoginPolicyReq, "existing policy must be updated in place")
				assert.True(t, stub.updateCustomLoginPolicyReq.GetAllowUsernamePassword())
				assert.Equal(t, "https://organizer.test.local/?org_id=zitadel-org-xyz",
					stub.updateCustomLoginPolicyReq.GetDefaultRedirectUri())
				// The existing user is looked up in the tenant org only.
				require.Len(t, userV2.listUsersReqs, 1)
				assert.Equal(t, zitadelOrgID, orgIDFilter(userV2.listUsersReqs[0]))
				require.Len(t, stub.addUserGrantReqs, 1)
				assert.Equal(t, "existing-operator-id", stub.addUserGrantReqs[0].GetUserId())
			},
		},
		{
			// @spec components/entity/organizer/provision-tenant "Operator email used in another tenant"
			name: "email collision: the email belongs to a user in another org, fails with AlreadyExists and grants nothing",
			stub: &stubMgmt{},
			userV2: &stubUserV2{
				// AddHumanUser reports the instance-wide user name collision, but
				// no user with the email exists in the tenant org.
				addHumanUserErr: alreadyExists("user exists"),
				listUsersResp:   tenantUsers(),
			},
			wantErr: apperr.ErrAlreadyExists,
			check: func(t *testing.T, stub *stubMgmt, userV2 *stubUserV2) {
				t.Helper()
				require.Len(t, userV2.listUsersReqs, 1)
				assert.Equal(t, zitadelOrgID, orgIDFilter(userV2.listUsersReqs[0]))
				assert.Empty(t, stub.addUserGrantReqs, "no owner grant for a foreign user")
				assert.Nil(t, userV2.createInviteReq, "no invite to a foreign user")
			},
		},
		{
			// @spec components/entity/organizer/provision-tenant "Retry after a partial failure"
			name: "partial retry: an earlier attempt stopped before the owner grant; the existing operator is reused and granted owner",
			stub: &stubMgmt{
				// The policy and project grant were created by the failed attempt;
				// the owner grant was not.
				addCustomLoginPolicyErr: alreadyExists("policy exists"),
				addProjectGrantErr:      alreadyExists("grant exists"),
			},
			userV2: &stubUserV2{
				addHumanUserErr: alreadyExists("user exists"),
				listUsersResp:   tenantUsers("existing-operator-id"),
			},
			check: func(t *testing.T, stub *stubMgmt, userV2 *stubUserV2) {
				t.Helper()
				assert.Equal(t, 1, userV2.addHumanUserCalls, "no second operator is created")
				require.Len(t, userV2.listUsersReqs, 1)
				assert.Equal(t, zitadelOrgID, orgIDFilter(userV2.listUsersReqs[0]))
				require.Len(t, stub.addUserGrantReqs, 1)
				assert.Equal(t, "existing-operator-id", stub.addUserGrantReqs[0].GetUserId())
				assert.Equal(t, []string{"owner"}, stub.addUserGrantReqs[0].GetRoleKeys())
			},
		},
		{
			name: "rejected email: AddHumanUser InvalidArgument fails with InvalidArgument",
			stub: &stubMgmt{},
			userV2: &stubUserV2{
				addHumanUserErr: invalidArg("email invalid"),
			},
			wantErr: apperr.ErrInvalidArgument,
		},
		{
			name:    "user lookup failure after AlreadyExists is Internal",
			stub:    &stubMgmt{},
			userV2:  &stubUserV2{addHumanUserErr: alreadyExists("user exists"), listUsersErr: internalErr("search failed")},
			wantErr: apperr.ErrInternal,
		},
		{
			name: "FATAL: UpdateCustomLoginPolicy failure on an existing policy is Internal",
			stub: &stubMgmt{
				addCustomLoginPolicyErr:    alreadyExists("policy exists"),
				updateCustomLoginPolicyErr: internalErr("update failed"),
			},
			wantErr: apperr.ErrInternal,
		},
		{
			name:    "FATAL: CreateInviteCode failure is Internal",
			stub:    &stubMgmt{},
			userV2:  &stubUserV2{addHumanUserResp: &userv2pb.AddHumanUserResponse{UserId: "op-1"}, createInviteErr: internalErr("email delivery failed")},
			wantErr: apperr.ErrInternal,
		},
		{
			name: "idempotent: CreateInviteCode FailedPrecondition (operator already onboarded) is benign, saga proceeds to grant",
			stub: &stubMgmt{},
			userV2: &stubUserV2{
				addHumanUserResp: &userv2pb.AddHumanUserResponse{UserId: "op-onboarded"},
				createInviteErr:  grpcstatus.Error(grpccodes.FailedPrecondition, "user already initialized"),
			},
			check: func(t *testing.T, stub *stubMgmt, _ *stubUserV2) {
				t.Helper()
				require.Len(t, stub.addUserGrantReqs, 1)
				assert.Equal(t, "op-onboarded", stub.addUserGrantReqs[0].GetUserId())
			},
		},
		{
			name:    "AddHumanUser transient error is Internal",
			stub:    &stubMgmt{},
			userV2:  &stubUserV2{addHumanUserErr: internalErr("create user failed")},
			wantErr: apperr.ErrInternal,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if tt.userV2 == nil {
				tt.userV2 = &stubUserV2{addHumanUserResp: &userv2pb.AddHumanUserResponse{UserId: "op-1"}}
			}
			p := newTestProvisioner(t, tt.stub, tt.userV2)

			err := p.ProvisionTenant(ctx, organizerID, zitadelOrgID, email)

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
			} else {
				require.NoError(t, err)
			}
			if tt.check != nil {
				tt.check(t, tt.stub, tt.userV2)
			}
		})
	}
}

func TestOrganizerProvisioner_ProvisionTenant_collisionMessageReachesAdmin(t *testing.T) {
	t.Parallel()

	p := newTestProvisioner(t, &stubMgmt{}, &stubUserV2{addHumanUserErr: alreadyExists("user exists")})

	err := p.ProvisionTenant(context.Background(), "organizer-1", "zitadel-org-1", "admin@example.com")

	// The Connect error interceptor exposes the innermost AppErr message of a
	// client error, so that is what the admin sees.
	var appErr *apperr.AppErr
	require.ErrorAs(t, err, &appErr)
	assert.Contains(t, appErr.Msg, "operator email is already used by another account")
}

func TestOrganizerProvisioner_EnsureTenantOrg(t *testing.T) {
	t.Parallel()

	const organizerID = "01a11dec-968d-762a-a64d-17f4ce185b92"
	orgName := "org-" + organizerID

	tests := []struct {
		name    string
		stub    *stubMgmt
		orgV2   *stubOrgV2
		want    string
		wantErr error
	}{
		{
			// @spec components/entity/organizer/ensure-tenant-org "First attempt"
			name: "creates the org named after the organizer",
			stub: &stubMgmt{addOrgResp: &mgmtpb.AddOrgResponse{Id: "zitadel-org-new"}},
			want: "zitadel-org-new",
		},
		{
			// @spec components/entity/organizer/ensure-tenant-org "Earlier attempt created the tenant"
			name: "resolves an org created by an earlier attempt by its exact name",
			stub: &stubMgmt{addOrgErr: alreadyExists("org exists")},
			orgV2: &stubOrgV2{listOrgsResp: &orgv2pb.ListOrganizationsResponse{Result: []*orgv2pb.Organization{
				{Id: "394277002850336802", Name: orgName, State: orgv2pb.OrganizationState_ORGANIZATION_STATE_ACTIVE},
			}}},
			want: "394277002850336802",
		},
		{
			name:    "an existing org that cannot be found by name is Internal, not NotFound",
			stub:    &stubMgmt{addOrgErr: alreadyExists("org exists")},
			orgV2:   &stubOrgV2{listOrgsResp: &orgv2pb.ListOrganizationsResponse{}},
			wantErr: apperr.ErrInternal,
		},
		{
			name:    "AddOrg failure is Internal",
			stub:    &stubMgmt{addOrgErr: internalErr("db error")},
			wantErr: apperr.ErrInternal,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			p := newTestProvisionerWithOrgs(t, tt.stub, nil, tt.orgV2)

			got, err := p.EnsureTenantOrg(context.Background(), organizerID)

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				assert.False(t, errors.Is(err, apperr.ErrNotFound))
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestOrganizerProvisioner_FindTenantOrg(t *testing.T) {
	t.Parallel()

	const organizerID = "01a11dec-968d-762a-a64d-17f4ce185b92"
	orgName := "org-" + organizerID

	tests := []struct {
		name    string
		orgV2   *stubOrgV2
		want    string
		wantErr error
	}{
		{
			// @spec components/entity/organizer/find-tenant-org "Tenant left by a failed attempt"
			name: "returns the org whose name matches exactly",
			orgV2: &stubOrgV2{listOrgsResp: &orgv2pb.ListOrganizationsResponse{Result: []*orgv2pb.Organization{
				{Id: "394277002850336802", Name: orgName, State: orgv2pb.OrganizationState_ORGANIZATION_STATE_ACTIVE},
			}}},
			want: "394277002850336802",
		},
		{
			name: "ignores a removed org and a different name",
			orgV2: &stubOrgV2{listOrgsResp: &orgv2pb.ListOrganizationsResponse{Result: []*orgv2pb.Organization{
				{Id: "removed", Name: orgName, State: orgv2pb.OrganizationState_ORGANIZATION_STATE_REMOVED},
				{Id: "other", Name: orgName + "-x", State: orgv2pb.OrganizationState_ORGANIZATION_STATE_ACTIVE},
			}}},
			wantErr: apperr.ErrNotFound,
		},
		{
			// @spec components/entity/organizer/find-tenant-org "No tenant"
			name:    "returns NotFound when no org has the name",
			orgV2:   &stubOrgV2{},
			wantErr: apperr.ErrNotFound,
		},
		{
			name:    "search failure is Internal",
			orgV2:   &stubOrgV2{listOrgsErr: internalErr("search failed")},
			wantErr: apperr.ErrInternal,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			p := newTestProvisionerWithOrgs(t, nil, nil, tt.orgV2)

			got, err := p.FindTenantOrg(context.Background(), organizerID)

			require.Len(t, tt.orgV2.listOrgsReqs, 1)
			nameQuery := tt.orgV2.listOrgsReqs[0].GetQueries()[0].GetNameQuery()
			assert.Equal(t, orgName, nameQuery.GetName(), "the org is searched by its exact deterministic name")
			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestOrganizerProvisioner_CheckOperatorEmailAvailable(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		userV2  *stubUserV2
		wantErr error
	}{
		{
			// @spec components/entity/organizer/check-operator-email-available "Unused email"
			name:   "available when no user in the instance uses the email",
			userV2: &stubUserV2{listUsersResp: tenantUsers()},
		},
		{
			// @spec components/entity/organizer/check-operator-email-available "Email of the platform administrator"
			name:    "AlreadyExists when a user in any org uses the email",
			userV2:  &stubUserV2{listUsersResp: tenantUsers("human-admin")},
			wantErr: apperr.ErrAlreadyExists,
		},
		{
			name:    "search failure is Internal",
			userV2:  &stubUserV2{listUsersErr: internalErr("search failed")},
			wantErr: apperr.ErrInternal,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			p := newTestProvisioner(t, nil, tt.userV2)

			err := p.CheckOperatorEmailAvailable(context.Background(), "admin@example.com")

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
			} else {
				assert.NoError(t, err)
			}
			require.Len(t, tt.userV2.listUsersReqs, 1)
			req := tt.userV2.listUsersReqs[0]
			assert.Empty(t, orgIDFilter(req), "the check spans the whole instance")
			or := req.GetQueries()[0].GetOrQuery().GetQueries()
			require.Len(t, or, 3)
			assert.Equal(t, "admin@example.com", or[0].GetEmailQuery().GetEmailAddress())
			assert.Equal(t, "admin@example.com", or[1].GetUserNameQuery().GetUserName())
			assert.Equal(t, "admin@example.com", or[2].GetLoginNameQuery().GetLoginName())
		})
	}
}

// @spec components/entity/organizer/check-operator-email-available "Different letter case"
func TestOrganizerProvisioner_CheckOperatorEmailAvailable_ignoresLetterCase(t *testing.T) {
	t.Parallel()

	// Zitadel returns the user registered as Operator@Example.com for the
	// case-insensitive search.
	userV2 := &stubUserV2{listUsersResp: tenantUsers("operator-mixed-case")}
	p := newTestProvisioner(t, nil, userV2)

	err := p.CheckOperatorEmailAvailable(context.Background(), "operator@example.com")

	assert.ErrorIs(t, err, apperr.ErrAlreadyExists)
	require.Len(t, userV2.listUsersReqs, 1)
	or := userV2.listUsersReqs[0].GetQueries()[0].GetOrQuery().GetQueries()
	require.Len(t, or, 3)
	ignoreCase := objectv2pb.TextQueryMethod_TEXT_QUERY_METHOD_EQUALS_IGNORE_CASE
	assert.Equal(t, ignoreCase, or[0].GetEmailQuery().GetMethod(), "email is compared without regard to letter case")
	assert.Equal(t, ignoreCase, or[1].GetUserNameQuery().GetMethod(), "user name is compared without regard to letter case")
	assert.Equal(t, ignoreCase, or[2].GetLoginNameQuery().GetMethod(), "login name is compared without regard to letter case")
}

func TestOrganizerProvisioner_ProvisionTenant_sendsStandardVerifyInvite(t *testing.T) {
	t.Parallel()

	stub := &stubMgmt{}
	userV2 := &stubUserV2{addHumanUserResp: &userv2pb.AddHumanUserResponse{UserId: "operator-user-1"}}
	p := newTestProvisioner(t, stub, userV2)

	err := p.ProvisionTenant(context.Background(), "org-abc12345", "zitadel-org-xyz", "op@acme.com")
	require.NoError(t, err)

	// The onboarding email is Zitadel's STANDARD invite: its "Accept invite" link
	// points at the hosted Login v2 /verify page on the AUTH host (not the
	// console) and carries Zitadel's own {{.Code}}/{{.UserID}}/{{.OrgID}}
	// placeholders. The code therefore rides on the IdP surface, never in a
	// console URL.
	require.NotNil(t, userV2.createInviteReq)
	assert.Equal(t, "operator-user-1", userV2.createInviteReq.GetUserId())
	send := userV2.createInviteReq.GetSendCode()
	require.NotNil(t, send, "invite must be SendCode (Zitadel-sent email), not ReturnCode")
	tmpl := send.GetUrlTemplate()
	assert.Equal(t,
		"https://auth.test.local/ui/v2/login/verify?code={{.Code}}&userId={{.UserID}}&organization={{.OrgID}}&invite=true",
		tmpl,
	)
	assert.Contains(t, tmpl, "auth.test.local", "invite link points at the auth (IdP) host, not the console")
	assert.NotContains(t, tmpl, "organizer.test.local", "invite link must not point at the console")
	assert.Contains(t, tmpl, "{{.Code}}", "code rides on the IdP surface via the Zitadel placeholder")
	assert.Equal(t, "Liverty Music Organizer", send.GetApplicationName())
}

func TestInviteVerifyURLTemplate(t *testing.T) {
	t.Parallel()

	assert.Equal(t,
		"https://auth.liverty-music.app/ui/v2/login/verify?code={{.Code}}&userId={{.UserID}}&organization={{.OrgID}}&invite=true",
		inviteVerifyURLTemplate("https://auth.liverty-music.app"),
	)
	// A trailing slash on the issuer must not double up.
	assert.Equal(t,
		"https://auth.dev.liverty-music.app/ui/v2/login/verify?code={{.Code}}&userId={{.UserID}}&organization={{.OrgID}}&invite=true",
		inviteVerifyURLTemplate("https://auth.dev.liverty-music.app/"),
	)
}

func TestConsoleBaseURLFromIssuer(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		issuer  string
		want    string
		wantErr bool
	}{
		{name: "prod", issuer: "https://auth.liverty-music.app", want: "https://organizer.liverty-music.app"},
		{name: "dev", issuer: "https://auth.dev.liverty-music.app", want: "https://organizer.dev.liverty-music.app"},
		{name: "issuer host not prefixed with auth.", issuer: "https://id.example.com", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := consoleBaseURLFromIssuer(tt.issuer)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
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

func TestOrganizerProvisioner_DeleteTenant(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		stub    *stubMgmt
		wantErr error
	}{
		{
			// @spec components/entity/organizer/delete-tenant "Tenant with operators"
			name: "removes the tenant org, which removes its operators",
			stub: &stubMgmt{},
		},
		{
			// @spec components/entity/organizer/delete-tenant "Already removed"
			name: "succeeds when the tenant org no longer exists",
			stub: &stubMgmt{removeOrgErr: grpcstatus.Error(grpccodes.NotFound, "org not found")},
		},
		{
			name:    "returns Internal when the tenant org cannot be removed",
			stub:    &stubMgmt{removeOrgErr: grpcstatus.Error(grpccodes.Unavailable, "zitadel down")},
			wantErr: apperr.ErrInternal,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			p := newTestProvisioner(t, tt.stub, nil)

			err := p.DeleteTenant(context.Background(), "zitadel-org-tenant")

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
			} else {
				assert.NoError(t, err)
			}
			assert.Equal(t, []string{"zitadel-org-tenant"}, tt.stub.removeOrgOrgIDs, "RemoveOrg must target the tenant org")
		})
	}
}
