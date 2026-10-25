package payouts

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"github.com/Fatifizo/fundzim/internal/audit"
	"github.com/Fatifizo/fundzim/internal/platform/errs"
	"github.com/Fatifizo/fundzim/internal/platform/httpx"
	"github.com/Fatifizo/fundzim/internal/platform/ids"
	"github.com/Fatifizo/fundzim/internal/platform/reconsweep"
	"github.com/Fatifizo/fundzim/internal/psp"
)

// Payout reconciliation foundation (brief §39; ADR-043 §8). A run compares, for every payout past the grace
// period: the request and its reservation, the ledger journals its state requires (by deterministic key and
// amount), and — for submitted payouts — the provider's own status for our reference. Each mismatch is an
// immutable app.payout_reconciliation_references row and opens (at most one unresolved per payout) a
// RECONCILIATION_MISMATCH exception. Settlement of disbursements against provider statements is NOT_PERFORMED:
// no statement source exists (PCR-007, PCR-016; Stage 17).
//
// Stage 19 (KI-S18-25, ADR-051): the ledger follows the payout's committed state in separate transactions
// (syncLedger: the journal on the ledger pool, then the reservation link), so a payout can be COMPLETED with its
// reservation not yet CONSUMED. The run's list only proposes candidates: each payout is re-read, and when its state
// requires a journal or reservation transition that is not linked yet, reconciliation first runs the same idempotent
// recovery as the Recover job and then evaluates the state as it is. A transition still in flight therefore settles
// (or is waited for at its row lock) instead of being reported; a payout the ledger cannot settle is reported with
// the ledger's refusal (detail ledger_sync = FAILED, error_code). Nothing is classified as transient by assumption.

// DefaultReconGrace skips payouts changed more recently than this (their journals may still be posting).
const DefaultReconGrace = 5 * time.Minute

// Remediation of KI-S19-09 (ADR-052). A run has two passes. The recency pass re-checks the newest DefaultReconWindow
// payouts (fast detection after recent transitions; it guarantees nothing about older payouts). The coverage sweep
// (internal/platform/reconsweep) walks every payout past PAYOUT_REQUESTED in id order, a bounded batch per run, with
// a checkpoint in app.payout_reconciliation_sweeps advanced only after its batch was examined and recorded; a cycle
// re-examines every payout in its current state, so a payout that becomes inconsistent after leaving the recency
// window (e.g. a later provider-side change) is found by the next cycle. A payout inside the grace period is carried
// as a deferral, never passed. The sweep's limits are engineering defaults, not capacity targets (D-05, D-06).
const (
	DefaultReconWindow = 5000
	SweepDetector      = "PAYOUTS"
	sweepTable         = "app.payout_reconciliation_sweeps"
)

// DefaultReconSweep bounds one run of the coverage sweep (each submitted payout costs a provider status query).
var DefaultReconSweep = reconsweep.Config{Scope: reconsweep.DefaultScope, BatchSize: 200, MaxRecords: 2000, MaxDuration: 3 * time.Minute,
	MaxDeferred: 10000}

// ReconReport summarises a run.
type ReconReport struct {
	RunID         string         `json:"run_id"`
	Checked       int            `json:"checked"` // recency pass
	NewMismatches int            `json:"new_mismatches"`
	ByType        map[string]int `json:"by_type"`
	// OpenExceptionEvidence counts mismatch references added under an exception that was already open (a changed
	// discrepancy: a mismatch type not referenced since the exception opened).
	OpenExceptionEvidence    int                 `json:"open_exception_evidence"`
	Sweep                    reconsweep.Report   `json:"sweep"`
	Coverage                 reconsweep.Coverage `json:"coverage"`
	SettlementReconciliation string              `json:"settlement_reconciliation"`
}

type mismatch struct {
	typ              string
	expected, actual *int64
	providerRef      string
	ledgerTx         string
	detail           map[string]any
}

// Reconcile runs the payout reconciliation (job every 15 minutes; staff payout.operate).
func (s *Service) Reconcile(ctx context.Context, triggeredBy string) (ReconReport, error) {
	rep := ReconReport{RunID: "payout-recon:" + ids.New(), ByType: map[string]int{},
		SettlementReconciliation: "NOT_PERFORMED: no provider disbursement statements (PCR-007, PCR-016; Stage 17)"}
	window := s.ReconWindow
	if window <= 0 {
		window = DefaultReconWindow
	}
	rows, err := s.Pool.Query(ctx, `SELECT `+payoutCols+payoutFrom+` WHERE p.status <> 'PAYOUT_REQUESTED' AND p.updated_at < $1
		ORDER BY p.created_at DESC LIMIT $2`, s.now().Add(-s.reconGrace()), window)
	if err != nil {
		return rep, err
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Payout, error) { return scanPayout(r) })
	if err != nil {
		return rep, err
	}
	cutoff := s.now().Add(-s.reconGrace())
	for _, cand := range list {
		p, ms, err := s.reconcileFresh(ctx, cand.ID, cutoff)
		if err != nil {
			return rep, err
		}
		if p == nil {
			continue // changed since the run started: the sweep carries it as a deferral when it reaches it
		}
		rep.Checked++
		if _, err := s.recordMismatches(ctx, p, ms, &rep); err != nil {
			return rep, err
		}
	}
	// coverage sweep: every payout, a bounded batch per run, resumable
	store := reconsweep.SQLStore{Pool: s.Pool, Table: sweepTable}
	cfg := s.ReconSweep.Or(DefaultReconSweep)
	rep.Sweep, err = reconsweep.Run(ctx, store, reconsweep.Options{Config: cfg, Detector: SweepDetector, RunID: rep.RunID, Now: s.now},
		s.sweepNext, func(ctx context.Context, id string) (reconsweep.Outcome, error) {
			p, ms, err := s.reconcileFresh(ctx, id, s.now().Add(-s.reconGrace()))
			switch {
			case err != nil:
				return reconsweep.Outcome{}, err
			case p == nil:
				return reconsweep.Outcome{Deferred: true}, nil
			case p.Status == StRequested:
				return reconsweep.Outcome{OutOfScope: true}, nil
			}
			n, err := s.recordMismatches(ctx, p, ms, &rep)
			return reconsweep.Outcome{Exceptions: n}, err
		})
	// the run's evidence is recorded even when the sweep stopped on an error (non-cancelled context)
	actx := context.WithoutCancel(ctx)
	rep.Coverage, _ = store.Coverage(actx, SweepDetector, cfg.Scope)
	aerr := s.tx(actx, func(ctx context.Context, tx pgx.Tx) error {
		return audit.Record(ctx, tx, audit.Event{Action: "payout.reconciliation.run", ActorType: "system", TargetType: "payout_reconciliation",
			Metadata: map[string]any{"run": rep.RunID, "checked": rep.Checked, "new_mismatches": rep.NewMismatches, "triggered_by": triggeredBy,
				"open_exception_evidence": rep.OpenExceptionEvidence, "settlement": "NOT_PERFORMED", "sweep": rep.Sweep,
				"coverage_watermark": rep.Coverage.LastCompletedCycleStartedAt}})
	})
	if err != nil {
		return rep, err
	}
	return rep, aerr
}

// sweepNext lists the next payouts of the coverage scope in id order.
func (s *Service) sweepNext(ctx context.Context, after string, limit int) ([]string, error) {
	rows, err := s.Pool.Query(ctx, `SELECT p.id::text FROM app.payout_requests p WHERE p.id > $1::uuid AND p.status <> 'PAYOUT_REQUESTED'
		ORDER BY p.id LIMIT $2`, reconsweep.After(after), limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

// recordMismatches records one payout's mismatches: immutable references plus (at most one unresolved per payout) a
// RECONCILIATION_MISMATCH exception. When an exception is already open, a repeated mismatch is not recorded again,
// but a mismatch type not referenced since the exception opened is (a changed discrepancy stays visible); no second
// exception is opened. Returns the number of references inserted.
func (s *Service) recordMismatches(ctx context.Context, p *Payout, ms []mismatch, rep *ReconReport) (int, error) {
	if len(ms) == 0 {
		return 0, nil
	}
	var inserted []string
	underOpen := false
	err := s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		inserted, underOpen = nil, false
		// one recorder per payout at a time: concurrent runs (job and staff) never duplicate its references
		if _, err := tx.Exec(ctx, `SELECT 1 FROM app.payout_requests WHERE id = $1 FOR UPDATE`, p.ID); err != nil {
			return err
		}
		var since *time.Time
		if err := tx.QueryRow(ctx, `SELECT coalesce((SELECT r.detected_at FROM app.payout_reconciliation_references r
			WHERE r.id = e.reconciliation_reference_id), e.detected_at) FROM app.payout_exceptions e WHERE e.payout_request_id = $1
			AND e.exception_type = 'RECONCILIATION_MISMATCH' AND e.status <> 'RESOLVED'`, p.ID).Scan(&since); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		var first string
		types := []string{}
		for _, m := range ms {
			if since != nil {
				var seen bool
				if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM app.payout_reconciliation_references WHERE payout_request_id = $1
					AND mismatch_type = $2 AND detected_at >= $3)`, p.ID, m.typ, *since).Scan(&seen); err != nil {
					return err
				}
				if seen {
					continue // already under investigation: a rerun does not duplicate
				}
			}
			id := ids.New()
			d, _ := json.Marshal(nonNil(m.detail))
			if _, err := tx.Exec(ctx, `INSERT INTO app.payout_reconciliation_references (id, payout_request_id, mismatch_type, expected_amount_minor,
				actual_amount_minor, currency, provider_reference, ledger_transaction_id, detail, detected_at, detected_by_run)
				VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`, id, p.ID, m.typ, m.expected, m.actual, p.Currency, nilIfEmpty(m.providerRef),
				nilIfEmpty(m.ledgerTx), d, s.now(), rep.RunID); err != nil {
				return err
			}
			if first == "" {
				first = id
			}
			types = append(types, m.typ)
		}
		inserted, underOpen = types, since != nil
		if len(types) == 0 || since != nil {
			return nil // nothing new, or evidence added to the exception already open
		}
		sev := "SEV2"
		for _, t := range types {
			if t == "LEDGER_MISMATCH" || t == "AMOUNT_MISMATCH" || t == "DUPLICATE_PAYOUT_REFERENCE" || t == "PROVIDER_STATUS_MISMATCH" {
				sev = "SEV1"
			}
		}
		if err := s.insertEvent(ctx, tx, p.ID, event{Type: "RECONCILIATION_REQUIRED", Source: "SYSTEM", Reason: strings.Join(types, ",")}); err != nil {
			return err
		}
		return s.openException(ctx, tx, "RECONCILIATION_MISMATCH", sev, "RECONCILIATION", p, "", map[string]any{"mismatch_types": types,
			"run": rep.RunID}, first)
	})
	if err != nil {
		return 0, err
	}
	rep.NewMismatches += len(inserted)
	for _, t := range inserted {
		rep.ByType[t]++
	}
	if underOpen {
		rep.OpenExceptionEvidence += len(inserted)
	}
	return len(inserted), nil
}

func (s *Service) reconGrace() time.Duration {
	if s.ReconGrace > 0 {
		return s.ReconGrace
	}
	return DefaultReconGrace
}

func nonNil(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}

// reconcileFresh re-reads one candidate (nil when it changed after cutoff), lets the ledger catch up with its state
// when a required link is missing (syncLedger, the Recover job's path; idempotent keys, links only where NULL), and
// lists the mismatches of the state as it is afterwards. A recovery that fails is part of the evidence.
func (s *Service) reconcileFresh(ctx context.Context, id string, cutoff time.Time) (*Payout, []mismatch, error) {
	p, err := s.payout(ctx, s.Pool, id, false)
	if err != nil {
		return nil, nil, err
	}
	if !p.UpdatedAt.Before(cutoff) {
		return nil, nil, nil
	}
	syncCode := ""
	if ledgerSyncPending(p) {
		if serr := s.syncLedger(ctx, id); serr != nil {
			if ctx.Err() != nil {
				return nil, nil, serr
			}
			syncCode = "LEDGER_ERROR"
			if e := errs.As(serr); e != nil {
				syncCode = e.Code
			}
		}
		if p, err = s.payout(ctx, s.Pool, id, false); err != nil {
			return nil, nil, err
		}
	}
	ms, err := s.reconcileOne(ctx, p)
	if err != nil {
		return nil, nil, err
	}
	if syncCode != "" {
		for i := range ms {
			if ms[i].typ == "LEDGER_MISMATCH" || ms[i].typ == "RESERVATION_MISMATCH" {
				ms[i].detail = nonNil(ms[i].detail)
				ms[i].detail["ledger_sync"], ms[i].detail["error_code"] = "FAILED", syncCode
			}
		}
	}
	return &p, ms, nil
}

// ledgerSyncPending: the payout's state requires a journal (and reservation transition) syncLedger has not linked yet
// — the same condition the Recover job selects.
func ledgerSyncPending(p Payout) bool {
	r := p.Reservation
	if r == nil {
		return false
	}
	switch {
	case p.SubmittedAt != nil && r.LedgerSubmitTxnID == nil:
		return true
	case (p.Status == StCompleted || p.Status == StReversed) && r.LedgerConsumeTxnID == nil:
		return true
	case (p.Status == StFailed || p.Status == StRejected || p.Status == StCancelled) && r.LedgerReleaseTxnID == nil:
		return true
	}
	return false
}

// reconcileOne lists the mismatches of one payout.
func (s *Service) reconcileOne(ctx context.Context, p Payout) ([]mismatch, error) {
	var out []mismatch
	amt := p.AmountMinor
	r := p.Reservation
	// reservation consistent with the payout's state
	wantRes := map[string][]string{StReview: {ResActive}, StApproved: {ResActive}, StSubmitted: {ResActive}, StProcessing: {ResActive},
		StUnknown: {ResBlocked, ResActive}, StCompleted: {ResConsumed}, StReversed: {ResConsumed}, StFailed: {ResReleased},
		StCancelled: {ResReleased, ResExpired}, StRejected: {ResReleased, ResExpired}}
	if want, ok := wantRes[p.Status]; ok {
		switch {
		case r == nil && !(p.Status == StRejected || p.Status == StCancelled):
			out = append(out, mismatch{typ: "RESERVATION_MISMATCH", detail: map[string]any{"reason": "RESERVATION_MISSING", "status": p.Status}})
		case r != nil && !contains(want, r.Status):
			out = append(out, mismatch{typ: "RESERVATION_MISMATCH", detail: map[string]any{"payout_status": p.Status, "reservation_status": r.Status}})
		}
	}
	// ledger journals the state requires (deterministic keys) with the payout's amount
	need := []string{}
	if r != nil {
		need = append(need, "reserved")
		if p.SubmittedAt != nil {
			need = append(need, "submitted")
		}
		switch p.Status {
		case StCompleted, StReversed:
			need = append(need, "completed")
		case StFailed:
			if p.SubmittedAt != nil {
				need = append(need, "failed")
			}
		case StRejected, StCancelled:
			need = append(need, "released")
		}
	}
	for _, step := range need {
		j, ok, err := s.Ledger.JournalByKey(ctx, "payout:"+p.ID+":"+step)
		if err != nil {
			return nil, err
		}
		switch {
		case !ok:
			out = append(out, mismatch{typ: "LEDGER_MISMATCH", expected: &amt, detail: map[string]any{"missing_journal": step}})
		case j.DebitMinor != amt || j.Currency != p.Currency:
			a := j.DebitMinor
			out = append(out, mismatch{typ: "LEDGER_MISMATCH", expected: &amt, actual: &a, ledgerTx: j.TransactionID, detail: map[string]any{"journal": step}})
		}
	}
	// unknown outcome and missing confirmation
	if p.Status == StUnknown && p.UnknownSince != nil && s.now().Sub(*p.UnknownSince) > unknownHorizon {
		out = append(out, mismatch{typ: "UNKNOWN_OUTCOME", detail: map[string]any{"unknown_since": p.UnknownSince}})
	}
	if (p.Status == StSubmitted || p.Status == StProcessing) && p.SubmittedAt != nil && s.now().Sub(*p.SubmittedAt) > time.Hour {
		out = append(out, mismatch{typ: "MISSING_PROVIDER_CONFIRMATION", detail: map[string]any{"submitted_at": p.SubmittedAt}})
	}
	// more than one accepted CREATE_PAYOUT, or different provider references for one payout
	var accepted, refs int
	if err := s.Pool.QueryRow(ctx, `SELECT count(*) FILTER (WHERE operation = 'CREATE_PAYOUT' AND classification = 'ACCEPTED'),
		count(DISTINCT provider_reference_returned) FROM app.payout_attempts WHERE payout_request_id = $1`, p.ID).Scan(&accepted, &refs); err != nil {
		return nil, err
	}
	if accepted > 1 || refs > 1 {
		out = append(out, mismatch{typ: "DUPLICATE_PAYOUT_REFERENCE", detail: map[string]any{"accepted_creates": accepted, "distinct_refs": refs}})
	}
	// the provider's own view of our reference (status API), for payouts that reached it
	if p.SubmittedAt != nil && s.ExecEnv == ExecLocalSimulator {
		pp, meta, err := s.adapterFor(ctx, p)
		if err == nil {
			o := pp.GetPayout(ctx, meta, p.ID)
			if o.Class == psp.ClassAccepted {
				prov := o.Canonical
				ref := o.ProviderRef
				if o.ReportedAmount != nil && *o.ReportedAmount != amt {
					a := *o.ReportedAmount
					out = append(out, mismatch{typ: "AMOUNT_MISMATCH", expected: &amt, actual: &a, providerRef: ref})
				}
				if o.ReportedCcy != "" && o.ReportedCcy != p.Currency {
					out = append(out, mismatch{typ: "CURRENCY_MISMATCH", providerRef: ref, detail: map[string]any{"provider_currency": o.ReportedCcy}})
				}
				ours := map[string]string{StCompleted: psp.PayoutCompleted, StReversed: psp.PayoutReturned, StFailed: psp.PayoutFailed}[p.Status]
				if ours != "" && prov != ours && !(p.Status == StCompleted && prov == psp.PayoutReturned) {
					out = append(out, mismatch{typ: "PROVIDER_STATUS_MISMATCH", providerRef: ref, detail: map[string]any{"ours": p.Status, "provider": prov}})
				}
			} else if o.Class == psp.ClassNotFound && (p.Status == StCompleted || p.Status == StReversed) {
				out = append(out, mismatch{typ: "PROVIDER_STATUS_MISMATCH", detail: map[string]any{"ours": p.Status, "provider": "NOT_FOUND"}})
			}
		}
	}
	return out, nil
}

func contains(xs []string, v string) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}

// ---- exceptions -------------------------------------------------------------------------------------------------

// Exception is an operations exception.
type Exception struct {
	ID              string           `json:"id"`
	PayoutRequestID *string          `json:"payout_request_id"`
	CampaignID      *string          `json:"campaign_id"`
	Type            string           `json:"exception_type"`
	Severity        string           `json:"severity"`
	Source          string           `json:"source"`
	Status          string           `json:"status"`
	OwnerID         *string          `json:"owner_id"`
	ReconRefID      *string          `json:"reconciliation_reference_id"`
	Evidence        json.RawMessage  `json:"evidence"`
	ResolutionCode  *string          `json:"resolution_code"`
	DetectedAt      time.Time        `json:"detected_at"`
	ResolvedAt      *time.Time       `json:"resolved_at"`
	AgeSeconds      int64            `json:"age_seconds"`
	History         []map[string]any `json:"history,omitempty"`
}

// ExceptionFilter filters the exception queue.
type ExceptionFilter struct {
	Status, Severity, PayoutID string
	All                        bool // include resolved
}

// Exceptions lists exceptions (queue order: open first, severity, oldest).
func (s *Service) Exceptions(ctx context.Context, f ExceptionFilter) ([]Exception, error) {
	rows, err := s.Pool.Query(ctx, `SELECT e.id, e.payout_request_id, e.campaign_id, e.exception_type, e.severity, e.source, e.status, e.owner_id,
		e.reconciliation_reference_id, e.evidence, e.resolution_code, e.detected_at, e.resolved_at,
		coalesce((SELECT jsonb_agg(jsonb_build_object('from', h.from_status, 'to', h.to_status, 'actor_type', h.actor_type, 'actor_id', h.actor_id,
		   'note', h.note, 'at', h.recorded_at) ORDER BY h.recorded_at) FROM app.payout_exception_events h WHERE h.payout_exception_id = e.id), '[]')
		FROM app.payout_exceptions e
		WHERE ($1 = '' OR e.status = $1) AND ($2 = '' OR e.severity = $2) AND ($3 = '' OR e.payout_request_id = nullif($3, '')::uuid)
		  AND ($4 OR e.status <> 'RESOLVED')
		ORDER BY (e.status = 'RESOLVED'), e.severity, e.detected_at LIMIT 500`, f.Status, f.Severity, f.PayoutID, f.All || f.Status == "RESOLVED")
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Exception, error) {
		var e Exception
		var hist []byte
		err := r.Scan(&e.ID, &e.PayoutRequestID, &e.CampaignID, &e.Type, &e.Severity, &e.Source, &e.Status, &e.OwnerID, &e.ReconRefID, &e.Evidence,
			&e.ResolutionCode, &e.DetectedAt, &e.ResolvedAt, &hist)
		_ = json.Unmarshal(hist, &e.History)
		e.AgeSeconds = int64(s.now().Sub(e.DetectedAt).Seconds())
		return e, err
	})
}

var (
	ErrExceptionNotFound = errs.New(errs.NotFound, "PAYOUT_EXCEPTION_NOT_FOUND", "No such payout exception.")
	ErrExceptionState    = errs.New(errs.Conflict, "PAYOUT_EXCEPTION_STATE", "This action is not allowed in the exception's current status.")
)

// resolution codes (no money moves on resolution: corrections are separate, maker-checker ledger adjustments)
var resolutionCodes = map[string]bool{"CONFIRMED_NO_ACTION": true, "CORRECTED_BY_ADJUSTMENT": true, "PROVIDER_CONFIRMED": true,
	"DUPLICATE_DETECTION": true, "ESCALATED_INCIDENT": true, "OTHER": true}

// InvestigateException assigns the exception to the staff member (OPEN → INVESTIGATING).
func (s *Service) InvestigateException(ctx context.Context, staffID, id, note string) error {
	return s.exceptionStep(ctx, staffID, id, "INVESTIGATING", "", note)
}

// ResolveException resolves it with a code and a note (OPEN/INVESTIGATING → RESOLVED). It never posts or moves a
// payout: the payout's state changes only through its own flow.
func (s *Service) ResolveException(ctx context.Context, staffID, id, code, note string) error {
	if !resolutionCodes[code] {
		return httpx.Validation(errs.Detail{Field: "resolution_code", Code: "INVALID_VALUE"})
	}
	return s.exceptionStep(ctx, staffID, id, "RESOLVED", code, note)
}

func (s *Service) exceptionStep(ctx context.Context, staffID, id, to, code, note string) error {
	note = strings.TrimSpace(note)
	if n := utf8.RuneCountInString(note); n < 5 || n > 2000 {
		return httpx.Validation(errs.Detail{Field: "note", Code: "INVALID_LENGTH"})
	}
	if !ids.Valid(id) {
		return ErrExceptionNotFound
	}
	return s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var from string
		if err := tx.QueryRow(ctx, `SELECT status FROM app.payout_exceptions WHERE id = $1 FOR UPDATE`, id).Scan(&from); err != nil {
			if err == pgx.ErrNoRows {
				return ErrExceptionNotFound
			}
			return err
		}
		if from == "RESOLVED" || from == to {
			return ErrExceptionState
		}
		var err error
		if to == "INVESTIGATING" {
			_, err = tx.Exec(ctx, `UPDATE app.payout_exceptions SET status = 'INVESTIGATING', owner_id = $2, version = version + 1 WHERE id = $1`, id, staffID)
		} else {
			_, err = tx.Exec(ctx, `UPDATE app.payout_exceptions SET status = 'RESOLVED', owner_id = coalesce(owner_id, $2), resolution_code = $3,
				resolved_at = $4, version = version + 1 WHERE id = $1`, id, staffID, code, s.now())
		}
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO app.payout_exception_events (id, payout_exception_id, from_status, to_status, actor_type, actor_id, note)
			VALUES ($1, $2, $3, $4, 'STAFF', $5, $6)`, ids.New(), id, from, to, staffID, note); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{Action: "payout.exception." + strings.ToLower(to), ActorType: "staff", ActorID: staffID,
			TargetType: "payout_exception", TargetID: id, Reason: note, Metadata: map[string]any{"resolution_code": code}})
	})
}

// ReconReferences lists the recorded mismatches (latest first).
func (s *Service) ReconReferences(ctx context.Context, limit int) ([]map[string]any, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	rows, err := s.Pool.Query(ctx, `SELECT to_jsonb(x) FROM (SELECT id, payout_request_id, mismatch_type, expected_amount_minor::text AS expected_amount_minor,
		actual_amount_minor::text AS actual_amount_minor, currency, provider_reference, ledger_transaction_id, detail, detected_at, detected_by_run
		FROM app.payout_reconciliation_references ORDER BY detected_at DESC, id DESC LIMIT $1) x`, limit)
	if err != nil {
		return nil, err
	}
	out := []map[string]any{}
	defer rows.Close()
	for rows.Next() {
		var b []byte
		if err := rows.Scan(&b); err != nil {
			return nil, err
		}
		m := map[string]any{}
		_ = json.Unmarshal(b, &m)
		out = append(out, m)
	}
	return out, rows.Err()
}
