package storage

import (
	"bytes"
	"errors"
	"testing"
)

func TestSniffAllowList(t *testing.T) {
	cases := map[string]struct {
		in   []byte
		want string
	}{
		"png":                {samplePNG(), TypePNG},
		"jpeg":               {sampleJPEG(), TypeJPEG},
		"pdf":                {samplePDF(), TypePDF},
		"html":               {[]byte("<!doctype html><html><script>alert(1)</script></html>"), ""},
		"svg":                {[]byte(`<svg xmlns="http://www.w3.org/2000/svg" onload="alert(1)"/>`), ""},
		"gif":                {[]byte("GIF89a\x01\x00\x01\x00"), ""},
		"zip":                {[]byte("PK\x03\x04rest"), ""},
		"png magic only":     {[]byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}, ""},
		"pdf bad version":    {[]byte("%PDF-x.y\n"), ""},
		"leading whitespace": {append([]byte("  "), samplePDF()...), ""},
		"empty":              {nil, ""},
	}
	for name, c := range cases {
		if got := Sniff(c.in); got != c.want {
			t.Errorf("%s: Sniff = %q, want %q", name, got, c.want)
		}
	}
}

func TestPolyglotImageWithMarkupRefused(t *testing.T) {
	// a real PNG header followed by HTML inside the browser sniffing window (GIFAR/PNG-HTML polyglot)
	poly := append(samplePNG()[:33], []byte("<html><script>alert(document.cookie)</script></html>")...)
	if got := Sniff(poly); got != "" {
		t.Fatalf("polyglot sniffed as %q", got)
	}
	jp := sampleJPEG()
	poly2 := append(append([]byte{}, jp[:20]...), []byte("<SCRIPT>x</SCRIPT>")...)
	poly2 = append(poly2, jp[20:]...)
	if got := Sniff(poly2); got != "" {
		t.Fatalf("jpeg polyglot sniffed as %q", got)
	}
}

func TestCheckContent(t *testing.T) {
	big := bytes.Repeat([]byte{0}, 10)
	cases := []struct {
		name, bucket, declared string
		content                []byte
		tooLarge               bool
		wantErr                error
		wantReason, wantType   string
	}{
		{"png ok", BucketPrivateKYC, "image/png", samplePNG(), false, nil, "", TypePNG},
		{"jpeg alias", BucketPrivateKYC, "image/jpg", sampleJPEG(), false, nil, "", TypeJPEG},
		{"declared with params", BucketPrivateEvidence, "application/pdf; charset=binary", samplePDF(), false, nil, "", TypePDF},
		{"png declared as pdf", BucketPrivateKYC, "application/pdf", samplePNG(), false, ErrTypeMismatch, RejectTypeMismatch, ""},
		{"pdf declared as png", BucketPrivateKYC, "image/png", samplePDF(), false, ErrTypeMismatch, RejectTypeMismatch, ""},
		{"html declared as png", BucketPrivateKYC, "image/png", []byte("<html><body>hi</body></html>"), false, ErrUnsupportedType, RejectUnsupported, ""},
		{"png declared as html", BucketPrivateKYC, "text/html", samplePNG(), false, ErrTypeMismatch, RejectTypeMismatch, ""},
		{"octet-stream", BucketPrivateKYC, "application/octet-stream", samplePNG(), false, ErrTypeMismatch, RejectTypeMismatch, ""},
		{"garbage declared", BucketPrivateKYC, ";;;", samplePNG(), false, ErrTypeMismatch, RejectTypeMismatch, ""},
		{"pdf not allowed for public media", BucketPublicMedia, "application/pdf", samplePDF(), false, ErrUnsupportedType, RejectUnsupported, ""},
		{"empty", BucketPrivateKYC, "image/png", nil, false, ErrEmptyFile, RejectEmpty, ""},
		{"too large", BucketPrivateKYC, "image/png", big, true, ErrTooLarge, RejectTooLarge, ""},
	}
	for _, c := range cases {
		typ, reason, err := checkContent(c.bucket, c.declared, c.content, c.tooLarge)
		if c.wantErr == nil && err != nil || c.wantErr != nil && !errors.Is(err, c.wantErr) {
			t.Errorf("%s: err = %v, want %v", c.name, err, c.wantErr)
		}
		if reason != c.wantReason || typ != c.wantType {
			t.Errorf("%s: (%q, %q), want (%q, %q)", c.name, typ, reason, c.wantType, c.wantReason)
		}
	}
}

func TestPurposeTableMatchesBucketClasses(t *testing.T) {
	want := map[string]string{"PAYOUT_DESTINATION_EVIDENCE": BucketPrivateKYC, "COMPLIANCE_EVIDENCE": BucketPrivateEvidence,
		"KYC_DOCUMENT": BucketPrivateKYC, "KYB_DOCUMENT": BucketPrivateKYC, "BENEFICIARY_EVIDENCE": BucketPrivateKYC}
	for p, b := range want {
		if purposes[p].bucket != b {
			t.Errorf("%s in %s, want %s", p, purposes[p].bucket, b)
		}
	}
	for p, info := range purposes {
		if info.bucket == BucketPrivateKYC && info.classification != "C3" {
			t.Errorf("%s must be C3", p)
		}
		if platformClass(info.bucket) == "" {
			t.Errorf("%s has no platform class", p)
		}
	}
}
