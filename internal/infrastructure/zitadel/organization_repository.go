// Package zitadel provides infrastructure-layer integration with the Zitadel
// identity management API for email verification operations.
package zitadel

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"time"

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
	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/pannpers/go-logging/logging"

	grpccodes "google.golang.org/grpc/codes"
)

// organizerConsoleRoleOwner is the role key that grants the owner permission
// within the organizer-console project.
const organizerConsoleRoleOwner = "owner"

// userSearchLimit caps the owner-email user search within one Organization.
// An email names at most one identity there, so a few results are enough.
const userSearchLimit = 10

// userNameInUseMsg is the admin-facing message for an owner identity whose
// user name is taken outside the Organization.
const userNameInUseMsg = "the owner identity's user name is already used outside the organization"

// Compile-time interface compliance check.
var _ entity.OrganizationRepository = (*OrganizationRepository)(nil)

// OrganizationRepository creates, provisions and removes Organizers'
// Organizations via the Zitadel Management API using the
// organizer-provisioner machine user credential. Provision is idempotent: the
// saga steps are existence-checked so a retry after a partial failure
// completes without creating duplicates and never leaves the owner without
// the owner role.
type OrganizationRepository struct {
	mgmt                      mgmtpb.ManagementServiceClient
	userV2                    userv2pb.UserServiceClient
	orgV2                     orgv2pb.OrganizationServiceClient
	organizerConsoleProjectID string
	// consoleBaseURL is the organizer console origin (e.g.
	// https://organizer.dev.liverty-music.app), derived from the issuer. It is
	// set as the Organization login policy's default_redirect_uri so the owner
	// is returned to the console after accepting the invite (see ensureLoginPolicy).
	consoleBaseURL string
	// inviteURLTemplate is the Zitadel-standard invitation link — a literal with
	// Zitadel's own {{.Code}}/{{.UserID}}/{{.OrgID}} placeholders — pointing at
	// the hosted Login v2 /verify page on the issuer host. Derived once from the
	// issuer; the same template serves every owner identity.
	inviteURLTemplate string
	logger            *logging.Logger
}

// NewOrganizationRepository creates an OrganizationRepository that authenticates
// to the Zitadel Management API using a machine user's private key JWT — the
// same pattern as NewEmailVerifier in this package.
//
// issuerURL is the OIDC issuer URL (e.g., "https://auth.dev.liverty-music.app").
// provisionerKeyPath is the path to the organizer-provisioner machine key JSON.
// organizerConsoleProjectID is the Zitadel project id for the organizer-console
// application, used as the project-grant target and role container.
func NewOrganizationRepository(
	ctx context.Context,
	issuerURL, provisionerKeyPath, organizerConsoleProjectID string,
	logger *logging.Logger,
) (*OrganizationRepository, error) {
	apiEndpoint, err := grpcEndpoint(issuerURL)
	if err != nil {
		return nil, fmt.Errorf("parse zitadel domain: %w", err)
	}

	consoleBaseURL, err := ConsoleBaseURLFromIssuer(issuerURL)
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

	return &OrganizationRepository{
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
// which Zitadel substitutes when it sends the mail. The owner clicks the
// "Accept invite" link and the invite code rides on the IdP surface (auth host)
// — never in a console URL. After passkey setup the Organization login policy's
// default_redirect_uri returns them to the console (see ensureLoginPolicy). This
// is Zitadel's standard invite UX (zitadel/zitadel#8310, zitadel/typescript#166)
// and mirrors the login app's own buildVerificationUrlTemplate.
func inviteVerifyURLTemplate(issuerURL string) string {
	base := strings.TrimRight(issuerURL, "/")
	return base + "/ui/v2/login/verify?code={{.Code}}&userId={{.UserID}}&organization={{.OrgID}}&invite=true"
}

// ConsoleBaseURLFromIssuer derives the organizer console origin from the Zitadel
// issuer URL by swapping the `auth.` host prefix for `organizer.`:
// `https://auth.dev.liverty-music.app` → `https://organizer.dev.liverty-music.app`.
// The console and the issuer share the same base domain per environment
// (see cloud-provisioning `baseDomainMap` / the organizer-console host), so this
// avoids threading a separate console-URL config through DI.
func ConsoleBaseURLFromIssuer(issuerURL string) (string, error) {
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

// Create creates the Organizer's Organization, named by its short name, and
// returns its id. It only creates: when AddOrg reports AlreadyExists, Create
// fails with AlreadyExists and leaves resolving the existing Organization to
// the caller (GetByName).
func (p *OrganizationRepository) Create(ctx context.Context, shortName string) (string, error) {
	orgName := shortName
	//nolint:staticcheck // SA1019: Zitadel Management API v1 is legacy but fully supported; v2 migration deferred until a live Zitadel is available to verify the provisioning saga (esp. the passkey registration email link).
	resp, err := p.mgmt.AddOrg(ctx, &mgmtpb.AddOrgRequest{Name: orgName})
	if err != nil {
		if isAlreadyExists(err) {
			return "", apperr.Wrap(err, codes.AlreadyExists, "organization name already used",
				slog.String("org_name", orgName))
		}
		return "", apperr.Wrap(err, codes.Internal, "add organization")
	}
	p.logger.Info(ctx, "organization created",
		slog.String("short_name", shortName),
		slog.String("zitadel_org_id", resp.GetId()),
	)
	return resp.GetId(), nil
}

// GetByName resolves the Organization by its exact name, the Organizer's
// short name, through the v2 organization search, ignoring removed
// Organizations.
func (p *OrganizationRepository) GetByName(ctx context.Context, shortName string) (string, error) {
	orgName := shortName
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
		return "", apperr.Wrap(err, codes.Internal, "search organization by name")
	}
	for _, o := range resp.GetResult() {
		if o.GetName() == orgName && o.GetState() != orgv2pb.OrganizationState_ORGANIZATION_STATE_REMOVED {
			return o.GetId(), nil
		}
	}
	return "", apperr.New(codes.NotFound, "organization not found", slog.String("org_name", orgName))
}

// Provision completes the Organization providerOrganizerID, which Create
// created. It is idempotent: each step is existence-checked so
// a retry after a partial failure completes without creating duplicates.
//
// The steps:
//  1. Set a passkey-primary custom login policy on the Organization.
//  2. Project-grant the organizer-console project to the Organization.
//  3. Create the owner identity (user name "<ownerEmail> (<shortName>)"), or
//     reuse the one an earlier attempt created in the Organization.
//  4. Grant the owner identity the owner role on the organizer-console project.
//
// Failures caused by the owner identity itself (Zitadel rejects the email or
// the user name, or the user name is taken outside the Organization) carry
// InvalidArgument or AlreadyExists and cannot succeed on retry; every other
// failure is Internal.
func (p *OrganizationRepository) Provision(ctx context.Context, providerOrganizerID, shortName, ownerEmail string) error {
	if shortName == "" {
		// Only the Organizer created before short names lacks one; it is
		// given one by hand, after which a retry succeeds.
		return apperr.New(codes.Internal, "organizer has no short name",
			slog.String("zitadel_org_id", providerOrganizerID))
	}

	// Management API calls for the Organization's own resources are scoped to
	// the Organization.
	orgCtx := middleware.SetOrgID(ctx, providerOrganizerID)

	if err := p.ensureLoginPolicy(orgCtx, providerOrganizerID); err != nil {
		return apperr.Wrap(err, codes.Internal, "ensure passkey-primary login policy")
	}

	// AddProjectGrant is called from the provisioner's own org context; the
	// organizer-console project lives in the provisioner's org, so we call
	// it without the Organization header.
	if err := p.ensureProjectGrant(ctx, providerOrganizerID); err != nil {
		return apperr.Wrap(err, codes.Internal, "ensure project grant")
	}

	ownerID, err := p.ensureOwnerIdentity(ctx, providerOrganizerID, shortName, ownerEmail)
	if err != nil {
		// ensureOwnerIdentity already assigns the code (AlreadyExists /
		// InvalidArgument / Internal); keep it.
		return fmt.Errorf("ensure owner identity: %w", err)
	}

	if err := p.ensureUserGrant(orgCtx, ownerID); err != nil {
		return apperr.Wrap(err, codes.Internal, "ensure owner identity grant")
	}

	p.logger.Info(ctx, "organization provisioned",
		slog.String("short_name", shortName),
		slog.String("zitadel_org_id", providerOrganizerID),
		slog.String("owner_email", ownerEmail),
	)
	return nil
}

// DeactivateIdentities lists every human user in the Organization and turns
// each one off so it can no longer act. It is idempotent: an already-inactive
// user is treated as success. Identities still in Zitadel's `initial` state (created
// but never completed first sign-in — e.g. the passkey init link was never
// used) are DELETED rather than deactivated, because Zitadel rejects
// deactivating an initial user ("User with state initial can only be deleted
// not deactivated") — and such a user has no credentials/session to disable
// anyway, so removing it fully satisfies the off-switch intent.
func (p *OrganizationRepository) DeactivateIdentities(ctx context.Context, providerOrganizerID string) error {
	orgCtx := middleware.SetOrgID(ctx, providerOrganizerID)

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
		return apperr.Wrap(err, codes.Internal, "list users in organization")
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
				return apperr.Wrap(err, codes.Internal, fmt.Sprintf("remove initial identity %s", u.GetId()))
			}
			p.logger.Info(ctx, "initial identity removed",
				slog.String("zitadel_org_id", providerOrganizerID),
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
			return apperr.Wrap(err, codes.Internal, fmt.Sprintf("deactivate identity %s", u.GetId()))
		}
		p.logger.Info(ctx, "identity deactivated",
			slog.String("zitadel_org_id", providerOrganizerID),
			slog.String("user_id", u.GetId()),
		)
	}
	return nil
}

// Delete removes the Organization, which removes every user in it. The org
// is addressed through the x-zitadel-orgid header, as RemoveOrg acts on the
// org of the request. A NotFound response means the org is already gone and
// counts as success, so a retry after a partial deletion completes.
func (p *OrganizationRepository) Delete(ctx context.Context, providerOrganizerID string) error {
	orgCtx := middleware.SetOrgID(ctx, providerOrganizerID)

	//nolint:staticcheck // SA1019: Zitadel Management API v1 is legacy but fully supported; v2 migration deferred until a live Zitadel is available to verify the provisioning saga (esp. the passkey registration email link).
	if _, err := p.mgmt.RemoveOrg(orgCtx, &mgmtpb.RemoveOrgRequest{}); err != nil {
		if isNotFound(err) {
			p.logger.Info(ctx, "organization already removed", slog.String("zitadel_org_id", providerOrganizerID))
			return nil
		}
		return apperr.Wrap(err, codes.Internal, fmt.Sprintf("remove organization %s", providerOrganizerID))
	}
	p.logger.Info(ctx, "organization removed", slog.String("zitadel_org_id", providerOrganizerID))
	return nil
}

// Check lifetimes of the Organization login policy, set to Zitadel's instance
// defaults (cmd/defaults.yaml at v4.19.4). Omitting them stores 0, which the
// Zitadel Console rejects on save ("Form contains invalid value(s).";
// minValueValidator(1) in login-policy.component.ts), so the Organization policy
// could no longer be edited from the Console.
var (
	loginPolicyPasswordCheckLifetime      = durationpb.New(240 * time.Hour)
	loginPolicyExternalLoginCheckLifetime = durationpb.New(240 * time.Hour)
	loginPolicyMFAInitSkipLifetime        = durationpb.New(720 * time.Hour)
	loginPolicySecondFactorCheckLifetime  = durationpb.New(18 * time.Hour)
	loginPolicyMultiFactorCheckLifetime   = durationpb.New(12 * time.Hour)
)

// ensureLoginPolicy sets a passkey-primary custom login policy on the
// Organization, converging an existing policy to the correct shape on retry.
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
// IdP is configured on the Organization, the owner lands on an empty login
// card with no way to proceed — breaking the whole invite onboarding flow.
//
// "Passkey-primary" is achieved by PasswordlessType=ALLOWED plus never setting a
// password on the owner identity (ensureHumanUser), NOT by disabling local auth. With
// local auth enabled and no password on file, the only usable local method is
// the passkey — exactly the intended behavior — while the username form still
// renders so the owner can enter the invite flow.
//
// DefaultRedirectUri MUST be the organizer console. The invite is accepted on
// the hosted login /verify page OUTSIDE any in-flight OIDC request, so after
// passkey setup Login v2 has no requestId to resume; it falls back to the org
// login policy's default_redirect_uri (source-verified: apps/login
// `lib/server/passkeys.ts` passes `getLoginSettings().defaultRedirectUri` to
// `completeFlowOrGetUrl`, and `lib/client.ts` `resolveRedirectUri` uses it).
// Pointing it at the console returns the owner there to complete sign-in;
// without it they would dead-end on a Zitadel page.
//
// AddCustomLoginPolicy is create-only. Orgs provisioned before this change carry
// a policy without the default redirect (and older ones the broken
// allow_username_password=false, ignore_unknown_usernames=true or
// hide_password_reset=false), so on AlreadyExists we UpdateCustomLoginPolicy
// to converge them in place (idempotent saga).
func (p *OrganizationRepository) ensureLoginPolicy(orgCtx context.Context, providerOrganizerID string) error {
	// The default redirect carries this Organization's id so the console can
	// enforce, after the post-invite callback, that the authenticated token's
	// org is THIS Organization — a stale/other-org SSO session in the browser
	// must not silently onboard the wrong member. On a mismatch the console forces
	// re-authentication (design D-D, Option 2).
	defaultRedirectURI := fmt.Sprintf("%s/?org_id=%s", p.consoleBaseURL, url.QueryEscape(providerOrganizerID))
	_, err := p.mgmt.AddCustomLoginPolicy(orgCtx, &mgmtpb.AddCustomLoginPolicyRequest{
		PasswordlessType:      policypb.PasswordlessType_PASSWORDLESS_TYPE_ALLOWED,
		AllowUsernamePassword: true,
		AllowRegister:         false,
		AllowExternalIdp:      true,
		// Report an email that names no identity here as unknown. With it on,
		// Login v2 passes the typed email, not the identity's user name
		// "<email> (<short name>)", to the passkey step, which then cannot
		// find the identity (verified in production on 2026-10-15).
		IgnoreUnknownUsernames: false,
		// People sign in by typing their email; the user name is never typed.
		DisableLoginWithEmail: false,
		DefaultRedirectUri:    defaultRedirectURI,
		// Owner identities never get a password; hiding the reset removes the hosted
		// login's only self-service path to set one while local
		// authentication must stay enabled for passkeys.
		HidePasswordReset:          true,
		PasswordCheckLifetime:      loginPolicyPasswordCheckLifetime,
		ExternalLoginCheckLifetime: loginPolicyExternalLoginCheckLifetime,
		MfaInitSkipLifetime:        loginPolicyMFAInitSkipLifetime,
		SecondFactorCheckLifetime:  loginPolicySecondFactorCheckLifetime,
		MultiFactorCheckLifetime:   loginPolicyMultiFactorCheckLifetime,
	})
	if err == nil {
		return nil
	}
	if !isAlreadyExists(err) {
		return fmt.Errorf("add custom login policy: %w", err)
	}

	// A custom policy already exists — update it in place so Organizations created
	// before this change converge to the correct passkey-primary shape and gain
	// the console default_redirect_uri.
	if _, err := p.mgmt.UpdateCustomLoginPolicy(orgCtx, &mgmtpb.UpdateCustomLoginPolicyRequest{
		PasswordlessType:           policypb.PasswordlessType_PASSWORDLESS_TYPE_ALLOWED,
		AllowUsernamePassword:      true,
		AllowRegister:              false,
		AllowExternalIdp:           true,
		IgnoreUnknownUsernames:     false,
		DisableLoginWithEmail:      false,
		DefaultRedirectUri:         defaultRedirectURI,
		HidePasswordReset:          true,
		PasswordCheckLifetime:      loginPolicyPasswordCheckLifetime,
		ExternalLoginCheckLifetime: loginPolicyExternalLoginCheckLifetime,
		MfaInitSkipLifetime:        loginPolicyMFAInitSkipLifetime,
		SecondFactorCheckLifetime:  loginPolicySecondFactorCheckLifetime,
		MultiFactorCheckLifetime:   loginPolicyMultiFactorCheckLifetime,
	}); err != nil {
		return fmt.Errorf("update custom login policy: %w", err)
	}
	return nil
}

// ensureProjectGrant grants the organizer-console project to the Organization.
// This call is made from the provisioner's own org context (not the
// Organization's),
// because the project lives in the provisioner's org. On AlreadyExists, the
// existing grant satisfies the requirement.
func (p *OrganizationRepository) ensureProjectGrant(ctx context.Context, providerOrganizerID string) error {
	//nolint:staticcheck // SA1019: Zitadel Management API v1 is legacy but fully supported; v2 migration deferred until a live Zitadel is available to verify the provisioning saga (esp. the passkey registration email link).
	_, err := p.mgmt.AddProjectGrant(ctx, &mgmtpb.AddProjectGrantRequest{
		ProjectId:    p.organizerConsoleProjectID,
		GrantedOrgId: providerOrganizerID,
		RoleKeys:     []string{organizerConsoleRoleOwner},
	})
	if err != nil && !isAlreadyExists(err) {
		return fmt.Errorf("add project grant: %w", err)
	}
	return nil
}

// ensureOwnerIdentity ensures the owner identity exists in the Organization
// and has a pending Zitadel invite (the onboarding email). It returns the
// user id.
//
// The invite is (re)created on every call. Its failure is FATAL — a provisioning
// that cannot deliver the owner's onboarding email must not report success,
// or the Organizer would go active with an owner who can never sign in — with
// ONE exception: if the owner has already completed onboarding, Zitadel
// rejects re-inviting them (they are past the invitable state) and that is
// benign. An already-onboarded owner can already sign in, so we treat that
// specific rejection as success and let the idempotent saga proceed to the owner
// grant instead of the reconciler re-failing forever. On any other failure the
// caller leaves the row `provisioning` and the reconciler retries.
//
// ctx carries no org header; the Organization-scoped calls derive their own.
// Every returned error is an apperr: AlreadyExists or InvalidArgument when the
// owner email itself is unusable, Internal otherwise.
func (p *OrganizationRepository) ensureOwnerIdentity(ctx context.Context, providerOrganizerID, shortName, ownerEmail string) (string, error) {
	orgCtx := middleware.SetOrgID(ctx, providerOrganizerID)
	userID, err := p.ensureHumanUser(ctx, providerOrganizerID, shortName, ownerEmail)
	if err != nil {
		return "", err
	}

	// Create the invite using Zitadel's STANDARD invitation flow: Zitadel sends
	// one branded "Invitation to Liverty Music Organizer" email via its own SMTP (the
	// backend has no SMTP of its own) whose "Accept invite" link opens the hosted
	// Login v2 /verify page directly, with the code carried in the link. The
	// owner CLICKS the link (never transcribes a code), registers a passkey,
	// and — since the invite is accepted outside any in-flight OIDC request — is
	// returned to the console via the Organization login policy default_redirect_uri
	// (see ensureLoginPolicy). This is the standard UX (zitadel/zitadel#8310,
	// zitadel/typescript#166) and replaces the earlier console-pointed transport
	// invite, which routed the owner to /verify WITHOUT the code (an empty
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
		// Idempotency: an owner who already completed onboarding is past the
		// invitable state, and Zitadel rejects re-inviting them
		// (FailedPrecondition / AlreadyExists). That is benign for this
		// compensating saga — the owner can already sign in, and the invite is
		// only the email transport — so treat it as success and let provisioning
		// proceed to the owner grant, rather than wedging the row in
		// `provisioning` on every reconciler sweep. Any other code (a genuine
		// delivery/transient failure that would leave a not-yet-onboarded owner
		// unable to sign in) stays FATAL.
		if isAlreadyInState(err) || isAlreadyExists(err) {
			p.logger.Info(orgCtx, "owner already onboarded; skipping re-invite",
				slog.String("user_id", userID),
				slog.String("email", ownerEmail),
			)
			return userID, nil
		}
		return "", apperr.Wrap(err, codes.Internal, "send owner invite")
	}
	p.logger.Info(orgCtx, "owner invite sent",
		slog.String("user_id", userID),
		slog.String("email", ownerEmail),
		slog.String("invite_url", inviteURL),
	)
	return userID, nil
}

// ownerUserName is the user name of an Organizer's owner identity: the owner
// email followed by the short name in parentheses. User names are unique
// across the identity provider while emails are not, so the short name keeps
// the user name unique and lets the same email belong to a fan account and to
// owner identities in several Organizations. People type the email, never the
// user name.
func ownerUserName(ownerEmail, shortName string) string {
	return fmt.Sprintf("%s (%s)", ownerEmail, shortName)
}

// ensureHumanUser creates the owner identity in the Organization, or returns
// the id of the identity an earlier attempt created there (idempotent).
//
// AddHumanUser reports AlreadyExists when the user name is taken anywhere in
// the instance. The identity is therefore looked up by email in this
// Organization: a match is a true retry and is reused. No match means the user
// name belongs to an identity elsewhere, which no retry fixes, so it fails
// with AlreadyExists.
func (p *OrganizationRepository) ensureHumanUser(ctx context.Context, providerOrganizerID, shortName, ownerEmail string) (string, error) {
	orgCtx := middleware.SetOrgID(ctx, providerOrganizerID)
	userName := ownerUserName(ownerEmail, shortName)
	// User v2 AddHumanUser with a verified email and NO password creates a
	// passwordless identity and sends NO email: the v2 handler hardcodes
	// allowInitMail=false, so — unlike v1 — no password-initialization mail is
	// sent (which would contradict the org's passwordless login policy). The
	// email MUST be verified here so the subsequent passkey-registration mail
	// has a valid recipient; it targets verified_email, and an empty recipient
	// is rejected by the SMTP provider (501). Org is scoped via the request
	// organization field rather than the v1 context header.
	//
	//nolint:staticcheck // SA1019: v2 AddHumanUser is deprecated in favor of CreateUser, but on Zitadel v4.14.0 AddHumanUser is fully supported AND source-verified to hardcode allowInitMail=false (no password-init email) — the exact behavior this fix relies on. CreateUser's init-mail suppression is NOT verified for v4.14.0, so switching to it risks reintroducing the password-init email. Revisit when prod Zitadel is upgraded and CreateUser's behavior is confirmed.
	addResp, err := p.userV2.AddHumanUser(orgCtx, &userv2pb.AddHumanUserRequest{
		Username: &userName,
		Organization: &objectv2pb.Organization{
			Org: &objectv2pb.Organization_OrgId{OrgId: providerOrganizerID},
		},
		Profile: &userv2pb.SetHumanProfile{
			GivenName:   "Owner",
			FamilyName:  "Owner",
			DisplayName: &userName,
		},
		Email: &userv2pb.SetHumanEmail{
			Email:        ownerEmail,
			Verification: &userv2pb.SetHumanEmail_IsVerified{IsVerified: true},
		},
		// No PasswordType → passwordless identity (passkey-only onboarding).
	})
	if err == nil {
		return addResp.GetUserId(), nil
	}
	switch {
	case isAlreadyExists(err):
		// Resolved below.
	case isInvalidArgument(err):
		// Zitadel rejected the email or the user name (e.g. malformed, or the
		// email's domain is claimed by another org); retrying cannot help.
		return "", apperr.Wrap(err, codes.InvalidArgument, "owner email was rejected by the identity provider")
	default:
		return "", apperr.Wrap(err, codes.Internal, "add owner user")
	}

	users, lookupErr := p.listOrgUsersByEmail(ctx, providerOrganizerID, ownerEmail)
	if lookupErr != nil {
		return "", lookupErr
	}
	if len(users) == 0 {
		return "", apperr.New(codes.AlreadyExists, userNameInUseMsg,
			slog.String("zitadel_org_id", providerOrganizerID),
		)
	}
	p.logger.Info(ctx, "owner already exists in organization; reusing",
		slog.String("zitadel_org_id", providerOrganizerID),
		slog.String("user_id", users[0].GetUserId()),
	)
	return users[0].GetUserId(), nil
}

// listOrgUsersByEmail returns the users of the Organization orgID whose email
// equals email, ignoring case. Errors are Internal.
func (p *OrganizationRepository) listOrgUsersByEmail(ctx context.Context, orgID, email string) ([]*userv2pb.User, error) {
	resp, err := p.userV2.ListUsers(ctx, &userv2pb.ListUsersRequest{
		Query: &objectv2pb.ListQuery{Limit: userSearchLimit},
		Queries: []*userv2pb.SearchQuery{
			{Query: &userv2pb.SearchQuery_EmailQuery{EmailQuery: &userv2pb.EmailQuery{
				EmailAddress: email,
				Method:       objectv2pb.TextQueryMethod_TEXT_QUERY_METHOD_EQUALS_IGNORE_CASE,
			}}},
			{Query: &userv2pb.SearchQuery_OrganizationIdQuery{
				OrganizationIdQuery: &userv2pb.OrganizationIdQuery{OrganizationId: orgID},
			}},
		},
	})
	if err != nil {
		return nil, apperr.Wrap(err, codes.Internal, "search users by owner email")
	}
	return resp.GetResult(), nil
}

// ensureUserGrant grants the owner identity the owner role on the organizer-console
// project. On AlreadyExists the existing grant is sufficient.
func (p *OrganizationRepository) ensureUserGrant(orgCtx context.Context, ownerID string) error {
	//nolint:staticcheck // SA1019: Zitadel Management API v1 is legacy but fully supported; v2 migration deferred until a live Zitadel is available to verify the provisioning saga (esp. the passkey registration email link).
	_, err := p.mgmt.AddUserGrant(orgCtx, &mgmtpb.AddUserGrantRequest{
		UserId:    ownerID,
		ProjectId: p.organizerConsoleProjectID,
		RoleKeys:  []string{organizerConsoleRoleOwner},
	})
	if err != nil && !isAlreadyExists(err) {
		return fmt.Errorf("add user grant: %w", err)
	}
	return nil
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
