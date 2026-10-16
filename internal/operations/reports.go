package operations

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Fatifizo/fundzim/internal/audit"
	"github.com/Fatifizo/fundzim/internal/platform/opsview"
)

// Operational reports (brief §56–§58). Every report states its source, time range, filters, environment, freshness and
// limitations. Figures are counts of records; financial ones are TEST_ONLY; currencies are never combined; no SLA
// compliance is claimed where no approved target exists.

// ReportRow is one figure.
type ReportRow struct {
	Group  string `json:"group"`
	Label  string `json:"label"`
	Value  *int64 `json:"value"`
	Unit   string `json:"unit"` // count | seconds
	Nature string `json:"nature"`
}

// Report is one operational report.
type Report struct {
	Key         string            `json:"key"`
	Title       string            `json:"title"`
	GeneratedAt time.Time         `json:"generated_at"`
	From        *time.Time        `json:"from"`
	To          *time.Time        `json:"to"`
	Filters     map[string]string `json:"filters"`
	Environment string            `json:"environment"`
	Source      string            `json:"source"`
	Freshness   string            `json:"freshness"`
	Limitations []string          `json:"limitations"`
	Rows        []ReportRow       `json:"rows"`
	Permission  string            `json:"-"`
}

// ReportKeys lists the reports.
var ReportKeys = []string{"campaign_moderation_backlog", "verification_processing", "risk_investigation_backlog", "payment_exceptions",
	"payout_exceptions", "financial_reconciliation", "support_case_volumes", "approval_queue_age", "operational_incidents", "staff_access_review"}

// Reports builds the reports the viewer may see for a time range (default: the last 30 days; at most 366).
func (s *Service) Reports(ctx context.Context, v Viewer, key string, from, to *time.Time) ([]Report, error) {
	now := s.now()
	if to == nil {
		t := now
		to = &t
	}
	if from == nil {
		f := to.Add(-30 * 24 * time.Hour)
		from = &f
	}
	if to.Before(*from) || to.Sub(*from) > 366*24*time.Hour {
		return nil, validation("from", "INVALID_RANGE")
	}
	c := s.collect(ctx)
	byKey := map[string]opsview.Metric{}
	exceptions := map[string][]opsview.Exception{}
	approvals := []opsview.Approval{}
	for _, snap := range c.snaps {
		for _, m := range snap.Metrics {
			byKey[m.Key] = m
		}
		exceptions[snap.Module] = append(exceptions[snap.Module], snap.Exceptions...)
		approvals = append(approvals, snap.Approvals...)
	}
	unavailable := []string{}
	for _, st := range c.statuses {
		if st.Status != opsview.Healthy {
			unavailable = append(unavailable, st.Module)
		}
	}
	base := func(k, title, src, perm string, lim ...string) Report {
		r := Report{Key: k, Title: title, GeneratedAt: now, Environment: "TEST", Source: src, Freshness: "computed at request time",
			Filters: map[string]string{}, Limitations: lim, Rows: []ReportRow{}, Permission: perm}
		if len(unavailable) > 0 {
			r.Limitations = append(r.Limitations, "modules not readable now (their figures are missing, not zero): "+strings.Join(unavailable, ", "))
		}
		return r
	}
	metricRows := func(r *Report, group string, keys ...string) {
		for _, k := range keys {
			if m, ok := byKey[k]; ok {
				r.Rows = append(r.Rows, ReportRow{Group: group, Label: m.Label, Value: m.Value, Unit: "count", Nature: m.Nature})
			}
		}
	}
	exceptionRows := func(r *Report, module string) {
		counts := map[string]int64{}
		for _, e := range exceptions[module] {
			counts[e.Severity+" · "+e.Category]++
		}
		ks := make([]string, 0, len(counts))
		for k := range counts {
			ks = append(ks, k)
		}
		sort.Strings(ks)
		for _, k := range ks {
			r.Rows = append(r.Rows, ReportRow{Group: module, Label: k, Value: opsview.Int64(counts[k]), Unit: "count", Nature: opsview.TestOnly})
		}
	}
	var out []Report
	pointInTime := "a point-in-time count of open items (the time range does not apply)"

	r := base("campaign_moderation_backlog", "Campaign moderation backlog", "campaigns module snapshot (app.campaign_reviews, reports, updates)",
		"campaign.review", pointInTime, "review ages are shown in the review queue, not here")
	metricRows(&r, "campaigns", "campaigns.reviews.pending", "campaigns.reviews.in_review", "campaigns.reviews.escalated", "campaigns.reviews.second_approval",
		"campaigns.reports.open", "campaigns.updates.pending")
	out = append(out, r)

	r = base("verification_processing", "Verification processing", "kyc and beneficiaries module snapshots", "kyc.case.review", pointInTime,
		"no identity data is reported")
	metricRows(&r, "verification", "kyc.reviews.pending", "kyc.reviews.awaiting_information", "kyb.reviews.pending", "kyb.reviews.awaiting_information",
		"verification.escalated", "beneficiaries.reviews.pending", "payouts.destinations.pending")
	out = append(out, r)

	r = base("risk_investigation_backlog", "Risk investigation backlog", "risk module snapshot", "risk.operations.view", pointInTime,
		"case targets are ENGINEERING_DEFAULT values, not regulatory deadlines", "accuracy of risk rules is NOT_MEASURED")
	metricRows(&r, "risk", "risk.cases.open", "risk.cases.high_priority", "risk.cases.overdue", "risk.cases.unassigned_p1", "risk.holds.active",
		"risk.screening.awaiting_review", "risk.rules.active")
	out = append(out, r)

	r = base("payment_exceptions", "Payment exceptions (TEST)", "payments and providers module snapshots", "payment.view", pointInTime,
		"TEST payments through the sandbox simulator only")
	metricRows(&r, "payments", "payments.unknown", "payments.reconciliation.open", "providers.webhooks.dead_lettered")
	exceptionRows(&r, "payments")
	out = append(out, r)

	r = base("payout_exceptions", "Payout exceptions (TEST)", "payouts module snapshot", "payout.view", pointInTime, "TEST payouts through the simulator only")
	metricRows(&r, "payouts", "payouts.unknown", "payouts.exceptions.open")
	exceptionRows(&r, "payouts")
	out = append(out, r)

	r = base("financial_reconciliation", "Financial reconciliation status (TEST)", "ledger, fees, payments and payouts module snapshots", "ledger.view",
		pointInTime, "TEST ledger and TEST fees: never business performance", "settlement reconciliation is NOT_PERFORMED (no settlement evidence)",
		"currencies are never combined; this report counts items, not amounts")
	metricRows(&r, "reconciliation", "ledger.reconciliation.open", "ledger.integrity", "fees.reconciliation.open", "payments.reconciliation.open",
		"payouts.exceptions.open")
	out = append(out, r)

	r = base("support_case_volumes", "Support case volumes", "app.ops_support_cases (created in the range)", PermSupportView)
	r.From, r.To = from, to
	if err := s.supportRows(ctx, &r, *from, *to); err != nil {
		return nil, err
	}
	out = append(out, r)

	r = base("approval_queue_age", "Approval queue age", "module snapshots (pending maker-checker decisions)", PermDashboard, pointInTime,
		"turnaround of decided approvals is not aggregated across modules (each module keeps its own history)")
	buckets := map[string]int64{"under 1 hour": 0, "1–24 hours": 0, "1–7 days": 0, "over 7 days": 0}
	order := []string{"under 1 hour", "1–24 hours", "1–7 days", "over 7 days"}
	for _, a := range approvals {
		if !v.Can(a.RequiredPermission) {
			continue
		}
		age := now.Sub(a.RequestedAt)
		switch {
		case age < time.Hour:
			buckets["under 1 hour"]++
		case age < 24*time.Hour:
			buckets["1–24 hours"]++
		case age < 7*24*time.Hour:
			buckets["1–7 days"]++
		default:
			buckets["over 7 days"]++
		}
	}
	for _, k := range order {
		r.Rows = append(r.Rows, ReportRow{Group: "pending approvals you may decide or see", Label: k, Value: opsview.Int64(buckets[k]), Unit: "count",
			Nature: opsview.Real})
	}
	out = append(out, r)

	r = base("operational_incidents", "Operational incidents", "app.ops_incidents (started in the range)", PermIncidentView)
	r.From, r.To = from, to
	if err := s.incidentRows(ctx, &r, *from, *to); err != nil {
		return nil, err
	}
	out = append(out, r)

	r = base("staff_access_review", "Staff access review", "app.ops_access_reviews and items (created in the range)", PermStaffView,
		"no review frequency is defined (none is invented)")
	r.From, r.To = from, to
	if err := s.reviewRows(ctx, &r, *from, *to); err != nil {
		return nil, err
	}
	out = append(out, r)

	res := []Report{}
	for _, x := range out {
		if (key == "" || x.Key == key) && v.Can(x.Permission) {
			res = append(res, x)
		}
	}
	return res, nil
}

func (s *Service) supportRows(ctx context.Context, r *Report, from, to time.Time) error {
	rows, err := s.Pool.Query(ctx, `SELECT 'by status', status, count(*) FROM app.ops_support_cases WHERE created_at >= $1 AND created_at < $2 GROUP BY status
		UNION ALL SELECT 'by category', category, count(*) FROM app.ops_support_cases WHERE created_at >= $1 AND created_at < $2 GROUP BY category
		ORDER BY 1, 2`, from, to)
	if err != nil {
		return err
	}
	for rows.Next() {
		var g, l string
		var n int64
		if err := rows.Scan(&g, &l, &n); err != nil {
			rows.Close()
			return err
		}
		r.Rows = append(r.Rows, ReportRow{Group: g, Label: l, Value: opsview.Int64(n), Unit: "count", Nature: opsview.Real})
	}
	rows.Close()
	var median *float64
	var target *int64
	var within, total int64
	if err := s.Pool.QueryRow(ctx, `SELECT percentile_cont(0.5) WITHIN GROUP (ORDER BY extract(epoch FROM resolved_at - created_at)),
		(SELECT target_seconds FROM app.ops_sla_targets WHERE metric_key = 'SUPPORT_RESOLUTION' AND status = 'ACTIVE'),
		count(*) FILTER (WHERE resolved_at - created_at <= make_interval(secs => coalesce((SELECT target_seconds FROM app.ops_sla_targets
		  WHERE metric_key = 'SUPPORT_RESOLUTION' AND status = 'ACTIVE'), 0))), count(*)
		FROM app.ops_support_cases WHERE resolved_at IS NOT NULL AND created_at >= $1 AND created_at < $2`, from, to).Scan(&median, &target, &within, &total); err != nil {
		return err
	}
	if median != nil {
		r.Rows = append(r.Rows, ReportRow{Group: "resolution", Label: "median time to resolution", Value: opsview.Int64(int64(*median)), Unit: "seconds",
			Nature: opsview.Real})
	}
	if target == nil {
		r.Limitations = append(r.Limitations, "no approved SUPPORT_RESOLUTION target: SLA attainment is NOT_CONFIGURED")
	} else {
		r.Rows = append(r.Rows, ReportRow{Group: "internal target", Label: "resolved within the internal target", Value: opsview.Int64(within), Unit: "count",
			Nature: opsview.Real}, ReportRow{Group: "internal target", Label: "resolved in the range", Value: opsview.Int64(total), Unit: "count", Nature: opsview.Real})
		r.Limitations = append(r.Limitations, "the target is an internal operational target, not a contractual or regulatory commitment")
	}
	return nil
}

func (s *Service) incidentRows(ctx context.Context, r *Report, from, to time.Time) error {
	rows, err := s.Pool.Query(ctx, `SELECT 'by severity', severity, count(*) FROM app.ops_incidents WHERE started_at >= $1 AND started_at < $2 GROUP BY severity
		UNION ALL SELECT 'by status', status, count(*) FROM app.ops_incidents WHERE started_at >= $1 AND started_at < $2 GROUP BY status ORDER BY 1, 2`, from, to)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var g, l string
		var n int64
		if err := rows.Scan(&g, &l, &n); err != nil {
			return err
		}
		r.Rows = append(r.Rows, ReportRow{Group: g, Label: l, Value: opsview.Int64(n), Unit: "count", Nature: opsview.Real})
	}
	return rows.Err()
}

func (s *Service) reviewRows(ctx context.Context, r *Report, from, to time.Time) error {
	rows, err := s.Pool.Query(ctx, `SELECT 'items by decision', i.decision, count(*) FROM app.ops_access_review_items i JOIN app.ops_access_reviews a ON a.id = i.review_id
		WHERE a.created_at >= $1 AND a.created_at < $2 GROUP BY i.decision
		UNION ALL SELECT 'reviews by status', status, count(*) FROM app.ops_access_reviews WHERE created_at >= $1 AND created_at < $2 GROUP BY status ORDER BY 1, 2`, from, to)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var g, l string
		var n int64
		if err := rows.Scan(&g, &l, &n); err != nil {
			return err
		}
		r.Rows = append(r.Rows, ReportRow{Group: g, Label: l, Value: opsview.Int64(n), Unit: "count", Nature: opsview.Real})
	}
	return rows.Err()
}

// ---- global search ----------------------------------------------------------------------------------------------------

// SearchResult is the global search response.
type SearchResult struct {
	Query   string              `json:"query"`
	Hits    []opsview.SearchHit `json:"hits"`
	Sources []SourceStatus      `json:"sources"`
	Notice  string              `json:"notice"`
}

// Search fans the query out to every module the viewer may search (identifiers and campaign titles only; at most 20
// hits per module; no email, phone or document search). The search itself is audited (its kind and hit count, never
// the text).
func (s *Service) Search(ctx context.Context, v Viewer, q string) (SearchResult, error) {
	q = strings.TrimSpace(q)
	if n := len([]rune(q)); n < 3 || n > 100 {
		return SearchResult{}, validation("q", "INVALID_LENGTH")
	}
	res := SearchResult{Query: q, Hits: []opsview.SearchHit{}, Sources: []SourceStatus{},
		Notice: "Search covers identifiers (case numbers, record ids) and campaign titles. People cannot be searched by email, phone or document."}
	to := s.SourceTimeout
	if to <= 0 {
		to = 3 * time.Second
	}
	for _, src := range s.Sources {
		if src.Search == nil || !v.Can(src.SearchPermission) {
			continue
		}
		c, cancel := context.WithTimeout(ctx, to)
		hits, err := src.Search(c, q)
		cancel()
		if err != nil {
			res.Sources = append(res.Sources, SourceStatus{Module: src.Module, Status: opsview.Unavailable, Error: "SOURCE_UNAVAILABLE"})
			continue
		}
		res.Sources = append(res.Sources, SourceStatus{Module: src.Module, Status: opsview.Healthy})
		for i, h := range hits {
			if i >= 20 {
				break
			}
			if v.Can(h.Permission) {
				res.Hits = append(res.Hits, h)
			}
		}
	}
	if v.Can(PermSupportView) {
		cases, _, err := s.SupportCases(ctx, SupportFilter{Query: q, Limit: 20})
		if err == nil {
			for _, c := range cases {
				res.Hits = append(res.Hits, opsview.SearchHit{Kind: "SUPPORT_CASE", ID: c.ID, Label: "Support case " + itoa(c.CaseNumber) + ": " + c.Subject,
					Status: c.Status, Permission: PermSupportView, Href: "/admin/support/" + c.ID})
			}
		}
	}
	if v.Can(PermAuditRead) {
		evs, _, err := audit.Search(ctx, s.Pool, audit.SearchFilter{CorrelationID: q, Limit: 20})
		if err == nil {
			for _, e := range evs {
				res.Hits = append(res.Hits, opsview.SearchHit{Kind: "AUDIT_EVENT", ID: e.ID, Label: e.Action + " · " + e.TargetType, Status: e.Outcome,
					Permission: PermAuditRead, Href: "/admin/audit?correlation_id=" + q})
			}
		}
	}
	kind := "TEXT"
	if isUUIDish(q) {
		kind = "IDENTIFIER"
	}
	if err := s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return audit.Record(ctx, tx, audit.Event{Action: "ops.search.performed", TargetType: "ops_search", Metadata: map[string]any{"kind": kind,
			"hits": len(res.Hits)}})
	}); err != nil {
		return SearchResult{}, err
	}
	return res, nil
}

func isUUIDish(q string) bool {
	return len(q) == 36 && strings.Count(q, "-") == 4
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
