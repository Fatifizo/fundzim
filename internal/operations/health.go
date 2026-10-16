package operations

import (
	"context"
	"time"

	"github.com/Fatifizo/fundzim/internal/platform/jobs"
	"github.com/Fatifizo/fundzim/internal/platform/opsview"
	"github.com/Fatifizo/fundzim/internal/platform/outbox"
)

// Platform health (brief §48–§50). Every status comes from a measurement taken now: a probe, the outbox table, the job
// queue. Nothing is reported HEALTHY without evidence; a disabled capability is BLOCKED, never HEALTHY. Thresholds are
// ENGINEERING_DEFAULT values (documented in docs/stage-14/platform-health.md), not service-level commitments.

// Engineering-default thresholds.
const (
	WorkerStaleAfter   = 5 * time.Minute // periodic jobs complete every second (outbox relay): minutes without one is stale
	OutboxBacklogAfter = 5 * time.Minute // the oldest undispatched event older than this is a backlog
)

// Component is one health line.
type Component struct {
	Name     string         `json:"name"`
	Status   string         `json:"status"`
	Detail   string         `json:"detail"`
	Source   string         `json:"source"`
	Observed time.Time      `json:"observed_at"`
	Facts    map[string]any `json:"facts,omitempty"`
}

// HealthReport is the platform health dashboard.
type HealthReport struct {
	GeneratedAt time.Time    `json:"generated_at"`
	Overall     string       `json:"overall"`
	Components  []Component  `json:"components"`
	Outbox      outbox.Stats `json:"outbox"`
	Jobs        jobs.Stats   `json:"jobs"`
	Notice      string       `json:"notice"`
}

// Health measures the platform now.
func (s *Service) Health(ctx context.Context) (HealthReport, error) {
	now := s.now()
	h := HealthReport{GeneratedAt: now, Components: []Component{}, Notice: "Statuses come from checks run now; thresholds are engineering defaults. " +
		"Application error rates are not collected in a queryable store (NOT_AVAILABLE)."}
	h.Components = append(h.Components, Component{Name: "api", Status: opsview.Healthy, Detail: "this response was served by the API", Source: "request",
		Observed: now})
	for _, p := range s.Probes {
		c := Component{Name: p.Name, Source: "probe", Observed: now}
		pc, cancel := context.WithTimeout(ctx, 2*time.Second)
		err := p.Check(pc)
		cancel()
		if err != nil {
			c.Status, c.Detail = opsview.Unavailable, "check failed"
		} else {
			c.Status, c.Detail = opsview.Healthy, "check passed"
		}
		h.Components = append(h.Components, c)
	}
	ob, err := outbox.ReadStats(ctx, s.Pool)
	if err != nil {
		h.Components = append(h.Components, Component{Name: "outbox", Status: opsview.Unavailable, Detail: "outbox state could not be read", Source: "app.outbox_events",
			Observed: now})
	} else {
		h.Outbox = ob
		c := Component{Name: "outbox", Status: opsview.Healthy, Detail: "no backlog", Source: "app.outbox_events", Observed: now,
			Facts: map[string]any{"pending": ob.Pending, "dead_lettered": ob.DeadLettered, "dispatched_last_1h": ob.DispatchedLast1h}}
		switch {
		case ob.DeadLettered > 0:
			c.Status, c.Detail = opsview.Degraded, "dead-lettered events need an operator"
		case ob.OldestPendingAt != nil && now.Sub(*ob.OldestPendingAt) > OutboxBacklogAfter:
			c.Status, c.Detail = opsview.Degraded, "events are waiting longer than the backlog threshold"
		}
		h.Components = append(h.Components, c)
	}
	js, err := jobs.ReadStats(ctx, s.Pool)
	if err != nil {
		h.Components = append(h.Components, Component{Name: "workers", Status: opsview.Unavailable, Detail: "job queue state could not be read",
			Source: jobs.Schema + ".river_job", Observed: now})
	} else {
		h.Jobs = js
		c := Component{Name: "workers", Source: jobs.Schema + ".river_job / river_leader", Observed: now,
			Facts: map[string]any{"available": js.Available, "running": js.Running, "retryable": js.Retryable, "discarded_last_24h": js.DiscardedLast24h}}
		switch {
		case js.LastCompletedAt == nil:
			c.Status, c.Detail = opsview.StatusUnknown, "no completed job recorded"
		case now.Sub(*js.LastCompletedAt) > WorkerStaleAfter:
			c.Status, c.Detail = opsview.Unavailable, "no job completed recently: workers may be stopped"
		case js.DiscardedLast24h > 0:
			c.Status, c.Detail = opsview.Degraded, "jobs failed permanently in the last 24 hours"
		default:
			c.Status, c.Detail = opsview.Healthy, "jobs are completing"
		}
		h.Components = append(h.Components, c)
	}
	h.Components = append(h.Components, Component{Name: "application_errors", Status: opsview.StatusUnknown, Detail: "NOT_AVAILABLE: error rates are exported to Prometheus, not stored for queries",
		Source: "none", Observed: now})
	h.Overall = opsview.Healthy
	for _, c := range h.Components {
		switch c.Status {
		case opsview.Unavailable:
			h.Overall = opsview.Unavailable
		case opsview.Degraded, opsview.StatusUnknown:
			if h.Overall == opsview.Healthy {
				h.Overall = opsview.Degraded
			}
		}
	}
	return h, nil
}

// Alert is an operational alert derived from health and module snapshots (no external delivery: Stage 15).
type Alert struct {
	Key        string    `json:"key"`
	Severity   string    `json:"severity"`
	Title      string    `json:"title"`
	Source     string    `json:"source"`
	ObservedAt time.Time `json:"observed_at"`
	IncidentID *string   `json:"incident_id"`
}

func (s *Service) alerts(ctx context.Context, h HealthReport, snaps []opsview.Snapshot) []Alert {
	now := s.now()
	out := []Alert{}
	add := func(key, sev, title, src string) {
		out = append(out, Alert{Key: key, Severity: sev, Title: title, Source: src, ObservedAt: now})
	}
	for _, c := range h.Components {
		switch {
		case c.Name == "workers" && c.Status == opsview.Unavailable:
			add("WORKER_STOPPED", "SEV1", "No background job has completed recently: workers may be stopped", c.Source)
		case c.Name == "workers" && c.Status == opsview.Degraded:
			add("JOBS_DISCARDED", "SEV3", "Background jobs failed permanently in the last 24 hours", c.Source)
		case c.Name == "outbox" && c.Status == opsview.Degraded:
			add("OUTBOX_BACKLOG", "SEV2", "Outbox events are backing up or dead-lettered", c.Source)
		case c.Status == opsview.Unavailable && c.Source == "probe":
			add("DEPENDENCY_"+c.Name+"_UNAVAILABLE", "SEV1", "Dependency check failed: "+c.Name, c.Source)
		}
	}
	for _, snap := range snaps {
		for _, m := range snap.Metrics {
			switch {
			case m.Key == "ledger.integrity" && m.Status == opsview.Unavailable:
				add("LEDGER_INTEGRITY_FAILURE", "SEV1", "A ledger integrity check failed (TEST ledger)", m.Source)
			case m.Key == "risk.rules.active" && m.Status == opsview.Degraded:
				add("RISK_EVALUATION_FAILURE", "SEV2", "An active risk rule cannot be evaluated: evaluations are degraded", m.Source)
			case m.Key == "risk.cases.unassigned_p1" && m.Value != nil && *m.Value > 0:
				add("RISK_P1_UNASSIGNED", "SEV2", "Critical risk cases have no investigator", m.Source)
			case m.Key == "providers.webhooks.dead_lettered" && m.Value != nil && *m.Value > 0:
				add("PROVIDER_WEBHOOKS_DEAD_LETTERED", "SEV2", "Provider webhooks could not be dispatched", m.Source)
			}
		}
		for _, e := range snap.Exceptions {
			if e.Category == "SCREENING_ERROR" {
				add("SCREENING_UNAVAILABLE", "SEV3", "Screening requests ended in ERROR (TEST simulator; never treated as cleared)", "risk.risk_screening_requests ERROR")
				break
			}
		}
	}
	// link open incidents declared from an alert
	if len(out) > 0 {
		keys := make([]string, 0, len(out))
		for _, a := range out {
			keys = append(keys, a.Key)
		}
		rows, err := s.Pool.Query(ctx, `SELECT alert_key, id::text FROM app.ops_incidents WHERE alert_key = ANY($1) AND status NOT IN ('RESOLVED','CLOSED')`, keys)
		if err == nil {
			m := map[string]string{}
			for rows.Next() {
				var k, id string
				if rows.Scan(&k, &id) == nil {
					m[k] = id
				}
			}
			rows.Close()
			for i := range out {
				if id, ok := m[out[i].Key]; ok {
					x := id
					out[i].IncidentID = &x
				}
			}
		}
	}
	return out
}

// Alerts returns the current alerts (GET /api/v1/admin/operations/alerts).
func (s *Service) Alerts(ctx context.Context) ([]Alert, error) {
	h, err := s.Health(ctx)
	if err != nil {
		return nil, err
	}
	c := s.collect(ctx)
	return s.alerts(ctx, h, c.snaps), nil
}
