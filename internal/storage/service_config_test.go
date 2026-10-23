package storage

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	pstorage "github.com/Fatifizo/fundzim/internal/platform/storage"
)

func lazyPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	p, err := pgxpool.New(context.Background(), "postgres://nobody@127.0.0.1:1/none?connect_timeout=1")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	return p
}

func TestNewRefusesUnsafeProductionConfig(t *testing.T) {
	blobs, _ := pstorage.New(pstorage.Options{})
	key := []byte(strings.Repeat("t", 32))
	sse := []byte(strings.Repeat("s", 32))
	base := Deps{Pool: lazyPool(t), Blobs: blobs}

	d := base
	d.Scanner = DevScanner{}
	if _, err := New(Config{AppEnv: "production", TicketKey: key, SSEKey: sse}, d); !errors.Is(err, ErrDevScannerInProduction) {
		t.Errorf("dev scanner in production: %v", err)
	}
	if _, err := New(Config{AppEnv: "production", TicketKey: key}, base); err == nil {
		t.Error("production without SSE key accepted")
	}
	if _, err := New(Config{AppEnv: "production", TicketKey: key, SSEKey: sse}, base); err == nil {
		t.Error("production with a local SSE key accepted (KMS required)")
	}
	if _, err := New(Config{AppEnv: "development", TicketKey: key, SSEKey: []byte("short")}, base); err == nil {
		t.Error("short SSE key accepted")
	}
	if _, err := New(Config{AppEnv: "development", SSEKey: sse}, base); err == nil {
		t.Error("missing ticket key accepted")
	}
	s, err := New(Config{AppEnv: "development", TicketKey: key, SSEKey: sse}, d)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(s.keyID, "sse-c/hmac-sha256/local/") {
		t.Errorf("key id %q", s.keyID)
	}
	if s.sse(BucketPublicMedia, tObj) != nil || len(s.sse(BucketPrivateKYC, tObj)) != 32 {
		t.Error("SSE key derivation")
	}
	if string(s.sse(BucketPrivateKYC, tObj)) == string(s.sse(BucketPrivateKYC, tUser)) {
		t.Error("per-object keys must differ")
	}
	u, err := New(Config{AppEnv: "development", TicketKey: key}, base)
	if err != nil || u.keyID != UnencryptedKeyID || u.sse(BucketPrivateKYC, tObj) != nil {
		t.Errorf("unencrypted dev config: %v %q", err, u.keyID)
	}
}

func TestTransitionTableMatchesStatuses(t *testing.T) {
	for from, tos := range Transitions {
		for _, to := range tos {
			if to == StatusClean && from != StatusScanning {
				t.Errorf("CLEAN reachable from %q: only a completed scan may produce CLEAN", from)
			}
		}
	}
}

// Stage 18 (ADR-050): a managed (secret-manager) master is accepted outside development; objects written under a previous
// master stay readable while it is listed; a retired master makes its objects unavailable (never readable under the
// wrong key); new objects always use the active master.
func TestSSEMasterRotation(t *testing.T) {
	blobs, _ := pstorage.New(pstorage.Options{})
	key := []byte(strings.Repeat("t", 32))
	oldM := []byte(strings.Repeat("o", 32))
	newM := []byte(strings.Repeat("n", 32))
	base := Deps{Pool: lazyPool(t), Blobs: blobs}

	before, err := New(Config{AppEnv: "production", TicketKey: key, SSEKey: oldM, SSEKeyManaged: true}, base)
	if err != nil {
		t.Fatalf("managed key in production: %v", err)
	}
	if !strings.HasPrefix(before.keyID, "sse-c/hmac-sha256/managed/") {
		t.Fatalf("managed key id %q", before.keyID)
	}
	oldID := before.keyID
	rotated, err := New(Config{AppEnv: "production", TicketKey: key, SSEKey: newM, SSEKeyManaged: true, SSEPreviousKeys: [][]byte{oldM}}, base)
	if err != nil {
		t.Fatal(err)
	}
	if rotated.keyID == oldID {
		t.Fatal("rotation kept the key id")
	}
	got, ok := rotated.sseFor(BucketPrivateKYC, tObj, &oldID)
	if !ok || string(got) != string(before.sse(BucketPrivateKYC, tObj)) {
		t.Fatal("an object under the previous master is not readable during the overlap")
	}
	if string(rotated.sse(BucketPrivateKYC, tObj)) == string(got) {
		t.Fatal("new objects must use the active master")
	}
	retired, _ := New(Config{AppEnv: "production", TicketKey: key, SSEKey: newM, SSEKeyManaged: true}, base)
	if _, ok := retired.sseFor(BucketPrivateKYC, tObj, &oldID); ok {
		t.Fatal("an object under a retired master must be unavailable")
	}
	none := UnencryptedKeyID
	if _, ok := retired.sseFor(BucketPrivateKYC, tObj, &none); ok {
		t.Fatal("an unencrypted record must not be served once a key is required")
	}
	if _, ok := retired.sseFor(BucketPrivateKYC, tObj, nil); ok {
		t.Fatal("a record without a key id must not be served")
	}
	if _, err := New(Config{AppEnv: "production", TicketKey: key, SSEKey: newM, SSEKeyManaged: true, SSEPreviousKeys: [][]byte{newM}}, base); err == nil {
		t.Fatal("a previous master equal to the active one must be refused")
	}
	if _, err := New(Config{AppEnv: "development", TicketKey: key, SSEPreviousKeys: [][]byte{oldM}}, base); err == nil {
		t.Fatal("previous masters without an active one must be refused")
	}
}
