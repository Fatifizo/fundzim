package risk

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Fatifizo/fundzim/internal/audit"
	"github.com/Fatifizo/fundzim/internal/platform/ids"
	"github.com/Fatifizo/fundzim/internal/platform/outbox"
)

// The evaluation pipeline (ADR-045 §5; brief §18): a trusted event became a signal (validated source, catalogue type);
// the subject's ACTIVE rule versions are evaluated over its signals and risk state; the evaluation is persisted with its
// explanation; the authorised automated actions follow (monitoring event, case, temporary payout-protective hold); an
// outbox event is published. Everything happens in the signal's transaction, so it is atomic and — keyed on the trigger
// signal — idempotent: a redelivered event changes nothing.

// EvEvaluationRecorded is published for every evaluation (ids, score, classification and action only).
const EvEvaluationRecorded = "risk.evaluation_recorded"

// Band limits (approved risk.limits rows override the engineering defaults).
const (
	BandLimitMedium   = "risk.v2.band.medium_min"
	BandLimitHigh     = "risk.v2.band.high_min"
	BandLimitCritical = "risk.v2.band.critical_min"
)

var priorityRank = map[string]int{"P1": 1, "P2": 2, "P3": 3, "P4": 4}

// activeRules loads the ACTIVE rule versions that apply to a subject type.
func activeRules(ctx context.Context, q pgx.Tx, subjectType string) ([]RuleInput, error) {
	rows, err := q.Query(ctx, `SELECT v.id, r.code, r.name, v.definition, v.contribution, v.severity, v.recommended_action,
		coalesce(v.hold_type, ''), coalesce(v.hold_duration_seconds, 0), v.case_priority
		FROM risk.risk_rule_versions v JOIN risk.risk_rules r ON r.id = v.rule_id
		WHERE v.status = 'ACTIVE' AND r.subject_type = $1 ORDER BY r.code`, subjectType)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RuleInput
	for rows.Next() {
		var r RuleInput
		var raw []byte
		var secs int
		if err := rows.Scan(&r.VersionID, &r.RuleCode, &r.RuleName, &raw, &r.Contribution, &r.Severity, &r.Action, &r.HoldType, &secs, &r.Priority); err != nil {
			return nil, err
		}
		d, err := ParseDefinition(raw, func(s string) bool { return SignalMetaOf(s).Known })
		if err != nil {
			return nil, err // an active definition always validated at submission; a failure here is a rule engine error
		}
		r.Definition, r.HoldDuration = d, time.Duration(secs)*time.Second
		out = append(out, r)
	}
	return out, rows.Err()
}

func subjectSignals(ctx context.Context, q pgx.Tx, subjectType, subjectID string, since time.Time) ([]SignalFact, error) {
	rows, err := q.Query(ctx, `SELECT id, signal_type, observed_at, confidence, facts FROM risk.risk_signals
		WHERE subject_type = $1 AND subject_id = $2 AND observed_at >= $3 ORDER BY observed_at DESC, id DESC LIMIT $4`,
		subjectType, subjectID, since, MaxSignalsInput)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SignalFact
	for rows.Next() {
		var f SignalFact
		var raw []byte
		if err := rows.Scan(&f.ID, &f.Type, &f.ObservedAt, &f.Confidence, &raw); err != nil {
			return nil, err
		}
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.UseNumber()
		_ = dec.Decode(&f.Facts)
		out = append(out, f)
	}
	return out, rows.Err()
}

func subjectState(ctx context.Context, q pgx.Tx, subjectType, subjectID string) (State, error) {
	var st State
	var screening, prior *string
	err := q.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM risk.risk_holds WHERE subject_type = $1 AND subject_id = $2 AND status IN ('ACTIVE','UNDER_REVIEW','RELEASE_PENDING')),
		(SELECT count(*) FROM risk.risk_cases WHERE subject_type = $1 AND subject_id = $2 AND status NOT IN ('RESOLVED','CLOSED')),
		(SELECT status FROM risk.risk_screening_requests WHERE subject_type = $1 AND subject_id = $2 ORDER BY created_at DESC LIMIT 1),
		(SELECT classification FROM risk.risk_subject_profiles WHERE subject_type = $1 AND subject_id = $2)`,
		subjectType, subjectID).Scan(&st.ActiveHolds, &st.OpenCases, &screening, &prior)
	if screening != nil {
		st.ScreeningStatus = *screening
	} else {
		st.ScreeningStatus = "NOT_REQUESTED"
	}
	if prior != nil {
		st.PriorClassification = *prior
	}
	return st, err
}

// bands returns approved band limits, or the engineering defaults when they are absent or inconsistent.
func bandsAt(ctx context.Context, tx pgx.Tx, at time.Time) (Bands, error) {
	lim, err := loadLimits(ctx, tx, at, []string{BandLimitMedium, BandLimitHigh, BandLimitCritical})
	if err != nil {
		return Bands{}, err
	}
	m, ok1 := lim.Values[BandLimitMedium]
	h, ok2 := lim.Values[BandLimitHigh]
	c, ok3 := lim.Values[BandLimitCritical]
	if ok1 && ok2 && ok3 {
		b := Bands{Medium: int(m), High: int(h), Critical: int(c), Source: "LIMITS"}
		if ValidBands(b) {
			return b, nil
		}
	}
	return DefaultBands, nil
}

// evaluateSignal runs the engine for the signal's subject (no ACTIVE rule for the subject type: nothing to do).
func (s *Service) evaluateSignal(ctx context.Context, tx pgx.Tx, sig Signal, sigID string, out *Outcome) error {
	rules, err := activeRules(ctx, tx, sig.SubjectType)
	if err != nil || len(rules) == 0 {
		return err
	}
	now := s.Clock.Now().UTC()
	sigs, err := subjectSignals(ctx, tx, sig.SubjectType, sig.SubjectID, now.Add(-MaxWindowOf(rules)))
	if err != nil {
		return err
	}
	st, err := subjectState(ctx, tx, sig.SubjectType, sig.SubjectID)
	if err != nil {
		return err
	}
	bands, err := bandsAt(ctx, tx, now)
	if err != nil {
		return err
	}
	ev := EvaluateRules(now, sigs, st, rules, bands)
	results, _ := json.Marshal(ev.Outcomes)
	expl, _ := json.Marshal(ev.Factors)
	versionIDs := make([]string, 0, len(rules))
	for _, r := range rules {
		versionIDs = append(versionIDs, r.VersionID)
	}
	evalID := ids.New()
	tag, err := tx.Exec(ctx, `INSERT INTO risk.risk_evaluations (id, subject_type, subject_id, trigger_signal_id, engine_version, rule_version_ids,
		results, score, classification, band_source, confidence, recommended_action, explanation, inputs_hash, evaluated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
		ON CONFLICT ON CONSTRAINT uq_risk_evaluations_trigger DO NOTHING`,
		evalID, sig.SubjectType, sig.SubjectID, sigID, EngineVersion, versionIDs, results, ev.Score, ev.Classification, ev.BandSource,
		ev.Confidence, ev.RecommendedAction, expl, ev.InputsHash[:], now)
	if err != nil || tag.RowsAffected() == 0 {
		return err
	}
	out.EvaluationID, out.Score2, out.Classification, out.RecommendedAction = evalID, ev.Score, ev.Classification, ev.RecommendedAction
	if _, err := tx.Exec(ctx, `INSERT INTO risk.risk_subject_profiles (subject_type, subject_id, latest_evaluation_id, score, classification,
		evaluated_at) VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (subject_type, subject_id) DO UPDATE SET latest_evaluation_id = EXCLUDED.latest_evaluation_id, score = EXCLUDED.score,
		  classification = EXCLUDED.classification, evaluated_at = EXCLUDED.evaluated_at, updated_at = now()
		WHERE risk.risk_subject_profiles.evaluated_at <= EXCLUDED.evaluated_at`,
		sig.SubjectType, sig.SubjectID, evalID, ev.Score, ev.Classification, now); err != nil {
		return err
	}
	if err := s.applyActions(ctx, tx, sig, evalID, ev, now, out); err != nil {
		return err
	}
	_, err = outbox.Write(ctx, tx, outbox.Event{AggregateType: "risk_evaluation", AggregateID: evalID, EventType: EvEvaluationRecorded,
		Payload: map[string]any{"evaluation_id": evalID, "subject_type": sig.SubjectType, "subject_id": sig.SubjectID, "score": ev.Score,
			"classification": ev.Classification, "recommended_action": ev.RecommendedAction, "case_id": out.CaseID, "hold_id": out.HoldID},
		CorrelationID: sig.CorrelationID, OccurredAt: now})
	return err
}

// applyActions performs the automated actions an evaluation is authorised to take (ADR-045 §5): never a ban, a
// confiscation, a fraud finding, a report or a campaign termination.
func (s *Service) applyActions(ctx context.Context, tx pgx.Tx, sig Signal, evalID string, ev EngineResult, now time.Time, out *Outcome) error {
	action := func(typ, caseID, holdID string, detail map[string]any) error {
		b, _ := json.Marshal(nonNil(detail))
		_, err := tx.Exec(ctx, `INSERT INTO risk.risk_evaluation_actions (id, evaluation_id, action_type, automated, case_id, hold_id, detail)
			VALUES ($1, $2, $3, true, nullif($4, '')::uuid, nullif($5, '')::uuid, $6) ON CONFLICT ON CONSTRAINT uq_risk_evaluation_actions DO NOTHING`,
			ids.New(), evalID, typ, caseID, holdID, b)
		return err
	}
	switch ev.RecommendedAction {
	case ActAllow:
		return action("NO_ACTION", "", "", nil)
	case ActMonitor:
		if err := s.monitor(ctx, tx, "RULE_MONITORING_MATCH", SevLow, sig.SubjectType, sig.SubjectID, "evaluation:"+evalID,
			map[string]any{"evaluation_id": evalID, "score": ev.Score, "classification": ev.Classification}, now); err != nil {
			return err
		}
		return action("MONITORING_RECORDED", "", "", nil)
	}
	// REVIEW_REQUIRED, TEMPORARY_HOLD, ESCALATE, BLOCK: a human must look at it — open (or link to) one case
	priority, caseType := "P4", caseTypeFor(sig.Category)
	for _, r := range ev.Fired {
		if priorityRank[r.Priority] < priorityRank[priority] {
			priority = r.Priority
		}
	}
	if ev.RecommendedAction == ActBlock || ev.RecommendedAction == ActEscalate || ev.Classification == ClassCritical {
		priority = "P1"
	}
	caseID, created, err := s.openOrLinkCase(ctx, tx, caseSpec{CaseType: caseType, SubjectType: caseSubject(sig.SubjectType), SubjectID: sig.SubjectID,
		Priority: priority, Score: ev.Score, Classification: ev.Classification, Recommended: ev.RecommendedAction, OpenedByType: "SYSTEM",
		OpenedBy: "risk.engine"}, now)
	if err != nil {
		return err
	}
	out.CaseID = caseID
	if err := s.addEvidence(ctx, tx, caseID, "EVALUATION", "risk_evaluation", evalID, map[string]any{"score": ev.Score,
		"classification": ev.Classification, "recommended_action": ev.RecommendedAction, "factors": ev.Factors}, "risk.engine", "SYSTEM", "risk.engine", now); err != nil {
		return err
	}
	for _, o := range ev.Outcomes {
		for _, sid := range o.SignalIDs {
			if err := s.addEvidence(ctx, tx, caseID, "SIGNAL", "risk_signal", sid, map[string]any{"rule_code": o.RuleCode}, "risk.engine", "SYSTEM",
				"risk.engine", now); err != nil {
				return err
			}
		}
	}
	if err := action(map[bool]string{true: "CASE_OPENED", false: "CASE_LINKED"}[created], caseID, "", nil); err != nil {
		return err
	}
	// temporary payout-protective holds, only where a fired rule asks for one
	for _, r := range ev.Fired {
		if r.Action != ActTemporaryHold || r.HoldType == "" {
			continue
		}
		subjectType, subjectID := holdSubject(r.HoldType, sig, ev)
		if subjectID == "" {
			if err := s.monitor(ctx, tx, "RULE_MONITORING_MATCH", SevMed, sig.SubjectType, sig.SubjectID, "hold-subject-missing:"+evalID+":"+r.VersionID,
				map[string]any{"evaluation_id": evalID, "hold_type": r.HoldType, "detail": "no subject for the hold in the matched signals"}, now); err != nil {
				return err
			}
			continue
		}
		holdID, made, err := s.createHold(ctx, tx, holdSpec{HoldType: r.HoldType, SubjectType: subjectType, SubjectID: subjectID,
			ReasonCode: "RULE_" + r.RuleCode, Automated: true, EvaluationID: evalID, CaseID: caseID, CreatedByType: "SYSTEM", CreatedBy: "risk.engine",
			Duration: r.HoldDuration, Description: "Automated temporary hold from rule " + r.RuleCode + " (human review required)"}, now)
		if err != nil {
			return err
		}
		out.HoldID = holdID
		if err := action(map[bool]string{true: "HOLD_CREATED", false: "HOLD_EXISTING"}[made], caseID, holdID, map[string]any{"hold_type": r.HoldType}); err != nil {
			return err
		}
	}
	return audit.Record(ctx, tx, audit.Event{Action: "risk.evaluation.actioned", ActorType: "system", TargetType: "risk_evaluation", TargetID: evalID,
		Metadata: map[string]any{"subject_type": sig.SubjectType, "subject_id": sig.SubjectID, "score": ev.Score, "classification": ev.Classification,
			"recommended_action": ev.RecommendedAction, "case_id": caseID, "hold_id": out.HoldID}})
}

// holdSubject picks what an automated hold applies to: a WITHDRAWAL_HOLD on the evaluated owner (user or
// organisation) or campaign; a PAYOUT_DESTINATION_HOLD on the destination named by the latest matching signal.
func holdSubject(holdType string, sig Signal, ev EngineResult) (string, string) {
	switch holdType {
	case "WITHDRAWAL_HOLD":
		switch sig.SubjectType {
		case ownerUser, ownerOrg, subjectCampaign:
			return sig.SubjectType, sig.SubjectID
		}
	case "PAYOUT_DESTINATION_HOLD":
		if sig.SubjectType == subjectDest {
			return subjectDest, sig.SubjectID
		}
		if d, ok := sig.Facts["destination_id"].(string); ok && ids.Valid(d) {
			return subjectDest, d
		}
	}
	return "", ""
}

func caseTypeFor(category string) string {
	switch category {
	case CatCampaign:
		return "CAMPAIGN_RISK"
	case CatSession, CatDevice:
		return "ACCOUNT_TAKEOVER"
	case CatDestination:
		return "DESTINATION_RISK"
	case CatWithdrawal:
		return "WITHDRAWAL_RISK"
	case CatFee:
		return "FEE_MANIPULATION"
	case CatIdentity:
		return "IDENTITY_RISK"
	case CatCompliance:
		return "SCREENING_REVIEW"
	}
	return "TRANSACTION_MONITORING"
}

// caseSubject maps a signal subject to a case subject (cases cover the parties and objects risk works on).
func caseSubject(t string) string { return t }

// monitor records a deduplicated monitoring event.
func (s *Service) monitor(ctx context.Context, q pgx.Tx, typ, severity, subjectType, subjectID, dedupe string, detail map[string]any, now time.Time) error {
	b, _ := json.Marshal(nonNil(detail))
	var st, sid any
	if subjectType != "" && ids.Valid(subjectID) {
		st, sid = subjectType, subjectID
	}
	_, err := q.Exec(ctx, `INSERT INTO risk.risk_monitoring_events (id, event_type, severity, subject_type, subject_id, detail, dedupe_key, occurred_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8) ON CONFLICT ON CONSTRAINT uq_risk_monitoring_events_dedupe DO NOTHING`,
		ids.New(), typ, severity, st, sid, b, typ+":"+dedupe, now)
	return err
}

// event appends to the risk domain history.
func riskEvent(ctx context.Context, tx pgx.Tx, aggType, aggID, evType, actorType, actorID string, detail map[string]any, at time.Time) error {
	b, _ := json.Marshal(nonNil(detail))
	_, err := tx.Exec(ctx, `INSERT INTO risk.risk_events (id, aggregate_type, aggregate_id, event_type, actor_type, actor_id, detail, occurred_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`, ids.New(), aggType, aggID, evType, actorType, actorID, b, at)
	return err
}

func isNoRows(err error) bool { return errors.Is(err, pgx.ErrNoRows) }
