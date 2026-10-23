package archtest

// Stage 18 financial safety regression (docs/stage-18/security-testing.md §2): no infrastructure or security change may
// silently re-enable live money movement. The Go gates are compile-time constants; the database gates are named CHECK
// constraints. This test fails when a constant flips or when a later migration drops a gate CHECK without re-adding it.
// The integration test TestSecurityFinancialGatesInDatabase checks the same constraints in a migrated database.

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/Fatifizo/fundzim/internal/discovery"
	"github.com/Fatifizo/fundzim/internal/fees"
	"github.com/Fatifizo/fundzim/internal/ledger"
	"github.com/Fatifizo/fundzim/internal/notifications"
	"github.com/Fatifizo/fundzim/internal/payouts"
	"github.com/Fatifizo/fundzim/internal/psp"
	"github.com/Fatifizo/fundzim/internal/reconciliation"
	"github.com/Fatifizo/fundzim/internal/risk"
)

func TestFinancialGateConstantsStayOff(t *testing.T) {
	gates := map[string]bool{
		"psp.LiveExecutionEnabled":                    psp.LiveExecutionEnabled,
		"payouts.LiveExecutionEnabled":                payouts.LiveExecutionEnabled,
		"ledger.ProductionPostingEnabled":             ledger.ProductionPostingEnabled,
		"fees.ProductionFeesEnabled":                  fees.ProductionFeesEnabled,
		"risk.ScreeningProductionEnabled":             risk.ScreeningProductionEnabled,
		"notifications.LiveMessagingEnabled":          notifications.LiveMessagingEnabled,
		"notifications.MarketingEnabled":              notifications.MarketingEnabled,
		"discovery.ProductionReferralsEnabled":        discovery.ProductionReferralsEnabled,
		"reconciliation.ProductionImportsEnabled":     reconciliation.ProductionImportsEnabled,
		"reconciliation.SettlementPostingEnabled":     reconciliation.SettlementPostingEnabled,
		"reconciliation.ProductionAdjustmentsEnabled": reconciliation.ProductionAdjustmentsEnabled,
	}
	for name, on := range gates {
		if on {
			t.Errorf("%s is true: re-enabling a production gate needs an ADR, provider selection, legal sign-off and stage acceptance", name)
		}
	}
	if psp.KillSwitchFlag != "payments.live_execution.enabled" || payouts.KillSwitchFlag != "payouts.live_execution.enabled" {
		t.Errorf("kill switch flag keys changed: %q, %q", psp.KillSwitchFlag, payouts.KillSwitchFlag)
	}
}

// gateChecks are the database CHECK constraints that keep production rows out (Stages 9–17); the list is the
// contract shared with the integration test.
var gateChecks = []string{
	"ck_donation_intents_no_production_stage9",
	"ck_payment_intents_no_production_stage9",
	"ck_ledger_accounts_no_production_stage10",
	"ck_ledger_environment_no_production_stage10",
	"ck_ledger_transactions_no_production_stage10",
	"ck_payout_requests_no_production_stage11",
	"ck_payout_requests_test_only_stage11",
	"ck_fee_adjustments_test_only_stage12",
	"ck_fee_assessments_test_only_stage12",
	"ck_fee_calculations_test_only_stage12",
	"ck_fee_policies_test_only_stage12",
	"ck_fee_policy_versions_test_only_stage12",
	"ck_fee_provider_schedules_test_only_stage12",
	"ck_fee_quotes_test_only_stage12",
	"ck_fee_reconciliation_references_test_only_stage12",
	"ck_risk_screening_requests_test_only_stage13",
	"ck_notification_provider_configs_no_production_stage15",
	"ck_notification_provider_configs_test_only_stage15",
	"ck_recon_imports_test_only_stage17",
	"ck_recon_sources_test_only_stage17",
}

var (
	gateCheckRE = regexp.MustCompile(`ck_[a-z0-9_]*(?:no_production|test_only)_stage[0-9]+`)
	dropCheckRE = regexp.MustCompile(`(?i)DROP\s+CONSTRAINT\s+(?:IF\s+EXISTS\s+)?(ck_[a-z0-9_]+)`)
)

// gateState replays the Up sections in order: a statement naming a gate CHECK adds it unless it drops it.
func gateState(ups []string) map[string]bool {
	state := map[string]bool{}
	for _, up := range ups {
		if i := strings.Index(up, "-- +goose Down"); i >= 0 {
			up = up[:i]
		}
		for _, stmt := range strings.Split(up, ";") {
			dropped := map[string]bool{}
			for _, m := range dropCheckRE.FindAllStringSubmatch(stmt, -1) {
				dropped[m[1]] = true
			}
			for _, name := range gateCheckRE.FindAllString(stmt, -1) {
				state[name] = !dropped[name]
			}
		}
	}
	return state
}

func TestFinancialGateChecksSurviveMigrations(t *testing.T) {
	files, err := filepath.Glob(filepath.Join(repoRoot(t), "migrations", "*.sql"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no migrations: %v", err)
	}
	sort.Strings(files) // goose order: timestamp prefix
	var ups []string
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		ups = append(ups, string(raw))
	}
	state := gateState(ups)
	for _, name := range gateChecks {
		if !state[name] {
			t.Errorf("gate CHECK %s is missing (or dropped and not re-added) after the last migration", name)
		}
	}
	// a new gate CHECK must be added to the contract list (and to the integration test) when it is introduced
	known := map[string]bool{}
	for _, n := range gateChecks {
		known[n] = true
	}
	for name := range state {
		if !known[name] {
			t.Errorf("gate CHECK %s exists in migrations but not in gateChecks: add it to the Stage 18 regression contract", name)
		}
	}
}

// The detector itself: a later migration that drops a gate CHECK is caught; dropping and re-adding is not.
func TestGateStateDetectsDroppedCheck(t *testing.T) {
	add := "ALTER TABLE x ADD CONSTRAINT ck_x_no_production_stage9 CHECK (e <> 'PRODUCTION');"
	drop := "ALTER TABLE x DROP CONSTRAINT IF EXISTS ck_x_no_production_stage9;"
	if gateState([]string{add, drop})["ck_x_no_production_stage9"] {
		t.Fatal("a dropped gate CHECK was not detected")
	}
	if !gateState([]string{add, drop + "\n" + add})["ck_x_no_production_stage9"] {
		t.Fatal("a re-added gate CHECK was reported missing")
	}
	if gateState([]string{add + "\n-- +goose Down\n" + drop})["ck_x_no_production_stage9"] != true {
		t.Fatal("a Down section must not count")
	}
}
