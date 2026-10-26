package fees

import (
	"slices"
	"testing"
)

// F-R2-01: every missing fee category of a group is its own discrepancy with its own dedupe key; an open finding of one
// category or comparison never hides another; repeated judgements of the same discrepancy produce the same key.

func comp(cat, basis string) componentRow {
	if basis == BasisUnknown {
		return componentRow{Category: cat, Basis: basis}
	}
	return componentRow{Category: cat, Basis: basis, Amount: p64(100)}
}

func missingKeys(fs []finding) []string {
	var out []string
	for _, f := range fs {
		out = append(out, dedupeKey(f))
	}
	slices.Sort(out)
	return out
}

func TestMissingFindingsReportsEveryMissingCategory(t *testing.T) {
	group := []Assessment{{ID: "a1", Currency: "USD", Category: CatPlatform}, {ID: "a2", Currency: "USD", Category: CatProcessing}}
	have := func(cats ...string) map[string]bool {
		m := map[string]bool{}
		for _, a := range group {
			m[a.Category] = true
		}
		for _, c := range cats {
			m[c] = true
		}
		return m
	}
	full := []componentRow{comp(CatPlatform, BasisQuoted), comp(CatProcessing, BasisEstimated)}
	cases := []struct {
		name string
		cs   []componentRow
		have map[string]bool
		want []string
	}{
		{"complete group", full, have(), nil},
		{"one missing", append(slices.Clone(full), comp(CatRefund, BasisUnknown)), have(), []string{"MISSING_FEE:a1::category:REFUND_FEE"}},
		{"two missing", append(slices.Clone(full), comp(CatRefund, BasisUnknown), comp(CatChargeback, BasisQuoted)), have(),
			[]string{"MISSING_FEE:a1::category:CHARGEBACK_FEE", "MISSING_FEE:a1::category:REFUND_FEE"}},
		{"several missing", append(slices.Clone(full), comp(CatPayout, BasisEstimated), comp(CatRefund, BasisUnknown), comp(CatChargeback, BasisQuoted)), have(),
			[]string{"MISSING_FEE:a1::category:CHARGEBACK_FEE", "MISSING_FEE:a1::category:PAYOUT_PROCESSING_FEE", "MISSING_FEE:a1::category:REFUND_FEE"}},
		// an OPEN finding for REFUND_FEE (have) leaves CHARGEBACK_FEE reported
		{"open finding for one, another newly missing", append(slices.Clone(full), comp(CatRefund, BasisUnknown), comp(CatChargeback, BasisQuoted)),
			have(CatRefund), []string{"MISSING_FEE:a1::category:CHARGEBACK_FEE"}},
		// a category listed twice is one discrepancy
		{"duplicate component category", append(slices.Clone(full), comp(CatRefund, BasisUnknown), comp(CatRefund, BasisQuoted)), have(),
			[]string{"MISSING_FEE:a1::category:REFUND_FEE"}},
		// NOT_CONFIGURED charges nothing: never missing
		{"not configured is never missing", append(slices.Clone(full), comp(CatRefund, BasisNotConfigured)), have(), nil},
		// an assessment whose category the calculation does not list (inconsistent group) neither creates nor hides a finding
		{"assessed category absent from the calculation", []componentRow{comp(CatPlatform, BasisQuoted), comp(CatChargeback, BasisQuoted)}, have(),
			[]string{"MISSING_FEE:a1::category:CHARGEBACK_FEE"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := missingKeys(missingFindings(group, c.cs, c.have))
			if !slices.Equal(got, c.want) {
				t.Fatalf("keys = %q, want %q", got, c.want)
			}
			// a second judgement of the same state yields the same keys (the open-uniqueness index absorbs them)
			if again := missingKeys(missingFindings(group, c.cs, c.have)); !slices.Equal(again, got) {
				t.Fatalf("repeated judgement changed the keys: %q → %q", got, again)
			}
		})
	}
}

func TestDedupeKeysSeparateComparisons(t *testing.T) {
	calc := finding{kind: RecAmountMismatch, assessmentID: "a1", detail: detailCalcComponent, check: checkCalcComponent}
	prov := finding{kind: RecAmountMismatch, assessmentID: "a1", detail: detailProviderReport, check: checkProviderReport}
	lines := finding{kind: RecAmountMismatch, assessmentID: "a1", txID: "t1", detail: detailJournalLines, check: checkJournalLines}
	posted := finding{kind: RecAmountMismatch, assessmentID: "a1", txID: "t1", detail: detailPostedAssessed, check: checkPostedAssessed}
	keys := map[string]bool{}
	for _, f := range []finding{calc, prov, lines, posted} {
		keys[dedupeKey(f)] = true
	}
	if len(keys) != 4 {
		t.Fatalf("comparisons on the same assessment and journal share a key: %v", keys)
	}
	// kinds without a check keep the pre-F-R2-01 key, so their OPEN references still deduplicate
	dup := finding{kind: RecDuplicateFee, assessmentID: "a1", txID: "t1"}
	if dedupeKey(dup) != "DUPLICATE_FEE:a1:t1" || legacyKey(dup) != dedupeKey(dup) {
		t.Fatalf("unchecked kind changed its key: %q", dedupeKey(dup))
	}
}

func TestLegacyCheckFromStoredContent(t *testing.T) {
	cases := []struct {
		kind, detail string
		expected     map[string]any
		want         string
	}{
		{RecMissingFee, "the calculation has a REFUND_FEE component but no assessment exists for it", map[string]any{"category": "REFUND_FEE"}, "category:REFUND_FEE"},
		{RecMissingFee, "x", map[string]any{}, ""},
		{RecAmountMismatch, detailCalcComponent, nil, checkCalcComponent},
		{RecAmountMismatch, detailProviderReport, nil, checkProviderReport},
		{RecAmountMismatch, detailJournalLines, nil, checkJournalLines},
		{RecAmountMismatch, detailPostedAssessed, nil, checkPostedAssessed},
		// content that matches no known comparison is never taken to represent a finding (it hides nothing)
		{RecAmountMismatch, "some older wording", nil, ""},
		{RecDuplicateFee, "more than one CAPTURE journal carries this fee", nil, ""},
	}
	for _, c := range cases {
		if got := legacyCheck(c.kind, c.detail, c.expected); got != c.want {
			t.Errorf("legacyCheck(%s, %q) = %q, want %q", c.kind, c.detail, got, c.want)
		}
	}
	// the check derived from a legacy reference equals the check of the finding the code produces today
	f := missingFindings([]Assessment{{ID: "a1", Currency: "USD", Category: CatPlatform}}, []componentRow{comp(CatRefund, BasisUnknown)}, map[string]bool{CatPlatform: true})[0]
	if legacyCheck(f.kind, f.detail, map[string]any{"category": CatRefund}) != f.check {
		t.Fatalf("legacy MISSING_FEE content does not map to the current check %q", f.check)
	}
}
