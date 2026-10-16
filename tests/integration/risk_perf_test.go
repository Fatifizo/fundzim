//go:build integration

package integration

// Stage 13 risk performance (docs/stage-13/testing.md §perf). Opt-in: FUNDZIM_IT_PERF=1 (it writes signals, evaluations,
// cases and holds into the development database; its rules match only its own reason code and are retired at the end).
// Every number is measured; the table is printed and written to $FUNDZIM_PERF_OUT/risk-perf.md when set.

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/Fatifizo/fundzim/internal/platform/ids"
	"github.com/Fatifizo/fundzim/internal/risk"
)

func TestRiskPerf(t *testing.T) {
	if os.Getenv("FUNDZIM_IT_PERF") != "1" {
		t.Skip("set FUNDZIM_IT_PERF=1")
	}
	r := newRiskIT(t)
	const workers = 8
	code := itCode("ITP")
	// five ACTIVE rules on the CAMPAIGN subject type: every campaign signal is evaluated against all of them
	for i := 0; i < 4; i++ {
		r.rule(t, "CAMPAIGN", "CAMPAIGN_RISK", reportsRule(code, 50+i), 10, "REVIEW_REQUIRED", nil, "P3")
	}
	r.rule(t, "CAMPAIGN", "CAMPAIGN_RISK", map[string]any{"when": map[string]any{"any": []any{
		map[string]any{"distinct_count": map[string]any{"signal_types": []string{"CAMPAIGN_REPORTED"}, "fact": "reason_code", "window_seconds": 86400, "op": ">=", "value": 3}},
		map[string]any{"sequence": map[string]any{"signal_types": []string{"CAMPAIGN_REPORTED", "CAMPAIGN_REVIEW_ESCALATED"}, "window_seconds": 86400}},
	}}}, 30, "TEMPORARY_HOLD", map[string]any{"hold_type": "WITHDRAWAL_HOLD", "hold_duration_seconds": 3600}, "P2")
	camps := make([]string, workers)
	for i := range camps {
		camps[i] = r.campaign(t, 0)
	}
	signal := func(camp, typ, reason string) error {
		_, err := r.rs.RecordSignal(ctx(t), risk.Signal{Type: typ, SubjectType: "CAMPAIGN", SubjectID: camp, SourceModule: "campaigns",
			SourceEventID: ids.New(), SourceEventType: "campaigns.report_escalated", ObservedAt: time.Now().UTC(), Facts: map[string]any{"reason_code": reason}})
		return err
	}
	var out string
	add := func(res poRes) { out += res.row(); t.Log(res.row()) }
	header := "| scenario | c | n | p50 ms | p95 ms | max ms | ops/s | errors |\n|---|---:|---:|---:|---:|---:|---:|---:|\n"
	for _, c := range []int{1, workers} {
		add(poMeasure(fmt.Sprintf("signal → evaluation (5 active rules, no action), c=%d", c), 400, c, func(w, i int) error {
			return signal(camps[w%len(camps)], risk.SigCampaignReported, "ITP_OTHER")
		}))
	}
	add(poMeasure("signal → evaluation → case + automated hold (first) / link (rest)", 200, workers, func(w, i int) error {
		return signal(camps[w%len(camps)], risk.SigCampaignReported, fmt.Sprintf("%s_%d", code, i%4))
	}))
	add(poMeasure("hold lookup at an enforcement point (WithdrawalHolds)", 2000, workers, func(w, i int) error {
		_, err := r.rs.WithdrawalHolds(ctx(t), risk.WithdrawalScope{CampaignID: camps[w%len(camps)], OwnerUserID: r.owner.id, DestinationID: r.dest})
		return err
	}))
	add(poMeasure("case queue (open, 100)", 300, workers, func(w, i int) error {
		_, err := r.rs.Cases(ctx(t), risk.CaseFilter{Status: "OPEN_ANY", Limit: 100})
		return err
	}))
	add(poMeasure("evaluation list (100)", 300, workers, func(w, i int) error {
		_, err := r.rs.Evaluations(ctx(t), "", "", "", 100)
		return err
	}))
	add(poMeasure("risk overview", 100, workers, func(w, i int) error {
		_, err := r.rs.RiskOverview(ctx(t))
		return err
	}))
	add(poMeasure("risk reconciliation run", 3, 1, func(w, i int) error {
		_, err := r.rs.Reconcile(ctx(t), "perf")
		return err
	}))
	// release the perf holds through the normal maker-checker path
	for _, camp := range camps {
		hs, _ := r.rs.Holds(ctx(t), risk.HoldFilter{Status: "OPEN", SubjectType: "CAMPAIGN", SubjectID: camp})
		for _, h := range hs {
			_, _ = r.rs.RequestRelease(ctx(t), risk.HoldOp{StaffID: r.f.compliance.id, HoldID: h.ID, Reason: "perf cleanup release"})
			_, _ = r.rs.DecideRelease(ctx(t), risk.HoldOp{StaffID: r.f.compliance2.id, StepUpAt: time.Now(), HoldID: h.ID, Reason: "perf cleanup release"}, true)
		}
	}
	var signals, evals, cases, holds int
	_ = r.mig.QueryRow(ctx(t), `SELECT (SELECT count(*) FROM risk.risk_signals), (SELECT count(*) FROM risk.risk_evaluations), (SELECT count(*) FROM risk.risk_cases),
		(SELECT count(*) FROM risk.risk_holds)`).Scan(&signals, &evals, &cases, &holds)
	md := fmt.Sprintf("## Risk performance\n\nmeasured %s UTC; GOMAXPROCS=%d; development stack (compose PostgreSQL 17.11, default settings); "+
		"in-process service calls; dataset after the run: %d signals, %d evaluations, %d cases, %d holds.\n\n",
		time.Now().UTC().Format(time.RFC3339), runtime.GOMAXPROCS(0), signals, evals, cases, holds) + header + out
	fmt.Println(md)
	if dir := os.Getenv("FUNDZIM_PERF_OUT"); dir != "" {
		_ = os.MkdirAll(dir, 0o755)
		_ = os.WriteFile(filepath.Join(dir, "risk-perf.md"), []byte(md), 0o644)
	}
}
