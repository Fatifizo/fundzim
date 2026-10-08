//go:build integration

// Package integration exercises the Stage 3 foundation against REAL local services (docker compose).
// Run:  set -a; . ./.env; set +a; go test -tags integration -count=1 ./tests/integration/...
//
// Required environment (all from .env): DATABASE_URL, DATABASE_MIGRATION_URL, POSTGRES_WORKER_PASSWORD,
// REDIS_URL, STORAGE_*. Optional: FUNDZIM_IT_API_URL (black-box API checks, e.g. http://127.0.0.1:8080),
// FUNDZIM_IT_WEB_URL (frontend → backend check), FUNDZIM_IT_DESTRUCTIVE=1 (migration down/up round trip;
// only on a throwaway database such as CI's).
// These tests never touch production resources and use only synthetic data.
package integration

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/Fatifizo/fundzim/internal/platform/cache"
	"github.com/Fatifizo/fundzim/internal/platform/db"
	"github.com/Fatifizo/fundzim/internal/platform/ids"
	"github.com/Fatifizo/fundzim/internal/platform/storage"
	"github.com/Fatifizo/fundzim/migrations"
)

func need(t *testing.T, keys ...string) {
	t.Helper()
	for _, k := range keys {
		if os.Getenv(k) == "" {
			t.Fatalf("%s is not set; load .env first (set -a; . ./.env; set +a)", k)
		}
	}
}

func ctx(t *testing.T) context.Context {
	c, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	return c
}

func pool(t *testing.T, url string) *pgxpool.Pool {
	t.Helper()
	p, err := db.Open(ctx(t), db.Options{URL: url, MaxConns: 4, ConnectTimeout: 5 * time.Second, AppName: "fundzim-it"})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(p.Close)
	return p
}

// workerURL derives the fundzim_worker URL from DATABASE_URL (same host/db, different role).
func workerURL(t *testing.T) string {
	u, err := url.Parse(os.Getenv("DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	u.User = url.UserPassword("fundzim_worker", os.Getenv("POSTGRES_WORKER_PASSWORD"))
	return u.String()
}

func sqlState(err error) string {
	var pe *pgconn.PgError
	if errors.As(err, &pe) {
		return pe.Code
	}
	return ""
}

// --- migrations ---------------------------------------------------------------------------------

func TestMigrationsAppliedAndRepeatable(t *testing.T) {
	need(t, "DATABASE_MIGRATION_URL")
	sdb, err := sql.Open("pgx", os.Getenv("DATABASE_MIGRATION_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer sdb.Close()
	p, err := goose.NewProvider(goose.DialectPostgres, sdb, migrations.FS)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Up(ctx(t)); err != nil {
		t.Fatalf("up: %v", err)
	}
	res, err := p.Up(ctx(t)) // second run must be a no-op
	if err != nil || len(res) != 0 {
		t.Fatalf("re-running migrations applied %d (%v)", len(res), err)
	}
	v, err := p.GetDBVersion(ctx(t))
	if err != nil || v != migrations.ExpectedVersion() {
		t.Fatalf("db version %d, expected %d (%v)", v, migrations.ExpectedVersion(), err)
	}
	if os.Getenv("FUNDZIM_IT_DESTRUCTIVE") == "1" {
		if _, err := p.DownTo(ctx(t), 0); err != nil {
			t.Fatalf("down to 0: %v", err)
		}
		if _, err := p.Up(ctx(t)); err != nil {
			t.Fatalf("up after down: %v", err)
		}
	}
}

// --- PostgreSQL grants and invariants (as the runtime roles) ------------------------------------

func TestAppRoleReadinessQueries(t *testing.T) {
	need(t, "DATABASE_URL")
	p := pool(t, os.Getenv("DATABASE_URL"))
	if err := db.CheckSchemaCurrent(ctx(t), p, migrations.ExpectedVersion()); err != nil {
		t.Fatalf("schema check as app role: %v", err)
	}
	var n int
	if err := p.QueryRow(ctx(t), `SELECT count(*) FROM app.currencies WHERE code IN ('USD','ZWG')`).Scan(&n); err != nil || n != 2 {
		t.Fatalf("currency seed: %d %v", n, err)
	}
}

func TestAuditEventsAreAppendOnlyForRuntimeRoles(t *testing.T) {
	need(t, "DATABASE_URL", "POSTGRES_WORKER_PASSWORD")
	for name, url := range map[string]string{"fundzim_app": os.Getenv("DATABASE_URL"), "fundzim_worker": workerURL(t)} {
		t.Run(name, func(t *testing.T) {
			p := pool(t, url)
			id := ids.New()
			_, err := p.Exec(ctx(t), `INSERT INTO audit.audit_events (id, seq, occurred_at, actor_type, action, target_type, outcome, hash)
				VALUES ($1, 0, now(), 'system', 'platform.integration_test.ran', 'test', 'success', '\x00')`, id)
			if err != nil {
				t.Fatalf("insert audit event: %v", err)
			}
			for _, stmt := range []string{
				`UPDATE audit.audit_events SET reason = 'x' WHERE id = $1`,
				`DELETE FROM audit.audit_events WHERE id = $1`,
			} {
				if _, err := p.Exec(ctx(t), stmt, id); err == nil {
					t.Errorf("%s succeeded; audit_events must be append-only", stmt)
				} else if s := sqlState(err); s != "42501" && s != "23001" {
					t.Errorf("%s: unexpected error %v", stmt, err)
				}
			}
			if _, err := p.Exec(ctx(t), `TRUNCATE audit.audit_events`); err == nil {
				t.Error("TRUNCATE succeeded")
			}
		})
	}
}

func TestAuditHashChainVerifiesAndIsWorkerOnly(t *testing.T) {
	need(t, "DATABASE_URL", "POSTGRES_WORKER_PASSWORD")
	app := pool(t, os.Getenv("DATABASE_URL"))
	if _, err := app.Exec(ctx(t), `SELECT * FROM audit.verify_chain('audit.audit_events')`); err == nil || sqlState(err) != "42501" {
		t.Fatalf("api role must not execute the worker-only routine: %v", err)
	}
	w := pool(t, workerURL(t))
	rows, err := w.Query(ctx(t), `SELECT seq, problem FROM audit.verify_chain('audit.audit_events')`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var seq int64
		var problem string
		_ = rows.Scan(&seq, &problem)
		t.Errorf("chain problem at seq %d: %s", seq, problem)
	}
}

func TestReferenceDataAndPrivilegeBoundaries(t *testing.T) {
	need(t, "DATABASE_URL")
	p := pool(t, os.Getenv("DATABASE_URL"))
	checks := map[string]string{
		"currencies are reference data": `UPDATE app.currencies SET name = name WHERE code = 'USD'`,
		"no DDL for the app role":       `CREATE TABLE app.should_not_exist (id int)`,
		"no public schema writes":       `CREATE TABLE public.should_not_exist (id int)`,
		"status transitions read-only":  `INSERT INTO app.status_transitions VALUES ('x', '', 'Y')`,
		"goose table read-only":         `DELETE FROM public.goose_db_version`,
	}
	for name, stmt := range checks {
		if _, err := p.Exec(ctx(t), stmt); err == nil {
			t.Errorf("%s: %q succeeded", name, stmt)
		}
	}
}

func TestOutboxContentImmutableAndUndispatchedNotDeletable(t *testing.T) {
	need(t, "DATABASE_URL")
	p := pool(t, os.Getenv("DATABASE_URL"))
	id := ids.New()
	if _, err := p.Exec(ctx(t), `INSERT INTO app.outbox_events (id, aggregate_type, aggregate_id, event_type, payload, occurred_at)
		VALUES ($1, 'test', $1, 'platform.integration_tested', '{}', now())`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Exec(ctx(t), `UPDATE app.outbox_events SET payload = '{"x":1}' WHERE id = $1`, id); err == nil {
		t.Error("outbox payload changed")
	}
	if _, err := p.Exec(ctx(t), `DELETE FROM app.outbox_events WHERE id = $1`, id); err == nil {
		t.Error("undispatched outbox event deleted")
	}
	// bookkeeping may change, after which the row may be purged
	if _, err := p.Exec(ctx(t), `UPDATE app.outbox_events SET dispatched_at = now(), attempts = 1 WHERE id = $1`, id); err != nil {
		t.Fatalf("bookkeeping update: %v", err)
	}
	if _, err := p.Exec(ctx(t), `DELETE FROM app.outbox_events WHERE id = $1`, id); err != nil {
		t.Fatalf("purge of dispatched event: %v", err)
	}
}

// --- Redis ---------------------------------------------------------------------------------------

func TestRedisConnectivity(t *testing.T) {
	need(t, "REDIS_URL")
	c, err := cache.Open(os.Getenv("REDIS_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.Ping(ctx(t)); err != nil {
		t.Fatalf("ping: %v", err)
	}
	key := "fundzim:it:" + ids.New()
	if err := c.Set(ctx(t), key, "v", 5*time.Second); err != nil {
		t.Fatal(err)
	}
	if v, ok, err := c.Get(ctx(t), key); err != nil || !ok || v != "v" {
		t.Fatalf("get: %q %v %v", v, ok, err)
	}
	if err := c.Set(ctx(t), key, "v", 0); err == nil {
		t.Fatal("cache entries without TTL must be refused")
	}
}

// --- Object storage ------------------------------------------------------------------------------

type cred struct{ bucket, id, secret string }

func creds() map[storage.Class]cred {
	return map[storage.Class]cred{
		storage.PublicCampaignMedia:        {os.Getenv("STORAGE_PUBLIC_BUCKET"), os.Getenv("STORAGE_PUBLIC_ACCESS_KEY_ID"), os.Getenv("STORAGE_PUBLIC_SECRET_ACCESS_KEY")},
		storage.PrivateIdentityDocuments:   {os.Getenv("STORAGE_KYC_BUCKET"), os.Getenv("STORAGE_KYC_ACCESS_KEY_ID"), os.Getenv("STORAGE_KYC_SECRET_ACCESS_KEY")},
		storage.PrivateComplianceDocuments: {os.Getenv("STORAGE_EVIDENCE_BUCKET"), os.Getenv("STORAGE_EVIDENCE_ACCESS_KEY_ID"), os.Getenv("STORAGE_EVIDENCE_SECRET_ACCESS_KEY")},
	}
}

func client(t *testing.T, class storage.Class, c cred) *storage.Client {
	t.Helper()
	sc, err := storage.New(storage.Options{Endpoint: os.Getenv("STORAGE_ENDPOINT"), Region: os.Getenv("STORAGE_REGION"),
		ForcePathStyle: true, Credentials: map[storage.Class]storage.Credential{class: {Bucket: c.bucket, AccessKeyID: c.id, SecretAccessKey: c.secret}}})
	if err != nil {
		t.Fatal(err)
	}
	return sc
}

func TestStorageEachClassWithItsOwnCredential(t *testing.T) {
	need(t, "STORAGE_ENDPOINT", "STORAGE_KYC_BUCKET")
	for class, c := range creds() {
		t.Run(string(class), func(t *testing.T) {
			sc := client(t, class, c)
			if err := sc.Check(ctx(t), class); err != nil {
				t.Fatalf("check: %v", err)
			}
			ref := storage.NewRef(class)
			body := []byte("synthetic integration-test object; no personal data")
			if err := sc.Put(ctx(t), ref, bytes.NewReader(body), int64(len(body)), "text/plain"); err != nil {
				t.Fatalf("put: %v", err)
			}
			r, err := sc.Get(ctx(t), ref)
			if err != nil {
				t.Fatalf("get: %v", err)
			}
			got, _ := io.ReadAll(r)
			r.Close()
			if !bytes.Equal(got, body) {
				t.Fatal("content mismatch")
			}
			if err := sc.Delete(ctx(t), ref); err != nil {
				t.Fatalf("delete: %v", err)
			}
		})
	}
}

// Each credential must be DENIED on the other classes' buckets (I-19/I-26).
func TestStorageCredentialsCannotCrossBuckets(t *testing.T) {
	need(t, "STORAGE_ENDPOINT", "STORAGE_KYC_BUCKET")
	all := creds()
	for owner, c := range all {
		for target, tc := range all {
			if owner == target {
				continue
			}
			t.Run(string(owner)+"_on_"+string(target), func(t *testing.T) {
				// a client for the TARGET class/bucket, but holding the OWNER's credential
				sc := client(t, target, cred{bucket: tc.bucket, id: c.id, secret: c.secret})
				ref := storage.NewRef(target)
				err := sc.Put(ctx(t), ref, strings.NewReader("x"), 1, "text/plain")
				if err == nil {
					_ = sc.Delete(ctx(t), ref)
					t.Fatalf("credential of %s could write to the %s bucket", owner, target)
				}
				if err := sc.Check(ctx(t), target); err == nil {
					t.Fatalf("credential of %s could list the %s bucket", owner, target)
				}
			})
		}
	}
}

func TestPrivateBucketsHaveNoAnonymousAccess(t *testing.T) {
	need(t, "STORAGE_ENDPOINT", "STORAGE_KYC_BUCKET")
	kyc := creds()[storage.PrivateIdentityDocuments]
	sc := client(t, storage.PrivateIdentityDocuments, kyc)
	ref := storage.NewRef(storage.PrivateIdentityDocuments)
	if err := sc.Put(ctx(t), ref, strings.NewReader("synthetic"), 9, "text/plain"); err != nil {
		t.Fatal(err)
	}
	defer sc.Delete(ctx(t), ref)
	for _, bucket := range []string{kyc.bucket, os.Getenv("STORAGE_EVIDENCE_BUCKET")} {
		resp, err := http.Get(strings.TrimRight(os.Getenv("STORAGE_ENDPOINT"), "/") + "/" + bucket + "/" + ref.Key)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			t.Fatalf("anonymous GET on %s returned 200", bucket)
		}
	}
}

// --- API black box --------------------------------------------------------------------------------

func TestRunningAPI(t *testing.T) {
	base := os.Getenv("FUNDZIM_IT_API_URL")
	if base == "" {
		t.Skip("FUNDZIM_IT_API_URL not set (start the API, then set it to e.g. http://127.0.0.1:8080)")
	}
	type env struct {
		Data  map[string]any `json:"data"`
		Error map[string]any `json:"error"`
		Meta  map[string]any `json:"meta"`
	}
	get := func(path string) (int, env, http.Header) {
		resp, err := http.Get(base + path)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var e env
		_ = json.NewDecoder(resp.Body).Decode(&e)
		return resp.StatusCode, e, resp.Header
	}
	if code, e, h := get("/api/v1/health"); code != 200 || e.Data["status"] != "ok" || h.Get("X-Request-ID") == "" {
		t.Errorf("health: %d %v", code, e)
	}
	if code, e, _ := get("/api/v1/ready"); code != 200 || e.Data["status"] != "ready" {
		t.Errorf("ready: %d %v (are all services up?)", code, e)
	}
	if code, e, _ := get("/api/v1/version"); code != 200 || e.Data["name"] != "FundZim" {
		t.Errorf("version: %d %v", code, e)
	}
	if code, e, _ := get("/api/v1/does-not-exist"); code != 404 || e.Error["code"] != "ROUTE_NOT_FOUND" || e.Meta["request_id"] == "" {
		t.Errorf("404: %d %v", code, e)
	}
}

func TestFrontendReachesBackend(t *testing.T) {
	web := os.Getenv("FUNDZIM_IT_WEB_URL")
	if web == "" {
		t.Skip("FUNDZIM_IT_WEB_URL not set (e.g. http://127.0.0.1:3000)")
	}
	resp, err := http.Get(web + "/api/v1/version")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var e struct {
		Data map[string]any `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&e); err != nil || resp.StatusCode != 200 || e.Data["name"] != "FundZim" {
		t.Fatalf("web → api proxy: %d %v %v", resp.StatusCode, e, err)
	}
	home, err := http.Get(web + "/")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(home.Body)
	home.Body.Close()
	if home.StatusCode != 200 || !strings.Contains(string(b), "FundZim") {
		t.Fatalf("homepage: %d", home.StatusCode)
	}
}
