package verifcase

import (
	"os"
	"regexp"
	"sort"
	"testing"
)

// The Go edge list must equal the database's (CLAUDE.md: keep Go and SQL edge lists identical).
func TestEdgesMatchMigration(t *testing.T) {
	sql, err := os.ReadFile("../../../migrations/20261009150200_kyc.sql")
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(`\('verification_case',\s*'([A-Z_]*)',\s*'([A-Z_]+)'\)`)
	var db []string
	for _, m := range re.FindAllStringSubmatch(string(sql), -1) {
		db = append(db, m[1]+">"+m[2])
	}
	var goList []string
	for from, tos := range Edges {
		for _, to := range tos {
			goList = append(goList, from+">"+to)
		}
	}
	sort.Strings(db)
	sort.Strings(goList)
	if len(db) == 0 || len(db) != len(goList) {
		t.Fatalf("edge count differs: db %d go %d\n%v\n%v", len(db), len(goList), db, goList)
	}
	for i := range db {
		if db[i] != goList[i] {
			t.Fatalf("edge mismatch: db %s go %s", db[i], goList[i])
		}
	}
}

func TestActions(t *testing.T) {
	cases := []struct {
		a    Action
		from string
		want string
	}{
		{ActStartReview, Submitted, UnderReview},
		{ActStartReview, Draft, ""},
		{ActApprove, UnderReview, Approved},
		{ActApprove, Escalated, Approved},
		{ActApprove, Submitted, ""}, // must start review first
		{ActApprove, Approved, ""},  // no double approval
		{ActReject, Approved, ""},   // a completed decision is not silently changed
		{ActRequestInfo, UnderReview, AdditionalInfoRequired},
		{ActEscalate, UnderReview, Escalated},
		{ActReturn, Escalated, UnderReview},
		{ActReturn, UnderReview, ""},
		{ActSuspend, Approved, Suspended},
		{ActReinstate, Suspended, Approved},
		{ActReinstate, Approved, ""},
		{ActReopen, Suspended, UnderReview},
		{ActRevoke, Approved, Revoked},
		{ActRevoke, Suspended, Revoked},
		{ActRevoke, Rejected, ""},
	}
	for _, c := range cases {
		if got := Target(c.a, c.from); got != c.want {
			t.Errorf("%s from %s: %q, want %q", c.a, c.from, got, c.want)
		}
	}
	for _, s := range []string{Rejected, Expired, Revoked, Withdrawn} {
		if !Final(s) || Open(s) {
			t.Errorf("%s should be final", s)
		}
	}
	if !Editable(Draft) || !Editable(AdditionalInfoRequired) || Editable(Submitted) {
		t.Error("editable states")
	}
}
