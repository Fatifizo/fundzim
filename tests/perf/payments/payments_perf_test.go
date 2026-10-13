//go:build integration && perf

// Stage 8 payment-layer performance harness (docs/stage-8/performance.md).
//
// This file is compiled INTO the integration test package (tests/integration) through the go command's -overlay
// flag, so it reuses the integration fixtures exactly as tests/integration/payments_test.go does: newITServer with the
// sandbox SIMULATOR enabled, the shared staff fixture (FINANCE officer), verified donor accounts and real published
// campaigns. It lives under tests/perf/ (like the Stage 7 harness) and never runs in normal CI: it needs both build
// tags `integration` and `perf`, the overlay, and FUNDZIM_PERF_PAYMENTS=1.
//
// Run from the repository root, holding the shared-stack lock (each test is a separate, short locked run):
//
//	export PATH="$HOME/.local/go/bin:$PATH"; set -a; . ./.env; set +a
//	flock /tmp/claude-1000/fundzim-stack.lock env FUNDZIM_PERF_PAYMENTS=1 FUNDZIM_PERF_OUT=/tmp/payperf \
//	  go test -tags integration,perf -overlay tests/perf/payments/overlay.json -count=1 -timeout 20m \
//	  -run '^TestPaymentsPerfDonorAPI$' -v ./tests/integration/
//	(likewise TestPaymentsPerfWebhooks, TestPaymentsPerfStaffReads)
//
// Rate limits stay ON (the real Valkey-backed limiter is part of every measured request). Per-donor and
// per-campaign limits are respected by spreading the load over many donors and campaigns; the per-IP limits
// (global.ip 20/s burst 40, payments.webhook.ip 600/min) are kept out of the latency rows by giving every request
// its own client IP (X-Forwarded-For through the trusted loopback proxy, as the integration browser does) and are
// measured separately in explicit "one client IP" rows. Every non-expected status (including 429) is counted.
//
// The provider is the in-process sandbox simulator: provider latency is ~0 here; a real PSP's latency would
// dominate the attempt path in production.
package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Fatifizo/fundzim/internal/platform/config"
	"github.com/Fatifizo/fundzim/internal/platform/ids"
	"github.com/Fatifizo/fundzim/internal/platform/outbox"
	"github.com/Fatifizo/fundzim/internal/psp"
	"github.com/Fatifizo/fundzim/internal/psp/sandbox"
)

func perfGate(t *testing.T) {
	t.Helper()
	if os.Getenv("FUNDZIM_PERF_PAYMENTS") != "1" {
		t.Skip("set FUNDZIM_PERF_PAYMENTS=1 (payments performance harness)")
	}
	need(t, "SECRETREF_FUNDZIM_SANDBOX_WEBHOOK_CURRENT", "SECRETREF_FUNDZIM_SANDBOX_API", "DATABASE_MIGRATION_URL")
}

// ---- measurement ---------------------------------------------------------------------------------------------

type pres struct {
	name                 string
	n, conc              int
	p50, p95, p99, max   time.Duration
	wall                 time.Duration
	rps                  float64
	codes                map[int]int
	errors, throttled    int // errors: transport failures + any status other than want (429 included); throttled: 429s
	bytes                int64
}

func ppct(d []time.Duration, p float64) time.Duration {
	if len(d) == 0 {
		return 0
	}
	i := int(float64(len(d))*p+0.999999) - 1
	if i < 0 {
		i = 0
	}
	if i >= len(d) {
		i = len(d) - 1
	}
	return d[i]
}

// pmeasure runs n operations with conc workers (closed loop) and returns client-observed latency percentiles.
// f returns the HTTP status (0 on a transport error) and the response size.
func pmeasure(name string, n, conc, want int, f func(i int) (int, int, error)) pres {
	lat := make([]time.Duration, n)
	codes := make([]int, n)
	var next, bytesN atomic.Int64
	var wg sync.WaitGroup
	t0 := time.Now()
	for w := 0; w < conc; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				i := int(next.Add(1)) - 1
				if i >= n {
					return
				}
				s := time.Now()
				code, b, err := f(i)
				lat[i] = time.Since(s)
				if err != nil {
					code = 0
				}
				codes[i] = code
				bytesN.Store(int64(b))
			}
		}()
	}
	wg.Wait()
	wall := time.Since(t0)
	r := pres{name: name, n: n, conc: conc, wall: wall, rps: float64(n) / wall.Seconds(), codes: map[int]int{}, bytes: bytesN.Load()}
	for _, c := range codes {
		r.codes[c]++
		if c != want {
			r.errors++
		}
		if c == http.StatusTooManyRequests {
			r.throttled++
		}
	}
	sort.Slice(lat, func(a, b int) bool { return lat[a] < lat[b] })
	r.p50, r.p95, r.p99, r.max = ppct(lat, .50), ppct(lat, .95), ppct(lat, .99), lat[n-1]
	return r
}

func pms(d time.Duration) string { return strconv.FormatFloat(float64(d.Microseconds())/1000, 'f', 1, 64) }

func codesStr(m map[int]int) string {
	keys := make([]int, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	var parts []string
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%d×%d", k, m[k]))
	}
	return strings.Join(parts, " ")
}

const presHeader = "| scenario | c | n | p50 ms | p95 ms | p99 ms | max ms | req/s | errors | 429s | statuses |\n|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---|\n"

func (r pres) row() string {
	return fmt.Sprintf("| %s | %d | %d | %s | %s | %s | %s | %.0f | %d | %d | %s |\n", r.name, r.conc, r.n, pms(r.p50), pms(r.p95),
		pms(r.p99), pms(r.max), r.rps, r.errors, r.throttled, codesStr(r.codes))
}

// report collects markdown output, prints it and writes it to $FUNDZIM_PERF_OUT/<test>.md when set.
type report struct {
	t *testing.T
	b strings.Builder
}

func (r *report) f(format string, a ...any) { fmt.Fprintf(&r.b, format, a...) }

func (r *report) flush() {
	fmt.Println(r.b.String())
	if dir := os.Getenv("FUNDZIM_PERF_OUT"); dir != "" {
		_ = os.MkdirAll(dir, 0o755)
		if err := os.WriteFile(filepath.Join(dir, r.t.Name()+".md"), []byte(r.b.String()), 0o644); err != nil {
			r.t.Error(err)
		}
	}
}

// ---- HTTP without t.Fatal (safe in worker goroutines) -----------------------------------------------------------

var perfIPSeq atomic.Uint32

// freshIP returns a client IP not used before in this process (198.19.0.0/16; the integration browser uses 198.18/16).
func freshIP() string {
	n := perfIPSeq.Add(1) + uint32(time.Now().UnixNano()%7919)*8
	return fmt.Sprintf("198.19.%d.%d", (n/250)%250, n%250+1)
}

var perfTransport = &http.Transport{MaxIdleConns: 1024, MaxIdleConnsPerHost: 1024, IdleConnTimeout: 90 * time.Second}

// pdo sends one request with b's session cookies and CSRF token from the given client IP.
func (b *browser) pdo(method, path string, body any, ip string, hdr ...string) (int, []byte, error) {
	var rd io.Reader
	if body != nil {
		buf, _ := json.Marshal(body)
		rd = bytes.NewReader(buf)
	}
	req, err := http.NewRequest(method, b.s.srv.URL+"/api/v1"+path, rd)
	if err != nil {
		return 0, nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("X-Forwarded-For", ip)
	if c := b.cookie("fz_csrf"); c != "" {
		req.Header.Set("X-CSRF-Token", c)
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	resp, err := b.c.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	return resp.StatusCode, raw, err
}

func dataField(raw []byte, name string) string {
	var env struct {
		Data map[string]any `json:"data"`
	}
	_ = json.Unmarshal(raw, &env)
	v, _ := env.Data[name].(string)
	return v
}

// ---- fixtures ------------------------------------------------------------------------------------------------

type perfEnv struct {
	s   *itServer
	mig *pgxpool.Pool
	rep *report
}

func newPerfEnv(t *testing.T) *perfEnv {
	t.Helper()
	perfGate(t)
	s := newITServer(t, func(c *config.Config) { c.Payments.SandboxEnabled = true })
	m := s.d.Verification
	if m.Sandbox == nil {
		t.Fatal("sandbox not registered although enabled")
	}
	m.Sandbox.WebhookURL = s.srv.URL + "/api/v1/webhooks/payments/sandbox"
	e := &perfEnv{s: s, mig: pool(t, os.Getenv("DATABASE_MIGRATION_URL")), rep: &report{t: t}}
	e.rep.f("## %s\n\nstarted %s UTC; GOMAXPROCS=%d; NumCPU=%d; DATABASE_MAX_CONNS=%s; go %s\n\n", t.Name(),
		time.Now().UTC().Format(time.RFC3339), runtime.GOMAXPROCS(0), runtime.NumCPU(), os.Getenv("DATABASE_MAX_CONNS"), runtime.Version())
	return e
}

func (e *perfEnv) counts(t *testing.T, label string) {
	t.Helper()
	var di, pi, inbox, pev, att, out int
	if err := e.mig.QueryRow(ctx(t), `SELECT (SELECT count(*) FROM app.donation_intents), (SELECT count(*) FROM app.payment_intents),
		(SELECT count(*) FROM app.provider_webhook_inbox), (SELECT count(*) FROM app.payment_events), (SELECT count(*) FROM app.payment_attempts),
		(SELECT count(*) FROM app.outbox_events)`).Scan(&di, &pi, &inbox, &pev, &att, &out); err != nil {
		t.Fatal(err)
	}
	e.rep.f("dataset %s: app.donation_intents=%d app.payment_intents=%d app.provider_webhook_inbox=%d app.payment_events=%d "+
		"app.payment_attempts=%d app.outbox_events=%d\n\n", label, di, pi, inbox, pev, att, out)
}

// donors registers n donor accounts (verified email, signed in) in parallel and gives them a pooled transport.
func (e *perfEnv) donors(t *testing.T, n int) []*browser {
	t.Helper()
	t0 := time.Now()
	out := make([]*browser, n)
	var next atomic.Int64
	var wg sync.WaitGroup
	for w := 0; w < 6; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				i := int(next.Add(1)) - 1
				if i >= n {
					return
				}
				b := e.s.browser(t)
				email := uniqueEmail("perfdonor")
				b.registerVerified(email)
				b.mustLogin(email, itPassword)
				b.c = &http.Client{Jar: b.c.Jar, Timeout: 60 * time.Second, Transport: perfTransport}
				out[i] = b
			}
		}()
	}
	wg.Wait()
	for i, b := range out {
		if b == nil {
			t.Fatalf("donor %d not created", i)
		}
	}
	t.Logf("%d donors in %s", n, time.Since(t0).Round(time.Millisecond))
	return out
}

// campaigns returns k ACTIVE USD campaigns (synthetic, published by earlier integration runs) that received no
// donation intent in the last hour (so the per-campaign limit bucket is not already drained), each probed with
// one real intent creation.
func (e *perfEnv) campaigns(t *testing.T, k int, probe *browser) []string {
	t.Helper()
	rows, err := e.mig.Query(ctx(t), `SELECT c.id::text FROM app.campaigns c JOIN app.campaign_versions v ON v.id = c.approved_version_id
		WHERE c.status = 'ACTIVE' AND c.visibility = 'PUBLIC' AND v.goal_currency = 'USD'
		  AND NOT EXISTS (SELECT 1 FROM app.donation_intents d WHERE d.campaign_id = c.id AND d.created_at > now() - interval '65 minutes')
		ORDER BY c.published_at DESC LIMIT 40`)
	if err != nil {
		t.Fatal(err)
	}
	var cands []string
	for rows.Next() {
		var id string
		_ = rows.Scan(&id)
		cands = append(cands, id)
	}
	rows.Close()
	var out []string
	for _, c := range cands {
		if len(out) == k {
			break
		}
		code, raw, err := probe.pdo("POST", "/donation-intents", map[string]any{"campaign_id": c, "amount_minor": "1000", "currency": "USD"},
			freshIP(), "Idempotency-Key", ids.New())
		if err == nil && code == 201 {
			out = append(out, c)
		} else {
			t.Logf("campaign %s skipped: %d %.200s", c, code, raw)
		}
	}
	if len(out) < k {
		t.Fatalf("only %d of %d usable campaigns (candidates %d)", len(out), k, len(cands))
	}
	return out
}

type perfIntent struct {
	id     string
	donor  *browser
	amount string
}

// ---- 1, 2, 5, 7: donor API -------------------------------------------------------------------------------------

func TestPaymentsPerfDonorAPI(t *testing.T) {
	e := newPerfEnv(t)
	defer e.rep.flush()
	e.counts(t, "before")

	type cell struct{ n, c int }
	intentCells := []cell{{50, 1}, {200, 8}, {320, 32}}
	total := 0
	for _, c := range intentCells {
		total += c.n
	}
	const perDonor = 19 // payments.intent.user is 20/h
	nd := (total + perDonor - 1) / perDonor
	ds := e.donors(t, nd+3) // + probe + two duplicate-key donors
	probe, dupA, dupB, ds := ds[0], ds[1], ds[2], ds[3:]
	k := (total + 449) / 450 // payments.intent.campaign is 600/h
	camps := e.campaigns(t, k, probe)
	e.rep.f("fixtures: %d donors (≤%d intents each), %d campaigns (≤%d intents each), every request from its own client IP\n\n",
		len(ds), perDonor, len(camps), (total+len(camps)-1)/len(camps))

	// 1. POST /donation-intents
	var mu sync.Mutex
	var created []perfIntent
	var gi atomic.Int64
	e.rep.f("### 1. POST /donation-intents\n\n" + presHeader)
	for _, cl := range intentCells {
		name := "create intent"
		if cl.c == 32 {
			name = "create intent (burst)"
		}
		r := pmeasure(name, cl.n, cl.c, 201, func(int) (int, int, error) {
			g := int(gi.Add(1)) - 1
			d := ds[g%len(ds)]
			amount := "1000"
			if g%2 == 1 {
				amount = "1024"
			}
			code, raw, err := d.pdo("POST", "/donation-intents", map[string]any{"campaign_id": camps[g%len(camps)], "amount_minor": amount,
				"currency": "USD"}, freshIP(), "Idempotency-Key", ids.New())
			if code == 201 {
				mu.Lock()
				created = append(created, perfIntent{id: dataField(raw, "id"), donor: d, amount: amount})
				mu.Unlock()
			}
			return code, len(raw), err
		})
		e.rep.f("%s", r.row())
	}
	e.rep.f("\n")

	// 2. POST /donation-intents/{id}/attempts (ECOCASH; suffix 00 = success webhook after 1 s; 24 = pending forever)
	pick := func(amount string) []perfIntent {
		var out []perfIntent
		for _, c := range created {
			if c.amount == amount {
				out = append(out, c)
			}
		}
		return out
	}
	e.rep.f("### 2. POST /donation-intents/{id}/attempts (sandbox ECOCASH)\n\n" + presHeader)
	statusTargets := []perfIntent{}
	for _, amount := range []string{"1000", "1024"} {
		list := pick(amount)
		off := 0
		for _, cl := range []cell{{30, 1}, {100, 8}, {150, 32}} {
			n := cl.n
			if off+n > len(list) {
				n = len(list) - off
			}
			if n <= 0 {
				e.rep.f("| attempt amount %s c=%d | not run: no intents left |\n", amount, cl.c)
				continue
			}
			sub := list[off : off+n]
			off += n
			var pendings atomic.Int64
			r := pmeasure(fmt.Sprintf("attempt, amount %s (suffix %s)", amount, amount[2:]), n, cl.c, 201, func(i int) (int, int, error) {
				it := sub[i]
				code, raw, err := it.donor.pdo("POST", "/donation-intents/"+it.id+"/attempts", map[string]string{"method": "ECOCASH"},
					freshIP(), "Idempotency-Key", ids.New())
				if code == 201 && dataField(raw, "status") == "PENDING" {
					pendings.Add(1)
				}
				return code, len(raw), err
			})
			e.rep.f("%s", strings.TrimSuffix(r.row(), "\n")+fmt.Sprintf(" (PENDING: %d) |\n", pendings.Load()))
			statusTargets = append(statusTargets, sub...)
		}
	}
	e.rep.f("\n")

	// 5. GET /donation-intents/{id}/status
	e.rep.f("### 5. GET /donation-intents/{id}/status\n\n" + presHeader)
	for _, cl := range []cell{{300, 1}, {1000, 8}, {2000, 32}} {
		r := pmeasure("status (own intent)", cl.n, cl.c, 200, func(i int) (int, int, error) {
			it := statusTargets[i%len(statusTargets)]
			code, raw, err := it.donor.pdo("GET", "/donation-intents/"+it.id+"/status", nil, freshIP())
			return code, len(raw), err
		})
		e.rep.f("%s", r.row())
	}
	{ // one donor, one client IP: the global per-IP limiter (RATE_LIMIT_RPS/BURST) applies
		it := statusTargets[0]
		ip := freshIP()
		r := pmeasure("status, ONE donor from ONE client IP", 300, 8, 200, func(int) (int, int, error) {
			code, raw, err := it.donor.pdo("GET", "/donation-intents/"+it.id+"/status", nil, ip)
			return code, len(raw), err
		})
		e.rep.f("%s", r.row())
	}
	e.rep.f("\n")

	// 7. 100 concurrent duplicate requests with one Idempotency-Key
	e.rep.f("### 7. 100 concurrent POST /donation-intents with ONE Idempotency-Key (one donor)\n\n" + presHeader)
	for _, v := range []struct {
		name   string
		d      *browser
		sameIP bool
	}{{"100 duplicates, one client IP", dupA, true}, {"100 duplicates, distinct client IPs", dupB, false}} {
		key := ids.New()
		body := map[string]any{"campaign_id": camps[0], "amount_minor": "1100", "currency": "USD"}
		ip := freshIP()
		gotIDs := make([]string, 100)
		var start sync.WaitGroup
		start.Add(1)
		var ready sync.WaitGroup
		ready.Add(100)
		go func() { ready.Wait(); start.Done() }() // release all 100 workers at once
		r := pmeasure(v.name, 100, 100, 201, func(i int) (int, int, error) {
			ready.Done()
			start.Wait() // release all 100 together
			use := ip
			if !v.sameIP {
				use = freshIP()
			}
			code, raw, err := v.d.pdo("POST", "/donation-intents", body, use, "Idempotency-Key", key)
			if code == 201 {
				gotIDs[i] = dataField(raw, "id")
			}
			return code, len(raw), err
		})
		e.rep.f("%s", r.row())
		distinct := map[string]bool{}
		for _, g := range gotIDs {
			if g != "" {
				distinct[g] = true
			}
		}
		var donorID string
		_ = e.mig.QueryRow(ctx(t), `SELECT donor_user_id::text FROM app.donation_intents WHERE idempotency_key = $1`, key).Scan(&donorID)
		stored := 0
		_ = e.mig.QueryRow(ctx(t), `SELECT count(*) FROM app.donation_intents WHERE idempotency_key = $1`, key).Scan(&stored)
		e.rep.f("| ↳ %s: intents stored for the key = %d; distinct ids in 201 responses = %d |\n", v.name, stored, len(distinct))
		if stored != 1 || len(distinct) > 1 {
			t.Errorf("%s: %d intents stored, %d distinct ids returned", v.name, stored, len(distinct))
		}
	}
	e.rep.f("\n")
	e.counts(t, "after")
}

// Note on pmeasure + barrier: pmeasure runs 100 workers for 100 operations, so each worker performs exactly one
// operation; the start barrier inside f is released below.
func init() {}

// ---- 3, 4: webhook ingestion and dispatch -------------------------------------------------------------------

type pendingPay struct{ id, ref string }

// pendingPayments creates n PENDING sandbox payments (amount 1024 = pending_forever: the simulator never sends a
// webhook for them). Setup only; not measured.
func (e *perfEnv) pendingPayments(t *testing.T, n int) []pendingPay {
	t.Helper()
	const perDonor = 19
	ds := e.donors(t, (n+perDonor-1)/perDonor+1)
	probe, ds := ds[0], ds[1:]
	camps := e.campaigns(t, (n+449)/450, probe)
	out := make([]pendingPay, n)
	var bad atomic.Int64
	var next atomic.Int64
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				i := int(next.Add(1)) - 1
				if i >= n {
					return
				}
				d := ds[i%len(ds)]
				code, raw, err := d.pdo("POST", "/donation-intents", map[string]any{"campaign_id": camps[i%len(camps)], "amount_minor": "1024",
					"currency": "USD"}, freshIP(), "Idempotency-Key", ids.New())
				if err != nil || code != 201 {
					bad.Add(1)
					continue
				}
				code, raw, err = d.pdo("POST", "/donation-intents/"+dataField(raw, "id")+"/attempts", map[string]string{"method": "ECOCASH"},
					freshIP(), "Idempotency-Key", ids.New())
				if err != nil || code != 201 || dataField(raw, "status") != "PENDING" {
					bad.Add(1)
					continue
				}
				out[i].id = dataField(raw, "id")
			}
		}()
	}
	wg.Wait()
	if bad.Load() > 0 {
		t.Fatalf("%d pending payments could not be created", bad.Load())
	}
	for i := range out {
		if err := e.mig.QueryRow(ctx(t), `SELECT provider_payment_ref FROM app.payment_intents WHERE id = $1`, out[i].id).Scan(&out[i].ref); err != nil {
			t.Fatal(err)
		}
	}
	return out
}

func evtID() string { return "sbx_evt_" + strings.ReplaceAll(ids.New(), "-", "")[12:32] }

var whClient = &http.Client{Timeout: 60 * time.Second, Transport: perfTransport}

// postSigned signs a sandbox payload with the current webhook secret and POSTs it from the given client IP.
func postSigned(base string, secret []byte, pl sandbox.Payload, ip string) (int, int, error) {
	body, _ := json.Marshal(pl)
	req, _ := http.NewRequest("POST", base+"/api/v1/webhooks/payments/sandbox", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(sandbox.SignatureHeader, sandbox.Sign(secret, "current", time.Now(), body))
	req.Header.Set("X-Forwarded-For", ip)
	resp, err := whClient.Do(req)
	if err != nil {
		return 0, 0, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, len(raw), nil
}

func payload(p pendingPay, status string) sandbox.Payload {
	return sandbox.Payload{ID: evtID(), Type: "payment." + status, Created: time.Now().Unix(), Simulated: true,
		Data: sandbox.PayloadData{PaymentReference: p.id, ProviderPaymentID: p.ref, Status: status, AmountMinor: "1024", Currency: "USD",
			PayerMasked: "****1234"}}
}

func TestPaymentsPerfWebhooks(t *testing.T) {
	e := newPerfEnv(t)
	defer e.rep.flush()
	e.counts(t, "before")
	secret := []byte(os.Getenv("SECRETREF_FUNDZIM_SANDBOX_WEBHOOK_CURRENT"))
	base := e.s.srv.URL
	m := e.s.d.Verification

	const perRun = 150
	pays := e.pendingPayments(t, 2*perRun)
	e.rep.f("fixtures: %d PENDING sandbox payments (ECOCASH, amount 1024 = pending_forever)\n\n", len(pays))

	// 4. dispatch → apply throughput. Each event is ingested through the real endpoint ("payment.paid" for a PENDING
	// payment: a real P9 PENDING→SUCCEEDED apply). Immediately after its 200, the outbox row is deferred
	// (available_at = infinity: a bookkeeping column the relay itself uses for backoff) so the shared compose worker's
	// relay does not dispatch it; an event the relay already claimed is excluded and counted. The harness then
	// drains the events through psp.Service.Dispatch with 1 or 4 concurrent dispatchers, records consumption in
	// app.inbox_events exactly as the outbox delivery does, and marks the outbox rows dispatched.
	e.rep.f("### 4. Webhook processing: drain N stored events through psp.Service.Dispatch (claim → apply → processed)\n\n")
	e.rep.f("| dispatchers | N drained | excluded (relayed by the compose worker first) | wall ms | events/s | per-event p50 ms | p95 ms | p99 ms | max ms | dispatch errors | inbox PROCESSED | payments SUCCEEDED |\n|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|\n")
	type stored struct {
		eventID string
		payload []byte
		inboxID string
		payID   string
	}
	for run, workers := range []int{1, 4} {
		set := pays[run*perRun : (run+1)*perRun]
		evs := make([]stored, 0, len(set))
		excluded := 0
		var emu sync.Mutex
		var next atomic.Int64
		var wg sync.WaitGroup
		var ingestErr atomic.Int64
		for w := 0; w < 8; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for {
					i := int(next.Add(1)) - 1
					if i >= len(set) {
						return
					}
					pl := payload(set[i], "paid")
					code, _, err := postSigned(base, secret, pl, freshIP())
					if err != nil || code != 200 {
						ingestErr.Add(1)
						continue
					}
					var st stored
					c, cancel := context.WithTimeout(context.Background(), 10*time.Second)
					err = e.mig.QueryRow(c, `UPDATE app.outbox_events o SET available_at = 'infinity'
						FROM app.provider_webhook_inbox i WHERE i.provider_event_id = $1 AND o.aggregate_id = i.id AND o.event_type = $2
						AND o.dispatched_at IS NULL RETURNING o.id::text, o.payload, i.id::text`, pl.ID, psp.EvWebhookReceived).Scan(&st.eventID, &st.payload, &st.inboxID)
					cancel()
					emu.Lock()
					if err != nil {
						excluded++
					} else {
						st.payID = set[i].id
						evs = append(evs, st)
					}
					emu.Unlock()
				}
			}()
		}
		wg.Wait()
		if ingestErr.Load() > 0 {
			t.Fatalf("dispatch setup: %d webhooks not ingested", ingestErr.Load())
		}
		lat := make([]time.Duration, len(evs))
		var dErr atomic.Int64
		next.Store(0)
		t0 := time.Now()
		for w := 0; w < workers; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for {
					i := int(next.Add(1)) - 1
					if i >= len(evs) {
						return
					}
					c, cancel := context.WithTimeout(context.Background(), 30*time.Second)
					s := time.Now()
					err := m.PSP.Dispatch(c, outbox.Delivery{EventID: evs[i].eventID, EventType: psp.EvWebhookReceived, Payload: evs[i].payload, Attempt: 1})
					lat[i] = time.Since(s)
					cancel()
					if err != nil {
						dErr.Add(1)
					}
				}
			}()
		}
		wg.Wait()
		wall := time.Since(t0)
		inboxIDs, eventIDs, payIDs := make([]string, len(evs)), make([]string, len(evs)), make([]string, len(evs))
		for i, s := range evs {
			inboxIDs[i], eventIDs[i], payIDs[i] = s.inboxID, s.eventID, s.payID
		}
		// bookkeeping exactly as the outbox delivery would leave it
		if _, err := e.mig.Exec(ctx(t), `INSERT INTO app.inbox_events (id, consumer, event_id, event_type)
			SELECT gen_random_uuid(), $1, e, $2 FROM unnest($3::uuid[]) e ON CONFLICT (consumer, event_id) DO NOTHING`,
			psp.ConsumerDispatch, psp.EvWebhookReceived, eventIDs); err != nil {
			t.Fatalf("inbox_events: %v", err)
		}
		if _, err := e.mig.Exec(ctx(t), `UPDATE app.outbox_events SET available_at = created_at, dispatched_at = now(), attempts = attempts + 1
			WHERE id = ANY($1::uuid[]) AND dispatched_at IS NULL`, eventIDs); err != nil {
			t.Fatalf("outbox bookkeeping: %v", err)
		}
		var processed, succeeded int
		_ = e.mig.QueryRow(ctx(t), `SELECT count(*) FROM app.provider_webhook_inbox WHERE id = ANY($1::uuid[]) AND status = 'PROCESSED'`, inboxIDs).Scan(&processed)
		_ = e.mig.QueryRow(ctx(t), `SELECT count(*) FROM app.payment_intents WHERE id = ANY($1::uuid[]) AND status = 'SUCCEEDED'`, payIDs).Scan(&succeeded)
		sort.Slice(lat, func(a, b int) bool { return lat[a] < lat[b] })
		var mx time.Duration
		if len(lat) > 0 {
			mx = lat[len(lat)-1]
		}
		e.rep.f("| %d | %d | %d | %s | %.0f | %s | %s | %s | %s | %d | %d | %d |\n", workers, len(evs), excluded, pms(wall),
			float64(len(evs))/wall.Seconds(), pms(ppct(lat, .5)), pms(ppct(lat, .95)), pms(ppct(lat, .99)), pms(mx), dErr.Load(), processed, succeeded)
		if processed != len(evs) || succeeded != len(evs) {
			t.Errorf("dispatch run %d: %d processed, %d succeeded of %d", workers, processed, succeeded, len(evs))
		}
	}
	e.rep.f("\n")

	// 3. ingestion: valid signed "payment.pending" events (unique ids) for the now-SUCCEEDED payments (a lower-rank
	// status: whoever dispatches them later records them without changing a payment).
	e.rep.f("### 3. Webhook ingestion POST /webhooks/payments/sandbox (valid signature, unique event ids)\n\n" + presHeader)
	var gi atomic.Int64
	ing := func(ip func() string) func(int) (int, int, error) {
		return func(int) (int, int, error) {
			g := int(gi.Add(1)) - 1
			return postSigned(base, secret, payload(pays[g%len(pays)], "pending"), ip())
		}
	}
	for _, cl := range []struct{ n, c int }{{200, 1}, {800, 8}, {1600, 32}, {3200, 320}} {
		name := "ingest"
		if cl.c == 320 {
			name = "ingest, burst (10× c=32)"
		}
		r := pmeasure(name, cl.n, cl.c, 200, ing(freshIP))
		e.rep.f("%s", r.row())
	}
	one := freshIP()
	r := pmeasure("ingest, ONE client IP (a PSP sending from one address)", 300, 8, 200, ing(func() string { return one }))
	e.rep.f("%s", r.row())
	e.rep.f("\n")
	e.counts(t, "after")
}

// ---- 6: staff operations queue reads --------------------------------------------------------------------------

func TestPaymentsPerfStaffReads(t *testing.T) {
	e := newPerfEnv(t)
	defer e.rep.flush()
	f := staffFixture(t, e.s)
	fin := e.s.freshStaff(t, f.finance1)
	fin.c = &http.Client{Jar: fin.c.Jar, Timeout: 60 * time.Second, Transport: perfTransport}
	e.counts(t, "at measurement")
	var unknown, recon int
	var inboxBy string
	_ = e.mig.QueryRow(ctx(t), `SELECT count(*) FILTER (WHERE status = 'UNKNOWN'), count(*) FILTER (WHERE needs_reconciliation) FROM app.payment_intents`).Scan(&unknown, &recon)
	_ = e.mig.QueryRow(ctx(t), `SELECT string_agg(status || '=' || n, ' ' ORDER BY status) FROM (SELECT status, count(*) n FROM app.provider_webhook_inbox GROUP BY status) x`).Scan(&inboxBy)
	e.rep.f("payment_intents UNKNOWN=%d needs_reconciliation=%d; inbox by status: %s\n\n", unknown, recon, inboxBy)

	e.rep.f("### 6. Staff operations queue reads (FINANCE officer; first page, default limit 50)\n\n" + presHeader)
	paths := []string{"/admin/payments/attempts", "/admin/payments/attempts?status=PENDING", "/admin/payments/unknown",
		"/admin/payments/webhooks", "/admin/payments/webhooks?status=PROCESSED"}
	for _, p := range paths { // warm-up + sanity
		code, raw, err := fin.pdo("GET", p, nil, freshIP())
		if err != nil || code != 200 {
			t.Fatalf("%s: %d %v %.300s", p, code, err, raw)
		}
		var env struct {
			Data struct {
				Items []json.RawMessage `json:"items"`
			} `json:"data"`
		}
		_ = json.Unmarshal(raw, &env)
		e.rep.f("- `%s`: %d items on the first page, %d bytes\n", p, len(env.Data.Items), len(raw))
	}
	e.rep.f("\n" + presHeader)
	for _, cl := range []struct{ n, c int }{{200, 1}, {400, 8}} {
		for _, p := range paths {
			r := pmeasure("GET "+p, cl.n, cl.c, 200, func(int) (int, int, error) {
				code, raw, err := fin.pdo("GET", p, nil, freshIP())
				return code, len(raw), err
			})
			e.rep.f("%s", r.row())
		}
	}
	e.rep.f("\n")
}
