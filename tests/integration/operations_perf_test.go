//go:build integration

package integration

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/Fatifizo/fundzim/internal/app"
	"github.com/Fatifizo/fundzim/internal/operations"
)

// TestOperationsPerf measures the operations portal with in-process service calls on the server's own composition
// (app.NewOperations: every module snapshot, the probes, permissions). Opt-in: FUNDZIM_IT_PERF=1; FUNDZIM_PERF_OUT
// writes operations-perf.md. Measured, never estimated.
func TestOperationsPerf(t *testing.T) {
	if os.Getenv("FUNDZIM_IT_PERF") != "1" {
		t.Skip("set FUNDZIM_IT_PERF=1")
	}
	o := newOpsIT(t)
	const workers = 8
	ops := app.NewOperations(o.s.d)
	comp, err := ops.ViewerFor(ctx(t), o.f.compliance.id)
	if err != nil {
		t.Fatal(err)
	}
	var out string
	add := func(res poRes) { out += res.row(); t.Log(res.row()) }
	header := "| scenario | c | n | p50 ms | p95 ms | max ms | ops/s | errors |\n|---|---:|---:|---:|---:|---:|---:|---:|\n"
	for _, src := range ops.Sources {
		snap := src.Snapshot
		add(poMeasure("snapshot: "+src.Module, 50, 1, func(w, i int) error { _, err := snap(ctx(t)); return err }))
	}
	for _, c := range []int{1, workers} {
		add(poMeasure(fmt.Sprintf("command center overview (all module snapshots), c=%d", c), 100, c, func(w, i int) error {
			_, err := ops.Overview(ctx(t), comp)
			return err
		}))
	}
	add(poMeasure("viewer permissions (EffectivePermissions)", 500, workers, func(w, i int) error {
		_, err := ops.ViewerFor(ctx(t), o.f.compliance.id)
		return err
	}))
	add(poMeasure("approval center (all modules)", 200, workers, func(w, i int) error {
		_, _, err := ops.Approvals(ctx(t), comp, operations.ApprovalFilter{})
		return err
	}))
	add(poMeasure("exception center (all modules)", 200, workers, func(w, i int) error {
		_, _, err := ops.Exceptions(ctx(t), comp, operations.ExceptionFilter{})
		return err
	}))
	add(poMeasure("platform health (probes measured now)", 100, workers, func(w, i int) error {
		_, err := ops.Health(ctx(t))
		return err
	}))
	add(poMeasure("global search (uuid)", 200, workers, func(w, i int) error {
		_, err := ops.Search(ctx(t), comp, o.owner.id)
		return err
	}))
	add(poMeasure("support case queue (50)", 300, workers, func(w, i int) error {
		_, _, err := ops.SupportCases(ctx(t), operations.SupportFilter{Status: "OPEN_ANY", Limit: 50})
		return err
	}))
	add(poMeasure("reports (30 days)", 50, workers, func(w, i int) error {
		_, err := ops.Reports(ctx(t), comp, "", nil, nil)
		return err
	}))
	var audits, cases int
	_ = o.mig.QueryRow(ctx(t), `SELECT (SELECT count(*) FROM audit.audit_events), (SELECT count(*) FROM app.ops_support_cases)`).Scan(&audits, &cases)
	md := fmt.Sprintf("## Operations portal performance\n\nmeasured %s UTC; GOMAXPROCS=%d; development stack (compose PostgreSQL 17.11, default settings); "+
		"in-process service calls on the server's composition (app.NewOperations); dataset: %d audit events, %d support cases.\n\n",
		time.Now().UTC().Format(time.RFC3339), runtime.GOMAXPROCS(0), audits, cases) + header + out
	fmt.Println(md)
	if dir := os.Getenv("FUNDZIM_PERF_OUT"); dir != "" {
		_ = os.MkdirAll(dir, 0o755)
		_ = os.WriteFile(filepath.Join(dir, "operations-perf.md"), []byte(md), 0o644)
	}
}
