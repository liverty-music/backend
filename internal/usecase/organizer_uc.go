package usecase

import (
	"context"
	"errors"
	"log/slog"

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
	// Create registers a new Organizer and synchronously provisions its isolated
	// Zitadel tenant, seeding the initial operator as owner. On success the
	// Organizer is active. The operator email is checked against every existing
	// Zitadel user before anything is created. A transient provisioning failure
	// leaves the Organizer in the provisioning state for the reconciler to
	// complete and returns the error. A failure that no retry can fix (the
	// operator email is taken or rejected) removes the Organizer and its tenant
	// again and returns the error.
	//
	// # Possible errors:
	//   - InvalidArgument: the name is empty, or the operator email is malformed
	//     or rejected by Zitadel.
	//   - AlreadyExists: the operator email is already used by another account.
	//   - Internal: provisioning failed; the reconciler retries it.
	Create(ctx context.Context, name, operatorEmail string) (*entity.Organizer, error)

	// Get returns a single Organizer by id.
	//
	// # Possible errors:
	//   - NotFound: no organizer with the id exists.
	Get(ctx context.Context, id string) (*entity.Organizer, error)

	// List returns every Organizer.
	List(ctx context.Context) ([]*entity.Organizer, error)

	// ListArtists returns the artists an Organizer represents. Used by the
	// admin-facing handler, which may query any Organizer — it performs no
	// ownership check. The organizer-facing handler MUST use ListOwnArtists
	// instead.
	//
	// # Possible errors:
	//   - NotFound: no organizer with the id exists.
	ListArtists(ctx context.Context, organizerID string) ([]*entity.Artist, error)

	// ListOwnArtists returns the artists represented by the caller's own
	// Organizer, after verifying that reqOrganizerID — the organizer_id
	// supplied by the client — matches callerOrganizerID. Used by the
	// organizer-facing handler.
	//
	// # Possible errors:
	//   - PermissionDenied: reqOrganizerID does not match callerOrganizerID.
	//   - NotFound: no organizer with the given id exists.
	ListOwnArtists(ctx context.Context, callerOrganizerID, reqOrganizerID string) ([]*entity.Artist, error)

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
	ResolveCaller(ctx context.Context, zitadelOrgID string) (*entity.Organizer, error)

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

	// Deactivate turns an Organizer off: it deactivates the Zitadel operators,
	// frees the artist associations, and marks the Organizer deactivated
	// (idempotent). It accepts an Organizer in any status, including one stuck
	// in provisioning; a tenant whose link was never recorded is found by its
	// deterministic name.
	//
	// # Possible errors:
	//   - NotFound: the organizer does not exist.
	Deactivate(ctx context.Context, organizerID string) error

	// ReconcileProvisioning completes provisioning for any Organizers stuck in the
	// provisioning state (e.g. after a partial failure). It is safe to run
	// repeatedly (the saga is idempotent); per-organizer failures are logged and
	// skipped so one bad row does not stall the sweep. An Organizer whose
	// provisioning can never succeed (its operator email is taken or rejected)
	// is deactivated so the sweep stops retrying it; an admin deletes it.
	ReconcileProvisioning(ctx context.Context) error

	// Delete permanently removes a deactivated Organizer: the original and
	// served files of every Media it owns, then its tenant (when it has one,
	// found by its deterministic name when the link was never recorded), then
	// its records. Before anything is removed it runs the record deletion
	// as a dry run, so a purchase or payout account that blocks deletion stops
	// the call with nothing removed. Removed files and tenants count as removed
	// on a retry, so calling Delete again completes a deletion that failed
	// partway.
	//
	// # Possible errors:
	//   - NotFound: the organizer does not exist.
	//   - FailedPrecondition: the organizer is not deactivated, or an Order
	//     that is not Refunded, a Settlement that is not Reversed or a payout
	//     account blocks deletion.
	//   - Internal: a file or the tenant could not be removed, or media
	//     storage is not configured while the organizer owns media.
	Delete(ctx context.Context, organizerID string) error
}

// OrganizerMediaBuckets names the GCS buckets that hold Organizer media: the
// internal bucket for uploaded originals and the served bucket for the
// processed variants.
type OrganizerMediaBuckets struct {
	Internal string
	Served   string
}

type organizerUseCase struct {
	organizerRepo entity.OrganizerRepository
	artistRepo    entity.ArtistRepository
	provisioner   OrganizerProvisioner
	mediaRepo     entity.MediaRepository
	imageStorer   ImageStorer
	mediaBuckets  OrganizerMediaBuckets
	publisher     EventPublisher
	metrics       OrganizerMetrics
	logger        *logging.Logger
}

// NewOrganizerUseCase creates a new OrganizerUseCase. imageStorer may be nil
// in local development; Delete then fails with Internal for an Organizer that
// owns media rather than leaving its files behind.
func NewOrganizerUseCase(
	organizerRepo entity.OrganizerRepository,
	artistRepo entity.ArtistRepository,
	provisioner OrganizerProvisioner,
	mediaRepo entity.MediaRepository,
	imageStorer ImageStorer,
	mediaBuckets OrganizerMediaBuckets,
	publisher EventPublisher,
	metrics OrganizerMetrics,
	logger *logging.Logger,
) OrganizerUseCase {
	return &organizerUseCase{
		organizerRepo: organizerRepo,
		artistRepo:    artistRepo,
		provisioner:   provisioner,
		mediaRepo:     mediaRepo,
		imageStorer:   imageStorer,
		mediaBuckets:  mediaBuckets,
		publisher:     publisher,
		metrics:       metrics,
		logger:        logger,
	}
}

// Create registers a new Organizer and synchronously provisions its Zitadel
// tenant. The operator email is checked first so that an email already used by
// another Zitadel account fails before anything is created. On a transient
// provisioning failure the row is left in the provisioning state and the error
// is returned; the reconciler completes the saga on the next sweep. On a
// permanent failure the row and its tenant are removed again (see
// discardUnprovisionable) so nothing is left for the reconciler.
func (uc *organizerUseCase) Create(ctx context.Context, name, operatorEmail string) (*entity.Organizer, error) {
	if err := uc.provisioner.CheckOperatorEmailAvailable(ctx, operatorEmail); err != nil {
		return nil, err
	}

	// Insert the organizers row in the provisioning state, capturing the
	// operator email so the reconciler can complete provisioning after a failure.
	org := entity.NewOrganizer(name)
	org.OperatorEmail = operatorEmail
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
// be fixed by retrying: the provisioner reports a conflict or a rejected input
// (e.g. the operator email belongs to another account) with these codes, while
// transient failures are Internal.
func isPermanentProvisioningFailure(err error) bool {
	return errors.Is(err, apperr.ErrAlreadyExists) ||
		errors.Is(err, apperr.ErrInvalidArgument) ||
		errors.Is(err, apperr.ErrFailedPrecondition)
}

// discardUnprovisionable compensates a Create whose provisioning failed
// permanently: it deactivates the Organizer, which immediately takes it out of
// the reconciler's provisioning sweep, then deletes it together with its tenant
// org through the regular Delete path. Create returns cause either way, so
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
// organizer and flips it to active. The tenant org id is recorded as soon as
// the org exists, before the remaining steps, so a retry reuses the recorded id
// and never creates or searches for the org again. It wraps the call in a span
// + a provisioning-outcome metric. On failure the organizer is left in the
// provisioning state.
func (uc *organizerUseCase) completeProvisioning(ctx context.Context, org *entity.Organizer) error {
	ctx, span := otel.Tracer("usecase/organizer").Start(ctx, "ProvisionTenant")
	span.SetAttributes(attribute.String("organizer.id", org.ID))
	defer span.End()

	fail := func(err error) error {
		span.RecordError(err)
		span.SetStatus(otelcodes.Error, "organizer tenant provisioning failed")
		uc.metrics.RecordOrganizerProvisioning(ctx, "failed")
		return err
	}

	if org.ZitadelOrgID == "" {
		zitadelOrgID, err := uc.provisioner.EnsureTenantOrg(ctx, org.ID)
		if err != nil {
			return fail(err)
		}
		if err := uc.organizerRepo.SetZitadelOrgID(ctx, org.ID, zitadelOrgID); err != nil {
			return fail(err)
		}
		org.ZitadelOrgID = zitadelOrgID
	}

	if err := uc.provisioner.ProvisionTenant(ctx, org.ID, org.ZitadelOrgID, org.OperatorEmail); err != nil {
		return fail(err)
	}

	// Flip to active only if the row is still provisioning. A concurrent
	// Deactivate (which does not guard against an in-flight saga) may have moved
	// the row to deactivated during the multi-second ProvisionTenant window; an
	// unconditional flip would silently clobber that, leaving an "active" row
	// whose operators are disabled and whose artists were freed — and which the
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
	uc.logger.Info(ctx, "organizer tenant provisioned",
		slog.String("organizer_id", org.ID),
		slog.String("zitadel_org_id", org.ZitadelOrgID),
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
// and its tenant with Delete. The sweep does not delete on its own, so nothing
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

// tenantOrgID returns the Organizer's tenant org id: the recorded link, or,
// when none was recorded (an Organizer whose first provisioning attempt failed
// before the link was stored), the org found by its deterministic name. It
// returns "" when the Organizer has no tenant org.
func (uc *organizerUseCase) tenantOrgID(ctx context.Context, org *entity.Organizer) (string, error) {
	if org.ZitadelOrgID != "" {
		return org.ZitadelOrgID, nil
	}
	zitadelOrgID, err := uc.provisioner.FindTenantOrg(ctx, org.ID)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			return "", nil
		}
		return "", err
	}
	return zitadelOrgID, nil
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

// ListArtists returns the artists represented by an Organizer. Returns
// NotFound when no organizer with the given id exists.
func (uc *organizerUseCase) ListArtists(ctx context.Context, organizerID string) ([]*entity.Artist, error) {
	if _, err := uc.organizerRepo.Get(ctx, organizerID); err != nil {
		return nil, err
	}
	return uc.organizerRepo.ListArtists(ctx, organizerID)
}

// ListOwnArtists returns the artists represented by the caller's own
// Organizer. Returns PermissionDenied when reqOrganizerID does not match
// callerOrganizerID, and NotFound when no organizer with that id exists.
func (uc *organizerUseCase) ListOwnArtists(ctx context.Context, callerOrganizerID, reqOrganizerID string) ([]*entity.Artist, error) {
	if reqOrganizerID != callerOrganizerID {
		return nil, apperr.New(codes.PermissionDenied, "permission denied")
	}
	return uc.ListArtists(ctx, callerOrganizerID)
}

// ResolveCaller returns the Organizer linked to zitadelOrgID, enforcing that
// it is active. Returns PermissionDenied (non-revealing) when no organizer
// is linked or its status is neither Active nor Deactivated, and
// FailedPrecondition when it is deactivated.
func (uc *organizerUseCase) ResolveCaller(ctx context.Context, zitadelOrgID string) (*entity.Organizer, error) {
	organizer, err := uc.organizerRepo.GetByZitadelOrgID(ctx, zitadelOrgID)
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

// Deactivate turns an Organizer off: it deactivates all Zitadel operators in
// the tenant, releases artist associations so they can be re-associated
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

	// Deactivate the tenant operators in Zitadel (skip when there is no tenant).
	zitadelOrgID, err := uc.tenantOrgID(ctx, org)
	if err != nil {
		return err
	}
	if zitadelOrgID != "" {
		if err := uc.provisioner.DeactivateOperators(ctx, zitadelOrgID); err != nil {
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
// blocker check, the media files, the tenant, and finally the records. The
// records go last because they are what lets a retry find the tenant and the
// media ids again.
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

	media, err := uc.mediaRepo.ListMediaByOrganizer(ctx, organizerID)
	if err != nil {
		return err
	}
	if len(media) > 0 && (uc.imageStorer == nil || uc.mediaBuckets.Internal == "" || uc.mediaBuckets.Served == "") {
		return apperr.New(codes.Internal, "organizer media storage is not configured", slog.String("organizer_id", organizerID))
	}
	for _, m := range media {
		if err := uc.imageStorer.DeleteOriginal(ctx, uc.mediaBuckets.Internal, organizerID, m.ID); err != nil {
			return err
		}
		if err := uc.imageStorer.DeleteVariants(ctx, uc.mediaBuckets.Served, organizerID, m.ID); err != nil {
			return err
		}
	}

	// An Organizer whose provisioning failed before the tenant link was recorded
	// may still own a tenant org; find it by name so it is not orphaned.
	zitadelOrgID, err := uc.tenantOrgID(ctx, org)
	if err != nil {
		return err
	}
	if zitadelOrgID != "" {
		if err := uc.provisioner.DeleteTenant(ctx, zitadelOrgID); err != nil {
			return err
		}
	}

	if err := uc.organizerRepo.Delete(ctx, organizerID, false); err != nil {
		return err
	}
	uc.logger.Info(ctx, "organizer deleted",
		slog.String("organizer_id", organizerID),
		slog.String("zitadel_org_id", zitadelOrgID),
		slog.Int("media_count", len(media)),
	)
	return nil
}
