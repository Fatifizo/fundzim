package app

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Fatifizo/fundzim/internal/campaigns"
	"github.com/Fatifizo/fundzim/internal/compliance"
	"github.com/Fatifizo/fundzim/internal/kyc"
	"github.com/Fatifizo/fundzim/internal/platform/featureflags"
	"github.com/Fatifizo/fundzim/internal/platform/httpx"
	"github.com/Fatifizo/fundzim/internal/platform/idempotency"
	"github.com/Fatifizo/fundzim/internal/platform/ids"
	"github.com/Fatifizo/fundzim/internal/platform/outbox"
)

// Stage 6 campaign engine wiring (ADR-036). The core campaigns service reads restrictions, age attestation
// and feature flags through small adapters so it depends on the owning modules' public APIs only.

type campaignRestrictions struct{ c *compliance.Service }

func (a campaignRestrictions) Levels(ctx context.Context, subjects []campaigns.Subject) (map[campaigns.Subject]string, error) {
	in := make([]compliance.Subject, 0, len(subjects))
	for _, s := range subjects {
		in = append(in, compliance.Subject{Type: s.Type, ID: s.ID})
	}
	got, err := a.c.Restrictions(ctx, in)
	if err != nil {
		return nil, err
	}
	out := make(map[campaigns.Subject]string, len(got))
	for s, l := range got {
		out[campaigns.Subject{Type: s.Type, ID: s.ID}] = string(l)
	}
	return out, nil
}

type campaignAge struct{ k *kyc.Service }

// Attested: a current ATTESTED self-declaration for at least the policy age, or a document-verified date of
// birth; contradicted when a verified date of birth is below the policy age (ADR-037 §1).
func (a campaignAge) Attested(ctx context.Context, userID string) (bool, bool, error) {
	st, err := a.k.AgeStatus(ctx, userID)
	if err != nil {
		return false, false, err
	}
	if st.VerifiedDOBBelowAdultAge {
		return false, true, nil
	}
	if st.Assurance == "DOCUMENT_VERIFIED" {
		return true, false, nil
	}
	at := st.Attestation
	return at != nil && at.Outcome == "ATTESTED" && at.AdultAge >= st.AdultAge, false, nil
}

type campaignFlags struct{ pool *pgxpool.Pool }

func (f campaignFlags) Enabled(ctx context.Context, key string) (bool, error) {
	return featureflags.Enabled(ctx, f.pool, key)
}

func newCampaigns(m *VerificationModules, d VerificationDeps) *campaigns.Service {
	svc := campaigns.New(campaigns.Deps{Pool: d.AppPool, KYC: m.KYC, Beneficiaries: m.Beneficiaries, Orgs: d.Orgs,
		Restrictions: campaignRestrictions{m.Compliance}, Age: campaignAge{m.KYC}, Flags: campaignFlags{d.AppPool},
		Limiter: d.Limiter, AEAD: m.appAEAD, Keyed: m.appKeyed, Clock: d.Clock, Logger: d.Logger})
	if d.Auth != nil {
		svc.StaffCan = d.Auth.StaffHasPermission
	}
	return svc
}

// registerCampaignRoutes registers the campaign API.
func registerCampaignRoutes(r *httpx.Router, m *VerificationModules, idem *idempotency.Store, logger *slog.Logger) {
	for _, rt := range m.Campaigns.Routes() {
		var opts []httpx.RouteOption
		if rt.Idempotent {
			opts = append(opts, httpx.With(idempotency.Middleware(idem, idempotency.Optional, "campaigns.create", logger)))
		}
		r.HandleFunc(rt.Pattern, rt.Policy, rt.Handler, opts...)
	}
}

// Consumer names (dedupe keys; never rename).
const (
	consumerCampaignRestriction = "campaigns.restriction_enforcement"
	consumerCampaignNotify      = "campaigns.notify"
)

// Owner notices for campaign events are Stage 15 notification templates (CAMPAIGN_*; internal/notifications/events.go).

type campaignEvent struct {
	CampaignID string `json:"campaign_id"`
	OwnerUser  string `json:"owner_user_id"`
	OwnerOrg   string `json:"owner_organisation_id"`
	SubjectT   string `json:"subject_type"`
	SubjectID  string `json:"subject_id"`
	Level      string `json:"level"`
}

// registerCampaignConsumers subscribes restriction enforcement (worker).
func registerCampaignConsumers(reg *outbox.Registry, m *VerificationModules) {
	parse := func(del outbox.Delivery) (campaignEvent, error) {
		var p campaignEvent
		if err := json.Unmarshal(del.Payload, &p); err != nil {
			return p, fmt.Errorf("%s: malformed payload", del.EventType)
		}
		return p, nil
	}
	// ADR-036 §6: SUSPENDED / OFFBOARDED restrictions suspend affected campaigns. Lifting never reactivates.
	reg.Subscribe(consumerCampaignRestriction, compliance.EvRestrictionApplied, func(ctx context.Context, del outbox.Delivery) error {
		p, err := parse(del)
		if err != nil {
			return err
		}
		if p.Level != string(compliance.LevelSuspended) && p.Level != string(compliance.LevelOffboarded) || !ids.Valid(p.SubjectID) {
			return nil
		}
		list, err := m.Campaigns.CampaignsForSubject(ctx, p.SubjectT, p.SubjectID)
		if err != nil {
			return err
		}
		for _, id := range list {
			if err := m.Campaigns.SuspendForRestriction(ctx, id); err != nil {
				return err
			}
		}
		return nil
	})
}
