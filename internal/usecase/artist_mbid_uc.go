package usecase

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/pannpers/go-apperr/apperr"
)

// mbidDecision is what decideMBID concluded about an artist's MBID.
type mbidDecision struct {
	// status is Verified or Ambiguous.
	status entity.MBIDStatus
	// mbid is the verified MBID: the canonical MBID the catalog returned for
	// the chosen one. Empty when Ambiguous.
	mbid string
	// name and activityEnded are the catalog's name (the registered name when
	// the catalog gives none) and activity status for mbid.
	name          string
	activityEnded bool
	// candidates are the MBIDs that were compared: the artist's own and its
	// namesakes'. Logged with an Ambiguous decision.
	candidates []string
	// reason says why the MBID is Ambiguous.
	reason string
}

// ambiguous returns the Ambiguous decision for the given candidates.
func ambiguous(candidates []string, reason string) mbidDecision {
	return mbidDecision{status: entity.MBIDStatusAmbiguous, candidates: candidates, reason: reason}
}

// decideMBID decides the MBID of an artist with the given name and MBID from
// the music catalog's artists of the same name (components/usecase/artist/
// verify-mbid): with no other namesake the MBID is verified; otherwise the one
// candidate (its own MBID included) with an official homepage is verified,
// and with none or several the MBID is ambiguous. The artist's own MBID is
// compared in the canonical form the catalog returns for it, so a merged MBID
// is not its own namesake. A verified MBID comes with the catalog's name and
// activity status; an MBID to verify that the catalog does not know makes the
// MBID ambiguous. It stores nothing, and it returns
// the error of the first catalog read that fails, so nothing is decided on
// partial reads.
func (uc *artistUseCase) decideMBID(ctx context.Context, name, mbid string) (mbidDecision, error) {
	own, canonical, err := uc.canonicalMBID(ctx, mbid)
	if err != nil {
		return mbidDecision{}, err
	}

	namesakes, err := uc.namesakes.ListNamesakes(ctx, name)
	if err != nil {
		return mbidDecision{}, fmt.Errorf("list namesakes of %q: %w", name, err)
	}
	candidates := []string{canonical}
	for _, n := range namesakes {
		if n.MBID != canonical && n.MBID != mbid {
			candidates = append(candidates, n.MBID)
		}
	}

	chosen := canonical
	catalog := own
	if len(candidates) > 1 {
		withSite, err := uc.withOfficialSite(ctx, candidates)
		if err != nil {
			return mbidDecision{}, err
		}
		if len(withSite) != 1 {
			return ambiguous(candidates, fmt.Sprintf("%d of the %d artists of the name have an official homepage", len(withSite), len(candidates))), nil
		}
		chosen = withSite[0]
		if chosen != canonical {
			if catalog, err = uc.catalogArtist(ctx, chosen); err != nil {
				return mbidDecision{}, err
			}
		}
	}
	if catalog == nil {
		// An MBID the catalog does not know needs a person: an Unverified
		// artist always comes from the listening service.
		return ambiguous(candidates, "the catalog does not know MBID "+chosen), nil
	}
	return verified(catalog, chosen, name, candidates), nil
}

// canonicalMBID reads the artist's own MBID from the catalog. The catalog
// answers a merged MBID with the surviving artist, and its name search lists
// only the survivor, so the own MBID is compared in that canonical form: the
// survivor is the artist itself, not a namesake. own is nil, and canonical the
// given MBID, when the catalog does not know it.
func (uc *artistUseCase) canonicalMBID(ctx context.Context, mbid string) (own *entity.Artist, canonical string, err error) {
	own, err = uc.catalogArtist(ctx, mbid)
	if err != nil {
		return nil, "", err
	}
	if own != nil && own.MBID != "" {
		return own, own.MBID, nil
	}
	return own, mbid, nil
}

// catalogArtist reads an MBID from the catalog, or returns nil when the
// catalog does not know it.
func (uc *artistUseCase) catalogArtist(ctx context.Context, mbid string) (*entity.Artist, error) {
	artist, err := uc.idManager.GetArtist(ctx, mbid)
	switch {
	case errors.Is(err, apperr.ErrNotFound):
		return nil, nil
	case err != nil:
		return nil, fmt.Errorf("get artist %s from the catalog: %w", mbid, err)
	}
	return artist, nil
}

// withOfficialSite returns the candidates the catalog records an official
// homepage for, in order.
func (uc *artistUseCase) withOfficialSite(ctx context.Context, candidates []string) ([]string, error) {
	var withSite []string
	for _, candidate := range candidates {
		site, err := officialSiteOf(ctx, uc.officialSites, candidate)
		if err != nil {
			return nil, err
		}
		if site != "" {
			withSite = append(withSite, candidate)
		}
	}
	return withSite, nil
}

// verified is the Verified decision for the catalog's record of the chosen
// MBID, falling back to the chosen MBID and the registered name where the
// record has none.
func verified(catalog *entity.Artist, chosen, name string, candidates []string) mbidDecision {
	decided := mbidDecision{
		status:        entity.MBIDStatusVerified,
		mbid:          catalog.MBID,
		name:          catalog.Name,
		activityEnded: catalog.ActivityEnded,
		candidates:    candidates,
	}
	if decided.mbid == "" {
		decided.mbid = chosen
	}
	if decided.name == "" {
		decided.name = name
	}
	return decided
}

// officialSiteOf returns the official homepage the catalog records for the
// MBID, chosen with entity.SelectOfficialSite, or "" when it has none or does
// not know the MBID.
func officialSiteOf(ctx context.Context, catalog entity.OfficialSiteCatalog, mbid string) (string, error) {
	rels, err := catalog.ListOfficialSiteRelations(ctx, mbid)
	switch {
	case errors.Is(err, apperr.ErrNotFound):
		return "", nil
	case err != nil:
		return "", fmt.Errorf("list official site relations of %s: %w", mbid, err)
	}
	return entity.SelectOfficialSite(*rels), nil
}

// logAmbiguous logs an Ambiguous decision as an error, which alerts the
// owner, naming the artist, the compared MBIDs and the reason.
func (uc *artistUseCase) logAmbiguous(ctx context.Context, artistID, name string, d mbidDecision) {
	uc.logger.Error(ctx, "artist MBID is ambiguous", nil,
		slog.String("artist_id", artistID),
		slog.String("artist_name", name),
		slog.Any("candidate_mbids", d.candidates),
		slog.String("reason", d.reason),
	)
}

// VerifyMBID implements [ArtistUseCase].
func (uc *artistUseCase) VerifyMBID(ctx context.Context, artistID string) error {
	_, err := uc.verifyMBID(ctx, artistID)
	return err
}

// verifyMBID verifies an Unverified artist's MBID and returns the status it
// left the artist in (the artist's own status when it was not Unverified).
func (uc *artistUseCase) verifyMBID(ctx context.Context, artistID string) (entity.MBIDStatus, error) {
	artist, err := uc.artistRepo.Get(ctx, artistID)
	if err != nil {
		return "", err
	}
	if artist.MBIDStatus != entity.MBIDStatusUnverified {
		return artist.MBIDStatus, nil
	}

	decision, err := uc.decideMBID(ctx, artist.Name, artist.MBID)
	if err != nil {
		return "", err
	}
	if decision.status == entity.MBIDStatusVerified {
		err := uc.artistRepo.UpdateMBID(ctx, artistID, decision.mbid, decision.name, decision.activityEnded)
		switch {
		case errors.Is(err, apperr.ErrAlreadyExists):
			decision = ambiguous(decision.candidates, "another artist holds MBID "+decision.mbid)
		case err != nil:
			return "", err
		default:
			uc.logger.Info(ctx, "artist MBID verified",
				slog.String("artist_id", artistID),
				slog.String("mbid", decision.mbid),
				slog.Bool("corrected", decision.mbid != artist.MBID),
			)
			return entity.MBIDStatusVerified, nil
		}
	}

	if err := uc.artistRepo.MarkMBIDAmbiguous(ctx, artistID); err != nil {
		return "", err
	}
	uc.logAmbiguous(ctx, artistID, artist.Name, decision)
	return entity.MBIDStatusAmbiguous, nil
}

// ListByMBIDStatus implements [ArtistUseCase].
func (uc *artistUseCase) ListByMBIDStatus(ctx context.Context, status entity.MBIDStatus) ([]*entity.Artist, error) {
	return uc.artistRepo.ListByMBIDStatus(ctx, status)
}
