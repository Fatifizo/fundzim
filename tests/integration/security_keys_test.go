//go:build integration

package integration

// Stage 18 (ADR-050): application keys from the secret manager (the compose OpenBao dev server, never a production
// store). Proves resolution, the rotation overlap through a new KV version, and the failure modes: a missing key and a
// revoked credential stop startup, and no error carries a value or a reference.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/Fatifizo/fundzim/internal/platform/config"
	"github.com/Fatifizo/fundzim/internal/platform/crypto"
	"github.com/Fatifizo/fundzim/internal/platform/secretstore"
)

func randHex(t *testing.T) string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(b)
}

func TestSecurityKeysFromSecretManager(t *testing.T) {
	need(t, "SECRETS_KV2_ADDR", "SECRETS_KV2_TOKEN", "SECRETS_KV2_MOUNT")
	run := "fundzim/test/keys/it-" + randHex(t)[:8]
	names := []string{"FIELD_ENCRYPTION_LOCAL_KEY", "BLIND_INDEX_KEY", "KYC_FIELD_ENCRYPTION_LOCAL_KEY", "KYC_BLIND_INDEX_KEY",
		"COMPLIANCE_FIELD_ENCRYPTION_LOCAL_KEY", "DOCUMENT_TICKET_KEY", "STORAGE_SSE_C_KEY"}
	vals := map[string]string{}
	refs := map[string]string{}
	for _, n := range names {
		vals[n] = randHex(t)
		path := run + "/" + strings.ToLower(n)
		baoPut(t, path, vals[n])
		refs[n] = "secretref://" + path
	}
	sec := config.Security{FieldEncryptionProvider: "secretstore", Managed: true,
		FieldEncryptionKey: config.Secret(refs["FIELD_ENCRYPTION_LOCAL_KEY"]), BlindIndexKey: config.Secret(refs["BLIND_INDEX_KEY"]),
		KYCFieldKey: config.Secret(refs["KYC_FIELD_ENCRYPTION_LOCAL_KEY"]), KYCBlindIndexKey: config.Secret(refs["KYC_BLIND_INDEX_KEY"]),
		ComplianceFieldKey: config.Secret(refs["COMPLIANCE_FIELD_ENCRYPTION_LOCAL_KEY"]), DocumentTicketKey: config.Secret(refs["DOCUMENT_TICKET_KEY"]),
		StorageSSEKey: config.Secret(refs["STORAGE_SSE_C_KEY"])}
	kv := func(token string) *secretstore.KV2 {
		c, err := secretstore.New(secretstore.Config{Addr: os.Getenv("SECRETS_KV2_ADDR"), Mount: os.Getenv("SECRETS_KV2_MOUNT"), Token: token})
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	store := kv(os.Getenv("SECRETS_KV2_TOKEN"))

	resolved := sec
	if err := resolved.ResolveManaged(ctx(t), store.Resolve); err != nil {
		t.Fatalf("resolve from the secret manager: %v", err)
	}
	if resolved.FieldEncryptionKey.Reveal() != vals["FIELD_ENCRYPTION_LOCAL_KEY"] {
		t.Fatal("resolved field key differs from the stored one")
	}
	aead, err := crypto.NewAEAD("field-v1", resolved.FieldEncryptionKey.Reveal())
	if err != nil {
		t.Fatal(err)
	}
	ct, _ := aead.Seal([]byte("totp secret"), []byte("mfa_methods:it"))

	// rotation: a new active key in the store, the old one listed as previous; old data stays readable
	newKey := randHex(t)
	baoPut(t, run+"/field_encryption_next", newKey)
	rot := sec
	rot.FieldEncryptionKey = config.Secret("secretref://" + run + "/field_encryption_next")
	rot.FieldEncryptionPrevious = config.Secret(refs["FIELD_ENCRYPTION_LOCAL_KEY"])
	if err := rot.ResolveManaged(ctx(t), store.Resolve); err != nil {
		t.Fatalf("rotated configuration: %v", err)
	}
	ring, err := crypto.NewAEAD("field-v2", rot.FieldEncryptionKey.Reveal(), rot.FieldEncryptionPrevious.List()...)
	if err != nil {
		t.Fatal(err)
	}
	if pt, previous, err := ring.OpenWithStatus(ct, []byte("mfa_methods:it")); err != nil || !previous || string(pt) != "totp secret" {
		t.Fatalf("data sealed before the rotation: %q %v %v", pt, previous, err)
	}

	// failure injection: a missing key and a revoked/invalid credential stop startup; errors name variables only
	missing := sec
	missing.DocumentTicketKey = config.Secret("secretref://" + run + "/does_not_exist")
	err = missing.ResolveManaged(ctx(t), store.Resolve)
	if err == nil || !strings.Contains(err.Error(), "DOCUMENT_TICKET_KEY") || strings.Contains(err.Error(), run) {
		t.Fatalf("missing key: %v", err)
	}
	revoked := sec
	err = revoked.ResolveManaged(ctx(t), kv("not-a-valid-token-"+randHex(t)[:16]).Resolve)
	if err == nil {
		t.Fatal("an invalid secret-manager credential resolved keys")
	}
	for _, v := range vals {
		if strings.Contains(err.Error(), v) {
			t.Fatal("an error carries key material")
		}
	}
	unreachable := sec
	down, _ := secretstore.New(secretstore.Config{Addr: "http://127.0.0.1:1", Mount: "secret", Token: "x"})
	if err := unreachable.ResolveManaged(context.Background(), down.Resolve); err == nil {
		t.Fatal("an unreachable secret manager resolved keys")
	} else {
		t.Logf("secret manager outage: %v", fmt.Sprint(err)[:min(len(err.Error()), 120)])
	}
}
