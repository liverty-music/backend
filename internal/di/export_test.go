package di

import (
	"context"
	"io"
)

// NewBootstrapLoggerTo exposes newBootstrapLogger for testing.
var NewBootstrapLoggerTo = newBootstrapLogger

// ProvideLogger exposes provideLogger for testing.
var ProvideLogger = provideLogger

// NewPasskeyRegistrationLinkSender exposes newPasskeyRegistrationLinkSender for testing.
var NewPasskeyRegistrationLinkSender = newPasskeyRegistrationLinkSender

// PasskeyRecoveryBehaviorNames returns the behavior names passkeyRecoveryBehaviors
// registers for enabled.
func PasskeyRecoveryBehaviorNames(enabled bool) []string {
	var names []string
	for _, e := range passkeyRecoveryBehaviors(context.Background(), enabled, nil, newBootstrapLogger(io.Discard)) {
		names = append(names, e.behavior+" "+e.subject)
	}
	return names
}

// ProvideIdentityClients exposes provideIdentityClients for testing.
var ProvideIdentityClients = provideIdentityClients

// NewConsumerEmailVerifier exposes newConsumerEmailVerifier for testing.
var NewConsumerEmailVerifier = newConsumerEmailVerifier

// ProvideOrganizationRepository exposes provideOrganizationRepository for testing.
var ProvideOrganizationRepository = provideOrganizationRepository
