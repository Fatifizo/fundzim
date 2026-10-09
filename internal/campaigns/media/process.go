package media

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/Fatifizo/fundzim/internal/platform/ids"
	"github.com/Fatifizo/fundzim/internal/platform/jobs"
	"github.com/Fatifizo/fundzim/internal/platform/outbox"
	"github.com/Fatifizo/fundzim/internal/storage"
)

// ConsumerObjectScanned is the outbox consumer name for storage.object_scanned (stable: part of the dedupe key).
const ConsumerObjectScanned = "campaigns.media_object_scanned"

// Advance moves one media item forward from the state of its stored objects. It is idempotent and safe to call
// concurrently (every write happens under the media row lock and re-checks the state), so the outbox consumer and
// the periodic sweep can both call it. It never approves media unless the derivative object is CLEAN, and the
// derivative only exists after the original was CLEAN and decoded within the limits. Infrastructure failures
// (database, object storage) are returned so the caller retries; content failures reject the media.
func (s *Service) Advance(ctx context.Context, mediaID string) error {
	m, err := s.load(ctx, s.d.Pool, "", mediaID, false)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	switch m.Status {
	case StatusUploaded, StatusQuarantined, StatusScanning:
	default:
		return nil // final (APPROVED, REJECTED, REMOVED)
	}
	if m.processedObjectID == nil {
		orig, err := s.d.Storage.Get(ctx, m.storedObjectID)
		if err != nil {
			return err
		}
		switch orig.Status {
		case storage.StatusClean:
			return s.processOriginal(ctx, m.ID)
		case storage.StatusRejected:
			reason := ReasonUploadRejected
			if orig.RejectReason == storage.RejectMalware {
				reason = ReasonMalware
			}
			return s.finish(ctx, m.ID, nil, StatusRejected, reason)
		case storage.StatusDeleted:
			return s.finish(ctx, m.ID, nil, StatusRejected, ReasonObjectDeleted)
		case storage.StatusScanning, storage.StatusFailedScan:
			// the scan is running or the scanner failed and the storage rescan job is retrying: show it as SCANNING
			return s.markScanning(ctx, m.ID)
		}
		return nil // UPLOADED / QUARANTINED: waiting for the scan
	}
	der, err := s.d.Storage.Get(ctx, *m.processedObjectID)
	if err != nil {
		return err
	}
	switch der.Status {
	case storage.StatusClean:
		return s.finish(ctx, m.ID, m.processedObjectID, StatusApproved, "")
	case storage.StatusRejected:
		reason := ReasonDerivativeRejected
		if der.RejectReason == storage.RejectMalware {
			reason = ReasonMalware
		}
		return s.finish(ctx, m.ID, m.processedObjectID, StatusRejected, reason)
	case storage.StatusDeleted:
		return s.finish(ctx, m.ID, m.processedObjectID, StatusRejected, ReasonObjectDeleted)
	}
	return nil // derivative in quarantine or being (re)scanned
}

func (s *Service) markScanning(ctx context.Context, mediaID string) error {
	return s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		m, err := s.load(ctx, tx, "", mediaID, true)
		if err != nil || (m.Status != StatusUploaded && m.Status != StatusQuarantined) {
			return ignoreNotFound(err)
		}
		if m.Status == StatusUploaded {
			if err := s.apply(ctx, tx, &m, systemActor, step{to: StatusQuarantined, event: "QUARANTINED"}); err != nil {
				return err
			}
		}
		return s.apply(ctx, tx, &m, systemActor, step{to: StatusScanning, event: "SCAN_STARTED"})
	})
}

func ignoreNotFound(err error) error {
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	return err
}

// finish approves or rejects media whose processed object is still expectedDerivative (nil = not yet processed).
func (s *Service) finish(ctx context.Context, mediaID string, expectedDerivative *string, to, reason string) error {
	return s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		m, err := s.load(ctx, tx, "", mediaID, true)
		if err != nil {
			return ignoreNotFound(err)
		}
		if m.Status != StatusUploaded && m.Status != StatusQuarantined && m.Status != StatusScanning {
			return nil
		}
		if (expectedDerivative == nil) != (m.processedObjectID == nil) ||
			(expectedDerivative != nil && *expectedDerivative != *m.processedObjectID) {
			return nil // state moved on; the next Advance decides
		}
		return s.finishLocked(ctx, tx, &m, to, reason)
	})
}

func (s *Service) finishLocked(ctx context.Context, tx pgx.Tx, m *Media, to, reason string) error {
	from := m.Status
	if to == StatusApproved {
		if m.Status != StatusScanning {
			return fmt.Errorf("campaign media %s: approval from %s", m.ID, m.Status) // unreachable: derivatives exist only in SCANNING
		}
		if err := s.apply(ctx, tx, m, systemActor, step{to: StatusApproved, event: "APPROVED",
			set: map[string]any{"approved_at": s.now()}}); err != nil {
			return err
		}
		return s.audit(ctx, tx, systemActor, AuditMediaApproved, *m, from, "", nil)
	}
	if err := s.apply(ctx, tx, m, systemActor, step{to: StatusRejected, event: "REJECTED", reason: reason,
		set: map[string]any{"rejected_reason": reason}}); err != nil {
		return err
	}
	return s.audit(ctx, tx, systemActor, AuditMediaRejected, *m, from, reason, nil)
}

// processOriginal decodes and re-encodes a CLEAN original and stores the derivative in quarantine. It holds the
// media row lock for the whole step, so concurrent deliveries create at most one derivative.
func (s *Service) processOriginal(ctx context.Context, mediaID string) error {
	var orphan string
	err := s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		m, err := s.load(ctx, tx, "", mediaID, true)
		if err != nil {
			return ignoreNotFound(err)
		}
		if (m.Status != StatusUploaded && m.Status != StatusQuarantined && m.Status != StatusScanning) || m.processedObjectID != nil {
			return nil
		}
		rc, obj, err := s.d.Storage.Open(ctx, m.storedObjectID)
		if err != nil {
			return fmt.Errorf("campaign media: open original: %w", err)
		}
		content, rerr := io.ReadAll(io.LimitReader(rc, obj.SizeBytes+1))
		_ = rc.Close()
		if rerr != nil {
			return fmt.Errorf("campaign media: read original: %w", rerr) // includes an integrity (SHA-256) mismatch
		}
		p, perr := s.process(ctx, content, obj.SniffedType)
		if perr != nil {
			if code := RejectCode(perr); code != "" {
				s.d.Logger.Info("campaign media rejected", slog.String("media_id", m.ID), slog.String("reason", code))
				return s.finishLocked(ctx, tx, &m, StatusRejected, code)
			}
			return perr
		}
		der, uerr := s.d.Storage.Upload(ctx, storage.UploadInput{BucketClass: storage.BucketPublicMedia, Purpose: PurposeCampaignMedia,
			OwnerModule: OwnerModule, UploaderUserID: m.createdBy, DeclaredContentType: p.ContentType, Body: bytes.NewReader(p.Data),
			MaxBytes: s.d.MaxUploadBytes})
		if uerr != nil {
			if storage.IsUploadRejection(uerr) {
				reason := ReasonDerivativeRejected
				if errors.Is(uerr, storage.ErrTooLarge) {
					reason = ReasonDerivativeTooLarge
				}
				return s.finishLocked(ctx, tx, &m, StatusRejected, reason)
			}
			return fmt.Errorf("campaign media: store derivative: %w", uerr)
		}
		orphan = der.ID
		if m.Status == StatusUploaded {
			if err := s.apply(ctx, tx, &m, systemActor, step{to: StatusQuarantined, event: "QUARANTINED"}); err != nil {
				return err
			}
		}
		if err := s.apply(ctx, tx, &m, systemActor, step{to: StatusScanning, event: "PROCESSED",
			set:     map[string]any{"processed_object_id": der.ID, "content_type": p.ContentType, "width": p.Width, "height": p.Height},
			payload: map[string]any{"derivative_object_id": der.ID, "width": p.Width, "height": p.Height}}); err != nil {
			return err
		}
		orphan = ""
		return nil
	})
	if err != nil && orphan != "" {
		// the derivative was stored but not linked: soft-delete it so it can never be served
		if derr := s.d.Storage.Delete(context.WithoutCancel(ctx), orphan, ""); derr != nil {
			s.d.Logger.Warn("campaign media: unlinked derivative not deleted", slog.String("object_id", orphan),
				slog.String("error", derr.Error()))
		}
	}
	return err
}

// process runs Process with at most one decode at a time per process (bounded memory).
func (s *Service) process(ctx context.Context, content []byte, contentType string) (Processed, error) {
	select {
	case s.decode <- struct{}{}:
		defer func() { <-s.decode }()
	case <-ctx.Done():
		return Processed{}, ctx.Err()
	}
	return Process(content, contentType, s.d.Limits, s.d.MaxUploadBytes)
}

// HandleObjectScanned is the outbox handler for storage.object_scanned: it advances the campaign media that uses the
// object (as original or derivative). Errors are returned so the delivery is retried.
func (s *Service) HandleObjectScanned(ctx context.Context, d outbox.Delivery) error {
	var p struct {
		ObjectID    string `json:"object_id"`
		OwnerModule string `json:"owner_module"`
		Purpose     string `json:"purpose"`
	}
	if err := json.Unmarshal(d.Payload, &p); err != nil || !ids.Valid(p.ObjectID) {
		s.d.Logger.Error("campaign media: malformed storage.object_scanned payload", slog.String("event_id", d.EventID))
		return nil
	}
	if p.OwnerModule != OwnerModule || p.Purpose != PurposeCampaignMedia {
		return nil
	}
	rows, err := s.d.Pool.Query(ctx, `SELECT id::text FROM app.campaign_media WHERE stored_object_id = $1 OR processed_object_id = $1`,
		p.ObjectID)
	if err != nil {
		return err
	}
	list, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return err
	}
	for _, id := range list {
		if err := s.Advance(ctx, id); err != nil {
			return err
		}
	}
	return nil
}

// Consumer is an outbox subscription for the composition root.
type Consumer struct {
	Name, EventType string
	Handle          outbox.Handler
}

// Consumers lists the media outbox consumers (worker).
func (s *Service) Consumers() []Consumer {
	return []Consumer{{Name: ConsumerObjectScanned, EventType: storage.EventObjectScanned, Handle: s.HandleObjectScanned}}
}

// Sweep advances media still in progress (missed or exhausted deliveries, interrupted processing). It returns the
// number of items examined.
func (s *Service) Sweep(ctx context.Context, limit int) (int, error) {
	rows, err := s.d.Pool.Query(ctx, `SELECT id::text FROM app.campaign_media WHERE status IN ('UPLOADED', 'QUARANTINED', 'SCANNING')
		ORDER BY updated_at LIMIT $1`, limit)
	if err != nil {
		return 0, err
	}
	list, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return 0, err
	}
	var firstErr error
	for _, id := range list {
		if err := s.Advance(ctx, id); err != nil {
			s.d.Logger.Warn("campaign media sweep: advance failed; will retry", slog.String("media_id", id), slog.String("error", err.Error()))
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	return len(list), firstErr
}

// KindSweep is the periodic sweep job kind (stable once shipped).
const KindSweep = "campaigns.media_sweep"

// SweepArgs is the periodic media sweep.
type SweepArgs struct{}

// Kind implements river.JobArgs.
func (SweepArgs) Kind() string { return KindSweep }

// InsertOpts implements river.JobArgsWithInsertOpts.
func (SweepArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: jobs.QueueMaintenance, MaxAttempts: 3, UniqueOpts: river.UniqueOpts{ByPeriod: 30 * time.Second}}
}

// SweepWorker runs Service.Sweep.
type SweepWorker struct {
	river.WorkerDefaults[SweepArgs]
	Service *Service
}

// Timeout implements river.Worker.
func (w *SweepWorker) Timeout(*river.Job[SweepArgs]) time.Duration { return 5 * time.Minute }

// Work implements river.Worker.
func (w *SweepWorker) Work(ctx context.Context, _ *river.Job[SweepArgs]) error {
	_, err := w.Service.Sweep(ctx, 50)
	return err
}

// AddWorkers registers the media job workers.
func (s *Service) AddWorkers(workers *river.Workers) {
	river.AddWorker(workers, &SweepWorker{Service: s})
}

// PeriodicJobs returns the media periodic jobs (sweep every 30 s).
func PeriodicJobs() []*river.PeriodicJob {
	return []*river.PeriodicJob{river.NewPeriodicJob(river.PeriodicInterval(30*time.Second),
		func() (river.JobArgs, *river.InsertOpts) { return SweepArgs{}, nil },
		&river.PeriodicJobOpts{ID: KindSweep})}
}
