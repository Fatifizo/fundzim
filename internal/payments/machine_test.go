package payments

import (
	"io/fs"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/Fatifizo/fundzim/internal/psp"
	"github.com/Fatifizo/fundzim/migrations"
)

// sqlEdges parses the app.status_transitions rows of one machine from a migration.
func sqlEdges(t *testing.T, file, machine string) map[[2]string]bool {
	t.Helper()
	raw, err := fs.ReadFile(migrations.FS, file)
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(`\('` + machine + `',\s*'([A-Z_]*)',\s*'([A-Z_]+)'\)`)
	out := map[[2]string]bool{}
	for _, m := range re.FindAllStringSubmatch(string(raw), -1) {
		out[[2]string{m[1], m[2]}] = true
	}
	return out
}

// The Go edge list must equal the database's (payment-state-machine §2: "a Stage 8 test diffs them").
func TestPaymentEdgesMatchMigration(t *testing.T) {
	db := sqlEdges(t, "20261012090100_payments.sql", "payment_intent")
	if len(db) != 42 || len(PaymentEdges) != 42 {
		t.Fatalf("edge counts: sql %d, go %d (want 42)", len(db), len(PaymentEdges))
	}
	for _, e := range PaymentEdges {
		if !db[[2]string{e.From, e.To}] {
			t.Errorf("Go edge %q → %q missing in SQL", e.From, e.To)
		}
	}
	for k := range db {
		if _, ok := EdgeCode(k[0], k[1]); !ok {
			t.Errorf("SQL edge %q → %q missing in Go", k[0], k[1])
		}
	}
}

// Ranks must equal app.payment_status_rank.
func TestRanksMatchMigration(t *testing.T) {
	raw, err := fs.ReadFile(migrations.FS, "20261012090100_payments.sql")
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(`WHEN '([A-Z_]+)' THEN (\d+)`)
	got := map[string]string{}
	for _, m := range re.FindAllStringSubmatch(string(raw), -1) {
		got[m[1]] = m[2]
	}
	if len(got) != len(Rank) {
		t.Fatalf("rank count sql %d go %d", len(got), len(Rank))
	}
	for st, r := range Rank {
		if got[st] != strconv.Itoa(r) {
			t.Errorf("%s: sql %s go %d", st, got[st], r)
		}
	}
}

// Refund requests can never be executed in Stage 8: no edge leaves APPROVED.
func TestRefundRequestHasNoExecutionEdge(t *testing.T) {
	db := sqlEdges(t, "20261012090100_payments.sql", "refund_request")
	for k := range db {
		if k[0] == "APPROVED" || k[1] == "EXECUTING" || k[1] == "COMPLETED" {
			t.Fatalf("execution edge present: %v", k)
		}
	}
	if !db[[2]string{"PENDING_APPROVAL", "APPROVED"}] {
		t.Fatal("approval edge missing")
	}
}

// No source other than an authoritative provider path can produce SUCCEEDED; only INTERNAL produces UNKNOWN;
// FAILED is never produced by INTERNAL except from CREATED (pre-submit) — the timeout path cannot fail a payment.
func TestSourceRules(t *testing.T) {
	sources := []string{SrcSync, SrcWebhook, SrcPoll, SrcRecon, SrcInternal, SrcStaff, SrcDonor}
	for _, e := range PaymentEdges {
		for _, src := range sources {
			ok := sourceAllowed(e.From, e.To, src)
			switch e.To {
			case psp.StatusSucceeded:
				if ok != (src == SrcSync || src == SrcWebhook || src == SrcPoll || src == SrcRecon) {
					t.Errorf("%s→SUCCEEDED from %s: %v", e.From, src, ok)
				}
			case psp.StatusUnknown:
				if ok != (src == SrcInternal) {
					t.Errorf("%s→UNKNOWN from %s: %v", e.From, src, ok)
				}
			case psp.StatusFailed:
				if src == SrcInternal && e.From != psp.StatusCreated && ok {
					t.Errorf("INTERNAL %s→FAILED allowed", e.From)
				}
				if src == SrcStaff && ok {
					t.Errorf("STAFF FAILED allowed without the evidence path")
				}
			}
			if src == SrcDonor && ok {
				t.Errorf("a donor (browser) source may change a payment: %s→%s", e.From, e.To)
			}
		}
	}
}

// Every forward move is either higher-rank or one of the listed exceptions (precedence: no regression).
func TestEdgesRespectPrecedence(t *testing.T) {
	var bad []string
	for _, e := range PaymentEdges {
		if e.From == "" {
			continue
		}
		if Rank[e.To] <= Rank[e.From] && !exception(e.From, e.To, SrcInternal) && !exception(e.From, e.To, SrcWebhook) {
			bad = append(bad, e.From+"→"+e.To)
		}
	}
	sort.Strings(bad)
	if len(bad) > 0 {
		t.Fatalf("lower-rank edges without exception: %v", bad)
	}
	if exception(psp.StatusPending, psp.StatusUnknown, SrcWebhook) {
		t.Fatal("a webhook must never move a payment back to UNKNOWN")
	}
	for _, st := range []string{psp.StatusRefunded, psp.StatusChargedBack} {
		for _, e := range PaymentEdges {
			if e.From == st {
				t.Fatalf("%s must be final", st)
			}
		}
	}
	if _, ok := EdgeCode(psp.StatusSucceeded, psp.StatusFailed); ok {
		t.Fatal("SUCCEEDED → FAILED must not exist")
	}
}

func TestPollBackoffIsBounded(t *testing.T) {
	prev := pollDelay(0)
	for i := 1; i < 40; i++ {
		d := pollDelay(i)
		if d < prev || d.Minutes() > 10 {
			t.Fatalf("attempt %d: %s", i, d)
		}
		prev = d
	}
}

func TestSafeRedirect(t *testing.T) {
	hosts := []string{"sandbox.fundzim.invalid"}
	ok := []string{"https://sandbox.fundzim.invalid/checkout/abc"}
	bad := []string{"http://sandbox.fundzim.invalid/x", "https://evil.example/x", "https://sandbox.fundzim.invalid.evil.example/x",
		"https://user:pw@sandbox.fundzim.invalid/x", "https://sandbox.fundzim.invalid:8443/x", "javascript:alert(1)",
		"//sandbox.fundzim.invalid/x", "https://sandbox.fundzim.invalid", "https://" + strings.Repeat("a", 2100)}
	for _, u := range ok {
		if !safeRedirect(u, hosts) {
			t.Errorf("refused %s", u)
		}
	}
	for _, u := range bad {
		if safeRedirect(u, hosts) {
			t.Errorf("allowed %s", u)
		}
	}
}

func TestCleanReason(t *testing.T) {
	if _, ok := cleanReason("ok"); ok {
		t.Fatal("too short accepted")
	}
	if _, ok := cleanReason("line\nbreak"); ok {
		t.Fatal("control character accepted")
	}
	if v, ok := cleanReason("  checking the outcome  "); !ok || v != "checking the outcome" {
		t.Fatal("trim")
	}
	if _, ok := cleanReason(strings.Repeat("x", 2001)); ok {
		t.Fatal("too long accepted")
	}
}

func TestDonorMessagesNeverClaimMoneyBeforeSuccess(t *testing.T) {
	for st := range Rank {
		m := strings.ToLower(donorMessage(st))
		if st != psp.StatusSucceeded && (strings.Contains(m, "received") || strings.Contains(m, "thank you for your donation")) {
			t.Errorf("%s: %q", st, m)
		}
	}
	if !strings.Contains(donorMessage(psp.StatusUnknown), "don't pay again") {
		t.Fatal("UNKNOWN must warn against paying again")
	}
}

func TestAmountFormat(t *testing.T) {
	for _, ok := range []string{"1", "100", "999999999999999999"} {
		if !amountRE.MatchString(ok) {
			t.Errorf("refused %s", ok)
		}
	}
	for _, bad := range []string{"0", "01", "-1", "1.00", "1e3", "", " 1", "9999999999999999999"} {
		if amountRE.MatchString(bad) {
			t.Errorf("accepted %q", bad)
		}
	}
}
