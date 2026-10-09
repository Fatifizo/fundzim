package kyc

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// seededRules is the currently shipped policy: v1 from 20261009150200 plus the `basic` section that
// 20261009170000 adds in v2 (ADR-037), so what the migrations approve is what is validated.
func seededRules(t *testing.T) PolicyRules {
	t.Helper()
	r := seededV1Rules(t)
	r.Basic = seededBasic(t)
	return r
}

// seededV1Rules extracts the v1 policy JSON from the migration.
func seededV1Rules(t *testing.T) PolicyRules {
	t.Helper()
	raw, err := os.ReadFile("../../migrations/20261009150200_kyc.sql")
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	start := strings.Index(s, "'v1', '{")
	end := strings.Index(s[start:], "}', 'PROPOSED'")
	if start < 0 || end < 0 {
		t.Fatal("seeded policy not found in migration")
	}
	var r PolicyRules
	if err := json.Unmarshal([]byte(s[start+len("'v1', '"):start+end+1]), &r); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestSeededPolicyValidates(t *testing.T) {
	r := seededRules(t)
	if err := r.Validate(); err != nil {
		t.Fatalf("seeded v1 policy: %v", err)
	}
	if r.KYC[RiskStandard].SecondApproval || !r.KYC[RiskEnhanced].SecondApproval {
		t.Fatal("STANDARD must be single review and ENHANCED four-eyes")
	}
}

func TestPolicyValidateRefusesUnsafeRules(t *testing.T) {
	cases := map[string]func(*PolicyRules){
		"minor without second approval": func(r *PolicyRules) {
			m := r.Beneficiary["MINOR"]
			m.SecondApproval = false
			r.Beneficiary["MINOR"] = m
		},
		"restricted without second approval": func(r *PolicyRules) {
			k := r.KYC[RiskRestricted]
			k.SecondApproval = false
			r.KYC[RiskRestricted] = k
		},
		"unknown document type": func(r *PolicyRules) {
			k := r.KYC[RiskStandard]
			k.RequiredDocuments = append(k.RequiredDocuments, Requirement{"DRIVING_LICENCE_SCAN"})
			r.KYC[RiskStandard] = k
		},
		"empty requirement": func(r *PolicyRules) {
			k := r.KYC[RiskLow]
			k.RequiredDocuments = append(k.RequiredDocuments, Requirement{})
			r.KYC[RiskLow] = k
		},
		"missing risk level": func(r *PolicyRules) { delete(r.KYC, RiskEnhanced) },
		"validity out of range": func(r *PolicyRules) {
			k := r.KYC[RiskLow]
			k.ValidityDays = 99999
			r.KYC[RiskLow] = k
		},
		"information request expiry": func(r *PolicyRules) { r.InformationRequestExpiryDays = 0 },
		"adult age missing":          func(r *PolicyRules) { r.AdultAge = 0 },
	}
	for name, mutate := range cases {
		r := seededRules(t)
		mutate(&r)
		if r.Validate() == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestIdentityNumberMaskingAndNormalisation(t *testing.T) {
	if got := NormalizeIDNumber(" 63-123456 a 42/ "); got != "63123456A42" {
		t.Fatalf("normalise: %q", got)
	}
	if got := MaskIDNumber("63-123456A42"); got != "*********42" || strings.Contains(got, "123456") {
		t.Fatalf("mask: %q", got)
	}
	if got := MaskIDNumber("AB1"); got != "****" {
		t.Fatalf("short mask: %q", got)
	}
}

func TestReasonCodesAndLevels(t *testing.T) {
	for _, c := range []string{"DOCUMENTS_CONSISTENT", "ABC"} {
		if !ValidReasonCode(c) {
			t.Errorf("%s refused", c)
		}
	}
	for _, c := range []string{"", "ok", "Ab_C", "A B", "AB", strings.Repeat("A", 65), "DROP TABLE"} {
		if ValidReasonCode(c) {
			t.Errorf("%q accepted", c)
		}
	}
	if !AtLeast(LevelPayout, LevelIdentity) || AtLeast(LevelBasic, LevelIdentity) || AtLeast("NONSENSE", LevelUnverified) && !AtLeast(LevelUnverified, LevelUnverified) {
		t.Fatal("level ordering")
	}
}

func TestIdentityPatchValidation(t *testing.T) {
	str := func(s string) *string { return &s }
	var d IdentityDraft
	det := IdentityPatch{LegalFirstName: str("<script>"), Nationality: str("zimbabwe"), IDDocumentType: str("LIBRARY_CARD"),
		IDDocumentNumber: str("!!"), DateOfBirth: str("1990-13-40")}.apply(&d)
	fields := map[string]bool{}
	for _, x := range det {
		fields[x.Field] = true
	}
	for _, f := range []string{"legal_first_name", "nationality", "id_document_type", "id_document_number", "date_of_birth"} {
		if !fields[f] {
			t.Errorf("%s not refused (%v)", f, det)
		}
	}
	d = IdentityDraft{}
	if det := (IdentityPatch{LegalFirstName: str("  Tariro   Rudo "), Nationality: str("ZW"), IDDocumentType: str("ZW_PASSPORT"),
		IDDocumentNumber: str("FN123456")}).apply(&d); len(det) != 0 || d.LegalFirstName != "Tariro Rudo" {
		t.Fatalf("valid patch: %v %+v", det, d)
	}
	// a national ID number format is NOT validated against an assumed legal structure
	d = IdentityDraft{}
	if det := (IdentityPatch{IDDocumentType: str("ZW_NATIONAL_ID"), IDDocumentNumber: str("ANY 1234 FORMAT")}).apply(&d); len(det) != 0 {
		t.Fatalf("structural national-ID rule applied: %v", det)
	}
}
