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
// and with none or several the MBID is ambiguous. A verified MBID comes with
// the catalog's name and activity status; an MBID to verify that the catalog
// does not know makes the MBID ambiguous. It stores nothing, and it returns
// the error of the first catalog read that fails, so nothing is decided on
// partial reads.
func (uc *artistUseCase) decideMBID(ctx context.Context, name, mbid string) (mbidDecision, error) {
	namesakes, err := uc.namesakes.ListNamesakes(ctx, name)
	if err != nil {
		return mbidDecision{}, fmt.Errorf("list namesakes of %q: %w", name, err)
	}
	candidates := []string{mbid}
	for _, n := range namesakes {
		if n.MBID != mbid {
			candidates = append(candidates, n.MBID)
		}
	}

	chosen := mbid
	if len(candidates) > 1 {
		var withSite []string
		for _, candidate := range candidates {
			site, err := officialSiteOf(ctx, uc.officialSites, candidate)
			if err != nil {
				return mbidDecision{}, err
			}
			if site != "" {
				withSite = append(withSite, candidate)
			}
		}
		if len(withSite) != 1 {
			return ambiguous(candidates, fmt.Sprintf("%d of the %d artists of the name have an official homepage", len(withSite), len(candidates))), nil
		}
		chosen = withSite[0]
	}

	catalog, err := uc.idManager.GetArtist(ctx, chosen)
	switch {
	case errors.Is(err, apperr.ErrNotFound):
		// An MBID the catalog does not know needs a person: an Unverified
		// artist always comes from the listening service.
		return ambiguous(candidates, "the catalog does not know MBID "+chosen), nil
	case err != nil:
		return mbidDecision{}, fmt.Errorf("get artist %s from the catalog: %w", chosen, err)
	}
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
	return decided, nil
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
