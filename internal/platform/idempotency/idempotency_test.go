package idempotency

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

func TestFingerprint(t *testing.T) {
	base := Fingerprint("POST", "POST /api/v1/x", "/api/v1/x", "", []byte(`{"a":1,"b":{"d":[1,2],"c":"x"}}`))
	if len(base) != 32 {
		t.Fatalf("len %d", len(base))
	}
	same := Fingerprint("POST", "POST /api/v1/x", "/api/v1/x", "", []byte("{ \"b\": {\"c\":\"x\", \"d\":[1, 2]},\n \"a\": 1 }"))
	if !bytes.Equal(base, same) {
		t.Fatal("canonically equal JSON must hash equal")
	}
	diff := map[string][]byte{
		"method": Fingerprint("PUT", "POST /api/v1/x", "/api/v1/x", "", []byte(`{"a":1,"b":{"d":[1,2],"c":"x"}}`)),
		"route":  Fingerprint("POST", "POST /api/v1/y", "/api/v1/x", "", []byte(`{"a":1,"b":{"d":[1,2],"c":"x"}}`)),
		"path":   Fingerprint("POST", "POST /api/v1/x", "/api/v1/z", "", []byte(`{"a":1,"b":{"d":[1,2],"c":"x"}}`)),
		"query":  Fingerprint("POST", "POST /api/v1/x", "/api/v1/x", "q=1", []byte(`{"a":1,"b":{"d":[1,2],"c":"x"}}`)),
		"body":   Fingerprint("POST", "POST /api/v1/x", "/api/v1/x", "", []byte(`{"a":2,"b":{"d":[1,2],"c":"x"}}`)),
		"number": Fingerprint("POST", "POST /api/v1/x", "/api/v1/x", "", []byte(`{"a":1.0,"b":{"d":[1,2],"c":"x"}}`)),
		"order":  Fingerprint("POST", "POST /api/v1/x", "/api/v1/x", "", []byte(`{"a":1,"b":{"d":[2,1],"c":"x"}}`)),
	}
	for name, h := range diff {
		if bytes.Equal(base, h) {
			t.Fatalf("%s change must change the fingerprint", name)
		}
	}
	// Field boundaries are unambiguous; non-JSON bodies hash as raw bytes.
	if bytes.Equal(Fingerprint("PO", "ST", "/", "", nil), Fingerprint("POS", "T", "/", "", nil)) {
		t.Fatal("ambiguous field boundaries")
	}
	if bytes.Equal(Fingerprint("POST", "r", "/", "", []byte("a=1")), Fingerprint("POST", "r", "/", "", []byte("a=2"))) {
		t.Fatal("raw body ignored")
	}
	if bytes.Equal(Fingerprint("POST", "r", "/", "", []byte(`{"a":1} x`)), Fingerprint("POST", "r", "/", "", []byte(`{"a":1}`))) {
		t.Fatal("trailing data must not be canonicalised away")
	}
}

func TestMiddlewareHeaderValidation(t *testing.T) {
	s := NewStore(nil, nil, 0) // never reached by these requests
	called := 0
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called++; w.WriteHeader(204) })
	do := func(mode Mode, keys ...string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", "/x", bytes.NewReader([]byte(`{}`)))
		for _, k := range keys {
			req.Header.Add(HeaderKey, k)
		}
		rec := httptest.NewRecorder()
		Middleware(s, mode, "test.scope", quiet)(next).ServeHTTP(rec, req)
		return rec
	}
	code := func(rec *httptest.ResponseRecorder) string {
		var b struct {
			Error struct{ Code string } `json:"error"`
		}
		_ = json.NewDecoder(rec.Body).Decode(&b)
		return b.Error.Code
	}
	if rec := do(Required); rec.Code != 400 || code(rec) != CodeKeyRequired {
		t.Fatalf("required missing: %d", rec.Code)
	}
	for _, bad := range []string{"abc", "not-a-uuid-not-a-uuid-not-a-uuid-xxxx", "{0190a5c4-7f3a-7cc2-8d3f-6b9e0c1a2b3c}"} {
		if rec := do(Optional, bad); rec.Code != 400 || code(rec) != CodeKeyInvalid {
			t.Fatalf("invalid key %q: %d", bad, rec.Code)
		}
	}
	if rec := do(Optional, "0190a5c4-7f3a-7cc2-8d3f-6b9e0c1a2b3c", "0190a5c4-7f3a-7cc2-8d3f-6b9e0c1a2b3d"); rec.Code != 400 {
		t.Fatal("two keys must be invalid")
	}
	if called != 0 {
		t.Fatal("handler ran on invalid requests")
	}
	if rec := do(Optional); rec.Code != 204 || called != 1 {
		t.Fatal("optional mode without a key must pass through")
	}
}

func TestMiddlewarePanicsOnBadScope(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic")
		}
	}()
	Middleware(NewStore(nil, nil, 0), Optional, "has space", quiet)
}

func TestCaptureWriterAndHelpers(t *testing.T) {
	cw := &captureWriter{header: http.Header{}}
	cw.Write([]byte("x"))
	cw.WriteHeader(500)
	if cw.status != 200 || cw.buf.String() != "x" {
		t.Fatal("first status wins")
	}
	if ceilSeconds(0) != 1 || ceilSeconds(1500*time.Millisecond) != 2 {
		t.Fatal("ceilSeconds")
	}
	rec := httptest.NewRecorder()
	writeReplay(rec, beginResult{status: 201, body: []byte(`{"data":1}`)})
	if rec.Code != 201 || rec.Header().Get(HeaderReplayed) != "true" || rec.Body.String() != `{"data":1}` {
		t.Fatal("replay")
	}
	rec = httptest.NewRecorder()
	writeReplay(rec, beginResult{status: 204})
	if rec.Code != 204 || rec.Body.Len() != 0 {
		t.Fatal("empty replay")
	}
}
