package reconciliation

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Fatifizo/fundzim/internal/audit"
	"github.com/Fatifizo/fundzim/internal/platform/opsview"
)

// OpsSnapshot is reconciliation's contribution to the operations portal (Stage 14, ADR-046): backlog, exceptions by
// severity, approvals waiting, worker health and the production gates, from its own tables. Read-only: decisions stay on
// the reconciliation routes (maker-checker, step-up).
func (s *Service) OpsSnapshot(ctx context.Context) (opsview.Snapshot, error) {
	const m = "reconciliation"
	snap := opsview.Snapshot{Module: m, Metrics: []opsview.Metric{}, Approvals: []opsview.Approval{}, Exceptions: []opsview.Exception{}}
	var open, crit, unknown, unmatched, pending, failedImports, discrepancy int64
	if err := s.Pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM recon.exceptions WHERE status NOT IN ('RESOLVED','CLOSED')),
		(SELECT count(*) FROM recon.exceptions WHERE status NOT IN ('RESOLVED','CLOSED') AND severity = 'CRITICAL'),
		(SELECT count(*) FROM recon.exceptions WHERE status NOT IN ('RESOLVED','CLOSED') AND exception_type IN ('UNKNOWN_PAYMENT_OUTCOME','UNKNOWN_PAYOUT_OUTCOME')),
		(SELECT count(*) FROM recon.items WHERE match_status IN ('UNMATCHED','EXCEPTION')),
		(SELECT count(*) FROM recon.resolutions WHERE status IN ('PENDING_REVIEW','APPROVED')),
		(SELECT count(*) FROM recon.imports WHERE status = 'FAILED' AND received_at > now() - interval '7 days'),
		(SELECT count(*) FROM recon.settlement_batches WHERE status = 'DISCREPANCY')`).
		Scan(&open, &crit, &unknown, &unmatched, &pending, &failedImports, &discrepancy); err != nil {
		return snap, err
	}
	tm := func(key, label string, v int64, src, href string) opsview.Metric {
		x := opsview.Count(m, key, label, v, PermView, src, href)
		x.Nature = opsview.TestOnly // every statement in Stage 17 is TEST evidence
		return x
	}
	snap.Metrics = append(snap.Metrics,
		tm("reconciliation.exceptions.open", "Open reconciliation exceptions (TEST)", open, "recon.exceptions not RESOLVED/CLOSED", "/admin/reconciliation/exceptions"),
		tm("reconciliation.exceptions.critical", "Critical reconciliation exceptions (TEST)", crit, "recon.exceptions CRITICAL", "/admin/reconciliation/exceptions?severity=CRITICAL"),
		tm("reconciliation.unknown_outcomes", "Statement evidence for unknown outcomes (TEST)", unknown, "recon.exceptions UNKNOWN_*_OUTCOME", "/admin/reconciliation/exceptions"),
		tm("reconciliation.lines.open", "Unmatched statement lines (TEST)", unmatched, "recon.items UNMATCHED/EXCEPTION", "/admin/reconciliation/batches"),
		tm("reconciliation.resolutions.pending", "Resolutions waiting for a checker", pending, "recon.resolutions PENDING_REVIEW/APPROVED", "/admin/reconciliation/exceptions"),
		tm("reconciliation.imports.failed", "Statement imports that failed validation (7 days)", failedImports, "recon.imports FAILED", "/admin/reconciliation/batches"),
		tm("reconciliation.settlements.discrepancy", "Settlement batches in discrepancy (TEST)", discrepancy, "recon.settlement_batches DISCREPANCY", "/admin/reconciliation/settlements"),
		opsview.Metric{Key: "reconciliation.production", Label: "Production statement imports and settlement postings", Module: m, Nature: opsview.Blocked,
			Status: opsview.StatusBlocked, Source: "ProductionImportsEnabled = false, SettlementPostingEnabled = false; no provider selected (ADR-040, ADR-049)",
			Permission: PermView},
	)
	if _, ok, err := s.ActiveRuleSet(ctx); err != nil {
		return snap, err
	} else if !ok {
		snap.Metrics = append(snap.Metrics, opsview.Metric{Key: "reconciliation.rule_set", Label: "Matching rule set", Module: m, Nature: opsview.NotAvailable,
			Status: opsview.NotConfigured, Source: "recon.matching_rule_sets: none ACTIVE (matching fails closed)", Permission: PermView,
			Href: "/admin/reconciliation/rules"})
	}
	workers, err := s.Workers(ctx)
	if err != nil {
		return snap, err
	}
	for _, w := range workers {
		st := map[string]string{"HEALTHY": opsview.Healthy, "DEGRADED": opsview.Degraded}[w.Status]
		if st == "" {
			st = opsview.StatusUnknown
		}
		snap.Metrics = append(snap.Metrics, opsview.Metric{Key: "reconciliation.worker." + w.Job, Label: "Worker " + w.Job, Module: m, Nature: opsview.Real,
			Status: st, Source: "recon.worker_heartbeats", Permission: PermView, Value: opsview.Int64(int64(w.ConsecutiveFailures))})
	}
	rows, err := s.Pool.Query(ctx, `SELECT r.id::text, r.proposed_by::text, r.proposed_at, r.status, r.resolution_type, e.exception_type
		FROM recon.resolutions r JOIN recon.exceptions e ON e.id = r.exception_id WHERE r.status = 'PENDING_REVIEW' ORDER BY r.proposed_at LIMIT 200`)
	if err != nil {
		return snap, err
	}
	for rows.Next() {
		var a opsview.Approval
		var typ, exc string
		if err := rows.Scan(&a.SourceRecordID, &a.RequestedBy, &a.RequestedAt, &a.Status, &typ, &exc); err != nil {
			rows.Close()
			return snap, err
		}
		a.ID, a.SourceModule, a.Action, a.RequiredPermission = m+":"+a.SourceRecordID, m, "RECONCILIATION_RESOLUTION", PermResolve
		a.Summary, a.Makers, a.Href = typ+" for "+exc+" (TEST)", []string{a.RequestedBy}, "/admin/reconciliation/exceptions"
		snap.Approvals = append(snap.Approvals, a)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return snap, err
	}
	rows, err = s.Pool.Query(ctx, `SELECT id::text, severity, exception_type, status, detected_at FROM recon.exceptions
		WHERE status NOT IN ('RESOLVED','CLOSED') ORDER BY CASE severity WHEN 'CRITICAL' THEN 0 WHEN 'HIGH' THEN 1 WHEN 'MEDIUM' THEN 2 ELSE 3 END,
		detected_at LIMIT 200`)
	if err != nil {
		return snap, err
	}
	defer rows.Close()
	for rows.Next() {
		var e opsview.Exception
		var sev string
		if err := rows.Scan(&e.SourceRef, &sev, &e.Category, &e.DomainStatus, &e.CreatedAt); err != nil {
			return snap, err
		}
		e.Severity = map[string]string{SevCritical: "SEV1", SevHigh: "SEV2", SevMedium: "SEV3", SevLow: "SEV4"}[sev]
		e.ID, e.SourceModule, e.Permission = m+":"+e.SourceRef, m, PermView
		e.Summary, e.Href = e.Category+" (TEST evidence)", "/admin/reconciliation/exceptions/"+e.SourceRef
		snap.Exceptions = append(snap.Exceptions, e)
	}
	return snap, rows.Err()
}

// Ageing is the periodic housekeeping: expire undecided proposals, retry approved executions, reap lost runs, flag stale
// sources and escalate old exceptions (only when the active rule set configures it). Returns counts for logging.
func (s *Service) Ageing(ctx context.Context) (map[string]int, error) {
	out := map[string]int{}
	var err error
	if out["resolutions_expired"], err = s.ExpireResolutions(ctx); err != nil {
		return out, err
	}
	if out["resolutions_executed"], err = s.ExecuteApproved(ctx); err != nil {
		return out, err
	}
	if out["runs_reaped"], err = s.ReapStaleRuns(ctx); err != nil {
		return out, err
	}
	rules, ok, err := s.ActiveRuleSet(ctx)
	if err != nil || !ok {
		return out, err
	}
	// staleness: a (source, currency) that has been used, with no statement parsed within stale_after_hours
	rows, err := s.Pool.Query(ctx, `SELECT s.id::text, s.code, x.currency, max(i.parsed_at) FROM recon.sources s
		JOIN recon.imports i ON i.source_id = s.id AND i.status IN ('PARSED','SUPERSEDED')
		JOIN recon.items x ON x.import_id = i.id WHERE s.enabled GROUP BY 1, 2, 3`)
	if err != nil {
		return out, err
	}
	type scope struct {
		id, code, ccy string
		last          time.Time
	}
	scopes, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (scope, error) {
		var x scope
		err := r.Scan(&x.id, &x.code, &x.ccy, &x.last)
		return x, err
	})
	if err != nil {
		return out, err
	}
	limit := time.Duration(rules.StaleAfterHours) * time.Hour
	for _, sc := range scopes {
		key := "STALE_RECONCILIATION:source:" + sc.id + ":" + sc.ccy
		stale := s.now().Sub(sc.last) > limit
		err := s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
			if stale {
				opened, err := s.openException(ctx, tx, "", ExceptionDraft{Type: "STALE_RECONCILIATION", Severity: SevMedium, DedupeKey: key,
					Detail: map[string]any{"source": sc.code, "last_parsed_at": sc.last.UTC().Format(time.RFC3339), "stale_after_hours": rules.StaleAfterHours,
						"rule_set_version": rules.Version}}, sc.id, sc.ccy)
				if opened {
					out["stale_opened"]++
				}
				return err
			}
			var id string
			err := tx.QueryRow(ctx, `SELECT id::text FROM recon.exceptions WHERE dedupe_key = $1 AND status NOT IN ('CLOSED','RESOLVED','PENDING_APPROVAL')`, key).Scan(&id)
			if err != nil {
				return nil
			}
			out["stale_closed"]++
			return s.closeCleared(ctx, tx, id, "")
		})
		if err != nil {
			return out, err
		}
	}
	if rules.EscalateAfterHours != nil {
		rows, err := s.Pool.Query(ctx, `SELECT id::text FROM recon.exceptions WHERE status NOT IN ('RESOLVED','CLOSED') AND escalated_at IS NULL
			AND detected_at < $1 ORDER BY detected_at LIMIT 500`, s.now().Add(-time.Duration(*rules.EscalateAfterHours)*time.Hour))
		if err != nil {
			return out, err
		}
		list, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			return out, err
		}
		for _, id := range list {
			if err := s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
				tag, err := tx.Exec(ctx, `UPDATE recon.exceptions SET escalated_at = $2, version = version + 1 WHERE id = $1 AND escalated_at IS NULL`, id, s.now())
				if err != nil || tag.RowsAffected() == 0 {
					return err
				}
				if err := s.addEvent(ctx, tx, id, "ESCALATED", "", "", "", "", "", "older than the rule set's escalation age", ""); err != nil {
					return err
				}
				return audit.Record(ctx, tx, audit.Event{Action: "reconciliation.exception.escalated", ActorType: "system", TargetType: "reconciliation_exception",
					TargetID: id, Metadata: map[string]any{"escalate_after_hours": *rules.EscalateAfterHours, "rule_set_version": rules.Version}})
			}); err != nil {
				return out, err
			}
			out["escalated"]++
		}
	}
	return out, nil
}
