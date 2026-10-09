// Package risk is FundZim's risk foundation (Stage 5, interface contract §4): it records append-only risk
// signals from domain events, recomputes an explainable assessment of the signal's subject with a versioned
// rule-weighted model whose weights and thresholds are risk.limits rows, and records a routing decision
// (NO_ACTION / MANUAL_REVIEW / ESCALATE). On ESCALATE it emits risk.escalation_recommended for the
// compliance module. A score is never proof of fraud: decisions only route subjects to humans.
//
// The module uses the app pool (fundzim_app / fundzim_worker) and owns the risk schema. It imports
// platform and audit only.
package risk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Fatifizo/fundzim/internal/audit"
	"github.com/Fatifizo/fundzim/internal/platform/clock"
	"github.com/Fatifizo/fundzim/internal/platform/db"
	"github.com/Fatifizo/fundzim/internal/platform/ids"
	"github.com/Fatifizo/fundzim/internal/platform/outbox"
)

// EvEscalationRecommended is emitted when a decision is ESCALATE.
const EvEscalationRecommended = "risk.escalation_recommended"

// ConsumerName is the outbox consumer name of every risk signal subscription. Never rename it.
const ConsumerName = "risk.record_signal"

// assessable subject types (ck_risk_assessments_subject).
var assessable = map[string]bool{"USER": true, "ORGANISATION": true, "BENEFICIARY": true, "PAYOUT_DESTINATION": true,
	"KYC_CASE": true, "KYB_CASE": true, "CAMPAIGN": true, "PAYOUT": true}

// Service is the risk module.
type Service struct {
	Pool   *pgxpool.Pool // app pool (fundzim_app in the API, fundzim_worker in the worker)
	Clock  clock.Clock
	Logger *slog.Logger
}

// New returns the risk service.
func New(pool *pgxpool.Pool, clk clock.Clock, logger *slog.Logger) *Service {
	if clk == nil {
		clk = clock.System
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{Pool: pool, Clock: clk, Logger: logger}
}

// Signal is one recorded risk fact.
type Signal struct {
	ID              string         `json:"id"`
	Type            string         `json:"signal_type"`
	SubjectType     string         `json:"subject_type"`
	SubjectID       string         `json:"subject_id"`
	SourceModule    string         `json:"source_module"`
	SourceEventID   string         `json:"source_event_id"`
	SourceEventType string         `json:"source_event_type"`
	ObservedAt      time.Time      `json:"observed_at"`
	Facts           map[string]any `json:"facts"`
}

// Decision is a routing decision.
type Decision struct {
	ID            string    `json:"id"`
	AssessmentID  string    `json:"assessment_id,omitempty"`
	Decision      string    `json:"decision"`
	ReasonCode    string    `json:"reason_code"`
	PolicyVersion string    `json:"policy_version"`
	DecidedAt     time.Time `json:"decided_at"`
}

// Assessment is the latest assessment of a subject with its decision. ID is empty when the subject has never
// been assessed; Decision may then still hold a fail-closed MANUAL_REVIEW decision.
type Assessment struct {
	ID                string       `json:"id,omitempty"`
	SubjectType       string       `json:"subject_type"`
	SubjectID         string       `json:"subject_id"`
	Score             int          `json:"score"`
	Rating            string       `json:"rating,omitempty"`
	ModelVersion      string       `json:"model_version,omitempty"`
	ContributingRules []string     `json:"contributing_rules"`
	Explanation       []RuleResult `json:"explanation"`
	ComputedAt        *time.Time   `json:"computed_at,omitempty"`
	Decision          *Decision    `json:"decision,omitempty"`
}

// ErrInvalidSubject is returned by Latest for an unknown subject type or a malformed id.
var ErrInvalidSubject = errors.New("risk: invalid subject")

// Latest returns the subject's latest assessment (with its decision) and its most recent signals (newest
// first, at most 50). Reviewers use it to see why a subject was routed to them.
func (s *Service) Latest(ctx context.Context, subjectType, subjectID string) (Assessment, []Signal, error) {
	out := Assessment{SubjectType: subjectType, SubjectID: subjectID, ContributingRules: []string{}, Explanation: []RuleResult{}}
	if !assessable[subjectType] || !ids.Valid(subjectID) {
		return out, nil, ErrInvalidSubject
	}
	var expl []byte
	var computed time.Time
	err := s.Pool.QueryRow(ctx, `SELECT id, score, rating, model_version, contributing_rules, explanation, computed_at
		FROM risk.risk_assessments WHERE subject_type = $1 AND subject_id = $2 ORDER BY computed_at DESC, id DESC LIMIT 1`,
		subjectType, subjectID).Scan(&out.ID, &out.Score, &out.Rating, &out.ModelVersion, &out.ContributingRules, &expl, &computed)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
	case err != nil:
		return out, nil, err
	default:
		out.ComputedAt = &computed
		if err := json.Unmarshal(expl, &out.Explanation); err != nil {
			return out, nil, fmt.Errorf("risk: explanation: %w", err)
		}
	}
	var d Decision
	var aID *string
	err = s.Pool.QueryRow(ctx, `SELECT id, assessment_id, decision, reason_code, policy_version, decided_at FROM risk.risk_decisions
		WHERE subject_type = $1 AND subject_id = $2 ORDER BY decided_at DESC, id DESC LIMIT 1`, subjectType, subjectID).
		Scan(&d.ID, &aID, &d.Decision, &d.ReasonCode, &d.PolicyVersion, &d.DecidedAt)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
	case err != nil:
		return out, nil, err
	default:
		if aID != nil {
			d.AssessmentID = *aID
		}
		out.Decision = &d
	}
	rows, err := s.Pool.Query(ctx, `SELECT id, signal_type, subject_type, subject_id, source_module, source_event_id, source_event_type,
		observed_at, facts FROM risk.risk_signals WHERE subject_type = $1 AND subject_id = $2 ORDER BY observed_at DESC, id DESC LIMIT 50`,
		subjectType, subjectID)
	if err != nil {
		return out, nil, err
	}
	defer rows.Close()
	var sigs []Signal
	for rows.Next() {
		var sg Signal
		var facts []byte
		if err := rows.Scan(&sg.ID, &sg.Type, &sg.SubjectType, &sg.SubjectID, &sg.SourceModule, &sg.SourceEventID, &sg.SourceEventType,
			&sg.ObservedAt, &facts); err != nil {
			return out, nil, err
		}
		if err := json.Unmarshal(facts, &sg.Facts); err != nil {
			return out, nil, err
		}
		sigs = append(sigs, sg)
	}
	return out, sigs, rows.Err()
}

// Outcome reports what processing one signal did (for tests and logs).
type Outcome struct {
	Duplicate    bool
	SignalID     string
	AssessmentID string
	DecisionID   string
	Decision     string
	Rating       string
	Score        int
	EventID      string // risk.escalation_recommended outbox id when ESCALATE
}

// RecordSignal records one signal and, if it is new, reassesses its subject and records the decision, all in
// one transaction. A duplicate (same source event and signal type) changes nothing.
func (s *Service) RecordSignal(ctx context.Context, sig Signal) (Outcome, error) {
	if err := validateSignal(sig); err != nil {
		return Outcome{}, err
	}
	var out Outcome
	err := db.WithTx(ctx, s.Pool, db.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		out = Outcome{}
		// one assessment at a time per subject, so concurrent signals never compute from a stale count
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('risk:' || $1 || ':' || $2, 0))`,
			sig.SubjectType, sig.SubjectID); err != nil {
			return err
		}
		facts, err := json.Marshal(nonNil(sig.Facts))
		if err != nil {
			return err
		}
		sigID := ids.New()
		tag, err := tx.Exec(ctx, `INSERT INTO risk.risk_signals (id, signal_type, subject_type, subject_id, source_module, source_event_id,
			source_event_type, observed_at, facts, recorded_at) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
			ON CONFLICT (source_event_id, signal_type) DO NOTHING`,
			sigID, sig.Type, sig.SubjectType, sig.SubjectID, sig.SourceModule, sig.SourceEventID, sig.SourceEventType, sig.ObservedAt.UTC(),
			facts, s.Clock.Now())
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			out.Duplicate = true
			return nil
		}
		out.SignalID = sigID
		if !assessable[sig.SubjectType] {
			return nil
		}
		return s.assess(ctx, tx, sig.SubjectType, sig.SubjectID, sigID, &out)
	})
	return out, err
}

func (s *Service) assess(ctx context.Context, tx pgx.Tx, subjectType, subjectID, triggerSignalID string, out *Outcome) error {
	now := s.Clock.Now()
	lim, err := loadLimits(ctx, tx, now, RequiredKeys())
	if err != nil {
		return err
	}
	var res Result
	evalErr := ErrLimitsUnavailable
	if window, ok := lim.Values[KeySignalWindow]; ok && window > 0 {
		counts, err := countSignals(ctx, tx, subjectType, subjectID, now.Add(-time.Duration(window)*time.Second))
		if err != nil {
			return err
		}
		res, evalErr = Evaluate(lim, counts)
	}
	decID := ids.New()
	if errors.Is(evalErr, ErrLimitsUnavailable) {
		// fail closed: without effective limits the model cannot run; route the subject to a human
		s.Logger.Warn("risk model limits unavailable; routing to manual review", slog.String("subject_type", subjectType),
			slog.String("subject_id", subjectID))
		if _, err := tx.Exec(ctx, `INSERT INTO risk.risk_decisions (id, assessment_id, subject_type, subject_id, decision, reason_code,
			policy_version, decided_at) VALUES ($1, NULL, $2, $3, 'MANUAL_REVIEW', $4, $5, $6)`,
			decID, subjectType, subjectID, ReasonLimitsUnavailable, PolicyVersion, now); err != nil {
			return err
		}
		out.DecisionID, out.Decision = decID, DecisionManualReview
		return s.auditDecision(ctx, tx, subjectType, subjectID, "", decID, DecisionManualReview, ReasonLimitsUnavailable, "")
	} else if evalErr != nil {
		return evalErr
	}
	expl, err := json.Marshal(res.Explanation)
	if err != nil {
		return err
	}
	asmID := ids.New()
	if _, err := tx.Exec(ctx, `INSERT INTO risk.risk_assessments (id, subject_type, subject_id, decision_point, score, rating, model_version,
		contributing_rules, explanation, rating_limit_ids, trigger_signal_id, computed_at)
		VALUES ($1, $2, $3, 'EVENT', $4, $5, $6, $7, $8, $9, $10, $11)`,
		asmID, subjectType, subjectID, res.Score, res.Rating, ModelVersion, res.ContributingRules, expl, res.LimitIDs, triggerSignalID, now); err != nil {
		return err
	}
	decision, reason := Decide(res.Rating)
	if _, err := tx.Exec(ctx, `INSERT INTO risk.risk_decisions (id, assessment_id, subject_type, subject_id, decision, reason_code,
		policy_version, decision_limit_ids, decided_at) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		decID, asmID, subjectType, subjectID, decision, reason, PolicyVersion, res.LimitIDs, now); err != nil {
		return err
	}
	*out = Outcome{SignalID: out.SignalID, AssessmentID: asmID, DecisionID: decID, Decision: decision, Rating: res.Rating, Score: res.Score}
	if decision == DecisionNoAction {
		return nil
	}
	if decision == DecisionEscalate {
		evID, err := outbox.Write(ctx, tx, outbox.Event{AggregateType: "risk_subject", AggregateID: subjectID, EventType: EvEscalationRecommended,
			Payload: map[string]any{"subject_type": subjectType, "subject_id": subjectID, "assessment_id": asmID, "decision_id": decID,
				"rating": res.Rating}, OccurredAt: now})
		if err != nil {
			return err
		}
		out.EventID = evID
	}
	return s.auditDecision(ctx, tx, subjectType, subjectID, asmID, decID, decision, reason, res.Rating)
}

func (s *Service) auditDecision(ctx context.Context, tx pgx.Tx, subjectType, subjectID, asmID, decID, decision, reason, rating string) error {
	action := "risk.review.recommended"
	if decision == DecisionEscalate {
		action = "risk.escalation.recommended"
	}
	md := map[string]any{"subject_type": subjectType, "subject_id": subjectID, "decision_id": decID, "decision": decision,
		"reason_code": reason, "policy_version": PolicyVersion, "model_version": ModelVersion}
	if asmID != "" {
		md["assessment_id"], md["rating"] = asmID, rating
	}
	return audit.Record(ctx, tx, audit.Event{Action: action, ActorType: "system", TargetType: "risk_decision", TargetID: decID, Metadata: md})
}

// loadLimits returns the effective APPROVED GLOBAL value of each key at `at`: per limit type the highest
// approved version whose window contains `at`, combined across types by mostRestrictive.
func loadLimits(ctx context.Context, tx pgx.Tx, at time.Time, keys []string) (Limits, error) {
	rows, err := tx.Query(ctx, `SELECT DISTINCT ON (limit_key, limit_type) id, limit_key, value_kind, value_count, value_seconds
		FROM risk.limits
		WHERE status = 'APPROVED' AND scope_type = 'GLOBAL' AND limit_key = ANY($1)
		  AND effective_from <= $2 AND (effective_to IS NULL OR effective_to > $2)
		ORDER BY limit_key, limit_type, version DESC`, keys, at)
	if err != nil {
		return Limits{}, err
	}
	defer rows.Close()
	lim := Limits{Values: map[string]int64{}, IDs: map[string]string{}}
	for rows.Next() {
		var id, key, kind string
		var cnt, secs *int64
		if err := rows.Scan(&id, &key, &kind, &cnt, &secs); err != nil {
			return Limits{}, err
		}
		var v int64
		switch {
		case kind == "COUNT" && cnt != nil:
			v = *cnt
		case kind == "DURATION" && secs != nil:
			v = *secs
		default:
			continue // a key with the wrong kind is unusable: treated as missing (fail closed)
		}
		if old, ok := lim.Values[key]; ok {
			nv := mostRestrictive(key, old, v)
			if nv == old {
				continue
			}
			v = nv
		}
		lim.Values[key], lim.IDs[key] = v, id
	}
	return lim, rows.Err()
}

func countSignals(ctx context.Context, tx pgx.Tx, subjectType, subjectID string, since time.Time) (map[string]int, error) {
	rows, err := tx.Query(ctx, `SELECT signal_type, count(*) FROM risk.risk_signals
		WHERE subject_type = $1 AND subject_id = $2 AND observed_at >= $3 GROUP BY signal_type`, subjectType, subjectID, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var t string
		var n int
		if err := rows.Scan(&t, &n); err != nil {
			return nil, err
		}
		out[t] = n
	}
	return out, rows.Err()
}

func nonNil(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}
