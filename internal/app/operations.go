package app

import (
	"context"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Fatifizo/fundzim/internal/operations"
	"github.com/Fatifizo/fundzim/internal/platform/clock"
	"github.com/Fatifizo/fundzim/internal/platform/db"
	"github.com/Fatifizo/fundzim/internal/platform/httpx"
	"github.com/Fatifizo/fundzim/internal/platform/idempotency"
	"github.com/Fatifizo/fundzim/internal/platform/opsview"
	"github.com/Fatifizo/fundzim/internal/platform/storage"
)

// NewOperations builds the Stage 14 operations module (ADR-046): one source per module (each module's OpsSnapshot over
// its own tables), infrastructure probes, and the identity services it needs for permissions, linked accounts and
// access reviews. It never receives a module's pool to read another module's tables.
func NewOperations(d *Deps) *operations.Service {
	m := d.Verification
	s := &operations.Service{Pool: d.DB, Clock: clock.System, Logger: d.Logger, Environment: string(d.Config.App.Env)}
	if d.DB != nil {
		s.Sources = append(s.Sources, securitySources(d.DB, clock.System)...) // Stage 18: identity signals, audit verification
	}
	if d.Auth != nil {
		s.Perms, s.Revoker, s.StaffRoles = d.Auth, d.Auth, d.Auth
		s.Sources = append(s.Sources, operations.Source{Module: "identity", Snapshot: d.Auth.OpsSnapshot})
	}
	if m != nil {
		s.Identities = m.KYC
		add := func(module string, snap func(context.Context) (opsview.Snapshot, error), search func(context.Context, string) ([]opsview.SearchHit, error), perm string) {
			s.Sources = append(s.Sources, operations.Source{Module: module, Snapshot: snap, Search: search, SearchPermission: perm})
		}
		if m.Campaigns != nil {
			add("campaigns", m.Campaigns.OpsSnapshot, m.Campaigns.OpsSearch, "campaign.view")
		}
		if m.KYC != nil {
			add("kyc", m.KYC.OpsSnapshot, m.KYC.OpsSearch, "kyc.case.review")
		}
		if m.Beneficiaries != nil {
			add("beneficiaries", m.Beneficiaries.OpsSnapshot, nil, "")
		}
		if m.Compliance != nil {
			add("compliance", m.Compliance.OpsSnapshot, nil, "")
		}
		if m.Risk != nil {
			add("risk", m.Risk.OpsSnapshot, m.Risk.OpsSearch, "risk.operations.view")
		}
		if m.Payments != nil {
			add("payments", m.Payments.OpsSnapshot, m.Payments.OpsSearch, "payment.view")
		}
		if m.PSP != nil {
			add("providers", m.PSP.OpsSnapshot, nil, "")
		}
		if m.Ledger != nil {
			add("ledger", m.Ledger.OpsSnapshot, nil, "")
		}
		if m.Payouts != nil {
			add("payouts", m.Payouts.OpsSnapshot, m.Payouts.OpsSearch, "payout.view")
		}
		if m.Fees != nil {
			add("fees", m.Fees.OpsSnapshot, nil, "")
		}
		if d.Notifications != nil {
			add("notifications", d.Notifications.OpsSnapshot, nil, "") // Stage 15 (ADR-047)
		}
		if d.Discovery != nil {
			add("discovery", d.Discovery.OpsSnapshot, nil, "") // Stage 16 (ADR-048)
		}
		if d.Reconciliation != nil {
			add("reconciliation", d.Reconciliation.OpsSnapshot, nil, "") // Stage 17 (ADR-049)
		}
		for _, x := range []struct {
			name string
			pool *pgxpool.Pool
		}{{"kyc_database", m.KYCPool}, {"compliance_database", m.CompliancePool}, {"ledger_database", m.LedgerPool}} {
			if x.pool != nil {
				p := x.pool
				s.Probes = append(s.Probes, operations.HealthProbe{Name: x.name, Check: func(ctx context.Context) error { return db.Ping(ctx, p) }})
			}
		}
	}
	if d.DB != nil {
		s.Probes = append(s.Probes, operations.HealthProbe{Name: "database", Check: func(ctx context.Context) error { return db.Ping(ctx, d.DB) }})
	}
	if d.Redis != nil {
		s.Probes = append(s.Probes, operations.HealthProbe{Name: "cache", Check: d.Redis.Ping})
	}
	if d.Storage != nil {
		for _, class := range []storage.Class{storage.PublicCampaignMedia, storage.PrivateIdentityDocuments, storage.PrivateComplianceDocuments} {
			c := class
			s.Probes = append(s.Probes, operations.HealthProbe{Name: "object_storage_" + string(c), Check: func(ctx context.Context) error {
				return d.Storage.Check(ctx, c)
			}})
		}
	}
	return s
}

// registerOperationsRoutes registers the operations API and the Stage 14 staff management routes (auth).
func registerOperationsRoutes(r *httpx.Router, d *Deps, idem *idempotency.Store, logger *slog.Logger) {
	if d.Auth != nil {
		perm := httpx.Permission
		r.HandleFunc("GET /api/v1/admin/staff", perm("staff.view"), d.Auth.ListStaff)
		r.HandleFunc("GET /api/v1/admin/staff/roles", perm("staff.view"), d.Auth.ListRoles)
		r.HandleFunc("GET /api/v1/admin/staff/{user_id}", perm("staff.view"), d.Auth.GetStaff)
		r.HandleFunc("POST /api/v1/admin/staff/{user_id}/sessions/revoke", perm("session.revoke"), d.Auth.RevokeStaffSessions)
	}
	if d.Verification == nil || d.DB == nil {
		return
	}
	h := &operations.HTTP{S: NewOperations(d)}
	for _, rt := range h.Routes() {
		var opts []httpx.RouteOption
		if rt.Idempotent {
			opts = append(opts, httpx.With(idempotency.Middleware(idem, idempotency.Required, rt.Scope, logger)))
		}
		r.HandleFunc(rt.Pattern, rt.Policy, rt.Handler, opts...)
	}
}
