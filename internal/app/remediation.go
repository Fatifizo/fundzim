package app

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Fatifizo/fundzim/internal/auth"
	"github.com/Fatifizo/fundzim/internal/platform/httpx"
	"github.com/Fatifizo/fundzim/internal/platform/ids"
	"github.com/Fatifizo/fundzim/internal/platform/outbox"
)

// Stage 6 stream R wiring (ADR-037): age attestation and BASIC_VERIFIED, staff <-> personal links. Restriction
// maintenance and the campaigns.review_escalated case trigger live in the compliance module and are wired with
// its other consumers (m.Compliance.Consumers()).

// registerRemediationRoutes registers the age-attestation and staff-link routes.
func registerRemediationRoutes(r *httpx.Router, a *auth.Service, m *VerificationModules) {
	if m != nil {
		r.HandleFunc("GET /api/v1/me/age-attestation", httpx.User(), m.HTTP.GetAgeAttestation)
		r.HandleFunc("POST /api/v1/me/age-attestation", httpx.User(), m.HTTP.PostAgeAttestation)
	}
	if a != nil {
		// staff session; the handler requires a fresh step-up for the request (no permission: it is the staff
		// member's own account)
		r.HandleFunc("POST /api/v1/admin/me/personal-account-link", httpx.Staff(), a.RequestStaffLink)
		r.HandleFunc("GET /api/v1/admin/me/personal-account-link", httpx.Staff(), a.GetStaffLink)
		r.HandleFunc("POST /api/v1/me/staff-link/confirm", httpx.User(), a.ConfirmStaffLink)
	}
}

// Consumer names (dedupe keys; never rename).
const (
	consumerEvaluateBasic           = "kyc.evaluate_basic"
	consumerRegistrationAttestation = "kyc.registration_age_attestation"
)

// basicTriggers are the identity events after which BASIC_VERIFIED is re-evaluated (email or phone verified,
// account suspended or reactivated). Attestations re-evaluate inside the kyc transaction itself.
var basicTriggers = []string{auth.EvEmailVerified, auth.EvPhoneVerified, auth.EvAccountSuspended, auth.EvAccountReactivated}

// registerRemediationConsumers subscribes the kyc consumers of identity events (worker). auth cannot import
// kyc (design-baseline §4), so the facts travel through the outbox.
func registerRemediationConsumers(reg *outbox.Registry, m *VerificationModules) {
	userOf := func(del outbox.Delivery) (string, error) {
		var p struct {
			UserID string `json:"user_id"`
		}
		if err := json.Unmarshal(del.Payload, &p); err != nil || !ids.Valid(p.UserID) {
			return "", fmt.Errorf("%s: malformed payload", del.EventType)
		}
		return p.UserID, nil
	}
	for _, ev := range basicTriggers {
		reg.Subscribe(consumerEvaluateBasic, ev, func(ctx context.Context, del outbox.Delivery) error {
			uid, err := userOf(del)
			if err != nil {
				return err
			}
			_, err = m.KYC.EvaluateBasic(ctx, uid)
			return err
		})
	}
	reg.Subscribe(consumerRegistrationAttestation, auth.EvRegistrationAgeAttested, func(ctx context.Context, del outbox.Delivery) error {
		uid, err := userOf(del)
		if err != nil {
			return err
		}
		return m.KYC.RecordRegistrationAttestation(ctx, del.EventID, uid, del.OccurredAt)
	})
}
