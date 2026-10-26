//go:build integration

package integration

// Stage 18 security alerting (docs/stage-18/security-monitoring.md): a scan raises one deduplicated alert per condition,
// marks it cleared and recurred, people acknowledge, escalate and resolve it, the lifecycle is database-guarded and the
// API is permissioned. The test's scans read only its own source and clear only its own module (ClearScope), because
// the compose worker scans the shared database every minute.

import (
	"context"
	"encoding/json"
	"math/rand"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/Fatifizo/fundzim/internal/operations"
	"github.com/Fatifizo/fundzim/internal/platform/clock"
	"github.com/Fatifizo/fundzim/internal/platform/opsview"
)

func TestSecurityAlertsLifecycle(t *testing.T) {
	need(t, "DATABASE_URL", "DATABASE_MIGRATION_URL")
	letters := make([]byte, 8)
	for i := range letters {
		letters[i] = byte('a' + rand.Intn(26))
	}
	mod := "it_sec_" + string(letters) // module names are lower-case letters and underscores (CHECK)
	gateKey := mod + ".live_execution"
	var mu sync.Mutex
	reporting := true
	src := operations.Source{Module: mod, Snapshot: func(context.Context) (opsview.Snapshot, error) {
		mu.Lock()
		defer mu.Unlock()
		snap := opsview.Snapshot{Module: mod}
		if reporting {
			snap.Metrics = []opsview.Metric{{Key: gateKey, Label: "IT gate", Module: mod, Status: opsview.StatusUnknown, Nature: opsview.Unknown,
				Source: "integration test"}}
		}
		return snap, nil
	}}
	app := pool(t, os.Getenv("DATABASE_URL"))
	scanner := &operations.Service{Pool: app, Clock: clock.System, Sources: []operations.Source{src}, ClearScope: []string{mod}}
	dedupe := "FINANCIAL_GATE_NOT_BLOCKED:" + gateKey
	mig := pool(t, os.Getenv("DATABASE_MIGRATION_URL"))
	state := func() (id, status string, active bool, occ, version int) {
		if err := mig.QueryRow(ctx(t), `SELECT id::text, status, condition_active, occurrences, version FROM app.ops_security_alerts
			WHERE dedupe_key = $1 ORDER BY created_at DESC LIMIT 1`, dedupe).Scan(&id, &status, &active, &occ, &version); err != nil {
			t.Fatalf("alert for %s: %v", dedupe, err)
		}
		return
	}
	events := func(id string) map[string]int {
		rows, err := mig.Query(ctx(t), `SELECT event_type FROM app.ops_security_alert_events WHERE alert_id = $1`, id)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		out := map[string]int{}
		for rows.Next() {
			var e string
			_ = rows.Scan(&e)
			out[e]++
		}
		return out
	}

	// two concurrent scans raise exactly one alert (advisory lock + one live alert per condition)
	var wg sync.WaitGroup
	scanErrs := make([]error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) { defer wg.Done(); _, scanErrs[i] = scanner.SecurityScan(context.Background()) }(i)
	}
	wg.Wait()
	for _, err := range scanErrs {
		if err != nil {
			t.Fatalf("concurrent scan: %v", err)
		}
	}
	var live int
	if err := mig.QueryRow(ctx(t), `SELECT count(*) FROM app.ops_security_alerts WHERE dedupe_key = $1 AND status <> 'RESOLVED'`, dedupe).Scan(&live); err != nil || live != 1 {
		t.Fatalf("live alerts for one condition: %d %v", live, err)
	}
	id, status, active, occ, _ := state()
	if status != "OPEN" || !active || occ != 1 {
		t.Fatalf("raised alert: %s active=%v occurrences=%d", status, active, occ)
	}
	var sev string
	_ = mig.QueryRow(ctx(t), `SELECT severity FROM app.ops_security_alerts WHERE id = $1`, id).Scan(&sev)
	if sev != "CRITICAL" {
		t.Fatalf("a production gate not reported BLOCKED must be CRITICAL, got %s", sev)
	}
	// the condition goes away, then comes back
	mu.Lock()
	reporting = false
	mu.Unlock()
	if _, err := scanner.SecurityScan(ctx(t)); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	reporting = true
	mu.Unlock()
	if _, err := scanner.SecurityScan(ctx(t)); err != nil {
		t.Fatal(err)
	}
	if ev := events(id); ev["RAISED"] != 1 || ev["CONDITION_CLEARED"] != 1 || ev["RECURRED"] != 1 {
		t.Fatalf("alert history: %v", ev)
	}
	if _, _, _, occ, _ := state(); occ != 2 {
		t.Fatalf("occurrences after recurrence: %d", occ)
	}

	// the lifecycle is guarded in the database: OPEN → RESOLVED directly is refused even for the table owner
	if _, err := mig.Exec(ctx(t), `UPDATE app.ops_security_alerts SET status = 'RESOLVED', resolved_by = acknowledged_by WHERE id = $1`, id); err == nil {
		t.Fatal("OPEN → RESOLVED was allowed")
	}
	if _, err := mig.Exec(ctx(t), `DELETE FROM app.ops_security_alert_events WHERE alert_id = $1`, id); err == nil {
		t.Fatal("alert history was deletable")
	}

	// API: permissions, acknowledge, escalate, resolve
	s := newITServer(t, nil)
	f := staffFixture(t, s)
	admin := s.browser(t) // ADMIN: security.alert.manage
	admin.staffLogin(f.ledgerAdmin.email, f.ledgerAdmin.secret)
	support := s.browser(t)
	support.staffLogin(f.support.email, f.support.secret)
	support.expect(support.do("GET", "/admin/security/alerts", nil), 403, "PERMISSION_DENIED")
	user := s.browser(t)
	userEmail := uniqueEmail("sec-user")
	user.registerVerified(userEmail)
	user.mustLogin(userEmail, itPassword)
	user.expect(user.do("GET", "/admin/security/alerts", nil), 404, "")

	r := admin.do("GET", "/admin/security/alerts?status=LIVE&min_severity=CRITICAL", nil)
	admin.expect(r, 200, "")
	var page struct {
		Items []operations.SecurityAlert `json:"items"`
	}
	_ = json.Unmarshal(r.Data, &page)
	found := false
	for _, a := range page.Items {
		found = found || a.ID == id
		if a.Severity != "CRITICAL" {
			t.Fatalf("min_severity filter returned %s", a.Severity)
		}
	}
	if !found {
		t.Fatal("the alert is not in the live CRITICAL queue")
	}
	_, _, _, _, version := state()
	admin.expect(admin.do("POST", "/admin/security/alerts/"+id+"/resolve", map[string]any{"note": "resolving before acknowledging", "version": version}),
		409, "SECURITY_ALERT_STATE")
	admin.expect(admin.do("POST", "/admin/security/alerts/"+id+"/acknowledge", map[string]any{"version": version + 99}), 409, "OPS_RECORD_CHANGED")
	admin.expect(admin.do("POST", "/admin/security/alerts/"+id+"/acknowledge", map[string]any{"note": "looking", "version": version}), 200, "")
	admin.expect(admin.do("POST", "/admin/security/alerts/"+id+"/acknowledge", map[string]any{}), 409, "SECURITY_ALERT_STATE")
	admin.expect(admin.do("POST", "/admin/security/alerts/"+id+"/escalate", map[string]any{"note": "no"}), 422, "VALIDATION_FAILED")
	r = admin.do("POST", "/admin/security/alerts/"+id+"/escalate", map[string]any{"note": "IT: gate reported not blocked"})
	admin.expect(r, 200, "")
	var esc operations.SecurityAlert
	_ = json.Unmarshal(r.Data, &esc)
	if esc.EscalationStatus != "ESCALATED" || esc.IncidentID == nil {
		t.Fatalf("escalation: %+v", esc)
	}
	admin.expect(admin.do("POST", "/admin/security/alerts/"+id+"/resolve", map[string]any{"note": "IT: resolved after review"}), 200, "")
	r = admin.do("GET", "/admin/security/alerts/"+id, nil)
	admin.expect(r, 200, "")
	if ev := events(id); ev["ACKNOWLEDGED"] != 1 || ev["ESCALATED"] != 1 || ev["RESOLVED"] != 1 {
		t.Fatalf("history after the lifecycle: %v", ev)
	}
	var incStatus string
	_ = mig.QueryRow(ctx(t), `SELECT status FROM app.ops_incidents WHERE id = $1`, *esc.IncidentID).Scan(&incStatus)
	if incStatus != "DETECTED" {
		t.Fatalf("escalation incident status %q", incStatus)
	}

	// still present after resolution: the next scan opens a NEW alert (a resolution never hides a live condition)
	if _, err := scanner.SecurityScan(ctx(t)); err != nil {
		t.Fatal(err)
	}
	if newID, status, _, _, _ := state(); newID == id || status != "OPEN" {
		t.Fatalf("a live condition after resolution must open a new alert (got %s %s)", newID, status)
	}
	// clean up: the condition goes away (cleared; it stays OPEN for a person, as in production)
	mu.Lock()
	reporting = false
	mu.Unlock()
	_, _ = scanner.SecurityScan(ctx(t))
	r = admin.do("GET", "/admin/security/backups", nil)
	admin.expect(r, 200, "")
}

// Remediation phase 3 (CI run 40300772129): scans take their observation time before they serialise on the advisory
// lock, so two concurrent scans may write in the opposite order of their times. The scan with the earlier time must
// neither fail (ck_ops_security_alerts_counts: last_seen_at >= first_seen_at) nor move last_seen_at or the evidence back.
func TestSecurityAlertsOutOfOrderScanTimes(t *testing.T) {
	need(t, "DATABASE_MIGRATION_URL")
	letters := make([]byte, 8)
	for i := range letters {
		letters[i] = byte('a' + rand.Intn(26))
	}
	mod := "it_sec_ooo_" + string(letters) // lower-case letters and underscores (CHECK)
	gateKey := mod + ".live_execution"
	src := operations.Source{Module: mod, Snapshot: func(context.Context) (opsview.Snapshot, error) {
		return opsview.Snapshot{Module: mod, Metrics: []opsview.Metric{{Key: gateKey, Label: "IT gate", Module: mod, Status: opsview.StatusUnknown,
			Nature: opsview.Unknown, Source: "integration test"}}}, nil
	}}
	app := pool(t, os.Getenv("DATABASE_URL"))
	mig := pool(t, os.Getenv("DATABASE_MIGRATION_URL"))
	later := time.Now().UTC().Truncate(time.Second).Add(time.Hour)
	earlier := later.Add(-time.Minute)
	scan := func(at time.Time) {
		t.Helper()
		s := &operations.Service{Pool: app, Clock: clock.NewFake(at), Sources: []operations.Source{src}, ClearScope: []string{mod}}
		if _, err := s.SecurityScan(ctx(t)); err != nil {
			t.Fatalf("scan at %s: %v", at.Format(time.RFC3339), err)
		}
	}
	scan(later)   // the later observation commits first
	scan(earlier) // the earlier observation writes afterwards
	var first, last time.Time
	var observed string
	if err := mig.QueryRow(ctx(t), `SELECT first_seen_at, last_seen_at, evidence->>'observed_at' FROM app.ops_security_alerts
		WHERE dedupe_key = $1 AND status <> 'RESOLVED'`, "FINANCIAL_GATE_NOT_BLOCKED:"+gateKey).Scan(&first, &last, &observed); err != nil {
		t.Fatal(err)
	}
	if !first.Equal(later) || !last.Equal(later) || observed != later.Format(time.RFC3339) {
		t.Fatalf("out-of-order scan moved the alert back: first %s last %s evidence %s (want %s)", first, last, observed, later)
	}
}
