package organisations

import "testing"

func TestSlugify(t *testing.T) {
	for in, want := range map[string]string{
		"Harare Community Trust": "harare-community-trust",
		"  St. Mary's School!! ": "st-mary-s-school",
		"ÜÖ":                     "org",
		"a--b":                   "a-b",
	} {
		if got := slugify(in); got != want {
			t.Errorf("slugify(%q) = %q, want %q", in, got, want)
		}
	}
	long := slugify("x" + string(make([]byte, 0)) + "abcdefghij abcdefghij abcdefghij abcdefghij abcdefghij abcdefghij abcdefghij")
	if len(long) > 60 || long[len(long)-1] == '-' {
		t.Errorf("long slug: %q", long)
	}
}

func TestOnlyKnownRolesAndTypes(t *testing.T) {
	if _, ok := roleID("ORG_ADMIN"); !ok {
		t.Fatal("ORG_ADMIN")
	}
	for _, r := range []string{"SUPER_ADMIN", "org_admin", "", "ORG_OWNER"} {
		if _, ok := roleID(r); ok {
			t.Errorf("role %q must be rejected", r)
		}
	}
	if OrgTypes["BANK"] || !OrgTypes["PVO"] {
		t.Fatal("org types")
	}
}
