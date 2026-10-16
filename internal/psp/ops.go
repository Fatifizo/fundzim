package psp

import (
	"context"

	"github.com/Fatifizo/fundzim/internal/platform/opsview"
)

// OpsSnapshot is the provider registry's contribution to the operations portal (Stage 14, ADR-046): provider status as
// derived from observations (no observation = UNKNOWN; nothing is invented), the live-execution gate and webhook dead
// letters. Read-only.
func (s *Service) OpsSnapshot(ctx context.Context) (opsview.Snapshot, error) {
	const m = "providers"
	snap := opsview.Snapshot{Module: m, Metrics: []opsview.Metric{}, Approvals: []opsview.Approval{}, Exceptions: []opsview.Exception{}}
	const view = "payment.view"
	ps, err := s.Providers(ctx) // not AdminProviders: the snapshot needs no secret resolution
	if err != nil {
		return snap, err
	}
	for _, p := range ps {
		h, err := s.ProviderHealth(ctx, p)
		if err != nil {
			return snap, err
		}
		status := h.Status
		if status == "" {
			status = opsview.StatusUnknown
		}
		nature := opsview.Real
		if p.IsSandbox {
			nature = opsview.Simulated
		}
		snap.Metrics = append(snap.Metrics, opsview.Metric{Key: "providers." + p.Code + ".health", Label: "Provider " + p.DisplayName + " (" + p.SelectionStatus + ")",
			Module: m, Nature: nature, Status: status, Permission: view, Href: "/admin/payments/providers",
			Source: "derived from recorded provider observations in the health window (" + h.Reason + ")"})
	}
	gate := opsview.Metric{Key: "providers.live_execution", Label: "Live payment execution gate", Module: m, Nature: opsview.Blocked, Status: opsview.StatusBlocked,
		Permission: view, Source: "constant psp.LiveExecutionEnabled and kill switch payments.live_execution.enabled", Href: "/admin/payments/health"}
	if s.Gates(ctx).Enabled {
		gate.Status, gate.Nature = opsview.Unknown, opsview.Unknown // never reachable in Stage 14 (constant false); reported, not hidden
	}
	snap.Metrics = append(snap.Metrics, gate)
	var dead int64
	if err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM app.provider_webhook_inbox WHERE status = 'DEAD_LETTERED'`).Scan(&dead); err != nil {
		return snap, err
	}
	snap.Metrics = append(snap.Metrics, opsview.Count(m, "providers.webhooks.dead_lettered", "Dead-lettered provider webhooks", dead, view,
		"app.provider_webhook_inbox DEAD_LETTERED", "/admin/payments/webhooks"))
	rows, err := s.Pool.Query(ctx, `SELECT id::text, received_at, status FROM app.provider_webhook_inbox WHERE status = 'DEAD_LETTERED'
		ORDER BY received_at DESC LIMIT 100`)
	if err != nil {
		return snap, err
	}
	defer rows.Close()
	for rows.Next() {
		var e opsview.Exception
		if err := rows.Scan(&e.SourceRef, &e.CreatedAt, &e.DomainStatus); err != nil {
			return snap, err
		}
		e.ID, e.SourceModule, e.Severity, e.Category, e.Permission = m+":webhook:"+e.SourceRef, m, "SEV2", "WEBHOOK_DEAD_LETTERED", view
		e.Summary, e.Href = "Provider webhook could not be dispatched (re-queue by staff)", "/admin/payments/webhooks"
		snap.Exceptions = append(snap.Exceptions, e)
	}
	return snap, rows.Err()
}
