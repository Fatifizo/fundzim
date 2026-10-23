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
