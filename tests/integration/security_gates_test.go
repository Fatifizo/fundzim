//go:build integration

package integration

// Stage 18 financial safety regression in the database (docs/stage-18/security-testing.md §2). The unit test
// internal/archtest TestFinancialGateChecksSurviveMigrations proves the gate CHECKs are in the migrations; this proves
// they are present and VALIDATED in a migrated database, that the kill switches are off, that the ledger is designated
// TEST, and that no runtime role can rewrite financial history (ledger, audit, recon) through table privileges.

import (
	"os"
	"strings"
	"testing"
)

// securityGateChecks mirrors internal/archtest gateChecks (the contract); keep both lists identical.
var securityGateChecks = []string{
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

var runtimeRoles = []string{"fundzim_app", "fundzim_worker", "fundzim_kyc", "fundzim_compliance", "fundzim_ledger", "fundzim_backup"}

func TestSecurityFinancialGatesInDatabase(t *testing.T) {
	need(t, "DATABASE_MIGRATION_URL")
	mig := pool(t, os.Getenv("DATABASE_MIGRATION_URL"))

	for _, name := range securityGateChecks {
		var validated bool
		var n int
		if err := mig.QueryRow(ctx(t), `SELECT count(*), coalesce(bool_and(convalidated), false) FROM pg_constraint
			WHERE conname = $1 AND contype = 'c'`, name).Scan(&n, &validated); err != nil {
			t.Fatal(err)
		}
		if n != 1 || !validated {
			t.Errorf("gate CHECK %s: present %d, validated %v", name, n, validated)
		}
	}

	for _, flag := range []string{"payments.live_execution.enabled", "payouts.live_execution.enabled"} {
		var enabled bool
		if err := mig.QueryRow(ctx(t), `SELECT enabled FROM app.feature_flags WHERE key = $1`, flag).Scan(&enabled); err != nil {
			t.Fatalf("kill switch %s: %v", flag, err)
		}
		if enabled {
			t.Errorf("kill switch %s is ON", flag)
		}
	}

	var env string
	if err := mig.QueryRow(ctx(t), `SELECT environment FROM ledger.ledger_environment`).Scan(&env); err != nil || env != "TEST" {
		t.Errorf("ledger environment %q (%v), want TEST", env, err)
	}
	for _, q := range []string{
		`SELECT count(*) FROM app.payment_intents WHERE execution_environment = 'PRODUCTION'`,
		`SELECT count(*) FROM app.payout_requests WHERE execution_environment = 'PRODUCTION'`,
	} {
		var n int
		if err := mig.QueryRow(ctx(t), q).Scan(&n); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		if n != 0 {
			t.Errorf("%s = %d", q, n)
		}
	}
}

func TestSecurityRuntimeRolesCannotRewriteFinancialHistory(t *testing.T) {
	need(t, "DATABASE_MIGRATION_URL")
	mig := pool(t, os.Getenv("DATABASE_MIGRATION_URL"))
	type grant struct{ role, table, priv string }
	rows, err := mig.Query(ctx(t), `
		SELECT r.rolname, c.oid::regclass::text, p.priv
		FROM pg_class c
		JOIN pg_namespace n ON n.oid = c.relnamespace
		CROSS JOIN unnest(ARRAY['INSERT','UPDATE','DELETE','TRUNCATE']) AS p(priv)
		CROSS JOIN pg_roles r
		WHERE n.nspname IN ('ledger', 'audit', 'recon') AND c.relkind IN ('r', 'p')
		  AND r.rolname = ANY($1) AND has_table_privilege(r.rolname, c.oid, p.priv)`, runtimeRoles)
	if err != nil {
		t.Fatal(err)
	}
	var got []grant
	for rows.Next() {
		var g grant
		if err := rows.Scan(&g.role, &g.table, &g.priv); err != nil {
			t.Fatal(err)
		}
		got = append(got, g)
	}
	rows.Close()
	if len(got) == 0 {
		t.Fatal("no grants returned: the query or the roles are wrong")
	}
	for _, g := range got {
		schema := g.table[:strings.Index(g.table, ".")]
		switch {
		case g.priv == "TRUNCATE" || g.priv == "DELETE":
			// nothing in the financial-history schemas is ever deleted by a runtime role
			t.Errorf("%s has %s on %s", g.role, g.priv, g.table)
		case schema == "ledger" && g.role != "fundzim_ledger":
			t.Errorf("%s has %s on %s: only fundzim_ledger writes the ledger", g.role, g.priv, g.table)
		case schema == "recon" && g.role != "fundzim_app" && g.role != "fundzim_worker":
			t.Errorf("%s has %s on %s", g.role, g.priv, g.table)
		}
	}
	// posted journals and their entries are never updated, even by the ledger role (corrections are new journals)
	for _, table := range []string{"ledger.ledger_transactions", "ledger.ledger_entries"} {
		var can bool
		if err := mig.QueryRow(ctx(t), `SELECT has_table_privilege('fundzim_ledger', $1, 'UPDATE')`, table).Scan(&can); err != nil {
			t.Fatalf("%s: %v", table, err)
		}
		if can {
			t.Errorf("fundzim_ledger may UPDATE %s", table)
		}
	}
}

// Stage 18: the retention registry invents no legal period and nothing financial, audit, identity or evidence related
// may be deleted until a legal decision is recorded (LR-012).
func TestSecurityRetentionRegistryInventsNoPeriods(t *testing.T) {
	need(t, "DATABASE_MIGRATION_URL")
	mig := pool(t, os.Getenv("DATABASE_MIGRATION_URL"))
	rows, err := mig.Query(ctx(t), `SELECT record_family, status, deletion_mode, retention_period IS NOT NULL, classification FROM audit.retention_policies`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var fam, status, mode, class string
		var hasPeriod bool
		if err := rows.Scan(&fam, &status, &mode, &hasPeriod, &class); err != nil {
			t.Fatal(err)
		}
		n++
		switch status {
		case "APPROVED":
			t.Errorf("%s is APPROVED: no retention decision has been recorded (LR-012)", fam)
		case "LEGAL_REVIEW_REQUIRED":
			if hasPeriod || mode != "NEVER_DELETE" {
				t.Errorf("%s: a legal-review family has a period or a deletion mode (%s)", fam, mode)
			}
		case "OPERATIONAL_ONLY":
			if class == "C3" || class == "C4" || strings.HasPrefix(fam, "ledger.") || strings.HasPrefix(fam, "audit.") || strings.HasPrefix(fam, "payments.") {
				t.Errorf("%s: operational retention on a record family", fam)
			}
		}
	}
	if n < 15 {
		t.Fatalf("registry has %d families", n)
	}
	// a period without an approval is refused by the database
	if _, err := mig.Exec(ctx(t), `UPDATE audit.retention_policies SET retention_period = interval '7 years' WHERE record_family = 'ledger.journals'`); err == nil {
		t.Fatal("a retention period was set without an approval")
	}
	if _, err := mig.Exec(ctx(t), `DELETE FROM audit.retention_policies WHERE record_family = 'ledger.journals'`); err == nil {
		t.Fatal("a registry row was deletable")
	}
}

// Stage 18: the backup role reads everything (pg_read_all_data, BYPASSRLS so a dump is never silently partial) and can
// write nothing.
func TestSecurityBackupRoleIsReadOnly(t *testing.T) {
	need(t, "DATABASE_MIGRATION_URL")
	mig := pool(t, os.Getenv("DATABASE_MIGRATION_URL"))
	var super, bypass, canWrite bool
	if err := mig.QueryRow(ctx(t), `SELECT rolsuper, rolbypassrls FROM pg_roles WHERE rolname = 'fundzim_backup'`).Scan(&super, &bypass); err != nil {
		t.Fatalf("backup role: %v", err)
	}
	if super || !bypass {
		t.Fatalf("fundzim_backup superuser=%v bypassrls=%v", super, bypass)
	}
	if err := mig.QueryRow(ctx(t), `SELECT bool_or(has_table_privilege('fundzim_backup', c.oid, p))
		FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace CROSS JOIN unnest(ARRAY['INSERT','UPDATE','DELETE','TRUNCATE']) AS p
		WHERE n.nspname IN ('app','ledger','audit','kyc','risk','compliance','recon') AND c.relkind IN ('r','p')`).Scan(&canWrite); err != nil {
		t.Fatal(err)
	}
	if canWrite {
		t.Fatal("fundzim_backup can write a FundZim table")
	}
}
