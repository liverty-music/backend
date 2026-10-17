package usecase

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/pannpers/go-logging/logging"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	otelcodes "go.opentelemetry.io/otel/codes"
)

// OrganizerMetrics records organizer provisioning outcomes. Implemented by the
// telemetry BusinessMetrics.
type OrganizerMetrics interface {
	// RecordOrganizerProvisioning records a provisioning outcome ("success" / "failed").
	RecordOrganizerProvisioning(ctx context.Context, status string)
}

// OrganizerUseCase defines the admin-side business logic for managing Organizers.
type OrganizerUseCase interface {
	// Create registers a new Organizer and synchronously provisions its
	// Organization at the identity provider, named by its short name, with an
	// owner identity for the owner email. On success the Organizer is active.
	// The owner email is not checked against other Organizations: it may be
	// the email of a fan account, an admin or another Organizer's owner. A
	// transient provisioning failure leaves the Organizer in the provisioning
	// state for the reconciler to complete and returns the error. A failure
	// that no retry can fix (the owner email is rejected) removes the
	// Organizer and its Organization again and returns the error.
	//
	// # Possible errors:
	//   - InvalidArgument: the name, short name or owner email breaks the
	//     Organizer rule, or the identity provider rejects the owner email.
	//   - AlreadyExists: another Organizer, in any status, has the short name;
	//     nothing is created.
	//   - Internal: provisioning failed; the reconciler retries it.
	Create(ctx context.Context, name, shortName, ownerEmail string) (*entity.Organizer, error)

	// Get returns a single Organizer by id.
	//
	// # Possible errors:
	//   - NotFound: no organizer with the id exists.
	Get(ctx context.Context, id string) (*entity.Organizer, error)

	// List returns every Organizer.
	List(ctx context.Context) ([]*entity.Organizer, error)

	// ResolveCaller returns the caller's own Organizer, resolved from the
	// Zitadel organization id extracted from the authenticated request
	// context by the organizer-facing handler, and enforces that the
	// Organizer is active.
	//
	// The status→code mapping is a business rule (non-revealing
	// authorization), not transport policy: an unlinked Zitadel org id and
	// any non-active status other than Deactivated collapse into the same
	// PermissionDenied so a caller cannot distinguish "no such organizer"
	// from "organizer not yet active." FailedPrecondition is used only for
	// the caller's own deactivated Organizer, whose state may be disclosed
	// per spec D3.
	//
	// # Possible errors:
	//   - PermissionDenied: no organizer is linked to zitadelOrgID, or its
	//     status is neither Active nor Deactivated (e.g. still provisioning).
	//   - FailedPrecondition: the organizer is deactivated.
	ResolveCaller(ctx context.Context, providerOrganizerID string) (*entity.Organizer, error)

	// AssociateArtist links an existing artist to an Organizer.
	//
	// # Possible errors:
	//   - NotFound: the organizer or the artist does not exist.
	//   - AlreadyExists: the artist is already represented by an organizer.
	//   - FailedPrecondition: the organizer is deactivated.
	AssociateArtist(ctx context.Context, organizerID, artistID string) error

	// DisassociateArtist removes the link between an Organizer and an artist
	// (idempotent).
	//
	// # Possible errors:
	//   - NotFound: the organizer does not exist.
	//   - FailedPrecondition: the organizer is deactivated.
	DisassociateArtist(ctx context.Context, organizerID, artistID string) error

	// Deactivate turns an Organizer off: it deactivates its members'
	// identities, frees the artist associations, and marks the Organizer
	// deactivated (idempotent). It accepts an Organizer in any status,
	// including one stuck in provisioning; an Organization whose link was
	// never recorded is found by its deterministic name.
	//
	// # Possible errors:
	//   - NotFound: the organizer does not exist.
	Deactivate(ctx context.Context, organizerID string) error

	// ReconcileProvisioning completes provisioning for any Organizers stuck in the
	// provisioning state (e.g. after a partial failure). It is safe to run
	// repeatedly (the saga is idempotent); per-organizer failures are logged and
	// skipped so one bad row does not stall the sweep. An Organizer whose
	// provisioning can never succeed (its owner email is taken or rejected)
	// is deactivated so the sweep stops retrying it; an admin deletes it.
	ReconcileProvisioning(ctx context.Context) error

	// Delete permanently removes a deactivated Organizer: the original and
	// served files of every SeriesImage it owns, then its Organization (when it has one,
	// found by its deterministic name when the link was never recorded), then
	// its records. Before anything is removed it runs the record deletion
	// as a dry run, so a purchase or payout account that blocks deletion stops
	// the call with nothing removed. Removed files and Organizations count as removed
	// on a retry, so calling Delete again completes a deletion that failed
	// partway.
	//
	// # Possible errors:
	//   - NotFound: the organizer does not exist.
	//   - FailedPrecondition: the organizer is not deactivated, or an Order
	//     that is not Refunded, a Settlement that is not Reversed or a payout
	//     account blocks deletion.
	//   - Internal: a file or the Organization could not be removed, or image
	//     storage is not configured while the organizer owns series images.
	Delete(ctx context.Context, organizerID string) error

	// SetBusinessDetails records the Organizer's 特商法 business details (legal
	// name, representative name, business address, business phone number and
	// contact email), checked by an admin at vetting, and returns the
	// Organizer.
	//
	// # Possible errors
	//
	//   - NotFound: the organizer does not exist.
	//   - FailedPrecondition: the organizer is deactivated; nothing changed.
	//   - InvalidArgument: a detail is missing or breaks the business details
	//     rule.
	SetBusinessDetails(ctx context.Context, organizerID string, details entity.BusinessDetails) (*entity.Organizer, error)

	// SetPlatformFeeRate sets the platform fee rate applied to the Organizer's
	// future Orders and returns the Organizer. Issued Orders keep the rate of
	// their Settlement.
	//
	// # Possible errors
	//
	//   - NotFound: the organizer does not exist.
	//   - InvalidArgument: the rate is outside 0 to 3000 basis points.
	SetPlatformFeeRate(ctx context.Context, organizerID string, rateBps int) (*entity.Organizer, error)

	// SendSignInLinks emails the address one organizer console sign-in link
	// per active Organizer it owns, and sends nothing when it owns none. It
	// succeeds in the same way either way, so the caller cannot tell whether
	// the address owns an Organizer: after the per-address limit (at most 3
	// sends per 60 minutes, compared without regard to letter case; a further
	// request succeeds without sending) it returns at once, and the lookup and
	// the send run in the background, where failures are logged.
	SendSignInLinks(ctx context.Context, email string)
}

// StorageBuckets names the GCS buckets that hold Organizer series images: the
// internal bucket for uploaded originals and the served bucket for the
// processed variants.
type StorageBuckets struct {
	Internal string
	Served   string
}

type organizerUseCase struct {
	organizerRepo    entity.OrganizerRepository
	signInLinks      entity.SignInLinkSender
	artistRepo       entity.ArtistRepository
	organizationRepo entity.OrganizationRepository
	seriesImageRepo  entity.SeriesImageRepository
	imageStorer      ImageStorer
	storageBuckets   StorageBuckets
	publisher        EventPublisher
	metrics          OrganizerMetrics
	logger           *logging.Logger

	// signInLinkLimiter bounds the sign-in link emails sent to one address.
	signInLinkLimiter *slidingWindowLimiter
	// background runs the sign-in link lookup and send after the call
	// returns. Tests replace it to run the task synchronously.
	background func(task func())
}

// signInLinkLimit and signInLinkWindow bound the sign-in link emails sent to
// one address: at most signInLinkLimit within any signInLinkWindow.
const (
	signInLinkLimit  = 3
	signInLinkWindow = 60 * time.Minute
	// signInLinkTimeout bounds the background lookup and send.
	signInLinkTimeout = 30 * time.Second
)

// NewOrganizerUseCase creates a new OrganizerUseCase. imageStorer may be nil
// in local development; Delete then fails with Internal for an Organizer that
// owns series images rather than leaving its files behind. signInLinks sends
// the sign-in link emails of SendSignInLinks.
func NewOrganizerUseCase(
	organizerRepo entity.OrganizerRepository,
	signInLinks entity.SignInLinkSender,
	artistRepo entity.ArtistRepository,
	organizationRepo entity.OrganizationRepository,
	seriesImageRepo entity.SeriesImageRepository,
	imageStorer ImageStorer,
	storageBuckets StorageBuckets,
	publisher EventPublisher,
	metrics OrganizerMetrics,
	logger *logging.Logger,
) OrganizerUseCase {
	return &organizerUseCase{
		organizerRepo:     organizerRepo,
		signInLinks:       signInLinks,
		signInLinkLimiter: newSlidingWindowLimiter(signInLinkLimit, signInLinkWindow, time.Now),
		background:        func(task func()) { go task() },
		artistRepo:        artistRepo,
		organizationRepo:  organizationRepo,
		seriesImageRepo:   seriesImageRepo,
		imageStorer:       imageStorer,
		storageBuckets:    storageBuckets,
		publisher:         publisher,
		metrics:           metrics,
		logger:            logger,
	}
}

// Create registers a new Organizer and synchronously provisions its
// Organization. A short name already used fails at the insert, before
// anything else is created. On a transient provisioning failure the row is
// left in the provisioning state and the error is returned; the reconciler
// completes the saga on the next sweep. On a permanent failure the row and its
// Organization are removed again (see discardUnprovisionable) so nothing is
// left for the reconciler.
func (uc *organizerUseCase) Create(ctx context.Context, name, shortName, ownerEmail string) (*entity.Organizer, error) {
	// Insert the organizers row in the provisioning state, capturing the
	// owner email so the reconciler can complete provisioning after a failure.
	org := entity.NewOrganizer(name, shortName, ownerEmail)
	if err := org.Validate(); err != nil {
		return nil, apperr.Wrap(err, codes.InvalidArgument, err.Error())
	}
	created, err := uc.organizerRepo.Create(ctx, org)
	if err != nil {
		return nil, err
	}

	if err := uc.completeProvisioning(ctx, created); err != nil {
		if isPermanentProvisioningFailure(err) {
			uc.discardUnprovisionable(ctx, created.ID, err)
		}
		return nil, err
	}
	return created, nil
}

// isPermanentProvisioningFailure reports whether a provisioning error can never
// be fixed by retrying: the identity provider reports a conflict or a rejected input
// (e.g. the identity provider rejects the owner email) with these codes, while
// transient failures are Internal.
func isPermanentProvisioningFailure(err error) bool {
	return errors.Is(err, apperr.ErrAlreadyExists) ||
		errors.Is(err, apperr.ErrInvalidArgument) ||
		errors.Is(err, apperr.ErrFailedPrecondition)
}

// discardUnprovisionable compensates a Create whose provisioning failed
// permanently: it deactivates the Organizer, which immediately takes it out of
// the reconciler's provisioning sweep, then deletes it together with its
// Organization through the regular Delete path. Create returns cause either way, so
// a compensation failure is only logged; whatever remains is a deactivated
// Organizer that an admin can delete.
func (uc *organizerUseCase) discardUnprovisionable(ctx context.Context, organizerID string, cause error) {
	if err := uc.Deactivate(ctx, organizerID); err != nil {
		uc.logger.Warn(ctx, "failed to deactivate an organizer whose provisioning failed permanently; the reconciler will retry",
			slog.String("organizer_id", organizerID),
			slog.Any("cause", cause),
			slog.Any("error", err),
		)
		return
	}
	if err := uc.Delete(ctx, organizerID); err != nil {
		uc.logger.Warn(ctx, "failed to delete an organizer whose provisioning failed permanently; it stays deactivated for an admin to delete",
			slog.String("organizer_id", organizerID),
			slog.Any("cause", cause),
			slog.Any("error", err),
		)
		return
	}
	uc.logger.Info(ctx, "organizer discarded after a permanent provisioning failure",
		slog.String("organizer_id", organizerID),
		slog.Any("cause", cause),
	)
}

// completeProvisioning runs the Zitadel provisioning saga (idempotent) for an
// Organizer and flips it to active. The Organization's id is recorded as soon
// as the Organization exists, before the remaining steps, so a retry reuses
// the recorded id and never creates or searches for the Organization again. It wraps the call in a span
// + a provisioning-outcome metric. On failure the organizer is left in the
// provisioning state.
func (uc *organizerUseCase) completeProvisioning(ctx context.Context, org *entity.Organizer) error {
	ctx, span := otel.Tracer("usecase/organizer").Start(ctx, "ProvisionOrganization")
	span.SetAttributes(attribute.String("organizer.id", org.ID))
	defer span.End()

	fail := func(err error) error {
		span.RecordError(err)
		span.SetStatus(otelcodes.Error, "organization provisioning failed")
		uc.metrics.RecordOrganizerProvisioning(ctx, "failed")
		return err
	}

	if org.ProviderOrganizerID == "" {
		providerOrganizerID, err := uc.createOrganization(ctx, org.ShortName)
		if err != nil {
			return fail(err)
		}
		if err := uc.organizerRepo.SetProviderOrganizerID(ctx, org.ID, providerOrganizerID); err != nil {
			return fail(err)
		}
		org.ProviderOrganizerID = providerOrganizerID
	}

	if err := uc.organizationRepo.Provision(ctx, org.ProviderOrganizerID, org.ShortName, org.OwnerEmail); err != nil {
		return fail(err)
	}

	// Flip to active only if the row is still provisioning. A concurrent
	// Deactivate (which does not guard against an in-flight saga) may have moved
	// the row to deactivated during the multi-second Provision window; an
	// unconditional flip would silently clobber that, leaving an "active" row
	// whose identities are disabled and whose artists were freed — and which the
	// reconciler (provisioning-only) never revisits. Treat a superseded row as a
	// no-op: deactivation wins.
	activated, err := uc.organizerRepo.CompareAndSetStatus(ctx, org.ID, entity.OrganizerStatusProvisioning, entity.OrganizerStatusActive)
	if err != nil {
		return err
	}
	if !activated {
		uc.logger.Info(ctx, "organizer provisioning superseded before activation; skipping",
			slog.String("organizer_id", org.ID),
		)
		return nil
	}

	org.Status = entity.OrganizerStatusActive
	uc.metrics.RecordOrganizerProvisioning(ctx, "success")
	uc.logger.Info(ctx, "organizer provisioned",
		slog.String("organizer_id", org.ID),
		slog.String("provider_organizer_id", org.ProviderOrganizerID),
	)

	if err := uc.publisher.PublishEvent(ctx, entity.SubjectOrganizerCreated, entity.OrganizerCreatedData{
		OrganizerID: org.ID,
	}); err != nil {
		uc.logger.Warn(ctx, "failed to publish ORGANIZER.created event",
			slog.String("organizer_id", org.ID),
			slog.Any("error", err),
		)
		// Non-fatal: the organizer is already active in the database.
	}
	return nil
}

// ReconcileProvisioning retries the Zitadel provisioning saga for every
// Organizer stuck in the provisioning state. Per-organizer failures are
// logged and skipped so one bad row does not stall the sweep. Safe to call
// repeatedly — completeProvisioning is idempotent.
//
// A permanent failure (see isPermanentProvisioningFailure) would fail the same
// way on every sweep, so the Organizer is deactivated instead: deactivated is
// the existing terminal status the sweep ignores, and an admin removes the row
// and its Organization with Delete. The sweep does not delete on its own, so nothing
// an admin created disappears in the background.
func (uc *organizerUseCase) ReconcileProvisioning(ctx context.Context) error {
	stuck, err := uc.organizerRepo.ListByStatus(ctx, entity.OrganizerStatusProvisioning)
	if err != nil {
		return err
	}
	for _, org := range stuck {
		err := uc.completeProvisioning(ctx, org)
		switch {
		case err == nil:
			uc.logger.Info(ctx, "reconcile: organizer provisioning completed",
				slog.String("organizer_id", org.ID),
			)
		case isPermanentProvisioningFailure(err):
			if deactivateErr := uc.Deactivate(ctx, org.ID); deactivateErr != nil {
				uc.logger.Warn(ctx, "reconcile: failed to deactivate an organizer whose provisioning failed permanently",
					slog.String("organizer_id", org.ID),
					slog.Any("cause", err),
					slog.Any("error", deactivateErr),
				)
				continue
			}
			uc.logger.Warn(ctx, "reconcile: organizer provisioning failed permanently; organizer deactivated, delete it via the admin console",
				slog.String("organizer_id", org.ID),
				slog.Any("error", err),
			)
		default:
			uc.logger.Warn(ctx, "reconcile: organizer provisioning still incomplete",
				slog.String("organizer_id", org.ID),
				slog.Any("error", err),
			)
		}
	}
	return nil
}

// createOrganization creates the Organizer's Organization, named by its short
// name, and returns its id. When Create reports AlreadyExists, an earlier
// attempt created the Organization but did not record it (short names are
// unique among Organizers and the product and admin names are reserved), so it
// is resolved by its name. NotFound there contradicts AlreadyExists (e.g. a
// lagging search index), so it is Internal: the Organizer stays provisioning
// and the next sweep retries.
func (uc *organizerUseCase) createOrganization(ctx context.Context, shortName string) (string, error) {
	providerOrganizerID, err := uc.organizationRepo.Create(ctx, shortName)
	if err == nil {
		return providerOrganizerID, nil
	}
	if !errors.Is(err, apperr.ErrAlreadyExists) {
		return "", err
	}
	providerOrganizerID, err = uc.organizationRepo.GetByName(ctx, shortName)
	if errors.Is(err, apperr.ErrNotFound) {
		return "", apperr.New(codes.Internal, "organization already exists but was not found by name",
			slog.String("short_name", shortName))
	}
	if err != nil {
		return "", err
	}
	uc.logger.Info(ctx, "organization created by an earlier attempt; reusing it",
		slog.String("short_name", shortName),
		slog.String("provider_organizer_id", providerOrganizerID),
	)
	return providerOrganizerID, nil
}

// organizationID returns the Organizer's Organization id: the recorded link, or,
// when none was recorded (an Organizer whose first provisioning attempt failed
// before the link was stored), the Organization found by its short name. It
// returns "" when the Organizer has no Organization.
func (uc *organizerUseCase) organizationID(ctx context.Context, org *entity.Organizer) (string, error) {
	if org.ProviderOrganizerID != "" {
		return org.ProviderOrganizerID, nil
	}
	if org.ShortName == "" {
		// Only the Organizer created before short names lacks one, and it
		// has a recorded Organization.
		return "", nil
	}
	providerOrganizerID, err := uc.organizationRepo.GetByName(ctx, org.ShortName)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			return "", nil
		}
		return "", err
	}
	return providerOrganizerID, nil
}

// Get returns the Organizer with the given id. Returns NotFound when no
// organizer with that id exists.
func (uc *organizerUseCase) Get(ctx context.Context, id string) (*entity.Organizer, error) {
	return uc.organizerRepo.Get(ctx, id)
}

// List returns every Organizer regardless of status.
func (uc *organizerUseCase) List(ctx context.Context) ([]*entity.Organizer, error) {
	return uc.organizerRepo.List(ctx)
}

// ResolveCaller returns the Organizer linked to zitadelOrgID, enforcing that
// it is active. Returns PermissionDenied (non-revealing) when no organizer
// is linked or its status is neither Active nor Deactivated, and
// FailedPrecondition when it is deactivated.
func (uc *organizerUseCase) ResolveCaller(ctx context.Context, providerOrganizerID string) (*entity.Organizer, error) {
	organizer, err := uc.organizerRepo.GetByProviderOrganizerID(ctx, providerOrganizerID)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			return nil, apperr.New(codes.PermissionDenied, "permission denied")
		}
		return nil, err
	}

	switch organizer.Status {
	case entity.OrganizerStatusActive:
		return organizer, nil
	case entity.OrganizerStatusDeactivated:
		return nil, apperr.New(codes.FailedPrecondition, "organizer is deactivated")
	default:
		return nil, apperr.New(codes.PermissionDenied, "permission denied")
	}
}

// AssociateArtist links an existing artist to an Organizer. Returns
// NotFound if either the organizer or the artist does not exist,
// AlreadyExists if the artist is already represented by any organizer, and
// FailedPrecondition if the organizer is deactivated.
func (uc *organizerUseCase) AssociateArtist(ctx context.Context, organizerID, artistID string) error {
	org, err := uc.organizerRepo.Get(ctx, organizerID)
	if err != nil {
		return err
	}
	if org.Status == entity.OrganizerStatusDeactivated {
		return apperr.New(codes.FailedPrecondition, "organizer is deactivated")
	}
	// No create-on-demand: an unknown artist is rejected with NotFound.
	if _, err := uc.artistRepo.Get(ctx, artistID); err != nil {
		return err
	}
	if err := uc.organizerRepo.AssociateArtist(ctx, organizerID, artistID); err != nil {
		return err
	}

	if err := uc.publisher.PublishEvent(ctx, entity.SubjectOrganizerArtistAssociated, entity.OrganizerArtistAssociatedData{
		OrganizerID: organizerID,
		ArtistID:    artistID,
	}); err != nil {
		uc.logger.Warn(ctx, "failed to publish ORGANIZER.artist_associated event",
			slog.String("organizer_id", organizerID),
			slog.String("artist_id", artistID),
			slog.Any("error", err),
		)
		// Non-fatal: the association is already persisted in the database.
	}
	return nil
}

// DisassociateArtist removes the link between an Organizer and an artist
// (idempotent at the DB level). Returns NotFound if the organizer does not
// exist and FailedPrecondition if it is deactivated.
func (uc *organizerUseCase) DisassociateArtist(ctx context.Context, organizerID, artistID string) error {
	org, err := uc.organizerRepo.Get(ctx, organizerID)
	if err != nil {
		return err
	}
	if org.Status == entity.OrganizerStatusDeactivated {
		return apperr.New(codes.FailedPrecondition, "organizer is deactivated")
	}
	return uc.organizerRepo.DisassociateArtist(ctx, organizerID, artistID)
}

// Deactivate turns an Organizer off: it deactivates every identity in its
// Organization, releases artist associations so they can be re-associated
// elsewhere, and marks the Organizer deactivated. It accepts an Organizer in
// any status, including provisioning. The operation is idempotent — calling it
// on an already-deactivated organizer returns nil. Returns NotFound if the
// organizer does not exist.
func (uc *organizerUseCase) Deactivate(ctx context.Context, organizerID string) error {
	org, err := uc.organizerRepo.Get(ctx, organizerID)
	if err != nil {
		return err
	}
	if org.Status == entity.OrganizerStatusDeactivated {
		return nil // idempotent
	}

	// Deactivate the members' identities (skip when there is no Organization).
	providerOrganizerID, err := uc.organizationID(ctx, org)
	if err != nil {
		return err
	}
	if providerOrganizerID != "" {
		if err := uc.organizationRepo.DeactivateIdentities(ctx, providerOrganizerID); err != nil {
			return err
		}
	}

	// Free the artist associations so they can be re-associated.
	if err := uc.organizerRepo.FreeArtists(ctx, organizerID); err != nil {
		return err
	}

	if err := uc.organizerRepo.SetStatus(ctx, organizerID, entity.OrganizerStatusDeactivated); err != nil {
		return err
	}
	uc.logger.Info(ctx, "organizer deactivated", slog.String("organizer_id", organizerID))
	return nil
}

// Delete removes a deactivated Organizer in the order of design D1: the dry-run
// blocker check, the image files, the Organization, and finally the records.
// The records go last because they are what lets a retry find the
// Organization and the
// series image id again.
func (uc *organizerUseCase) Delete(ctx context.Context, organizerID string) error {
	org, err := uc.organizerRepo.Get(ctx, organizerID)
	if err != nil {
		return err
	}
	if org.Status != entity.OrganizerStatusDeactivated {
		return apperr.New(codes.FailedPrecondition, "organizer is not deactivated", slog.String("organizer_id", organizerID))
	}
	if err := uc.organizerRepo.Delete(ctx, organizerID, true); err != nil {
		return err
	}

	seriesImage, err := uc.seriesImageRepo.ListByOrganizer(ctx, organizerID)
	if err != nil {
		return err
	}
	if len(seriesImage) > 0 && (uc.imageStorer == nil || uc.storageBuckets.Internal == "" || uc.storageBuckets.Served == "") {
		return apperr.New(codes.Internal, "organizer image storage is not configured", slog.String("organizer_id", organizerID))
	}
	for _, m := range seriesImage {
		if err := uc.imageStorer.DeleteOriginal(ctx, uc.storageBuckets.Internal, organizerID, m.ID); err != nil {
			return err
		}
		if err := uc.imageStorer.DeleteVariants(ctx, uc.storageBuckets.Served, organizerID, m.ID); err != nil {
			return err
		}
	}

	// An Organizer whose provisioning failed before the Organization link was
	// recorded may still own an Organization; find it by name so it is not
	// orphaned.
	providerOrganizerID, err := uc.organizationID(ctx, org)
	if err != nil {
		return err
	}
	if providerOrganizerID != "" {
		if err := uc.organizationRepo.Delete(ctx, providerOrganizerID); err != nil {
			return err
		}
	}

	if err := uc.organizerRepo.Delete(ctx, organizerID, false); err != nil {
		return err
	}
	uc.logger.Info(ctx, "organizer deleted",
		slog.String("organizer_id", organizerID),
		slog.String("provider_organizer_id", providerOrganizerID),
		slog.Int("series_image_count", len(seriesImage)),
	)
	return nil
}

// SetBusinessDetails implements [OrganizerUseCase].
func (uc *organizerUseCase) SetBusinessDetails(ctx context.Context, organizerID string, details entity.BusinessDetails) (*entity.Organizer, error) {
	org, err := uc.organizerRepo.Get(ctx, organizerID)
	if err != nil {
		return nil, err
	}
	if org.Status == entity.OrganizerStatusDeactivated {
		return nil, apperr.New(codes.FailedPrecondition, "organizer is deactivated")
	}
	return uc.organizerRepo.SetBusinessDetails(ctx, organizerID, details)
}

// SetPlatformFeeRate implements [OrganizerUseCase].
func (uc *organizerUseCase) SetPlatformFeeRate(ctx context.Context, organizerID string, rateBps int) (*entity.Organizer, error) {
	if err := uc.organizerRepo.SetPlatformFeeRate(ctx, organizerID, rateBps); err != nil {
		return nil, err
	}
	uc.logger.Info(ctx, "organizer platform fee rate set",
		slog.String("organizer_id", organizerID), slog.Int("platform_fee_rate_bps", rateBps))
	return uc.organizerRepo.Get(ctx, organizerID)
}

// SendSignInLinks implements [OrganizerUseCase]. The per-address check runs
// before it returns; the lookup and the send run afterwards on a context
// detached from the request, so neither the answer nor its timing depends on
// whether the address owns an Organizer.
func (uc *organizerUseCase) SendSignInLinks(ctx context.Context, email string) {
	address := strings.ToLower(email)
	if !uc.signInLinkLimiter.allow(address) {
		uc.logger.Info(ctx, "sign-in links not sent: per-address limit reached")
		return
	}
	bgCtx := context.WithoutCancel(ctx)
	uc.background(func() {
		ctx, cancel := context.WithTimeout(bgCtx, signInLinkTimeout)
		defer cancel()
		uc.sendSignInLinks(ctx, email)
	})
}

// sendSignInLinks reads the active Organizers the address owns and, when
// there are any, sends their sign-in links to the owner email as stored.
// Failures are logged.
func (uc *organizerUseCase) sendSignInLinks(ctx context.Context, email string) {
	organizers, err := uc.organizerRepo.ListByOwnerEmail(ctx, email)
	if err != nil {
		uc.logger.Error(ctx, "failed to list organizers for sign-in links", err)
		return
	}
	if len(organizers) == 0 {
		return
	}
	// The lookup ignores letter case; send to the address as stored, which
	// was given by an admin, not to the caller's spelling of it.
	if err := uc.signInLinks.SendSignInLinks(ctx, organizers[0].OwnerEmail, organizers); err != nil {
		uc.logger.Error(ctx, "failed to send sign-in links", err,
			slog.Int("organizer_count", len(organizers)))
		return
	}
	uc.logger.Info(ctx, "sign-in links sent", slog.Int("organizer_count", len(organizers)))
}
