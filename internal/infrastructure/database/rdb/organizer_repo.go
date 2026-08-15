package rdb

import (
	"context"
	"log/slog"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
)

// OrganizerRepository implements entity.OrganizerRepository for PostgreSQL.
type OrganizerRepository struct {
	db *Database
}

// NewOrganizerRepository creates a new PostgreSQL-backed OrganizerRepository.
func NewOrganizerRepository(db *Database) *OrganizerRepository {
	return &OrganizerRepository{db: db}
}

const (
	insertOrganizerQuery = `
		INSERT INTO organizers (id, name, operator_email, zitadel_org_id, status)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, name, operator_email, zitadel_org_id, status
	`
	getOrganizerQuery = `
		SELECT id, name, operator_email, zitadel_org_id, status FROM organizers WHERE id = $1
	`
	listOrganizersQuery = `
		SELECT id, name, operator_email, zitadel_org_id, status FROM organizers ORDER BY name
	`
	listOrganizersByStatusQuery = `
		SELECT id, name, operator_email, zitadel_org_id, status FROM organizers WHERE status = $1 ORDER BY name
	`
	setOrganizerZitadelOrgIDQuery = `
		UPDATE organizers SET zitadel_org_id = $2 WHERE id = $1
	`
	setOrganizerStatusQuery = `
		UPDATE organizers SET status = $2 WHERE id = $1
	`
	insertOrganizerArtistQuery = `
		INSERT INTO organizer_artists (organizer_id, artist_id) VALUES ($1, $2)
	`
	deleteOrganizerArtistQuery = `
		DELETE FROM organizer_artists WHERE organizer_id = $1 AND artist_id = $2
	`
	listOrganizerArtistsQuery = `
		SELECT a.id, a.name, a.mbid, a.fanart, a.fanart_synced_at
		FROM artists a
		JOIN organizer_artists oa ON oa.artist_id = a.id
		WHERE oa.organizer_id = $1
		ORDER BY a.name
	`
	deleteOrganizerArtistsQuery = `
		DELETE FROM organizer_artists WHERE organizer_id = $1
	`
)

// scanOrganizer extracts an Organizer from a pgx row.
func scanOrganizer(scan func(dest ...any) error) (*entity.Organizer, error) {
	var o entity.Organizer
	var zitadelOrgID *string
	var status int16

	if err := scan(&o.ID, &o.Name, &o.OperatorEmail, &zitadelOrgID, &status); err != nil {
		return nil, err
	}
	if zitadelOrgID != nil {
		o.ZitadelOrgID = *zitadelOrgID
	}
	o.Status = entity.OrganizerStatus(status)
	return &o, nil
}

// Create inserts a new Organizer row and returns the persisted record. The ID
// is generated from the entity if absent. zitadel_org_id is stored as NULL
// until provisioning completes, satisfying the partial unique index that only
// covers non-NULL values.
func (r *OrganizerRepository) Create(ctx context.Context, o *entity.Organizer) (*entity.Organizer, error) {
	if o.ID == "" {
		o.ID = entity.NewOrganizer(o.Name).ID
	}

	// Keep zitadel_org_id NULL until provisioning persists it (the partial unique
	// index only indexes non-NULL values).
	var zitadelOrgID *string
	if o.ZitadelOrgID != "" {
		zitadelOrgID = &o.ZitadelOrgID
	}

	row := r.db.Pool.QueryRow(ctx, insertOrganizerQuery, o.ID, o.Name, o.OperatorEmail, zitadelOrgID, int16(o.Status))
	created, err := scanOrganizer(row.Scan)
	if err != nil {
		return nil, toAppErr(err, "failed to insert organizer", slog.String("id", o.ID))
	}

	r.db.logger.Info(ctx, "organizer created", slog.String("entityType", "organizer"), slog.String("id", created.ID))
	return created, nil
}

// Get retrieves a single Organizer by its id. Returns NotFound when no row
// matches.
func (r *OrganizerRepository) Get(ctx context.Context, id string) (*entity.Organizer, error) {
	row := r.db.Pool.QueryRow(ctx, getOrganizerQuery, id)
	o, err := scanOrganizer(row.Scan)
	if err != nil {
		return nil, toAppErr(err, "failed to get organizer", slog.String("id", id))
	}
	return o, nil
}

// List returns all Organizers ordered alphabetically by name.
func (r *OrganizerRepository) List(ctx context.Context) ([]*entity.Organizer, error) {
	rows, err := r.db.Pool.Query(ctx, listOrganizersQuery)
	if err != nil {
		return nil, toAppErr(err, "failed to list organizers")
	}
	defer rows.Close()

	var organizers []*entity.Organizer
	for rows.Next() {
		o, err := scanOrganizer(rows.Scan)
		if err != nil {
			return nil, toAppErr(err, "failed to scan organizer")
		}
		organizers = append(organizers, o)
	}
	if err := rows.Err(); err != nil {
		return nil, toAppErr(err, "failed to iterate organizers")
	}
	return organizers, nil
}

// ListByStatus returns all Organizers whose status matches the given value,
// ordered alphabetically by name. Used by the reconciler to find rows stuck
// in the provisioning state.
func (r *OrganizerRepository) ListByStatus(ctx context.Context, status entity.OrganizerStatus) ([]*entity.Organizer, error) {
	rows, err := r.db.Pool.Query(ctx, listOrganizersByStatusQuery, int16(status))
	if err != nil {
		return nil, toAppErr(err, "failed to list organizers by status", slog.Int("status", int(status)))
	}
	defer rows.Close()

	var organizers []*entity.Organizer
	for rows.Next() {
		o, err := scanOrganizer(rows.Scan)
		if err != nil {
			return nil, toAppErr(err, "failed to scan organizer")
		}
		organizers = append(organizers, o)
	}
	if err := rows.Err(); err != nil {
		return nil, toAppErr(err, "failed to iterate organizers by status")
	}
	return organizers, nil
}

// SetZitadelOrgID persists the Zitadel org id on the Organizer row after
// successful tenant provisioning. Returns NotFound when the organizer no
// longer exists.
func (r *OrganizerRepository) SetZitadelOrgID(ctx context.Context, id, zitadelOrgID string) error {
	tag, err := r.db.Pool.Exec(ctx, setOrganizerZitadelOrgIDQuery, id, zitadelOrgID)
	if err != nil {
		return toAppErr(err, "failed to set organizer zitadel_org_id", slog.String("id", id))
	}
	if tag.RowsAffected() == 0 {
		return apperr.New(codes.NotFound, "organizer not found")
	}
	return nil
}

// SetStatus updates the lifecycle status of an Organizer. Returns NotFound
// when the organizer no longer exists.
func (r *OrganizerRepository) SetStatus(ctx context.Context, id string, status entity.OrganizerStatus) error {
	tag, err := r.db.Pool.Exec(ctx, setOrganizerStatusQuery, id, int16(status))
	if err != nil {
		return toAppErr(err, "failed to set organizer status", slog.String("id", id))
	}
	if tag.RowsAffected() == 0 {
		return apperr.New(codes.NotFound, "organizer not found")
	}
	return nil
}

// AssociateArtist inserts a row into organizer_artists to link an artist to an
// Organizer. The unique index on artist_id ensures an artist can only be
// represented by one organizer at a time; a duplicate insert is mapped to
// AlreadyExists by toAppErr.
func (r *OrganizerRepository) AssociateArtist(ctx context.Context, organizerID, artistID string) error {
	// The unique index on artist_id rejects an already-represented artist with a
	// unique violation, which toAppErr maps to AlreadyExists.
	if _, err := r.db.Pool.Exec(ctx, insertOrganizerArtistQuery, organizerID, artistID); err != nil {
		return toAppErr(err, "failed to associate artist",
			slog.String("organizer_id", organizerID), slog.String("artist_id", artistID))
	}
	return nil
}

// DisassociateArtist removes the link between an Organizer and an artist.
// The operation is idempotent: removing a non-existent association affects
// zero rows and succeeds without error.
func (r *OrganizerRepository) DisassociateArtist(ctx context.Context, organizerID, artistID string) error {
	// Idempotent: removing a non-existent association affects zero rows and succeeds.
	if _, err := r.db.Pool.Exec(ctx, deleteOrganizerArtistQuery, organizerID, artistID); err != nil {
		return toAppErr(err, "failed to disassociate artist",
			slog.String("organizer_id", organizerID), slog.String("artist_id", artistID))
	}
	return nil
}

// ListArtists returns the artists linked to an Organizer via organizer_artists,
// ordered alphabetically by name.
func (r *OrganizerRepository) ListArtists(ctx context.Context, organizerID string) ([]*entity.Artist, error) {
	rows, err := r.db.Pool.Query(ctx, listOrganizerArtistsQuery, organizerID)
	if err != nil {
		return nil, toAppErr(err, "failed to list organizer artists", slog.String("organizer_id", organizerID))
	}
	defer rows.Close()

	var artists []*entity.Artist
	for rows.Next() {
		a, err := scanArtist(rows.Scan)
		if err != nil {
			return nil, toAppErr(err, "failed to scan artist")
		}
		artists = append(artists, a)
	}
	if err := rows.Err(); err != nil {
		return nil, toAppErr(err, "failed to iterate organizer artists")
	}
	return artists, nil
}

// FreeArtists removes all artist associations for an Organizer so those
// artists can be re-associated with another organizer. Called during
// Deactivate to release the organizer's exclusive hold on each artist.
func (r *OrganizerRepository) FreeArtists(ctx context.Context, organizerID string) error {
	if _, err := r.db.Pool.Exec(ctx, deleteOrganizerArtistsQuery, organizerID); err != nil {
		return toAppErr(err, "failed to free organizer artists", slog.String("organizer_id", organizerID))
	}
	return nil
}
