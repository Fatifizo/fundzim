package payouts

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/Fatifizo/fundzim/internal/audit"
	"github.com/Fatifizo/fundzim/internal/ledger"
	"github.com/Fatifizo/fundzim/internal/platform/errs"
	"github.com/Fatifizo/fundzim/internal/platform/httpx"
	"github.com/Fatifizo/fundzim/internal/platform/ids"
	"github.com/Fatifizo/fundzim/internal/platform/outbox"
)

// Withdrawal (payout) requests — Stage 11, ADR-043. A "withdrawal request" in the brief is an app.payout_requests
// row (Stage 1/2 name). States are ADR-020's; transitions are guarded in the database (machine 'payout') and
// every status change carries an applied payout_events row in the same transaction.

// LiveExecutionEnabled is the code-level gate for real payout execution. It is false and changing it needs an ADR,
// legal sign-off and stage acceptance (CLAUDE.md). With it false, only TEST payouts through the simulator exist.
const LiveExecutionEnabled = false

// KillSwitchFlag is the seeded (off) kill switch for real payout execution.
const KillSwitchFlag = "payouts.live_execution.enabled"

// Execution environments of this process (PAYOUTS_EXECUTION_ENVIRONMENT).
const (
	ExecDisabled       = "DISABLED"
	ExecLocalSimulator = "LOCAL_SIMULATOR"
)

// Payout statuses (ADR-020).
const (
	StRequested  = "PAYOUT_REQUESTED"
	StReview     = "PENDING_REVIEW"
	StApproved   = "APPROVED"
	StSubmitted  = "SUBMITTED"
	StProcessing = "PROCESSING"
	StUnknown    = "UNKNOWN"
	StCompleted  = "COMPLETED"
	StFailed     = "FAILED"
	StRejected   = "REJECTED"
	StCancelled  = "CANCELLED"
	StReversed   = "REVERSED"
)

// Reservation statuses.
const (
	ResActive   = "ACTIVE"
	ResBlocked  = "BLOCKED"
	ResConsumed = "CONSUMED"
	ResReleased = "RELEASED"
	ResExpired  = "EXPIRED"
)

var statusRank = map[string]int{StRequested: 0, StReview: 10, StApproved: 20, StSubmitted: 30, StUnknown: 35, StProcessing: 40,
	StFailed: 50, StRejected: 50, StCancelled: 50, StCompleted: 60, StReversed: 70}

// terminal statuses (no transition out, except COMPLETED → REVERSED)
func terminal(st string) bool {
	return st == StFailed || st == StRejected || st == StCancelled || st == StReversed
}

var (
	ErrPayoutNotFound    = errs.New(errs.NotFound, "PAYOUT_REQUEST_NOT_FOUND", "No such payout request.")
	ErrNotEligible       = errs.New(errs.Unprocessable, "PAYOUT_NOT_ELIGIBLE", "A payout cannot be requested right now.")
	ErrInProgress        = errs.New(errs.Conflict, "PAYOUT_IN_PROGRESS", "Another payout for this campaign and currency is in progress.")
	ErrPayoutState       = errs.New(errs.Conflict, "PAYOUT_STATE", "This action is not allowed in the payout's current status.")
	ErrIdempotencyReused = errs.New(errs.Conflict, "IDEMPOTENCY_KEY_REUSED", "This Idempotency-Key was used for a different payout request.")
	ErrStepUp            = errs.New(errs.Forbidden, "STEP_UP_REQUIRED", "Confirm it's you to continue.")
	ErrSelfApproval      = errs.New(errs.Forbidden, "SELF_APPROVAL_FORBIDDEN", "You cannot decide a payout you requested, own, or are linked to, or whose destination you changed or verified.")
	ErrPayoutsDisabled   = errs.New(errs.Unprocessable, "PAYOUTS_DISABLED", "Payouts are not available in this environment.")
)

// RequestInput is the owner's request.
type RequestInput struct {
	Currency      string  `json:"currency"`
	AmountMinor   string  `json:"amount_minor"`
	DestinationID string  `json:"destination_id"`
	Purpose       *string `json:"purpose"`
}

// Payout is a payout request as staff see it (owners get OwnerView).
type Payout struct {
	ID                   string       `json:"id"`
	CampaignID           string       `json:"campaign_id"`
	OwnerUserID          *string      `json:"owner_user_id"`
	OwnerOrganisationID  *string      `json:"owner_organisation_id"`
	BeneficiaryID        *string      `json:"beneficiary_id"`
	Currency             string       `json:"currency"`
	AmountMinor          int64        `json:"amount_minor,string"`
	Purpose              *string      `json:"purpose"`
	RequestedBy          string       `json:"requested_by"`
	RequestedAt          time.Time    `json:"requested_at"`
	Status               string       `json:"status"`
	ApprovalPolicy       string       `json:"approval_policy"`
	ApprovalRound        int          `json:"approval_round"`
	ExecutionEnvironment string       `json:"execution_environment"`
	TestPayout           bool         `json:"test_payout"`
	LedgerEnvironment    string       `json:"ledger_environment"`
	DestinationID        string       `json:"destination_id"`
	DestinationVersion   int          `json:"destination_version"`
	DestinationRail      string       `json:"destination_rail"`
	DestinationMasked    string       `json:"destination_masked"`
	DestinationCategory  string       `json:"destination_category"`
	ProviderID           string       `json:"provider_id"`
	ProviderAccountID    string       `json:"provider_account_id"`
	CapabilityID         string       `json:"capability_id"`
	ProviderPayoutRef    *string      `json:"provider_payout_ref"`
	ProviderFeeMinor     *int64       `json:"provider_fee_minor,string"`
	ReversedAmountMinor  *int64       `json:"reversed_amount_minor,string"`
	UnknownSince         *time.Time   `json:"unknown_since"`
	UnknownReason        *string      `json:"unknown_reason"`
	NextPollAt           *time.Time   `json:"next_poll_at"`
	PollAttempts         int          `json:"poll_attempts"`
	NeedsReconciliation  bool         `json:"needs_reconciliation"`
	SubmittedAt          *time.Time   `json:"submitted_at"`
	CompletedAt          *time.Time   `json:"completed_at"`
	FailureCode          *string      `json:"failure_code"`
	DecisionReasonCode   *string      `json:"decision_reason_code"`
	CorrelationID        *string      `json:"correlation_id"`
	Version              int          `json:"version"`
	CreatedAt            time.Time    `json:"created_at"`
	UpdatedAt            time.Time    `json:"updated_at"`
	Reservation          *Reservation `json:"reservation"`
	idemScope, idemKey   string
}

// Reservation is a payout's reservation of funds.
type Reservation struct {
	ID                 string     `json:"id"`
	PayoutID           string     `json:"payout_request_id"`
	CampaignID         string     `json:"campaign_id"`
	Currency           string     `json:"currency"`
	AmountMinor        int64      `json:"amount_minor,string"`
	AccountScope       string     `json:"account_scope"`
	Status             string     `json:"status"`
	LedgerReserveTxnID string     `json:"ledger_reserve_transaction_id"`
	LedgerSubmitTxnID  *string    `json:"ledger_submit_transaction_id"`
	LedgerReleaseTxnID *string    `json:"ledger_release_transaction_id"`
	LedgerConsumeTxnID *string    `json:"ledger_consume_transaction_id"`
	ExpiresAt          *time.Time `json:"expires_at"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
}

const payoutCols = `p.id, p.campaign_id, p.owner_user_id, p.owner_organisation_id, p.beneficiary_id, p.currency, p.amount_minor, p.purpose,
	p.requested_by, p.requested_at, p.status, p.approval_policy, p.approval_round, p.execution_environment, p.test_payout, p.ledger_environment,
	p.destination_id, p.destination_version, p.destination_rail, p.destination_masked, p.destination_category, p.provider_id,
	p.provider_account_id, p.capability_id, p.provider_payout_ref, p.provider_fee_minor, p.reversed_amount_minor, p.unknown_since,
	p.unknown_reason, p.next_poll_at, p.poll_attempts, p.needs_reconciliation, p.submitted_at, p.completed_at, p.failure_code,
	p.decision_reason_code, p.correlation_id, p.version, p.created_at, p.updated_at, p.idempotency_scope, p.idempotency_key,
	r.id, r.status, r.amount_minor, r.account_scope, r.ledger_reserve_txn_id, r.ledger_submit_txn_id, r.ledger_release_txn_id,
	r.ledger_consume_txn_id, r.expires_at, r.created_at, r.updated_at`

const payoutFrom = ` FROM app.payout_requests p LEFT JOIN app.payout_reservations r ON r.payout_request_id = p.id`

func scanPayout(row pgx.Row) (Payout, error) {
	var p Payout
	var rid, rst, rscope, rres *string
	var ramt *int64
	var rsub, rrel, rcon *string
	var rexp, rc, ru *time.Time
	err := row.Scan(&p.ID, &p.CampaignID, &p.OwnerUserID, &p.OwnerOrganisationID, &p.BeneficiaryID, &p.Currency, &p.AmountMinor, &p.Purpose,
		&p.RequestedBy, &p.RequestedAt, &p.Status, &p.ApprovalPolicy, &p.ApprovalRound, &p.ExecutionEnvironment, &p.TestPayout,
		&p.LedgerEnvironment, &p.DestinationID, &p.DestinationVersion, &p.DestinationRail, &p.DestinationMasked, &p.DestinationCategory,
		&p.ProviderID, &p.ProviderAccountID, &p.CapabilityID, &p.ProviderPayoutRef, &p.ProviderFeeMinor, &p.ReversedAmountMinor,
		&p.UnknownSince, &p.UnknownReason, &p.NextPollAt, &p.PollAttempts, &p.NeedsReconciliation, &p.SubmittedAt, &p.CompletedAt,
		&p.FailureCode, &p.DecisionReasonCode, &p.CorrelationID, &p.Version, &p.CreatedAt, &p.UpdatedAt, &p.idemScope, &p.idemKey,
		&rid, &rst, &ramt, &rscope, &rres, &rsub, &rrel, &rcon, &rexp, &rc, &ru)
	if errors.Is(err, pgx.ErrNoRows) {
		return p, ErrPayoutNotFound
	}
	if err == nil && rid != nil {
		p.Reservation = &Reservation{ID: *rid, PayoutID: p.ID, CampaignID: p.CampaignID, Currency: p.Currency, AmountMinor: *ramt,
			AccountScope: *rscope, Status: *rst, LedgerReserveTxnID: *rres, LedgerSubmitTxnID: rsub, LedgerReleaseTxnID: rrel,
			LedgerConsumeTxnID: rcon, ExpiresAt: rexp, CreatedAt: *rc, UpdatedAt: *ru}
	}
	return p, err
}

type rowQuerier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

// payout loads a payout; lock = FOR UPDATE OF the payout row.
func (s *Service) payout(ctx context.Context, q rowQuerier, id string, lock bool) (Payout, error) {
	if !ids.Valid(id) {
		return Payout{}, ErrPayoutNotFound
	}
	sql := `SELECT ` + payoutCols + payoutFrom + ` WHERE p.id = $1`
	if lock {
		sql += ` FOR UPDATE OF p`
	}
	return scanPayout(q.QueryRow(ctx, sql, id))
}

// ---- events, transitions, exceptions -----------------------------------------------------------------------

type event struct {
	Type, From, To, Code, Source, ActorType, ActorID, Reason   string
	NotApplied                                                 string // "" = applied
	ProviderEventID, InboxID, AttemptID, DecisionID, RawStatus string
	EvidenceID                                                 string
	LedgerTxIDs                                                []string
	OccurredAt                                                 *time.Time
}

func (s *Service) insertEvent(ctx context.Context, tx pgx.Tx, payoutID string, e event) error {
	if e.ActorType == "" {
		e.ActorType = "SYSTEM"
	}
	if e.Source == "" {
		e.Source = "SYSTEM"
	}
	if e.LedgerTxIDs == nil {
		e.LedgerTxIDs = []string{}
	}
	_, err := tx.Exec(ctx, `INSERT INTO app.payout_events (id, payout_request_id, event_type, from_status, to_status, applied, not_applied_reason,
		transition_code, source, provider_event_id, inbox_event_id, payout_attempt_id, eligibility_decision_id, raw_status, actor_type, actor_id,
		reason, evidence_record_id, ledger_transaction_ids, correlation_id, occurred_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21)`,
		ids.New(), payoutID, e.Type, nilIfEmpty(e.From), nilIfEmpty(e.To), e.NotApplied == "", nilIfEmpty(e.NotApplied), nilIfEmpty(e.Code),
		e.Source, nilIfEmpty(e.ProviderEventID), nilIfEmpty(e.InboxID), nilIfEmpty(e.AttemptID), nilIfEmpty(e.DecisionID),
		nilIfEmpty(e.RawStatus), e.ActorType, nilIfEmpty(e.ActorID), nilIfEmpty(e.Reason), nilIfEmpty(e.EvidenceID), e.LedgerTxIDs,
		nilIfEmpty(httpx.RequestID(ctx)), e.OccurredAt)
	return err
}

var errStale = errors.New("payouts: payout changed concurrently")

// move changes a payout's status from→to under the state machine (DB-guarded), with its event, audit event and
// outbox event in the caller's transaction. extra is an optional SET fragment with parameters from $4.
func (s *Service) move(ctx context.Context, tx pgx.Tx, p *Payout, to string, e event, extra string, extraArgs ...any) error {
	set := `status = $2, status_rank = app.payout_status_rank($2), version = version + 1`
	if extra != "" {
		set += ", " + extra
	}
	args := append([]any{p.ID, to, p.Status}, extraArgs...)
	tag, err := tx.Exec(ctx, `UPDATE app.payout_requests SET `+set+` WHERE id = $1 AND status = $3`, args...)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return errStale
	}
	e.From, e.To = p.Status, to
	if e.Type == "" {
		e.Type = "PAYOUT_STATUS_CHANGED"
	}
	if err := s.insertEvent(ctx, tx, p.ID, e); err != nil {
		return err
	}
	actorType := "system"
	switch e.ActorType {
	case "STAFF":
		actorType = "staff"
	case "OWNER":
		actorType = "user"
	case "PROVIDER":
		actorType = "provider"
	}
	if err := audit.Record(ctx, tx, audit.Event{Action: "payout.status_changed", ActorType: actorType, ActorID: e.ActorID, TargetType: "payout_request",
		TargetID: p.ID, Reason: e.Reason, Metadata: map[string]any{"from": p.Status, "to": to, "transition": e.Code, "source": e.Source,
			"test_payout": p.TestPayout}}); err != nil {
		return err
	}
	if _, err := outbox.Write(ctx, tx, outbox.Event{AggregateType: "payout_request", AggregateID: p.ID, EventType: "payouts.status_changed",
		Payload:       map[string]any{"payout_request_id": p.ID, "campaign_id": p.CampaignID, "from": p.Status, "to": to, "test_payout": p.TestPayout},
		CorrelationID: httpx.RequestID(ctx), OccurredAt: s.now()}); err != nil {
		return err
	}
	p.Status = to
	return nil
}

// openException records an operations exception (one unresolved per type and payout).
func (s *Service) openException(ctx context.Context, tx pgx.Tx, typ, severity, source string, p *Payout, campaignID string,
	evidence map[string]any, reconRef string) error {
	if evidence == nil {
		evidence = map[string]any{}
	}
	ev, _ := json.Marshal(evidence)
	var pid any
	if p != nil {
		pid, campaignID = p.ID, p.CampaignID
	}
	id := ids.New()
	tag, err := tx.Exec(ctx, `INSERT INTO app.payout_exceptions (id, payout_request_id, campaign_id, exception_type, severity, source, status,
		reconciliation_reference_id, evidence, detected_at) VALUES ($1, $2, $3, $4, $5, $6, 'OPEN', $7, $8, $9)
		ON CONFLICT (exception_type, payout_request_id) WHERE status <> 'RESOLVED' AND payout_request_id IS NOT NULL DO NOTHING`,
		id, pid, nilIfEmpty(campaignID), typ, severity, source, nilIfEmpty(reconRef), ev, s.now())
	if err != nil || tag.RowsAffected() == 0 {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO app.payout_exception_events (id, payout_exception_id, from_status, to_status, actor_type, note)
		VALUES ($1, $2, NULL, 'OPEN', 'SYSTEM', $3)`, ids.New(), id, typ); err != nil {
		return err
	}
	if severity == "SEV1" {
		s.Logger.Error("payouts: SEV1 payout exception (ALERT)", "type", typ, "payout_request_id", pid)
	}
	_, err = outbox.Write(ctx, tx, outbox.Event{AggregateType: "payout_exception", AggregateID: id, EventType: "payouts.exception_opened",
		Payload: map[string]any{"exception_id": id, "exception_type": typ, "severity": severity, "payout_request_id": pid}, OccurredAt: s.now()})
	return err
}

// ---- request ---------------------------------------------------------------------------------------------------

var (
	amountRE = regexp.MustCompile(`^[1-9][0-9]{0,15}$`)
	ccyRE    = regexp.MustCompile(`^[A-Z]{3}$`)
)

// RequestPayout creates a withdrawal request for a campaign: request-time eligibility, the single in-flight slot,
// then the ledger reservation (ADR-043 §3). Owner-facing errors carry only a coarse reason category.
func (s *Service) RequestPayout(ctx context.Context, actorID string, stepUpFresh bool, campaignID string, in RequestInput, idemKey string) (Payout, error) {
	if s.Ledger == nil || s.Campaigns == nil {
		return Payout{}, ErrPayoutsDisabled
	}
	var det []errs.Detail
	if !ccyRE.MatchString(in.Currency) {
		det = append(det, errs.Detail{Field: "currency", Code: "INVALID_VALUE"})
	}
	if !amountRE.MatchString(in.AmountMinor) {
		det = append(det, errs.Detail{Field: "amount_minor", Code: "INVALID_FORMAT"})
	}
	if !ids.Valid(in.DestinationID) {
		det = append(det, errs.Detail{Field: "destination_id", Code: "INVALID_FORMAT"})
	}
	var purpose *string
	if in.Purpose != nil {
		v := strings.TrimSpace(*in.Purpose)
		if n := utf8.RuneCountInString(v); n > 500 {
			det = append(det, errs.Detail{Field: "purpose", Code: "TOO_LONG"})
		} else if n > 0 {
			purpose = &v
		}
	}
	if n := len(idemKey); n < 8 || n > 255 {
		det = append(det, errs.Detail{Field: "Idempotency-Key", Code: "REQUIRED"})
	}
	if len(det) > 0 {
		return Payout{}, httpx.Validation(det...)
	}
	if !ids.Valid(campaignID) {
		return Payout{}, ErrPayoutNotFound
	}
	amount, _ := strconv.ParseInt(in.AmountMinor, 10, 64)
	scope := "payouts.request:" + actorID
	// replay of the same Idempotency-Key returns the original request (a different body is refused)
	if p, err := scanPayout(s.Pool.QueryRow(ctx, `SELECT `+payoutCols+payoutFrom+` WHERE p.idempotency_scope = $1 AND p.idempotency_key = $2`,
		scope, idemKey)); err == nil {
		if p.CampaignID != campaignID || p.AmountMinor != amount || p.Currency != in.Currency || p.DestinationID != in.DestinationID {
			return Payout{}, ErrIdempotencyReused
		}
		return p, nil
	} else if !errors.Is(err, ErrPayoutNotFound) {
		return Payout{}, err
	}
	if !stepUpFresh {
		return Payout{}, ErrStepUp
	}
	if s.ExecEnv != ExecLocalSimulator {
		return Payout{}, ErrPayoutsDisabled
	}
	sub := subject{CampaignID: campaignID, RequesterID: actorID, DestinationID: in.DestinationID, Currency: in.Currency, Amount: amount}
	f, acc, route, err := s.gather(ctx, PhaseRequest, sub)
	if err != nil {
		return Payout{}, err
	}
	dec := Evaluate(f)
	id := ids.New()
	if dec.Outcome == OutcomeRejected {
		// no payout row; the refused attempt is still evidence
		err := s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
			did, err := s.writeDecision(ctx, tx, "", sub, actorID, f, dec)
			if err != nil {
				return err
			}
			if dec.Result("EC-09").Detail == "INSUFFICIENT_ELIGIBLE_FUNDS" {
				if err := s.openException(ctx, tx, "INSUFFICIENT_ELIGIBLE_FUNDS", "SEV3", "ELIGIBILITY", nil, campaignID,
					map[string]any{"decision_id": did}, ""); err != nil {
					return err
				}
			}
			return audit.Record(ctx, tx, audit.Event{Action: "payout.request.refused", ActorType: "user", ActorID: actorID, TargetType: "campaign",
				TargetID: campaignID, Outcome: "denied", Metadata: map[string]any{"decision_id": did, "reason_category": dec.ReasonCode,
					"failed_checks": strings.Join(dec.FailedChecks(), ",")}})
		})
		if err != nil {
			return Payout{}, err
		}
		if dec.Result("EC-11").Result == Fail {
			return Payout{}, ErrInProgress
		}
		return Payout{}, notEligible(dec.ReasonCode)
	}
	if route == nil {
		return Payout{}, ErrPayoutsDisabled // unreachable: EC-08 fails without a route
	}
	d, err := s.load(ctx, s.Pool, in.DestinationID, false)
	if err != nil {
		return Payout{}, err
	}
	hash := sha256.Sum256([]byte(strings.Join([]string{d.ID, strconv.Itoa(d.detailsVersion), d.Rail, d.Currency, d.MaskedIdentifier, d.HolderName}, "|")))
	env := f.LedgerEnvironment
	err = s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO app.payout_requests (id, campaign_id, owner_user_id, owner_organisation_id, beneficiary_id, currency,
			amount_minor, purpose, requested_by, requested_at, status, status_rank, approval_policy, policy_version, execution_environment,
			test_payout, ledger_environment, destination_id, destination_version, destination_hash, destination_rail, destination_masked,
			destination_category, provider_id, provider_is_sandbox, provider_account_id, capability_id, capability_routable,
			capability_payout_api, idempotency_scope, idempotency_key, correlation_id)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,'PAYOUT_REQUESTED',0,$11,$12,$13,true,$14,$15,$16,$17,$18,$19,$20,$21,true,$22,$23,true,true,$24,$25,$26)`,
			id, campaignID, acc.OwnerUserID, acc.OwnerOrgID, f.BeneficiaryID, in.Currency, amount, purpose, actorID, s.now(), dec.ApprovalTier,
			EngineVersion, s.ExecEnv, env, d.ID, d.detailsVersion, hash[:], d.Rail, d.MaskedIdentifier, d.Category, route.Provider.ID,
			route.Account.ID, route.Capability.ID, scope, idemKey, nilIfEmpty(httpx.RequestID(ctx))); err != nil {
			return err
		}
		did, err := s.writeDecision(ctx, tx, id, sub, actorID, f, dec)
		if err != nil {
			return err
		}
		if err := s.insertEvent(ctx, tx, id, event{Type: "WITHDRAWAL_REQUESTED", To: StRequested, Code: "Y1", Source: "OWNER", ActorType: "OWNER",
			ActorID: actorID, DecisionID: did}); err != nil {
			return err
		}
		if err := s.insertEvent(ctx, tx, id, event{Type: "ELIGIBILITY_CHECKED", Source: "SYSTEM", DecisionID: did,
			Reason: dec.Outcome + "/" + dec.ApprovalTier}); err != nil {
			return err
		}
		if err := audit.Record(ctx, tx, audit.Event{Action: "payout.requested", ActorType: "user", ActorID: actorID, TargetType: "payout_request",
			TargetID: id, Metadata: map[string]any{"campaign_id": campaignID, "currency": in.Currency, "amount_minor": in.AmountMinor,
				"decision_id": did, "approval_tier": dec.ApprovalTier, "test_payout": true}}); err != nil {
			return err
		}
		_, err = outbox.Write(ctx, tx, outbox.Event{AggregateType: "payout_request", AggregateID: id, EventType: "payouts.requested",
			Payload: map[string]any{"payout_request_id": id, "campaign_id": campaignID, "currency": in.Currency, "amount_minor": in.AmountMinor,
				"test_payout": true}, CorrelationID: httpx.RequestID(ctx), OccurredAt: s.now()})
		return err
	})
	if err != nil {
		var pg *pgconn.PgError
		if errors.As(err, &pg) && pg.Code == "23505" {
			switch pg.ConstraintName {
			case "uq_payout_requests_one_inflight":
				return Payout{}, ErrInProgress
			case "uq_payout_requests_idempotency":
				return s.RequestPayout(ctx, actorID, stepUpFresh, campaignID, in, idemKey) // concurrent replay: answer from the winner
			}
		}
		return Payout{}, err
	}
	refused, err := s.reserve(ctx, id)
	if refused != nil {
		return Payout{}, refused // the ledger refused the reservation: the payout is REJECTED
	}
	if err != nil {
		s.Logger.Warn("payouts: reservation deferred to recovery", "payout_request_id", id, "err", err)
	}
	return s.payout(ctx, s.Pool, id, false)
}

func (s *Service) writeDecision(ctx context.Context, tx pgx.Tx, payoutID string, sub subject, actorID string, f Facts, d Decision) (string, error) {
	id := ids.New()
	inputs, err := json.Marshal(f)
	if err != nil {
		return "", err
	}
	results, _ := json.Marshal(d.Results)
	var tier any
	if d.ApprovalTier != "" {
		tier = d.ApprovalTier
	}
	rec := map[string]any{"id": id, "payout_request_id": payoutID, "campaign_id": sub.CampaignID, "currency": sub.Currency,
		"amount_minor": sub.Amount, "phase": d.Phase, "engine": EngineVersion, "inputs": json.RawMessage(inputs),
		"results": json.RawMessage(results), "outcome": d.Outcome, "tier": d.ApprovalTier}
	_, err = tx.Exec(ctx, `INSERT INTO app.payout_eligibility_decisions (id, payout_request_id, campaign_id, currency, amount_minor, requested_by,
		phase, evaluated_at, engine_version, policy_version, inputs, results, outcome, approval_tier, request_id, correlation_id, record_hash)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$9,$10,$11,$12,$13,$14,$14,$15)`,
		id, nilIfEmpty(payoutID), sub.CampaignID, sub.Currency, sub.Amount, actorID, d.Phase, f.At, EngineVersion, inputs, results, d.Outcome,
		tier, nilIfEmpty(httpx.RequestID(ctx)), recordHash(rec))
	if err != nil {
		return "", err
	}
	return id, audit.Record(ctx, tx, audit.Event{Action: "payout.eligibility.evaluated", ActorType: "system", TargetType: "payout_eligibility_decision",
		TargetID: id, Metadata: map[string]any{"payout_request_id": payoutID, "phase": d.Phase, "outcome": d.Outcome}})
}

// notEligible is the owner-facing refusal with a coarse reason category (LR-008: no detail that could tip off).
func notEligible(category string) *errs.Error {
	e := errs.New(errs.Unprocessable, "PAYOUT_NOT_ELIGIBLE", "A payout cannot be requested right now.")
	e.Details = []errs.Detail{{Field: "reason_category", Code: category}}
	return e
}

// ---- reservation ---------------------------------------------------------------------------------------------

func (s *Service) ledgerPosting(p Payout, rule, step string, actor ledger.Actor, lines ...ledger.Line) ledger.Posting {
	return ledger.Posting{Rule: rule, IdempotencyKey: "payout:" + p.ID + ":" + step, Currency: p.Currency, OccurredAt: s.now(),
		Description: "TEST_ONLY payout " + step + " (simulator)", SourceSystem: ledger.SourcePayouts, SourceType: "payout", SourceID: p.ID,
		SourceEventID: "payout:" + p.ID + ":" + step, EvidenceClass: ledger.EvidenceSimulated, Actor: actor,
		Metadata: map[string]string{"test_payout": "true", "payout_request_id": p.ID, "campaign_id": p.CampaignID, "step": step}, Lines: lines}
}

func payable(p Payout) ledger.AccountRef {
	return ledger.AccountRef{Class: ledger.ClassCampaignPayable, OwnerID: p.CampaignID}
}
func pending(p Payout) ledger.AccountRef {
	return ledger.AccountRef{Class: ledger.ClassCampaignPayoutPend, OwnerID: p.CampaignID}
}
func inTransit(p Payout) ledger.AccountRef {
	return ledger.AccountRef{Class: ledger.ClassPayoutInTransit, OwnerID: p.ProviderID}
}
func settled(p Payout) ledger.AccountRef {
	return ledger.AccountRef{Class: ledger.ClassPSPSettled, OwnerID: p.ProviderID}
}

var sysActor = ledger.Actor{Type: "SYSTEM", ID: "payouts"}

// reserve posts the reservation journal for a PAYOUT_REQUESTED payout (idempotent) and moves it to PENDING_REVIEW,
// or REJECTED when the ledger refuses (the funds are no longer there): refused is then the owner-facing error.
// Safe to re-run (recovery job); err is a transient failure that leaves the payout PAYOUT_REQUESTED.
func (s *Service) reserve(ctx context.Context, id string) (refused *errs.Error, err error) {
	p, err := s.payout(ctx, s.Pool, id, false)
	if err != nil {
		return nil, err
	}
	if p.Status != StRequested {
		return nil, nil
	}
	res, perr := s.Ledger.PostOnce(ctx, s.ledgerPosting(p, "PAYOUT_RESERVED", "reserved", sysActor, ledger.Dr(payable(p), p.AmountMinor),
		ledger.Cr(pending(p), p.AmountMinor)))
	if perr != nil {
		var e *errs.Error
		if !errors.As(perr, &e) || (e.Code != "LEDGER_INSUFFICIENT_FUNDS" && e.Code != "LEDGER_ACCOUNT_FROZEN" && e.Code != "LEDGER_ACCOUNT_CLOSED") {
			return nil, perr // transient: the recovery job retries with the same key
		}
		if err := s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
			cur, err := s.payout(ctx, tx, id, true)
			if err != nil || cur.Status != StRequested {
				return err
			}
			if err := s.move(ctx, tx, &cur, StRejected, event{Type: "WITHDRAWAL_REJECTED", Code: "Y4", Reason: e.Code},
				`decision_reason_code = $4`, "INSUFFICIENT_FUNDS"); err != nil {
				return err
			}
			return s.openException(ctx, tx, "INSUFFICIENT_ELIGIBLE_FUNDS", "SEV3", "LEDGER", &cur, "", map[string]any{"ledger_error": e.Code}, "")
		}); err != nil {
			return nil, err
		}
		return notEligible("INSUFFICIENT_FUNDS"), nil
	}
	return nil, s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		cur, err := s.payout(ctx, tx, id, true)
		if err != nil {
			return err
		}
		if cur.Status != StRequested {
			return nil // moved meanwhile (cannot happen: no transition out of PAYOUT_REQUESTED except here and recovery)
		}
		var exp any
		if v, err := s.valueLimit(ctx, LimitRequestValidity, "DURATION", s.now()); err != nil {
			return err
		} else if v.Configured {
			exp = s.now().Add(time.Duration(v.Value) * time.Second)
		}
		rid := ids.New()
		if _, err := tx.Exec(ctx, `INSERT INTO app.payout_reservations (id, payout_request_id, campaign_id, currency, amount_minor, account_scope,
			status, ledger_reserve_txn_id, expires_at) VALUES ($1,$2,$3,$4,$5,$6,'ACTIVE',$7,$8)`,
			rid, cur.ID, cur.CampaignID, cur.Currency, cur.AmountMinor, payable(cur).Code(), res.TransactionID, exp); err != nil {
			return err
		}
		if err := s.insertEvent(ctx, tx, cur.ID, event{Type: "RESERVATION_CREATED", LedgerTxIDs: []string{res.TransactionID}}); err != nil {
			return err
		}
		if err := audit.Record(ctx, tx, audit.Event{Action: "payout.reservation.created", ActorType: "system", TargetType: "payout_reservation",
			TargetID: rid, Metadata: map[string]any{"payout_request_id": cur.ID, "ledger_transaction_id": res.TransactionID}}); err != nil {
			return err
		}
		return s.move(ctx, tx, &cur, StReview, event{Type: "APPROVAL_REQUESTED", Code: "Y2", Reason: cur.ApprovalPolicy}, "")
	})
}

// ---- cancellation ------------------------------------------------------------------------------------------------

// CancelByOwner cancels a payout before submission (owner or ORG_ADMIN of the owning organisation, fresh step-up).
func (s *Service) CancelByOwner(ctx context.Context, actorID string, stepUpFresh bool, id string) (Payout, error) {
	p, err := s.OwnerPayout(ctx, actorID, id)
	if err != nil {
		return Payout{}, err
	}
	if !stepUpFresh {
		return Payout{}, ErrStepUp
	}
	if _, err := s.Campaigns.OwnerAccess(ctx, actorID, p.CampaignID, true); err != nil {
		return Payout{}, ErrPayoutNotFound
	}
	return s.cancel(ctx, id, event{Type: "WITHDRAWAL_CANCELLED", Code: "Y5", Source: "OWNER", ActorType: "OWNER", ActorID: actorID}, "OWNER_CANCELLED")
}

// CancelByStaff cancels a payout before submission (payout.operate; reason required).
func (s *Service) CancelByStaff(ctx context.Context, staffID, id, reason string) (Payout, error) {
	if utf8.RuneCountInString(strings.TrimSpace(reason)) < 5 {
		return Payout{}, httpx.Validation(errs.Detail{Field: "reason", Code: "TOO_SHORT"})
	}
	return s.cancel(ctx, id, event{Type: "WITHDRAWAL_CANCELLED", Code: "Y5", Source: "STAFF", ActorType: "STAFF", ActorID: staffID,
		Reason: strings.TrimSpace(reason)}, "STAFF_CANCELLED")
}

func (s *Service) cancel(ctx context.Context, id string, e event, code string) (Payout, error) {
	err := s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		p, err := s.payout(ctx, tx, id, true)
		if err != nil {
			return err
		}
		if p.Status != StReview && p.Status != StApproved {
			return ErrPayoutState // never after submission; PAYOUT_REQUESTED is still reserving
		}
		return s.move(ctx, tx, &p, StCancelled, e, `decision_reason_code = $4`, code)
	})
	if err != nil {
		return Payout{}, err
	}
	if err := s.syncLedger(ctx, id); err != nil {
		s.Logger.Warn("payouts: release deferred to recovery", "payout_request_id", id, "err", err)
	}
	return s.payout(ctx, s.Pool, id, false)
}
