package auth

import (
	artistconnect "buf.build/gen/go/liverty-music/schema/connectrpc/go/liverty_music/rpc/artist/v1/artistv1connect"
	concertconnect "buf.build/gen/go/liverty-music/schema/connectrpc/go/liverty_music/rpc/concert/v1/concertv1connect"
	receptionconnect "buf.build/gen/go/liverty-music/schema/connectrpc/go/liverty_music/rpc/organizer/reception/v1/receptionv1connect"
	ticketsaleconnect "buf.build/gen/go/liverty-music/schema/connectrpc/go/liverty_music/rpc/ticket_sale/v1/ticket_salev1connect"
)

// FanPublicProcedures returns the consumer (fan) Connect procedures that are
// accessible without authentication: read-only endpoints that return publicly
// available data (artist charts, concert schedules, public event pages) and
// are called during onboarding or by guests. Write endpoints remain fully
// authenticated. The admin server never uses this allowlist.
func FanPublicProcedures() map[string]bool {
	return map[string]bool{
		"/" + artistconnect.ArtistServiceName + "/ListTop":          true,
		"/" + artistconnect.ArtistServiceName + "/ListSimilar":      true,
		"/" + artistconnect.ArtistServiceName + "/Search":           true,
		"/" + concertconnect.ConcertServiceName + "/List":           true,
		"/" + concertconnect.ConcertServiceName + "/ListByArtists":  true,
		"/" + concertconnect.ConcertServiceName + "/ListByLocation": true,
		"/" + concertconnect.ConcertServiceName + "/Get":            true,
		"/" + concertconnect.ConcertServiceName + "/ListBySeries":   true,
		ticketsaleconnect.TicketSaleServiceGetProcedure:             true,
	}
}

// ReceptionPublicProcedures returns the reception-server Connect procedures,
// all of which are called without a sign-in: the ReceptionService a venue
// staff device calls. Its caller is identified by a reception link token and
// the bound device's signature, checked by the use cases, so the authn
// middleware lets these calls through without a token. The organizer server
// has no public procedures.
func ReceptionPublicProcedures() map[string]bool {
	return map[string]bool{
		receptionconnect.ReceptionServiceOpenProcedure:  true,
		receptionconnect.ReceptionServiceAdmitProcedure: true,
	}
}
