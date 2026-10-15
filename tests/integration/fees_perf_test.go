//go:build integration

package integration

// Stage 12 fee performance (docs/stage-12/testing.md §perf). Opt-in: FUNDZIM_IT_PERF=1 (it writes quotes, fixture
// assessments and TEST_ONLY journals into the development database). Every number is measured; the table is printed
// and written to $FUNDZIM_PERF_OUT/fees-perf.md when set.

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/Fatifizo/fundzim/internal/fees"
)

func TestFeesPerf(t *testing.T) {
	if os.Getenv("FUNDZIM_IT_PERF") != "1" {
		t.Skip("set FUNDZIM_IT_PERF=1")
	}
	f := newFeeIT(t, 0)
	const workers = 8
	f.policy(t, "PLATFORM_FEE", map[string]any{"campaign_id": f.camp}, platformTerms(500, "ORGANISER_COVERED"))
	f.policy(t, "PAYMENT_PROCESSING_FEE", map[string]any{"campaign_id": f.camp}, passThrough("ORGANISER_COVERED"))
	f.schedule(t, "TRANSACTION", map[string]any{"model": "PERCENTAGE", "rate_bps": 200})
	donors := make([]string, workers)
	for i := range donors {
		donors[i] = verifiedUser(t, f.s, fmt.Sprintf("feeperf%d", i)).id
	}
	var out string
	add := func(r poRes) { out += r.row(); t.Log(r.row()) }
	header := "| scenario | c | n | p50 ms | p95 ms | max ms | ops/s | errors |\n|---|---:|---:|---:|---:|---:|---:|---:|\n"
	method := "ECOCASH"
	for _, c := range []int{1, workers} {
		add(poMeasure(fmt.Sprintf("fee quote (policy selection + exact calculation + store), c=%d", c), 400, c, func(w, i int) error {
			_, err := f.fs.CreateQuote(ctx(t), donors[w], "", fees.QuoteRequest{CampaignID: f.camp, AmountMinor: fmt.Sprint(1000 + i), Currency: "USD",
				PaymentMethod: &method})
			return err
		}))
	}
	add(poMeasure("policy lookup (Resolve: platform fee, most specific scope)", 1000, workers, func(w, i int) error {
		_, err := f.fs.Resolve(ctx(t), nil, fees.Scope{Category: fees.CatPlatform, TransactionType: fees.TxDonation, Currency: "USD",
			PaymentMethod: "ECOCASH", CampaignID: f.camp, CampaignCategory: "EDUCATION", At: time.Now()})
		return err
	}))
	add(poMeasure("fee assessment + fee journals (fixture donation: 2 assessments, capture + provider-fee journals)", 200, workers, func(w, i int) error {
		_, err := f.fs.RunFixture(ctx(t), f.f.ledgerAdmin.id, fees.FixtureRequest{Scenario: fees.FixtureDonation, CampaignID: f.camp,
			AmountMinor: fmt.Sprint(5000 + i), Currency: "USD", PaymentMethod: &method})
		return err
	}))
	add(poMeasure("staff policy list", 200, workers, func(w, i int) error {
		_, err := f.fs.Policies(ctx(t), fees.PolicyFilter{})
		return err
	}))
	add(poMeasure("staff assessment list (100)", 300, workers, func(w, i int) error {
		_, err := f.fs.Assessments(ctx(t), fees.AssessmentFilter{Limit: 100})
		return err
	}))
	add(poMeasure("staff quote list (50)", 300, workers, func(w, i int) error {
		_, err := f.fs.Quotes(ctx(t), "", 50)
		return err
	}))
	from := time.Now().AddDate(0, -12, 0)
	add(poMeasure("revenue report (12 months, per currency)", 50, workers, func(w, i int) error {
		_, err := f.fs.Revenue(ctx(t), from, time.Now())
		return err
	}))
	add(poMeasure("fee reconciliation run (latest ≤ 5000 assessments)", 3, 1, func(w, i int) error {
		_, err := f.fs.Reconcile(ctx(t), "perf")
		return err
	}))
	var quotes, assessments, policies, journals int
	_ = f.mig.QueryRow(ctx(t), `SELECT (SELECT count(*) FROM app.fee_quotes), (SELECT count(*) FROM app.fee_assessments), (SELECT count(*) FROM app.fee_policies),
		(SELECT count(*) FROM ledger.ledger_transactions WHERE source_system = 'fees')`).Scan(&quotes, &assessments, &policies, &journals)
	md := fmt.Sprintf("## Fee performance\n\nmeasured %s UTC; GOMAXPROCS=%d; development stack (compose PostgreSQL 17.11, default settings); "+
		"in-process service calls; dataset after the run: %d fee quotes, %d assessments, %d fee policies, %d fee ledger journals.\n\n",
		time.Now().UTC().Format(time.RFC3339), runtime.GOMAXPROCS(0), quotes, assessments, policies, journals) + header + out
	fmt.Println(md)
	if dir := os.Getenv("FUNDZIM_PERF_OUT"); dir != "" {
		_ = os.MkdirAll(dir, 0o755)
		_ = os.WriteFile(filepath.Join(dir, "fees-perf.md"), []byte(md), 0o644)
	}
}
