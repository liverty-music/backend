// Package pocketsign provides the Pocket Sign Verify API integration for the
// identity eKYC flow. The real implementation is pending vendor onboarding
// (identity-ekyc-jpki Section 0).
//
// TODO: integrate real Pocket Sign Verify SDK/API after onboarding
// (identity-ekyc-jpki Section 0).
package pocketsign

import (
	"context"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
)

// Compile-time assertion that StubVerifier satisfies usecase.PocketSignVerifier.
var _ usecase.PocketSignVerifier = (*StubVerifier)(nil)

// StubVerifier is a placeholder implementation of PocketSignVerifier used
// when the real Pocket Sign Verify API is not yet configured (pre-onboarding).
// It returns a clear "not configured" error on every call so callers get an
// explicit UNAVAILABLE rather than a panic or silent no-op.
//
// TODO: replace this with a real Pocket Sign Verify API client after
// onboarding (identity-ekyc-jpki Section 0 — 加盟契約 + sandbox credentials).
type StubVerifier struct{}

// NewStubVerifier creates a StubVerifier.
func NewStubVerifier() *StubVerifier {
	return &StubVerifier{}
}

// IssueChallenge returns UNAVAILABLE because Pocket Sign is not yet configured.
//
// TODO: integrate real Pocket Sign Verify SDK/API after onboarding
// (identity-ekyc-jpki Section 0).
func (s *StubVerifier) IssueChallenge(_ context.Context, _ entity.VerificationMethod) (*usecase.PocketSignChallenge, error) {
	return nil, apperr.New(codes.Unavailable,
		"Pocket Sign Verify API is not configured; "+
			"onboarding required before identity verification is available "+
			"(identity-ekyc-jpki Section 0)")
}

// ValidateResponse returns UNAVAILABLE because Pocket Sign is not yet configured.
//
// TODO: integrate real Pocket Sign Verify SDK/API after onboarding
// (identity-ekyc-jpki Section 0).
func (s *StubVerifier) ValidateResponse(_ context.Context, _ string, _ []byte) (*usecase.PocketSignResult, error) {
	return nil, apperr.New(codes.Unavailable,
		"Pocket Sign Verify API is not configured; "+
			"onboarding required before identity verification is available "+
			"(identity-ekyc-jpki Section 0)")
}

// Recheck returns UNAVAILABLE because Pocket Sign is not yet configured.
//
// TODO: integrate real Pocket Sign Verify SDK/API after onboarding
// (identity-ekyc-jpki Section 0).
func (s *StubVerifier) Recheck(_ context.Context, _ string) (*usecase.PocketSignRecheckResult, error) {
	return nil, apperr.New(codes.Unavailable,
		"Pocket Sign Verify API is not configured; "+
			"onboarding required before identity verification is available "+
			"(identity-ekyc-jpki Section 0)")
}
