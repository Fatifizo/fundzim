// Package secretstore resolves secret REFERENCES (secretref://…) from a secret manager that speaks the Vault KV
// version 2 HTTP API: OpenBao by default, HashiCorp Vault or a compatible managed service (ADR-041).
//
// secretref://<path> → GET /v1/<mount>/data/<path>, field "value". Values are cached for a bounded TTL and never
// served stale: a failed refresh after the TTL fails closed. Values never appear in errors, logs or metrics; errors
// carry the reference path and the HTTP status only.
package secretstore

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"
)

// ErrUnavailable means the reference does not resolve now (missing, forbidden, malformed, store unreachable or
// sealed). Callers treat it like a missing credential: the provider is not usable.
var ErrUnavailable = errors.New("secretstore: secret reference does not resolve")

// MinValueBytes refuses weak values (same floor as the environment resolver).
const MinValueBytes = 32

var refRE = regexp.MustCompile(`^secretref://[a-z0-9][a-z0-9/_.-]{2,200}$`)

// Config configures a KV v2 client.
type Config struct {
	Addr     string // https://bao.internal:8200 (http only outside production; enforced by config loading)
	Mount    string // KV v2 mount, e.g. "secret"
	Token    string // static token (development and CI only)
	RoleID   string // AppRole (required in production)
	SecretID string
	CACert   string // optional PEM file for a private CA
	CacheTTL time.Duration
	Timeout  time.Duration
	// OnRotate is called (outside the client's lock) when a reference resolves to a newer KV version than the
	// one seen before in this process. It receives versions only, never values.
	OnRotate func(ref string, from, to int)
	Now      func() time.Time
	HTTP     *http.Client
}

type entry struct {
	value   []byte
	version int
	at      time.Time
}

// KV2 is a KV v2 resolver. It is safe for concurrent use.
type KV2 struct {
	cfg  Config
	http *http.Client

	mu       sync.Mutex
	cache    map[string]entry
	versions map[string]int
	token    string
	tokenExp time.Time
	loginMu  sync.Mutex
}

// New validates cfg and builds a client.
func New(cfg Config) (*KV2, error) {
	u, err := url.Parse(cfg.Addr)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, errors.New("secretstore: address must be an http(s) URL")
	}
	cfg.Addr = strings.TrimRight(cfg.Addr, "/")
	if cfg.Mount == "" || strings.Contains(cfg.Mount, "..") {
		return nil, errors.New("secretstore: mount is required")
	}
	if cfg.Token == "" && (cfg.RoleID == "" || cfg.SecretID == "") {
		return nil, errors.New("secretstore: a token or an AppRole (role id and secret id) is required")
	}
	if cfg.CacheTTL <= 0 {
		cfg.CacheTTL = time.Minute
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 5 * time.Second
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	hc := cfg.HTTP
	if hc == nil {
		tr := http.DefaultTransport.(*http.Transport).Clone()
		tr.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
		if cfg.CACert != "" {
			pem, err := os.ReadFile(cfg.CACert)
			if err != nil {
				return nil, fmt.Errorf("secretstore: read CA file: %w", err)
			}
			pool := x509.NewCertPool()
			if !pool.AppendCertsFromPEM(pem) {
				return nil, errors.New("secretstore: CA file has no certificates")
			}
			tr.TLSClientConfig.RootCAs = pool
		}
		hc = &http.Client{Transport: tr, Timeout: cfg.Timeout,
			// never follow redirects: a redirect could send the token elsewhere
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	k := &KV2{cfg: cfg, http: hc, cache: map[string]entry{}, versions: map[string]int{}}
	if cfg.Token != "" {
		k.token = cfg.Token
	}
	return k, nil
}

// Resolve implements psp.SecretResolver.
func (k *KV2) Resolve(ctx context.Context, ref string) ([]byte, error) {
	if !refRE.MatchString(ref) || strings.Contains(ref, "..") {
		return nil, ErrUnavailable
	}
	now := k.cfg.Now()
	k.mu.Lock()
	if e, ok := k.cache[ref]; ok && now.Sub(e.at) < k.cfg.CacheTTL {
		v := append([]byte(nil), e.value...)
		k.mu.Unlock()
		return v, nil
	}
	k.mu.Unlock()

	val, ver, err := k.read(ctx, strings.TrimPrefix(ref, "secretref://"))
	if err != nil {
		k.mu.Lock()
		delete(k.cache, ref) // fail closed: never serve a value past its TTL
		k.mu.Unlock()
		return nil, err
	}
	k.mu.Lock()
	prev, seen := k.versions[ref]
	k.versions[ref] = ver
	k.cache[ref] = entry{value: val, version: ver, at: now}
	k.mu.Unlock()
	if seen && ver > prev && k.cfg.OnRotate != nil {
		k.cfg.OnRotate(ref, prev, ver)
	}
	return append([]byte(nil), val...), nil
}

// Version returns the KV version last resolved for ref in this process (0 when never resolved).
func (k *KV2) Version(ref string) int {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.versions[ref]
}

// Health reports whether the store is reachable, initialised and unsealed (GET /v1/sys/health → 200).
func (k *KV2) Health(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, k.cfg.Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, k.cfg.Addr+"/v1/sys/health", nil)
	if err != nil {
		return err
	}
	resp, err := k.http.Do(req)
	if err != nil {
		return fmt.Errorf("secretstore: health: %w", ErrUnavailable)
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("secretstore: health: HTTP %d: %w", resp.StatusCode, ErrUnavailable)
	}
	return nil
}

type kvResponse struct {
	Data struct {
		Data     map[string]any `json:"data"`
		Metadata struct {
			Version      int    `json:"version"`
			DeletionTime string `json:"deletion_time"`
			Destroyed    bool   `json:"destroyed"`
		} `json:"metadata"`
	} `json:"data"`
}

func (k *KV2) read(ctx context.Context, path string) ([]byte, int, error) {
	for attempt := 0; attempt < 2; attempt++ {
		tok, err := k.currentToken(ctx)
		if err != nil {
			return nil, 0, err
		}
		status, body, err := k.do(ctx, http.MethodGet, "/v1/"+k.cfg.Mount+"/data/"+path, tok, nil)
		if err != nil {
			return nil, 0, fmt.Errorf("secretstore: read %s: %w", path, ErrUnavailable)
		}
		if status == http.StatusForbidden && attempt == 0 && k.cfg.RoleID != "" {
			k.dropToken(tok) // expired or revoked AppRole token: log in again once
			continue
		}
		if status != http.StatusOK {
			return nil, 0, fmt.Errorf("secretstore: read %s: HTTP %d: %w", path, status, ErrUnavailable)
		}
		var r kvResponse
		if err := json.Unmarshal(body, &r); err != nil {
			return nil, 0, fmt.Errorf("secretstore: read %s: malformed response: %w", path, ErrUnavailable)
		}
		if r.Data.Metadata.DeletionTime != "" || r.Data.Metadata.Destroyed {
			return nil, 0, fmt.Errorf("secretstore: read %s: deleted: %w", path, ErrUnavailable)
		}
		v, _ := r.Data.Data["value"].(string)
		if len(v) < MinValueBytes {
			return nil, 0, fmt.Errorf("secretstore: read %s: value missing or shorter than %d bytes: %w", path, MinValueBytes, ErrUnavailable)
		}
		return []byte(v), r.Data.Metadata.Version, nil
	}
	return nil, 0, fmt.Errorf("secretstore: read %s: %w", path, ErrUnavailable)
}

func (k *KV2) currentToken(ctx context.Context) (string, error) {
	if k.cfg.RoleID == "" {
		return k.cfg.Token, nil
	}
	k.mu.Lock()
	tok, exp := k.token, k.tokenExp
	k.mu.Unlock()
	if tok != "" && k.cfg.Now().Before(exp) {
		return tok, nil
	}
	k.loginMu.Lock()
	defer k.loginMu.Unlock()
	k.mu.Lock()
	tok, exp = k.token, k.tokenExp
	k.mu.Unlock()
	if tok != "" && k.cfg.Now().Before(exp) {
		return tok, nil
	}
	body, _ := json.Marshal(map[string]string{"role_id": k.cfg.RoleID, "secret_id": k.cfg.SecretID})
	status, resp, err := k.do(ctx, http.MethodPost, "/v1/auth/approle/login", "", body)
	if err != nil || status != http.StatusOK {
		return "", fmt.Errorf("secretstore: AppRole login failed (HTTP %d): %w", status, ErrUnavailable)
	}
	var r struct {
		Auth struct {
			ClientToken   string `json:"client_token"`
			LeaseDuration int    `json:"lease_duration"`
		} `json:"auth"`
	}
	if err := json.Unmarshal(resp, &r); err != nil || r.Auth.ClientToken == "" {
		return "", fmt.Errorf("secretstore: AppRole login: malformed response: %w", ErrUnavailable)
	}
	lease := time.Duration(r.Auth.LeaseDuration) * time.Second
	if lease <= 0 {
		lease = 5 * time.Minute
	}
	k.mu.Lock()
	k.token, k.tokenExp = r.Auth.ClientToken, k.cfg.Now().Add(lease*8/10) // renew by logging in again before expiry
	k.mu.Unlock()
	return r.Auth.ClientToken, nil
}

func (k *KV2) dropToken(tok string) {
	k.mu.Lock()
	if k.token == tok {
		k.token, k.tokenExp = "", time.Time{}
	}
	k.mu.Unlock()
}

func (k *KV2) do(ctx context.Context, method, path, token string, body []byte) (int, []byte, error) {
	ctx, cancel := context.WithTimeout(ctx, k.cfg.Timeout)
	defer cancel()
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, k.cfg.Addr+path, rd)
	if err != nil {
		return 0, nil, err
	}
	if token != "" {
		req.Header.Set("X-Vault-Token", token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := k.http.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return resp.StatusCode, b, err
}
