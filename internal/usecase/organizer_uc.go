package usecase

import (
	"context"
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
	// Organizer is active. A partial provisioning failure leaves it in the
	// provisioning state for the reconciler to complete and returns the error.
	//
	// # Possible errors:
	//   - InvalidArgument: the name is empty or the operator email is malformed.
	Create(ctx context.Context, name, operatorEmail string) (*entity.Organizer, error)

	// Get returns a single Organizer by id.
	//
	// # Possible errors:
	//   - NotFound: no organizer with the id exists.
	Get(ctx context.Context, id string) (*entity.Organizer, error)

	// List returns every Organizer.
	List(ctx context.Context) ([]*entity.Organizer, error)

	// ListArtists returns the artists an Organizer represents.
	//
	// # Possible errors:
	//   - NotFound: no organizer with the id exists.
	ListArtists(ctx context.Context, organizerID string) ([]*entity.Artist, error)

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
	// (idempotent).
	//
	// # Possible errors:
	//   - NotFound: the organizer does not exist.
	Deactivate(ctx context.Context, organizerID string) error

	// ReconcileProvisioning completes provisioning for any Organizers stuck in the
	// provisioning state (e.g. after a partial failure). It is safe to run
	// repeatedly (the saga is idempotent); per-organizer failures are logged and
	// skipped so one bad row does not stall the sweep.
	ReconcileProvisioning(ctx context.Context) error
}

type organizerUseCase struct {
	organizerRepo entity.OrganizerRepository
	artistRepo    entity.ArtistRepository
	provisioner   entity.OrganizerProvisioner
	metrics       OrganizerMetrics
	logger        *logging.Logger
}

// NewOrganizerUseCase creates a new OrganizerUseCase.
func NewOrganizerUseCase(
	organizerRepo entity.OrganizerRepository,
	artistRepo entity.ArtistRepository,
	provisioner entity.OrganizerProvisioner,
	metrics OrganizerMetrics,
	logger *logging.Logger,
) OrganizerUseCase {
	return &organizerUseCase{
		organizerRepo: organizerRepo,
		artistRepo:    artistRepo,
		provisioner:   provisioner,
		metrics:       metrics,
		logger:        logger,
	}
}

func (uc *organizerUseCase) Create(ctx context.Context, name, operatorEmail string) (*entity.Organizer, error) {
	// Step 1: insert the organizers row in the provisioning state, capturing the
	// operator email so the reconciler can complete provisioning after a failure.
	org := entity.NewOrganizer(name)
	org.OperatorEmail = operatorEmail
	created, err := uc.organizerRepo.Create(ctx, org)
	if err != nil {
		return nil, err
	}

	// Steps 2–6: provision the Zitadel tenant and flip to active. On failure the
	// row stays in the provisioning state for the reconciler to complete.
	if err := uc.completeProvisioning(ctx, created); err != nil {
		return nil, err
	}
	return created, nil
}

// completeProvisioning runs the Zitadel provisioning saga (idempotent +
// compensating) for an organizer and flips it to active. It wraps the call in a
// span + a provisioning-outcome metric. On failure the organizer is left in the
// provisioning state for the reconciler.
func (uc *organizerUseCase) completeProvisioning(ctx context.Context, org *entity.Organizer) error {
	ctx, span := otel.Tracer("usecase/organizer").Start(ctx, "ProvisionTenant")
	span.SetAttributes(attribute.String("organizer.id", org.ID))
	defer span.End()

	zitadelOrgID, err := uc.provisioner.ProvisionTenant(ctx, org.ID, org.Name, org.OperatorEmail)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(otelcodes.Error, "organizer tenant provisioning failed")
		uc.metrics.RecordOrganizerProvisioning(ctx, "failed")
		return err
	}

	if err := uc.organizerRepo.SetZitadelOrgID(ctx, org.ID, zitadelOrgID); err != nil {
		return err
	}
	if err := uc.organizerRepo.SetStatus(ctx, org.ID, entity.OrganizerStatusActive); err != nil {
		return err
	}

	org.ZitadelOrgID = zitadelOrgID
	org.Status = entity.OrganizerStatusActive
	uc.metrics.RecordOrganizerProvisioning(ctx, "success")
	uc.logger.Info(ctx, "organizer tenant provisioned",
		slog.String("organizer_id", org.ID),
		slog.String("zitadel_org_id", zitadelOrgID),
	)
	return nil
}

func (uc *organizerUseCase) ReconcileProvisioning(ctx context.Context) error {
	stuck, err := uc.organizerRepo.ListByStatus(ctx, entity.OrganizerStatusProvisioning)
	if err != nil {
		return err
	}
	for _, org := range stuck {
		if err := uc.completeProvisioning(ctx, org); err != nil {
			uc.logger.Warn(ctx, "reconcile: organizer provisioning still incomplete",
				slog.String("organizer_id", org.ID),
				slog.Any("error", err),
			)
			continue
		}
		uc.logger.Info(ctx, "reconcile: organizer provisioning completed",
			slog.String("organizer_id", org.ID),
		)
	}
	return nil
}

func (uc *organizerUseCase) Get(ctx context.Context, id string) (*entity.Organizer, error) {
	return uc.organizerRepo.Get(ctx, id)
}

func (uc *organizerUseCase) List(ctx context.Context) ([]*entity.Organizer, error) {
	return uc.organizerRepo.List(ctx)
}

func (uc *organizerUseCase) ListArtists(ctx context.Context, organizerID string) ([]*entity.Artist, error) {
	if _, err := uc.organizerRepo.Get(ctx, organizerID); err != nil {
		return nil, err
	}
	return uc.organizerRepo.ListArtists(ctx, organizerID)
}

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
	return uc.organizerRepo.AssociateArtist(ctx, organizerID, artistID)
}

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

func (uc *organizerUseCase) Deactivate(ctx context.Context, organizerID string) error {
	org, err := uc.organizerRepo.Get(ctx, organizerID)
	if err != nil {
		return err
	}
	if org.Status == entity.OrganizerStatusDeactivated {
		return nil // idempotent
	}

	// Deactivate the tenant operators in Zitadel (skip if not yet provisioned).
	if org.ZitadelOrgID != "" {
		if err := uc.provisioner.DeactivateOperators(ctx, org.ZitadelOrgID); err != nil {
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
