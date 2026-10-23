package secretstore

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Stage 18 failure injection (docs/stage-18/network-security.md §3): the secret-manager client verifies certificates
// against the configured private CA and refuses an expired server certificate, a certificate for another name and a
// certificate from an unknown CA — keys are never fetched over a connection it cannot authenticate.
func TestTLSRefusesExpiredWrongNameAndUnknownCA(t *testing.T) {
	ca, caKey := newCA(t)
	caFile := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(caFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ca.Raw}), 0o600); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	cases := []struct {
		name       string
		notAfter   time.Time
		host       string
		selfSigned bool
		wantErr    bool
	}{
		{"valid certificate from the private CA", now.Add(time.Hour), "127.0.0.1", false, false},
		{"expired certificate", now.Add(-time.Minute), "127.0.0.1", false, true},
		{"certificate for another name", now.Add(time.Hour), "secrets.other.example", false, true},
		{"certificate from an unknown CA", now.Add(time.Hour), "127.0.0.1", true, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newFake()
			f.put("fundzim/test/key", strings.Repeat("k", 40))
			srv := httptest.NewUnstartedServer(f)
			srv.TLS = &tls.Config{Certificates: []tls.Certificate{leaf(t, ca, caKey, c.host, c.notAfter, c.selfSigned)}}
			srv.StartTLS()
			defer srv.Close()
			kv, err := New(Config{Addr: srv.URL, Mount: "secret", Token: "root-token", CACert: caFile, Timeout: 2 * time.Second})
			if err != nil {
				t.Fatal(err)
			}
			_, err = kv.Resolve(context.Background(), "secretref://fundzim/test/key")
			if (err != nil) != c.wantErr {
				t.Fatalf("Resolve error = %v, want error %v", err, c.wantErr)
			}
		})
	}
}

func newCA(t *testing.T) (*x509.Certificate, *ecdsa.PrivateKey) {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "fundzim test CA"}, NotBefore: time.Now().Add(-time.Hour),
		NotAfter: time.Now().Add(24 * time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	ca, _ := x509.ParseCertificate(der)
	return ca, key
}

func leaf(t *testing.T, ca *x509.Certificate, caKey *ecdsa.PrivateKey, host string, notAfter time.Time, selfSigned bool) tls.Certificate {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tpl := &x509.Certificate{SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: host},
		NotBefore: notAfter.Add(-48 * time.Hour), NotAfter: notAfter, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	if ip := net.ParseIP(host); ip != nil {
		tpl.IPAddresses = []net.IP{ip}
	} else {
		tpl.DNSNames = []string{host}
	}
	parent, signer := ca, caKey
	if selfSigned {
		parent, signer = tpl, key
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, parent, &key.PublicKey, signer)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}
