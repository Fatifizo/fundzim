package storage

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Fatifizo/fundzim/internal/audit"
	"github.com/Fatifizo/fundzim/internal/platform/authz"
	"github.com/Fatifizo/fundzim/internal/platform/clock"
	"github.com/Fatifizo/fundzim/internal/platform/db"
	"github.com/Fatifizo/fundzim/internal/platform/ids"
	"github.com/Fatifizo/fundzim/internal/platform/jobs"
	"github.com/Fatifizo/fundzim/internal/platform/outbox"
	pstorage "github.com/Fatifizo/fundzim/internal/platform/storage"
)

// UnencryptedKeyID is recorded as encryption_key_id for private objects when no SSE-C key is configured
// (development/test only). It states the truth: the object store holds the bytes unencrypted.
const UnencryptedKeyID = "none/garage-at-rest-unencrypted-dev"

// BlobStore is the subset of the platform object-storage client the pipeline uses (*storage.Client).
type BlobStore interface {
	PutSSE(ctx context.Context, ref pstorage.Ref, body io.ReadSeeker, size int64, contentType string, key pstorage.SSEKey) error
	GetSSE(ctx context.Context, ref pstorage.Ref, key pstorage.SSEKey) (io.ReadCloser, error)
	CopySSE(ctx context.Context, src, dst pstorage.Ref, key pstorage.SSEKey) error
	Delete(ctx context.Context, ref pstorage.Ref) error
}

// Config configures the service. The composition root fills it from config.Verification / config.Security.
type Config struct {
	AppEnv               string        // APP_ENV
	UploadMaxBytes       int64         // UPLOAD_MAX_BYTES (default 10 MiB)
	UploadTTL            time.Duration // UPLOAD_TTL (default 15 min)
	ScanTimeout          time.Duration // MALWARE_SCAN_TIMEOUT (default 60 s)
	TicketKey            []byte        // DOCUMENT_TICKET_KEY (32 bytes)
	TicketTTL            time.Duration // DOCUMENT_TICKET_TTL (default 60 s)
	SSEKey               []byte        // STORAGE_SSE_C_KEY (32 bytes); nil = unencrypted, development/test only
	MaxConcurrentUploads int           // uploads buffered at once per process (default 8; memory ≈ n × UploadMaxBytes)
	MaxScanAttempts      int           // automatic scan attempts before an object needs an operator (default 10)
	ScanBackoff          jobs.Backoff  // FAILED_SCAN retry delay (default 30 s doubling to 1 h)
}

// Deps are the service's collaborators.
type Deps struct {
	Pool    *pgxpool.Pool // fundzim_app (API) or fundzim_worker (worker)
	Blobs   BlobStore     // needs the credentials of every bucket class it serves
	Scanner Scanner       // required for scanning (worker); may be nil in the API process
	Clock   clock.Clock
	Logger  *slog.Logger
	Metrics *Metrics
}

// Service implements the storage contract (§3).
type Service struct {
	cfg     Config
	pool    *pgxpool.Pool
	blobs   BlobStore
	scanner Scanner
	clock   clock.Clock
	logger  *slog.Logger
	metrics *Metrics
	tickets *Tickets
	keyID   string
	slots   chan struct{}
}

func isLocalEnv(env string) bool { return env == "development" || env == "test" }

// New validates cfg and builds the service.
func New(cfg Config, d Deps) (*Service, error) {
	if d.Pool == nil || d.Blobs == nil {
		return nil, invalidInput("pool and blob store are required")
	}
	if cfg.AppEnv == "" {
		return nil, invalidInput("AppEnv is required")
	}
	if cfg.UploadMaxBytes <= 0 {
		cfg.UploadMaxBytes = 10 << 20
	}
	if cfg.UploadTTL <= 0 {
		cfg.UploadTTL = 15 * time.Minute
	}
	if cfg.ScanTimeout <= 0 {
		cfg.ScanTimeout = 60 * time.Second
	}
	if cfg.MaxConcurrentUploads <= 0 {
		cfg.MaxConcurrentUploads = 8
	}
	if cfg.MaxScanAttempts <= 0 {
		cfg.MaxScanAttempts = 10
	}
	if cfg.ScanBackoff.Base <= 0 {
		cfg.ScanBackoff = jobs.Backoff{Base: 30 * time.Second, Cap: time.Hour}
	}
	if d.Clock == nil {
		d.Clock = clock.System
	}
	if d.Logger == nil {
		d.Logger = slog.New(slog.DiscardHandler)
	}
	if d.Metrics == nil {
		d.Metrics = NewMetrics(nil)
	}
	if _, isDev := d.Scanner.(DevScanner); isDev && !isLocalEnv(cfg.AppEnv) {
		return nil, ErrDevScannerInProduction
	}
	keyID := UnencryptedKeyID
	switch {
	case cfg.SSEKey == nil:
		if !isLocalEnv(cfg.AppEnv) {
			return nil, invalidInput("STORAGE_SSE_C_KEY is required outside development and test (private documents must be encrypted at rest)")
		}
	case len(cfg.SSEKey) != 32:
		return nil, invalidInput("STORAGE_SSE_C_KEY must be 32 bytes")
	default:
		if !isLocalEnv(cfg.AppEnv) {
			// same rule as FIELD_ENCRYPTION_PROVIDER=local: a key from the environment is not KMS (Stage 18)
			return nil, invalidInput("a local STORAGE_SSE_C_KEY is refused outside development and test (KMS is required; Stage 18)")
		}
		fp := sha256.Sum256(append([]byte("fundzim-sse-key-id/"), cfg.SSEKey...))
		keyID = "sse-c/hmac-sha256/local/" + hex.EncodeToString(fp[:8])
	}
	tickets, err := NewTickets(cfg.TicketKey, cfg.TicketTTL, d.Clock.Now)
	if err != nil {
		return nil, err
	}
	return &Service{cfg: cfg, pool: d.Pool, blobs: d.Blobs, scanner: d.Scanner, clock: d.Clock, logger: d.Logger,
		metrics: d.Metrics, tickets: tickets, keyID: keyID, slots: make(chan struct{}, cfg.MaxConcurrentUploads)}, nil
}

// Metrics returns the service's metrics.
func (s *Service) Metrics() *Metrics { return s.metrics }

// sse returns the per-object SSE-C key for a private object (nil for public media or when unconfigured).
func (s *Service) sse(bucket, objectID string) pstorage.SSEKey {
	if bucket == BucketPublicMedia || s.cfg.SSEKey == nil {
		return nil
	}
	m := hmac.New(sha256.New, s.cfg.SSEKey)
	m.Write([]byte("fundzim/sse-c/v1/" + objectID))
	return m.Sum(nil)
}

func (s *Service) encryptionKeyID(bucket string) any {
	if bucket == BucketPublicMedia {
		return nil
	}
	return s.keyID
}

func quarantineKey(id string) string { return "quarantine/" + id }
func promotedKey(id string) string   { return "objects/" + id }

func nullable(v string) any {
	if v == "" {
		return nil
	}
	return v
}

// --- Upload --------------------------------------------------------------------------------------------

// Upload streams in.Body into a bounded buffer while hashing it, applies the content rules, writes it to the
// bucket class's quarantine key and records the object as QUARANTINED (emitting storage.object_uploaded).
// Content rejections return ErrTooLarge / ErrEmptyFile / ErrUnsupportedType / ErrTypeMismatch /
// ErrMalformedUpload and leave a REJECTED row; infrastructure failures return ErrStorageUnavailable.
func (s *Service) Upload(ctx context.Context, in UploadInput) (Object, error) {
	info, ok := purposes[in.Purpose]
	if !ok || info.bucket != in.BucketClass {
		return Object{}, invalidInput("purpose %q is not valid for bucket class %q", in.Purpose, in.BucketClass)
	}
	if !ownerModules[in.OwnerModule] {
		return Object{}, invalidInput("unknown owner module %q", in.OwnerModule)
	}
	if in.UploaderUserID != "" && !ids.Valid(in.UploaderUserID) {
		return Object{}, invalidInput("uploader must be a UUID")
	}
	if in.Body == nil {
		return Object{}, invalidInput("body is required")
	}
	max := in.MaxBytes
	if max <= 0 || max > s.cfg.UploadMaxBytes {
		max = s.cfg.UploadMaxBytes
	}
	declared := truncate(in.DeclaredContentType, 255)
	if declared == "" {
		declared = "application/octet-stream"
	}

	select {
	case s.slots <- struct{}{}:
		defer func() { <-s.slots }()
	case <-ctx.Done():
		return Object{}, errors.Join(ErrStorageUnavailable, ctx.Err())
	}

	id := ids.New()
	now := s.clock.Now().UTC()
	obj := Object{ID: id, BucketClass: in.BucketClass, Purpose: in.Purpose, OwnerModule: in.OwnerModule,
		UploadedBy: in.UploaderUserID, Status: StatusUploaded, CreatedAt: now}
	err := db.WithTx(ctx, s.pool, db.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
			INSERT INTO app.stored_objects (id, bucket_class, purpose, owner_module, quarantine_key, declared_content_type,
			  scan_status, classification, retention_class, encryption_key_id, uploaded_by_user_id, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, 'UPLOADED', $7, $8, $9, $10, $11, $11)`,
			id, in.BucketClass, in.Purpose, in.OwnerModule, quarantineKey(id), declared, info.classification, info.retention,
			s.encryptionKeyID(in.BucketClass), nullable(in.UploaderUserID), now); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `
			INSERT INTO app.upload_sessions (id, stored_object_id, user_id, max_bytes, status, expires_at, created_at, updated_at)
			VALUES ($1, $2, $3, $4, 'PENDING', $5, $6, $6)`,
			ids.New(), id, nullable(in.UploaderUserID), max, now.Add(s.cfg.UploadTTL), now)
		return err
	})
	if err != nil {
		s.metrics.uploads.WithLabelValues(in.BucketClass, "failed").Inc()
		return Object{}, errors.Join(ErrStorageUnavailable, fmt.Errorf("storage: create upload: %w", err))
	}

	// Read with a hard cap: max+1 bytes tells "too large" apart from "exactly max".
	var buf bytes.Buffer
	h := sha256.New()
	n, rerr := io.Copy(io.MultiWriter(&buf, h), io.LimitReader(in.Body, max+1))
	var mbe *http.MaxBytesError
	tooLarge := n > max || errors.As(rerr, &mbe)
	if rerr != nil && !tooLarge {
		// the client went away or sent a malformed multipart body: nothing is stored
		return Object{}, s.reject(ctx, obj, RejectMalformed, errors.Join(ErrMalformedUpload, rerr))
	}
	sniffed, reason, cerr := checkContent(in.BucketClass, declared, buf.Bytes(), tooLarge)
	if cerr != nil {
		return Object{}, s.reject(ctx, obj, reason, cerr)
	}
	sum := h.Sum(nil)
	content := buf.Bytes()

	ref := pstorage.RefFor(platformClass(in.BucketClass), quarantineKey(id))
	if err := s.blobs.PutSSE(ctx, ref, bytes.NewReader(content), int64(len(content)), sniffed, s.sse(in.BucketClass, id)); err != nil {
		s.failUpload(ctx, obj, ref)
		s.metrics.uploads.WithLabelValues(in.BucketClass, "failed").Inc()
		return Object{}, errors.Join(ErrStorageUnavailable, fmt.Errorf("storage: put quarantine object: %w", err))
	}

	obj.Status, obj.SniffedType, obj.SizeBytes, obj.SHA256 = StatusQuarantined, sniffed, int64(len(content)), sum
	err = db.WithTx(ctx, s.pool, db.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
			UPDATE app.stored_objects SET scan_status = 'QUARANTINED', content_sha256 = $2, size_bytes = $3, sniffed_content_type = $4
			 WHERE id = $1 AND scan_status = 'UPLOADED'`, id, sum, len(content), sniffed)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return errors.New("object is no longer UPLOADED (expired while uploading)")
		}
		if _, err := tx.Exec(ctx, `UPDATE app.upload_sessions SET status = 'COMPLETED', finished_at = $2
			 WHERE stored_object_id = $1 AND status = 'PENDING'`, id, s.clock.Now().UTC()); err != nil {
			return err
		}
		if _, err := outbox.Write(ctx, tx, outbox.Event{AggregateType: "stored_object", AggregateID: id,
			EventType: EventObjectUploaded, OccurredAt: s.clock.Now().UTC(),
			Payload: map[string]string{"object_id": id, "bucket_class": in.BucketClass, "purpose": in.Purpose}}); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{Action: "storage.object.uploaded", TargetType: "stored_object", TargetID: id,
			OccurredAt: s.clock.Now().UTC(), Metadata: map[string]any{"bucket_class": in.BucketClass, "purpose": in.Purpose,
				"owner_module": in.OwnerModule, "size_bytes": len(content), "media_type": sniffed,
				"content_sha256": hex.EncodeToString(sum)}})
	})
	if err != nil {
		// The bytes are in quarantine but not recorded; the row stays UPLOADED and the expiry job removes both.
		s.metrics.uploads.WithLabelValues(in.BucketClass, "failed").Inc()
		return Object{}, errors.Join(ErrStorageUnavailable, fmt.Errorf("storage: record upload: %w", err))
	}
	s.metrics.uploads.WithLabelValues(in.BucketClass, "accepted").Inc()
	s.metrics.uploadBytes.Observe(float64(len(content)))
	return obj, nil
}

// finalizeCtx keeps bookkeeping alive after the client disconnects.
func finalizeCtx(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
}

// reject records a content rejection (UPLOADED → REJECTED, session ABORTED) and returns cause.
func (s *Service) reject(ctx context.Context, obj Object, reason string, cause error) error {
	fctx, cancel := finalizeCtx(ctx)
	defer cancel()
	err := db.WithTx(fctx, s.pool, db.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE app.stored_objects SET scan_status = 'REJECTED', reject_reason = $2
			 WHERE id = $1 AND scan_status = 'UPLOADED'`, obj.ID, reason); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE app.upload_sessions SET status = 'ABORTED', finished_at = $2
			 WHERE stored_object_id = $1 AND status = 'PENDING'`, obj.ID, s.clock.Now().UTC()); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{Action: "storage.object.upload_rejected", TargetType: "stored_object",
			TargetID: obj.ID, Outcome: "failed", Reason: reason, OccurredAt: s.clock.Now().UTC(),
			Metadata: map[string]any{"bucket_class": obj.BucketClass, "purpose": obj.Purpose, "owner_module": obj.OwnerModule}})
	})
	if err != nil {
		s.logger.Warn("storage: could not record upload rejection; the expiry job will clean up",
			slog.String("object_id", obj.ID), slog.String("error", err.Error()))
	}
	s.metrics.uploads.WithLabelValues(obj.BucketClass, "rejected:"+reason).Inc()
	return cause
}

// failUpload handles a failed bucket write: best-effort removal of partial bytes, then UPLOADED → DELETED.
func (s *Service) failUpload(ctx context.Context, obj Object, ref pstorage.Ref) {
	fctx, cancel := finalizeCtx(ctx)
	defer cancel()
	purged := s.blobs.Delete(fctx, ref) == nil
	err := db.WithTx(fctx, s.pool, db.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		now := s.clock.Now().UTC()
		var purgedAt any
		if purged {
			purgedAt = now
		}
		if _, err := tx.Exec(ctx, `UPDATE app.stored_objects SET scan_status = 'DELETED', deleted_at = $2, delete_reason = 'UPLOAD_FAILED',
			  purged_at = $3 WHERE id = $1 AND scan_status = 'UPLOADED'`, obj.ID, now, purgedAt); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE app.upload_sessions SET status = 'ABORTED', finished_at = $2
			 WHERE stored_object_id = $1 AND status = 'PENDING'`, obj.ID, now)
		return err
	})
	if err != nil {
		s.logger.Warn("storage: could not record failed upload; the expiry job will clean up",
			slog.String("object_id", obj.ID), slog.String("error", err.Error()))
	}
}

// --- Get / Open / Delete -------------------------------------------------------------------------------

type row struct {
	Object
	keyID     *string
	promoted  *string
	deletedAt *time.Time
}

const selectObject = `SELECT id::text, bucket_class, purpose, owner_module, coalesce(uploaded_by_user_id::text, ''), scan_status,
	coalesce(sniffed_content_type, ''), coalesce(size_bytes, 0), content_sha256, created_at, scanned_at, coalesce(reject_reason, ''),
	encryption_key_id, promoted_key, deleted_at FROM app.stored_objects`

func scanRow(r pgx.Row) (row, error) {
	var o row
	err := r.Scan(&o.ID, &o.BucketClass, &o.Purpose, &o.OwnerModule, &o.UploadedBy, &o.Status, &o.SniffedType, &o.SizeBytes,
		&o.SHA256, &o.CreatedAt, &o.ScannedAt, &o.RejectReason, &o.keyID, &o.promoted, &o.deletedAt)
	return o, err
}

func (s *Service) load(ctx context.Context, q pgx.Tx, id string, lock bool) (row, error) {
	if !ids.Valid(id) {
		return row{}, ErrNotFound
	}
	sql := selectObject + ` WHERE id = $1`
	if lock {
		sql += ` FOR UPDATE`
	}
	var r pgx.Row
	if q != nil {
		r = q.QueryRow(ctx, sql, id)
	} else {
		r = s.pool.QueryRow(ctx, sql, id)
	}
	o, err := scanRow(r)
	if errors.Is(err, pgx.ErrNoRows) {
		return row{}, ErrNotFound
	}
	if err != nil {
		return row{}, errors.Join(ErrStorageUnavailable, err)
	}
	return o, nil
}

// Get returns an object's metadata (any status). Callers authorise access through the owning module.
func (s *Service) Get(ctx context.Context, id string) (Object, error) {
	o, err := s.load(ctx, nil, id, false)
	return o.Object, err
}

// Open streams a CLEAN object's bytes (ErrNotClean otherwise). The SHA-256 recorded at upload is
// re-verified while reading: a mismatch surfaces as a read error at EOF. The caller must Close the reader.
func (s *Service) Open(ctx context.Context, id string) (io.ReadCloser, Object, error) {
	o, err := s.load(ctx, nil, id, false)
	if err != nil {
		return nil, Object{}, err
	}
	if o.Status != StatusClean || o.promoted == nil {
		return nil, o.Object, ErrNotClean
	}
	if o.BucketClass != BucketPublicMedia && (o.keyID == nil || *o.keyID != s.keyID) {
		// encrypted under another key (rotation is not implemented) or recorded as unencrypted while a key is now set
		return nil, o.Object, errors.Join(ErrStorageUnavailable, errors.New("storage: object encryption key does not match the configured key"))
	}
	rc, err := s.blobs.GetSSE(ctx, pstorage.RefFor(platformClass(o.BucketClass), *o.promoted), s.sse(o.BucketClass, o.ID))
	if err != nil {
		return nil, o.Object, errors.Join(ErrStorageUnavailable, err)
	}
	return &verifyingReader{rc: rc, h: sha256.New(), want: o.SHA256}, o.Object, nil
}

// ErrIntegrity is returned by an Open reader whose content does not match the recorded SHA-256.
var ErrIntegrity = errors.New("storage: object content does not match its recorded SHA-256")

type verifyingReader struct {
	rc   io.ReadCloser
	h    hash.Hash
	want []byte
}

func (v *verifyingReader) Read(p []byte) (int, error) {
	n, err := v.rc.Read(p)
	v.h.Write(p[:n])
	if errors.Is(err, io.EOF) && !bytes.Equal(v.h.Sum(nil), v.want) {
		return n, ErrIntegrity
	}
	return n, err
}

func (v *verifyingReader) Close() error { return v.rc.Close() }

// Delete soft-deletes an object (→ DELETED, OWNER_DELETED; STAFF_DELETED is not distinguished here — the
// audit event's actor records who). The row and the bytes are kept for retention; a later retention job
// purges them (purged_at). Idempotent: deleting a DELETED object succeeds.
func (s *Service) Delete(ctx context.Context, id, actorID string) error {
	if actorID != "" && !ids.Valid(actorID) {
		return invalidInput("actor must be a UUID")
	}
	return db.WithTx(ctx, s.pool, db.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		o, err := s.load(ctx, tx, id, true)
		if err != nil {
			return err
		}
		if o.Status == StatusDeleted {
			return nil
		}
		now := s.clock.Now().UTC()
		if _, err := tx.Exec(ctx, `UPDATE app.stored_objects SET scan_status = 'DELETED', deleted_at = $2, deleted_by_user_id = $3,
			  delete_reason = 'OWNER_DELETED' WHERE id = $1`, id, now, nullable(actorID)); err != nil {
			return errors.Join(ErrStorageUnavailable, err)
		}
		if o.Status == StatusUploaded {
			if _, err := tx.Exec(ctx, `UPDATE app.upload_sessions SET status = 'ABORTED', finished_at = $2
				 WHERE stored_object_id = $1 AND status = 'PENDING'`, id, now); err != nil {
				return errors.Join(ErrStorageUnavailable, err)
			}
		}
		ev := audit.Event{Action: "storage.object.deleted", TargetType: "stored_object", TargetID: id, OccurredAt: now,
			Reason: DeleteOwner, Metadata: map[string]any{"bucket_class": o.BucketClass, "purpose": o.Purpose,
				"owner_module": o.OwnerModule, "previous_status": o.Status}}
		if actorID != "" && authz.PrincipalFrom(ctx) == nil {
			ev.ActorID = actorID
			ev.ActorType = "user"
		}
		return audit.Record(ctx, tx, ev)
	})
}

// IssueTicket issues a document access ticket bound to (object, user, session); ttl ≤ 0 uses
// DOCUMENT_TICKET_TTL (default 60 s).
func (s *Service) IssueTicket(objectID, userID, sessionID string, ttl time.Duration) (string, time.Time) {
	return s.tickets.Issue(objectID, userID, sessionID, ttl)
}

// VerifyTicket checks a ticket (ErrTicketExpired / ErrTicketInvalid).
func (s *Service) VerifyTicket(ticket, objectID, userID, sessionID string) error {
	return s.tickets.Verify(ticket, objectID, userID, sessionID)
}
