package compliance

import (
	"encoding/json"
	"errors"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/Fatifizo/fundzim/internal/platform/httpx"
	"github.com/Fatifizo/fundzim/internal/platform/ids"
)

func migrationSQL(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("../../migrations/20261009150600_compliance.sql")
	if err != nil {
		t.Fatal(err)
	}
	up, _, _ := strings.Cut(string(b), "-- +goose Down")
	return up
}

// CLAUDE.md: the Go and SQL edge lists of every state machine are identical.
func TestTransitionsMatchMigration(t *testing.T) {
	re := regexp.MustCompile(`\('compliance_case',\s*'([A-Z_]*)',\s*'([A-Z_]+)'\)`)
	var sqlEdges, goEdges []string
	for _, m := range re.FindAllStringSubmatch(migrationSQL(t), -1) {
		sqlEdges = append(sqlEdges, m[1]+">"+m[2])
	}
	for from, tos := range Transitions {
		for _, to := range tos {
			goEdges = append(goEdges, from+">"+to)
		}
	}
	sort.Strings(sqlEdges)
	sort.Strings(goEdges)
	if strings.Join(sqlEdges, ",") != strings.Join(goEdges, ",") {
		t.Fatalf("edge lists differ:\nsql %v\ngo  %v", sqlEdges, goEdges)
	}
}

func TestStateMachineShape(t *testing.T) {
	ok := [][2]string{{"", "OPEN"}, {"OPEN", "ASSIGNED"}, {"ASSIGNED", "IN_REVIEW"}, {"IN_REVIEW", "AWAITING_INFORMATION"},
		{"AWAITING_INFORMATION", "IN_REVIEW"}, {"IN_REVIEW", "ESCALATED"}, {"ESCALATED", "IN_REVIEW"}, {"IN_REVIEW", "RESOLVED"},
		{"ESCALATED", "RESOLVED"}, {"RESOLVED", "CLOSED"}, {"RESOLVED", "IN_REVIEW"}, {"CLOSED", "IN_REVIEW"}}
	for _, e := range ok {
		if !CanTransition(e[0], e[1]) {
			t.Errorf("%s -> %s must be allowed (ADR-034 §5)", e[0], e[1])
		}
	}
	bad := [][2]string{{"OPEN", "IN_REVIEW"}, {"OPEN", "RESOLVED"}, {"ASSIGNED", "RESOLVED"}, {"AWAITING_INFORMATION", "RESOLVED"},
		{"CLOSED", "OPEN"}, {"CLOSED", "RESOLVED"}, {"RESOLVED", "ESCALATED"}, {"", "ASSIGNED"}}
	for _, e := range bad {
		if CanTransition(e[0], e[1]) {
			t.Errorf("%s -> %s must be refused", e[0], e[1])
		}
	}
}

// The maker-checker decision list is identical in Go and in the CHECK constraints.
func TestCheckerDecisionsMatchMigration(t *testing.T) {
	sql := migrationSQL(t)
	re := regexp.MustCompile(`ck_compliance_cases_checker_required CHECK \([^;]*?decision NOT IN \(([^)]*)\)`)
	m := re.FindStringSubmatch(sql)
	if m == nil {
		t.Fatal("checker CHECK not found")
	}
	var got []string
	for _, p := range strings.Split(m[1], ",") {
		got = append(got, strings.Trim(strings.TrimSpace(p), "'"))
	}
	var want []string
	for d := range CheckerRequired {
		want = append(want, d)
	}
	sort.Strings(got)
	sort.Strings(want)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("SQL %v, Go %v", got, want)
	}
	for d := range CheckerRequired {
		if !Decisions[d] {
			t.Errorf("%s is not a decision", d)
		}
	}
}

func raw(m map[string]any) json.RawMessage { b, _ := json.Marshal(m); return b }

func TestTriggerFromEvent(t *testing.T) {
	user, org, kc, ben, asm, dec := ids.New(), ids.New(), ids.New(), ids.New(), ids.New(), ids.New()
	tr, err := TriggerFromEvent(EvKYCCaseEscalated, raw(map[string]any{"case_id": kc, "kind": "KYC", "subject_type": "USER",
		"subject_id": user, "reason_code": "POSSIBLE_PEP"}))
	if err != nil || tr.CaseType != "KYC_REVIEW" || tr.Source != "KYC_ESCALATION" || tr.ReasonCode != "POSSIBLE_PEP" || len(tr.Links) != 2 ||
		tr.Links[0] != (LinkInput{"USER", user, "PRIMARY_SUBJECT"}) || tr.Links[1] != (LinkInput{"KYC_CASE", kc, "RELATED_OBJECT"}) {
		t.Fatalf("kyc: %+v %v", tr, err)
	}
	tr, err = TriggerFromEvent(EvKYCCaseEscalated, raw(map[string]any{"case_id": kc, "kind": "KYB", "subject_type": "ORGANISATION",
		"subject_id": org, "reason_code": "free text"}))
	if err != nil || tr.CaseType != "KYB_REVIEW" || tr.ReasonCode != "KYB_ESCALATED" || tr.Links[1].SubjectType != "KYB_CASE" {
		t.Fatalf("kyb: %+v %v", tr, err)
	}
	tr, err = TriggerFromEvent(EvBeneficiaryEscalated, raw(map[string]any{"beneficiary_id": ben, "owner_type": "USER", "owner_id": user}))
	if err != nil || tr.CaseType != "BENEFICIARY_REVIEW" || tr.Links[0].SubjectType != "BENEFICIARY" || tr.Links[1] != (LinkInput{"USER", user, "RELATED_SUBJECT"}) {
		t.Fatalf("beneficiary: %+v %v", tr, err)
	}
	tr, err = TriggerFromEvent(EvRiskEscalationRecommended, raw(map[string]any{"subject_type": "USER", "subject_id": user,
		"assessment_id": asm, "decision_id": dec, "rating": "RESTRICTED"}))
	if err != nil || tr.CaseType != "RISK_REVIEW" || tr.Severity != "S2" || len(tr.Links) != 3 || tr.ReasonCode != "RISK_RATING_RESTRICTED" {
		t.Fatalf("risk: %+v %v", tr, err)
	}
	tr, err = TriggerFromEvent(EvRiskEscalationRecommended, raw(map[string]any{"subject_type": "KYC_CASE", "subject_id": kc,
		"assessment_id": asm, "decision_id": dec, "rating": "RESTRICTED"}))
	if err != nil || tr.Links[0].Role != "RELATED_OBJECT" {
		t.Fatalf("risk on kyc case: %+v %v", tr, err)
	}
	for _, links := range [][]LinkInput{tr.Links} {
		if d := validateLinks(links); len(d) != 0 {
			t.Fatalf("derived links invalid: %v", d)
		}
	}
	bad := []struct {
		ev string
		p  map[string]any
	}{
		{EvKYCCaseEscalated, map[string]any{"case_id": kc, "kind": "KYC", "subject_type": "USER"}},
		{EvKYCCaseEscalated, map[string]any{"case_id": kc, "kind": "OTHER", "subject_type": "USER", "subject_id": user}},
		{EvKYCCaseEscalated, map[string]any{"case_id": "1", "kind": "KYC", "subject_type": "USER", "subject_id": user}},
		{EvBeneficiaryEscalated, map[string]any{"beneficiary_id": ben, "owner_type": "DEVICE", "owner_id": user}},
		{EvRiskEscalationRecommended, map[string]any{"subject_type": "USER", "subject_id": user, "assessment_id": asm, "decision_id": dec, "rating": "LOW"}},
		{EvRiskEscalationRecommended, map[string]any{"subject_type": "DEVICE", "subject_id": user, "assessment_id": asm, "decision_id": dec, "rating": "RESTRICTED"}},
		{EvRiskEscalationRecommended, map[string]any{"subject_type": "USER", "subject_id": user, "rating": "RESTRICTED"}},
		{"kyc.case_approved", map[string]any{"case_id": kc}},
	}
	for i, b := range bad {
		if _, err := TriggerFromEvent(b.ev, raw(b.p)); !errors.Is(err, ErrInvalidPayload) {
			t.Errorf("bad[%d] %s: %v", i, b.ev, err)
		}
	}
	if _, err := TriggerFromEvent(EvKYCCaseEscalated, json.RawMessage(`"x"`)); !errors.Is(err, ErrInvalidPayload) {
		t.Errorf("non-object payload: %v", err)
	}
}

func TestValidateLinks(t *testing.T) {
	u := ids.New()
	if d := validateLinks([]LinkInput{{"USER", u, "PRIMARY_SUBJECT"}, {"KYC_CASE", ids.New(), "RELATED_OBJECT"}}); len(d) != 0 {
		t.Fatalf("valid links refused: %v", d)
	}
	cases := [][]LinkInput{
		nil,
		{{"USER", "not-a-uuid", "PRIMARY_SUBJECT"}},
		{{"KYC_CASE", u, "PRIMARY_SUBJECT"}}, // objects are never subjects
		{{"USER", u, "RELATED_OBJECT"}},      // parties are never objects
		{{"DEVICE", u, "PRIMARY_SUBJECT"}},   // unknown type
		{{"USER", u, "PRIMARY_SUBJECT"}, {"ORGANISATION", ids.New(), "PRIMARY_SUBJECT"}},
	}
	for i, c := range cases {
		if d := validateLinks(c); len(d) == 0 {
			t.Errorf("case %d accepted", i)
		}
	}
}

func TestCleanNote(t *testing.T) {
	if n, ok := cleanNote("  hello  "); !ok || n != "hello" {
		t.Fatal(n, ok)
	}
	if _, ok := cleanNote(strings.Repeat("x", MaxNoteRunes+1)); ok {
		t.Fatal("over-long note accepted")
	}
	if _, ok := cleanNote(string([]byte{0xff, 0xfe})); ok {
		t.Fatal("invalid UTF-8 accepted")
	}
}

func TestRoutesDeclarePolicies(t *testing.T) {
	s := &Service{}
	want := map[string]string{
		"GET /api/v1/admin/compliance/cases":                               PermView,
		"GET /api/v1/admin/compliance/cases/{case_id}":                     PermView,
		"POST /api/v1/admin/compliance/cases":                              PermCreate,
		"POST /api/v1/admin/compliance/cases/{case_id}/resolve":            PermManage,
		"POST /api/v1/admin/compliance/cases/{case_id}/approve-resolution": PermManage,
		"POST /api/v1/admin/compliance/cases/{case_id}/notes":              PermManage,
	}
	got := map[string]string{}
	for _, r := range s.Routes() {
		if r.Policy.Kind != httpx.KindPermission || r.Handler == nil {
			t.Fatalf("%s: every compliance route is a permission route", r.Pattern)
		}
		got[r.Pattern] = r.Policy.Permission
	}
	if len(got) != 12 {
		t.Fatalf("routes: %d", len(got))
	}
	for p, perm := range want {
		if got[p] != perm {
			t.Errorf("%s: %q, want %q", p, got[p], perm)
		}
	}
}

func TestReasonCodes(t *testing.T) {
	for _, ok := range []string{"KYC_ESCALATED", "ABC", "RISK_RATING_RESTRICTED"} {
		if !ValidReasonCode(ok) {
			t.Errorf("%s refused", ok)
		}
	}
	for _, bad := range []string{"", "ab", "lower_case", "WITH SPACE", "1ABC", strings.Repeat("A", 65)} {
		if ValidReasonCode(bad) {
			t.Errorf("%q accepted", bad)
		}
	}
}
