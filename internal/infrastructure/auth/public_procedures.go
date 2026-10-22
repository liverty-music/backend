package auth

import (
	artistconnect "buf.build/gen/go/liverty-music/schema/connectrpc/go/v2/liverty_music/rpc/artist/v1/artistv1connect"
	concertconnect "buf.build/gen/go/liverty-music/schema/connectrpc/go/v2/liverty_music/rpc/concert/v1/concertv1connect"
	organizerconnect "buf.build/gen/go/liverty-music/schema/connectrpc/go/v2/liverty_music/rpc/organizer/v1/organizerv1connect"
	scannerconnect "buf.build/gen/go/liverty-music/schema/connectrpc/go/v2/liverty_music/rpc/scanner/v1/scannerv1connect"
	ticketsaleconnect "buf.build/gen/go/liverty-music/schema/connectrpc/go/v2/liverty_music/rpc/ticket_sale/v1/ticket_salev1connect"
	userconnect "buf.build/gen/go/liverty-music/schema/connectrpc/go/v2/liverty_music/rpc/user/v1/userv1connect"
)

// FanPublicProcedures returns the consumer (fan) Connect procedures that are
// accessible without authentication: read-only endpoints that return publicly
// available data (artist charts, concert schedules, public event pages) and
// are called during onboarding or by guests, and UserService.RequestPasskeyRecovery,
// which serves people who cannot sign in and is limited per client
// ([FanClientLimitedProcedures]). Other write endpoints remain fully
// authenticated. The admin server never uses this allowlist.
func FanPublicProcedures() map[string]bool {
	return map[string]bool{
		"/" + artistconnect.ArtistServiceName + "/ListTop":          true,
		"/" + artistconnect.ArtistServiceName + "/ListSimilar":      true,
		"/" + artistconnect.ArtistServiceName + "/Search":           true,
		"/" + concertconnect.ConcertServiceName + "/ListByArtist":   true,
		"/" + concertconnect.ConcertServiceName + "/ListByArtists":  true,
		"/" + concertconnect.ConcertServiceName + "/ListByLocation": true,
		"/" + concertconnect.ConcertServiceName + "/Get":            true,
		"/" + concertconnect.ConcertServiceName + "/ListBySeries":   true,
		ticketsaleconnect.TicketSaleServiceListByEventProcedure:     true,
		userconnect.UserServiceRequestPasskeyRecoveryProcedure:      true,
	}
}

// FanClientLimitedProcedures returns the fan Connect procedures whose calls
// are limited per client: UserService.RequestPasskeyRecovery, which anyone
// can call without a sign-in and which announces a recovery request.
func FanClientLimitedProcedures() map[string]bool {
	return map[string]bool{
		userconnect.UserServiceRequestPasskeyRecoveryProcedure: true,
	}
}

// ReceptionPublicProcedures returns the reception-server Connect procedures,
// all of which are called without a sign-in: the device-facing ScannerService
// (rpc.scanner.v1) a venue staff device calls. Its caller is identified by a link token and
// the bound device's signature, checked by the use cases, so the authn
// middleware lets these calls through without a token.
func ReceptionPublicProcedures() map[string]bool {
	return map[string]bool{
		scannerconnect.ScannerServiceOpenProcedure:  true,
		scannerconnect.ScannerServiceAdmitProcedure: true,
	}
}

// OrganizerPublicProcedures returns the organizer-server Connect procedures
// that are called without a sign-in: only OrganizerService.SendSignInLinks,
// which a person on a device that knows no Organizer calls to receive their
// sign-in links by email. Every other organizer procedure requires an
// organizer-console sign-in. The OrgScopedInterceptor skips these
// procedures, and the organizer server limits calls to them per client.
func OrganizerPublicProcedures() map[string]bool {
	return map[string]bool{
		organizerconnect.OrganizerServiceSendSignInLinksProcedure: true,
	}
}
