package operations

import (
	"context"
	"sort"
	"time"

	"github.com/Fatifizo/fundzim/internal/platform/opsview"
)

// Overview is the operations command center (GET /api/v1/admin/operations/overview).
type Overview struct {
	GeneratedAt time.Time        `json:"generated_at"`
	Environment string           `json:"environment"`
	Metrics     []opsview.Metric `json:"metrics"`
	Sources     []SourceStatus   `json:"sources"`
	Alerts      []Alert          `json:"alerts"`
	Tasks       Tasks            `json:"tasks"`
	Notice      string           `json:"notice"`
}

// OverviewNotice accompanies every command center view.
const OverviewNotice = "Counts are operational records in this TEST environment. Financial figures are TEST_ONLY or SIMULATED and are " +
	"never business performance. Live donations, real payouts, production ledger posting and production screening are BLOCKED."

// Tasks are the viewer's in-app task indicators (no email, SMS or WhatsApp: Stage 15).
type Tasks struct {
	ApprovalsToDecide     int `json:"approvals_to_decide"`
	SupportAssigned       int `json:"support_cases_assigned"`
	SupportOverdue        int `json:"support_cases_overdue"`
	ExceptionsAssigned    int `json:"exceptions_assigned"`
	IncidentsOwned        int `json:"incidents_owned"`
	AccessReviewItems     int `json:"access_review_items_pending"`
	OpenIncidentsAffected int `json:"open_incidents"`
}

// Overview builds the command center for a viewer: only metrics the viewer may see, each with its source.
func (s *Service) Overview(ctx context.Context, v Viewer) (Overview, error) {
	c := s.collect(ctx)
	o := Overview{GeneratedAt: s.now(), Environment: "TEST", Metrics: []opsview.Metric{}, Sources: c.statuses, Notice: OverviewNotice}
	for _, snap := range c.snaps {
		for _, m := range snap.Metrics {
			if v.Can(m.Permission) {
				o.Metrics = append(o.Metrics, m)
			}
		}
	}
	own, err := s.ownMetrics(ctx)
	if err != nil {
		return Overview{}, err
	}
	for _, m := range own {
		if v.Can(m.Permission) {
			o.Metrics = append(o.Metrics, m)
		}
	}
	// a module that could not be read shows as UNAVAILABLE with no figures (never zero)
	for _, st := range c.statuses {
		if st.Status != opsview.Healthy {
			o.Metrics = append(o.Metrics, opsview.Metric{Key: st.Module + ".source", Label: "Data from " + st.Module, Module: st.Module,
				Nature: opsview.Unknown, Status: opsview.Unavailable, Source: "module snapshot could not be read", Permission: PermDashboard})
		}
	}
	sort.SliceStable(o.Metrics, func(i, j int) bool { return o.Metrics[i].Module < o.Metrics[j].Module })
	h, err := s.Health(ctx)
	if err != nil {
		return Overview{}, err
	}
	o.Alerts = s.alerts(ctx, h, c.snaps)
	if o.Tasks, err = s.tasks(ctx, v, c); err != nil {
		return Overview{}, err
	}
	return o, nil
}

// ownMetrics are the operations module's own counts.
func (s *Service) ownMetrics(ctx context.Context) ([]opsview.Metric, error) {
	var open, unassigned, overdue, escalated, incidents, triage, reviews int64
	if err := s.Pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM app.ops_support_cases WHERE status NOT IN ('RESOLVED','CLOSED')),
		(SELECT count(*) FROM app.ops_support_cases WHERE status NOT IN ('RESOLVED','CLOSED') AND assigned_to IS NULL),
		(SELECT count(*) FROM app.ops_support_cases WHERE status NOT IN ('RESOLVED','CLOSED') AND due_at < now()),
		(SELECT count(*) FROM app.ops_support_cases WHERE status = 'ESCALATED'),
		(SELECT count(*) FROM app.ops_incidents WHERE status NOT IN ('RESOLVED','CLOSED')),
		(SELECT count(*) FROM app.ops_exception_assignments WHERE status NOT IN ('RESOLVED','CLOSED')),
		(SELECT count(*) FROM app.ops_access_reviews WHERE status = 'OPEN')`).
		Scan(&open, &unassigned, &overdue, &escalated, &incidents, &triage, &reviews); err != nil {
		return nil, err
	}
	const m = "operations"
	return []opsview.Metric{
		opsview.Count(m, "support.open", "Open support cases", open, PermSupportView, "app.ops_support_cases not RESOLVED/CLOSED", "/admin/support"),
		opsview.Count(m, "support.unassigned", "Unassigned support cases", unassigned, PermSupportView, "open app.ops_support_cases without an assignee", "/admin/support"),
		opsview.Count(m, "support.overdue", "Support cases past their internal target", overdue, PermSupportView,
			"open app.ops_support_cases with due_at in the past (approved SLA targets only)", "/admin/support"),
		opsview.Count(m, "support.escalated", "Escalated support cases", escalated, PermSupportView, "app.ops_support_cases ESCALATED", "/admin/support"),
		opsview.Count(m, "incidents.open", "Open operational incidents", incidents, PermIncidentView, "app.ops_incidents not RESOLVED/CLOSED", "/admin/operations/incidents"),
		opsview.Count(m, "exceptions.in_triage", "Exceptions in operations triage", triage, PermDashboard, "app.ops_exception_assignments not RESOLVED/CLOSED",
			"/admin/operations/exceptions"),
		opsview.Count(m, "access_reviews.open", "Open staff access reviews", reviews, PermStaffView, "app.ops_access_reviews OPEN", "/admin/staff/access-reviews"),
	}, nil
}

func (s *Service) tasks(ctx context.Context, v Viewer, c collected) (Tasks, error) {
	var t Tasks
	for _, snap := range c.snaps {
		for _, a := range snap.Approvals {
			maker := false
			for _, m := range a.Makers {
				maker = maker || (m != "" && v.Identities[m])
			}
			if !maker && v.Can(a.RequiredPermission) {
				t.ApprovalsToDecide++
			}
		}
	}
	err := s.Pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM app.ops_support_cases WHERE assigned_to = $1 AND status NOT IN ('RESOLVED','CLOSED')),
		(SELECT count(*) FROM app.ops_support_cases WHERE assigned_to = $1 AND status NOT IN ('RESOLVED','CLOSED') AND due_at < now()),
		(SELECT count(*) FROM app.ops_exception_assignments WHERE assigned_to = $1 AND status NOT IN ('RESOLVED','CLOSED')),
		(SELECT count(*) FROM app.ops_incidents WHERE owner_id = $1 AND status NOT IN ('RESOLVED','CLOSED')),
		(SELECT count(*) FROM app.ops_access_review_items i JOIN app.ops_access_reviews r ON r.id = i.review_id
		  WHERE r.reviewer_id = $1 AND r.status = 'OPEN' AND i.decision = 'PENDING'),
		(SELECT count(*) FROM app.ops_incidents WHERE status NOT IN ('RESOLVED','CLOSED'))`, v.StaffID).
		Scan(&t.SupportAssigned, &t.SupportOverdue, &t.ExceptionsAssigned, &t.IncidentsOwned, &t.AccessReviewItems, &t.OpenIncidentsAffected)
	return t, err
}

// Metrics returns the flat list of metrics the viewer may see (GET /api/v1/admin/operations/metrics).
func (s *Service) Metrics(ctx context.Context, v Viewer) ([]opsview.Metric, []SourceStatus, error) {
	o, err := s.Overview(ctx, v)
	if err != nil {
		return nil, nil, err
	}
	return o.Metrics, o.Sources, nil
}
