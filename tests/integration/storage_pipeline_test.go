//go:build integration

// Document storage pipeline (Stage 5 work stream S) against the REAL local stack: PostgreSQL (fundzim_app for
// uploads, fundzim_worker for scans), Garage (S3, SSE-C) and — when reachable — ClamAV clamd
// (FUNDZIM_IT_CLAMAV_ADDR, default 127.0.0.1:${CLAMAV_HOST_PORT:-3310}). Without clamd the tests fall back to
// the dev scanner and say so in the log.
//
// The tests call ScanObject / ExpireUploads directly with a fake clock and test-local keys (a random SSE-C
// key and ticket key per run), so they do not depend on the fundzim-worker container.
package integration

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Fatifizo/fundzim/internal/platform/clock"
	"github.com/Fatifizo/fundzim/internal/platform/errs"
	"github.com/Fatifizo/fundzim/internal/platform/httpx"
	pstorage "github.com/Fatifizo/fundzim/internal/platform/storage"
	"github.com/Fatifizo/fundzim/internal/storage"
)

type pipeline struct {
	t       *testing.T
	app     *pgxpool.Pool
	blobs   *pstorage.Client
	clock   *clock.Fake
	cfg     storage.Config
	api     *storage.Service // fundzim_app: uploads
	scanner storage.Scanner
	engine  string
}

func randKey(t *testing.T) []byte {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return b
}

func clamdAddr() string {
	if a := os.Getenv("FUNDZIM_IT_CLAMAV_ADDR"); a != "" {
		return a
	}
	port := os.Getenv("CLAMAV_HOST_PORT")
	if port == "" {
		port = "3310"
	}
	return "127.0.0.1:" + port
}

func newPipeline(t *testing.T) *pipeline {
	t.Helper()
	need(t, "DATABASE_URL", "POSTGRES_WORKER_PASSWORD", "STORAGE_ENDPOINT", "STORAGE_KYC_BUCKET", "STORAGE_EVIDENCE_BUCKET")
	all := map[pstorage.Class]pstorage.Credential{}
	for class, c := range creds() {
		all[class] = pstorage.Credential{Bucket: c.bucket, AccessKeyID: c.id, SecretAccessKey: c.secret}
	}
	blobs, err := pstorage.New(pstorage.Options{Endpoint: os.Getenv("STORAGE_ENDPOINT"), Region: os.Getenv("STORAGE_REGION"),
		ForcePathStyle: true, Credentials: all})
	if err != nil {
		t.Fatal(err)
	}
	p := &pipeline{t: t, app: pool(t, os.Getenv("DATABASE_URL")), blobs: blobs, clock: clock.NewFake(time.Now()),
		cfg: storage.Config{AppEnv: "test", UploadMaxBytes: 4 << 20, UploadTTL: 15 * time.Minute, ScanTimeout: 20 * time.Second,
			TicketKey: randKey(t), SSEKey: randKey(t)}}
	cs := &storage.ClamdScanner{Addr: clamdAddr(), Timeout: 20 * time.Second}
	if v, err := cs.Version(ctx(t)); err == nil {
		p.scanner, p.engine = cs, v
		t.Logf("scanner: real clamd at %s (%s)", cs.Addr, v)
	} else {
		p.scanner, p.engine = storage.DevScanner{}, storage.DevEngine
		t.Logf("scanner: clamd NOT reachable at %s (%v); using the dev scanner", cs.Addr, err)
	}
	p.api = p.service(p.app, nil, nil)
	return p
}

// service builds a Service on pool with scanner sc (nil: the pipeline's scanner) and blob store bs (nil: real).
func (p *pipeline) service(pl *pgxpool.Pool, sc storage.Scanner, bs storage.BlobStore) *storage.Service {
	p.t.Helper()
	if sc == nil {
		sc = p.scanner
	}
	if bs == nil {
		bs = p.blobs
	}
	s, err := storage.New(p.cfg, storage.Deps{Pool: pl, Blobs: bs, Scanner: sc, Clock: p.clock, Logger: slog.New(slog.DiscardHandler)})
	if err != nil {
		p.t.Fatal(err)
	}
	return s
}

func (p *pipeline) worker(sc storage.Scanner) *storage.Service {
	return p.service(pool(p.t, workerURL(p.t)), sc, nil)
}

type objRow struct {
	status, reject, lastErr, engine, signature string
	attempts                                   int
	purged, promoted                           bool
}

func (p *pipeline) row(id string) objRow {
	p.t.Helper()
	var r objRow
	err := p.app.QueryRow(ctx(p.t), `SELECT scan_status, coalesce(reject_reason, ''), coalesce(last_scan_error, ''),
		coalesce(scan_engine, ''), coalesce(scan_signature, ''), scan_attempts, purged_at IS NOT NULL, promoted_key IS NOT NULL
		FROM app.stored_objects WHERE id = $1`, id).
		Scan(&r.status, &r.reject, &r.lastErr, &r.engine, &r.signature, &r.attempts, &r.purged, &r.promoted)
	if err != nil {
		p.t.Fatalf("load row %s: %v", id, err)
	}
	return r
}

func classOf(bucket string) pstorage.Class {
	switch bucket {
	case storage.BucketPrivateKYC:
		return pstorage.PrivateIdentityDocuments
	case storage.BucketPrivateEvidence:
		return pstorage.PrivateComplianceDocuments
	}
	return pstorage.PublicCampaignMedia
}

// blobExists probes a key WITHOUT the SSE-C key: Garage answers "object is encrypted" (400) for an existing
// SSE-C object and NoSuchKey for a missing one. So a non-ErrNotFound error proves existence AND that the bytes
// cannot be read without the key.
func (p *pipeline) blobExists(bucket, key string) bool {
	p.t.Helper()
	rc, err := p.blobs.GetSSE(ctx(p.t), pstorage.RefFor(classOf(bucket), key), nil)
	if err == nil {
		rc.Close()
		p.t.Errorf("%s/%s readable WITHOUT the SSE-C key: not encrypted at rest", bucket, key)
		return true
	}
	return !errors.Is(err, pstorage.ErrNotFound)
}

func (p *pipeline) upload(bucket, purpose, ctype string, content []byte) storage.Object {
	p.t.Helper()
	o, err := p.api.Upload(ctx(p.t), storage.UploadInput{BucketClass: bucket, Purpose: purpose, OwnerModule: "kyc",
		DeclaredContentType: ctype, Body: bytes.NewReader(content)})
	if err != nil {
		p.t.Fatalf("upload: %v", err)
	}
	return o
}

func itPNG(t *testing.T) []byte {
	img := image.NewRGBA(image.Rect(0, 0, 16, 16))
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func itPDF(extra string) []byte {
	return []byte("%PDF-1.4\n1 0 obj << /Type /Catalog /Pages 2 0 R >> endobj\n2 0 obj << /Type /Pages /Kids [] /Count 0 >> endobj\n" +
		extra + "trailer << /Root 1 0 R >>\n%%EOF\n")
}

// eicarPDF embeds the EICAR test string as a PDF stream; ClamAV extracts PDF streams and detects it.
func eicarPDF() []byte {
	e := string(storage.EICAR())
	return itPDF(fmt.Sprintf("3 0 obj << /Length %d >>\nstream\n%s\nendstream\nendobj\n", len(e), e))
}

func TestStoragePipelineUploadScanPromote(t *testing.T) {
	p := newPipeline(t)
	w := p.worker(nil)
	cases := []struct {
		bucket, purpose, ctype string
		content                []byte
	}{
		{storage.BucketPrivateKYC, "KYC_DOCUMENT", "application/pdf", itPDF("")},
		{storage.BucketPrivateEvidence, "COMPLIANCE_EVIDENCE", "image/png", itPNG(t)},
		{storage.BucketPrivateKYC, "PAYOUT_DESTINATION_EVIDENCE", "image/png", itPNG(t)},
	}
	for _, c := range cases {
		t.Run(c.purpose, func(t *testing.T) {
			o := p.upload(c.bucket, c.purpose, c.ctype, c.content)
			if o.Status != storage.StatusQuarantined || o.SizeBytes != int64(len(c.content)) || len(o.SHA256) != 32 {
				t.Fatalf("after upload: %+v", o)
			}
			// never readable before CLEAN
			if _, _, err := p.api.Open(ctx(t), o.ID); !errors.Is(err, storage.ErrNotClean) {
				t.Fatalf("open before scan: %v", err)
			}
			if !p.blobExists(c.bucket, "quarantine/"+o.ID) {
				t.Fatal("quarantine object missing")
			}
			if p.blobExists(c.bucket, "objects/"+o.ID) {
				t.Fatal("object promoted before scanning")
			}
			// anonymous HTTP GET on the bucket object URL is denied
			url := strings.TrimRight(os.Getenv("STORAGE_ENDPOINT"), "/") + "/" + p.blobs.Bucket(classOf(c.bucket)) + "/" +
				classOf(c.bucket).Prefix() + "quarantine/" + o.ID
			resp, err := http.Get(url)
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusForbidden {
				t.Fatalf("anonymous GET %s: %d", url, resp.StatusCode)
			}

			if err := w.ScanObject(ctx(t), o.ID); err != nil {
				t.Fatal(err)
			}
			r := p.row(o.ID)
			if r.status != storage.StatusClean || r.engine != p.engine || !r.promoted || r.attempts != 1 {
				t.Fatalf("after scan: %+v", r)
			}
			if p.blobExists(c.bucket, "quarantine/"+o.ID) {
				t.Fatal("quarantine copy not removed after promotion")
			}
			if !p.blobExists(c.bucket, "objects/"+o.ID) {
				t.Fatal("promoted object missing")
			}
			rc, got, err := p.api.Open(ctx(t), o.ID)
			if err != nil {
				t.Fatal(err)
			}
			b, err := io.ReadAll(rc)
			rc.Close()
			if err != nil || !bytes.Equal(b, c.content) || got.Status != storage.StatusClean {
				t.Fatalf("open after clean: %v", err)
			}
			var events, audits int
			_ = p.app.QueryRow(ctx(t), `SELECT count(*) FROM app.outbox_events WHERE aggregate_id = $1 AND event_type = 'storage.object_scanned'
				AND payload->>'status' = 'CLEAN' AND payload->>'owner_module' = 'kyc'`, o.ID).Scan(&events)
			_ = p.app.QueryRow(ctx(t), `SELECT count(*) FROM audit.audit_events WHERE target_id = $1
				AND action IN ('storage.object.uploaded', 'storage.object.scanned')`, o.ID).Scan(&audits)
			if events != 1 || audits != 2 {
				t.Fatalf("events %d (want 1), audit events %d (want 2)", events, audits)
			}
			// idempotent: scanning a CLEAN object again changes nothing
			if err := w.ScanObject(ctx(t), o.ID); err != nil || p.row(o.ID).attempts != 1 {
				t.Fatalf("re-scan of a CLEAN object: %v %+v", err, p.row(o.ID))
			}
		})
	}
}

func TestStoragePipelineEICARRejected(t *testing.T) {
	p := newPipeline(t)
	o := p.upload(storage.BucketPrivateKYC, "KYC_DOCUMENT", "application/pdf", eicarPDF())
	if err := p.worker(nil).ScanObject(ctx(t), o.ID); err != nil {
		t.Fatal(err)
	}
	r := p.row(o.ID)
	if r.status != storage.StatusRejected || r.reject != storage.RejectMalware || r.signature == "" || r.promoted {
		t.Fatalf("eicar: %+v", r)
	}
	t.Logf("EICAR detected by %s as %q", r.engine, r.signature)
	if _, _, err := p.api.Open(ctx(t), o.ID); !errors.Is(err, storage.ErrNotClean) {
		t.Fatalf("open rejected: %v", err)
	}
	if p.blobExists(storage.BucketPrivateKYC, "objects/"+o.ID) {
		t.Fatal("rejected object was promoted")
	}
	var n int
	_ = p.app.QueryRow(ctx(t), `SELECT count(*) FROM app.outbox_events WHERE aggregate_id = $1 AND event_type = 'storage.object_scanned'
		AND payload->>'status' = 'REJECTED'`, o.ID).Scan(&n)
	if n != 1 {
		t.Fatalf("REJECTED event count %d", n)
	}
}

func TestStoragePipelineScannerOutageThenRecovery(t *testing.T) {
	p := newPipeline(t)
	o := p.upload(storage.BucketPrivateEvidence, "COMPLIANCE_EVIDENCE", "application/pdf", itPDF(""))
	down := p.worker(&storage.ClamdScanner{Addr: "127.0.0.1:1", Timeout: 2 * time.Second})
	if err := down.ScanObject(ctx(t), o.ID); err != nil {
		t.Fatal(err)
	}
	r := p.row(o.ID)
	if r.status != storage.StatusFailedScan || r.lastErr != storage.ScanErrUnavailable || r.promoted {
		t.Fatalf("outage: %+v", r)
	}
	if _, _, err := p.api.Open(ctx(t), o.ID); !errors.Is(err, storage.ErrNotClean) {
		t.Fatalf("FAILED_SCAN must not be readable: %v", err)
	}
	// not due yet: a retry right away does nothing
	up := p.worker(nil)
	if err := up.ScanObject(ctx(t), o.ID); err != nil || p.row(o.ID).status != storage.StatusFailedScan {
		t.Fatalf("retry before next_scan_at: %v %+v", err, p.row(o.ID))
	}
	p.clock.Advance(2 * time.Hour)
	if err := up.ScanObject(ctx(t), o.ID); err != nil {
		t.Fatal(err)
	}
	if r := p.row(o.ID); r.status != storage.StatusClean || r.attempts != 2 || r.lastErr != "" {
		t.Fatalf("recovery: %+v", r)
	}
}

// interruptingScanner simulates a worker that dies mid-scan: during the first scan the lease is recovered
// (as the rescan job would after the lease expires); its late "clean" verdict must then be discarded.
type interruptingScanner struct {
	inner   storage.Scanner
	recover func()
	calls   atomic.Int32
}

func (s *interruptingScanner) Scan(ctx context.Context, r io.Reader) (storage.Verdict, error) {
	if s.calls.Add(1) == 1 {
		s.recover()
	}
	return s.inner.Scan(ctx, r)
}

func TestStoragePipelineInterruptedScanIsFenced(t *testing.T) {
	p := newPipeline(t)
	o := p.upload(storage.BucketPrivateKYC, "KYB_DOCUMENT", "image/png", itPNG(t))
	var w *storage.Service
	sc := &interruptingScanner{inner: p.scanner}
	sc.recover = func() {
		p.clock.Advance(time.Hour)
		if n, err := w.RecoverInterrupted(context.Background()); err != nil || n < 1 {
			t.Errorf("recover interrupted: %d %v", n, err)
		}
	}
	w = p.worker(sc)
	if err := w.ScanObject(ctx(t), o.ID); err != nil {
		t.Fatal(err)
	}
	r := p.row(o.ID)
	if r.status != storage.StatusFailedScan || r.lastErr != storage.ScanErrInterrupted || r.promoted {
		t.Fatalf("a late result from an interrupted attempt was applied: %+v", r)
	}
	if err := w.ScanObject(ctx(t), o.ID); err != nil {
		t.Fatal(err)
	}
	if r := p.row(o.ID); r.status != storage.StatusClean || r.attempts != 2 {
		t.Fatalf("rescan after interruption: %+v", r)
	}
}

type countingScanner struct {
	inner storage.Scanner
	calls atomic.Int32
}

func (s *countingScanner) Scan(ctx context.Context, r io.Reader) (storage.Verdict, error) {
	s.calls.Add(1)
	time.Sleep(200 * time.Millisecond) // widen the race window
	return s.inner.Scan(ctx, r)
}

func TestStoragePipelineConcurrentScansPromoteOnce(t *testing.T) {
	p := newPipeline(t)
	o := p.upload(storage.BucketPrivateKYC, "BENEFICIARY_EVIDENCE", "application/pdf", itPDF(""))
	sc := &countingScanner{inner: p.scanner}
	workers := []*storage.Service{p.worker(sc), p.worker(sc), p.worker(sc)}
	var wg sync.WaitGroup
	errc := make(chan error, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errc <- workers[i%len(workers)].ScanObject(context.Background(), o.ID)
		}(i)
	}
	wg.Wait()
	close(errc)
	for err := range errc {
		if err != nil {
			t.Fatal(err)
		}
	}
	r := p.row(o.ID)
	if r.status != storage.StatusClean || r.attempts != 1 || sc.calls.Load() != 1 {
		t.Fatalf("concurrent scans: %+v, scanner calls %d", r, sc.calls.Load())
	}
	var events, audits int
	_ = p.app.QueryRow(ctx(t), `SELECT count(*) FROM app.outbox_events WHERE aggregate_id = $1 AND event_type = 'storage.object_scanned'`, o.ID).Scan(&events)
	_ = p.app.QueryRow(ctx(t), `SELECT count(*) FROM audit.audit_events WHERE target_id = $1 AND action = 'storage.object.scanned'`, o.ID).Scan(&audits)
	if events != 1 || audits != 1 {
		t.Fatalf("double promotion: %d events, %d audit events", events, audits)
	}
}

// crashingBlobs writes the bytes, then kills the uploading goroutine before the upload is recorded — the
// moral equivalent of the API process dying mid-upload.
type crashingBlobs struct{ storage.BlobStore }

func (c crashingBlobs) PutSSE(ctx context.Context, ref pstorage.Ref, body io.ReadSeeker, size int64, ct string, key pstorage.SSEKey) error {
	_ = c.BlobStore.PutSSE(ctx, ref, body, size, ct, key)
	runtime.Goexit()
	return nil
}

func TestStoragePipelineInterruptedUploadCleanedUp(t *testing.T) {
	p := newPipeline(t)
	crashing := p.service(p.app, nil, crashingBlobs{p.blobs})
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = crashing.Upload(context.Background(), storage.UploadInput{BucketClass: storage.BucketPrivateKYC, Purpose: "KYC_SELFIE",
			OwnerModule: "kyc", DeclaredContentType: "image/png", Body: bytes.NewReader(itPNG(t))})
	}()
	<-done
	var id string
	if err := p.app.QueryRow(ctx(t), `SELECT id::text FROM app.stored_objects WHERE purpose = 'KYC_SELFIE' AND scan_status = 'UPLOADED'
		ORDER BY id DESC LIMIT 1`).Scan(&id); err != nil {
		t.Fatalf("crashed upload row: %v", err)
	}
	if !p.blobExists(storage.BucketPrivateKYC, "quarantine/"+id) {
		t.Fatal("expected partial bytes in quarantine")
	}
	if n, err := p.api.ExpireUploads(ctx(t), 1000); err != nil || n != 0 {
		t.Fatalf("expired before TTL: %d %v", n, err)
	}
	p.clock.Advance(p.cfg.UploadTTL + time.Minute)
	if _, err := p.api.ExpireUploads(ctx(t), 1000); err != nil {
		t.Fatal(err)
	}
	r := p.row(id)
	if r.status != storage.StatusDeleted || !r.purged {
		t.Fatalf("after expiry: %+v", r)
	}
	var sess string
	_ = p.app.QueryRow(ctx(t), `SELECT status FROM app.upload_sessions WHERE stored_object_id = $1`, id).Scan(&sess)
	if sess != "EXPIRED" || p.blobExists(storage.BucketPrivateKYC, "quarantine/"+id) {
		t.Fatalf("session %s / partial bytes not removed", sess)
	}
}

func TestStoragePipelineUploadRejectionsStoreNothing(t *testing.T) {
	p := newPipeline(t)
	cases := []struct {
		name, ctype string
		body        []byte
		want        error
		reason      string
	}{
		{"png declared as pdf", "application/pdf", itPNG(t), storage.ErrTypeMismatch, storage.RejectTypeMismatch},
		{"html disguised as png", "image/png", []byte("<html><script>alert(1)</script></html>"), storage.ErrUnsupportedType, storage.RejectUnsupported},
		{"empty", "image/png", nil, storage.ErrEmptyFile, storage.RejectEmpty},
		{"too large", "application/pdf", append(itPDF(""), bytes.Repeat([]byte(" "), 4<<20)...), storage.ErrTooLarge, storage.RejectTooLarge},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := p.api.Upload(ctx(t), storage.UploadInput{BucketClass: storage.BucketPrivateKYC, Purpose: "KYC_DOCUMENT",
				OwnerModule: "kyc", DeclaredContentType: c.ctype, Body: bytes.NewReader(c.body)})
			if !errors.Is(err, c.want) {
				t.Fatalf("err %v, want %v", err, c.want)
			}
			var id, reason, status string
			if err := p.app.QueryRow(ctx(t), `SELECT id::text, scan_status, coalesce(reject_reason, '') FROM app.stored_objects
				WHERE purpose = 'KYC_DOCUMENT' ORDER BY id DESC LIMIT 1`).Scan(&id, &status, &reason); err != nil {
				t.Fatal(err)
			}
			if status != storage.StatusRejected || reason != c.reason {
				t.Fatalf("row %s %s", status, reason)
			}
			if p.blobExists(storage.BucketPrivateKYC, "quarantine/"+id) {
				t.Fatal("rejected upload reached the bucket")
			}
		})
	}
}

func TestStoredObjectsDatabaseGuards(t *testing.T) {
	p := newPipeline(t)
	o := p.upload(storage.BucketPrivateKYC, "KYC_DOCUMENT", "application/pdf", itPDF(""))
	for name, stmt := range map[string]string{
		"skip the scan":            `UPDATE app.stored_objects SET scan_status = 'CLEAN' WHERE id = $1`,
		"promote without scanning": `UPDATE app.stored_objects SET promoted_key = 'objects/' || id::text, promoted_at = now() WHERE id = $1`,
		"change bucket class":      `UPDATE app.stored_objects SET bucket_class = 'PUBLIC_MEDIA' WHERE id = $1`,
		"rewrite the hash":         `UPDATE app.stored_objects SET content_sha256 = '\x00000000000000000000000000000000000000000000000000000000000000ff' WHERE id = $1`,
		"change the key":           `UPDATE app.stored_objects SET quarantine_key = 'quarantine/../x' WHERE id = $1`,
		"hard delete":              `DELETE FROM app.stored_objects WHERE id = $1`,
		"back to UPLOADED":         `UPDATE app.stored_objects SET scan_status = 'UPLOADED' WHERE id = $1`,
	} {
		if _, err := p.app.Exec(ctx(t), stmt, o.ID); err == nil {
			t.Errorf("%s: allowed", name)
		}
	}
	if _, err := p.app.Exec(ctx(t), `INSERT INTO app.stored_objects (id, bucket_class, purpose, owner_module, quarantine_key,
		declared_content_type, scan_status, classification, retention_class, encryption_key_id)
		VALUES ($1, 'PRIVATE_KYC', 'KYC_DOCUMENT', 'kyc', 'quarantine/' || $1::text, 'image/png', 'CLEAN', 'C3', 'KYC', 'x/y')`,
		"0192f5a1-0000-7000-8000-00000000abcd"); err == nil {
		t.Error("an object could be inserted CLEAN")
	}
	if _, err := p.app.Exec(ctx(t), `INSERT INTO app.stored_objects (id, bucket_class, purpose, owner_module, quarantine_key,
		declared_content_type, scan_status, classification, retention_class)
		VALUES ($1, 'PRIVATE_KYC', 'KYC_DOCUMENT', 'kyc', 'quarantine/' || $1::text, 'image/png', 'UPLOADED', 'C3', 'KYC')`,
		"0192f5a1-0000-7000-8000-00000000abce"); err == nil {
		t.Error("a private object without encryption_key_id was accepted")
	}
	// soft delete works, is idempotent and audited
	if err := p.api.Delete(ctx(t), o.ID, ""); err != nil {
		t.Fatal(err)
	}
	if err := p.api.Delete(ctx(t), o.ID, ""); err != nil {
		t.Fatal(err)
	}
	if r := p.row(o.ID); r.status != storage.StatusDeleted {
		t.Fatalf("after delete: %+v", r)
	}
	if _, err := p.app.Exec(ctx(t), `UPDATE app.stored_objects SET scan_status = 'CLEAN' WHERE id = $1`, o.ID); err == nil {
		t.Error("DELETED is not final")
	}
}

// End to end through HTTP: the upload route is exempt from the global 1 MiB body limit; others are not.
func TestStorageUploadRouteBodyLimitExemption(t *testing.T) {
	p := newPipeline(t)
	logger := slog.New(slog.DiscardHandler)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/verification/documents", func(w http.ResponseWriter, r *http.Request) {
		u, err := storage.ParseMultipart(w, r, storage.MultipartSpec{FileField: "file", Fields: []string{"subject_type"},
			MaxFileBytes: p.cfg.UploadMaxBytes})
		if err == nil {
			_, err = p.api.Upload(r.Context(), storage.UploadInput{BucketClass: storage.BucketPrivateKYC, Purpose: "KYC_DOCUMENT",
				OwnerModule: "kyc", DeclaredContentType: u.DeclaredContentType, Body: u.File})
		}
		if err != nil {
			httpx.WriteError(w, r, logger, errs.As(err))
			return
		}
		w.WriteHeader(http.StatusCreated)
	})
	mux.HandleFunc("POST /api/v1/other", func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.ReadAll(r.Body); err != nil {
			httpx.WriteError(w, r, logger, errs.New(errs.TooLarge, errs.CodePayloadTooLarge, "too large"))
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	h := httpx.BodyLimitWithExemptions(1<<20, []httpx.BodyLimitExemption{
		{Pattern: "POST /api/v1/verification/documents", Max: p.cfg.UploadMaxBytes + 64<<10}}, logger)(mux)
	srv := httptest.NewServer(h)
	defer srv.Close()

	big := append(itPDF(""), bytes.Repeat([]byte(" "), 2<<20)...) // 2 MiB: above the global limit
	post := func(path string, file []byte) int {
		var b bytes.Buffer
		mw := multipart.NewWriter(&b)
		_ = mw.WriteField("subject_type", "KYC_CASE")
		fw, _ := mw.CreatePart(map[string][]string{"Content-Disposition": {`form-data; name="file"; filename="x.pdf"`},
			"Content-Type": {"application/pdf"}})
		_, _ = fw.Write(file)
		_ = mw.Close()
		resp, err := http.Post(srv.URL+path, mw.FormDataContentType(), &b)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if c := post("/api/v1/verification/documents", big); c != http.StatusCreated {
		t.Fatalf("2 MiB upload on the exempt route: %d", c)
	}
	if c := post("/api/v1/other", big); c != http.StatusRequestEntityTooLarge {
		t.Fatalf("2 MiB on a normal route: %d", c)
	}
	huge := append(itPDF(""), bytes.Repeat([]byte(" "), 5<<20)...) // above UPLOAD cap
	if c := post("/api/v1/verification/documents", huge); c != http.StatusRequestEntityTooLarge {
		t.Fatalf("5 MiB upload: %d", c)
	}
}
