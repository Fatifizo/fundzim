package operations

// Stage 18 security monitoring and alerting (docs/stage-18/security-monitoring.md, ADR-050). The worker's security scan
// evaluates fixed rules over module snapshots (each built by its module from its own tables) and the derived operational
// alerts, and keeps one persisted alert per condition in app.ops_security_alerts: raised, repeated (occurrences),
// cleared and recurred by the scan; acknowledged, escalated to an incident and resolved by a person. Thresholds are
// engineering defaults, not service levels. No external notification is sent.

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Fatifizo/fundzim/internal/audit"
	"github.com/Fatifizo/fundzim/internal/platform/errs"
	"github.com/Fatifizo/fundzim/internal/platform/ids"
	"github.com/Fatifizo/fundzim/internal/platform/opsview"
)

// Security permissions.
const (
	PermSecurityAlertView   = "security.alert.view"
	PermSecurityAlertManage = "security.alert.manage"
)

// alertSeverities (most severe last).
var alertSeverities = []string{"INFO", "LOW", "MEDIUM", "HIGH", "CRITICAL"}

func severityRank(s string) int {
	for i, v := range alertSeverities {
		if v == s {
			return i
		}
	}
	return -1
}

// sevFromOps maps the Stage 14 operational severities.
var sevFromOps = map[string]string{"SEV1": "CRITICAL", "SEV2": "HIGH", "SEV3": "MEDIUM", "SEV4": "LOW"}

// SecurityAlert is a persisted alert.
type SecurityAlert struct {
	ID               string         `json:"id"`
	AlertKey         string         `json:"alert_key"`
	DedupeKey        string         `json:"dedupe_key"`
	Severity         string         `json:"severity"`
	SourceModule     string         `json:"source_module"`
	Component        string         `json:"component"`
	Title            string         `json:"title"`
	Source           string         `json:"source"`
	Evidence         map[string]any `json:"evidence"`
	Status           string         `json:"status"`
	ConditionActive  bool           `json:"condition_active"`
	Occurrences      int            `json:"occurrences"`
	FirstSeenAt      time.Time      `json:"first_seen_at"`
	LastSeenAt       time.Time      `json:"last_seen_at"`
	ClearedAt        *time.Time     `json:"cleared_at"`
	AcknowledgedBy   *string        `json:"acknowledged_by"`
	AcknowledgedAt   *time.Time     `json:"acknowledged_at"`
	EscalationStatus string         `json:"escalation_status"`
	IncidentID       *string        `json:"incident_id"`
	ResolvedBy       *string        `json:"resolved_by"`
	ResolvedAt       *time.Time     `json:"resolved_at"`
	ResolutionNote   *string        `json:"resolution_note"`
	Version          int            `json:"version"`
}

// finding is one condition observed by a scan.
type finding struct {
	key, dedupe, severity, module, component, title, source string
	evidence                                                map[string]any
}

// Thresholds (engineering defaults; docs/stage-18/security-monitoring.md §3).
const (
	thrLoginFailures15m    = 50
	thrSecondFactor15m     = 20
	thrRateLimited15m      = 100
	backupMaxAge           = 26 * time.Hour
	restoreDrillMaxAge     = 35 * 24 * time.Hour
	auditVerificationStale = time.Hour
)

func metricEvidence(m opsview.Metric, now time.Time) map[string]any {
	ev := map[string]any{"metric": m.Key, "status": m.Status, "nature": m.Nature, "observed_at": now.Format(time.RFC3339)}
	if m.Value != nil {
		ev["value"] = *m.Value
	}
	return ev
}

// securityFindings evaluates the rules. Pure apart from its inputs (tested directly).
func securityFindings(now time.Time, snaps []opsview.Snapshot, statuses []SourceStatus, derived []Alert, backups BackupEvidence) []finding {
	var out []finding
	add := func(key, sev, module, component, title, source string, ev map[string]any) {
		out = append(out, finding{key: key, dedupe: key + ":" + component, severity: sev, module: module, component: component,
			title: title, source: source, evidence: ev})
	}
	val := func(m opsview.Metric) int64 {
		if m.Value == nil {
			return 0
		}
		return *m.Value
	}
	for _, snap := range snaps {
		for _, m := range snap.Metrics {
			module := strings.ReplaceAll(snap.Module, "-", "_")
			switch {
			// a production gate that is not reported BLOCKED is a financial control violation
			case (strings.HasSuffix(m.Key, ".live_execution") || m.Key == "reconciliation.production" || m.Key == "ledger.production_posting") &&
				m.Status != opsview.StatusBlocked:
				add("FINANCIAL_GATE_NOT_BLOCKED", "CRITICAL", module, m.Key, "A production financial gate is not reported as blocked: "+m.Label,
					m.Source, metricEvidence(m, now))
			case strings.HasPrefix(m.Key, "audit.chain.") && m.Status == opsview.Unavailable:
				add("AUDIT_CHAIN_INTEGRITY_FAILURE", "CRITICAL", "audit", m.Key, "Audit chain verification found problems", m.Source, metricEvidence(m, now))
			case strings.HasPrefix(m.Key, "audit.chain.") && (m.Status == opsview.Degraded || m.Status == opsview.StatusUnknown):
				add("AUDIT_CHAIN_VERIFICATION_STALE", "MEDIUM", "audit", m.Key, "Audit chain verification is stale or has never run", m.Source,
					metricEvidence(m, now))
			case m.Key == "reconciliation.exceptions.critical" && val(m) > 0:
				add("RECONCILIATION_CRITICAL_EXCEPTIONS", "HIGH", module, m.Key, "Critical reconciliation exceptions are open (TEST evidence)", m.Source,
					metricEvidence(m, now))
			case m.Key == "auth.security.login_failed_15m" && val(m) >= thrLoginFailures15m:
				add("CREDENTIAL_STUFFING_SUSPECTED", "HIGH", module, m.Key, "Many failed sign-ins in 15 minutes", m.Source, metricEvidence(m, now))
			case m.Key == "auth.security.accounts_targeted_15m" && val(m) > 0:
				add("ACCOUNT_TARGETED", "MEDIUM", module, m.Key, "Accounts with ten or more failed sign-ins in 15 minutes", m.Source, metricEvidence(m, now))
			case m.Key == "auth.security.second_factor_failed_15m" && val(m) >= thrSecondFactor15m:
				add("SECOND_FACTOR_FAILURES", "MEDIUM", module, m.Key, "Many failed MFA, step-up or OTP checks in 15 minutes", m.Source, metricEvidence(m, now))
			case m.Key == "auth.security.rate_limited_15m" && val(m) >= thrRateLimited15m:
				add("RATE_LIMIT_ANOMALY", "MEDIUM", module, m.Key, "Many requests refused by rate limits in 15 minutes", m.Source, metricEvidence(m, now))
			case m.Key == "auth.security.break_glass_active" && val(m) > 0:
				add("BREAK_GLASS_ACTIVE", "HIGH", module, m.Key, "A break-glass grant is active", m.Source, metricEvidence(m, now))
			case m.Key == "auth.security.mfa_removed_24h" && val(m) > 0:
				add("MFA_REMOVED", "INFO", module, m.Key+":"+now.Format("2006-01-02"), "MFA methods were removed in the last 24 hours", m.Source,
					metricEvidence(m, now))
			case m.Key == "auth.security.recovery_codes_used_24h" && val(m) > 0:
				add("RECOVERY_CODES_USED", "LOW", module, m.Key+":"+now.Format("2006-01-02"), "Recovery codes were used in the last 24 hours", m.Source,
					metricEvidence(m, now))
			case m.Key == "auth.security.role_changes_24h" && val(m) > 0:
				add("PRIVILEGE_CHANGES", "INFO", module, m.Key+":"+now.Format("2006-01-02"), "Staff roles changed in the last 24 hours", m.Source,
					metricEvidence(m, now))
			}
		}
	}
	for _, st := range statuses {
		if st.Status == opsview.Unavailable {
			add("MONITORING_SOURCE_UNAVAILABLE", "MEDIUM", "operations", st.Module, "A monitoring source could not be read: "+st.Module,
				"operations source "+st.Module, map[string]any{"module": st.Module, "error": st.Error})
		}
	}
	for _, a := range derived {
		sev := sevFromOps[a.Severity]
		if sev == "" {
			sev = "MEDIUM"
		}
		add(a.Key, sev, "operations", a.Key, a.Title, a.Source, map[string]any{"derived_alert": a.Key, "observed_at": a.ObservedAt.Format(time.RFC3339)})
	}
	switch {
	case backups.LastBackup == nil:
		add("BACKUP_EVIDENCE_MISSING", "MEDIUM", "operations", "backup", "No successful backup has been recorded", "app.ops_backup_runs", map[string]any{})
	case now.Sub(*backups.LastBackup) > backupMaxAge:
		add("BACKUP_STALE", "HIGH", "operations", "backup", "The last successful backup is older than 26 hours", "app.ops_backup_runs",
			map[string]any{"last_success": backups.LastBackup.Format(time.RFC3339)})
	}
	if backups.LastFailedBackup != nil && (backups.LastBackup == nil || backups.LastFailedBackup.After(*backups.LastBackup)) {
		add("BACKUP_FAILED", "HIGH", "operations", "backup", "The most recent backup run failed", "app.ops_backup_runs",
			map[string]any{"failed_at": backups.LastFailedBackup.Format(time.RFC3339)})
	}
	switch {
	case backups.LastRestore == nil:
		add("RESTORE_DRILL_MISSING", "MEDIUM", "operations", "restore", "No successful restore drill has been recorded", "app.ops_backup_runs", map[string]any{})
	case now.Sub(*backups.LastRestore) > restoreDrillMaxAge:
		add("RESTORE_DRILL_STALE", "MEDIUM", "operations", "restore", "The last successful restore drill is older than 35 days", "app.ops_backup_runs",
			map[string]any{"last_success": backups.LastRestore.Format(time.RFC3339)})
	}
	return out
}

// BackupEvidence summarises app.ops_backup_runs.
type BackupEvidence struct {
	LastBackup       *time.Time `json:"last_backup"`
	LastFailedBackup *time.Time `json:"last_failed_backup"`
	LastRestore      *time.Time `json:"last_verified_restore"`
	Runs             int64      `json:"runs"`
}

func (s *Service) backupEvidence(ctx context.Context) (BackupEvidence, error) {
	var b BackupEvidence
	err := s.Pool.QueryRow(ctx, `SELECT
		(SELECT max(finished_at) FROM app.ops_backup_runs WHERE kind = 'BACKUP' AND outcome = 'SUCCEEDED'),
		(SELECT max(finished_at) FROM app.ops_backup_runs WHERE kind = 'BACKUP' AND outcome = 'FAILED'),
		(SELECT max(finished_at) FROM app.ops_backup_runs WHERE kind = 'RESTORE_DRILL' AND outcome = 'SUCCEEDED'),
		(SELECT count(*) FROM app.ops_backup_runs)`).Scan(&b.LastBackup, &b.LastFailedBackup, &b.LastRestore, &b.Runs)
	return b, err
}

// ScanResult summarises one security scan.
type ScanResult struct {
	Findings int `json:"findings"`
	Raised   int `json:"raised"`
	Recurred int `json:"recurred"`
	Cleared  int `json:"cleared"`
}

// SecurityScan evaluates the rules and updates the alerts (one scan at a time: transaction-scoped advisory lock).
func (s *Service) SecurityScan(ctx context.Context) (ScanResult, error) {
	now := s.now()
	h, err := s.Health(ctx)
	if err != nil {
		return ScanResult{}, err
	}
	c := s.collect(ctx)
	backups, err := s.backupEvidence(ctx)
	if err != nil {
		return ScanResult{}, err
	}
	found := securityFindings(now, c.snaps, c.statuses, s.alerts(ctx, h, c.snaps), backups)
	res := ScanResult{Findings: len(found)}
	err = s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('operations.security_scan', 0))`); err != nil {
			return err
		}
		seen := map[string]bool{}
		for _, f := range found {
			if seen[f.dedupe] {
				continue
			}
			seen[f.dedupe] = true
			ev, _ := json.Marshal(f.evidence)
			var id string
			var active bool
			err := tx.QueryRow(ctx, `SELECT id::text, condition_active FROM app.ops_security_alerts WHERE dedupe_key = $1 AND status <> 'RESOLVED'
				FOR UPDATE`, f.dedupe).Scan(&id, &active)
			switch {
			case errors.Is(err, pgx.ErrNoRows):
				id = ids.New()
				if _, err := tx.Exec(ctx, `INSERT INTO app.ops_security_alerts (id, alert_key, dedupe_key, severity, source_module, component, title, source,
					evidence, status, first_seen_at, last_seen_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,'OPEN',$10,$10)`,
					id, f.key, f.dedupe, f.severity, f.module, truncate(f.component, 80), truncate(f.title, 300), truncate(f.source, 300), ev, now); err != nil {
					return err
				}
				if err := alertEvent(ctx, tx, id, "RAISED", nil, "", map[string]any{"severity": f.severity}, now); err != nil {
					return err
				}
				res.Raised++
				if s.Logger != nil {
					s.Logger.Warn("security alert raised", "alert_key", f.key, "severity", f.severity, "component", f.component)
				}
			case err != nil:
				return err
			default:
				if _, err := tx.Exec(ctx, `UPDATE app.ops_security_alerts SET last_seen_at = $2, evidence = $3,
					severity = CASE WHEN $4 > array_position(ARRAY['INFO','LOW','MEDIUM','HIGH','CRITICAL'], severity) - 1 THEN $5 ELSE severity END,
					condition_active = true, cleared_at = NULL, occurrences = occurrences + CASE WHEN condition_active THEN 0 ELSE 1 END,
					version = version + 1 WHERE id = $1`, id, now, ev, severityRank(f.severity), f.severity); err != nil {
					return err
				}
				if !active {
					if err := alertEvent(ctx, tx, id, "RECURRED", nil, "", map[string]any{}, now); err != nil {
						return err
					}
					res.Recurred++
				}
			}
		}
		rows, err := tx.Query(ctx, `SELECT id::text, dedupe_key FROM app.ops_security_alerts WHERE status <> 'RESOLVED' AND condition_active
			AND ($1::text[] IS NULL OR source_module = ANY($1))`, s.ClearScope)
		if err != nil {
			return err
		}
		var clear []string
		for rows.Next() {
			var id, key string
			if err := rows.Scan(&id, &key); err != nil {
				rows.Close()
				return err
			}
			if !seen[key] {
				clear = append(clear, id)
			}
		}
		rows.Close()
		for _, id := range clear {
			if _, err := tx.Exec(ctx, `UPDATE app.ops_security_alerts SET condition_active = false, cleared_at = $2, version = version + 1 WHERE id = $1`,
				id, now); err != nil {
				return err
			}
			if err := alertEvent(ctx, tx, id, "CONDITION_CLEARED", nil, "", map[string]any{}, now); err != nil {
				return err
			}
			res.Cleared++
		}
		return nil
	})
	return res, err
}

func alertEvent(ctx context.Context, tx pgx.Tx, alertID, typ string, actor *string, note string, details map[string]any, now time.Time) error {
	d, _ := json.Marshal(details)
	var n *string
	if note != "" {
		n = &note
	}
	_, err := tx.Exec(ctx, `INSERT INTO app.ops_security_alert_events (id, alert_id, event_type, actor_id, note, details, occurred_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7)`, ids.New(), alertID, typ, actor, n, d, now)
	return err
}

const alertCols = `id::text, alert_key, dedupe_key, severity, source_module, component, title, source, evidence, status, condition_active,
	occurrences, first_seen_at, last_seen_at, cleared_at, acknowledged_by::text, acknowledged_at, escalation_status, incident_id::text,
	resolved_by::text, resolved_at, resolution_note, version`

func scanAlert(row pgx.Row) (SecurityAlert, error) {
	var a SecurityAlert
	var ev []byte
	err := row.Scan(&a.ID, &a.AlertKey, &a.DedupeKey, &a.Severity, &a.SourceModule, &a.Component, &a.Title, &a.Source, &ev, &a.Status,
		&a.ConditionActive, &a.Occurrences, &a.FirstSeenAt, &a.LastSeenAt, &a.ClearedAt, &a.AcknowledgedBy, &a.AcknowledgedAt,
		&a.EscalationStatus, &a.IncidentID, &a.ResolvedBy, &a.ResolvedAt, &a.ResolutionNote, &a.Version)
	if errors.Is(err, pgx.ErrNoRows) {
		return a, ErrNotFound
	}
	_ = json.Unmarshal(ev, &a.Evidence)
	return a, err
}

// SecurityAlerts lists alerts: status "" (live: OPEN and ACKNOWLEDGED), OPEN, ACKNOWLEDGED, RESOLVED or ALL; most severe
// and most recent first (bounded).
func (s *Service) SecurityAlerts(ctx context.Context, status, minSeverity string) ([]SecurityAlert, error) {
	where := `status <> 'RESOLVED'`
	switch status {
	case "", "LIVE":
	case "OPEN", "ACKNOWLEDGED", "RESOLVED":
		where = `status = '` + status + `'`
	case "ALL":
		where = `true`
	default:
		return nil, validation("status", "INVALID_VALUE")
	}
	minRank := 0
	if minSeverity != "" {
		if minRank = severityRank(minSeverity); minRank < 0 {
			return nil, validation("min_severity", "INVALID_VALUE")
		}
	}
	rows, err := s.Pool.Query(ctx, `SELECT `+alertCols+` FROM app.ops_security_alerts WHERE `+where+`
		AND array_position(ARRAY['INFO','LOW','MEDIUM','HIGH','CRITICAL'], severity) - 1 >= $1
		ORDER BY (status = 'RESOLVED'), array_position(ARRAY['INFO','LOW','MEDIUM','HIGH','CRITICAL'], severity) DESC, last_seen_at DESC LIMIT 500`, minRank)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SecurityAlert{}
	for rows.Next() {
		a, err := scanAlert(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// SecurityAlert returns one alert and its history.
func (s *Service) SecurityAlert(ctx context.Context, id string) (SecurityAlert, []HistoryEntry, error) {
	if !ids.Valid(id) {
		return SecurityAlert{}, nil, ErrNotFound
	}
	a, err := scanAlert(s.Pool.QueryRow(ctx, `SELECT `+alertCols+` FROM app.ops_security_alerts WHERE id = $1`, id))
	if err != nil {
		return a, nil, err
	}
	rows, err := s.Pool.Query(ctx, `SELECT event_type, actor_id::text, coalesce(note, ''), occurred_at FROM app.ops_security_alert_events
		WHERE alert_id = $1 ORDER BY occurred_at, id LIMIT 500`, id)
	if err != nil {
		return a, nil, err
	}
	defer rows.Close()
	hist := []HistoryEntry{}
	for rows.Next() {
		var h HistoryEntry
		var actor *string
		if err := rows.Scan(&h.EventType, &actor, &h.Note, &h.OccurredAt); err != nil {
			return a, nil, err
		}
		if actor != nil {
			h.ActorID = *actor
		}
		hist = append(hist, h)
	}
	return a, hist, rows.Err()
}

// AlertOp is a staff action on an alert.
type AlertOp struct {
	StaffID string
	ID      string
	Version int
	Note    string
}

var (
	ErrAlertState = errs.New(errs.Conflict, "SECURITY_ALERT_STATE", "The alert is not in a state that allows this action.")
)

func (s *Service) alertAction(ctx context.Context, op AlertOp, fn func(ctx context.Context, tx pgx.Tx, a SecurityAlert, now time.Time) error) (SecurityAlert, error) {
	if !ids.Valid(op.ID) {
		return SecurityAlert{}, ErrNotFound
	}
	now := s.now()
	err := s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		a, err := scanAlert(tx.QueryRow(ctx, `SELECT `+alertCols+` FROM app.ops_security_alerts WHERE id = $1 FOR UPDATE`, op.ID))
		if err != nil {
			return err
		}
		if op.Version != 0 && op.Version != a.Version {
			return ErrStale
		}
		return fn(ctx, tx, a, now)
	})
	if err != nil {
		return SecurityAlert{}, err
	}
	a, _, err := s.SecurityAlert(ctx, op.ID)
	return a, err
}

// AcknowledgeAlert takes ownership of an OPEN alert.
func (s *Service) AcknowledgeAlert(ctx context.Context, op AlertOp) (SecurityAlert, error) {
	note, err := text("note", op.Note, 0, 2000)
	if err != nil {
		return SecurityAlert{}, err
	}
	return s.alertAction(ctx, op, func(ctx context.Context, tx pgx.Tx, a SecurityAlert, now time.Time) error {
		if a.Status != "OPEN" {
			return ErrAlertState
		}
		if _, err := tx.Exec(ctx, `UPDATE app.ops_security_alerts SET status = 'ACKNOWLEDGED', acknowledged_by = $2, acknowledged_at = $3,
			version = version + 1 WHERE id = $1`, a.ID, op.StaffID, now); err != nil {
			return err
		}
		if err := alertEvent(ctx, tx, a.ID, "ACKNOWLEDGED", &op.StaffID, note, map[string]any{}, now); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{Stream: audit.Security, Action: "operations.security_alert.acknowledged", TargetType: "security_alert",
			TargetID: a.ID, Metadata: map[string]any{"alert_key": a.AlertKey, "severity": a.Severity}})
	})
}

// EscalateAlert declares (or links) an incident for an acknowledged alert.
func (s *Service) EscalateAlert(ctx context.Context, op AlertOp) (SecurityAlert, error) {
	note, err := text("note", op.Note, 5, 2000)
	if err != nil {
		return SecurityAlert{}, err
	}
	a, _, err := s.SecurityAlert(ctx, op.ID)
	if err != nil {
		return SecurityAlert{}, err
	}
	if a.Status != "ACKNOWLEDGED" || a.EscalationStatus == "ESCALATED" {
		return SecurityAlert{}, ErrAlertState
	}
	incSev := map[string]string{"CRITICAL": "SEV1", "HIGH": "SEV2", "MEDIUM": "SEV3"}[a.Severity]
	if incSev == "" {
		incSev = "SEV4"
	}
	inc, err := s.DeclareIncident(ctx, op.StaffID, NewIncident{Title: truncate("Security alert: "+a.Title, 200), Severity: incSev,
		AffectedModule: a.SourceModule, AlertKey: "SEC_" + truncate(a.AlertKey, 60) + "_" + shortHash(a.DedupeKey), Note: note})
	if err != nil {
		return SecurityAlert{}, err
	}
	return s.alertAction(ctx, op, func(ctx context.Context, tx pgx.Tx, a SecurityAlert, now time.Time) error {
		if a.Status != "ACKNOWLEDGED" || a.EscalationStatus == "ESCALATED" {
			return ErrAlertState
		}
		if _, err := tx.Exec(ctx, `UPDATE app.ops_security_alerts SET escalation_status = 'ESCALATED', escalated_by = $2, escalated_at = $3,
			incident_id = $4, version = version + 1 WHERE id = $1`, a.ID, op.StaffID, now, inc.ID); err != nil {
			return err
		}
		if err := alertEvent(ctx, tx, a.ID, "ESCALATED", &op.StaffID, note, map[string]any{"incident_id": inc.ID}, now); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{Stream: audit.Security, Action: "operations.security_alert.escalated", TargetType: "security_alert",
			TargetID: a.ID, Metadata: map[string]any{"alert_key": a.AlertKey, "incident_id": inc.ID}})
	})
}

func shortHash(v string) string {
	h := sha256.Sum256([]byte(v))
	const hexd = "0123456789ABCDEF"
	out := make([]byte, 8)
	for i := 0; i < 4; i++ {
		out[2*i], out[2*i+1] = hexd[h[i]>>4], hexd[h[i]&0xf]
	}
	return string(out)
}

// ResolveAlert closes an acknowledged alert with a resolution note. If the condition is still present, the next scan
// opens a new alert for it (a resolution never hides a live condition).
func (s *Service) ResolveAlert(ctx context.Context, op AlertOp) (SecurityAlert, error) {
	note, err := text("note", op.Note, 5, 2000)
	if err != nil {
		return SecurityAlert{}, err
	}
	return s.alertAction(ctx, op, func(ctx context.Context, tx pgx.Tx, a SecurityAlert, now time.Time) error {
		if a.Status != "ACKNOWLEDGED" {
			return ErrAlertState
		}
		if _, err := tx.Exec(ctx, `UPDATE app.ops_security_alerts SET status = 'RESOLVED', resolved_by = $2, resolved_at = $3, resolution_note = $4,
			version = version + 1 WHERE id = $1`, a.ID, op.StaffID, now, note); err != nil {
			return err
		}
		if err := alertEvent(ctx, tx, a.ID, "RESOLVED", &op.StaffID, note, map[string]any{"condition_active": a.ConditionActive}, now); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{Stream: audit.Security, Action: "operations.security_alert.resolved", TargetType: "security_alert",
			TargetID: a.ID, Metadata: map[string]any{"alert_key": a.AlertKey, "condition_active": a.ConditionActive}})
	})
}

// BackupRun is one backup or restore-drill record.
type BackupRun struct {
	Kind           string         `json:"kind"`
	BackupID       string         `json:"backup_id"`
	Environment    string         `json:"environment"`
	Outcome        string         `json:"outcome"`
	StartedAt      time.Time      `json:"started_at"`
	FinishedAt     time.Time      `json:"finished_at"`
	ManifestSHA256 []byte         `json:"-"`
	Details        map[string]any `json:"details"`
}

// RecordBackupRun stores backup evidence (fundzimctl backup record). Idempotent for the same run.
func (s *Service) RecordBackupRun(ctx context.Context, r BackupRun) (bool, error) {
	d, err := json.Marshal(r.Details)
	if err != nil {
		return false, err
	}
	tag, err := s.Pool.Exec(ctx, `INSERT INTO app.ops_backup_runs (id, kind, backup_id, environment, outcome, started_at, finished_at, manifest_sha256, details)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9) ON CONFLICT (kind, backup_id, manifest_sha256, started_at) DO NOTHING`,
		ids.New(), r.Kind, r.BackupID, r.Environment, r.Outcome, r.StartedAt, r.FinishedAt, r.ManifestSHA256, d)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// BackupEvidenceSummary is the backup evidence shown with the alerts.
func (s *Service) BackupEvidenceSummary(ctx context.Context) (BackupEvidence, error) {
	return s.backupEvidence(ctx)
}
