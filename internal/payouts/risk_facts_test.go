package payouts

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Fatifizo/fundzim/internal/campaigns"
	"github.com/Fatifizo/fundzim/internal/platform/ids"
	"github.com/Fatifizo/fundzim/internal/risk"
)

// Stage 13 failure injection: when the risk store cannot be read, eligibility stops with an error (fail closed) instead
// of evaluating without holds, screening or classification.
func TestRiskFactsFailClosedOnOutage(t *testing.T) {
	pool, err := pgxpool.New(context.Background(), "postgres://127.0.0.1:1/unreachable?connect_timeout=1")
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	s := &Service{Risk: risk.New(pool, nil, nil)}
	owner := ids.New()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var f Facts
	err = s.riskFacts(ctx, &f, subject{CampaignID: ids.New(), RequesterID: owner, DestinationID: ids.New()}, campaigns.Access{OwnerUserID: &owner})
	if err == nil {
		t.Fatalf("risk outage ignored: %+v", f)
	}
	// no risk module wired (tests only): no holds read, nothing invented
	var g Facts
	if err := (&Service{}).riskFacts(ctx, &g, subject{}, campaigns.Access{}); err != nil || len(g.RiskHolds) != 0 {
		t.Fatalf("nil risk: %v %+v", err, g)
	}
}
