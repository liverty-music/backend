// Package zitadel provides infrastructure-layer integration with the Zitadel
// identity management API for email verification operations.
package zitadel

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"

	"github.com/zitadel/oidc/v3/pkg/oidc"
	"github.com/zitadel/zitadel-go/v3/pkg/client/middleware"
	zitadelconn "github.com/zitadel/zitadel-go/v3/pkg/client/zitadel"
	mgmtpb "github.com/zitadel/zitadel-go/v3/pkg/client/zitadel/management"
	objectv2pb "github.com/zitadel/zitadel-go/v3/pkg/client/zitadel/object/v2"
	orgv2pb "github.com/zitadel/zitadel-go/v3/pkg/client/zitadel/org/v2"
	policypb "github.com/zitadel/zitadel-go/v3/pkg/client/zitadel/policy"
	userpb "github.com/zitadel/zitadel-go/v3/pkg/client/zitadel/user"
	userv2pb "github.com/zitadel/zitadel-go/v3/pkg/client/zitadel/user/v2"
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

// userSearchLimit caps the operator-email user search. Any match is decisive,
// so a few results are enough.
const userSearchLimit = 10

// Compile-time interface compliance check.
var _ usecase.OrganizerProvisioner = (*OrganizerProvisioner)(nil)

// OrganizerProvisioner provisions an Organizer's isolated Zitadel tenant via
// the Management API using the organizer-provisioner machine user credential.
// EnsureTenantOrg and ProvisionTenant are idempotent: the saga steps are
// existence-checked so a retry after a partial failure completes without
// creating duplicates and never leaves the operator without an owner grant.
type OrganizerProvisioner struct {
	mgmt                      mgmtpb.ManagementServiceClient
	userV2                    userv2pb.UserServiceClient
	orgV2                     orgv2pb.OrganizationServiceClient
	organizerConsoleProjectID string
	// consoleBaseURL is the organizer console origin (e.g.
	// https://organizer.dev.liverty-music.app), derived from the issuer. It is
	// set as the tenant login policy's default_redirect_uri so the operator is
	// returned to the console after accepting the invite (see ensureLoginPolicy).
	consoleBaseURL string
	// inviteURLTemplate is the Zitadel-standard invitation link — a literal with
	// Zitadel's own {{.Code}}/{{.UserID}}/{{.OrgID}} placeholders — pointing at
	// the hosted Login v2 /verify page on the issuer host. Derived once from the
	// issuer; the same template serves every operator.
	inviteURLTemplate string
	logger            *logging.Logger
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

	consoleBaseURL, err := consoleBaseURLFromIssuer(issuerURL)
	if err != nil {
		return nil, fmt.Errorf("derive organizer console base url: %w", err)
	}

	connOpts := []zitadelconn.Option{
		zitadelconn.WithJWTProfileTokenSource(
			jwtProfileFromPath(ctx, provisionerKeyPath),
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
		userV2:                    userv2pb.NewUserServiceClient(conn.ClientConn),
		orgV2:                     orgv2pb.NewOrganizationServiceClient(conn.ClientConn),
		organizerConsoleProjectID: organizerConsoleProjectID,
		consoleBaseURL:            consoleBaseURL,
		inviteURLTemplate:         inviteVerifyURLTemplate(issuerURL),
		logger:                    logger,
	}, nil
}

// inviteVerifyURLTemplate builds the Zitadel-standard invitation link for
// CreateInviteCode: it points at the hosted Login v2 /verify page on the issuer
// host and carries Zitadel's own {{.Code}}/{{.UserID}}/{{.OrgID}} placeholders,
// which Zitadel substitutes when it sends the mail. The operator clicks the
// "Accept invite" link and the invite code rides on the IdP surface (auth host)
// — never in a console URL. After passkey setup the tenant login policy's
// default_redirect_uri returns them to the console (see ensureLoginPolicy). This
// is Zitadel's standard invite UX (zitadel/zitadel#8310, zitadel/typescript#166)
// and mirrors the login app's own buildVerificationUrlTemplate.
func inviteVerifyURLTemplate(issuerURL string) string {
	base := strings.TrimRight(issuerURL, "/")
	return base + "/ui/v2/login/verify?code={{.Code}}&userId={{.UserID}}&organization={{.OrgID}}&invite=true"
}

// consoleBaseURLFromIssuer derives the organizer console origin from the Zitadel
// issuer URL by swapping the `auth.` host prefix for `organizer.`:
// `https://auth.dev.liverty-music.app` → `https://organizer.dev.liverty-music.app`.
// The console and the issuer share the same base domain per environment
// (see cloud-provisioning `baseDomainMap` / the organizer-console host), so this
// avoids threading a separate console-URL config through DI.
func consoleBaseURLFromIssuer(issuerURL string) (string, error) {
	u, err := url.Parse(issuerURL)
	if err != nil {
		return "", fmt.Errorf("parse issuer url %q: %w", issuerURL, err)
	}
	host, ok := strings.CutPrefix(u.Host, "auth.")
	if !ok {
		return "", fmt.Errorf("issuer host %q does not start with 'auth.'", u.Host)
	}
	return fmt.Sprintf("%s://organizer.%s", u.Scheme, host), nil
}

// operatorEmailInUseMsg is the admin-facing message for an operator email that
// already belongs to another Zitadel user.
const operatorEmailInUseMsg = "operator email is already used by another account"

// CheckOperatorEmailAvailable searches the whole instance (no org scope) for a
// user whose email, user name or login name equals operatorEmail, ignoring case.
// Zitadel user names are unique instance-wide when no org enforces
// userLoginMustBeDomain, and AddHumanUser defaults the user name to the email,
// so any such user makes the email unusable for a new operator. The instance's
// human admin, for example, uses its email as its user name.
//
// Search results are filtered by the caller's read permission. A user the
// provisioner cannot read is not found here; ensureHumanUser still rejects the
// email when AddHumanUser reports the collision.
func (p *OrganizerProvisioner) CheckOperatorEmailAvailable(ctx context.Context, operatorEmail string) error {
	users, err := p.listUsersByEmail(ctx, operatorEmail, "")
	if err != nil {
		return err
	}
	if len(users) > 0 {
		return apperr.New(codes.AlreadyExists, operatorEmailInUseMsg)
	}
	return nil
}

// EnsureTenantOrg creates the tenant org (deterministic name: "org-" +
// organizerID) and returns its id. When AddOrg reports AlreadyExists, an
// earlier attempt created the org, so it is resolved by its exact name.
func (p *OrganizerProvisioner) EnsureTenantOrg(ctx context.Context, organizerID string) (string, error) {
	orgName := tenantOrgName(organizerID)
	//nolint:staticcheck // SA1019: Zitadel Management API v1 is legacy but fully supported; v2 migration deferred until a live Zitadel is available to verify the provisioning saga (esp. the passkey registration email link).
	resp, err := p.mgmt.AddOrg(ctx, &mgmtpb.AddOrgRequest{Name: orgName})
	if err == nil {
		p.logger.Info(ctx, "tenant org created",
			slog.String("organizer_id", organizerID),
			slog.String("zitadel_org_id", resp.GetId()),
		)
		return resp.GetId(), nil
	}
	if !isAlreadyExists(err) {
		return "", apperr.Wrap(err, codes.Internal, "add tenant org")
	}

	zitadelOrgID, err := p.FindTenantOrg(ctx, organizerID)
	if errors.Is(err, apperr.ErrNotFound) {
		// AddOrg says the org exists, so NotFound here is an inconsistency
		// (e.g. a lagging projection), not a missing org; a retry may succeed.
		return "", apperr.New(codes.Internal, "tenant org already exists but was not found by name",
			slog.String("org_name", orgName),
		)
	}
	if err != nil {
		return "", fmt.Errorf("resolve existing tenant org: %w", err)
	}
	p.logger.Info(ctx, "tenant org already existed",
		slog.String("organizer_id", organizerID),
		slog.String("zitadel_org_id", zitadelOrgID),
	)
	return zitadelOrgID, nil
}

// FindTenantOrg resolves the tenant org by its exact deterministic name through
// the v2 organization search. Unlike a lookup by the generated org domain,
// which Zitadel suffixes, the name is fully determined by organizerID.
func (p *OrganizerProvisioner) FindTenantOrg(ctx context.Context, organizerID string) (string, error) {
	orgName := tenantOrgName(organizerID)
	resp, err := p.orgV2.ListOrganizations(ctx, &orgv2pb.ListOrganizationsRequest{
		Queries: []*orgv2pb.SearchQuery{
			{
				Query: &orgv2pb.SearchQuery_NameQuery{
					NameQuery: &orgv2pb.OrganizationNameQuery{
						Name:   orgName,
						Method: objectv2pb.TextQueryMethod_TEXT_QUERY_METHOD_EQUALS,
					},
				},
			},
		},
	})
	if err != nil {
		return "", apperr.Wrap(err, codes.Internal, "search tenant org by name")
	}
	for _, o := range resp.GetResult() {
		if o.GetName() == orgName && o.GetState() != orgv2pb.OrganizationState_ORGANIZATION_STATE_REMOVED {
			return o.GetId(), nil
		}
	}
	return "", apperr.New(codes.NotFound, "tenant org not found", slog.String("org_name", orgName))
}

// ProvisionTenant completes the tenant org zitadelOrgID, which EnsureTenantOrg
// created. It is idempotent: each step is existence-checked so a retry after a
// partial failure completes without creating duplicates.
//
// The steps:
//  1. Set a passkey-primary custom login policy on the tenant org.
//  2. Project-grant the organizer-console project to the tenant org.
//  3. Create the initial operator human user, or reuse the one an earlier
//     attempt created in the tenant org.
//  4. Grant the operator the owner role on the organizer-console project.
//
// Failures caused by the operator email (it belongs to a user outside the
// tenant org, or Zitadel rejects it) carry AlreadyExists or InvalidArgument and
// cannot succeed on retry; every other failure is Internal.
func (p *OrganizerProvisioner) ProvisionTenant(ctx context.Context, organizerID, zitadelOrgID, operatorEmail string) error {
	// Management API calls for the tenant's own resources are scoped to the
	// tenant org.
	orgCtx := middleware.SetOrgID(ctx, zitadelOrgID)

	if err := p.ensureLoginPolicy(orgCtx, zitadelOrgID); err != nil {
		return apperr.Wrap(err, codes.Internal, "ensure passkey-primary login policy")
	}

	// AddProjectGrant is called from the provisioner's own org context; the
	// organizer-console project lives in the provisioner's org, so we call
	// it without the tenant org header.
	if err := p.ensureProjectGrant(ctx, zitadelOrgID); err != nil {
		return apperr.Wrap(err, codes.Internal, "ensure project grant")
	}

	operatorID, err := p.ensureOperatorUser(ctx, zitadelOrgID, operatorEmail)
	if err != nil {
		// ensureOperatorUser already assigns the code (AlreadyExists /
		// InvalidArgument / Internal); keep it.
		return fmt.Errorf("ensure operator user: %w", err)
	}

	if err := p.ensureUserGrant(orgCtx, operatorID); err != nil {
		return apperr.Wrap(err, codes.Internal, "ensure operator user grant")
	}

	p.logger.Info(ctx, "organizer tenant provisioned",
		slog.String("organizer_id", organizerID),
		slog.String("zitadel_org_id", zitadelOrgID),
		slog.String("operator_email", operatorEmail),
	)
	return nil
}

// DeactivateOperators lists every human user in the tenant org and turns each
// one off so it can no longer act. It is idempotent: an already-inactive user
// is treated as success. Operators still in Zitadel's `initial` state (created
// but never completed first sign-in — e.g. the passkey init link was never
// used) are DELETED rather than deactivated, because Zitadel rejects
// deactivating an initial user ("User with state initial can only be deleted
// not deactivated") — and such a user has no credentials/session to disable
// anyway, so removing it fully satisfies the off-switch intent.
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
		if u.GetState() == userpb.UserState_USER_STATE_INITIAL {
			// Initial user: never onboarded (no password/passkey), and Zitadel
			// forbids deactivating it — delete instead.
			//nolint:staticcheck // SA1019: Zitadel Management API v1 is legacy but fully supported; v2 migration deferred until a live Zitadel instance is available to verify the provisioning saga.
			if _, err := p.mgmt.RemoveUser(orgCtx, &mgmtpb.RemoveUserRequest{Id: u.GetId()}); err != nil {
				if isNotFound(err) {
					// Already gone — idempotent, skip.
					continue
				}
				return apperr.Wrap(err, codes.Internal, fmt.Sprintf("remove initial operator %s", u.GetId()))
			}
			p.logger.Info(ctx, "initial operator removed",
				slog.String("zitadel_org_id", zitadelOrgID),
				slog.String("user_id", u.GetId()),
			)
			continue
		}

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

// DeleteTenant removes the tenant org, which removes every user in it. The org
// is addressed through the x-zitadel-orgid header, as RemoveOrg acts on the
// org of the request. A NotFound response means the org is already gone and
// counts as success, so a retry after a partial deletion completes.
func (p *OrganizerProvisioner) DeleteTenant(ctx context.Context, zitadelOrgID string) error {
	orgCtx := middleware.SetOrgID(ctx, zitadelOrgID)

	//nolint:staticcheck // SA1019: Zitadel Management API v1 is legacy but fully supported; v2 migration deferred until a live Zitadel is available to verify the provisioning saga (esp. the passkey registration email link).
	if _, err := p.mgmt.RemoveOrg(orgCtx, &mgmtpb.RemoveOrgRequest{}); err != nil {
		if isNotFound(err) {
			p.logger.Info(ctx, "tenant org already removed", slog.String("zitadel_org_id", zitadelOrgID))
			return nil
		}
		return apperr.Wrap(err, codes.Internal, fmt.Sprintf("remove tenant org %s", zitadelOrgID))
	}
	p.logger.Info(ctx, "tenant org removed", slog.String("zitadel_org_id", zitadelOrgID))
	return nil
}

// ensureLoginPolicy sets a passkey-primary custom login policy on the tenant
// org, converging an existing policy to the correct shape on retry.
//
// CRITICAL: allow_username_password MUST be true. Despite its name, this field
// (the deprecated alias of `allow_local_authentication`) does NOT merely toggle
// password login — it gates ALL *local* authentication, i.e. logging in with a
// username plus a passkey OR a password. Zitadel's own field doc: "If enabled,
// users can log in locally with their username and passkeys or password.
// Disabling this option will require users to log in with an external identity
// provider." Setting it false therefore removes the username-entry form from
// hosted Login v2's loginname page (source: apps/login `loginname/page.tsx`
// gates `<UsernameForm>` on `allowLocalAuthentication`), and since no external
// IdP is configured on the tenant org, the operator lands on an empty login
// card with no way to proceed — breaking the whole invite onboarding flow.
//
// "Passkey-primary" is achieved by PasswordlessType=ALLOWED plus never setting a
// password on the operator (ensureHumanUser), NOT by disabling local auth. With
// local auth enabled and no password on file, the only usable local method is
// the passkey — exactly the intended behavior — while the username form still
// renders so the operator can enter the invite flow.
//
// DefaultRedirectUri MUST be the organizer console. The invite is accepted on
// the hosted login /verify page OUTSIDE any in-flight OIDC request, so after
// passkey setup Login v2 has no requestId to resume; it falls back to the org
// login policy's default_redirect_uri (source-verified: apps/login
// `lib/server/passkeys.ts` passes `getLoginSettings().defaultRedirectUri` to
// `completeFlowOrGetUrl`, and `lib/client.ts` `resolveRedirectUri` uses it).
// Pointing it at the console returns the operator there to complete sign-in;
// without it they would dead-end on a Zitadel page.
//
// AddCustomLoginPolicy is create-only. Orgs provisioned before this change carry
// a policy without the default redirect (and older ones the broken
// allow_username_password=false), so on AlreadyExists we UpdateCustomLoginPolicy
// to converge them in place (idempotent saga).
func (p *OrganizerProvisioner) ensureLoginPolicy(orgCtx context.Context, zitadelOrgID string) error {
	// The default redirect carries this tenant's org id so the console can
	// enforce, after the post-invite callback, that the authenticated token's
	// org is THIS tenant — a stale/other-org SSO session in the browser must not
	// silently onboard the wrong operator. On a mismatch the console forces
	// re-authentication (design D-D, Option 2).
	defaultRedirectURI := fmt.Sprintf("%s/?org_id=%s", p.consoleBaseURL, url.QueryEscape(zitadelOrgID))
	_, err := p.mgmt.AddCustomLoginPolicy(orgCtx, &mgmtpb.AddCustomLoginPolicyRequest{
		PasswordlessType:       policypb.PasswordlessType_PASSWORDLESS_TYPE_ALLOWED,
		AllowUsernamePassword:  true,
		AllowRegister:          false,
		AllowExternalIdp:       true,
		IgnoreUnknownUsernames: true,
		DefaultRedirectUri:     defaultRedirectURI,
	})
	if err == nil {
		return nil
	}
	if !isAlreadyExists(err) {
		return fmt.Errorf("add custom login policy: %w", err)
	}

	// A custom policy already exists — update it in place so tenant orgs created
	// before this change converge to the correct passkey-primary shape and gain
	// the console default_redirect_uri.
	if _, err := p.mgmt.UpdateCustomLoginPolicy(orgCtx, &mgmtpb.UpdateCustomLoginPolicyRequest{
		PasswordlessType:       policypb.PasswordlessType_PASSWORDLESS_TYPE_ALLOWED,
		AllowUsernamePassword:  true,
		AllowRegister:          false,
		AllowExternalIdp:       true,
		IgnoreUnknownUsernames: true,
		DefaultRedirectUri:     defaultRedirectURI,
	}); err != nil {
		return fmt.Errorf("update custom login policy: %w", err)
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
// org and has a pending Zitadel invite (the onboarding email). It returns the
// user id.
//
// The invite is (re)created on every call. Its failure is FATAL — a provisioning
// that cannot deliver the operator's onboarding email must not report success,
// or the Organizer would go active with an operator who can never sign in — with
// ONE exception: if the operator has already completed onboarding, Zitadel
// rejects re-inviting them (they are past the invitable state) and that is
// benign. An already-onboarded operator can already sign in, so we treat that
// specific rejection as success and let the idempotent saga proceed to the owner
// grant instead of the reconciler re-failing forever. On any other failure the
// caller leaves the row `provisioning` and the reconciler retries.
//
// ctx carries no org header; the tenant-scoped calls derive their own.
// Every returned error is an apperr: AlreadyExists or InvalidArgument when the
// operator email itself is unusable, Internal otherwise.
func (p *OrganizerProvisioner) ensureOperatorUser(ctx context.Context, zitadelOrgID, operatorEmail string) (string, error) {
	orgCtx := middleware.SetOrgID(ctx, zitadelOrgID)
	userID, err := p.ensureHumanUser(ctx, zitadelOrgID, operatorEmail)
	if err != nil {
		return "", err
	}

	// Create the invite using Zitadel's STANDARD invitation flow: Zitadel sends
	// one branded "Invitation to Liverty Music Organizer" email via its own SMTP (the
	// backend has no SMTP of its own) whose "Accept invite" link opens the hosted
	// Login v2 /verify page directly, with the code carried in the link. The
	// operator CLICKS the link (never transcribes a code), registers a passkey,
	// and — since the invite is accepted outside any in-flight OIDC request — is
	// returned to the console via the tenant login policy default_redirect_uri
	// (see ensureLoginPolicy). This is the standard UX (zitadel/zitadel#8310,
	// zitadel/typescript#166) and replaces the earlier console-pointed transport
	// invite, which routed the operator to /verify WITHOUT the code (an empty
	// "enter code" screen) and triggered a second Login-v2 invite email.
	//
	// url_template carries Zitadel's own {{.Code}}/{{.UserID}}/{{.OrgID}}
	// placeholders (Zitadel substitutes them when sending). The code therefore
	// lives only on the IdP surface (auth host), never in a console URL.
	inviteURL := p.inviteURLTemplate
	if _, err := p.userV2.CreateInviteCode(orgCtx, &userv2pb.CreateInviteCodeRequest{
		UserId: userID,
		Verification: &userv2pb.CreateInviteCodeRequest_SendCode{
			SendCode: &userv2pb.SendInviteCode{
				UrlTemplate:     new(inviteURL),
				ApplicationName: new("Liverty Music Organizer"),
			},
		},
	}); err != nil {
		// Idempotency: an operator who already completed onboarding is past the
		// invitable state, and Zitadel rejects re-inviting them
		// (FailedPrecondition / AlreadyExists). That is benign for this
		// compensating saga — the operator can already sign in, and the invite is
		// only the email transport — so treat it as success and let provisioning
		// proceed to the owner grant, rather than wedging the row in
		// `provisioning` on every reconciler sweep. Any other code (a genuine
		// delivery/transient failure that would leave a not-yet-onboarded operator
		// unable to sign in) stays FATAL.
		if isAlreadyInState(err) || isAlreadyExists(err) {
			p.logger.Info(orgCtx, "operator already onboarded; skipping re-invite",
				slog.String("user_id", userID),
				slog.String("email", operatorEmail),
			)
			return userID, nil
		}
		return "", apperr.Wrap(err, codes.Internal, "send operator invite")
	}
	p.logger.Info(orgCtx, "operator invite sent",
		slog.String("user_id", userID),
		slog.String("email", operatorEmail),
		slog.String("invite_url", inviteURL),
	)
	return userID, nil
}

// ensureHumanUser creates the operator human user in the tenant org, or returns
// the id of the user an earlier attempt created there (idempotent).
//
// AddHumanUser reports AlreadyExists whenever the user name (defaulted to the
// email) is taken anywhere in the instance, not only in the tenant org. The
// user is therefore looked up in the tenant org: a match is a true retry and is
// reused; no match means the email belongs to a user in another org, which is
// a conflict the admin must resolve, so it fails with AlreadyExists.
func (p *OrganizerProvisioner) ensureHumanUser(ctx context.Context, zitadelOrgID, operatorEmail string) (string, error) {
	orgCtx := middleware.SetOrgID(ctx, zitadelOrgID)
	// User v2 AddHumanUser with a verified email and NO password creates a
	// passwordless operator and sends NO email: the v2 handler hardcodes
	// allowInitMail=false, so — unlike v1 — no password-initialization mail is
	// sent (which would contradict the org's passwordless login policy). The
	// email MUST be verified here so the subsequent passkey-registration mail
	// has a valid recipient; it targets verified_email, and an empty recipient
	// is rejected by the SMTP provider (501). Org is scoped via the request
	// organization field rather than the v1 context header.
	//
	//nolint:staticcheck // SA1019: v2 AddHumanUser is deprecated in favor of CreateUser, but on Zitadel v4.14.0 AddHumanUser is fully supported AND source-verified to hardcode allowInitMail=false (no password-init email) — the exact behavior this fix relies on. CreateUser's init-mail suppression is NOT verified for v4.14.0, so switching to it risks reintroducing the password-init email. Revisit when prod Zitadel is upgraded and CreateUser's behavior is confirmed.
	addResp, err := p.userV2.AddHumanUser(orgCtx, &userv2pb.AddHumanUserRequest{
		Organization: &objectv2pb.Organization{
			Org: &objectv2pb.Organization_OrgId{OrgId: zitadelOrgID},
		},
		Profile: &userv2pb.SetHumanProfile{
			GivenName:   "Operator",
			FamilyName:  "Operator",
			DisplayName: &operatorEmail,
		},
		Email: &userv2pb.SetHumanEmail{
			Email:        operatorEmail,
			Verification: &userv2pb.SetHumanEmail_IsVerified{IsVerified: true},
		},
		// No PasswordType → passwordless operator (passkey-only onboarding).
	})
	if err == nil {
		return addResp.GetUserId(), nil
	}
	switch {
	case isAlreadyExists(err):
		// Resolved below.
	case isInvalidArgument(err):
		// Zitadel rejected the email itself (e.g. malformed, or its domain is
		// claimed by another org); retrying cannot help.
		return "", apperr.Wrap(err, codes.InvalidArgument, "operator email was rejected by the identity provider")
	default:
		return "", apperr.Wrap(err, codes.Internal, "add operator user")
	}

	users, lookupErr := p.listUsersByEmail(ctx, operatorEmail, zitadelOrgID)
	if lookupErr != nil {
		return "", lookupErr
	}
	if len(users) == 0 {
		return "", apperr.New(codes.AlreadyExists, operatorEmailInUseMsg,
			slog.String("zitadel_org_id", zitadelOrgID),
		)
	}
	p.logger.Info(ctx, "operator already exists in tenant org; reusing",
		slog.String("zitadel_org_id", zitadelOrgID),
		slog.String("user_id", users[0].GetUserId()),
	)
	return users[0].GetUserId(), nil
}

// listUsersByEmail returns the users whose email, user name or login name
// equals email, ignoring case. With an empty orgID the search spans the whole
// instance (as far as the provisioner may read); otherwise it is limited to
// that org. Errors are Internal.
func (p *OrganizerProvisioner) listUsersByEmail(ctx context.Context, email, orgID string) ([]*userv2pb.User, error) {
	const ignoreCase = objectv2pb.TextQueryMethod_TEXT_QUERY_METHOD_EQUALS_IGNORE_CASE
	queries := []*userv2pb.SearchQuery{
		{
			Query: &userv2pb.SearchQuery_OrQuery{
				OrQuery: &userv2pb.OrQuery{
					Queries: []*userv2pb.SearchQuery{
						{Query: &userv2pb.SearchQuery_EmailQuery{EmailQuery: &userv2pb.EmailQuery{EmailAddress: email, Method: ignoreCase}}},
						{Query: &userv2pb.SearchQuery_UserNameQuery{UserNameQuery: &userv2pb.UserNameQuery{UserName: email, Method: ignoreCase}}},
						{Query: &userv2pb.SearchQuery_LoginNameQuery{LoginNameQuery: &userv2pb.LoginNameQuery{LoginName: email, Method: ignoreCase}}},
					},
				},
			},
		},
	}
	if orgID != "" {
		queries = append(queries, &userv2pb.SearchQuery{
			Query: &userv2pb.SearchQuery_OrganizationIdQuery{
				OrganizationIdQuery: &userv2pb.OrganizationIdQuery{OrganizationId: orgID},
			},
		})
	}

	resp, err := p.userV2.ListUsers(ctx, &userv2pb.ListUsersRequest{
		Query:   &objectv2pb.ListQuery{Limit: userSearchLimit},
		Queries: queries,
	})
	if err != nil {
		return nil, apperr.Wrap(err, codes.Internal, "search users by operator email")
	}
	return resp.GetResult(), nil
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

// isAlreadyExists reports whether the gRPC error represents an AlreadyExists
// status, which we treat as idempotent success for create operations.
func isAlreadyExists(err error) bool {
	st, ok := status.FromError(err)
	return ok && st.Code() == grpccodes.AlreadyExists
}

// isInvalidArgument reports whether the gRPC error represents an
// InvalidArgument status — Zitadel's answer to input it rejects.
func isInvalidArgument(err error) bool {
	st, ok := status.FromError(err)
	return ok && st.Code() == grpccodes.InvalidArgument
}

// isAlreadyInState reports whether the gRPC error represents a state where the
// resource is already in the target state (FailedPrecondition). Zitadel returns
// FailedPrecondition when deactivating an already-inactive user.
func isAlreadyInState(err error) bool {
	st, ok := status.FromError(err)
	return ok && st.Code() == grpccodes.FailedPrecondition
}

// isNotFound reports whether the gRPC error represents a NotFound status —
// treated as idempotent success when removing a user that is already gone.
func isNotFound(err error) bool {
	st, ok := status.FromError(err)
	return ok && st.Code() == grpccodes.NotFound
}
