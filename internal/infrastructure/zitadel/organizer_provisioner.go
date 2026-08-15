// Package zitadel provides infrastructure-layer integration with the Zitadel
// identity management API for email verification operations.
package zitadel

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/zitadel/oidc/v3/pkg/oidc"
	"github.com/zitadel/zitadel-go/v3/pkg/client/middleware"
	zitadelconn "github.com/zitadel/zitadel-go/v3/pkg/client/zitadel"
	mgmtpb "github.com/zitadel/zitadel-go/v3/pkg/client/zitadel/management"
	policypb "github.com/zitadel/zitadel-go/v3/pkg/client/zitadel/policy"
	userpb "github.com/zitadel/zitadel-go/v3/pkg/client/zitadel/user"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/status"

	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/pannpers/go-logging/logging"

	grpccodes "google.golang.org/grpc/codes"
)

// organizerConsoleRoleOwner is the role key that grants the owner permission
// within the organizer-console project.
const organizerConsoleRoleOwner = "owner"

// Compile-time interface compliance check.
var _ usecase.OrganizerProvisioner = (*OrganizerProvisioner)(nil)

// OrganizerProvisioner provisions an Organizer's isolated Zitadel tenant via
// the Management API using the organizer-provisioner machine user credential.
// Each ProvisionTenant call is idempotent and compensating: the saga steps are
// existence-checked so a retry after a partial failure completes without
// creating duplicates and never leaves the operator without an owner grant.
type OrganizerProvisioner struct {
	mgmt                      mgmtpb.ManagementServiceClient
	organizerConsoleProjectID string
	logger                    *logging.Logger
}

// NewOrganizerProvisioner creates an OrganizerProvisioner that authenticates
// to the Zitadel Management API using a machine user's private key JWT — the
// same pattern as NewEmailVerifier in this package.
//
// issuerURL is the OIDC issuer URL (e.g., "https://auth.dev.liverty-music.app").
// provisionerKeyPath is the path to the organizer-provisioner machine key JSON.
// organizerConsoleProjectID is the Zitadel project id for the organizer-console
// application, used as the project-grant target and role container.
func NewOrganizerProvisioner(
	ctx context.Context,
	issuerURL, provisionerKeyPath, organizerConsoleProjectID string,
	logger *logging.Logger,
) (*OrganizerProvisioner, error) {
	apiEndpoint, err := grpcEndpoint(issuerURL)
	if err != nil {
		return nil, fmt.Errorf("parse zitadel domain: %w", err)
	}

	connOpts := []zitadelconn.Option{
		zitadelconn.WithJWTProfileTokenSource(
			middleware.JWTProfileFromPath(ctx, provisionerKeyPath),
		),
		zitadelconn.WithDialOptions(
			grpc.WithStatsHandler(otelgrpc.NewClientHandler()),
		),
	}

	conn, err := zitadelconn.NewConnection(
		ctx,
		issuerURL,
		apiEndpoint,
		[]string{oidc.ScopeOpenID, zitadelconn.ScopeZitadelAPI()},
		connOpts...,
	)
	if err != nil {
		return nil, fmt.Errorf("create zitadel connection: %w", err)
	}

	return &OrganizerProvisioner{
		mgmt:                      mgmtpb.NewManagementServiceClient(conn.ClientConn),
		organizerConsoleProjectID: organizerConsoleProjectID,
		logger:                    logger,
	}, nil
}

// ProvisionTenant provisions an isolated Zitadel tenant for the Organizer. It
// is idempotent and compensating: each step is existence-checked so a retry
// after a partial failure completes without creating duplicates.
//
// The provisioning saga, keyed on organizerID:
//  1. Create or find the tenant org (deterministic name: "org-" + organizerID).
//  2. Set a passkey-primary custom login policy on the tenant org.
//  3. Project-grant the organizer-console project to the tenant org.
//  4. Create the initial operator human user (existence-checked by email).
//  5. Grant the operator the owner role on the organizer-console project.
//
// It returns the Zitadel org id.
func (p *OrganizerProvisioner) ProvisionTenant(ctx context.Context, organizerID, name, operatorEmail string) (string, error) {
	// Step 1: create or find the tenant org.
	orgName := tenantOrgName(organizerID)
	zitadelOrgID, err := p.ensureOrg(ctx, orgName)
	if err != nil {
		return "", apperr.Wrap(err, codes.Internal, "ensure tenant org")
	}
	p.logger.Info(ctx, "tenant org ready",
		slog.String("organizer_id", organizerID),
		slog.String("zitadel_org_id", zitadelOrgID),
	)

	// All subsequent Management API calls are scoped to the new tenant org.
	orgCtx := middleware.SetOrgID(ctx, zitadelOrgID)

	// Step 2: set passkey-primary custom login policy.
	if err := p.ensureLoginPolicy(orgCtx); err != nil {
		return "", apperr.Wrap(err, codes.Internal, "ensure passkey-primary login policy")
	}

	// Step 3: project-grant the organizer-console project to the tenant org.
	// AddProjectGrant is called from the provisioner's own org context; the
	// organizer-console project lives in the provisioner's org, so we call
	// it without the tenant org header.
	if err := p.ensureProjectGrant(ctx, zitadelOrgID); err != nil {
		return "", apperr.Wrap(err, codes.Internal, "ensure project grant")
	}

	// Step 4: create the initial operator human user in the tenant org.
	operatorID, err := p.ensureOperatorUser(orgCtx, operatorEmail)
	if err != nil {
		return "", apperr.Wrap(err, codes.Internal, "ensure operator user")
	}

	// Step 5: grant the operator the owner role on the organizer-console project.
	if err := p.ensureUserGrant(orgCtx, operatorID); err != nil {
		return "", apperr.Wrap(err, codes.Internal, "ensure operator user grant")
	}

	p.logger.Info(ctx, "organizer tenant provisioned",
		slog.String("organizer_id", organizerID),
		slog.String("zitadel_org_id", zitadelOrgID),
		slog.String("operator_email", operatorEmail),
	)
	return zitadelOrgID, nil
}

// DeactivateOperators lists every human user in the tenant org and deactivates
// each one. It is idempotent: deactivating an already-inactive user is treated
// as success.
func (p *OrganizerProvisioner) DeactivateOperators(ctx context.Context, zitadelOrgID string) error {
	orgCtx := middleware.SetOrgID(ctx, zitadelOrgID)

	//nolint:staticcheck // SA1019: Zitadel Management API v1 is legacy but fully supported; v2 migration deferred until a live Zitadel is available to verify the provisioning saga (esp. the passkey registration email link).
	resp, err := p.mgmt.ListUsers(orgCtx, &mgmtpb.ListUsersRequest{
		Queries: []*userpb.SearchQuery{
			{
				Query: &userpb.SearchQuery_TypeQuery{
					TypeQuery: &userpb.TypeQuery{
						Type: userpb.Type_TYPE_HUMAN,
					},
				},
			},
		},
	})
	if err != nil {
		return apperr.Wrap(err, codes.Internal, "list users in tenant org")
	}

	for _, u := range resp.GetResult() {
		//nolint:staticcheck // SA1019: Zitadel Management API v1 is legacy but fully supported; v2 migration deferred until a live Zitadel is available to verify the provisioning saga (esp. the passkey registration email link).
		_, err := p.mgmt.DeactivateUser(orgCtx, &mgmtpb.DeactivateUserRequest{
			Id: u.GetId(),
		})
		if err != nil {
			if isAlreadyInState(err) {
				// User is already inactive — idempotent, skip.
				continue
			}
			return apperr.Wrap(err, codes.Internal, fmt.Sprintf("deactivate operator %s", u.GetId()))
		}
		p.logger.Info(ctx, "operator deactivated",
			slog.String("zitadel_org_id", zitadelOrgID),
			slog.String("user_id", u.GetId()),
		)
	}
	return nil
}

// ensureOrg creates the tenant org if it does not already exist. On an
// AlreadyExists response it queries for the org by name and returns its id.
func (p *OrganizerProvisioner) ensureOrg(ctx context.Context, orgName string) (string, error) {
	//nolint:staticcheck // SA1019: Zitadel Management API v1 is legacy but fully supported; v2 migration deferred until a live Zitadel is available to verify the provisioning saga (esp. the passkey registration email link).
	resp, err := p.mgmt.AddOrg(ctx, &mgmtpb.AddOrgRequest{Name: orgName})
	if err == nil {
		return resp.GetId(), nil
	}
	if !isAlreadyExists(err) {
		return "", fmt.Errorf("add org: %w", err)
	}

	// Org already exists — find it by its deterministic domain name which
	// Zitadel derives from the org name.
	//nolint:staticcheck // SA1019: Zitadel Management API v1 is legacy but fully supported; v2 migration deferred until a live Zitadel is available to verify the provisioning saga (esp. the passkey registration email link).
	existing, err := p.mgmt.GetOrgByDomainGlobal(ctx, &mgmtpb.GetOrgByDomainGlobalRequest{
		Domain: orgNameToDomain(orgName),
	})
	if err != nil {
		return "", fmt.Errorf("get org by domain: %w", err)
	}
	return existing.GetOrg().GetId(), nil
}

// ensureLoginPolicy sets a passkey-primary custom login policy on the tenant
// org. If one already exists (AlreadyExists) we skip — the policy created on
// first provision is already correct.
func (p *OrganizerProvisioner) ensureLoginPolicy(orgCtx context.Context) error {
	_, err := p.mgmt.AddCustomLoginPolicy(orgCtx, &mgmtpb.AddCustomLoginPolicyRequest{
		PasswordlessType:       policypb.PasswordlessType_PASSWORDLESS_TYPE_ALLOWED,
		AllowUsernamePassword:  false,
		AllowRegister:          false,
		AllowExternalIdp:       true,
		IgnoreUnknownUsernames: true,
	})
	if err != nil && !isAlreadyExists(err) {
		return fmt.Errorf("add custom login policy: %w", err)
	}
	return nil
}

// ensureProjectGrant grants the organizer-console project to the tenant org.
// This call is made from the provisioner's own org context (not the tenant's),
// because the project lives in the provisioner's org. On AlreadyExists, the
// existing grant satisfies the requirement.
func (p *OrganizerProvisioner) ensureProjectGrant(ctx context.Context, tenantOrgID string) error {
	//nolint:staticcheck // SA1019: Zitadel Management API v1 is legacy but fully supported; v2 migration deferred until a live Zitadel is available to verify the provisioning saga (esp. the passkey registration email link).
	_, err := p.mgmt.AddProjectGrant(ctx, &mgmtpb.AddProjectGrantRequest{
		ProjectId:    p.organizerConsoleProjectID,
		GrantedOrgId: tenantOrgID,
		RoleKeys:     []string{organizerConsoleRoleOwner},
	})
	if err != nil && !isAlreadyExists(err) {
		return fmt.Errorf("add project grant: %w", err)
	}
	return nil
}

// ensureOperatorUser ensures the initial operator human user exists in the tenant
// org and has been sent a passkey-registration init link. It returns the user id.
//
// The init link is (re)sent on every call — fresh or existing — and its failure
// is FATAL: a provisioning that cannot deliver the operator's init link must not
// report success, or the Organizer would go active with an operator who can
// never sign in. On failure the caller leaves the row `provisioning` and the
// reconciler retries; Zitadel accepts re-sending the passkey init link.
func (p *OrganizerProvisioner) ensureOperatorUser(orgCtx context.Context, operatorEmail string) (string, error) {
	userID, err := p.ensureHumanUser(orgCtx, operatorEmail)
	if err != nil {
		return "", err
	}

	//nolint:staticcheck // SA1019: Zitadel Management API v1 is legacy but fully supported; v2 migration deferred until a live Zitadel is available to verify the provisioning saga (esp. the passkey registration email link).
	if _, err := p.mgmt.SendPasswordlessRegistration(orgCtx, &mgmtpb.SendPasswordlessRegistrationRequest{
		UserId: userID,
	}); err != nil {
		return "", fmt.Errorf("send passkey registration email: %w", err)
	}
	p.logger.Info(orgCtx, "operator passkey init link sent",
		slog.String("user_id", userID),
		slog.String("email", operatorEmail),
	)
	return userID, nil
}

// ensureHumanUser creates the operator human user, or returns the id of the
// existing user when one with operatorEmail is already present (idempotent).
func (p *OrganizerProvisioner) ensureHumanUser(orgCtx context.Context, operatorEmail string) (string, error) {
	//nolint:staticcheck // SA1019: Zitadel Management API v1 is legacy but fully supported; v2 migration deferred until a live Zitadel is available to verify the provisioning saga (esp. the passkey registration email link).
	addResp, err := p.mgmt.AddHumanUser(orgCtx, &mgmtpb.AddHumanUserRequest{
		UserName: operatorEmail,
		Profile: &mgmtpb.AddHumanUserRequest_Profile{
			FirstName:   "Operator",
			LastName:    "Operator",
			DisplayName: operatorEmail,
		},
		Email: &mgmtpb.AddHumanUserRequest_Email{
			Email:           operatorEmail,
			IsEmailVerified: false,
		},
	})
	if err == nil {
		return addResp.GetUserId(), nil
	}
	if !isAlreadyExists(err) {
		return "", fmt.Errorf("add human user: %w", err)
	}

	// User already exists — look them up by email to obtain the id.
	existingID, lookupErr := p.findUserByEmail(orgCtx, operatorEmail)
	if lookupErr != nil {
		return "", fmt.Errorf("find existing operator by email: %w", lookupErr)
	}
	return existingID, nil
}

// ensureUserGrant grants the operator the owner role on the organizer-console
// project. On AlreadyExists the existing grant is sufficient.
func (p *OrganizerProvisioner) ensureUserGrant(orgCtx context.Context, operatorID string) error {
	//nolint:staticcheck // SA1019: Zitadel Management API v1 is legacy but fully supported; v2 migration deferred until a live Zitadel is available to verify the provisioning saga (esp. the passkey registration email link).
	_, err := p.mgmt.AddUserGrant(orgCtx, &mgmtpb.AddUserGrantRequest{
		UserId:    operatorID,
		ProjectId: p.organizerConsoleProjectID,
		RoleKeys:  []string{organizerConsoleRoleOwner},
	})
	if err != nil && !isAlreadyExists(err) {
		return fmt.Errorf("add user grant: %w", err)
	}
	return nil
}

// findUserByEmail searches for a human user by email address in the current
// org context and returns the first match's id.
func (p *OrganizerProvisioner) findUserByEmail(orgCtx context.Context, email string) (string, error) {
	//nolint:staticcheck // SA1019: Zitadel Management API v1 is legacy but fully supported; v2 migration deferred until a live Zitadel is available to verify the provisioning saga (esp. the passkey registration email link).
	resp, err := p.mgmt.ListUsers(orgCtx, &mgmtpb.ListUsersRequest{
		Queries: []*userpb.SearchQuery{
			{
				Query: &userpb.SearchQuery_EmailQuery{
					EmailQuery: &userpb.EmailQuery{
						EmailAddress: email,
					},
				},
			},
			{
				Query: &userpb.SearchQuery_TypeQuery{
					TypeQuery: &userpb.TypeQuery{
						Type: userpb.Type_TYPE_HUMAN,
					},
				},
			},
		},
	})
	if err != nil {
		return "", fmt.Errorf("list users by email: %w", err)
	}
	if len(resp.GetResult()) == 0 {
		return "", apperr.New(codes.NotFound, fmt.Sprintf("no user found with email %q", email))
	}
	return resp.GetResult()[0].GetId(), nil
}

// tenantOrgName derives a deterministic org name from the full organizerID so
// that retries always resolve to the same Zitadel org AND distinct organizers
// never collide. The organizerID is a UUIDv7, whose first 48 bits are a
// millisecond timestamp — so a truncated prefix would be identical for any two
// organizers created within the same ~65s window, making AddOrg return
// AlreadyExists for the second and resolving it into the first organizer's
// tenant (cross-tenant owner grant). Using the full id keeps the name unique.
func tenantOrgName(organizerID string) string {
	return "org-" + organizerID
}

// orgNameToDomain converts an org name to the Zitadel-style domain used with
// GetOrgByDomainGlobal. Our org names use only lowercase alphanumerics and
// hyphens, so no transformation is needed.
//
// KNOWN RISK (verify in prod, task 6.x): Zitadel's auto-generated org domain may
// be suffixed with the instance domain (e.g. "org-xxxx.<instance>") rather than
// the bare org name. If so, this lookup fails to resolve the org on the
// retry-after-partial-failure path. This path only triggers when AddOrg returns
// AlreadyExists (i.e. a prior partial run created the org). A more robust
// idempotency approach — persisting zitadel_org_id early and passing it to the
// provisioner so retries skip org creation entirely — is deferred to the
// reconciler work (task 3.2).
func orgNameToDomain(orgName string) string {
	return orgName
}

// isAlreadyExists reports whether the gRPC error represents an AlreadyExists
// status, which we treat as idempotent success for create operations.
func isAlreadyExists(err error) bool {
	st, ok := status.FromError(err)
	return ok && st.Code() == grpccodes.AlreadyExists
}

// isAlreadyInState reports whether the gRPC error represents a state where the
// resource is already in the target state (FailedPrecondition). Zitadel returns
// FailedPrecondition when deactivating an already-inactive user.
func isAlreadyInState(err error) bool {
	st, ok := status.FromError(err)
	return ok && st.Code() == grpccodes.FailedPrecondition
}
