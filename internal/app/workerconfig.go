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
