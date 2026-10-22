package reconciliation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/Fatifizo/fundzim/internal/audit"
	"github.com/Fatifizo/fundzim/internal/ledger"
	"github.com/Fatifizo/fundzim/internal/payments"
	"github.com/Fatifizo/fundzim/internal/payouts"
	"github.com/Fatifizo/fundzim/internal/platform/errs"
	"github.com/Fatifizo/fundzim/internal/platform/httpx"
	"github.com/Fatifizo/fundzim/internal/platform/ids"
	"github.com/Fatifizo/fundzim/internal/platform/outbox"
	"github.com/Fatifizo/fundzim/internal/psp"
)

// Resolutions (docs/stage-17/approvals.md, adjustments.md). Proposed by one FINANCE member (maker) with a justification
// and evidence references, approved by another (checker: never the maker or a linked account — Go, CHECK and trigger —
// with step-up) before expiry and against the exact proposal hash; approving executes, idempotently. Money only moves
// through the owning module: a payment or payout outcome through its state machine (source RECON), a ledger correction
// through the ledger's own maker-checker adjustment with a named rule.

// ResolutionTTL is how long a proposal waits for a checker (operational-controls §3: 24 h, as ledger adjustments).
const ResolutionTTL = 24 * time.Hour

// Resolution types.
const (
	ResExplained        = "EXPLAINED_NO_ACTION"
	ResProviderCorrect  = "PROVIDER_CORRECTION_CONFIRMED"
	ResEscalated        = "ESCALATED_TO_CASE"
	ResManualMatch      = "MANUAL_MATCH"
	ResOutcomeApplied   = "PROVIDER_OUTCOME_APPLIED"
	ResLedgerAdjustment = "LEDGER_ADJUSTMENT"
)

// adjustmentRules are the named ledger rules a reconciliation adjustment may use (ADR-049 §6).
var adjustmentRules = set("SETTLEMENT_DISCREPANCY_RESOLVED", "ADJUSTMENT", "WRITE_OFF")

// manualMatchTypes are the exception types a manual match can resolve.
var manualMatchTypes = set("AMBIGUOUS_MATCH", "MISSING_PAYMENT", "MISSING_PAYOUT", "UNMATCHED_BANK_ENTRY")

// Resolution is a resolution as staff see it.
type Resolution struct {
	ID                 string         `json:"id"`
	ExceptionID        string         `json:"exception_id"`
	Type               string         `json:"resolution_type"`
	Status             string         `json:"status"`
	Proposal           map[string]any `json:"proposal"`
	ProposalSHA256     string         `json:"proposal_sha256"`
	Justification      string         `json:"justification"`
	EvidenceRefs       []string       `json:"evidence_refs"`
	ProposedBy         string         `json:"proposed_by"`
	ProposedAt         time.Time      `json:"proposed_at"`
	ExpiresAt          time.Time      `json:"expires_at"`
	DecidedBy          *string        `json:"decided_by"`
	DecidedAt          *time.Time     `json:"decided_at"`
	DecisionNote       *string        `json:"decision_note"`
	LedgerAdjustmentID *string        `json:"ledger_adjustment_id"`
	ExecutedAt         *time.Time     `json:"executed_at"`
	ExecutionResult    map[string]any `json:"execution_result"`
	LastError          *string        `json:"last_error"`
	Version            int            `json:"version"`
}

// ProposalInput is POST /admin/reconciliation/exceptions/{id}/resolutions.
type ProposalInput struct {
	Type          string                `json:"resolution_type"`
	Justification string                `json:"justification"`
	EvidenceRefs  []string              `json:"evidence_refs"`
	Version       int                   `json:"version"`
	SubjectType   string                `json:"subject_type"` // MANUAL_MATCH
	SubjectID     string                `json:"subject_id"`   // MANUAL_MATCH
	CaseRef       string                `json:"case_ref"`     // ESCALATED_TO_CASE
	PostingRule   string                `json:"posting_rule"` // LEDGER_ADJUSTMENT
	Lines         []ledger.ProposedLine `json:"lines"`        // LEDGER_ADJUSTMENT
}

const resCols = `id::text, exception_id::text, resolution_type, status, proposal, proposal_sha256, justification, evidence_refs, proposed_by::text,
	proposed_at, expires_at, decided_by::text, decided_at, decision_note, ledger_adjustment_id::text, executed_at, execution_result, last_error, version`

func scanResolution(r pgx.Row) (Resolution, error) {
	var x Resolution
	var prop, result, sum []byte
	err := r.Scan(&x.ID, &x.ExceptionID, &x.Type, &x.Status, &prop, &sum, &x.Justification, &x.EvidenceRefs, &x.ProposedBy, &x.ProposedAt,
		&x.ExpiresAt, &x.DecidedBy, &x.DecidedAt, &x.DecisionNote, &x.LedgerAdjustmentID, &x.ExecutedAt, &result, &x.LastError, &x.Version)
	if err != nil {
		return x, err
	}
	x.Proposal = map[string]any{}
	_ = json.Unmarshal(prop, &x.Proposal)
	if result != nil {
		_ = json.Unmarshal(result, &x.ExecutionResult)
	}
	x.ProposalSHA256 = hex.EncodeToString(sum)
	return x, nil
}

func (s *Service) resolutionsFor(ctx context.Context, excID string) ([]Resolution, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+resCols+` FROM recon.resolutions WHERE exception_id = $1 ORDER BY proposed_at, id`, excID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Resolution, error) { return scanResolution(r) })
}

// ResolutionByID returns one resolution.
func (s *Service) ResolutionByID(ctx context.Context, id string) (Resolution, error) {
	if !ids.Valid(id) {
		return Resolution{}, ErrNotFound
	}
	x, err := scanResolution(s.Pool.QueryRow(ctx, `SELECT `+resCols+` FROM recon.resolutions WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Resolution{}, ErrNotFound
	}
	return x, err
}

// PendingResolutions lists proposals waiting for a checker (oldest first).
func (s *Service) PendingResolutions(ctx context.Context, limit int) ([]Resolution, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	rows, err := s.Pool.Query(ctx, `SELECT `+resCols+` FROM recon.resolutions WHERE status IN ('PENDING_REVIEW','APPROVED') ORDER BY proposed_at, id LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Resolution, error) { return scanResolution(r) })
}

// ProposalHash is the SHA-256 of the canonical proposal the checker approves.
func ProposalHash(excID, typ, justification string, evidence []string, proposal map[string]any) [32]byte {
	raw, _ := json.Marshal(map[string]any{"exception_id": excID, "type": typ, "justification": justification, "evidence_refs": evidence,
		"proposal": proposal})
	return sha256.Sum256(raw)
}

// Propose files a resolution (maker) for an exception under investigation.
func (s *Service) Propose(ctx context.Context, staffID, excID string, in ProposalInput) (Resolution, error) {
	exc, err := s.ExceptionByID(ctx, excID)
	if err != nil {
		return Resolution{}, err
	}
	just, err := cleanNote(in.Justification, 20, 2000)
	if err != nil {
		return Resolution{}, httpx.Validation(errs.Detail{Field: "justification", Code: "INVALID_LENGTH"})
	}
	if len(in.EvidenceRefs) < 1 || len(in.EvidenceRefs) > 10 {
		return Resolution{}, httpx.Validation(errs.Detail{Field: "evidence_refs", Code: "ONE_TO_TEN_REQUIRED"})
	}
	for _, r := range in.EvidenceRefs {
		if !evidenceRefRE.MatchString(r) {
			return Resolution{}, httpx.Validation(errs.Detail{Field: "evidence_refs", Code: "INVALID_FORMAT"})
		}
	}
	if exc.Status != "INVESTIGATING" {
		return Resolution{}, ErrState
	}
	proposal, err := s.buildProposal(ctx, exc, in)
	if err != nil {
		return Resolution{}, err
	}
	var adjID string
	if in.Type == ResLedgerAdjustment {
		env, err := s.Ledger.Environment(ctx)
		if err != nil {
			return Resolution{}, err
		}
		if env != "TEST" && !ProductionAdjustmentsEnabled {
			return Resolution{}, ErrAdjustmentsOff
		}
		adj, err := s.Ledger.RequestAdjustment(ctx, staffID, ledger.AdjustmentRequest{Kind: "MANUAL", PostingRule: in.PostingRule, Currency: exc.Currency,
			Lines: in.Lines, ReasonCode: "RECON_DISCREPANCY", Reason: just, LinkedType: "reconciliation_exception", LinkedID: exc.ID})
		if err != nil {
			return Resolution{}, err
		}
		adjID = adj.ID
		proposal["ledger_adjustment_id"] = adj.ID
	}
	sum := ProposalHash(exc.ID, in.Type, just, in.EvidenceRefs, proposal)
	propRaw, _ := json.Marshal(proposal)
	id := ids.New()
	now := s.now()
	err = s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var status string
		var version int
		if err := tx.QueryRow(ctx, `SELECT status, version FROM recon.exceptions WHERE id = $1 FOR UPDATE`, exc.ID).Scan(&status, &version); err != nil {
			return err
		}
		if status != "INVESTIGATING" {
			return ErrState
		}
		if in.Version != 0 && in.Version != version {
			return ErrStale
		}
		_, err := tx.Exec(ctx, `INSERT INTO recon.resolutions (id, exception_id, resolution_type, status, proposal, proposal_sha256, justification,
			evidence_refs, proposed_by, proposed_at, expires_at, ledger_adjustment_id) VALUES ($1, $2, $3, 'PENDING_REVIEW', $4, $5, $6, $7, $8, $9, $10, $11)`,
			id, exc.ID, in.Type, propRaw, sum[:], just, in.EvidenceRefs, staffID, now, now.Add(ResolutionTTL), nullStr(adjID))
		var pg *pgconn.PgError
		if errors.As(err, &pg) && pg.Code == "23505" {
			return errs.New(errs.Conflict, "RESOLUTION_PENDING", "This exception already has a resolution waiting for a decision.")
		}
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE recon.exceptions SET status = 'PENDING_APPROVAL', version = version + 1 WHERE id = $1`, exc.ID); err != nil {
			return err
		}
		if err := s.addEvent(ctx, tx, exc.ID, "RESOLUTION_PROPOSED", "INVESTIGATING", "PENDING_APPROVAL", staffID, "", id, just,
			in.EvidenceRefs[0]); err != nil {
			return err
		}
		if err := audit.Record(ctx, tx, audit.Event{Action: "reconciliation.resolution.proposed", ActorType: "staff", ActorID: staffID,
			TargetType: "reconciliation_resolution", TargetID: id, Reason: just, Metadata: map[string]any{"exception_id": exc.ID,
				"resolution_type": in.Type, "proposal_sha256": hex.EncodeToString(sum[:]), "ledger_adjustment_id": adjID}}); err != nil {
			return err
		}
		_, err = outbox.Write(ctx, tx, outbox.Event{AggregateType: "reconciliation_resolution", AggregateID: id, EventType: EventResolutionPending,
			OccurredAt: now, Payload: map[string]any{"resolution_id": id, "exception_id": exc.ID, "resolution_type": in.Type, "actor_id": staffID}})
		return err
	})
	if err != nil {
		if adjID != "" {
			if _, werr := s.Ledger.WithdrawAdjustment(ctx, staffID, adjID, "reconciliation proposal was not recorded"); werr != nil {
				s.Logger.WarnContext(ctx, "reconciliation: orphan ledger adjustment request (it expires in 24 h)", "adjustment_id", adjID, "err", werr)
			}
		}
		return Resolution{}, err
	}
	return s.ResolutionByID(ctx, id)
}

// buildProposal validates the type-specific part and returns the canonical proposal.
func (s *Service) buildProposal(ctx context.Context, exc Exception, in ProposalInput) (map[string]any, error) {
	p := map[string]any{"exception_type": exc.Type, "currency": exc.Currency}
	switch in.Type {
	case ResExplained, ResProviderCorrect:
	case ResEscalated:
		if in.CaseRef == "" || len(in.CaseRef) > 100 {
			return nil, httpx.Validation(errs.Detail{Field: "case_ref", Code: "REQUIRED"})
		}
		p["case_ref"] = in.CaseRef
	case ResManualMatch:
		if !manualMatchTypes[exc.Type] || exc.Item == nil {
			return nil, httpx.Validation(errs.Detail{Field: "resolution_type", Code: "NOT_FOR_THIS_EXCEPTION"})
		}
		if !ids.Valid(in.SubjectID) {
			return nil, httpx.Validation(errs.Detail{Field: "subject_id", Code: "INVALID_FORMAT"})
		}
		amount, ccy, err := s.subjectAmount(ctx, exc, in.SubjectType, in.SubjectID)
		if err != nil {
			return nil, err
		}
		if ccy != exc.Item.Currency || amount != exc.Item.AmountMinor {
			// a manual match links equal evidence only; a difference needs an explanation or a ledger adjustment
			return nil, httpx.Validation(errs.Detail{Field: "subject_id", Code: "AMOUNT_OR_CURRENCY_DIFFERS"})
		}
		p["item_id"], p["subject_type"], p["subject_id"], p["amount_minor"] = exc.Item.ID, in.SubjectType, in.SubjectID, amount
	case ResOutcomeApplied:
		if (exc.Type != "UNKNOWN_PAYMENT_OUTCOME" && exc.Type != "UNKNOWN_PAYOUT_OUTCOME") || exc.Item == nil || exc.SubjectID == nil {
			return nil, httpx.Validation(errs.Detail{Field: "resolution_type", Code: "NOT_FOR_THIS_EXCEPTION"})
		}
		target := map[string]string{"SUCCEEDED": psp.StatusSucceeded, "COMPLETED": psp.PayoutCompleted, "FAILED": "FAILED"}[exc.Item.Status]
		if target == "" {
			return nil, httpx.Validation(errs.Detail{Field: "resolution_type", Code: "LINE_IS_NOT_FINAL"})
		}
		if exc.Type == "UNKNOWN_PAYOUT_OUTCOME" && exc.Item.Status == "FAILED" {
			target = psp.PayoutFailed
		}
		p["item_id"], p["subject_type"], p["subject_id"], p["target"] = exc.Item.ID, *exc.SubjectType, *exc.SubjectID, target
		p["amount_minor"], p["line_currency"], p["evidence_key"] = exc.Item.AmountMinor, exc.Item.Currency, "recon:item:"+exc.Item.ID
		if exc.Item.ProviderRef != nil {
			p["provider_ref"] = *exc.Item.ProviderRef
		}
	case ResLedgerAdjustment:
		if !adjustmentRules[in.PostingRule] {
			return nil, httpx.Validation(errs.Detail{Field: "posting_rule", Code: "NOT_A_RECONCILIATION_RULE"})
		}
		if len(in.Lines) < 2 || len(in.Lines) > 20 {
			return nil, httpx.Validation(errs.Detail{Field: "lines", Code: "TWO_TO_TWENTY_REQUIRED"})
		}
		p["posting_rule"], p["lines"] = in.PostingRule, in.Lines
	default:
		return nil, httpx.Validation(errs.Detail{Field: "resolution_type", Code: "INVALID_VALUE"})
	}
	return p, nil
}

func (s *Service) subjectAmount(ctx context.Context, exc Exception, subjectType, subjectID string) (string, string, error) {
	src, err := s.sourceByCode(ctx, exc.SourceCode)
	if err != nil {
		return "", "", err
	}
	switch subjectType {
	case SubjPayment, SubjPayout:
		if src.ProviderCode == nil {
			return "", "", httpx.Validation(errs.Detail{Field: "subject_type", Code: "NOT_FOR_THIS_SOURCE"})
		}
		if subjectType == SubjPayment {
			f, err := s.Payments.ReconFacts(ctx, *src.ProviderCode, []string{subjectID}, nil)
			if err != nil || len(f) != 1 {
				return "", "", errors.Join(err, httpx.Validation(errs.Detail{Field: "subject_id", Code: "NOT_FOUND_FOR_PROVIDER"}))
			}
			return fmt.Sprint(f[0].AmountMinor), f[0].Currency, nil
		}
		f, err := s.Payouts.ReconFacts(ctx, *src.ProviderCode, []string{subjectID}, nil)
		if err != nil || len(f) != 1 {
			return "", "", errors.Join(err, httpx.Validation(errs.Detail{Field: "subject_id", Code: "NOT_FOUND_FOR_PROVIDER"}))
		}
		return fmt.Sprint(f[0].AmountMinor), f[0].Currency, nil
	case SubjRemittance:
		it, err := s.ItemByID(ctx, subjectID)
		if err != nil || it.Kind != "FEE_REMITTANCE" {
			return "", "", httpx.Validation(errs.Detail{Field: "subject_id", Code: "NOT_A_REMITTANCE_LINE"})
		}
		return it.AmountMinor, it.Currency, nil
	}
	return "", "", httpx.Validation(errs.Detail{Field: "subject_type", Code: "INVALID_VALUE"})
}

// DecideInput is the checker's (or the maker's, for cancel) decision.
type DecideInput struct {
	Note           string `json:"note"`
	ProposalSHA256 string `json:"proposal_sha256"`
}

// Decide: approve (checker; executes), reject (checker), cancel (the proposer).
func (s *Service) Decide(ctx context.Context, staffID, resID, action string, in DecideInput) (Resolution, error) {
	note, err := cleanNote(in.Note, 5, 2000)
	if err != nil {
		return Resolution{}, err
	}
	idents, err := s.identities(ctx, staffID)
	if err != nil {
		return Resolution{}, err
	}
	res, err := s.ResolutionByID(ctx, resID)
	if err != nil {
		return Resolution{}, err
	}
	now := s.now()
	var expired bool
	err = s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var status, proposer, excID string
		var sum []byte
		var expires time.Time
		if err := tx.QueryRow(ctx, `SELECT status, proposed_by::text, exception_id::text, proposal_sha256, expires_at FROM recon.resolutions
			WHERE id = $1 FOR UPDATE`, resID).Scan(&status, &proposer, &excID, &sum, &expires); err != nil {
			return err
		}
		if status != "PENDING_REVIEW" {
			return ErrState
		}
		if !now.Before(expires) {
			expired = true
			return s.endProposal(ctx, tx, resID, excID, "EXPIRED", "", "proposal expired before a decision")
		}
		switch action {
		case "approve", "reject":
			if idents[proposer] {
				return ErrSelf
			}
			if action == "approve" && in.ProposalSHA256 != hex.EncodeToString(sum) {
				return ErrHashMismatch
			}
		case "cancel":
			if proposer != staffID {
				return errs.New(errs.Forbidden, "NOT_PROPOSER", "Only the proposer can cancel a proposal.")
			}
		default:
			return ErrNotFound
		}
		switch action {
		case "approve":
			if _, err := tx.Exec(ctx, `UPDATE recon.resolutions SET status = 'APPROVED', decided_by = $2, decided_at = $3, decision_note = $4,
				approved_sha256 = proposal_sha256, version = version + 1 WHERE id = $1`, resID, staffID, now, note); err != nil {
				return err
			}
			if err := s.addEvent(ctx, tx, excID, "RESOLUTION_APPROVED", "", "", staffID, "", resID, note, ""); err != nil {
				return err
			}
		case "reject":
			if _, err := tx.Exec(ctx, `UPDATE recon.resolutions SET status = 'REJECTED', decided_by = $2, decided_at = $3, decision_note = $4,
				version = version + 1 WHERE id = $1`, resID, staffID, now, note); err != nil {
				return err
			}
			if err := s.reopenInvestigation(ctx, tx, excID, staffID, resID, "RESOLUTION_REJECTED", note); err != nil {
				return err
			}
		case "cancel":
			if err := s.endProposal(ctx, tx, resID, excID, "CANCELLED", staffID, note); err != nil {
				return err
			}
		}
		return audit.Record(ctx, tx, audit.Event{Action: "reconciliation.resolution." + action, ActorType: "staff", ActorID: staffID,
			TargetType: "reconciliation_resolution", TargetID: resID, Reason: note, Metadata: map[string]any{"exception_id": excID,
				"resolution_type": res.Type}})
	})
	if err != nil {
		return Resolution{}, err
	}
	if expired {
		return Resolution{}, ErrExpired
	}
	if res.LedgerAdjustmentID != nil && action != "approve" {
		var lerr error
		if action == "reject" {
			_, lerr = s.Ledger.RejectAdjustment(ctx, staffID, *res.LedgerAdjustmentID, note)
		} else {
			_, lerr = s.Ledger.WithdrawAdjustment(ctx, staffID, *res.LedgerAdjustmentID, note)
		}
		if lerr != nil {
			s.Logger.WarnContext(ctx, "reconciliation: ledger adjustment not ended (it expires in 24 h)", "adjustment_id", *res.LedgerAdjustmentID, "err", lerr)
		}
	}
	if action == "approve" {
		if err := s.execute(ctx, resID, staffID, note); err != nil {
			return Resolution{}, err
		}
	}
	return s.ResolutionByID(ctx, resID)
}

func (s *Service) endProposal(ctx context.Context, tx pgx.Tx, resID, excID, status, staffID, note string) error {
	if _, err := tx.Exec(ctx, `UPDATE recon.resolutions SET status = $2, version = version + 1 WHERE id = $1`, resID, status); err != nil {
		return err
	}
	return s.reopenInvestigation(ctx, tx, excID, staffID, resID, "RESOLUTION_"+map[string]string{"EXPIRED": "EXPIRED", "CANCELLED": "WITHDRAWN"}[status], note)
}

func (s *Service) reopenInvestigation(ctx context.Context, tx pgx.Tx, excID, staffID, resID, event, note string) error {
	if _, err := tx.Exec(ctx, `UPDATE recon.exceptions SET status = 'INVESTIGATING', version = version + 1 WHERE id = $1 AND status = 'PENDING_APPROVAL'`,
		excID); err != nil {
		return err
	}
	return s.addEvent(ctx, tx, excID, event, "PENDING_APPROVAL", "INVESTIGATING", staffID, "", resID, note, "")
}

// execute performs an APPROVED resolution's effect, then marks it EXECUTED and resolves the exception. Idempotent: every
// effect is keyed (payment/payout evidence key, ledger adjustment id, unique active match), so a retry after a crash
// cannot apply anything twice.
func (s *Service) execute(ctx context.Context, resID, checkerID, note string) error {
	res, err := s.ResolutionByID(ctx, resID)
	if err != nil {
		return err
	}
	if res.Status != "APPROVED" {
		return nil
	}
	exc, err := s.ExceptionByID(ctx, res.ExceptionID)
	if err != nil {
		return err
	}
	result := map[string]any{}
	resolved := true
	str := func(k string) string { v, _ := res.Proposal[k].(string); return v }
	switch res.Type {
	case ResOutcomeApplied:
		amount, _ := res.Proposal["amount_minor"].(string)
		var amt int64
		_, _ = fmt.Sscan(amount, &amt)
		evidence := ""
		if exc.Item != nil {
			if imp, err := s.ImportByID(ctx, exc.Item.ImportID); err == nil {
				evidence = imp.EvidenceRecordID
			}
		}
		at := s.now()
		if exc.Item != nil {
			at = exc.Item.BookedAt
		}
		reason := "reconciliation resolution " + res.ID
		if str("subject_type") == SubjPayment {
			out, err := s.Payments.ApplyReconciliationOutcome(ctx, payments.ReconOutcome{PaymentID: str("subject_id"), Target: str("target"),
				ProviderRef: str("provider_ref"), AmountMinor: amt, Currency: str("line_currency"), EvidenceKey: str("evidence_key"), OccurredAt: at,
				Reason: reason})
			if err != nil {
				return s.executionFailed(ctx, res.ID, err)
			}
			result["applied"], result["from"], result["to"], result["not_applied"] = out.Applied, out.From, out.To, out.NotApplied
			resolved = out.Applied
		} else {
			applied, err := s.Payouts.ApplyReconciliationOutcome(ctx, payouts.ReconOutcome{PayoutID: str("subject_id"), Target: str("target"),
				ProviderRef: str("provider_ref"), AmountMinor: amt, Currency: str("line_currency"), EvidenceKey: str("evidence_key"),
				EvidenceRecordID: evidence, OccurredAt: at, Reason: reason})
			if err != nil {
				return s.executionFailed(ctx, res.ID, err)
			}
			result["applied"] = applied
			resolved = applied
		}
	case ResLedgerAdjustment:
		adj, err := s.Ledger.AdjustmentByID(ctx, *res.LedgerAdjustmentID)
		if err != nil {
			return s.executionFailed(ctx, res.ID, err)
		}
		if adj.Status == "PENDING_APPROVAL" {
			if adj, err = s.Ledger.ApproveAdjustment(ctx, checkerID, adj.ID, note); err != nil {
				return s.executionFailed(ctx, res.ID, err)
			}
		}
		if adj.Status != "POSTED" {
			return s.executionFailed(ctx, res.ID, fmt.Errorf("ledger adjustment is %s", adj.Status))
		}
		result["ledger_adjustment_id"], result["resulting_transaction_id"] = adj.ID, adj.ResultingTx
	}
	raw, _ := json.Marshal(result)
	return s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var status string
		if err := tx.QueryRow(ctx, `SELECT status FROM recon.resolutions WHERE id = $1 FOR UPDATE`, res.ID).Scan(&status); err != nil {
			return err
		}
		if status != "APPROVED" {
			return nil
		}
		if res.Type == ResManualMatch {
			expl, _ := json.Marshal(map[string]any{"manual": true, "resolution_id": res.ID, "maker": res.ProposedBy, "checker": res.DecidedBy})
			amt := res.Proposal["amount_minor"]
			if _, err := tx.Exec(ctx, `INSERT INTO recon.matches (id, run_id, item_id, currency, subject_type, subject_id, confidence, matcher_version,
				rule_code, matched_amount_minor, ledger_status, fee_status, explanation, resolution_id)
				SELECT $1, NULL, x.id, x.currency, $3, $4, 'MANUAL', $5, 'MANUAL_MATCH', $6::bigint, 'NOT_APPLICABLE', 'NOT_APPLICABLE', $7, $8
				FROM recon.items x WHERE x.id = $2 ON CONFLICT (item_id) WHERE status = 'ACTIVE' DO NOTHING`,
				ids.New(), str("item_id"), str("subject_type"), str("subject_id"), MatcherVersion, fmt.Sprint(amt), expl, res.ID); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE recon.items SET match_status = 'MATCHED' WHERE id = $1 AND match_status <> 'MATCHED'`, str("item_id")); err != nil {
				return err
			}
		} else if resolved && res.Type != ResOutcomeApplied && exc.ItemID != nil {
			// explained or corrected: the line is not reconsidered by later runs
			if _, err := tx.Exec(ctx, `UPDATE recon.items SET match_status = 'IGNORED' WHERE id = $1 AND match_status IN ('UNMATCHED','EXCEPTION')`,
				*exc.ItemID); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE recon.resolutions SET status = 'EXECUTED', executed_at = $2, execution_result = $3, last_error = NULL,
			version = version + 1 WHERE id = $1`, res.ID, s.now(), raw); err != nil {
			return err
		}
		if resolved {
			if _, err := tx.Exec(ctx, `UPDATE recon.exceptions SET status = 'RESOLVED', resolved_at = $2, version = version + 1
				WHERE id = $1 AND status = 'PENDING_APPROVAL'`, exc.ID, s.now()); err != nil {
				return err
			}
			if err := s.addEvent(ctx, tx, exc.ID, "RESOLUTION_EXECUTED", "PENDING_APPROVAL", "RESOLVED", checkerID, "", res.ID, note, ""); err != nil {
				return err
			}
		} else if err := s.reopenInvestigation(ctx, tx, exc.ID, checkerID, res.ID, "RESOLUTION_EXECUTED",
			"the owning module did not apply the evidence; investigation continues"); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{Action: "reconciliation.resolution.executed", ActorType: "staff", ActorID: checkerID,
			TargetType: "reconciliation_resolution", TargetID: res.ID, Metadata: map[string]any{"exception_id": exc.ID, "resolution_type": res.Type,
				"resolved": resolved, "result": result}})
	})
}

func (s *Service) executionFailed(ctx context.Context, resID string, cause error) error {
	msg := cause.Error()
	if len(msg) > 500 {
		msg = msg[:500]
	}
	if _, err := s.Pool.Exec(ctx, `UPDATE recon.resolutions SET last_error = $2, version = version + 1 WHERE id = $1 AND status = 'APPROVED'`,
		resID, strings.ToValidUTF8(msg, "?")); err != nil {
		return errors.Join(cause, err)
	}
	return cause
}

// ExecuteApproved retries approved resolutions whose execution did not complete (crash or a failing owning module).
func (s *Service) ExecuteApproved(ctx context.Context) (int, error) {
	rows, err := s.Pool.Query(ctx, `SELECT id::text, decided_by::text, decision_note FROM recon.resolutions WHERE status = 'APPROVED' ORDER BY decided_at LIMIT 50`)
	if err != nil {
		return 0, err
	}
	type pending struct{ id, checker, note string }
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (pending, error) {
		var p pending
		err := r.Scan(&p.id, &p.checker, &p.note)
		return p, err
	})
	if err != nil {
		return 0, err
	}
	n := 0
	for _, p := range list {
		if err := s.execute(ctx, p.id, p.checker, p.note); err == nil {
			n++
		}
	}
	return n, nil
}

// ExpireResolutions ends proposals nobody decided in time (the exception returns to investigation).
func (s *Service) ExpireResolutions(ctx context.Context) (int, error) {
	rows, err := s.Pool.Query(ctx, `SELECT id::text, exception_id::text FROM recon.resolutions WHERE status = 'PENDING_REVIEW' AND expires_at <= $1 LIMIT 200`, s.now())
	if err != nil {
		return 0, err
	}
	type p struct{ id, exc string }
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (p, error) {
		var x p
		err := r.Scan(&x.id, &x.exc)
		return x, err
	})
	if err != nil {
		return 0, err
	}
	for _, x := range list {
		if err := s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
			var st string
			if err := tx.QueryRow(ctx, `SELECT status FROM recon.resolutions WHERE id = $1 FOR UPDATE`, x.id).Scan(&st); err != nil || st != "PENDING_REVIEW" {
				return err
			}
			return s.endProposal(ctx, tx, x.id, x.exc, "EXPIRED", "", "proposal expired before a decision")
		}); err != nil {
			return 0, err
		}
	}
	return len(list), nil
}
