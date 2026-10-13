//go:build integration && perf

// Stage 9 performance rows (docs/stage-9/testing.md §4): secret resolution from OpenBao (uncached and cached) versus
// the environment resolver, the public method-availability endpoint, the staff provider detail and health views
// (health is derived per request from observations), and the startup gate. Compiled into tests/integration with
// the same overlay as payments_perf_test.go; needs FUNDZIM_PERF_PAYMENTS=1 and the compose OpenBao.
//
//	flock /tmp/claude-1000/fundzim-stack.lock env FUNDZIM_PERF_PAYMENTS=1 FUNDZIM_PERF_OUT=/tmp/payperf \
//	  go test -tags integration,perf -overlay tests/perf/payments/overlay.json -count=1 -timeout 20m \
//	  -run '^TestPaymentsPerfStage9$' -v ./tests/integration/
package integration

import (
	"context"
	"net/http"
	"os"
	"time"

	"testing"

	"github.com/Fatifizo/fundzim/internal/platform/secretstore"
	"github.com/Fatifizo/fundzim/internal/psp"
)

func TestPaymentsPerfStage9(t *testing.T) {
	e := newPerfEnv(t)
	defer e.rep.flush()
	need(t, "SECRETS_KV2_ADDR", "SECRETS_KV2_TOKEN")
	ref := "secretref://fundzim/sandbox/api"

	e.rep.f("### S9-1. Secret resolution (in-process; OpenBao dev server from compose over loopback HTTP)\n\n" + presHeader)
	mk := func(ttl time.Duration) *secretstore.KV2 {
		k, err := secretstore.New(secretstore.Config{Addr: os.Getenv("SECRETS_KV2_ADDR"), Mount: "secret", Token: os.Getenv("SECRETS_KV2_TOKEN"),
			CacheTTL: ttl, Timeout: 3 * time.Second})
		if err != nil {
			t.Fatal(err)
		}
		return k
	}
	uncached, cached := mk(time.Nanosecond), mk(time.Minute)
	env := psp.EnvResolver{Lookup: os.LookupEnv}
	for _, c := range []struct {
		name string
		r    psp.SecretResolver
	}{{"KV v2 uncached (every call reads OpenBao)", uncached}, {"KV v2 cached (TTL 60 s)", cached}, {"environment resolver (Stage 8)", env}} {
		for _, conc := range []int{1, 8} {
			r := pmeasure(c.name, 2000, conc, 200, func(int) (int, int, error) {
				v, err := c.r.Resolve(context.Background(), ref)
				if err != nil {
					return 0, 0, err
				}
				return 200, len(v), nil
			})
			e.rep.f("%s", r.row())
		}
	}

	e.rep.f("\n### S9-2. Public GET /payment-methods/available (anonymous; one client IP per request)\n\n" + presHeader)
	anon := e.s.browser(t)
	anon.c = &http.Client{Jar: anon.c.Jar, Timeout: 60 * time.Second, Transport: perfTransport}
	if code, raw, err := anon.pdo("GET", "/payment-methods/available", nil, freshIP()); err != nil || code != 200 {
		t.Fatalf("available: %d %v %s", code, err, raw)
	}
	for _, cl := range []struct{ n, c int }{{300, 1}, {600, 8}} {
		r := pmeasure("GET /payment-methods/available", cl.n, cl.c, 200, func(int) (int, int, error) {
			code, raw, err := anon.pdo("GET", "/payment-methods/available", nil, freshIP())
			return code, len(raw), err
		})
		e.rep.f("%s", r.row())
	}

	e.rep.f("\n### S9-3. Staff provider detail and health (FINANCE officer; health derived per request)\n\n" + presHeader)
	f := staffFixture(t, e.s)
	fin := e.s.staff(t, f.finance2)
	fin.c = &http.Client{Jar: fin.c.Jar, Timeout: 60 * time.Second, Transport: perfTransport}
	sim, err := e.s.d.Verification.PSP.ProviderByCode(ctx(t), "sandbox")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"/admin/payments/providers/" + sim.ID, "/admin/payments/health", "/admin/payments/providers"} {
		if code, raw, err := fin.pdo("GET", p, nil, freshIP()); err != nil || code != 200 {
			t.Fatalf("%s: %d %v %.200s", p, code, err, raw)
		}
		for _, cl := range []struct{ n, c int }{{200, 1}, {400, 8}} {
			r := pmeasure("GET "+p, cl.n, cl.c, 200, func(int) (int, int, error) {
				code, raw, err := fin.pdo("GET", p, nil, freshIP())
				return code, len(raw), err
			})
			e.rep.f("%s", r.row())
		}
	}

	e.rep.f("\n### S9-4. Startup gate (API process start; audit records included)\n\n" + presHeader)
	r := pmeasure("psp.StartupGate", 50, 1, 200, func(int) (int, int, error) {
		_, err := e.s.d.Verification.PSP.StartupGate(context.Background(), "kv2", "perf")
		if err != nil {
			return 0, 0, err
		}
		return 200, 0, nil
	})
	e.rep.f("%s\n", r.row())
}
