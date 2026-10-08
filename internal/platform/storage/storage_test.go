package storage

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestClassPrefixesAndPrivacy(t *testing.T) {
	if PublicCampaignMedia.Private() || !PrivateIdentityDocuments.Private() || !PrivateComplianceDocuments.Private() {
		t.Fatal("privacy flags wrong")
	}
	ref := NewRef(PrivateIdentityDocuments)
	if !strings.HasPrefix(ref.Key, "kyc/") || len(ref.Key) != len("kyc/")+36 {
		t.Fatalf("unexpected key %q", ref.Key)
	}
}

func TestRefsCannotEscapeTheirClass(t *testing.T) {
	c, err := New(Options{Endpoint: "http://127.0.0.1:1", Region: "x", Credentials: map[Class]Credential{
		PrivateComplianceDocuments: {Bucket: "b", AccessKeyID: "a", SecretAccessKey: "s"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, ref := range []Ref{
		{Class: PrivateComplianceDocuments, Key: "kyc/0192f5a1"},        // another class's prefix
		{Class: PrivateComplianceDocuments, Key: "compliance/../kyc/x"}, // traversal
	} {
		if _, err := c.Get(ctx, ref); !errors.Is(err, ErrWrongClass) {
			t.Errorf("%+v: expected ErrWrongClass, got %v", ref, err)
		}
	}
	if _, err := c.Get(ctx, NewRef(PrivateIdentityDocuments)); !errors.Is(err, ErrClassUnavailable) {
		t.Error("a client without the KYC credential must not serve the KYC class")
	}
	if err := c.Check(ctx, PublicCampaignMedia); !errors.Is(err, ErrClassUnavailable) {
		t.Error("unconfigured class must be unavailable")
	}
}

func TestIncompleteCredentialRejected(t *testing.T) {
	if _, err := New(Options{Credentials: map[Class]Credential{PublicCampaignMedia: {Bucket: "b"}}}); err == nil {
		t.Fatal("incomplete credential accepted")
	}
}
