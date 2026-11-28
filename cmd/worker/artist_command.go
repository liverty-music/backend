package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/liverty-music/backend/internal/di"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/liverty-music/backend/pkg/shutdown"
)

const artistUsage = `usage: worker artist <command> [arguments]

Runs in the workflow worker's Pod, as its database identity:
  kubectl exec deploy/workflow-worker -n backend -- /worker artist <command>

commands:
  correct-mbid ARTIST_ID MBID   give an artist registered with a namesake's MBID the MBID of the
                                right person; the replaced MBID stays, so a search that still
                                returns it finds this artist

See cloud-provisioning docs/runbooks/correct-artist-mbid.md.
`

// artistCommand runs a parsed subcommand against the artist use case.
type artistCommand func(context.Context, usecase.ArtistUseCase) (any, error)

// runArtistCommand runs one `artist` subcommand, prints its result as JSON to
// stdout and returns the process exit code.
func runArtistCommand(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		_, _ = fmt.Fprint(stderr, artistUsage)
		return 2
	}
	// Parse before anything connects, so a typo fails at once.
	run, err := parseArtistCommand(args)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%v\n\n%s", err, artistUsage)
		return 2
	}

	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()

	artistUC, _, err := di.InitializeArtistAdmin(ctx)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "connect to the artist tables: %v\n", err)
		return 1
	}
	defer func() { _ = shutdown.Shutdown(context.Background()) }()

	result, err := run(ctx, artistUC)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%v\n", err)
		return 1
	}
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(result); err != nil {
		_, _ = fmt.Fprintf(stderr, "print the result: %v\n", err)
		return 1
	}
	return 0
}

// parseArtistCommand parses args into the command to run.
func parseArtistCommand(args []string) (artistCommand, error) {
	name, rest := args[0], args[1:]
	switch name {
	case "correct-mbid":
		if len(rest) != 2 {
			return nil, fmt.Errorf("correct-mbid needs an artist id and an MBID")
		}
		artistID, mbid := rest[0], rest[1]
		return func(ctx context.Context, uc usecase.ArtistUseCase) (any, error) {
			artist, err := uc.CorrectMBID(ctx, artistID, mbid)
			if err != nil {
				return nil, err
			}
			return map[string]any{
				"artist_id":      artist.ID,
				"name":           artist.Name,
				"mbid":           artist.MBID,
				"other_mbids":    artist.OtherMBIDs,
				"activity_ended": artist.ActivityEnded,
			}, nil
		}, nil
	default:
		return nil, fmt.Errorf("unknown command %q", name)
	}
}
