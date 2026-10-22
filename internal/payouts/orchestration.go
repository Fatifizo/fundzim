package payouts

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"github.com/Fatifizo/fundzim/internal/audit"
	"github.com/Fatifizo/fundzim/internal/ledger"
	"github.com/Fatifizo/fundzim/internal/platform/errs"
	"github.com/Fatifizo/fundzim/internal/platform/httpx"
	"github.com/Fatifizo/fundzim/internal/platform/ids"
	"github.com/Fatifizo/fundzim/internal/platform/jobs"
	"github.com/Fatifizo/fundzim/internal/psp"
	"github.com/Fatifizo/fundzim/internal/risk"
)

// Orchestration (payout-lifecycle.md §3–§5; ADR-043 §6): approvals → pre-submission re-check → SUBMITTED (journal
// payout:{id}:submitted) → provider call outside any transaction, recorded as an attempt committed IN_FLIGHT first →
// provider observations (sync answer, webhook, poll) → terminal state → ledger journals derived from the state
// (syncLedger). An UNKNOWN outcome keeps the reservation (BLOCKED), is never resubmitted and is resolved only by
// an authoritative status, a webhook or reconciliation.

// Tunables (operational, not regulatory).
const (
	pollBase          = 5 * time.Second
	pollMax           = 10 * time.Minute
	unknownHorizon    = 15 * time.Minute // simulator contract: after this an UNKNOWN payout needs reconciliation
	crashedAttemptAge = 2 * time.Minute  // an IN_FLIGHT attempt this old belongs to a crashed worker

	// Defaults of the submission retry budget (PAYOUTS_SUBMIT_RETRY_*; KI-S15-19).
	DefaultSubmitRetryBudget = 5
	DefaultSubmitRetryBase   = 5 * time.Second
	DefaultSubmitRetryMax    = 10 * time.Minute

	// ExcSubmissionRetriesExhausted halts automatic submission of a payout whose same-reference retries after
	// DEFINITELY_NOT_SENT outcomes used up the budget. While it is unresolved the database refuses CREATE_PAYOUT;
	// resolving it (operator, audited) grants a fresh budget.
	ExcSubmissionRetriesExhausted = "SUBMISSION_RETRIES_EXHAUSTED"
)

// submitRetry returns the budget and the jittered, bounded backoff of automatic resubmissions.
func (s *Service) submitRetry() (int, jobs.Backoff) {
	budget, b := s.SubmitRetryBudget, jobs.Backoff{Base: s.SubmitRetryBase, Cap: s.SubmitRetryMax}
	if budget <= 0 {
		budget = DefaultSubmitRetryBudget
	}
	if b.Base <= 0 {
		b.Base = DefaultSubmitRetryBase
	}
	if b.Cap <= 0 {
		b.Cap = DefaultSubmitRetryMax
	}
	return budget, b
}

// notSentSinceResume counts the payout's DEFINITELY_NOT_SENT and RATE_LIMITED submissions (both refused before processing:
// psp.CategoryRetry AFTER_BACKOFF; KI-S17-01) since its budget was last renewed (the
// latest resolution of a SUBMISSION_RETRIES_EXHAUSTED exception; the payout's whole history before any).
func notSentSinceResume(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, id string) (int, error) {
	var n int
	err := q.QueryRow(ctx, `SELECT count(*) FROM app.payout_attempts a WHERE a.payout_request_id = $1 AND a.operation = 'CREATE_PAYOUT'
		AND a.classification IN ('DEFINITELY_NOT_SENT', 'RATE_LIMITED') AND a.started_at > coalesce((SELECT max(x.resolved_at) FROM app.payout_exceptions x
		  WHERE x.payout_request_id = $1 AND x.exception_type = 'SUBMISSION_RETRIES_EXHAUSTED' AND x.status = 'RESOLVED'), '-infinity')`, id).Scan(&n)
	return n, err
}

// haltSubmission opens the exhaustion exception (once; the open-exception index dedupes). The payout stays SUBMITTED:
// its reservation and in-transit journal are untouched.
func (s *Service) haltSubmission(ctx context.Context, tx pgx.Tx, p *Payout, attempts, budget int) error {
	return s.openException(ctx, tx, ExcSubmissionRetriesExhausted, "SEV2", "ORCHESTRATION", p, "",
		map[string]any{"definitely_not_sent_attempts": attempts, "budget": budget}, "")
}

// ---- approvals ---------------------------------------------------------------------------------------------------

// DecisionInput is a staff approval or rejection.
type DecisionInput struct {
	Reason           string `json:"reason"`
	ConflictAttested bool   `json:"conflict_attested"`
}

// ApprovePayout records a FINANCE approval (step-up enforced by the route permission and recorded); enough distinct
// approvals for the tier move the payout to APPROVED (Y6). Separation of duties is enforced in Go and by the
// database guard (requester, campaign owner, their linked accounts, destination changer or verifier).
func (s *Service) ApprovePayout(ctx context.Context, staffID string, stepUpAt time.Time, id string, in DecisionInput) (Payout, error) {
	return s.decide(ctx, staffID, stepUpAt, id, in, "APPROVE")
}

// RejectPayout records a FINANCE rejection: the payout is REJECTED (Y4) and its reservation released.
func (s *Service) RejectPayout(ctx context.Context, staffID string, stepUpAt time.Time, id string, in DecisionInput) (Payout, error) {
	return s.decide(ctx, staffID, stepUpAt, id, in, "REJECT")
}

func (s *Service) decide(ctx context.Context, staffID string, stepUpAt time.Time, id string, in DecisionInput, decision string) (Payout, error) {
	reason := strings.TrimSpace(in.Reason)
	if n := utf8.RuneCountInString(reason); n < 5 || n > 2000 {
		return Payout{}, httpx.Validation(errs.Detail{Field: "reason", Code: "INVALID_LENGTH"})
	}
	if !in.ConflictAttested {
		return Payout{}, httpx.Validation(errs.Detail{Field: "conflict_attested", Code: "REQUIRED"})
	}
	if stepUpAt.IsZero() {
		return Payout{}, ErrStepUp
	}
	identities, err := s.KYC.ActorIdentities(ctx, staffID)
	if err != nil {
		return Payout{}, err
	}
	var exp any
	if v, err := s.valueLimit(ctx, LimitApprovalValidity, "DURATION", s.now()); err != nil {
		return Payout{}, err
	} else if v.Configured {
		exp = s.now().Add(time.Duration(v.Value) * time.Second)
	}
	err = s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		p, err := s.payout(ctx, tx, id, true)
		if err != nil {
			return err
		}
		if p.Status != StReview {
			return ErrPayoutState
		}
		d, err := s.load(ctx, tx, p.DestinationID, false)
		if err != nil {
			return err
		}
		for _, who := range identities {
			if who == p.RequestedBy || (p.OwnerUserID != nil && who == *p.OwnerUserID) || who == d.lastChangedBy ||
				(d.verifiedBy != nil && who == *d.verifiedBy) {
				return ErrSelfApproval
			}
		}
		if p.OwnerOrganisationID != nil {
			for _, who := range identities {
				if role, err := s.Orgs.MemberRole(ctx, *p.OwnerOrganisationID, who); err != nil {
					return err
				} else if role != "" {
					return ErrSelfApproval // a member of the owning organisation never approves its payout
				}
			}
		}
		if decision == "APPROVE" && s.Risk != nil {
			// a payout under an enforced risk hold is never approved (Stage 13; the hold is released by its own maker-checker)
			owner, org := "", ""
			if p.OwnerUserID != nil {
				owner = *p.OwnerUserID
			}
			if p.OwnerOrganisationID != nil {
				org = *p.OwnerOrganisationID
			}
			hs, err := s.Risk.WithdrawalHolds(ctx, risk.WithdrawalScope{CampaignID: p.CampaignID, OwnerUserID: owner, OwnerOrgID: org,
				DestinationID: p.DestinationID})
			if err != nil {
				return err
			}
			if len(hs) > 0 {
				return ErrRiskHold
			}
		}
		if _, err := tx.Exec(ctx, `SAVEPOINT approve`); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO app.payout_approvals (id, payout_request_id, approval_round, approver_id, approver_role, decision,
			reason, step_up_at, conflict_attested, decided_at, expires_at) VALUES ($1,$2,$3,$4,'FINANCE',$5,$6,$7,true,$8,$9)`,
			ids.New(), p.ID, p.ApprovalRound, staffID, decision, reason, stepUpAt, s.now(), exp); err != nil {
			_, _ = tx.Exec(ctx, `ROLLBACK TO SAVEPOINT approve`)
			if strings.Contains(err.Error(), "uq_payout_approvals_approver") {
				return errs.New(errs.Conflict, "ALREADY_DECIDED", "You have already decided this payout in the current approval round.")
			}
			if strings.Contains(err.Error(), "maker-checker") || strings.Contains(err.Error(), "segregation of duties") {
				return ErrSelfApproval
			}
			return err
		}
		if decision == "REJECT" {
			if err := s.insertEvent(ctx, tx, p.ID, event{Type: "APPROVAL_REJECTED", Source: "STAFF", ActorType: "STAFF", ActorID: staffID, Reason: reason}); err != nil {
				return err
			}
			return s.move(ctx, tx, &p, StRejected, event{Type: "WITHDRAWAL_REJECTED", Code: "Y4", Source: "STAFF", ActorType: "STAFF",
				ActorID: staffID, Reason: reason}, `decision_reason_code = $4`, "STAFF_REJECTED")
		}
		if err := s.insertEvent(ctx, tx, p.ID, event{Type: "APPROVAL_GRANTED", Source: "STAFF", ActorType: "STAFF", ActorID: staffID, Reason: reason}); err != nil {
			return err
		}
		if err := audit.Record(ctx, tx, audit.Event{Action: "payout.approval.granted", ActorType: "staff", ActorID: staffID, TargetType: "payout_request",
			TargetID: p.ID, Reason: reason, Metadata: map[string]any{"round": p.ApprovalRound, "policy": p.ApprovalPolicy}}); err != nil {
			return err
		}
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(DISTINCT approver_id) FROM app.payout_approvals WHERE payout_request_id = $1 AND approval_round = $2
			AND decision = 'APPROVE' AND (expires_at IS NULL OR expires_at > $3)`, p.ID, p.ApprovalRound, s.now()).Scan(&n); err != nil {
			return err
		}
		required := 1
		if p.ApprovalPolicy == "DUAL" {
			required = 2
		}
		if n >= required {
			return s.move(ctx, tx, &p, StApproved, event{Code: "Y6", Source: "STAFF", ActorType: "STAFF", ActorID: staffID, Reason: reason}, "")
		}
		return nil
	})
	if err != nil {
		return Payout{}, err
	}
	if decision == "REJECT" {
		if err := s.syncLedger(ctx, id); err != nil {
			s.Logger.Warn("payouts: release deferred to recovery", "payout_request_id", id, "err", err)
		}
	}
	return s.payout(ctx, s.Pool, id, false)
}

// Recheck re-evaluates a payout under review (RECHECK decision) so approvers see current facts (payout.operate).
func (s *Service) Recheck(ctx context.Context, staffID, id string) (Decision, error) {
	p, err := s.payout(ctx, s.Pool, id, false)
	if err != nil {
		return Decision{}, err
	}
	if p.Status != StReview {
		return Decision{}, ErrPayoutState
	}
	sub := p.subject()
	f, _, _, err := s.gather(ctx, PhaseRecheck, sub)
	if err != nil {
		return Decision{}, err
	}
	dec := Evaluate(f)
	err = s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		did, err := s.writeDecision(ctx, tx, p.ID, sub, p.RequestedBy, f, dec)
		if err != nil {
			return err
		}
		if err := s.insertEvent(ctx, tx, p.ID, event{Type: "ELIGIBILITY_CHECKED", Source: "STAFF", ActorType: "STAFF", ActorID: staffID,
			Reason: "recheck: " + dec.Outcome, DecisionID: did}); err != nil {
			return err
		}
		// a higher tier only ever raises the policy (the database refuses lowering it)
		if dec.ApprovalTier == "DUAL" && p.ApprovalPolicy == "SINGLE" {
			_, err = tx.Exec(ctx, `UPDATE app.payout_requests SET approval_policy = 'DUAL', version = version + 1 WHERE id = $1`, p.ID)
		}
		return err
	})
	return dec, err
}

func (p Payout) subject() subject {
	return subject{CampaignID: p.CampaignID, RequesterID: p.RequestedBy, DestinationID: p.DestinationID, Currency: p.Currency,
		Amount: p.AmountMinor, PayoutID: p.ID, SnapshotVersion: p.DestinationVersion, ApprovalPolicy: p.ApprovalPolicy, ApprovalRound: p.ApprovalRound}
}

// invalidateApprovals starts a new approval round for the open payouts that pay to a destination whose details,
// status or verification changed (Y7 for APPROVED; partial approvals in PENDING_REVIEW stop counting). Called in
// the destination change transaction.
func (s *Service) invalidateApprovals(ctx context.Context, tx pgx.Tx, destinationID, reason, actorType, actorID string) error {
	rows, err := tx.Query(ctx, `SELECT id FROM app.payout_requests WHERE destination_id = $1 AND status IN ('PENDING_REVIEW','APPROVED') FOR UPDATE`, destinationID)
	if err != nil {
		return err
	}
	list, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return err
	}
	for _, id := range list {
		p, err := s.payout(ctx, tx, id, false)
		if err != nil {
			return err
		}
		src := "SYSTEM"
		if actorType == "OWNER" {
			src = "OWNER"
		}
		if err := s.insertEvent(ctx, tx, id, event{Type: "DESTINATION_CHANGED", Source: src, ActorType: actorType, ActorID: actorID, Reason: reason}); err != nil {
			return err
		}
		if p.Status == StApproved {
			if err := s.move(ctx, tx, &p, StReview, event{Type: "APPROVALS_INVALIDATED", Code: "Y7", Reason: reason},
				`approval_round = approval_round + 1`); err != nil {
				return err
			}
		} else {
			if _, err := tx.Exec(ctx, `UPDATE app.payout_requests SET approval_round = approval_round + 1, version = version + 1 WHERE id = $1`, id); err != nil {
				return err
			}
			if err := s.insertEvent(ctx, tx, id, event{Type: "APPROVALS_INVALIDATED", Reason: reason}); err != nil {
				return err
			}
		}
	}
	return nil
}

// ---- submission --------------------------------------------------------------------------------------------------

// SubmitDue runs the pre-submission re-check for APPROVED payouts and submits the eligible ones (Y8).
func (s *Service) SubmitDue(ctx context.Context, limit int) (int, error) {
	if s.ExecEnv != ExecLocalSimulator {
		return 0, nil // no payout leaves this process unless it is the simulator
	}
	rows, err := s.Pool.Query(ctx, `SELECT id FROM app.payout_requests WHERE status = 'APPROVED' ORDER BY updated_at LIMIT $1`, limit)
	if err != nil {
		return 0, err
	}
	list, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return 0, err
	}
	n := 0
	for _, id := range list {
		if ok, err := s.SubmitApproved(ctx, id); err != nil {
			s.Logger.Warn("payouts: submission failed", "payout_request_id", id, "err", err)
		} else if ok {
			n++
		}
	}
	return n, nil
}

// SubmitNow re-checks and submits one approved payout and sends the provider instruction right away.
func (s *Service) SubmitNow(ctx context.Context, id string) (bool, error) {
	ok, err := s.SubmitApproved(ctx, id)
	if err != nil || !ok {
		return ok, err
	}
	_, err = s.dispatch(ctx, id)
	return true, err
}

// SubmitApproved runs the pre-submission re-check of an APPROVED payout and, when ELIGIBLE, moves it to SUBMITTED
// and posts the in-transit journal (Y8). The provider instruction is sent separately (DispatchDue, or SubmitNow):
// the submit job runs both back to back.
func (s *Service) SubmitApproved(ctx context.Context, id string) (bool, error) {
	p, err := s.payout(ctx, s.Pool, id, false)
	if err != nil || p.Status != StApproved {
		return false, err
	}
	sub := p.subject()
	f, _, _, err := s.gather(ctx, PhasePreSubmit, sub)
	if err != nil {
		return false, err
	}
	dec := Evaluate(f)
	submitted := false
	err = s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		cur, err := s.payout(ctx, tx, id, true)
		if err != nil {
			return err
		}
		if cur.Status != StApproved || cur.ApprovalRound != p.ApprovalRound || cur.Version != p.Version {
			return nil // changed while we evaluated: the next run re-evaluates
		}
		did, err := s.writeDecision(ctx, tx, cur.ID, sub, cur.RequestedBy, f, dec)
		if err != nil {
			return err
		}
		switch dec.Outcome {
		case OutcomeEligible:
			submitted = true
			if err := s.move(ctx, tx, &cur, StSubmitted, event{Type: "PAYOUT_SUBMITTED_SANDBOX", Code: "Y8", DecisionID: did},
				`submitted_at = $4, next_poll_at = $5`, s.now(), s.now().Add(pollBase)); err != nil {
				return err
			}
			return audit.Record(ctx, tx, audit.Event{Action: "payout.submitted", ActorType: "system", TargetType: "payout_request", TargetID: cur.ID,
				Metadata: map[string]any{"decision_id": did, "execution_environment": cur.ExecutionEnvironment, "test_payout": cur.TestPayout}})
		case OutcomeDeferred:
			if dec.Result("EC-18").Result == Defer {
				return s.openException(ctx, tx, "PROVIDER_UNAVAILABLE", "SEV3", "ORCHESTRATION", &cur, "", map[string]any{"decision_id": did}, "")
			}
			return nil // e.g. destination cooling-off: retried by the next run
		case OutcomeReview:
			return s.move(ctx, tx, &cur, StReview, event{Type: "APPROVALS_INVALIDATED", Code: "Y7", DecisionID: did,
				Reason: strings.Join(dec.ReviewCodes(), ",")}, `approval_round = approval_round + 1`)
		}
		// REJECTED at submission: some conditions return the payout to review (funds stay reserved), others reject it
		failed := map[string]bool{}
		for _, c := range dec.FailedChecks() {
			failed[c] = true
		}
		switch {
		case failed["EC-08"] || failed["EC-19"]:
			// environment, route or ledger integrity: nothing the requester did; wait, and tell operations
			return s.openException(ctx, tx, "PROVIDER_UNAVAILABLE", "SEV2", "ORCHESTRATION", &cur, "", map[string]any{"decision_id": did,
				"failed_checks": dec.FailedChecks()}, "")
		case failed["EC-22"]:
			if err := s.openException(ctx, tx, "APPROVAL_EXPIRED", "SEV3", "ORCHESTRATION", &cur, "", map[string]any{"decision_id": did}, ""); err != nil {
				return err
			}
			return s.move(ctx, tx, &cur, StReview, event{Type: "APPROVALS_INVALIDATED", Code: "Y7", DecisionID: did, Reason: "EC-22"},
				`approval_round = approval_round + 1`)
		case failed["EC-01"] || failed["EC-12"] || failed["EC-13"] || failed["EC-15"]:
			if err := s.openException(ctx, tx, "COMPLIANCE_HOLD", "SEV2", "ELIGIBILITY", &cur, "", map[string]any{"decision_id": did}, ""); err != nil {
				return err
			}
			return s.move(ctx, tx, &cur, StReview, event{Type: "APPROVALS_INVALIDATED", Code: "Y7", DecisionID: did, Reason: "HOLD"},
				`approval_round = approval_round + 1`)
		case failed["EC-05"] || failed["EC-07"]:
			if err := s.openException(ctx, tx, "DESTINATION_MISMATCH", "SEV2", "ELIGIBILITY", &cur, "", map[string]any{"decision_id": did}, ""); err != nil {
				return err
			}
			return s.move(ctx, tx, &cur, StReview, event{Type: "APPROVALS_INVALIDATED", Code: "Y7", DecisionID: did, Reason: "DESTINATION"},
				`approval_round = approval_round + 1`)
		case failed["EC-09"]:
			if err := s.openException(ctx, tx, "RECONCILIATION_MISMATCH", "SEV1", "ORCHESTRATION", &cur, "", map[string]any{"decision_id": did,
				"detail": dec.Result("EC-09").Detail}, ""); err != nil {
				return err
			}
			return s.move(ctx, tx, &cur, StReview, event{Type: "APPROVALS_INVALIDATED", Code: "Y7", DecisionID: did, Reason: "RESERVATION"},
				`approval_round = approval_round + 1`)
		}
		return s.move(ctx, tx, &cur, StRejected, event{Type: "WITHDRAWAL_REJECTED", Code: "Y4", DecisionID: did,
			Reason: strings.Join(dec.FailedChecks(), ",")}, `decision_reason_code = $4`, dec.ReasonCode)
	})
	if err != nil {
		return false, err
	}
	if err := s.syncLedger(ctx, id); err != nil {
		s.Logger.Warn("payouts: ledger sync deferred to recovery", "payout_request_id", id, "err", err)
		return false, nil // without the in-transit journal no provider call happens (database guard)
	}
	return submitted, nil
}

// DispatchDue sends the provider instruction for SUBMITTED payouts whose in-transit journal has committed and that
// no earlier CREATE_PAYOUT may have reached (first send, or a same-reference retry after DEFINITELY_NOT_SENT or RATE_LIMITED).
func (s *Service) DispatchDue(ctx context.Context, limit int) (int, error) {
	if s.ExecEnv != ExecLocalSimulator {
		return 0, nil
	}
	rows, err := s.Pool.Query(ctx, `SELECT p.id FROM app.payout_requests p JOIN app.payout_reservations r ON r.payout_request_id = p.id
		WHERE p.status = 'SUBMITTED' AND r.ledger_submit_txn_id IS NOT NULL AND (p.next_poll_at IS NULL OR p.next_poll_at <= $1)
		  AND NOT EXISTS (SELECT 1 FROM app.payout_attempts a WHERE a.payout_request_id = p.id AND a.operation = 'CREATE_PAYOUT'
		                   AND a.classification NOT IN ('DEFINITELY_NOT_SENT', 'RATE_LIMITED'))
		  AND NOT EXISTS (SELECT 1 FROM app.payout_exceptions x WHERE x.payout_request_id = p.id
		                   AND x.exception_type = 'SUBMISSION_RETRIES_EXHAUSTED' AND x.status <> 'RESOLVED')
		ORDER BY p.submitted_at LIMIT $2`, s.now(), limit)
	if err != nil {
		return 0, err
	}
	list, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return 0, err
	}
	n := 0
	for _, id := range list {
		if ok, err := s.dispatch(ctx, id); err != nil {
			s.Logger.Warn("payouts: dispatch failed", "payout_request_id", id, "err", err)
		} else if ok {
			n++
		}
	}
	return n, nil
}

func (s *Service) adapterFor(ctx context.Context, p Payout) (psp.PayoutProvider, psp.CallMeta, error) {
	prov, err := s.PSP.ProviderByID(ctx, p.ProviderID)
	if err != nil {
		return nil, psp.CallMeta{}, err
	}
	pp, ok := s.PSP.PayoutAdapter(prov.Code)
	if !ok || !prov.IsSandbox || LiveExecutionEnabled {
		return nil, psp.CallMeta{}, errors.New("payouts: no payout adapter for the bound provider")
	}
	acct, err := s.PSP.AccountByID(ctx, p.ProviderAccountID)
	if err != nil {
		return nil, psp.CallMeta{}, err
	}
	m := psp.CallMeta{CorrelationID: httpx.RequestID(ctx), Deadline: s.now().Add(10 * time.Second), Account: acct}
	if p.SubmittedAt != nil {
		m.ReferenceCreatedAt = *p.SubmittedAt
	}
	return pp, m, nil
}

// dispatch makes ONE CREATE_PAYOUT call: attempt committed IN_FLIGHT first (the database refuses it unless the
// payout is SUBMITTED, the in-transit journal exists and no earlier attempt may have reached the provider), the
// call outside any transaction, then the result.
func (s *Service) dispatch(ctx context.Context, id string) (bool, error) {
	p, err := s.payout(ctx, s.Pool, id, false)
	if err != nil || p.Status != StSubmitted {
		return false, err
	}
	pp, meta, err := s.adapterFor(ctx, p)
	if err != nil {
		return false, err
	}
	// the retry budget is checked before the provider is called (also catches payouts that used it up before the budget
	// existed: they are halted without another attempt)
	budget, _ := s.submitRetry()
	n, err := notSentSinceResume(ctx, s.Pool, p.ID)
	if err != nil {
		return false, err
	}
	if n >= budget {
		return false, s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error { return s.haltSubmission(ctx, tx, &p, n, budget) })
	}
	attemptID, err := s.startAttempt(ctx, p, "CREATE_PAYOUT")
	if err != nil {
		if strings.Contains(err.Error(), "automatic submission") {
			return false, nil // halted by an exhaustion exception another worker opened: nothing to do
		}
		if strings.Contains(err.Error(), "resubmission forbidden") || strings.Contains(err.Error(), "in-transit journal") {
			return false, s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
				if err := s.insertEvent(ctx, tx, p.ID, event{Type: "DUPLICATE_SUBMISSION_BLOCKED", Reason: "database refused CREATE_PAYOUT"}); err != nil {
					return err
				}
				return s.openException(ctx, tx, "DUPLICATE_SUBMISSION_ATTEMPT", "SEV2", "ORCHESTRATION", &p, "", nil, "")
			})
		}
		return false, err
	}
	meta.AttemptID = attemptID
	sc := map[string]string{}
	obs := pp.CreatePayout(ctx, meta, psp.CreatePayoutRequest{PayoutID: p.ID, AmountMinor: p.AmountMinor, Currency: p.Currency,
		Rail: p.DestinationRail, DestinationMasked: p.DestinationMasked, Metadata: sc})
	return true, s.recordObservation(ctx, p.ID, attemptID, "CREATE_PAYOUT", obs, "SYNC")
}

func (s *Service) startAttempt(ctx context.Context, p Payout, op string) (string, error) {
	id := ids.New()
	err := s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var n int
		if err := tx.QueryRow(ctx, `SELECT coalesce(max(attempt_no), 0) + 1 FROM app.payout_attempts WHERE payout_request_id = $1`, p.ID).Scan(&n); err != nil {
			return err
		}
		corr := httpx.RequestID(ctx)
		if corr == "" {
			corr = "payouts:" + id
		}
		_, err := tx.Exec(ctx, `INSERT INTO app.payout_attempts (id, payout_request_id, attempt_no, operation, provider_id, our_reference, correlation_id,
			started_at) VALUES ($1,$2,$3,$4,$5,$2,$6,$7)`, id, p.ID, n, op, p.ProviderID, corr, s.now())
		return err
	})
	return id, err
}

func attemptClass(o psp.Observation) string {
	switch o.Class {
	case psp.ClassAccepted:
		return "ACCEPTED"
	case psp.ClassDefiniteFailure:
		return "DEFINITE_FAILURE"
	case psp.ClassRetryableBeforeSend:
		return "DEFINITELY_NOT_SENT"
	case psp.ClassNotFound:
		return "NOT_FOUND"
	case psp.ClassAuthConfig:
		return "AUTH_CONFIG_ERROR"
	case psp.ClassNotSupported:
		return "NOT_SUPPORTED"
	case psp.ClassRateLimited:
		return "RATE_LIMITED"
	case psp.ClassInvalidRequest:
		return "INVALID_REQUEST"
	}
	return "OUTCOME_UNKNOWN"
}

// recordObservation completes the attempt and applies what the provider said (source SYNC or POLL).
func (s *Service) recordObservation(ctx context.Context, id, attemptID, op string, o psp.Observation, source string) error {
	err := s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		cls := attemptClass(o)
		reason := ""
		if cls == "OUTCOME_UNKNOWN" {
			reason = o.UnknownReason
			if reason == "" {
				reason = psp.UnknownHTTP5xx
			}
		}
		var http any
		if o.HTTPStatus > 0 {
			http = o.HTTPStatus
		}
		if _, err := tx.Exec(ctx, `UPDATE app.payout_attempts SET completed_at = $2, classification = $3, unknown_reason = $4, http_status = $5,
			provider_status_raw = $6, provider_reference_returned = $7, failure_code = $8, latency_ms = $9 WHERE id = $1`,
			attemptID, s.now(), cls, nilIfEmpty(reason), http, nilIfEmpty(o.RawStatus), nilIfEmpty(o.ProviderRef), nilIfEmpty(o.FailureCode),
			int(o.Latency.Milliseconds())); err != nil {
			return err
		}
		p, err := s.payout(ctx, tx, id, true)
		if err != nil {
			return err
		}
		if err := s.insertEvent(ctx, tx, p.ID, event{Type: "PROVIDER_RESPONSE_RECEIVED", Source: source, AttemptID: attemptID, RawStatus: o.RawStatus,
			Reason: cls}); err != nil {
			return err
		}
		if o.ProviderRef != "" && p.ProviderPayoutRef == nil {
			if _, err := tx.Exec(ctx, `UPDATE app.payout_requests SET provider_payout_ref = $2 WHERE id = $1`, p.ID, o.ProviderRef); err != nil {
				return err
			}
		}
		switch o.Class {
		case psp.ClassAccepted:
			return s.applyStatus(ctx, tx, &p, o.Canonical, event{Source: source, AttemptID: attemptID, RawStatus: o.RawStatus}, o)
		case psp.ClassDefiniteFailure:
			if op == "CREATE_PAYOUT" || p.Status == StSubmitted || p.Status == StProcessing || p.Status == StUnknown {
				return s.applyStatus(ctx, tx, &p, psp.PayoutFailed, event{Source: source, AttemptID: attemptID, RawStatus: o.RawStatus}, o)
			}
		case psp.ClassRetryableBeforeSend, psp.ClassRateLimited:
			// nothing reached the provider: the same reference may be retried later (never a new one), within the budget,
			// with jittered exponential backoff; when the budget is spent automatic submission stops (exception)
			budget, b := s.submitRetry()
			notSent, err := notSentSinceResume(ctx, tx, p.ID)
			if err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE app.payout_requests SET next_poll_at = $2 WHERE id = $1`, p.ID, s.now().Add(b.Delay(max(notSent, 1)))); err != nil {
				return err
			}
			if notSent >= budget {
				return s.haltSubmission(ctx, tx, &p, notSent, budget)
			}
		case psp.ClassNotFound:
			if o.NotFoundFinal && (p.Status == StUnknown || p.Status == StSubmitted) {
				// the provider documents that the reference can no longer appear: authoritative final failure
				o.FailureCode = "NOT_FOUND_FINAL"
				return s.applyStatus(ctx, tx, &p, psp.PayoutFailed, event{Source: source, AttemptID: attemptID, RawStatus: "not_found"}, o)
			}
			return s.reschedule(ctx, tx, &p)
		default:
			// timeout, reset after send, 5xx, malformed, or anything not understood: UNKNOWN, never FAILED
			if p.Status == StSubmitted || p.Status == StProcessing {
				return s.toUnknown(ctx, tx, &p, reason, attemptID)
			}
			return s.reschedule(ctx, tx, &p)
		}
		return nil
	})
	if err != nil {
		return err
	}
	return s.syncLedger(ctx, id)
}

func backoff(n int) time.Duration {
	d := pollBase
	for i := 0; i < n && d < pollMax; i++ {
		d *= 2
	}
	if d > pollMax {
		d = pollMax
	}
	return d
}

func (s *Service) reschedule(ctx context.Context, tx pgx.Tx, p *Payout) error {
	_, err := tx.Exec(ctx, `UPDATE app.payout_requests SET next_poll_at = $2, poll_attempts = poll_attempts + 1 WHERE id = $1`,
		p.ID, s.now().Add(backoff(p.PollAttempts+1)))
	return err
}

func (s *Service) toUnknown(ctx context.Context, tx pgx.Tx, p *Payout, reason, attemptID string) error {
	if reason == "" {
		reason = psp.UnknownHTTP5xx
	}
	if err := s.move(ctx, tx, p, StUnknown, event{Type: "PAYOUT_UNKNOWN", Code: "Y12", AttemptID: attemptID, Reason: reason},
		`unknown_since = coalesce(unknown_since, $4), unknown_reason = $5, next_poll_at = $6`, s.now(), reason, s.now().Add(pollBase)); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE app.payout_reservations SET status = 'BLOCKED', version = version + 1 WHERE payout_request_id = $1 AND status = 'ACTIVE'`, p.ID); err != nil {
		return err
	}
	if err := s.insertEvent(ctx, tx, p.ID, event{Type: "RESERVATION_BLOCKED", Reason: "UNKNOWN_OUTCOME"}); err != nil {
		return err
	}
	return s.openException(ctx, tx, "UNKNOWN_PROVIDER_OUTCOME", "SEV2", "ORCHESTRATION", p, "", map[string]any{"unknown_reason": reason}, "")
}

// applyStatus applies a canonical provider status (from SYNC, WEBHOOK or POLL) to the payout: forward-only
// (lower-rank or duplicate observations are recorded, not applied); a completion after FAILED is a SEV1 incident.
func (s *Service) applyStatus(ctx context.Context, tx pgx.Tx, p *Payout, canonical string, e event, o psp.Observation) error {
	notApplied := func(reason string) error {
		e.Type, e.NotApplied, e.To = "PROVIDER_RESPONSE_RECEIVED", reason, ""
		e.Reason = canonical
		return s.insertEvent(ctx, tx, p.ID, e)
	}
	e.ActorType = "PROVIDER"
	switch canonical {
	case psp.PayoutProcessing:
		switch p.Status {
		case StSubmitted, StUnknown:
			code := "Y9"
			if p.Status == StUnknown {
				code = "Y13"
				if _, err := tx.Exec(ctx, `UPDATE app.payout_reservations SET status = 'ACTIVE', version = version + 1 WHERE payout_request_id = $1 AND status = 'BLOCKED'`, p.ID); err != nil {
					return err
				}
			}
			e.Code = code
			return s.move(ctx, tx, p, StProcessing, e, `next_poll_at = $4`, s.now().Add(pollBase))
		case StProcessing:
			return s.reschedule(ctx, tx, p)
		}
		return notApplied("LOWER_RANK")
	case psp.PayoutCompleted, psp.PayoutReturned:
		switch p.Status {
		case StSubmitted, StProcessing, StUnknown:
			e.Code = "Y10"
			if err := s.move(ctx, tx, p, StCompleted, e, `completed_at = $4, next_poll_at = NULL, beneficiary_received_minor = amount_minor`, s.now()); err != nil {
				return err
			}
			if canonical == psp.PayoutReturned {
				return s.reverse(ctx, tx, p, e, o)
			}
			return nil
		case StCompleted:
			if canonical == psp.PayoutReturned {
				return s.reverse(ctx, tx, p, e, o)
			}
			return notApplied("DUPLICATE")
		case StFailed:
			// the provider paid a payout we released as FAILED: the funds may have been paid twice
			if err := notApplied("ANOMALY"); err != nil {
				return err
			}
			return s.openException(ctx, tx, "LATE_COMPLETION_AFTER_FAILURE", "SEV1", "ORCHESTRATION", p, "", map[string]any{"raw_status": e.RawStatus}, "")
		}
		return notApplied("TERMINAL")
	case psp.PayoutFailed:
		switch p.Status {
		case StSubmitted, StProcessing, StUnknown:
			code := o.FailureCode
			if code == "" {
				code = "PROVIDER_FAILED"
			}
			e.Code = "Y11"
			return s.move(ctx, tx, p, StFailed, e, `failure_code = $4, next_poll_at = NULL`, code)
		case StFailed:
			return notApplied("DUPLICATE")
		}
		return notApplied("TERMINAL")
	}
	// unmapped status: never guessed
	if err := notApplied("NOT_AN_EDGE"); err != nil {
		return err
	}
	return s.reschedule(ctx, tx, p)
}

func (s *Service) reverse(ctx context.Context, tx pgx.Tx, p *Payout, e event, o psp.Observation) error {
	amt := p.AmountMinor
	if o.ReportedAmount != nil && *o.ReportedAmount > 0 && *o.ReportedAmount <= p.AmountMinor {
		amt = *o.ReportedAmount
	}
	e.Code = "Y14"
	if err := s.move(ctx, tx, p, StReversed, e, `reversed_amount_minor = $4`, amt); err != nil {
		return err
	}
	return s.openException(ctx, tx, "PAYOUT_RETURNED", "SEV2", "ORCHESTRATION", p, "", map[string]any{"reversed_amount_minor": amt}, "")
}

// ---- polling ------------------------------------------------------------------------------------------------------

// PollDue queries the provider for SUBMITTED (after a send), PROCESSING and UNKNOWN payouts whose poll is due.
func (s *Service) PollDue(ctx context.Context, limit int) (int, error) {
	if s.ExecEnv != ExecLocalSimulator {
		return 0, nil
	}
	rows, err := s.Pool.Query(ctx, `SELECT p.id FROM app.payout_requests p WHERE p.status IN ('PROCESSING','UNKNOWN','SUBMITTED')
		AND p.next_poll_at <= $1 AND (p.status <> 'SUBMITTED' OR EXISTS (SELECT 1 FROM app.payout_attempts a WHERE a.payout_request_id = p.id
		  AND a.operation = 'CREATE_PAYOUT' AND a.classification NOT IN ('DEFINITELY_NOT_SENT','RATE_LIMITED','IN_FLIGHT')))
		ORDER BY p.next_poll_at LIMIT $2`, s.now(), limit)
	if err != nil {
		return 0, err
	}
	list, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return 0, err
	}
	for _, id := range list {
		if err := s.StatusQuery(ctx, id); err != nil {
			s.Logger.Warn("payouts: status query failed", "payout_request_id", id, "err", err)
		}
	}
	return len(list), nil
}

// StatusQuery asks the provider for the payout's status by OUR reference (GET_STATUS attempt) and applies it.
func (s *Service) StatusQuery(ctx context.Context, id string) error {
	p, err := s.payout(ctx, s.Pool, id, false)
	if err != nil {
		return err
	}
	if p.Status != StSubmitted && p.Status != StProcessing && p.Status != StUnknown && p.Status != StCompleted {
		return ErrPayoutState
	}
	pp, meta, err := s.adapterFor(ctx, p)
	if err != nil {
		return err
	}
	attemptID, err := s.startAttempt(ctx, p, "GET_STATUS")
	if err != nil {
		return err
	}
	meta.AttemptID = attemptID
	obs := pp.GetPayout(ctx, meta, p.ID)
	if err := s.recordObservation(ctx, id, attemptID, "GET_STATUS", obs, "POLL"); err != nil {
		return err
	}
	// an UNKNOWN past the horizon needs reconciliation (still never FAILED by assumption)
	return s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		cur, err := s.payout(ctx, tx, id, true)
		if err != nil || cur.Status != StUnknown || cur.NeedsReconciliation || cur.UnknownSince == nil || s.now().Sub(*cur.UnknownSince) < unknownHorizon {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE app.payout_requests SET needs_reconciliation = true, version = version + 1 WHERE id = $1`, id); err != nil {
			return err
		}
		if err := s.insertEvent(ctx, tx, id, event{Type: "RECONCILIATION_REQUIRED", Reason: "UNKNOWN_PAST_HORIZON"}); err != nil {
			return err
		}
		return s.openException(ctx, tx, "RECONCILIATION_MISMATCH", "SEV2", "ORCHESTRATION", &cur, "", map[string]any{"reason": "UNKNOWN_PAST_HORIZON"}, "")
	})
}

// ---- webhooks -----------------------------------------------------------------------------------------------------

// HandleInbox applies a verified, stored PAYOUT webhook (psp.EventHandler). In ONE transaction it applies the event
// and calls exactly one of MarkProcessed or MarkUnrecognised on the inbox.
func (s *Service) HandleInbox(inbox *psp.Service) psp.EventHandler {
	return func(ctx context.Context, ev psp.InboxEvent) error {
		var id string
		err := s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
			if !ids.Valid(ev.OurReference) {
				return inbox.MarkUnrecognised(ctx, tx, ev.ID, "UNKNOWN_PAYOUT_REFERENCE")
			}
			p, err := s.payout(ctx, tx, ev.OurReference, true)
			if errors.Is(err, ErrPayoutNotFound) || (err == nil && p.ProviderID != ev.ProviderID) {
				return inbox.MarkUnrecognised(ctx, tx, ev.ID, "UNKNOWN_PAYOUT_REFERENCE")
			}
			if err != nil {
				return err
			}
			id = p.ID
			if p.TestPayout != ev.Simulated {
				// a simulated webhook can only ever concern a TEST payout and vice versa
				if err := s.insertEvent(ctx, tx, p.ID, event{Type: "PROVIDER_RESPONSE_RECEIVED", Source: "WEBHOOK", NotApplied: "ANOMALY",
					ProviderEventID: ev.ProviderEventID, InboxID: ev.ID, RawStatus: ev.RawStatus, Reason: "SIMULATION_FLAG_MISMATCH"}); err != nil {
					return err
				}
				return inbox.MarkUnrecognised(ctx, tx, ev.ID, "SIMULATION_FLAG_MISMATCH")
			}
			var dup bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM app.payout_events WHERE payout_request_id = $1 AND provider_event_id = $2)`,
				p.ID, ev.ProviderEventID).Scan(&dup); err != nil {
				return err
			}
			e := event{Source: "WEBHOOK", ProviderEventID: ev.ProviderEventID, InboxID: ev.ID, RawStatus: ev.RawStatus, OccurredAt: ev.OccurredAt}
			if dup {
				e.Type, e.NotApplied, e.ProviderEventID = "PROVIDER_RESPONSE_RECEIVED", "DUPLICATE", ""
				e.Reason = "duplicate provider event " + ev.ProviderEventID
				if err := s.insertEvent(ctx, tx, p.ID, e); err != nil {
					return err
				}
				return inbox.MarkProcessed(ctx, tx, ev.ID)
			}
			if ev.ReportedAmount != nil && (*ev.ReportedAmount != p.AmountMinor || ev.ReportedCcy != p.Currency) && !strings.HasSuffix(ev.TypeRaw, ".returned") {
				e.Type, e.NotApplied, e.Reason = "PROVIDER_RESPONSE_RECEIVED", "ANOMALY", "AMOUNT_OR_CURRENCY_MISMATCH"
				if err := s.insertEvent(ctx, tx, p.ID, e); err != nil {
					return err
				}
				if err := s.openException(ctx, tx, "RECONCILIATION_MISMATCH", "SEV1", "WEBHOOK", &p, "", map[string]any{"reason": "AMOUNT_OR_CURRENCY_MISMATCH",
					"provider_event_id": ev.ProviderEventID}, ""); err != nil {
					return err
				}
				return inbox.MarkProcessed(ctx, tx, ev.ID)
			}
			pp, ok := s.PSP.PayoutAdapter(ev.ProviderCode)
			if !ok {
				return inbox.MarkUnrecognised(ctx, tx, ev.ID, "NO_PAYOUT_ADAPTER")
			}
			canonical, _, _ := pp.MapPayoutStatus(ev.RawStatus)
			o := psp.Observation{ReportedAmount: ev.ReportedAmount, ReportedCcy: ev.ReportedCcy, ProviderRef: ev.ProviderReference}
			if ev.ProviderReference != "" && p.ProviderPayoutRef == nil {
				if _, err := tx.Exec(ctx, `UPDATE app.payout_requests SET provider_payout_ref = $2 WHERE id = $1`, p.ID, ev.ProviderReference); err != nil {
					return err
				}
			}
			if err := s.applyStatus(ctx, tx, &p, canonical, e, o); err != nil {
				return err
			}
			return inbox.MarkProcessed(ctx, tx, ev.ID)
		})
		if err != nil || id == "" {
			return err
		}
		return s.syncLedger(ctx, id)
	}
}

// ---- ledger -------------------------------------------------------------------------------------------------------

// syncLedger posts the journals the payout's state requires that are not yet linked, in order, and links them
// (idempotent keys; safe to re-run). The state is the source of truth; the ledger follows it.
func (s *Service) syncLedger(ctx context.Context, id string) error {
	p, err := s.payout(ctx, s.Pool, id, false)
	if err != nil {
		return err
	}
	r := p.Reservation
	if r == nil {
		return nil // PAYOUT_REQUESTED (reserve handles it) or rejected before reservation
	}
	post := func(rule, step string, lines ...ledger.Line) (string, error) {
		res, err := s.Ledger.PostOnce(ctx, s.ledgerPosting(p, rule, step, sysActor, lines...))
		if err != nil {
			return "", err
		}
		return res.TransactionID, nil
	}
	link := func(col, txID, newStatus, evType string) error {
		return s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
			q := `UPDATE app.payout_reservations SET ` + col + ` = $2, version = version + 1`
			args := []any{r.ID, txID}
			if newStatus != "" {
				q += `, status = $3`
				args = append(args, newStatus)
			}
			if _, err := tx.Exec(ctx, q+` WHERE id = $1 AND `+col+` IS NULL`, args...); err != nil {
				return err
			}
			return s.insertEvent(ctx, tx, p.ID, event{Type: evType, LedgerTxIDs: []string{txID}})
		})
	}
	submittedOnce := p.SubmittedAt != nil
	if submittedOnce && r.LedgerSubmitTxnID == nil {
		txID, err := post("PAYOUT_SUBMITTED", "submitted", ledger.Dr(pending(p), p.AmountMinor), ledger.Cr(inTransit(p), p.AmountMinor))
		if err != nil {
			return s.ledgerFailure(ctx, p, err)
		}
		if err := link("ledger_submit_txn_id", txID, "", "LEDGER_POSTED"); err != nil {
			return err
		}
		r.LedgerSubmitTxnID = &txID
	}
	switch p.Status {
	case StCompleted, StReversed:
		if r.LedgerConsumeTxnID == nil {
			txID, err := post("PAYOUT_COMPLETED", "completed", ledger.Dr(inTransit(p), p.AmountMinor), ledger.Cr(settled(p), p.AmountMinor))
			if err != nil {
				return s.ledgerFailure(ctx, p, err)
			}
			if err := link("ledger_consume_txn_id", txID, ResConsumed, "RESERVATION_CONSUMED"); err != nil {
				return err
			}
		}
		// Stage 12: assess the payout fee from its request-time estimate (idempotent; a platform-absorbed fee is
		// posted as a PAYOUT_FEE expense journal, retried by the fees job if the ledger refuses it now)
		if s.Fees != nil && p.FeeCalculationID != nil {
			if err := s.Fees.AssessPayoutFee(ctx, p.ID, p.CampaignID, p.Currency, p.ProviderID, p.DestinationRail, *p.FeeCalculationID,
				p.AmountMinor); err != nil {
				return err
			}
		}
		if p.Status == StReversed && p.ReversedAmountMinor != nil {
			if j, ok, err := s.Ledger.JournalByKey(ctx, "payout:"+p.ID+":reversed"); err != nil {
				return err
			} else if !ok || j.TransactionID == "" {
				amt := *p.ReversedAmountMinor
				txID, err := post("PAYOUT_REVERSED", "reversed", ledger.Dr(settled(p), amt), ledger.Cr(payable(p), amt))
				if err != nil {
					return s.ledgerFailure(ctx, p, err)
				}
				return s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
					return s.insertEvent(ctx, tx, p.ID, event{Type: "LEDGER_POSTED", Reason: "PAYOUT_REVERSED", LedgerTxIDs: []string{txID}})
				})
			}
		}
	case StFailed:
		if r.LedgerReleaseTxnID == nil && r.LedgerSubmitTxnID != nil {
			txID, err := post("PAYOUT_FAILED", "failed", ledger.Dr(inTransit(p), p.AmountMinor), ledger.Cr(payable(p), p.AmountMinor))
			if err != nil {
				return s.ledgerFailure(ctx, p, err)
			}
			return link("ledger_release_txn_id", txID, ResReleased, "RESERVATION_RELEASED")
		}
	case StRejected, StCancelled:
		if r.LedgerReleaseTxnID == nil && r.LedgerSubmitTxnID == nil {
			txID, err := post("PAYOUT_RELEASED", "released", ledger.Dr(pending(p), p.AmountMinor), ledger.Cr(payable(p), p.AmountMinor))
			if err != nil {
				return s.ledgerFailure(ctx, p, err)
			}
			st, ev := ResReleased, "RESERVATION_RELEASED"
			if p.DecisionReasonCode != nil && *p.DecisionReasonCode == "REQUEST_EXPIRED" {
				st, ev = ResExpired, "RESERVATION_EXPIRED"
			}
			return link("ledger_release_txn_id", txID, st, ev)
		}
	}
	return nil
}

func (s *Service) ledgerFailure(ctx context.Context, p Payout, cause error) error {
	code := "LEDGER_ERROR"
	if e := errs.As(cause); e != nil {
		code = e.Code
	}
	_ = s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return s.openException(ctx, tx, "LEDGER_POSTING_FAILED", "SEV2", "LEDGER", &p, "", map[string]any{"error_code": code}, "")
	})
	return cause
}

// ---- recovery -----------------------------------------------------------------------------------------------------

// Recover re-drives what a crash may have left behind (job): reservations not yet posted, crashed provider calls
// (IN_FLIGHT attempts of a dead worker become OUTCOME_UNKNOWN: the payout is UNKNOWN, never resent), journals the
// state requires, and request expiry where a validity limit is configured.
func (s *Service) Recover(ctx context.Context) error {
	now := s.now()
	ids1, err := s.idList(ctx, `SELECT id FROM app.payout_requests WHERE status = 'PAYOUT_REQUESTED' AND requested_at < $1 LIMIT 100`, now.Add(-30*time.Second))
	if err != nil {
		return err
	}
	for _, id := range ids1 {
		if _, err := s.reserve(ctx, id); err != nil {
			s.Logger.Warn("payouts: recover reservation", "payout_request_id", id, "err", err)
		}
	}
	crashed, err := s.idList(ctx, `SELECT id FROM app.payout_attempts WHERE classification = 'IN_FLIGHT' AND started_at < $1 LIMIT 100`, now.Add(-crashedAttemptAge))
	if err != nil {
		return err
	}
	for _, aid := range crashed {
		var pid, op string
		if err := s.Pool.QueryRow(ctx, `SELECT payout_request_id, operation FROM app.payout_attempts WHERE id = $1`, aid).Scan(&pid, &op); err != nil {
			return err
		}
		obs := psp.Observation{Class: psp.ClassIndeterminate, UnknownReason: psp.UnknownCrashDuring}
		if err := s.recordObservation(ctx, pid, aid, op, obs, "SYNC"); err != nil {
			s.Logger.Warn("payouts: recover crashed attempt", "attempt_id", aid, "err", err)
		}
	}
	need, err := s.idList(ctx, `SELECT p.id FROM app.payout_requests p JOIN app.payout_reservations r ON r.payout_request_id = p.id
		WHERE (p.submitted_at IS NOT NULL AND r.ledger_submit_txn_id IS NULL)
		   OR (p.status IN ('COMPLETED','REVERSED') AND r.ledger_consume_txn_id IS NULL)
		   OR (p.status IN ('FAILED','REJECTED','CANCELLED') AND r.ledger_release_txn_id IS NULL)
		   OR p.status = 'REVERSED' LIMIT 200`)
	if err != nil {
		return err
	}
	for _, id := range need {
		if err := s.syncLedger(ctx, id); err != nil {
			s.Logger.Warn("payouts: recover ledger", "payout_request_id", id, "err", err)
		}
	}
	expired, err := s.idList(ctx, `SELECT p.id FROM app.payout_requests p JOIN app.payout_reservations r ON r.payout_request_id = p.id
		WHERE p.status IN ('PENDING_REVIEW','APPROVED') AND r.expires_at IS NOT NULL AND r.expires_at <= $1 LIMIT 100`, now)
	if err != nil {
		return err
	}
	for _, id := range expired {
		if _, err := s.cancel(ctx, id, event{Type: "WITHDRAWAL_CANCELLED", Code: "Y5", Reason: "REQUEST_EXPIRED"}, "REQUEST_EXPIRED"); err != nil &&
			!errors.Is(err, ErrPayoutState) {
			s.Logger.Warn("payouts: expire request", "payout_request_id", id, "err", err)
		}
	}
	return nil
}

func (s *Service) idList(ctx context.Context, q string, args ...any) ([]string, error) {
	rows, err := s.Pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (string, error) {
		var v string
		err := r.Scan(&v)
		return v, err
	})
}
