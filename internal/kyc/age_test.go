package kyc

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// seededBasic extracts the `basic` section that migration 20261009170000 adds to policy v2 (ADR-037).
func seededBasic(t *testing.T) *BasicRules {
	t.Helper()
	raw, err := os.ReadFile("../../migrations/20261009170000_age_attestation.sql")
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	start := strings.Index(s, `'{"basic": `)
	end := strings.Index(s, `}'::jsonb`)
	if start < 0 || end < start {
		t.Fatal("basic section not found in migration 20261009170000")
	}
	var doc struct {
		Basic *BasicRules `json:"basic"`
	}
	if err := json.Unmarshal([]byte(s[start+1:end+1]), &doc); err != nil {
		t.Fatal(err)
	}
	return doc.Basic
}

func TestPolicyV1WithoutBasicCannotBeProposedAgain(t *testing.T) {
	err := seededV1Rules(t).Validate()
	if err == nil || !strings.Contains(err.Error(), "basic missing") {
		t.Fatalf("v1 without a basic section must not validate as a new version: %v", err)
	}
}

func TestBasicRulesCannotDropConditions(t *testing.T) {
	b := seededBasic(t)
	if b.AgeStatementVersion != "age-statement-v1" || len(b.Requires) != 4 {
		t.Fatalf("shipped basic section: %+v", b)
	}
	cases := map[string]func(*BasicRules){
		"phone dropped":        func(b *BasicRules) { b.Requires = []string{BasicEmailVerified, BasicAgeAttested, BasicAccountActive} },
		"attestation dropped":  func(b *BasicRules) { b.Requires = []string{BasicEmailVerified, BasicPhoneVerified, BasicAccountActive} },
		"unknown condition":    func(b *BasicRules) { b.Requires = append(b.Requires, "DEVICE_RISK_OK") },
		"no statement version": func(b *BasicRules) { b.AgeStatementVersion = "" },
		"bad statement":        func(b *BasicRules) { b.AgeStatementVersion = "Age Statement <1>" },
	}
	for name, mutate := range cases {
		r := seededRules(t)
		cp := *r.Basic
		cp.Requires = append([]string(nil), r.Basic.Requires...)
		mutate(&cp)
		r.Basic = &cp
		if r.Validate() == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestPolicyRulesDecodeV2Strictly(t *testing.T) {
	// ProposePolicy decodes with DisallowUnknownFields: the v2 document (v1 + basic) must round-trip.
	r := seededRules(t)
	raw, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	var back PolicyRules
	if err := dec.Decode(&back); err != nil || back.Basic == nil || back.Basic.AgeStatementVersion != r.Basic.AgeStatementVersion {
		t.Fatalf("v2 round trip: %v %+v", err, back.Basic)
	}
}
