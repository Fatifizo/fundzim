package storage

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Fatifizo/fundzim/internal/audit"
	"github.com/Fatifizo/fundzim/internal/platform/db"
	"github.com/Fatifizo/fundzim/internal/platform/ids"
	"github.com/Fatifizo/fundzim/internal/platform/outbox"
	pstorage "github.com/Fatifizo/fundzim/internal/platform/storage"
)

// ScanOutcome is the decision for one scan attempt.
type ScanOutcome struct {
	Status    string // StatusClean | StatusRejected | StatusFailedScan
	Code      string // reject reason (REJECTED) or last_scan_error (FAILED_SCAN)
	Signature string
	Engine    string
}

// Decide maps a scanner result to an outcome. It is the single place that can produce CLEAN, and only for a
// nil error, an explicit Clean verdict and a named engine; every error is FAILED_SCAN.
func Decide(v Verdict, err error) ScanOutcome {
	switch {
	case err != nil:
		return ScanOutcome{Status: StatusFailedScan, Code: scanErrorCode(err)}
	case v.Engine == "":
		return ScanOutcome{Status: StatusFailedScan, Code: ScanErrScanner}
	case !v.Clean:
		return ScanOutcome{Status: StatusRejected, Code: RejectMalware, Signature: truncate(v.Signature, 255), Engine: truncate(v.Engine, 255)}
	default:
		return ScanOutcome{Status: StatusClean, Engine: truncate(v.Engine, 255)}
	}
}

type claim struct {
	bucket, purpose, owner string
	uploadedBy             *string
	attempt                int
	sha                    []byte
}

// ScanObject runs one scan attempt for id. It is idempotent and safe to call concurrently: the claim
// (QUARANTINED | due FAILED_SCAN → SCANNING) is one conditional UPDATE, and every later write is fenced by
// the attempt number, so at most one attempt can promote an object. Objects that are not claimable (already
// final, being scanned, not yet due, deleted) are left alone and nil is returned. A scanner or storage
// failure is recorded as FAILED_SCAN with next_scan_at (the rescan job retries) and also returns nil; only
// database errors are returned.
func (s *Service) ScanObject(ctx context.Context, id string) error {
	if s.scanner == nil {
		return errors.New("storage: no scanner configured")
	}
	if !ids.Valid(id) {
		return nil
	}
	now := s.clock.Now().UTC()
	var c claim
	err := s.pool.QueryRow(ctx, `
		UPDATE app.stored_objects SET scan_status = 'SCANNING', scan_attempts = scan_attempts + 1, scan_started_at = $2
		 WHERE id = $1 AND (scan_status = 'QUARANTINED' OR (scan_status = 'FAILED_SCAN' AND next_scan_at <= $2))
		RETURNING bucket_class, purpose, owner_module, uploaded_by_user_id, scan_attempts, content_sha256`, id, now).
		Scan(&c.bucket, &c.purpose, &c.owner, &c.uploadedBy, &c.attempt, &c.sha)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("storage: claim scan: %w", err)
	}
	start := time.Now()
	out := s.runScan(ctx, id, c)
	if out.Status == StatusClean {
		if perr := s.promote(ctx, id, c); perr != nil {
			s.logger.Warn("storage: promotion failed; will retry", slog.String("object_id", id), slog.String("error", perr.Error()))
			out = ScanOutcome{Status: StatusFailedScan, Code: ScanErrStorage}
		}
	}
	committed, err := s.recordOutcome(ctx, id, c, out)
	label := out.Status
	if out.Status == StatusFailedScan {
		label += ":" + out.Code
	}
	s.metrics.scans.WithLabelValues(c.bucket, label).Inc()
	s.metrics.scanDuration.WithLabelValues(out.Status).Observe(time.Since(start).Seconds())
	if err != nil {
		return err
	}
	if committed && out.Status == StatusClean {
		// The object is CLEAN in objects/; the quarantine copy is no longer needed.
		qref := pstorage.RefFor(platformClass(c.bucket), quarantineKey(id))
		if derr := s.blobs.Delete(context.WithoutCancel(ctx), qref); derr != nil {
			s.logger.Warn("storage: quarantine copy not deleted after promotion", slog.String("object_id", id),
				slog.String("error", derr.Error()))
		}
	}
	if !committed && out.Status == StatusClean {
		// Lost the fence (lease recovered, object deleted meanwhile): another attempt owns the object now.
		s.logger.Warn("storage: scan result discarded (attempt no longer current)", slog.String("object_id", id),
			slog.Int("attempt", c.attempt))
	}
	return nil
}

// runScan downloads the quarantine copy and scans it, re-verifying the SHA-256 recorded at upload.
func (s *Service) runScan(ctx context.Context, id string, c claim) ScanOutcome {
	sctx, cancel := context.WithTimeout(ctx, s.cfg.ScanTimeout)
	defer cancel()
	rc, err := s.blobs.GetSSE(sctx, pstorage.RefFor(platformClass(c.bucket), quarantineKey(id)), s.sse(c.bucket, id))
	if err != nil {
		return ScanOutcome{Status: StatusFailedScan, Code: ScanErrStorage}
	}
	defer rc.Close()
	h := sha256.New()
	v, err := s.scanner.Scan(sctx, io.TeeReader(rc, h))
	out := Decide(v, err)
	if out.Status == StatusClean && !sumEquals(h, c.sha) {
		// the scanner may stop reading early only on a detection; a clean verdict must have seen every byte
		return ScanOutcome{Status: StatusFailedScan, Code: ScanErrStorage}
	}
	return out
}

func sumEquals(h hash.Hash, want []byte) bool {
	got := h.Sum(nil)
	if len(got) != len(want) {
		return false
	}
	var d byte
	for i := range got {
		d |= got[i] ^ want[i]
	}
	return d == 0
}

// promote copies quarantine/<id> to objects/<id> (server side). Copying the same bytes twice is harmless,
// so a retried promotion is safe.
func (s *Service) promote(ctx context.Context, id string, c claim) error {
	class := platformClass(c.bucket)
	return s.blobs.CopySSE(ctx, pstorage.RefFor(class, quarantineKey(id)), pstorage.RefFor(class, promotedKey(id)), s.sse(c.bucket, id))
}

// recordOutcome writes the outcome fenced by (SCANNING, attempt). committed=false means the attempt lost
// its claim and nothing was written.
func (s *Service) recordOutcome(ctx context.Context, id string, c claim, out ScanOutcome) (committed bool, err error) {
	fctx, cancel := finalizeCtx(ctx)
	defer cancel()
	err = db.WithTx(fctx, s.pool, db.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		now := s.clock.Now().UTC()
		var sql string
		args := []any{id, c.attempt, now}
		switch out.Status {
		case StatusClean:
			sql = `UPDATE app.stored_objects SET scan_status = 'CLEAN', scanned_at = $3, scan_engine = $4, promoted_key = $5,
				promoted_at = $3, last_scan_error = NULL, next_scan_at = NULL`
			args = append(args, out.Engine, promotedKey(id))
		case StatusRejected:
			sql = `UPDATE app.stored_objects SET scan_status = 'REJECTED', scanned_at = $3, scan_engine = $4, reject_reason = $5,
				scan_signature = $6, last_scan_error = NULL, next_scan_at = NULL`
			args = append(args, out.Engine, out.Code, nullable(out.Signature))
		default:
			sql = `UPDATE app.stored_objects SET scan_status = 'FAILED_SCAN', last_scan_error = $4,
				next_scan_at = greatest($5::timestamptz, $3::timestamptz)`
			args = append(args, out.Code, now.Add(s.cfg.ScanBackoff.Delay(c.attempt)))
		}
		tag, err := tx.Exec(ctx, sql+` WHERE id = $1 AND scan_status = 'SCANNING' AND scan_attempts = $2`, args...)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return nil
		}
		committed = true
		md := map[string]any{"bucket_class": c.bucket, "purpose": c.purpose, "owner_module": c.owner,
			"status": out.Status, "attempt": c.attempt}
		if out.Engine != "" {
			md["engine"] = out.Engine
		}
		if out.Signature != "" {
			md["malware_name"] = out.Signature
		}
		outcome := "success"
		if out.Status == StatusFailedScan {
			outcome = "failed"
			md["error_code"] = out.Code
		}
		if err := audit.Record(ctx, tx, audit.Event{Action: "storage.object.scanned", ActorType: "system", TargetType: "stored_object",
			TargetID: id, Outcome: outcome, Reason: out.Code, OccurredAt: now, Metadata: md}); err != nil {
			return err
		}
		if out.Status == StatusFailedScan {
			return nil // not final: no event (consumers react to CLEAN / REJECTED)
		}
		_, err = outbox.Write(ctx, tx, outbox.Event{AggregateType: "stored_object", AggregateID: id, EventType: EventObjectScanned,
			OccurredAt: now, Payload: scannedPayload(id, out.Status, c)})
		return err
	})
	if err != nil {
		return false, fmt.Errorf("storage: record scan outcome: %w", err)
	}
	return committed, nil
}

// ConsumerScan is the outbox consumer name for storage.object_uploaded (stable: part of the dedupe key).
const ConsumerScan = "storage.scan_uploaded_object"

// HandleObjectUploaded is the outbox handler for storage.object_uploaded.
func (s *Service) HandleObjectUploaded(ctx context.Context, d outbox.Delivery) error {
	var p struct {
		ObjectID string `json:"object_id"`
	}
	if err := json.Unmarshal(d.Payload, &p); err != nil || !ids.Valid(p.ObjectID) {
		s.logger.Error("storage: malformed storage.object_uploaded payload", slog.String("event_id", d.EventID))
		return nil // retrying cannot fix it
	}
	return s.ScanObject(ctx, p.ObjectID)
}

// RegisterConsumers subscribes the scan consumer (worker process).
func (s *Service) RegisterConsumers(reg *outbox.Registry) {
	reg.Subscribe(ConsumerScan, EventObjectUploaded, s.HandleObjectUploaded)
}

// --- periodic maintenance ------------------------------------------------------------------------------

// SweepResult summarises one RescanDue run.
type SweepResult struct {
	Interrupted, Scanned, Exhausted int
}

// RescanDue (periodic) moves SCANNING objects whose lease expired to FAILED_SCAN (SCAN_INTERRUPTED), then
// scans due FAILED_SCAN objects that still have attempts left and QUARANTINED objects older than staleAfter
// (an event that was never delivered). At most limit objects are scanned per run.
func (s *Service) RescanDue(ctx context.Context, staleAfter time.Duration, limit int) (SweepResult, error) {
	var res SweepResult
	now := s.clock.Now().UTC()
	n, err := s.RecoverInterrupted(ctx)
	if err != nil {
		return res, err
	}
	res.Interrupted = n
	rows, err := s.pool.Query(ctx, `
		SELECT id::text FROM app.stored_objects
		 WHERE (scan_status = 'FAILED_SCAN' AND next_scan_at <= $1 AND scan_attempts < $2)
		    OR (scan_status = 'QUARANTINED' AND updated_at < $3)
		 ORDER BY coalesce(next_scan_at, created_at) LIMIT $4`, now, s.cfg.MaxScanAttempts, now.Add(-staleAfter), limit)
	if err != nil {
		return res, fmt.Errorf("storage: list due scans: %w", err)
	}
	due, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return res, err
	}
	for _, id := range due {
		if err := s.ScanObject(ctx, id); err != nil {
			return res, err
		}
		res.Scanned++
	}
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM app.stored_objects WHERE scan_status = 'FAILED_SCAN' AND scan_attempts >= $1`,
		s.cfg.MaxScanAttempts).Scan(&res.Exhausted); err == nil {
		s.metrics.exhausted.Set(float64(res.Exhausted))
	}
	return res, nil
}

// RecoverInterrupted moves SCANNING objects whose lease (2 × scan timeout + 1 min) expired — the worker died
// or lost its database connection mid-scan — to FAILED_SCAN (SCAN_INTERRUPTED, due now). A late result from
// the interrupted attempt is then discarded by the attempt fence.
func (s *Service) RecoverInterrupted(ctx context.Context) (int, error) {
	now := s.clock.Now().UTC()
	lease := 2*s.cfg.ScanTimeout + time.Minute
	tag, err := s.pool.Exec(ctx, `
		UPDATE app.stored_objects SET scan_status = 'FAILED_SCAN', last_scan_error = 'SCAN_INTERRUPTED', next_scan_at = $1
		 WHERE scan_status = 'SCANNING' AND scan_started_at < $2`, now, now.Add(-lease))
	if err != nil {
		return 0, fmt.Errorf("storage: recover interrupted scans: %w", err)
	}
	n := int(tag.RowsAffected())
	s.metrics.stuck.Add(float64(n))
	return n, nil
}

// ExpireUploads (periodic) ends PENDING upload sessions past expires_at: session EXPIRED, object UPLOADED →
// DELETED (UPLOAD_EXPIRED), partial bytes deleted (purged_at). Returns the number of uploads expired.
func (s *Service) ExpireUploads(ctx context.Context, limit int) (int, error) {
	now := s.clock.Now().UTC()
	rows, err := s.pool.Query(ctx, `
		SELECT u.stored_object_id::text, o.bucket_class FROM app.upload_sessions u
		  JOIN app.stored_objects o ON o.id = u.stored_object_id
		 WHERE u.status = 'PENDING' AND u.expires_at < $1 ORDER BY u.expires_at LIMIT $2`, now, limit)
	if err != nil {
		return 0, fmt.Errorf("storage: list expired uploads: %w", err)
	}
	type exp struct{ id, bucket string }
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (exp, error) {
		var e exp
		return e, r.Scan(&e.id, &e.bucket)
	})
	if err != nil {
		return 0, err
	}
	n := 0
	for _, e := range list {
		var expired bool
		err := db.WithTx(ctx, s.pool, db.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
			tag, err := tx.Exec(ctx, `UPDATE app.upload_sessions SET status = 'EXPIRED', finished_at = $2
				 WHERE stored_object_id = $1 AND status = 'PENDING' AND expires_at < $2`, e.id, now)
			if err != nil || tag.RowsAffected() != 1 {
				return err
			}
			expired = true
			if _, err := tx.Exec(ctx, `UPDATE app.stored_objects SET scan_status = 'DELETED', deleted_at = $2, delete_reason = 'UPLOAD_EXPIRED'
				 WHERE id = $1 AND scan_status = 'UPLOADED'`, e.id, now); err != nil {
				return err
			}
			return audit.Record(ctx, tx, audit.Event{Action: "storage.object.upload_expired", ActorType: "system",
				TargetType: "stored_object", TargetID: e.id, Reason: DeleteUploadExpired, OccurredAt: now,
				Metadata: map[string]any{"bucket_class": e.bucket}})
		})
		if err != nil {
			return n, fmt.Errorf("storage: expire upload: %w", err)
		}
		if !expired {
			continue
		}
		n++
		s.metrics.expired.Inc()
		// Remove any partial/unrecorded bytes. Deleting a missing key succeeds (S3 semantics).
		if err := s.blobs.Delete(ctx, pstorage.RefFor(platformClass(e.bucket), quarantineKey(e.id))); err != nil {
			s.logger.Warn("storage: partial upload not deleted; will not be retried automatically", slog.String("object_id", e.id),
				slog.String("error", err.Error()))
			continue
		}
		if _, err := s.pool.Exec(ctx, `UPDATE app.stored_objects SET purged_at = $2
			 WHERE id = $1 AND scan_status = 'DELETED' AND purged_at IS NULL`, e.id, s.clock.Now().UTC()); err != nil {
			return n, fmt.Errorf("storage: mark purged: %w", err)
		}
	}
	return n, nil
}

// scannedPayload is the storage.object_scanned payload (ids and codes only; uploaded_by lets risk attribute
// repeated rejections to an account).
func scannedPayload(id, status string, c claim) map[string]string {
	p := map[string]string{"object_id": id, "status": status, "owner_module": c.owner, "purpose": c.purpose}
	if c.uploadedBy != nil {
		p["uploaded_by"] = *c.uploadedBy
	}
	return p
}
