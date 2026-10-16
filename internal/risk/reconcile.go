package risk

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Fatifizo/fundzim/internal/audit"
)

// Risk reconciliation (brief §61): read-only consistency checks over the risk records. A finding is a monitoring event
// (deduplicated per day and object) for people to investigate — reconciliation never edits evidence, releases a hold
// or closes a case.

// ReconCheck is one check's result.
type ReconCheck struct {
	Code     string   `json:"code"`
	Status   string   `json:"status"` // PASS | FINDINGS
	Findings int      `json:"findings"`
	Examples []string `json:"examples"`
}

// ReconReport is a reconciliation run.
type ReconReport struct {
	RunAt  time.Time    `json:"run_at"`
	Checks []ReconCheck `json:"checks"`
}

// Reconcile runs every check and records findings as monitoring events.
func (s *Service) Reconcile(ctx context.Context, actor string) (ReconReport, error) {
	now := s.now()
	day := now.Format("2006-01-02")
	rep := ReconReport{RunAt: now, Checks: []ReconCheck{}}
	checks := []struct {
		code, monitor, q string
		args             []any
	}{
		// signals recorded while an ACTIVE rule covered their subject type, without an evaluation
		{"MISSING_EVALUATION", "RECON_MISSING_EVALUATION", `SELECT s.id::text, s.subject_type, s.subject_id::text FROM risk.risk_signals s
			WHERE s.recorded_at > $1::timestamptz - interval '7 days' AND s.recorded_at < $1::timestamptz - interval '1 minute'
			  AND EXISTS (SELECT 1 FROM risk.risk_rule_versions v JOIN risk.risk_rules r ON r.id = v.rule_id
			              WHERE r.subject_type = s.subject_type AND v.status = 'ACTIVE' AND v.activated_at < s.recorded_at)
			  AND NOT EXISTS (SELECT 1 FROM risk.risk_evaluations e WHERE e.trigger_signal_id = s.id) LIMIT 100`, []any{now}},
		// holds still open although their case was resolved as no further action or a false positive
		{"ORPHANED_HOLD", "RECON_ORPHANED_HOLD", `SELECT h.id::text, h.subject_type, h.subject_id::text FROM risk.risk_holds h
			JOIN risk.risk_cases c ON c.id = h.case_id
			WHERE h.status IN ('ACTIVE','UNDER_REVIEW','RELEASE_PENDING') AND c.status IN ('RESOLVED','CLOSED')
			  AND c.decision_outcome IN ('NO_FURTHER_ACTION','FALSE_POSITIVE') LIMIT 100`, nil},
		// temporary holds past their expiry (the expiry job is behind)
		{"HOLD_PAST_EXPIRY", "RECON_HOLD_PAST_EXPIRY", `SELECT id::text, subject_type, subject_id::text FROM risk.risk_holds
			WHERE status IN ('ACTIVE','UNDER_REVIEW') AND expires_at < $1::timestamptz - interval '10 minutes' LIMIT 100`, []any{now}},
		// automated holds without an open or decided case (every automated hold must reach a human)
		{"AUTOMATED_HOLD_WITHOUT_CASE", "RECON_ORPHANED_HOLD", `SELECT id::text, subject_type, subject_id::text FROM risk.risk_holds
			WHERE automated AND case_id IS NULL AND status IN ('ACTIVE','UNDER_REVIEW','RELEASE_PENDING') LIMIT 100`, nil},
		// screenings left PENDING past their deadline (the poll job is behind)
		{"SCREENING_STALE", "SCREENING_STALE", `SELECT id::text, subject_type, subject_id::text FROM risk.risk_screening_requests
			WHERE status = 'PENDING' AND timeout_at < $1::timestamptz - interval '10 minutes' LIMIT 100`, []any{now}},
		// more than one open case of different types on one subject is allowed; several open cases of one type are not
		{"DUPLICATE_OPEN_CASES", "RECON_DUPLICATE_CASES", `SELECT min(id::text), subject_type, subject_id::text FROM risk.risk_cases
			WHERE status NOT IN ('RESOLVED','CLOSED') GROUP BY case_type, subject_type, subject_id HAVING count(*) > 1 LIMIT 100`, nil},
	}
	err := s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		for _, c := range checks {
			rows, err := tx.Query(ctx, c.q, c.args...)
			if err != nil {
				return err
			}
			type f struct{ id, st, sid string }
			var fs []f
			for rows.Next() {
				var x f
				if err := rows.Scan(&x.id, &x.st, &x.sid); err != nil {
					rows.Close()
					return err
				}
				fs = append(fs, x)
			}
			rows.Close()
			if err := rows.Err(); err != nil {
				return err
			}
			rc := ReconCheck{Code: c.code, Status: "PASS", Findings: len(fs), Examples: []string{}}
			for i, x := range fs {
				if i < 5 {
					rc.Examples = append(rc.Examples, x.id)
				}
				if err := s.monitor(ctx, tx, c.monitor, SevMed, x.st, x.sid, c.code+":"+x.id+":"+day, map[string]any{"check": c.code, "object_id": x.id}, now); err != nil {
					return err
				}
			}
			if len(fs) > 0 {
				rc.Status = "FINDINGS"
			}
			rep.Checks = append(rep.Checks, rc)
		}
		// evidence integrity: every stored hash matches its content
		rc := ReconCheck{Code: "EVIDENCE_HASH", Status: "PASS", Examples: []string{}}
		rows, err := tx.Query(ctx, `SELECT e.id::text, c.subject_type, c.subject_id::text, e.content, e.content_hash FROM risk.risk_case_evidence e
			JOIN risk.risk_cases c ON c.id = e.case_id WHERE e.added_at > $1::timestamptz - interval '30 days' ORDER BY e.added_at DESC LIMIT 5000`, now)
		if err != nil {
			return err
		}
		type bad struct{ id, st, sid string }
		var bads []bad
		for rows.Next() {
			var id, st, sid string
			var content json.RawMessage
			var h []byte
			if err := rows.Scan(&id, &st, &sid, &content, &h); err != nil {
				rows.Close()
				return err
			}
			if !VerifyEvidence(Evidence{Content: content, ContentHash: hexs(h)}) {
				bads = append(bads, bad{id, st, sid})
			}
		}
		rows.Close()
		for _, b := range bads {
			rc.Findings++
			if len(rc.Examples) < 5 {
				rc.Examples = append(rc.Examples, b.id)
			}
			if err := s.monitor(ctx, tx, "RECON_EVIDENCE_HASH_MISMATCH", SevHigh, b.st, b.sid, "EVIDENCE_HASH:"+b.id+":"+day,
				map[string]any{"evidence_id": b.id}, now); err != nil {
				return err
			}
		}
		if rc.Findings > 0 {
			rc.Status = "FINDINGS"
		}
		rep.Checks = append(rep.Checks, rc)
		// active rule definitions must still parse under this engine
		rc = ReconCheck{Code: "RULE_DEFINITIONS", Status: "PASS", Examples: []string{}}
		rows, err = tx.Query(ctx, `SELECT v.id::text, v.definition FROM risk.risk_rule_versions v WHERE v.status = 'ACTIVE'`)
		if err != nil {
			return err
		}
		var unparseable []string
		for rows.Next() {
			var id string
			var raw []byte
			if err := rows.Scan(&id, &raw); err != nil {
				rows.Close()
				return err
			}
			if _, err := ParseDefinition(raw, func(x string) bool { return SignalMetaOf(x).Known }); err != nil {
				unparseable = append(unparseable, id)
			}
		}
		rows.Close()
		for _, id := range unparseable {
			rc.Findings++
			rc.Examples = append(rc.Examples, id)
			if err := s.monitor(ctx, tx, "RECON_RULE_UNPARSEABLE", SevHigh, "", "", "RULE:"+id+":"+day, map[string]any{"rule_version_id": id}, now); err != nil {
				return err
			}
		}
		if rc.Findings > 0 {
			rc.Status = "FINDINGS"
		}
		rep.Checks = append(rep.Checks, rc)
		findings := 0
		for _, c := range rep.Checks {
			findings += c.Findings
		}
		return audit.Record(ctx, tx, audit.Event{Action: "risk.reconciliation.run", ActorType: "system", TargetType: "risk_reconciliation",
			Metadata: map[string]any{"actor": actor, "checks": len(rep.Checks), "findings": findings}})
	})
	return rep, err
}

// StaleCases records monitoring events for overdue cases and unassigned P1 cases (job).
func (s *Service) StaleCases(ctx context.Context) (int, error) {
	now := s.now()
	day := now.Format("2006-01-02")
	n := 0
	err := s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id::text, subject_type, subject_id::text, 'CASE_OVERDUE' FROM risk.risk_cases
			WHERE status NOT IN ('RESOLVED','CLOSED') AND due_at < $1
			UNION ALL
			SELECT id::text, subject_type, subject_id::text, 'CASE_UNASSIGNED_CRITICAL' FROM risk.risk_cases
			WHERE status NOT IN ('RESOLVED','CLOSED') AND priority = 'P1' AND assigned_to IS NULL AND created_at < $1::timestamptz - interval '1 hour'
			LIMIT 500`, now)
		if err != nil {
			return err
		}
		type x struct{ id, st, sid, typ string }
		var xs []x
		for rows.Next() {
			var v x
			if err := rows.Scan(&v.id, &v.st, &v.sid, &v.typ); err != nil {
				rows.Close()
				return err
			}
			xs = append(xs, v)
		}
		rows.Close()
		for _, v := range xs {
			if err := s.monitor(ctx, tx, v.typ, SevMed, v.st, v.sid, v.id+":"+day, map[string]any{"case_id": v.id}, now); err != nil {
				return err
			}
			n++
		}
		return nil
	})
	return n, err
}
