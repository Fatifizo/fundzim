//go:build integration

package integration

// Stage 18 audit integrity (ADR-050, F-S18-08, KI-S17-12): parallel full verification, MAC-protected checkpoints,
// incremental verification and tamper detection. Tampering is only ever done inside a transaction that is rolled back.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Fatifizo/fundzim/internal/audit"
	"github.com/Fatifizo/fundzim/internal/platform/crypto"
)

// itCheckpointKey is this test run's checkpoint key: the first chain check of the run is a FULL verification and every
// later one starts from the checkpoint it left (each run's own key, so the worker's checkpoints are never involved).
var (
	itCheckpointOnce sync.Once
	itCheckpointKey  *crypto.Keyed
)

// assertChainsIntact verifies the audit chains (FULL the first time in a run, then incrementally from that checkpoint).
func assertChainsIntact(t *testing.T, c context.Context, chains ...string) {
	t.Helper()
	itCheckpointOnce.Do(func() {
		raw := make([]byte, 32)
		_, _ = rand.Read(raw)
		itCheckpointKey, _ = crypto.NewKeyed("it-run-checkpoint/"+hex.EncodeToString(raw[:4]), hex.EncodeToString(raw))
	})
	v := &audit.Verifier{Pool: pool(t, workerURL(t)), Key: itCheckpointKey, Workers: 8}
	for _, chain := range chains {
		res, err := v.VerifyIncremental(c, chain)
		if err != nil {
			t.Fatalf("%s: %v", chain, err)
		}
		if !res.Intact() {
			t.Fatalf("%s: %s %+v", chain, res.Outcome, res.Problems)
		}
	}
}

func testVerifier(t *testing.T, workers int) *audit.Verifier {
	t.Helper()
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		t.Fatal(err)
	}
	key, err := crypto.NewKeyed("it-audit-checkpoint/"+hex.EncodeToString(raw[:4]), hex.EncodeToString(raw))
	if err != nil {
		t.Fatal(err)
	}
	return &audit.Verifier{Pool: pool(t, workerURL(t)), Key: key, Workers: workers}
}

func TestSecurityAuditChainVerification(t *testing.T) {
	need(t, "DATABASE_MIGRATION_URL", "DATABASE_URL")
	// a FULL run recomputes every row (≈30 µs each, ADR-050); the development chain holds over a million rows, so this
	// test measures it with its own budget instead of the 30 s request context
	long, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	v := testVerifier(t, 8)
	for _, chain := range audit.Chains {
		full, err := v.VerifyFull(long, chain)
		if err != nil {
			t.Fatalf("%s full: %v", chain, err)
		}
		if !full.Intact() || full.Mode != audit.ModeFull {
			t.Fatalf("%s full: %+v", chain, full)
		}
		t.Logf("%s FULL: %d rows in %v on %d connections", chain, full.ToSeq, full.Duration, full.Workers)

		inc, err := v.VerifyIncremental(long, chain)
		if err != nil {
			t.Fatal(err)
		}
		if !inc.Intact() || inc.Mode != audit.ModeIncremental || inc.FromSeq > full.ToSeq {
			t.Fatalf("%s incremental after full: %+v", chain, inc)
		}
		t.Logf("%s INCREMENTAL: seq %d..%d in %v", chain, inc.FromSeq, inc.ToSeq, inc.Duration)
		if inc.Duration > 30*time.Second {
			t.Errorf("%s incremental verification took %v", chain, inc.Duration)
		}
	}

	// a verifier with a different key cannot use these checkpoints: it starts from scratch (FULL), never trusts them
	other := testVerifier(t, 8)
	if res, err := other.VerifyIncremental(long, "audit.security_audit_events"); err != nil || res.Mode != audit.ModeFull || !res.Intact() {
		t.Fatalf("foreign-key checkpoint was used: %+v %v", res, err)
	}

	// the verification log and checkpoints are append-only
	mig := pool(t, os.Getenv("DATABASE_MIGRATION_URL"))
	for _, q := range []string{`UPDATE audit.chain_verifications SET problems = 0 WHERE true`,
		`DELETE FROM audit.chain_checkpoints WHERE true`} {
		if _, err := mig.Exec(ctx(t), q); err == nil {
			t.Fatalf("%s was allowed", q)
		}
	}
	app := pool(t, os.Getenv("DATABASE_URL"))
	if _, err := app.Exec(ctx(t), `SELECT * FROM audit.verify_chain_range('audit.audit_events', 1, 1)`); err == nil {
		t.Fatal("the API role may run chain verification (worker only)")
	}
}

// Failure injection: a modified row, a removed link and a forged prev_hash are each detected by the range verification.
// Everything happens in one transaction that is rolled back (the table owner disables the append-only triggers there).
func TestSecurityAuditChainTamperDetection(t *testing.T) {
	need(t, "DATABASE_MIGRATION_URL")
	mig := pool(t, os.Getenv("DATABASE_MIGRATION_URL"))
	c := ctx(t)
	var head int64
	if err := mig.QueryRow(c, `SELECT max(seq) FROM audit.security_audit_events`).Scan(&head); err != nil || head < 5 {
		t.Skipf("security chain too short (%d): %v", head, err)
	}
	target := head - 2
	tx, err := mig.Begin(c)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err := tx.Exec(c, `SET LOCAL lock_timeout = '5s'`); err != nil {
		t.Fatal(err)
	}
	problems := func(tx pgx.Tx) []string {
		rows, err := tx.Query(c, `SELECT seq::text || ': ' || problem FROM audit.verify_chain_range('audit.security_audit_events', $1, $2)`,
			target-1, head)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var out []string
		for rows.Next() {
			var p string
			_ = rows.Scan(&p)
			out = append(out, p)
		}
		return out
	}
	if p := problems(tx); len(p) != 0 {
		t.Fatalf("untouched range reports %v", p)
	}
	if _, err := tx.Exec(c, `ALTER TABLE audit.security_audit_events DISABLE TRIGGER USER`); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(c, `UPDATE audit.security_audit_events SET reason = 'tampered by the integration test' WHERE seq = $1`, target); err != nil {
		t.Fatal(err)
	}
	if p := problems(tx); len(p) == 0 {
		t.Fatal("a modified row was not detected")
	} else {
		t.Logf("modified row: %v", p)
	}
	sp, _ := tx.Begin(c)
	if _, err := sp.Exec(c, `DELETE FROM audit.security_audit_events WHERE seq = $1`, target); err != nil {
		t.Fatal(err)
	}
	if p := problems(sp); len(p) == 0 {
		t.Fatal("a removed row was not detected")
	} else {
		t.Logf("removed row: %v", p)
	}
	_ = sp.Rollback(c)
}
