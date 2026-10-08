package ids

import "testing"

func TestNewIsValidTimeOrderedAndUnique(t *testing.T) {
	seen := map[string]bool{}
	prev := ""
	for i := 0; i < 1000; i++ {
		id := New()
		if !Valid(id) || id[14] != '7' {
			t.Fatalf("%q is not a UUIDv7", id)
		}
		if seen[id] {
			t.Fatalf("duplicate %s", id)
		}
		if prev != "" && id < prev {
			t.Fatalf("not time ordered: %s < %s", id, prev)
		}
		seen[id], prev = true, id
	}
	for _, bad := range []string{"", "x", "0192f5a1-7c3e-7b8a-9d1e-2f3a4b5c6d7", "../../etc/passwd"} {
		if Valid(bad) {
			t.Errorf("%q accepted", bad)
		}
	}
}
