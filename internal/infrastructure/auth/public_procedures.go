package auth

import (
	artistconnect "buf.build/gen/go/liverty-music/schema/connectrpc/go/liverty_music/rpc/artist/v1/artistv1connect"
	concertconnect "buf.build/gen/go/liverty-music/schema/connectrpc/go/liverty_music/rpc/concert/v1/concertv1connect"
)

// FanPublicProcedures returns the consumer (fan) Connect procedures that are
// accessible without authentication: read-only endpoints that return publicly
// available data (artist charts, concert schedules, public event pages) and
// are called during onboarding or by guests. Write endpoints remain fully
// authenticated. The admin server never uses this allowlist.
func FanPublicProcedures() map[string]bool {
	return map[string]bool{
		"/" + artistconnect.ArtistServiceName + "/ListTop":             true,
		"/" + artistconnect.ArtistServiceName + "/ListSimilar":         true,
		"/" + artistconnect.ArtistServiceName + "/Search":              true,
		"/" + concertconnect.ConcertServiceName + "/List":              true,
		"/" + concertconnect.ConcertServiceName + "/SearchNewConcerts": true,
		"/" + concertconnect.ConcertServiceName + "/ListByArtists":     true,
		"/" + concertconnect.ConcertServiceName + "/ListByLocation":    true,
		"/" + concertconnect.ConcertServiceName + "/Get":               true,
		"/" + concertconnect.ConcertServiceName + "/ListBySeries":      true,
	}
}
