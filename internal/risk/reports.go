package risk

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Fatifizo/fundzim/internal/platform/ids"
)

// Risk operations reporting (brief §45–§48). Counts only: no accuracy, precision or recall is claimed — there is no
// labelled evidence set (false-positive reviews are counted as reviews, not as a measured error rate). Every report
// is TEST data.

// Count is a labelled count.
type Count struct {
	Key   string `json:"key"`
	Count int64  `json:"count"`
}

// Overview is the risk operations dashboard summary.
type Overview struct {
	GeneratedAt            time.Time `json:"generated_at"`
	Environment            string    `json:"environment"`
	SignalsLast24h         []Count   `json:"signals_last_24h_by_category"`
	EvaluationsLast7d      []Count   `json:"evaluations_last_7d_by_classification"`
	OpenCasesByPriority    []Count   `json:"open_cases_by_priority"`
	OpenCasesByStatus      []Count   `json:"open_cases_by_status"`
	OverdueCases           int64     `json:"overdue_cases"`
	OpenHoldsByType        []Count   `json:"open_holds_by_type"`
	ScreeningsByStatus     []Count   `json:"screenings_by_status"`
	MonitoringLast24h      []Count   `json:"monitoring_last_24h_by_type"`
	ActiveRules            int64     `json:"active_rules"`
	PendingRuleApprovals   int64     `json:"pending_rule_approvals"`
	PendingHoldActivations int64     `json:"pending_hold_activations"`
	PendingHoldReleases    int64     `json:"pending_hold_releases"`
	PendingCaseDecisions   int64     `json:"pending_case_decisions"`
	ScreeningProduction    string    `json:"screening_production"` // always BLOCKED
	AccuracyMetrics        string    `json:"accuracy_metrics"`     // always NOT_MEASURED
}

func counts(ctx context.Context, q interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}, sql string, args ...any) ([]Count, error) {
	rows, err := q.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Count{}
	for rows.Next() {
		var c Count
		if err := rows.Scan(&c.Key, &c.Count); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// RiskOverview returns the dashboard summary.
func (s *Service) RiskOverview(ctx context.Context) (Overview, error) {
	now := s.now()
	o := Overview{GeneratedAt: now, Environment: "TEST", ScreeningProduction: "BLOCKED", AccuracyMetrics: "NOT_MEASURED"}
	var err error
	q := []struct {
		dst *[]Count
		sql string
	}{
		{&o.SignalsLast24h, `SELECT category, count(*) FROM risk.risk_signals WHERE recorded_at > $1::timestamptz - interval '24 hours' GROUP BY 1 ORDER BY 1`},
		{&o.EvaluationsLast7d, `SELECT classification, count(*) FROM risk.risk_evaluations WHERE evaluated_at > $1::timestamptz - interval '7 days' GROUP BY 1 ORDER BY 1`},
		{&o.OpenCasesByPriority, `SELECT priority, count(*) FROM risk.risk_cases WHERE status NOT IN ('RESOLVED','CLOSED') AND $1::timestamptz IS NOT NULL GROUP BY 1 ORDER BY 1`},
		{&o.OpenCasesByStatus, `SELECT status, count(*) FROM risk.risk_cases WHERE status NOT IN ('RESOLVED','CLOSED') AND $1::timestamptz IS NOT NULL GROUP BY 1 ORDER BY 1`},
		{&o.OpenHoldsByType, `SELECT hold_type, count(*) FROM risk.risk_holds WHERE status IN ('REQUESTED','ACTIVE','UNDER_REVIEW','RELEASE_PENDING') AND $1::timestamptz IS NOT NULL GROUP BY 1 ORDER BY 1`},
		{&o.ScreeningsByStatus, `SELECT status, count(*) FROM risk.risk_screening_requests WHERE $1::timestamptz IS NOT NULL GROUP BY 1 ORDER BY 1`},
		{&o.MonitoringLast24h, `SELECT event_type, count(*) FROM risk.risk_monitoring_events WHERE occurred_at > $1::timestamptz - interval '24 hours' GROUP BY 1 ORDER BY 1`},
	}
	for _, x := range q {
		if *x.dst, err = counts(ctx, s.Pool, x.sql, now); err != nil {
			return Overview{}, err
		}
	}
	err = s.Pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM risk.risk_cases WHERE status NOT IN ('RESOLVED','CLOSED') AND due_at < $1),
		(SELECT count(*) FROM risk.risk_rule_versions WHERE status = 'ACTIVE'),
		(SELECT count(*) FROM risk.risk_rule_versions WHERE status = 'PENDING_APPROVAL'),
		(SELECT count(*) FROM risk.risk_holds WHERE status = 'REQUESTED'),
		(SELECT count(*) FROM risk.risk_holds WHERE status = 'RELEASE_PENDING'),
		(SELECT count(*) FROM risk.risk_cases WHERE status = 'DECISION_PENDING')`, now).Scan(&o.OverdueCases, &o.ActiveRules, &o.PendingRuleApprovals,
		&o.PendingHoldActivations, &o.PendingHoldReleases, &o.PendingCaseDecisions)
	return o, err
}

// SignalRow is a signal list row (staff; ids and codes only).
type SignalRow struct {
	ID            string          `json:"id"`
	Type          string          `json:"signal_type"`
	Category      string          `json:"category"`
	Severity      string          `json:"severity"`
	Confidence    int             `json:"confidence"`
	Kind          string          `json:"kind"`
	SubjectType   string          `json:"subject_type"`
	SubjectID     string          `json:"subject_id"`
	SourceModule  string          `json:"source_module"`
	SourceEventID string          `json:"source_event_id"`
	ObservedAt    time.Time       `json:"observed_at"`
	RecordedAt    time.Time       `json:"recorded_at"`
	Facts         json.RawMessage `json:"facts"`
	Reviews       []string        `json:"review_classifications"`
}

// SignalFilter filters signals.
type SignalFilter struct {
	Category, SignalType, SubjectType, SubjectID string
	Limit                                        int
}

// Signals lists recent signals (newest first).
func (s *Service) Signals(ctx context.Context, f SignalFilter) ([]SignalRow, error) {
	if f.Limit <= 0 || f.Limit > 200 {
		f.Limit = 100
	}
	sid := ""
	if ids.Valid(f.SubjectID) {
		sid = f.SubjectID
	}
	rows, err := s.Pool.Query(ctx, `SELECT s.id, s.signal_type, s.category, s.severity, s.confidence, s.kind, s.subject_type, s.subject_id,
		s.source_module, s.source_event_id, s.observed_at, s.recorded_at, s.facts,
		coalesce((SELECT array_agg(r.classification ORDER BY r.reviewed_at) FROM risk.risk_signal_reviews r WHERE r.signal_id = s.id), '{}')
		FROM risk.risk_signals s
		WHERE ($1 = '' OR s.category = $1) AND ($2 = '' OR s.signal_type = $2) AND ($3 = '' OR s.subject_type = $3)
		  AND ($4 = '' OR s.subject_id = nullif($4, '')::uuid)
		ORDER BY s.recorded_at DESC, s.id DESC LIMIT $5`, f.Category, f.SignalType, f.SubjectType, sid, f.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SignalRow{}
	for rows.Next() {
		var r SignalRow
		var conf int16
		if err := rows.Scan(&r.ID, &r.Type, &r.Category, &r.Severity, &conf, &r.Kind, &r.SubjectType, &r.SubjectID, &r.SourceModule, &r.SourceEventID,
			&r.ObservedAt, &r.RecordedAt, &r.Facts, &r.Reviews); err != nil {
			return nil, err
		}
		r.Confidence = int(conf)
		out = append(out, r)
	}
	return out, rows.Err()
}

// Evaluation is a stored evaluation with its explanation and actions.
type Evaluation struct {
	ID                string          `json:"id"`
	SubjectType       string          `json:"subject_type"`
	SubjectID         string          `json:"subject_id"`
	TriggerSignalID   string          `json:"trigger_signal_id"`
	EngineVersion     string          `json:"engine_version"`
	RuleVersionIDs    []string        `json:"rule_version_ids"`
	Score             int             `json:"score"`
	Classification    string          `json:"classification"`
	BandSource        string          `json:"band_source"`
	Confidence        int             `json:"confidence"`
	RecommendedAction string          `json:"recommended_action"`
	Results           json.RawMessage `json:"results,omitempty"`
	Explanation       json.RawMessage `json:"explanation"`
	InputsHash        string          `json:"inputs_hash"`
	EvaluatedAt       time.Time       `json:"evaluated_at"`
	Actions           []EvalAction    `json:"actions,omitempty"`
	// Notice is shown with every evaluation.
	Notice string `json:"notice"`
}

// EvalAction is an action taken on an evaluation.
type EvalAction struct {
	ActionType string          `json:"action_type"`
	Automated  bool            `json:"automated"`
	CaseID     *string         `json:"case_id"`
	HoldID     *string         `json:"hold_id"`
	Detail     json.RawMessage `json:"detail"`
}

// EvaluationNotice accompanies every score.
const EvaluationNotice = "A risk score is a prioritisation aid from configured rules over recorded signals. It is not proof of fraud or of any wrongdoing."

const evalCols = `id, subject_type, subject_id, trigger_signal_id, engine_version, rule_version_ids::text[], score, classification, band_source,
	confidence, recommended_action, explanation, inputs_hash, evaluated_at`

func scanEval(r pgx.Row) (Evaluation, error) {
	var e Evaluation
	var score, conf int16
	var h []byte
	err := r.Scan(&e.ID, &e.SubjectType, &e.SubjectID, &e.TriggerSignalID, &e.EngineVersion, &e.RuleVersionIDs, &score, &e.Classification,
		&e.BandSource, &conf, &e.RecommendedAction, &e.Explanation, &h, &e.EvaluatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Evaluation{}, ErrNotFound
	}
	e.Score, e.Confidence, e.InputsHash, e.Notice = int(score), int(conf), hexs(h), EvaluationNotice
	return e, err
}

// Evaluations lists evaluations (newest first).
func (s *Service) Evaluations(ctx context.Context, classification, subjectType, subjectID string, limit int) ([]Evaluation, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	if !ids.Valid(subjectID) {
		subjectID = ""
	}
	rows, err := s.Pool.Query(ctx, `SELECT `+evalCols+` FROM risk.risk_evaluations WHERE ($1 = '' OR classification = $1)
		AND ($2 = '' OR subject_type = $2) AND ($3 = '' OR subject_id = nullif($3, '')::uuid) ORDER BY evaluated_at DESC, id DESC LIMIT $4`,
		classification, subjectType, subjectID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Evaluation{}
	for rows.Next() {
		e, err := scanEval(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// EvaluationDetail returns one evaluation with its per-rule results and actions.
func (s *Service) EvaluationDetail(ctx context.Context, id string) (Evaluation, error) {
	if !ids.Valid(id) {
		return Evaluation{}, ErrNotFound
	}
	e, err := scanEval(s.Pool.QueryRow(ctx, `SELECT `+evalCols+` FROM risk.risk_evaluations WHERE id = $1`, id))
	if err != nil {
		return Evaluation{}, err
	}
	if err := s.Pool.QueryRow(ctx, `SELECT results FROM risk.risk_evaluations WHERE id = $1`, id).Scan(&e.Results); err != nil {
		return Evaluation{}, err
	}
	rows, err := s.Pool.Query(ctx, `SELECT action_type, automated, case_id::text, hold_id::text, detail FROM risk.risk_evaluation_actions
		WHERE evaluation_id = $1 ORDER BY created_at, id`, id)
	if err != nil {
		return Evaluation{}, err
	}
	defer rows.Close()
	e.Actions = []EvalAction{}
	for rows.Next() {
		var a EvalAction
		if err := rows.Scan(&a.ActionType, &a.Automated, &a.CaseID, &a.HoldID, &a.Detail); err != nil {
			return Evaluation{}, err
		}
		e.Actions = append(e.Actions, a)
	}
	return e, rows.Err()
}

// MonitoringEvent is a monitoring event.
type MonitoringEvent struct {
	ID          string          `json:"id"`
	EventType   string          `json:"event_type"`
	Severity    string          `json:"severity"`
	SubjectType *string         `json:"subject_type"`
	SubjectID   *string         `json:"subject_id"`
	Detail      json.RawMessage `json:"detail"`
	OccurredAt  time.Time       `json:"occurred_at"`
}

// Monitoring lists recent monitoring events.
func (s *Service) Monitoring(ctx context.Context, eventType string, limit int) ([]MonitoringEvent, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	rows, err := s.Pool.Query(ctx, `SELECT id, event_type, severity, subject_type, subject_id::text, detail, occurred_at FROM risk.risk_monitoring_events
		WHERE ($1 = '' OR event_type = $1) ORDER BY occurred_at DESC, id DESC LIMIT $2`, eventType, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []MonitoringEvent{}
	for rows.Next() {
		var m MonitoringEvent
		if err := rows.Scan(&m.ID, &m.EventType, &m.Severity, &m.SubjectType, &m.SubjectID, &m.Detail, &m.OccurredAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// RuleQuality is a rule's observed operational counts (not an accuracy measure).
type RuleQuality struct {
	RuleCode            string `json:"rule_code"`
	RuleVersionID       string `json:"rule_version_id"`
	Fired               int64  `json:"fired"`
	CasesResolved       int64  `json:"cases_resolved"`
	FalsePositiveCases  int64  `json:"false_positive_outcomes"`
	ConfirmedRiskCases  int64  `json:"risk_confirmed_outcomes"`
	NoFurtherAction     int64  `json:"no_further_action_outcomes"`
	SignalFalsePositive int64  `json:"signal_false_positive_reviews"`
}

// QualityReport is the model-quality report (brief §48).
type QualityReport struct {
	GeneratedAt time.Time     `json:"generated_at"`
	WindowDays  int           `json:"window_days"`
	Rules       []RuleQuality `json:"rules"`
	// Accuracy is NOT_MEASURED: reviewed outcomes are a small, unrepresentative sample, not labelled evidence.
	Accuracy string `json:"accuracy"`
	Notice   string `json:"notice"`
}

// Quality returns per-rule operational counts over the last `days` days.
func (s *Service) Quality(ctx context.Context, days int) (QualityReport, error) {
	if days <= 0 || days > 365 {
		days = 30
	}
	now := s.now()
	rep := QualityReport{GeneratedAt: now, WindowDays: days, Rules: []RuleQuality{}, Accuracy: "NOT_MEASURED",
		Notice: "Counts of rule firings and human case outcomes. They are not accuracy, precision or recall: no labelled evidence set exists."}
	rows, err := s.Pool.Query(ctx, `WITH fired AS (
		  SELECT e.id AS evaluation_id, (o->>'rule_version_id') AS version_id, (o->>'rule_code') AS rule_code
		  FROM risk.risk_evaluations e, jsonb_array_elements(e.results) o
		  WHERE e.evaluated_at > $1::timestamptz - make_interval(days => $2) AND (o->>'fired')::boolean),
		cases AS (
		  SELECT f.version_id, c.decision_outcome FROM fired f
		  JOIN risk.risk_evaluation_actions a ON a.evaluation_id = f.evaluation_id AND a.case_id IS NOT NULL
		  JOIN risk.risk_cases c ON c.id = a.case_id AND c.decision_outcome IS NOT NULL)
		SELECT f.rule_code, f.version_id, count(*),
		  (SELECT count(*) FROM cases c WHERE c.version_id = f.version_id),
		  (SELECT count(*) FROM cases c WHERE c.version_id = f.version_id AND c.decision_outcome = 'FALSE_POSITIVE'),
		  (SELECT count(*) FROM cases c WHERE c.version_id = f.version_id AND c.decision_outcome IN ('RISK_CONFIRMED_HOLD_MAINTAINED','ESCALATED_TO_COMPLIANCE')),
		  (SELECT count(*) FROM cases c WHERE c.version_id = f.version_id AND c.decision_outcome = 'NO_FURTHER_ACTION')
		FROM fired f GROUP BY f.rule_code, f.version_id ORDER BY f.rule_code, f.version_id`, now, days)
	if err != nil {
		return QualityReport{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var q RuleQuality
		if err := rows.Scan(&q.RuleCode, &q.RuleVersionID, &q.Fired, &q.CasesResolved, &q.FalsePositiveCases, &q.ConfirmedRiskCases,
			&q.NoFurtherAction); err != nil {
			return QualityReport{}, err
		}
		rep.Rules = append(rep.Rules, q)
	}
	if err := rows.Err(); err != nil {
		return QualityReport{}, err
	}
	rows.Close()
	for i := range rep.Rules {
		if err := s.Pool.QueryRow(ctx, `SELECT count(DISTINCT r.id) FROM risk.risk_signal_reviews r
			JOIN risk.risk_evaluations e ON r.signal_id::text IN (SELECT jsonb_array_elements_text(o->'signal_ids') FROM jsonb_array_elements(e.results) o
			  WHERE o->>'rule_version_id' = $1)
			WHERE r.classification = 'FALSE_POSITIVE' AND e.evaluated_at > $2::timestamptz - make_interval(days => $3)`, rep.Rules[i].RuleVersionID, now, days).
			Scan(&rep.Rules[i].SignalFalsePositive); err != nil {
			return QualityReport{}, err
		}
	}
	return rep, nil
}

var classRank = map[string]int{ClassLow: 1, ClassMedium: 2, ClassHigh: 3, ClassCritical: 4}

// HighestClassification returns the highest current classification among the subjects' latest evaluations ("" when
// none was evaluated). Consumers use it only to route to human review (never to refuse outright).
func (s *Service) HighestClassification(ctx context.Context, subjects ...HoldSubject) (string, error) {
	var st, sid []string
	for _, x := range subjects {
		if x.Type != "" && ids.Valid(x.ID) {
			st, sid = append(st, x.Type), append(sid, x.ID)
		}
	}
	if len(st) == 0 {
		return "", nil
	}
	rows, err := s.Pool.Query(ctx, `SELECT classification FROM risk.risk_subject_profiles WHERE (subject_type, subject_id) IN
		(SELECT t, i::uuid FROM unnest($1::text[], $2::text[]) AS u(t, i))`, st, sid)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	best := ""
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			return "", err
		}
		if classRank[c] > classRank[best] {
			best = c
		}
	}
	return best, rows.Err()
}

var screeningRank = map[string]int{ScrNotRequested: 0, ScrCleared: 1, ScrNoMatch: 2, ScrPending: 3, ScrError: 4, ScrPotentialMatch: 5,
	ScrReviewRequired: 6, ScrConfirmedMatch: 7}

// MostSignificantScreening returns the most significant latest screening status among the subjects.
func (s *Service) MostSignificantScreening(ctx context.Context, subjects ...HoldSubject) (string, error) {
	best := ScrNotRequested
	for _, x := range subjects {
		if x.Type == "" || !ids.Valid(x.ID) {
			continue
		}
		st, err := s.ScreeningStatus(ctx, x.Type, x.ID)
		if err != nil {
			return "", err
		}
		if screeningRank[st] > screeningRank[best] {
			best = st
		}
	}
	return best, nil
}

// DegradedEvaluation reports whether any subject's latest evaluation is degraded (a rule version could not be
// evaluated). Financial consumers treat it as "no complete risk decision" and route to human review (fail closed).
func (s *Service) DegradedEvaluation(ctx context.Context, subjects ...HoldSubject) (bool, error) {
	var st, sid []string
	for _, x := range subjects {
		if x.Type != "" && ids.Valid(x.ID) {
			st, sid = append(st, x.Type), append(sid, x.ID)
		}
	}
	if len(st) == 0 {
		return false, nil
	}
	var degraded bool
	err := s.Pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM risk.risk_subject_profiles WHERE degraded AND (subject_type, subject_id) IN
		(SELECT t, i::uuid FROM unnest($1::text[], $2::text[]) AS u(t, i)))`, st, sid).Scan(&degraded)
	return degraded, err
}

// RuleFailure is a recorded rule evaluation failure (operations exception source).
type RuleFailure struct {
	RuleVersionID string    `json:"rule_version_id"`
	RuleCode      string    `json:"rule_code"`
	ErrorCode     string    `json:"error_code"`
	Detail        string    `json:"detail"`
	SubjectType   string    `json:"subject_type"`
	Failures      int64     `json:"failures"`
	FirstAt       time.Time `json:"first_at"`
	LastAt        time.Time `json:"last_at"`
	StillActive   bool      `json:"still_active"`
}

// RuleFailures lists failing rule versions (grouped), newest first.
func (s *Service) RuleFailures(ctx context.Context, limit int) ([]RuleFailure, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	rows, err := s.Pool.Query(ctx, `SELECT f.rule_version_id::text, r.code, f.error_code, max(f.detail), f.subject_type, count(*), min(f.occurred_at),
		max(f.occurred_at), bool_or(v.status = 'ACTIVE')
		FROM risk.risk_rule_failures f JOIN risk.risk_rule_versions v ON v.id = f.rule_version_id JOIN risk.risk_rules r ON r.id = v.rule_id
		GROUP BY f.rule_version_id, r.code, f.error_code, f.subject_type ORDER BY max(f.occurred_at) DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RuleFailure{}
	for rows.Next() {
		var f RuleFailure
		if err := rows.Scan(&f.RuleVersionID, &f.RuleCode, &f.ErrorCode, &f.Detail, &f.SubjectType, &f.Failures, &f.FirstAt, &f.LastAt, &f.StillActive); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}
