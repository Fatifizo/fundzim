//go:build perf

// Stage 17 reconciliation performance (docs/stage-17/testing.md §4) against the scratch database fundzim_perf. Never
// run against the development database: need() refuses.
//
// What is measured: the pure matcher (1k / 10k / 100k lines, in memory), then the real import → parse → record path and
// a real run (matcher + every recon write in one transaction) at 1k / 10k / 100k lines, plus the exception queue and the
// dashboard queries. Two harness substitutions, both documented with the results: statement files are stored by an
// in-memory storage double that writes a CLEAN app.stored_objects row (no object store, no ClamAV — the scan is not
// part of this measurement), and payment facts come from an in-memory double (the matcher's input; payments' own
// queries are not measured here). The file cap is raised to 64 MiB for the 100k-line file (production cap: 10 MiB).
//
//	set -a; . ./.env; . tests/perf/env.sh; set +a
//	go test -tags perf -count=1 -timeout 60m -run TestReconciliationPerf -v ./tests/perf/
package perf

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Fatifizo/fundzim/internal/payments"
	"github.com/Fatifizo/fundzim/internal/platform/ids"
	"github.com/Fatifizo/fundzim/internal/reconciliation"
	"github.com/Fatifizo/fundzim/internal/storage"
)

// memStorage stores statement files in memory and records a CLEAN stored object (harness only).
type memStorage struct {
	mig  *pgxpool.Pool
	mu   sync.Mutex
	data map[string][]byte
}

func (m *memStorage) Upload(ctx context.Context, in storage.UploadInput) (storage.Object, error) {
	b, err := io.ReadAll(in.Body)
	if err != nil {
		return storage.Object{}, err
	}
	id := ids.New()
	sum := sha256.Sum256(b)
	steps := []string{
		`INSERT INTO app.stored_objects (id, bucket_class, purpose, owner_module, quarantine_key, declared_content_type, scan_status, classification,
		  retention_class, encryption_key_id) VALUES ($1::uuid, 'PRIVATE_EVIDENCE', 'RECONCILIATION_REPORT', 'reconciliation', 'quarantine/' || $1::text, $2,
		  'UPLOADED', 'C2', 'FINANCIAL', 'perf/harness')`,
		`UPDATE app.stored_objects SET scan_status = 'QUARANTINED', content_sha256 = $3, size_bytes = $4, sniffed_content_type = $2 WHERE id = $1::uuid`,
		`UPDATE app.stored_objects SET scan_status = 'SCANNING', scan_started_at = now(), scan_attempts = 1 WHERE id = $1::uuid AND $2::text <> ''`,
		`UPDATE app.stored_objects SET scan_status = 'CLEAN', promoted_key = 'objects/' || $1::text, promoted_at = now(), scan_engine = 'perf-harness',
		  scanned_at = now() WHERE id = $1::uuid AND $2::text <> ''`,
	}
	for i, q := range steps {
		args := []any{id, in.DeclaredContentType}
		if i == 1 {
			args = append(args, sum[:], len(b))
		}
		if _, err := m.mig.Exec(ctx, q, args...); err != nil {
			return storage.Object{}, fmt.Errorf("perf storage step %d: %w", i, err)
		}
	}
	m.mu.Lock()
	m.data[id] = b
	m.mu.Unlock()
	return storage.Object{ID: id, Status: storage.StatusClean, SHA256: sum[:], SizeBytes: int64(len(b))}, nil
}

func (m *memStorage) Get(_ context.Context, id string) (storage.Object, error) {
	return storage.Object{ID: id, Status: storage.StatusClean}, nil
}

func (m *memStorage) Open(_ context.Context, id string) (io.ReadCloser, storage.Object, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return io.NopCloser(bytes.NewReader(m.data[id])), storage.Object{ID: id, Status: storage.StatusClean}, nil
}

// fakePayments serves payment facts from memory (the matcher's input), keyed by id and provider reference.
type fakePayments struct {
	byID  map[string]payments.ReconFact
	byRef map[string]string
}

func (f *fakePayments) ReconFacts(_ context.Context, _ string, idsIn, refs []string) ([]payments.ReconFact, error) {
	seen := map[string]bool{}
	var out []payments.ReconFact
	for _, id := range idsIn {
		if p, ok := f.byID[id]; ok && !seen[id] {
			seen[id] = true
			out = append(out, p)
		}
	}
	for _, r := range refs {
		if id, ok := f.byRef[r]; ok && !seen[id] {
			seen[id] = true
			out = append(out, f.byID[id])
		}
	}
	return out, nil
}

func (f *fakePayments) ReconSucceededInWindow(context.Context, string, string, time.Time, time.Time, int) ([]payments.ReconFact, error) {
	return nil, nil
}

func (f *fakePayments) ApplyReconciliationOutcome(context.Context, payments.ReconOutcome) (payments.ReconApplyResult, error) {
	return payments.ReconApplyResult{}, nil
}

// statementCSV builds n lines: 90% exact matches, 5% amount differences, 5% references that name nothing.
func statementCSV(tok string, n int, from, to time.Time, f *fakePayments) []byte {
	var b bytes.Buffer
	b.WriteString("record_type,ref,kind,direction,status,amount_minor,fee_minor,net_minor,currency,provider_ref,merchant_ref,batch_ref,counterparty_ref," +
		"value_date,booked_at,period_start,period_end,opening_balance_minor,closing_balance_minor,declared_lines,gross_credits_minor,gross_debits_minor," +
		"fees_minor,account_ref\n")
	fmt.Fprintf(&b, "STATEMENT,PERF-%s,,,,,,,USD,,,,,,,%s,%s,,,%d,,,,\n", tok, from.Format(time.RFC3339), to.Format(time.RFC3339), n)
	at := from.Add(time.Minute)
	for i := 0; i < n; i++ {
		id, ref := ids.New(), fmt.Sprintf("perf_%s_%d", tok, i)
		amount := int64(1000 + i%5000)
		f.byID[id] = payments.ReconFact{ID: id, Status: "SUCCEEDED", AmountMinor: amount, Currency: "USD", ExecutionEnvironment: "LOCAL_SIMULATOR",
			ProviderReferences: []string{ref}, CreatedAt: at}
		f.byRef[ref] = id
		lineAmount, merchant := amount, id
		switch i % 20 {
		case 0:
			lineAmount = amount + 1 // amount mismatch
		case 1:
			merchant, ref = "", "nobody_"+ref // names nothing
		}
		fmt.Fprintf(&b, "LINE,L-%s-%d,PAYMENT,CREDIT,SUCCEEDED,%d,,,USD,%s,%s,,,,%s,,,,,,,,,\n", tok, i, lineAmount, ref, merchant,
			at.Add(time.Duration(i)*time.Millisecond).Format(time.RFC3339Nano))
	}
	return b.Bytes()
}

func heapMB() float64 {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return float64(m.HeapInuse) / (1 << 20)
}

func TestReconciliationPerf(t *testing.T) {
	need(t)
	ctx := context.Background()
	mig, err := pgxpool.New(ctx, mustEnv(t, "DATABASE_MIGRATION_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer mig.Close()
	s := start(t)
	defer s.close()
	if s.d.Reconciliation == nil {
		t.Fatal("reconciliation not wired")
	}
	svc := *s.d.Reconciliation
	fp := &fakePayments{byID: map[string]payments.ReconFact{}, byRef: map[string]string{}}
	svc.Storage, svc.Payments, svc.MaxFileBytes = &memStorage{mig: mig, data: map[string][]byte{}}, fp, 64<<20

	// two distinct staff users of the scratch dataset: maker and checker of the harness rule set
	var u1, u2 string
	if err := mig.QueryRow(ctx, `SELECT (array_agg(id::text ORDER BY id))[1], (array_agg(id::text ORDER BY id))[2] FROM (SELECT id FROM app.users LIMIT 2) x`).
		Scan(&u1, &u2); err != nil || u2 == "" {
		t.Fatalf("need two users in fundzim_perf: %v", err)
	}
	if _, ok, _ := svc.ActiveRuleSet(ctx); !ok {
		if _, err := mig.Exec(ctx, `INSERT INTO recon.matching_rule_sets (id, version_no, status, config, config_sha256, change_reason, proposed_by, proposed_at)
			VALUES (gen_random_uuid(), (SELECT coalesce(max(version_no), 0) + 1 FROM recon.matching_rule_sets), 'PENDING_APPROVAL',
			'{"date_window_days": 2, "settlement_horizon_days": 1, "stale_after_hours": 720}', sha256('perf'), 'perf harness rule set', $1, now())`, u1); err != nil {
			t.Fatal(err)
		}
		if _, err := mig.Exec(ctx, `UPDATE recon.matching_rule_sets SET status = 'ACTIVE', decided_by = $1, decided_at = now(), decision_note = 'perf',
			effective_from = now() WHERE status = 'PENDING_APPROVAL'`, u2); err != nil {
			t.Fatal(err)
		}
	}
	rules, _, _ := svc.ActiveRuleSet(ctx)

	// 1. the pure matcher
	fmt.Printf("\n| matcher lines | decisions | ms | lines/s | heap MB after |\n|---:|---:|---:|---:|---:|\n")
	for _, n := range []int{1_000, 10_000, 100_000} {
		var items []reconciliation.Item
		var subjects []reconciliation.Subject
		at := time.Now().UTC().Add(-time.Hour)
		for i := 0; i < n; i++ {
			id, ref := ids.New(), fmt.Sprintf("m_%d", i)
			subjects = append(subjects, reconciliation.Subject{Type: "PAYMENT", ID: id, Status: "SUCCEEDED", Amount: 1000, Currency: "USD",
				ProviderRefs: []string{ref}, At: at, Test: true})
			items = append(items, reconciliation.Item{ID: ids.New(), Kind: "PAYMENT", Direction: "CREDIT", Status: "SUCCEEDED", Amount: 1000 + int64(i%20/19),
				Currency: "USD", RawCurrency: "USD", MerchantRef: id, ProviderRef: ref, BookedAt: at.Add(time.Minute)})
		}
		runtime.GC()
		t0 := time.Now()
		res := reconciliation.Match(reconciliation.MatchInput{SourceID: "perf", SourceKind: reconciliation.KindTransactionReport, Currency: "USD",
			Rules: rules, Now: time.Now().UTC(), Items: items, Payments: subjects, Fees: map[string]reconciliation.FeeExpectation{}})
		d := time.Since(t0)
		fmt.Printf("| %d | %d | %.1f | %.0f | %.1f |\n", n, len(res.Decisions), float64(d.Microseconds())/1000, float64(n)/d.Seconds(), heapMB())
	}

	// 2. import → parse → run through the database
	fmt.Printf("\n| lines | file MB | upload ms | parse+record ms | run ms | lines in run | matched | exceptions opened | heap MB |\n" +
		"|---:|---:|---:|---:|---:|---:|---:|---:|---:|\n")
	for _, n := range []int{1_000, 10_000, 100_000} {
		tok := ids.New()[24:]
		now := time.Now().UTC().Truncate(time.Second)
		content := statementCSV(tok, n, now.Add(-2*time.Hour), now, fp)
		t0 := time.Now()
		imp, err := svc.Upload(ctx, u1, reconciliation.UploadInput{SourceCode: "sandbox_transactions_test", MediaType: "text/csv", Filename: "perf.csv",
			Content: content})
		if err != nil {
			t.Fatal(err)
		}
		up := time.Since(t0)
		t1 := time.Now()
		if done, err := svc.ParseImport(ctx, imp.ID); err != nil || !done {
			t.Fatalf("parse: %v %v", done, err)
		}
		parse := time.Since(t1)
		if got, _ := svc.ImportByID(ctx, imp.ID); got.Status != "PARSED" {
			t.Fatalf("import %s %v %+v", got.Status, got.FailureCode, got.ValidationReport)
		}
		run, err := svc.RequestRun(ctx, u1, reconciliation.RunInput{Source: "sandbox_transactions_test", Currency: "USD", Reason: "perf harness run"})
		if err != nil {
			t.Fatal(err)
		}
		t2 := time.Now()
		if err := svc.ExecuteRun(ctx, run.ID); err != nil {
			t.Fatal(err)
		}
		rd := time.Since(t2)
		run, _ = svc.RunByID(ctx, run.ID)
		if run.Status != "COMPLETED" {
			t.Fatalf("run %s %v", run.Status, run.FailureCode)
		}
		fmt.Printf("| %d | %.1f | %d | %d | %d | %d | %d | %d | %.1f |\n", n, float64(len(content))/(1<<20), up.Milliseconds(), parse.Milliseconds(),
			rd.Milliseconds(), run.ItemsConsidered, run.ItemsMatched, run.ExceptionsOpened, heapMB())
	}

	// 3. read models at the final volume (p50 / p95 of 50 calls each)
	measure := func(name string, f func() error) {
		var ds []time.Duration
		for i := 0; i < 50; i++ {
			t0 := time.Now()
			if err := f(); err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			ds = append(ds, time.Since(t0))
		}
		sortDurations(ds)
		fmt.Printf("| %s | %.1f | %.1f |\n", name, float64(ds[len(ds)/2].Microseconds())/1000, float64(ds[len(ds)*95/100].Microseconds())/1000)
	}
	fmt.Printf("\n| query | p50 ms | p95 ms |\n|---|---:|---:|\n")
	measure("exception queue (open, 200)", func() error {
		_, err := svc.Exceptions(ctx, reconciliation.ExceptionFilter{OpenOnly: true, Limit: 200})
		return err
	})
	measure("exception queue (CRITICAL)", func() error {
		_, err := svc.Exceptions(ctx, reconciliation.ExceptionFilter{Severity: "CRITICAL", Limit: 200})
		return err
	})
	measure("dashboard overview", func() error { _, err := svc.Overview(ctx); return err })
	measure("report unmatched_lines", func() error { _, err := svc.BuildReport(ctx, "unmatched_lines", time.Time{}, time.Time{}); return err })
	measure("report exception_ageing", func() error { _, err := svc.BuildReport(ctx, "exception_ageing", time.Time{}, time.Time{}); return err })
	var size string
	_ = mig.QueryRow(ctx, `SELECT pg_size_pretty(sum(pg_total_relation_size(c.oid))) FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = 'recon' AND c.relkind = 'r'`).Scan(&size)
	fmt.Printf("\nrecon schema size: %s\n", size)
}

func sortDurations(d []time.Duration) {
	for i := 1; i < len(d); i++ {
		for j := i; j > 0 && d[j] < d[j-1]; j-- {
			d[j], d[j-1] = d[j-1], d[j]
		}
	}
}
