package beneficiaries

import (
	"testing"
	"time"
)

func TestAgeOn(t *testing.T) {
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	for dob, want := range map[string]int{"2008-10-09": 18, "2008-10-10": 17, "2026-10-09": 0, "1990-01-01": 36} {
		if got, ok := ageOn(dob, now); !ok || got != want {
			t.Errorf("%s: %d %v, want %d", dob, got, ok, want)
		}
	}
	for _, bad := range []string{"2027-01-01", "09/10/2008", ""} {
		if _, ok := ageOn(bad, now); ok {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestCleanText(t *testing.T) {
	if v, ok := cleanText("  Rudo   Moyo ", 1, 100); !ok || v != "Rudo Moyo" {
		t.Fatalf("%q %v", v, ok)
	}
	for _, bad := range []string{"", "<b>x</b>", "{x}"} {
		if _, ok := cleanText(bad, 1, 100); ok {
			t.Errorf("%q accepted", bad)
		}
	}
}
