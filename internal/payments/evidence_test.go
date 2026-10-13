package payments

import (
	"testing"

	"github.com/Fatifizo/fundzim/internal/psp"
)

func TestClassifyEvidence(t *testing.T) {
	amt := func(n int64) *int64 { return &n }
	usd := "USD"
	ok := []evidenceRow{{ID: "e1", Mapped: psp.StatusPending}, {ID: "e2", Mapped: psp.StatusSucceeded, Amount: amt(500), Currency: &usd}}
	cases := []struct {
		name   string
		prev   []evidenceRow
		mapped string
		isMap  bool
		amount *int64
		ccy    string
		want   string
		contra string
	}{
		{"first", nil, psp.StatusPending, true, nil, "", EvidenceFirst, ""},
		{"same status again", ok, psp.StatusSucceeded, true, amt(500), "USD", EvidenceConsistent, ""},
		{"progression", ok[:1], psp.StatusSucceeded, true, nil, "", EvidenceProgression, ""},
		{"failed after succeeded", ok, psp.StatusFailed, true, nil, "", EvidenceContradicts, "e2"},
		{"expired after succeeded", ok, psp.StatusExpired, true, nil, "", EvidenceContradicts, "e2"},
		{"cancelled after refunded", []evidenceRow{{ID: "r", Mapped: psp.StatusRefunded}}, psp.StatusCancelled, true, nil, "", EvidenceContradicts, "r"},
		{"different amount", ok, psp.StatusSucceeded, true, amt(501), "USD", EvidenceContradicts, "e2"},
		{"different currency", ok, psp.StatusSucceeded, true, amt(500), "ZWG", EvidenceContradicts, "e2"},
		{"late success is not a contradiction here", []evidenceRow{{ID: "f", Mapped: psp.StatusFailed}}, psp.StatusSucceeded, true, nil, "", EvidenceProgression, ""},
		{"unmapped after success", ok, psp.StatusUnknown, false, nil, "", EvidenceProgression, ""},
	}
	for _, c := range cases {
		got, contra := classifyEvidence(c.prev, c.mapped, c.isMap, c.amount, c.ccy)
		if got != c.want || contra != c.contra {
			t.Errorf("%s: got %s/%q want %s/%q", c.name, got, contra, c.want, c.contra)
		}
	}
}
