package entity

import (
	"context"
	"time"
)

// ZeroAAGUID is the AAGUID of a passkey whose provider does not reveal
// itself (e.g. iCloud Keychain outside managed devices).
const ZeroAAGUID = "00000000-0000-0000-0000-000000000000"

// Passkey is the platform's record of one passkey registered for a User at
// the identity provider: which passkey it is, which passkey provider made it
// and when it was registered. The identity provider remains the authority on
// which passkeys exist and sign in.
//
// Corresponds to liverty_music.entity.v1.Passkey.
type Passkey struct {
	// ProviderPasskeyID is the identity provider's id for the passkey
	// (Zitadel webAuthNTokenId); at most one Passkey per value.
	ProviderPasskeyID string
	// UserID is the User the passkey signs in to.
	UserID string
	// AAGUID identifies the passkey provider model that made the passkey, in
	// canonical lower-case UUID form; [ZeroAAGUID] when the provider does not
	// reveal itself.
	AAGUID string
	// CreateTime is when the passkey finished registration.
	CreateTime time.Time
}

// ProviderName returns the name the passkey provider list gives the
// passkey's AAGUID (e.g. "Google Password Manager"), or "" when the AAGUID is
// all zeros or not in the list.
func (p *Passkey) ProviderName() string {
	return passkeyProviderNames[p.AAGUID]
}

// PasskeyRepository stores the platform's Passkey records.
type PasskeyRepository interface {
	// Create stores the Passkey. When a Passkey with the same provider
	// passkey id already exists, it succeeds and leaves the stored one
	// unchanged.
	//
	// # Possible errors
	//
	//  - InvalidArgument: a required attribute is missing or the AAGUID is
	//    not a UUID.
	//  - NotFound: the User does not exist; nothing is stored.
	Create(ctx context.Context, passkey *Passkey) error

	// Delete removes the Passkey with the given provider passkey id, and
	// succeeds without change when none exists.
	//
	// # Possible errors
	//
	//  - InvalidArgument: the provider passkey id is empty.
	Delete(ctx context.Context, providerPasskeyID string) error

	// ListByUser returns every Passkey of the User, newest create time
	// first; an empty list when it has none.
	//
	// # Possible errors
	//
	//  - InvalidArgument: the user id is empty.
	ListByUser(ctx context.Context, userID string) ([]*Passkey, error)
}
