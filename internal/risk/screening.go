package risk

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Fatifizo/fundzim/internal/audit"
	"github.com/Fatifizo/fundzim/internal/platform/errs"
	"github.com/Fatifizo/fundzim/internal/platform/ids"
	"github.com/Fatifizo/fundzim/internal/platform/outbox"
)

// Screening foundation (ADR-045 §8; brief §27–§31). No sanctions-screening provider is selected: production screening
// is BLOCKED and the only implementation of ScreeningProvider is the TEST simulator (screening_sandbox.go), which
// works on synthetic references and never sees a name, a date of birth or an identity document. A provider result is
// not an analyst review, and an analyst review is not a compliance decision:
//
//   - NO_MATCH_REPORTED is what a provider said — never CLEARED; CLEARED and CONFIRMED_MATCH are analyst decisions
//     (risk.screening.review, step-up, never the requester or anyone linked to them).
//   - A potential match opens a SCREENING_REVIEW case; nobody is labelled sanctioned from a potential match.
//   - A timeout or provider error is ERROR — never cleared, never silently bypassed (it is retried by a person).
//
// The provider is called outside database transactions; callbacks are verified (signature, timestamp), stored once
// per provider event and applied in sequence order (an older result never overwrites a newer one).

// ScreeningProductionEnabled gates production screening. A constant on purpose: screening real people needs a
// provider selection, a lawful basis and data-protection review (LR-007, LR-053, LR-055), an ADR and acceptance.
const ScreeningProductionEnabled = false

// Screening statuses.
const (
	ScrPending        = "PENDING"
	ScrPotentialMatch = "POTENTIAL_MATCH"
	ScrNoMatch        = "NO_MATCH_REPORTED"
	ScrReviewRequired = "REVIEW_REQUIRED"
	ScrConfirmedMatch = "CONFIRMED_MATCH"
	ScrCleared        = "CLEARED"
	ScrError          = "ERROR"
	ScrNotRequested   = "NOT_REQUESTED"
)

// ScreeningEdges mirrors the 'risk_screening' machine (keep identical; TestScreeningEdgesMatchDatabase).
var ScreeningEdges = map[string][]string{
	"":                {ScrPending},
	ScrPending:        {ScrNoMatch, ScrPotentialMatch, ScrReviewRequired, ScrError},
	ScrError:          {ScrPending},
	ScrPotentialMatch: {ScrReviewRequired, ScrConfirmedMatch, ScrCleared},
	ScrReviewRequired: {ScrConfirmedMatch, ScrCleared},
}

// Screening engineering defaults (not provider facts: no provider is selected).
const (
	ScreeningTimeout      = 10 * time.Minute
	ScreeningPollInterval = time.Minute
	ScreeningMaxPolls     = 20
)

// EvScreeningUpdated is published when a screening request changes status.
const EvScreeningUpdated = "risk.screening_updated"

// ProviderCapabilities describes a screening provider (GetProviderCapabilities).
type ProviderCapabilities struct {
	Code          string `json:"code"`
	Environment   string `json:"environment"` // TEST only in Stage 13
	Webhooks      bool   `json:"webhooks"`
	Polling       bool   `json:"polling"`
	SyntheticOnly bool   `json:"synthetic_only"`
}

// ScreeningSubmission is what a provider receives: a synthetic reference only (no identity data in Stage 13).
type ScreeningSubmission struct {
	RequestID          string
	SyntheticReference string
}

// ScreeningMatch is a provider's (synthetic) potential match. It is never a finding.
type ScreeningMatch struct {
	MatchID string `json:"match_id"`
	List    string `json:"list"`
	Score   int    `json:"score"`
	Label   string `json:"label"` // TEST_ONLY
}

// ProviderResult is a provider's reported state of a screening.
type ProviderResult struct {
	EventID  string           `json:"event_id"`
	Sequence int              `json:"sequence"`
	Status   string           `json:"status"` // PENDING | POTENTIAL_MATCH | NO_MATCH_REPORTED | ERROR
	Matches  []ScreeningMatch `json:"matches"`
}

// ScreeningCallback is a verified provider callback.
type ScreeningCallback struct {
	ProviderReference string `json:"provider_reference"`
	ProviderResult
}

// ScreeningProvider is the provider-neutral screening interface (brief §28).
type ScreeningProvider interface {
	GetProviderCapabilities() ProviderCapabilities
	SubmitScreening(ctx context.Context, sub ScreeningSubmission) (providerReference string, err error)
	GetScreeningStatus(ctx context.Context, providerReference string) (ProviderResult, error)
	RetrieveMatches(ctx context.Context, providerReference string) ([]ScreeningMatch, error)
	VerifyScreeningWebhook(header string, body []byte, receivedAt time.Time) (ScreeningCallback, error)
}

// ErrProviderTimeout is returned by a provider that did not answer in time (an unknown outcome: never cleared).
var ErrProviderTimeout = errors.New("risk: screening provider timeout")

// ScreeningRequest is a screening request (staff view).
type ScreeningRequest struct {
	ID                 string     `json:"id"`
	SubjectType        string     `json:"subject_type"`
	SubjectID          string     `json:"subject_id"`
	ProviderCode       string     `json:"provider_code"`
	Environment        string     `json:"environment"`
	SyntheticReference string     `json:"synthetic_reference"`
	Status             string     `json:"status"`
	ProviderReference  *string    `json:"provider_reference"`
	ResultSequence     int        `json:"result_sequence"`
	MatchCount         int        `json:"match_count"`
	TimeoutAt          time.Time  `json:"timeout_at"`
	PollAttempts       int        `json:"poll_attempts"`
	LastError          *string    `json:"last_error"`
	RequestedBy        string     `json:"requested_by"`
	ReviewedBy         *string    `json:"reviewed_by"`
	ReviewedAt         *time.Time `json:"reviewed_at"`
	ReviewReason       *string    `json:"review_reason"`
	CaseID             *string    `json:"case_id"`
	Version            int        `json:"version"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
	// Label is always TEST_ONLY: synthetic data, not a screening of a real person.
	Label string `json:"label"`
}

const screeningCols = `id, subject_type, subject_id, provider_code, environment, synthetic_reference, status, provider_reference, result_sequence,
	match_count, timeout_at, poll_attempts, last_error, requested_by, reviewed_by, reviewed_at, review_reason, case_id, version, created_at, updated_at`

func scanScreening(r pgx.Row) (ScreeningRequest, error) {
	var x ScreeningRequest
	err := r.Scan(&x.ID, &x.SubjectType, &x.SubjectID, &x.ProviderCode, &x.Environment, &x.SyntheticReference, &x.Status, &x.ProviderReference,
		&x.ResultSequence, &x.MatchCount, &x.TimeoutAt, &x.PollAttempts, &x.LastError, &x.RequestedBy, &x.ReviewedBy, &x.ReviewedAt, &x.ReviewReason,
		&x.CaseID, &x.Version, &x.CreatedAt, &x.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return ScreeningRequest{}, ErrNotFound
	}
	x.Label = "TEST_ONLY"
	return x, err
}

var screeningSubjects = map[string]bool{"USER": true, "ORGANISATION": true, "BENEFICIARY": true}

// NewScreening requests a TEST screening of a subject with a simulator scenario.
type NewScreening struct {
	SubjectType string `json:"subject_type"`
	SubjectID   string `json:"subject_id"`
	Scenario    string `json:"scenario"`
}

// RequestScreening records a PENDING request, then submits it to the provider (outside the transaction). A
// submission failure leaves the request in ERROR (never cleared). Idempotent per requester and key.
func (s *Service) RequestScreening(ctx context.Context, staffID, idemKey string, in NewScreening) (ScreeningRequest, error) {
	if s.Screening == nil {
		return ScreeningRequest{}, ErrScreeningOff
	}
	if !screeningSubjects[in.SubjectType] || !ids.Valid(in.SubjectID) {
		return ScreeningRequest{}, validation("subject_id", "INVALID_VALUE")
	}
	if !SandboxScenarios[in.Scenario] {
		return ScreeningRequest{}, validation("scenario", "INVALID_VALUE")
	}
	if !ids.Valid(idemKey) {
		return ScreeningRequest{}, validation("idempotency_key", "INVALID_FORMAT")
	}
	if self, err := s.linked(ctx, staffID, subjectUser(in.SubjectType, in.SubjectID)); err != nil {
		return ScreeningRequest{}, err
	} else if self {
		return ScreeningRequest{}, ErrConflictOfInt
	}
	now := s.now()
	id := ids.New()
	ref := "TEST-" + in.Scenario + "-" + strings.ToUpper(strings.ReplaceAll(id[len(id)-12:], "-", ""))
	var existing string
	err := s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `SELECT id FROM risk.risk_screening_requests WHERE requested_by = $1 AND idempotency_key = $2`, staffID, idemKey).Scan(&existing)
		if err == nil {
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO risk.risk_screening_requests (id, subject_type, subject_id, provider_code, synthetic_reference, status,
			timeout_at, next_poll_at, requested_by, idempotency_key) VALUES ($1, $2, $3, $4, $5, 'PENDING', $6, $7, $8, $9)`,
			id, in.SubjectType, in.SubjectID, s.Screening.GetProviderCapabilities().Code, ref, now.Add(ScreeningTimeout), now.Add(ScreeningPollInterval),
			staffID, idemKey); err != nil {
			return mapDB(err)
		}
		if err := riskEvent(ctx, tx, "risk_screening", id, "SCREENING_REQUESTED", "STAFF", staffID, map[string]any{"scenario": in.Scenario}, now); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{Action: "risk.screening.requested", TargetType: "risk_screening", TargetID: id,
			Metadata: map[string]any{"subject_type": in.SubjectType, "subject_id": in.SubjectID, "environment": "TEST"}})
	})
	if err != nil {
		return ScreeningRequest{}, err
	}
	if existing != "" {
		return s.Screening1(ctx, existing)
	}
	provRef, subErr := s.Screening.SubmitScreening(ctx, ScreeningSubmission{RequestID: id, SyntheticReference: ref})
	err = s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if subErr != nil {
			return s.screeningError(ctx, tx, id, "SUBMIT_FAILED", s.now())
		}
		_, err := tx.Exec(ctx, `UPDATE risk.risk_screening_requests SET provider_reference = $2, version = version + 1 WHERE id = $1`, id, provRef)
		return err
	})
	if err != nil {
		return ScreeningRequest{}, err
	}
	return s.Screening1(ctx, id)
}

// Screening1 returns one screening request.
func (s *Service) Screening1(ctx context.Context, id string) (ScreeningRequest, error) {
	if !ids.Valid(id) {
		return ScreeningRequest{}, ErrNotFound
	}
	return scanScreening(s.Pool.QueryRow(ctx, `SELECT `+screeningCols+` FROM risk.risk_screening_requests WHERE id = $1`, id))
}

// ScreeningResultRow is a stored provider result.
type ScreeningResultRow struct {
	ProviderEventID string          `json:"provider_event_id"`
	Source          string          `json:"source"`
	Sequence        int             `json:"sequence"`
	ReportedStatus  string          `json:"reported_status"`
	Matches         json.RawMessage `json:"matches"`
	Applied         bool            `json:"applied"`
	Label           string          `json:"label"`
	ReceivedAt      time.Time       `json:"received_at"`
}

// ScreeningResults lists a request's stored provider results.
func (s *Service) ScreeningResults(ctx context.Context, id string) ([]ScreeningResultRow, error) {
	rows, err := s.Pool.Query(ctx, `SELECT provider_event_id, source, sequence, reported_status, matches, applied, label, received_at
		FROM risk.risk_screening_results WHERE request_id = $1 ORDER BY received_at, id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ScreeningResultRow{}
	for rows.Next() {
		var r ScreeningResultRow
		if err := rows.Scan(&r.ProviderEventID, &r.Source, &r.Sequence, &r.ReportedStatus, &r.Matches, &r.Applied, &r.Label, &r.ReceivedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Screenings lists screening requests (newest first).
func (s *Service) Screenings(ctx context.Context, status string) ([]ScreeningRequest, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+screeningCols+` FROM risk.risk_screening_requests WHERE ($1 = '' OR status = $1)
		ORDER BY created_at DESC, id DESC LIMIT 200`, status)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ScreeningRequest{}
	for rows.Next() {
		x, err := scanScreening(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// ScreeningStatus is the latest screening status of a subject (NOT_REQUESTED when never screened). Payout eligibility
// reads it (EC-15); NO_MATCH_REPORTED is not a clearance.
func (s *Service) ScreeningStatus(ctx context.Context, subjectType, subjectID string) (string, error) {
	if !ids.Valid(subjectID) {
		return ScrNotRequested, nil
	}
	var st string
	err := s.Pool.QueryRow(ctx, `SELECT status FROM risk.risk_screening_requests WHERE subject_type = $1 AND subject_id = $2
		ORDER BY created_at DESC, id DESC LIMIT 1`, subjectType, subjectID).Scan(&st)
	if errors.Is(err, pgx.ErrNoRows) {
		return ScrNotRequested, nil
	}
	return st, err
}

func (s *Service) screeningError(ctx context.Context, tx pgx.Tx, id, code string, now time.Time) error {
	var subjectType, subjectID string
	tag, err := tx.Exec(ctx, `UPDATE risk.risk_screening_requests SET status = 'ERROR', last_error = $2, next_poll_at = NULL, version = version + 1
		WHERE id = $1 AND status = 'PENDING'`, id, code)
	if err != nil || tag.RowsAffected() == 0 {
		return mapDB(err)
	}
	if err := tx.QueryRow(ctx, `SELECT subject_type, subject_id FROM risk.risk_screening_requests WHERE id = $1`, id).Scan(&subjectType, &subjectID); err != nil {
		return err
	}
	if err := riskEvent(ctx, tx, "risk_screening", id, "SCREENING_ERROR", "SYSTEM", "risk.screening", map[string]any{"error": code}, now); err != nil {
		return err
	}
	if code == "TIMEOUT" {
		if err := s.monitor(ctx, tx, "SCREENING_TIMEOUT", SevMed, subjectType, subjectID, id, map[string]any{"request_id": id}, now); err != nil {
			return err
		}
	}
	return screeningOutbox(ctx, tx, id, subjectType, subjectID, ScrError, now)
}

func screeningOutbox(ctx context.Context, tx pgx.Tx, id, subjectType, subjectID, status string, now time.Time) error {
	_, err := outbox.Write(ctx, tx, outbox.Event{AggregateType: "risk_screening", AggregateID: id, EventType: EvScreeningUpdated,
		Payload:    map[string]any{"request_id": id, "subject_type": subjectType, "subject_id": subjectID, "status": status, "environment": "TEST"},
		OccurredAt: now})
	return err
}

var reportedStatuses = map[string]bool{ScrPending: true, ScrPotentialMatch: true, ScrNoMatch: true, ScrError: true}

// applyResult stores a provider result once (per provider event) and applies it only if it is newer than what was
// applied and the request still awaits a provider result. Returns whether it was applied.
func (s *Service) applyResult(ctx context.Context, tx pgx.Tx, req ScreeningRequest, source string, r ProviderResult, now time.Time) (bool, error) {
	if !reportedStatuses[r.Status] || r.EventID == "" || len(r.EventID) > 128 || r.Sequence < 0 {
		return false, ErrInvalidPayload
	}
	apply := r.Sequence > req.ResultSequence && req.Status == ScrPending && r.Status != ScrPending
	matches := r.Matches
	if matches == nil {
		matches = []ScreeningMatch{}
	}
	for i := range matches {
		matches[i].Label = "TEST_ONLY"
	}
	mb, _ := json.Marshal(matches)
	tag, err := tx.Exec(ctx, `INSERT INTO risk.risk_screening_results (id, request_id, provider_event_id, source, sequence, reported_status, matches,
		applied, received_at) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9) ON CONFLICT ON CONSTRAINT uq_risk_screening_results_event DO NOTHING`,
		ids.New(), req.ID, r.EventID, source, r.Sequence, r.Status, mb, apply, now)
	if err != nil {
		return false, err
	}
	if tag.RowsAffected() == 0 || !apply {
		return false, nil // a duplicate, an older result or a request no longer awaiting the provider: stored, not applied
	}
	if r.Status == ScrError {
		_, err := tx.Exec(ctx, `UPDATE risk.risk_screening_requests SET result_sequence = $2 WHERE id = $1`, req.ID, r.Sequence)
		if err != nil {
			return false, err
		}
		return true, s.screeningError(ctx, tx, req.ID, "PROVIDER_ERROR", now)
	}
	if _, err := tx.Exec(ctx, `UPDATE risk.risk_screening_requests SET status = $2, result_sequence = $3, match_count = $4, next_poll_at = NULL,
		version = version + 1 WHERE id = $1`, req.ID, r.Status, r.Sequence, len(matches)); err != nil {
		return false, mapDB(err)
	}
	if err := riskEvent(ctx, tx, "risk_screening", req.ID, "SCREENING_"+r.Status, "SYSTEM", "risk.screening", map[string]any{"source": source,
		"sequence": r.Sequence, "match_count": len(matches)}, now); err != nil {
		return false, err
	}
	if r.Status == ScrPotentialMatch {
		caseID, _, err := s.openOrLinkCase(ctx, tx, caseSpec{CaseType: "SCREENING_REVIEW", SubjectType: req.SubjectType, SubjectID: req.SubjectID,
			Priority: "P2", OpenedByType: "SYSTEM", OpenedBy: "risk.screening"}, now)
		if err != nil {
			return false, err
		}
		if _, err := tx.Exec(ctx, `UPDATE risk.risk_screening_requests SET case_id = $2 WHERE id = $1`, req.ID, caseID); err != nil {
			return false, err
		}
		if err := s.addEvidence(ctx, tx, caseID, "SCREENING_RESULT", "risk_screening", req.ID, map[string]any{"status": r.Status,
			"match_count": len(matches), "label": "TEST_ONLY", "note": "A potential match is not a finding: analyst review required."},
			"risk.screening", "SYSTEM", "risk.screening", now); err != nil {
			return false, err
		}
		var out Outcome
		sig := Signal{Type: SigScreeningPotentialMatch, SubjectType: req.SubjectType, SubjectID: req.SubjectID, SourceModule: "risk",
			SourceEventID: req.ID, SourceEventType: EvScreeningUpdated, ObservedAt: now, Facts: map[string]any{"match_count": len(matches)}}
		if err := sig.normalise(); err != nil {
			return false, err
		}
		if err := s.recordSignalTx(ctx, tx, sig, &out); err != nil {
			return false, err
		}
	}
	return true, screeningOutbox(ctx, tx, req.ID, req.SubjectType, req.SubjectID, r.Status, now)
}

// HandleScreeningCallback verifies and applies a provider callback. Rejected callbacks are recorded as monitoring
// events (never applied).
func (s *Service) HandleScreeningCallback(ctx context.Context, header string, body []byte) error {
	if s.Screening == nil {
		return ErrScreeningOff
	}
	now := s.now()
	cb, err := s.Screening.VerifyScreeningWebhook(header, body, now)
	if err != nil {
		_ = s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
			h := sha256Hex(body)
			return s.monitor(ctx, tx, "SCREENING_CALLBACK_REJECTED", SevMed, "", "", "cb:"+h+":"+now.Format(time.RFC3339Nano),
				map[string]any{"reason": err.Error(), "body_sha256": h}, now)
		})
		return errs.New(errs.Unauthenticated, "SCREENING_CALLBACK_REJECTED", "The callback could not be verified.")
	}
	return s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		req, err := scanScreening(tx.QueryRow(ctx, `SELECT `+screeningCols+` FROM risk.risk_screening_requests
			WHERE provider_code = $1 AND provider_reference = $2 FOR UPDATE`, s.Screening.GetProviderCapabilities().Code, cb.ProviderReference))
		if err != nil {
			return err
		}
		_, err = s.applyResult(ctx, tx, req, "CALLBACK", cb.ProviderResult, now)
		return err
	})
}

// PollScreenings polls PENDING requests that are due and times out those past their deadline (job). A timeout is
// ERROR — never cleared.
func (s *Service) PollScreenings(ctx context.Context, limit int) (int, error) {
	if s.Screening == nil {
		return 0, nil
	}
	if limit <= 0 {
		limit = 50
	}
	now := s.now()
	rows, err := s.Pool.Query(ctx, `SELECT `+screeningCols+` FROM risk.risk_screening_requests WHERE status = 'PENDING'
		AND (timeout_at <= $1 OR (next_poll_at IS NOT NULL AND next_poll_at <= $1)) ORDER BY coalesce(next_poll_at, timeout_at) LIMIT $2`, now, limit)
	if err != nil {
		return 0, err
	}
	var due []ScreeningRequest
	for rows.Next() {
		x, err := scanScreening(rows)
		if err != nil {
			rows.Close()
			return 0, err
		}
		due = append(due, x)
	}
	rows.Close()
	n := 0
	for _, req := range due {
		if !req.TimeoutAt.After(now) || req.PollAttempts >= ScreeningMaxPolls {
			if err := s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error { return s.screeningError(ctx, tx, req.ID, "TIMEOUT", now) }); err != nil {
				return n, err
			}
			n++
			continue
		}
		if req.ProviderReference == nil {
			continue
		}
		res, perr := s.Screening.GetScreeningStatus(ctx, *req.ProviderReference) // outside any transaction
		err := s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
			cur, err := scanScreening(tx.QueryRow(ctx, `SELECT `+screeningCols+` FROM risk.risk_screening_requests WHERE id = $1 FOR UPDATE`, req.ID))
			if err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE risk.risk_screening_requests SET poll_attempts = poll_attempts + 1, next_poll_at = $2 WHERE id = $1`,
				req.ID, now.Add(ScreeningPollInterval)); err != nil {
				return err
			}
			if perr != nil {
				return nil // unknown: keep PENDING until the deadline (never cleared)
			}
			_, err = s.applyResult(ctx, tx, cur, "POLL", res, now)
			return err
		})
		if err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

// ReviewScreening is the analyst decision on a potential match (risk.screening.review; fresh step-up; never the
// requester or anyone linked to them or to the subject). CONFIRMED_MATCH records a signal and escalates the case to
// compliance; it applies no restriction itself.
func (s *Service) ReviewScreening(ctx context.Context, staffID string, stepUpAt time.Time, id, decision, reason string, expect int) (ScreeningRequest, error) {
	if stepUpAt.IsZero() {
		return ScreeningRequest{}, ErrStepUp
	}
	if decision != ScrConfirmedMatch && decision != ScrCleared {
		return ScreeningRequest{}, validation("decision", "INVALID_VALUE")
	}
	r, err := reasonText(reason)
	if err != nil {
		return ScreeningRequest{}, err
	}
	if !ids.Valid(id) {
		return ScreeningRequest{}, ErrNotFound
	}
	now := s.now()
	err = s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		req, err := scanScreening(tx.QueryRow(ctx, `SELECT `+screeningCols+` FROM risk.risk_screening_requests WHERE id = $1 FOR UPDATE`, id))
		if err != nil {
			return err
		}
		if expect > 0 && req.Version != expect {
			return ErrStale
		}
		if req.Status != ScrPotentialMatch && req.Status != ScrReviewRequired {
			return ErrScreeningState
		}
		if self, err := s.linked(ctx, staffID, req.RequestedBy, subjectUser(req.SubjectType, req.SubjectID)); err != nil {
			return err
		} else if self {
			return ErrSelfApproval
		}
		if _, err := tx.Exec(ctx, `UPDATE risk.risk_screening_requests SET status = $2, reviewed_by = $3, reviewed_at = $4, review_step_up_at = $5,
			review_reason = $6, version = version + 1 WHERE id = $1`, id, decision, staffID, now, minTime(stepUpAt.UTC(), now), r); err != nil {
			return mapDB(err)
		}
		if err := riskEvent(ctx, tx, "risk_screening", id, "SCREENING_REVIEWED", "STAFF", staffID, map[string]any{"decision": decision, "reason": r}, now); err != nil {
			return err
		}
		if req.CaseID != nil {
			if err := s.addEvidence(ctx, tx, *req.CaseID, "SCREENING_RESULT", "risk_screening_review", id, map[string]any{"decision": decision,
				"label": "TEST_ONLY"}, "staff", "STAFF", staffID, now); err != nil {
				return err
			}
		}
		if decision == ScrConfirmedMatch {
			var out Outcome
			sig := Signal{Type: SigScreeningConfirmedMatch, SubjectType: req.SubjectType, SubjectID: req.SubjectID, SourceModule: "risk",
				SourceEventID: id, SourceEventType: EvScreeningUpdated, ObservedAt: now, Facts: map[string]any{}}
			if err := sig.normalise(); err != nil {
				return err
			}
			if err := s.recordSignalTx(ctx, tx, sig, &out); err != nil {
				return err
			}
			if req.CaseID != nil {
				c, err := lockCase(ctx, tx, *req.CaseID, 0)
				if err != nil {
					return err
				}
				if c.EscalatedAt == nil && c.Status != CaseResolved && c.Status != CaseClosed && c.Status != CaseDecision {
					if c.Status == CaseOpen || c.Status == CaseReopened {
						if err := setStatus(ctx, tx, c, CaseTriage, ""); err != nil {
							return err
						}
						c.Status = CaseTriage
					}
					if c.Status == CaseAwaitingInfo {
						if err := setStatus(ctx, tx, c, CaseInvestigating, ""); err != nil {
							return err
						}
						c.Status = CaseInvestigating
					}
					if err := setStatus(ctx, tx, c, CaseEscalated, `, escalated_at = $3, assigned_team = 'COMPLIANCE'`, now); err != nil {
						return err
					}
					if err := s.escalated(ctx, tx, c, "STAFF", staffID, "screening confirmed match (TEST)", now); err != nil {
						return err
					}
				}
			}
		}
		if err := screeningOutbox(ctx, tx, id, req.SubjectType, req.SubjectID, decision, now); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{Action: "risk.screening.reviewed", TargetType: "risk_screening", TargetID: id, Reason: r,
			Metadata: map[string]any{"decision": decision, "environment": "TEST"}})
	})
	if err != nil {
		return ScreeningRequest{}, err
	}
	return s.Screening1(ctx, id)
}

// RetryScreening resubmits a request in ERROR (a person decides to retry; ERROR → PENDING).
func (s *Service) RetryScreening(ctx context.Context, staffID, id string) (ScreeningRequest, error) {
	if s.Screening == nil {
		return ScreeningRequest{}, ErrScreeningOff
	}
	req, err := s.Screening1(ctx, id)
	if err != nil {
		return ScreeningRequest{}, err
	}
	if req.Status != ScrError {
		return ScreeningRequest{}, ErrScreeningState
	}
	now := s.now()
	err = s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE risk.risk_screening_requests SET status = 'PENDING', last_error = NULL, provider_reference = NULL,
			timeout_at = $2, next_poll_at = $3, poll_attempts = 0, version = version + 1 WHERE id = $1 AND status = 'ERROR'`,
			id, now.Add(ScreeningTimeout), now.Add(ScreeningPollInterval))
		if err != nil {
			return mapDB(err)
		}
		if tag.RowsAffected() == 0 {
			return ErrScreeningState
		}
		return riskEvent(ctx, tx, "risk_screening", id, "SCREENING_RETRIED", "STAFF", staffID, map[string]any{}, now)
	})
	if err != nil {
		return ScreeningRequest{}, err
	}
	provRef, subErr := s.Screening.SubmitScreening(ctx, ScreeningSubmission{RequestID: id, SyntheticReference: req.SyntheticReference})
	err = s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if subErr != nil {
			return s.screeningError(ctx, tx, id, "SUBMIT_FAILED", s.now())
		}
		_, err := tx.Exec(ctx, `UPDATE risk.risk_screening_requests SET provider_reference = $2, version = version + 1 WHERE id = $1`, id, provRef)
		return err
	})
	if err != nil {
		return ScreeningRequest{}, err
	}
	return s.Screening1(ctx, id)
}
