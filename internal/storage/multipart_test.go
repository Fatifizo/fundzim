package storage

import (
	"bytes"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"strings"
	"testing"
)

type part struct {
	name, filename, ctype string
	body                  []byte
}

func multipartRequest(t *testing.T, parts ...part) *http.Request {
	t.Helper()
	var b bytes.Buffer
	w := multipart.NewWriter(&b)
	for _, p := range parts {
		h := textproto.MIMEHeader{}
		cd := `form-data; name="` + p.name + `"`
		if p.filename != "" {
			cd += `; filename="` + p.filename + `"`
		}
		h.Set("Content-Disposition", cd)
		if p.ctype != "" {
			h.Set("Content-Type", p.ctype)
		}
		pw, err := w.CreatePart(h)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = pw.Write(p.body)
	}
	_ = w.Close()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/verification/documents", &b)
	r.Header.Set("Content-Type", w.FormDataContentType())
	return r
}

var spec = MultipartSpec{FileField: "file", Fields: []string{"subject_type", "subject_id", "document_type", "side"},
	Required: []string{"subject_type", "subject_id"}, MaxFileBytes: 1 << 20}

func TestParseMultipartHappyPathIgnoresFilename(t *testing.T) {
	png := samplePNG()
	r := multipartRequest(t, part{name: "subject_type", body: []byte("KYC_CASE")}, part{name: "subject_id", body: []byte(" abc ")},
		part{name: "file", filename: "../../../etc/passwd", ctype: "image/png", body: png})
	u, err := ParseMultipart(httptest.NewRecorder(), r, spec)
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(u.File)
	if err != nil || !bytes.Equal(got, png) {
		t.Fatalf("file: %v", err)
	}
	if u.Fields["subject_type"] != "KYC_CASE" || u.Fields["subject_id"] != "abc" || u.DeclaredContentType != "image/png" {
		t.Fatalf("fields: %+v", u)
	}
	for _, v := range u.Fields {
		if strings.Contains(v, "passwd") {
			t.Fatal("client filename leaked into fields")
		}
	}
}

func TestParseMultipartRejectsBadShapes(t *testing.T) {
	st := part{name: "subject_type", body: []byte("KYC_CASE")}
	sid := part{name: "subject_id", body: []byte("x")}
	file := part{name: "file", filename: "a.png", ctype: "image/png", body: samplePNG()}
	cases := map[string][]part{
		"no file":             {st, sid},
		"unknown field":       {st, sid, {name: "evil", body: []byte("1")}, file},
		"duplicate field":     {st, st, sid, file},
		"missing required":    {st, file},
		"field is a file":     {{name: "subject_type", filename: "x", body: []byte("x")}, sid, file},
		"file without name":   {st, sid, {name: "file", body: samplePNG()}},
		"oversized field":     {{name: "subject_type", body: bytes.Repeat([]byte("a"), 300)}, sid, file},
		"second file part":    {st, sid, file, file},
		"field after file":    {st, sid, file, {name: "side", body: []byte("FRONT")}},
		"part without a name": {st, sid, {name: "", body: []byte("x")}, file},
	}
	for name, parts := range cases {
		r := multipartRequest(t, parts...)
		u, err := ParseMultipart(httptest.NewRecorder(), r, spec)
		if err == nil {
			// shape errors after the file part surface while the file is read (before anything is stored)
			_, err = io.ReadAll(u.File)
		}
		if !errors.Is(err, ErrMalformedUpload) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestParseMultipartSizeCapAndContentType(t *testing.T) {
	big := part{name: "file", filename: "a.png", ctype: "image/png", body: bytes.Repeat([]byte("a"), 2<<20)}
	r := multipartRequest(t, part{name: "subject_type", body: []byte("K")}, part{name: "subject_id", body: []byte("x")}, big)
	if _, err := ParseMultipart(httptest.NewRecorder(), r, spec); !errors.Is(err, ErrTooLarge) {
		t.Errorf("declared length over cap: %v", err)
	}
	// chunked (no Content-Length): the cap bites while streaming
	r = multipartRequest(t, part{name: "subject_type", body: []byte("K")}, part{name: "subject_id", body: []byte("x")}, big)
	r.ContentLength = -1
	u, err := ParseMultipart(httptest.NewRecorder(), r, spec)
	if err == nil {
		_, err = io.ReadAll(u.File)
	}
	var mbe *http.MaxBytesError
	if !errors.As(err, &mbe) && !errors.Is(err, ErrTooLarge) {
		t.Errorf("streamed over cap: %v", err)
	}
	r = httptest.NewRequest(http.MethodPost, "/", strings.NewReader("{}"))
	r.Header.Set("Content-Type", "application/json")
	if _, err := ParseMultipart(httptest.NewRecorder(), r, spec); !errors.Is(err, ErrMalformedUpload) {
		t.Errorf("json body: %v", err)
	}
}
