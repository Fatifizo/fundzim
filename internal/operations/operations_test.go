package operations

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Fatifizo/fundzim/internal/platform/clock"
	"github.com/Fatifizo/fundzim/internal/platform/opsview"
)

func fixedClock() clock.Clock { return clock.NewFake(time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)) }

func src(module string, snap opsview.Snapshot, err error) Source {
	return Source{Module: module, Snapshot: func(context.Context) (opsview.Snapshot, error) { return snap, err }}
}

func viewer(id string, perms ...string) Viewer {
	v := Viewer{StaffID: id, Permissions: map[string]bool{}, Identities: map[string]bool{id: true}}
	for _, p := range perms {
		v.Permissions[p] = true
	}
	return v
}

// The approval center shows a pending decision to whoever holds its permission (and to its makers, who can follow it
// but never decide), oldest first; a failed module is reported, not hidden or invented.
func TestApprovalsVisibilityMakersAndPartialFailure(t *testing.T) {
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	snap := opsview.Snapshot{Module: "fees", Approvals: []opsview.Approval{
		{ID: "fees:a", SourceModule: "fees", Action: "FEE_POLICY_VERSION_APPROVAL", RequiredPermission: "fee.config.approve", RequestedAt: now.Add(-time.Hour),
			Makers: []string{"maker"}},
		{ID: "fees:b", SourceModule: "fees", Action: "FEE_ADJUSTMENT_APPROVAL", RequiredPermission: "fee.adjust.approve", RequestedAt: now.Add(-2 * time.Hour),
			Makers: []string{"other"}},
	}}
	s := &Service{Clock: fixedClock(), Sources: []Source{src("fees", snap, nil), src("ledger", opsview.Snapshot{}, errors.New("down"))}}
	ctx := context.Background()

	checker := viewer("checker", "fee.config.approve")
	got, statuses, err := s.Approvals(ctx, checker, ApprovalFilter{})
	if err != nil || len(got) != 1 || got[0].ID != "fees:a" || !got[0].CanDecide || got[0].AgeSecs != 3600 {
		t.Fatalf("checker: %+v %v", got, err)
	}
	if statuses[1].Module != "ledger" || statuses[1].Status != opsview.Unavailable || statuses[0].Status != opsview.Healthy {
		t.Fatalf("statuses: %+v", statuses)
	}
	// the maker sees their own request but cannot decide it, even with the permission
	maker := viewer("maker", "fee.config.approve")
	got, _, _ = s.Approvals(ctx, maker, ApprovalFilter{})
	if len(got) != 1 || got[0].CanDecide || got[0].Blocked != "MAKER" {
		t.Fatalf("maker: %+v", got)
	}
	if got, _, _ = s.Approvals(ctx, maker, ApprovalFilter{MineOnly: true}); len(got) != 0 {
		t.Fatalf("maker decidable: %+v", got)
	}
	// a linked account counts as the maker
	linked := viewer("staff2", "fee.config.approve")
	linked.Identities["maker"] = true
	if got, _, _ = s.Approvals(ctx, linked, ApprovalFilter{}); len(got) != 1 || got[0].CanDecide {
		t.Fatalf("linked: %+v", got)
	}
	// someone without the permission sees nothing
	if got, _, _ = s.Approvals(ctx, viewer("support", "ops.dashboard.view"), ApprovalFilter{}); len(got) != 0 {
		t.Fatalf("no permission: %+v", got)
	}
	// both, oldest first
	both := viewer("boss", "fee.config.approve", "fee.adjust.approve")
	if got, _, _ = s.Approvals(ctx, both, ApprovalFilter{}); len(got) != 2 || got[0].ID != "fees:b" {
		t.Fatalf("ordering: %+v", got)
	}
	if got, _, _ = s.Approvals(ctx, both, ApprovalFilter{Action: "FEE_ADJUSTMENT_APPROVAL"}); len(got) != 1 {
		t.Fatalf("filter: %+v", got)
	}
}

// A slow or panicking source is bounded and reported UNAVAILABLE; the other sources still answer.
func TestCollectBoundsSlowAndPanickingSources(t *testing.T) {
	slow := Source{Module: "slow", Snapshot: func(ctx context.Context) (opsview.Snapshot, error) {
		<-ctx.Done()
		return opsview.Snapshot{}, ctx.Err()
	}}
	bad := Source{Module: "bad", Snapshot: func(context.Context) (opsview.Snapshot, error) { panic("boom") }}
	ok := src("ok", opsview.Snapshot{Module: "ok", Metrics: []opsview.Metric{opsview.Count("ok", "k", "l", 3, "", "s", "")}}, nil)
	s := &Service{Clock: fixedClock(), Sources: []Source{slow, bad, ok}, SourceTimeout: 50 * time.Millisecond}
	start := time.Now()
	c := s.collect(context.Background())
	if time.Since(start) > 2*time.Second {
		t.Fatal("slow source not bounded")
	}
	if c.statuses[0].Status != opsview.Unavailable || c.statuses[1].Status != opsview.Unavailable || c.statuses[2].Status != opsview.Healthy {
		t.Fatalf("statuses: %+v", c.statuses)
	}
	if len(c.snaps[2].Metrics) != 1 || *c.snaps[2].Metrics[0].Value != 3 {
		t.Fatalf("ok source: %+v", c.snaps[2])
	}
}

func TestLifecyclesAreClosed(t *testing.T) {
	for name, edges := range map[string]map[string][]string{"support": SupportEdges, "incident": IncidentEdges, "exception": ExceptionEdges} {
		for from, tos := range edges {
			for _, to := range tos {
				if to == "" || to == from {
					t.Fatalf("%s: bad edge %q -> %q", name, from, to)
				}
			}
		}
	}
	// closed records only reopen (support, exception) or stay closed (incident)
	if len(IncidentEdges["CLOSED"]) != 0 || !allowed(SupportEdges, "CLOSED", "REOPENED") || allowed(SupportEdges, "CLOSED", "IN_PROGRESS") {
		t.Fatal("closure rules")
	}
	if allowed(ExceptionEdges, "OPEN", "RESOLVED") {
		t.Fatal("an exception must be acknowledged before triage can finish")
	}
}
