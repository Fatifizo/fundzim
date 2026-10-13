package secretstore

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeKV is a minimal KV v2 + AppRole server for unit tests (the integration suite uses real OpenBao).
type fakeKV struct {
	mu       sync.Mutex
	values   map[string]string
	versions map[string]int
	tokens   map[string]bool
	deleted  map[string]bool
	reads    atomic.Int32
	logins   atomic.Int32
	down     atomic.Bool
}

func newFake() *fakeKV {
	return &fakeKV{values: map[string]string{}, versions: map[string]int{}, tokens: map[string]bool{"root-token": true}, deleted: map[string]bool{}}
}

func (f *fakeKV) put(path, v string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.values[path] = v
	f.versions[path]++
}

func (f *fakeKV) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if f.down.Load() {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	switch {
	case r.URL.Path == "/v1/sys/health":
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"initialized":true,"sealed":false}`))
	case r.URL.Path == "/v1/auth/approle/login" && r.Method == http.MethodPost:
		var in map[string]string
		_ = json.NewDecoder(r.Body).Decode(&in)
		if in["role_id"] != "role" || in["secret_id"] != "sid" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		n := f.logins.Add(1)
		tok := "approle-token-" + string(rune('a'+n))
		f.mu.Lock()
		f.tokens[tok] = true
		f.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"auth": map[string]any{"client_token": tok, "lease_duration": 60}})
	case strings.HasPrefix(r.URL.Path, "/v1/secret/data/"):
		f.reads.Add(1)
		f.mu.Lock()
		defer f.mu.Unlock()
		if !f.tokens[r.Header.Get("X-Vault-Token")] {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"errors":["permission denied"]}`))
			return
		}
		path := strings.TrimPrefix(r.URL.Path, "/v1/secret/data/")
		v, ok := f.values[path]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"errors":[]}`))
			return
		}
		del := ""
		if f.deleted[path] {
			del = "2026-10-13T00:00:00Z"
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"data": map[string]any{"value": v},
			"metadata": map[string]any{"version": f.versions[path], "deletion_time": del, "destroyed": false}}})
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

const strong = "0123456789abcdef0123456789abcdef-strong-test-value"

type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.now }
func (c *clock) add(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

func newClient(t *testing.T, srv *httptest.Server, c *clock, mut func(*Config)) *KV2 {
	t.Helper()
	cfg := Config{Addr: srv.URL, Mount: "secret", Token: "root-token", CacheTTL: time.Minute, Timeout: time.Second, Now: c.Now}
	if mut != nil {
		mut(&cfg)
	}
	k, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func TestResolveCachesWithinTTLAndFailsClosedAfter(t *testing.T) {
	f := newFake()
	f.put("fundzim/sandbox/api", strong)
	srv := httptest.NewServer(f)
	defer srv.Close()
	c := &clock{now: time.Unix(1_800_000_000, 0)}
	k := newClient(t, srv, c, nil)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		v, err := k.Resolve(ctx, "secretref://fundzim/sandbox/api")
		if err != nil || string(v) != strong {
			t.Fatalf("resolve %d: %v", i, err)
		}
	}
	if n := f.reads.Load(); n != 1 {
		t.Fatalf("reads within TTL = %d, want 1 (cache)", n)
	}
	f.down.Store(true)
	if _, err := k.Resolve(ctx, "secretref://fundzim/sandbox/api"); err != nil {
		t.Fatalf("cached value must still serve within TTL: %v", err)
	}
	c.add(61 * time.Second)
	_, err := k.Resolve(ctx, "secretref://fundzim/sandbox/api")
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("store down after TTL: want ErrUnavailable (fail closed), got %v", err)
	}
	f.down.Store(false)
	if _, err := k.Resolve(ctx, "secretref://fundzim/sandbox/api"); err != nil {
		t.Fatalf("recovery: %v", err)
	}
}

func TestRotationCallbackGetsVersionsOnly(t *testing.T) {
	f := newFake()
	f.put("fundzim/sandbox/webhook-current", strong)
	srv := httptest.NewServer(f)
	defer srv.Close()
	c := &clock{now: time.Unix(1_800_000_000, 0)}
	type rot struct {
		ref      string
		from, to int
	}
	var got []rot
	k := newClient(t, srv, c, func(cfg *Config) {
		cfg.OnRotate = func(ref string, from, to int) { got = append(got, rot{ref, from, to}) }
	})
	ctx := context.Background()
	ref := "secretref://fundzim/sandbox/webhook-current"
	if _, err := k.Resolve(ctx, ref); err != nil {
		t.Fatal(err)
	}
	f.put("fundzim/sandbox/webhook-current", strong+"-rotated")
	c.add(2 * time.Minute)
	v, err := k.Resolve(ctx, ref)
	if err != nil || !strings.HasSuffix(string(v), "-rotated") {
		t.Fatalf("rotated value not picked up after TTL: %v", err)
	}
	if len(got) != 1 || got[0] != (rot{ref, 1, 2}) || k.Version(ref) != 2 {
		t.Fatalf("rotation callback = %+v, version %d", got, k.Version(ref))
	}
}

func TestRefusesWeakDeletedMissingAndMalformed(t *testing.T) {
	f := newFake()
	f.put("fundzim/sandbox/weak", "wk-Q7zX")
	f.put("fundzim/sandbox/gone", strong)
	f.deleted["fundzim/sandbox/gone"] = true
	srv := httptest.NewServer(f)
	defer srv.Close()
	k := newClient(t, srv, &clock{now: time.Unix(1_800_000_000, 0)}, nil)
	for _, ref := range []string{"secretref://fundzim/sandbox/weak", "secretref://fundzim/sandbox/gone", "secretref://fundzim/sandbox/missing",
		"secretref://fundzim/../sys/raw", "env://X", "secretref://A"} {
		v, err := k.Resolve(context.Background(), ref)
		if !errors.Is(err, ErrUnavailable) || v != nil {
			t.Fatalf("%s: want ErrUnavailable, got %v", ref, err)
		}
		if err != nil && strings.Contains(err.Error(), "wk-Q7zX") {
			t.Fatalf("error leaks the value: %v", err)
		}
	}
}

func TestAppRoleLoginAndReloginOnForbidden(t *testing.T) {
	f := newFake()
	f.put("fundzim/production/x/api", strong)
	srv := httptest.NewServer(f)
	defer srv.Close()
	c := &clock{now: time.Unix(1_800_000_000, 0)}
	k := newClient(t, srv, c, func(cfg *Config) { cfg.Token, cfg.RoleID, cfg.SecretID = "", "role", "sid" })
	ctx := context.Background()
	if _, err := k.Resolve(ctx, "secretref://fundzim/production/x/api"); err != nil {
		t.Fatal(err)
	}
	if f.logins.Load() != 1 {
		t.Fatalf("logins = %d", f.logins.Load())
	}
	// the token is revoked server-side: the client logs in once more and succeeds
	f.mu.Lock()
	f.tokens = map[string]bool{}
	f.mu.Unlock()
	c.add(2 * time.Minute) // past the cache TTL, before the lease's renewal point is irrelevant here
	if _, err := k.Resolve(ctx, "secretref://fundzim/production/x/api"); err != nil {
		t.Fatalf("re-login: %v", err)
	}
	if f.logins.Load() != 2 {
		t.Fatalf("logins = %d, want 2", f.logins.Load())
	}
	bad := newClient(t, srv, c, func(cfg *Config) { cfg.Token, cfg.RoleID, cfg.SecretID = "", "role", "wrong" })
	if _, err := bad.Resolve(ctx, "secretref://fundzim/production/x/api"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("bad AppRole: %v", err)
	}
}

func TestNeverFollowsRedirectsAndValidatesConfig(t *testing.T) {
	var leaked atomic.Bool
	sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Vault-Token") != "" {
			leaked.Store(true)
		}
	}))
	defer sink.Close()
	redir := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, sink.URL+r.URL.Path, http.StatusTemporaryRedirect)
	}))
	defer redir.Close()
	k := newClient(t, redir, &clock{now: time.Unix(1_800_000_000, 0)}, nil)
	if _, err := k.Resolve(context.Background(), "secretref://fundzim/sandbox/api"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("redirect: %v", err)
	}
	if leaked.Load() {
		t.Fatal("the token followed a redirect")
	}
	for _, cfg := range []Config{{Addr: "ftp://x", Mount: "secret", Token: "t"}, {Addr: "http://x", Token: "t"},
		{Addr: "http://x", Mount: "secret"}, {Addr: "http://x", Mount: "secret", RoleID: "r"}} {
		if _, err := New(cfg); err == nil {
			t.Fatalf("invalid config accepted: %+v", cfg.Addr)
		}
	}
}

func TestHealth(t *testing.T) {
	f := newFake()
	srv := httptest.NewServer(f)
	defer srv.Close()
	k := newClient(t, srv, &clock{now: time.Unix(1_800_000_000, 0)}, nil)
	if err := k.Health(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.down.Store(true)
	if err := k.Health(context.Background()); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("down: %v", err)
	}
}
