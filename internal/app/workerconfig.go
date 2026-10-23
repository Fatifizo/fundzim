package app

import (
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Fatifizo/fundzim/internal/platform/config"
)

// WorkerConfig holds the worker-only settings. They are read here rather than in internal/platform/config
// so the worker's variables do not become requirements of the API process. Errors name variables only,
// never values.
type WorkerConfig struct {
	DatabaseURL      config.Secret // DATABASE_WORKER_URL: the fundzim_worker role
	InternalHTTPPort int           // WORKER_INTERNAL_HTTP_PORT (default 9091): /healthz, /readyz, /metrics
	Concurrency      int           // WORKER_CONCURRENCY (default 4): workers on the default and outbox queues
	OutboxRetention  time.Duration // OUTBOX_RETENTION (default 168h): dispatched outbox rows kept this long
	MaxConns         int32         // WORKER_DATABASE_MAX_CONNS (default 10)
	// Stage 18 (ADR-050): audit chain verification. The checkpoint MAC key is the worker's alone (hex, 32 bytes) and must
	// differ from every other key, so a database-level rewrite of history cannot also forge a verified checkpoint.
	AuditCheckpointKey   config.Secret // AUDIT_CHECKPOINT_KEY
	AuditVerifyInterval  time.Duration // AUDIT_VERIFY_INTERVAL (default 15m): incremental verification from the last checkpoint
	AuditFullVerifyEvery time.Duration // AUDIT_FULL_VERIFY_INTERVAL (default 24h): full recomputation of both chains
}

// LoadWorkerConfig reads and validates the worker settings. env is the deployment environment from the
// main configuration (non-local environments require TLS-verified database connections).
func LoadWorkerConfig(get config.LookupFunc, env config.Env) (WorkerConfig, error) {
	var problems []string
	str := func(k string) string {
		if v, ok := get(k); ok {
			return strings.TrimSpace(v)
		}
		return ""
	}
	integer := func(k string, def, lo, hi int) int {
		raw := str(k)
		if raw == "" {
			return def
		}
		n, err := strconv.Atoi(raw)
		if err != nil || n < lo || n > hi {
			problems = append(problems, k+" must be an integer between "+strconv.Itoa(lo)+" and "+strconv.Itoa(hi))
			return def
		}
		return n
	}
	w := WorkerConfig{
		DatabaseURL:      config.Secret(str("DATABASE_WORKER_URL")),
		InternalHTTPPort: integer("WORKER_INTERNAL_HTTP_PORT", 9091, 1, 65535),
		Concurrency:      integer("WORKER_CONCURRENCY", 4, 1, 100),
		MaxConns:         int32(integer("WORKER_DATABASE_MAX_CONNS", 10, 2, 200)),
		OutboxRetention:  7 * 24 * time.Hour,

		AuditCheckpointKey:   config.Secret(str("AUDIT_CHECKPOINT_KEY")),
		AuditVerifyInterval:  15 * time.Minute,
		AuditFullVerifyEvery: 24 * time.Hour,
	}
	duration := func(k string, lo, hi time.Duration, into *time.Duration) {
		raw := str(k)
		if raw == "" {
			return
		}
		d, err := time.ParseDuration(raw)
		if err != nil || d < lo || d > hi {
			problems = append(problems, k+" must be a duration between "+lo.String()+" and "+hi.String())
			return
		}
		*into = d
	}
	duration("AUDIT_VERIFY_INTERVAL", time.Minute, 24*time.Hour, &w.AuditVerifyInterval)
	duration("AUDIT_FULL_VERIFY_INTERVAL", time.Hour, 31*24*time.Hour, &w.AuditFullVerifyEvery)
	if str("FIELD_ENCRYPTION_PROVIDER") == "secretstore" {
		// resolved at startup with the application keys (resolveWorkerKeys)
		if !strings.HasPrefix(w.AuditCheckpointKey.Reveal(), "secretref://") {
			problems = append(problems, "AUDIT_CHECKPOINT_KEY must be a secretref:// reference when FIELD_ENCRYPTION_PROVIDER=secretstore")
		}
	} else {
		others := map[string]string{}
		for _, n := range []string{"BLIND_INDEX_KEY", "FIELD_ENCRYPTION_LOCAL_KEY", "KYC_BLIND_INDEX_KEY", "KYC_FIELD_ENCRYPTION_LOCAL_KEY",
			"COMPLIANCE_FIELD_ENCRYPTION_LOCAL_KEY", "DOCUMENT_TICKET_KEY", "STORAGE_SSE_C_KEY", "CSRF_SECRET"} {
			others[n] = str(n)
		}
		if p := checkpointKeyProblem(w.AuditCheckpointKey.Reveal(), others); p != "" {
			problems = append(problems, p)
		}
	}
	if raw := str("OUTBOX_RETENTION"); raw != "" {
		d, err := time.ParseDuration(raw)
		if err != nil || d < time.Hour || d > 90*24*time.Hour {
			problems = append(problems, "OUTBOX_RETENTION must be a duration between 1h and 2160h")
		} else {
			w.OutboxRetention = d
		}
	}
	if w.DatabaseURL == "" {
		problems = append(problems, "DATABASE_WORKER_URL is required")
	} else if u, err := url.Parse(w.DatabaseURL.Reveal()); err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") || u.Host == "" {
		problems = append(problems, "DATABASE_WORKER_URL must be a postgres:// URL")
	} else {
		if !env.IsLocal() && u.Query().Get("sslmode") != "verify-full" {
			problems = append(problems, "DATABASE_WORKER_URL must use sslmode=verify-full outside development and test")
		}
		if u.User == nil || u.User.Username() == "" {
			problems = append(problems, "DATABASE_WORKER_URL must name the worker role")
		}
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		return WorkerConfig{}, &config.ValidationError{Problems: problems}
	}
	return w, nil
}

func sortedStrings(v []string) []string {
	sort.Strings(v)
	return v
}
