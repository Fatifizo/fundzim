package operations

import (
	"testing"
	"time"

	"github.com/Fatifizo/fundzim/internal/platform/opsview"
)

func keys(fs []finding) map[string]finding {
	out := map[string]finding{}
	for _, f := range fs {
		out[f.key] = f
	}
	return out
}

// Stage 18: the security rules over snapshots, sources, derived alerts and backup evidence.
func TestSecurityFindings(t *testing.T) {
	now := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	recent := now.Add(-time.Hour)
	ok := BackupEvidence{LastBackup: &recent, LastRestore: &recent}
	v := opsview.Int64

	// a quiet platform raises nothing
	quiet := []opsview.Snapshot{{Module: "payments", Metrics: []opsview.Metric{{Key: "payments.live_execution", Status: opsview.StatusBlocked}}},
		{Module: "audit", Metrics: []opsview.Metric{{Key: "audit.chain.audit_events", Status: opsview.Healthy, Value: v(10)}}},
		{Module: "identity_security", Metrics: []opsview.Metric{{Key: "auth.security.login_failed_15m", Value: v(3)}}}}
	if got := securityFindings(now, quiet, nil, nil, ok); len(got) != 0 {
		t.Fatalf("quiet platform raised %+v", got)
	}

	noisy := []opsview.Snapshot{
		{Module: "payments", Metrics: []opsview.Metric{{Key: "payments.live_execution", Status: opsview.StatusUnknown, Label: "Live donations"}}},
		{Module: "reconciliation", Metrics: []opsview.Metric{{Key: "reconciliation.production", Status: opsview.Healthy},
			{Key: "reconciliation.exceptions.critical", Value: v(2)}}},
		{Module: "audit", Metrics: []opsview.Metric{{Key: "audit.chain.audit_events", Status: opsview.Unavailable, Value: v(10)},
			{Key: "audit.chain.security_audit_events", Status: opsview.StatusUnknown}}},
		{Module: "identity_security", Metrics: []opsview.Metric{
			{Key: "auth.security.login_failed_15m", Value: v(50)}, {Key: "auth.security.accounts_targeted_15m", Value: v(1)},
			{Key: "auth.security.second_factor_failed_15m", Value: v(19)}, {Key: "auth.security.rate_limited_15m", Value: v(100)},
			{Key: "auth.security.break_glass_active", Value: v(1)}, {Key: "auth.security.role_changes_24h", Value: v(2)}}},
	}
	got := keys(securityFindings(now, noisy, []SourceStatus{{Module: "payouts", Status: opsview.Unavailable, Error: "SOURCE_UNAVAILABLE"}},
		[]Alert{{Key: "LEDGER_INTEGRITY_FAILURE", Severity: "SEV1", Title: "x", ObservedAt: now}}, BackupEvidence{}))
	want := map[string]string{
		"FINANCIAL_GATE_NOT_BLOCKED": "CRITICAL", "RECONCILIATION_CRITICAL_EXCEPTIONS": "HIGH", "AUDIT_CHAIN_INTEGRITY_FAILURE": "CRITICAL",
		"AUDIT_CHAIN_VERIFICATION_STALE": "MEDIUM", "CREDENTIAL_STUFFING_SUSPECTED": "HIGH", "ACCOUNT_TARGETED": "MEDIUM",
		"RATE_LIMIT_ANOMALY": "MEDIUM", "BREAK_GLASS_ACTIVE": "HIGH", "PRIVILEGE_CHANGES": "INFO", "MONITORING_SOURCE_UNAVAILABLE": "MEDIUM",
		"LEDGER_INTEGRITY_FAILURE": "CRITICAL", "BACKUP_EVIDENCE_MISSING": "MEDIUM", "RESTORE_DRILL_MISSING": "MEDIUM"}
	for k, sev := range want {
		if got[k].severity != sev {
			t.Errorf("%s: severity %q, want %q", k, got[k].severity, sev)
		}
	}
	if _, raised := got["SECOND_FACTOR_FAILURES"]; raised {
		t.Error("19 second-factor failures is below the threshold")
	}
	// both gates (unknown and healthy) are violations, each its own alert
	n := 0
	for _, f := range securityFindings(now, noisy, nil, nil, ok) {
		if f.key == "FINANCIAL_GATE_NOT_BLOCKED" {
			n++
		}
	}
	if n != 2 {
		t.Fatalf("gate violations: %d, want 2", n)
	}
	// informational daily windows dedupe per UTC day
	if got["PRIVILEGE_CHANGES"].dedupe != "PRIVILEGE_CHANGES:auth.security.role_changes_24h:2026-10-23" {
		t.Errorf("daily dedupe key %q", got["PRIVILEGE_CHANGES"].dedupe)
	}

	old := now.Add(-30 * time.Hour)
	failed := now.Add(-time.Minute)
	drill := now.Add(-40 * 24 * time.Hour)
	got = keys(securityFindings(now, nil, nil, nil, BackupEvidence{LastBackup: &old, LastFailedBackup: &failed, LastRestore: &drill}))
	for k, sev := range map[string]string{"BACKUP_STALE": "HIGH", "BACKUP_FAILED": "HIGH", "RESTORE_DRILL_STALE": "MEDIUM"} {
		if got[k].severity != sev {
			t.Errorf("%s: %q, want %q", k, got[k].severity, sev)
		}
	}
}

// F-R2-03: a reconciliation sweep that fails, stalls or is blocked by deferrals raises one in-app alert per detector and
// state; progressing (also slowly) or not-started sweeps raise nothing here (not started is the coverage figure's UNKNOWN).
func TestSecurityFindingsReconciliationSweepHealth(t *testing.T) {
	now := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	recent := now.Add(-time.Hour)
	ok := BackupEvidence{LastBackup: &recent, LastRestore: &recent}
	v := opsview.Int64
	m := func(key, state string) opsview.Metric {
		return opsview.Metric{Key: key, State: state, Value: v(7200), Label: key, Source: "checkpoint"}
	}
	quiet := []opsview.Snapshot{{Module: "payouts", Metrics: []opsview.Metric{m("payouts.reconciliation.sweep_health", "PROGRESSING")}},
		{Module: "fees", Metrics: []opsview.Metric{m("fees.reconciliation.sweep_health", "PROGRESSING_SLOWLY")}},
		{Module: "ledger", Metrics: []opsview.Metric{m("ledger.reconciliation.sweep_health.payment_facts", "NOT_STARTED")}}}
	if got := securityFindings(now, quiet, nil, nil, ok); len(got) != 0 {
		t.Fatalf("healthy or slow sweeps raised %+v", got)
	}
	bad := []opsview.Snapshot{{Module: "payouts", Metrics: []opsview.Metric{m("payouts.reconciliation.sweep_health", "STALLED")}},
		{Module: "fees", Metrics: []opsview.Metric{m("fees.reconciliation.sweep_health", "FAILING")}},
		{Module: "ledger", Metrics: []opsview.Metric{m("ledger.reconciliation.sweep_health.payment_journals", "BLOCKED_ON_DEFERRALS"),
			m("ledger.reconciliation.sweep_health.payment_facts", "STALLED")}}}
	fs := securityFindings(now, bad, nil, nil, ok)
	dedupes := map[string]string{}
	for _, f := range fs {
		dedupes[f.dedupe] = f.severity
		if f.evidence["state"] == nil {
			t.Errorf("%s: evidence lacks the health state", f.dedupe)
		}
	}
	for _, d := range []string{"RECONCILIATION_SWEEP_STALLED:payouts.reconciliation.sweep_health", "RECONCILIATION_SWEEP_FAILING:fees.reconciliation.sweep_health",
		"RECONCILIATION_SWEEP_BLOCKED:ledger.reconciliation.sweep_health.payment_journals",
		"RECONCILIATION_SWEEP_STALLED:ledger.reconciliation.sweep_health.payment_facts"} {
		if dedupes[d] != "HIGH" {
			t.Errorf("%s: severity %q, want HIGH (raised: %v)", d, dedupes[d], dedupes)
		}
	}
	if len(fs) != 4 {
		t.Errorf("expected 4 alerts, got %d", len(fs))
	}
}

// Remediation phase 4: a checkpoint that failed its integrity verification raises a CRITICAL in-app alert.
func TestSecurityFindingsReconciliationSweepIntegrity(t *testing.T) {
	now := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	recent := now.Add(-time.Hour)
	ok := BackupEvidence{LastBackup: &recent, LastRestore: &recent}
	snaps := []opsview.Snapshot{{Module: "ledger", Metrics: []opsview.Metric{{Key: "ledger.reconciliation.sweep_health.payment_journals",
		State: "INTEGRITY_FAILURE", Value: opsview.Int64(60), Label: "journals", Source: "checkpoint"}}}}
	fs := securityFindings(now, snaps, nil, nil, ok)
	if len(fs) != 1 || fs[0].key != "RECONCILIATION_SWEEP_INTEGRITY" || fs[0].severity != "CRITICAL" || fs[0].evidence["state"] != "INTEGRITY_FAILURE" {
		t.Fatalf("integrity alert: %+v", fs)
	}
}

// Remediation phase 6 (F-R5-01/02): an integrity metric with no current successful verification raises a HIGH in-app
// alert; a single failed check (LATEST_FAILED), a verified one and a new detector (NOT_RUN) do not. Recovery is the
// condition clearing (the scan's CONDITION_CLEARED), never an automatic resolution.
func TestSecurityFindingsReconciliationIntegrityUnverified(t *testing.T) {
	now := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	recent := now.Add(-time.Hour)
	ok := BackupEvidence{LastBackup: &recent, LastRestore: &recent}
	m := func(key, state string) opsview.Metric {
		return opsview.Metric{Key: key, State: state, Value: opsview.Int64(7200), Label: key, Source: "checkpoint"}
	}
	quiet := []opsview.Snapshot{{Module: "payouts", Metrics: []opsview.Metric{m("payouts.reconciliation.integrity_age_seconds", "VERIFIED")}},
		{Module: "fees", Metrics: []opsview.Metric{m("fees.reconciliation.integrity_age_seconds", "LATEST_FAILED")}},
		{Module: "ledger", Metrics: []opsview.Metric{m("ledger.reconciliation.integrity_age_seconds.payment_facts", "NOT_RUN")}}}
	if got := securityFindings(now, quiet, nil, nil, ok); len(got) != 0 {
		t.Fatalf("verified, single-failure or new detectors raised %+v", got)
	}
	stale := []opsview.Snapshot{{Module: "ledger", Metrics: []opsview.Metric{m("ledger.reconciliation.integrity_age_seconds.payment_journals", "STALE"),
		m("ledger.reconciliation.sweep_health.payment_journals", "BLOCKED_ON_DEFERRALS")}}}
	fs := securityFindings(now, stale, nil, nil, ok)
	got := map[string]string{}
	for _, f := range fs {
		got[f.dedupe] = f.severity
		if f.key == "RECONCILIATION_SWEEP_INTEGRITY_UNVERIFIED" && f.evidence["state"] != "STALE" {
			t.Errorf("evidence lacks the integrity state: %+v", f.evidence)
		}
	}
	if got["RECONCILIATION_SWEEP_INTEGRITY_UNVERIFIED:ledger.reconciliation.integrity_age_seconds.payment_journals"] != "HIGH" ||
		got["RECONCILIATION_SWEEP_BLOCKED:ledger.reconciliation.sweep_health.payment_journals"] != "HIGH" || len(fs) != 2 {
		t.Fatalf("stale integrity beside a blocked sweep: %v", got)
	}
	if got := securityFindings(now, quiet, nil, nil, ok); len(got) != 0 {
		t.Fatalf("after recovery: %+v", got)
	}
}
