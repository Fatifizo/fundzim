package psp

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Fatifizo/fundzim/internal/audit"
	"github.com/Fatifizo/fundzim/internal/platform/db"
	"github.com/Fatifizo/fundzim/internal/platform/ids"
)

// Health operations.
const (
	OpCreatePayment = "CREATE_PAYMENT"
	OpGetPayment    = "GET_PAYMENT"
	OpCancelPayment = "CANCEL_PAYMENT"
	OpWebhook       = "WEBHOOK"
)

// Circuit states.
const (
	CircuitClosed   = "CLOSED"
	CircuitOpen     = "OPEN"
	CircuitHalfOpen = "HALF_OPEN"
)

// Circuit-breaker parameters (technical, not regulatory): open after this many consecutive failed calls within
// the window; an open circuit is half-open (one probe allowed) after the cool-down.
const (
	circuitFailures = 5
	circuitWindow   = 60 * time.Second
	circuitCoolDown = 30 * time.Second
)

// Circuit derives the circuit state for (provider, operation, currency) from the latest CIRCUIT_* event.
func (s *Service) Circuit(ctx context.Context, providerID, op, currency string) (string, error) {
	var kind string
	var at time.Time
	err := s.Pool.QueryRow(ctx, `SELECT event_kind, observed_at FROM app.provider_health_events
		WHERE provider_id = $1 AND operation = $2 AND coalesce(currency, '') = $3 AND event_kind LIKE 'CIRCUIT_%'
		ORDER BY observed_at DESC, id DESC LIMIT 1`, providerID, op, currency).Scan(&kind, &at)
	if errors.Is(err, pgx.ErrNoRows) {
		return CircuitClosed, nil
	}
	if err != nil {
		return "", err
	}
	switch kind {
	case "CIRCUIT_OPENED":
		if s.now().Sub(at) >= circuitCoolDown {
			return CircuitHalfOpen, nil
		}
		return CircuitOpen, nil
	case "CIRCUIT_HALF_OPEN":
		return CircuitHalfOpen, nil
	}
	return CircuitClosed, nil
}

// ProviderCircuit is the worst circuit state of a provider over all operations (operations view).
func (s *Service) ProviderCircuit(ctx context.Context, providerID string) string {
	worst := CircuitClosed
	for _, op := range []string{OpCreatePayment, OpGetPayment} {
		rows, err := s.Pool.Query(ctx, `SELECT DISTINCT coalesce(currency, '') FROM app.provider_health_events
			WHERE provider_id = $1 AND operation = $2`, providerID, op)
		if err != nil {
			return worst
		}
		curs, _ := pgx.CollectRows(rows, pgx.RowTo[string])
		for _, c := range curs {
			st, err := s.Circuit(ctx, providerID, op, c)
			if err != nil {
				continue
			}
			if st == CircuitOpen || (st == CircuitHalfOpen && worst == CircuitClosed) {
				worst = st
			}
		}
	}
	return worst
}

// RecordCall writes CALL_OK / CALL_ERROR / TIMEOUT for one provider call and opens or closes the circuit. It is
// best-effort operational telemetry: a failure is logged, never returned (it must not change a payment outcome).
func (s *Service) RecordCall(ctx context.Context, providerID, providerCode, op, method, currency string, obs Observation) {
	s.Metrics.ProviderCalls.WithLabelValues(providerCode, op, string(obs.Class)).Inc()
	kind, errClass := "CALL_OK", ""
	switch obs.Class {
	case ClassAccepted, ClassNotFound, ClassDefiniteFailure:
		// the provider answered authoritatively: healthy
	case ClassIndeterminate:
		kind, errClass = "CALL_ERROR", string(ClassIndeterminate)
		if obs.UnknownReason == UnknownTimeout {
			kind = "TIMEOUT"
		}
	default:
		kind, errClass = "CALL_ERROR", string(obs.Class)
	}
	if errClass != "" {
		s.Metrics.ProviderErrors.WithLabelValues(providerCode, errClass).Inc()
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := s.insertHealth(ctx, providerID, op, method, currency, kind, errClass, obs.Latency, nil); err != nil {
		s.Logger.Warn("psp: record provider call failed", "provider", providerCode, "op", op, "err", err)
		return
	}
	st, err := s.Circuit(ctx, providerID, op, currency)
	if err != nil {
		return
	}
	if kind == "CALL_OK" {
		if st != CircuitClosed {
			_ = s.insertHealth(ctx, providerID, op, method, currency, "CIRCUIT_CLOSED", "", 0, nil)
		}
		return
	}
	if st == CircuitOpen {
		return
	}
	if st == CircuitHalfOpen {
		if s.insertHealth(ctx, providerID, op, method, currency, "CIRCUIT_OPENED", errClass, 0, map[string]any{"reason": "probe failed"}) == nil {
			s.auditUnavailable(ctx, providerID, providerCode, op, "CIRCUIT_REOPENED")
		}
		return
	}
	var bad int
	if err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM (SELECT event_kind FROM app.provider_health_events
		WHERE provider_id = $1 AND operation = $2 AND coalesce(currency, '') = $3 AND observed_at > $4
		  AND event_kind IN ('CALL_OK','CALL_ERROR','TIMEOUT') ORDER BY observed_at DESC, id DESC LIMIT $5) t
		WHERE t.event_kind <> 'CALL_OK'`, providerID, op, currency, s.now().Add(-circuitWindow), circuitFailures).Scan(&bad); err != nil {
		return
	}
	if bad >= circuitFailures {
		if s.insertHealth(ctx, providerID, op, method, currency, "CIRCUIT_OPENED", errClass, 0, map[string]any{"failures": bad}) == nil {
			s.auditUnavailable(ctx, providerID, providerCode, op, "CIRCUIT_OPENED")
		}
	}
}

// auditUnavailable records PROVIDER_UNAVAILABLE when the circuit opens (observed, not declared).
func (s *Service) auditUnavailable(ctx context.Context, providerID, providerCode, op, reason string) {
	err := db.WithTx(ctx, s.Pool, db.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		return audit.Record(ctx, tx, audit.Event{Action: "psp.provider.unavailable", ActorType: "system", TargetType: "payment_provider",
			TargetID: providerID, Reason: reason, Metadata: map[string]any{"audit_event": "PROVIDER_UNAVAILABLE", "provider": providerCode,
				"operation": op, "source": "CIRCUIT_BREAKER"}})
	})
	if err != nil {
		s.Logger.Warn("psp: provider unavailable audit failed", "provider", providerCode, "err", err)
	}
}

func (s *Service) insertHealth(ctx context.Context, providerID, op, method, currency, kind, errClass string, latency time.Duration,
	detail map[string]any) error {
	if detail == nil {
		detail = map[string]any{}
	}
	d, _ := json.Marshal(detail)
	var lat *int
	if latency > 0 {
		v := int(latency.Milliseconds())
		lat = &v
	}
	_, err := s.Pool.Exec(ctx, `INSERT INTO app.provider_health_events (id, provider_id, operation, method, currency, event_kind,
		error_class, latency_ms, detail, observed_at) VALUES ($1, $2, $3, nullif($4, ''), nullif($5, ''), $6, nullif($7, ''), $8, $9, $10)`,
		ids.New(), providerID, op, method, currency, kind, errClass, lat, d, s.now())
	return err
}

// Provider health statuses (Stage 9). Derived only from observations: calls, timeouts, circuit events,
// staff-declared outages, verified webhooks and secret resolution. Configuration alone never makes a provider
// HEALTHY: with no recent observation it is UNKNOWN.
const (
	HealthHealthy     = "HEALTHY"
	HealthDegraded    = "DEGRADED"
	HealthUnavailable = "UNAVAILABLE"
	HealthUnknown     = "UNKNOWN"
)

// Health derivation parameters (operational, not regulatory).
const (
	HealthWindow        = 15 * time.Minute
	healthDegradedRatio = 0.2 // ≥ 20 % of calls in the window failed or timed out
)

// HealthSummary is one provider's derived health with the observations behind it.
type HealthSummary struct {
	Status            string     `json:"status"`
	Reason            string     `json:"reason"`
	WindowSeconds     int        `json:"window_seconds"`
	Calls             int        `json:"calls"`
	Errors            int        `json:"errors"`
	Timeouts          int        `json:"timeouts"`
	WebhooksVerified  int        `json:"webhooks_verified"`
	P95LatencyMs      *int       `json:"p95_latency_ms"`
	Circuit           string     `json:"circuit"`
	OutageDeclared    bool       `json:"outage_declared"`
	CredentialsUsable *bool      `json:"credentials_usable"`
	LastObservedAt    *time.Time `json:"last_observed_at"`
}

// ProviderHealth derives a provider's health now. Order: declared outage or open circuit → UNAVAILABLE;
// credentials that do not resolve for a provider this process may use → UNAVAILABLE; no call and no verified
// webhook in the window → UNKNOWN; error/timeout ratio ≥ 20 % or a half-open circuit → DEGRADED; else HEALTHY.
func (s *Service) ProviderHealth(ctx context.Context, p Provider) (HealthSummary, error) {
	h := HealthSummary{WindowSeconds: int(HealthWindow.Seconds())}
	since := s.now().Add(-HealthWindow)
	var last *time.Time
	if err := s.Pool.QueryRow(ctx, `SELECT count(*) FILTER (WHERE event_kind IN ('CALL_OK','CALL_ERROR','TIMEOUT')),
			count(*) FILTER (WHERE event_kind = 'CALL_ERROR'), count(*) FILTER (WHERE event_kind = 'TIMEOUT'),
			(percentile_disc(0.95) WITHIN GROUP (ORDER BY latency_ms) FILTER (WHERE latency_ms IS NOT NULL))::int,
			max(observed_at)
		FROM app.provider_health_events WHERE provider_id = $1 AND observed_at > $2`, p.ID, since).
		Scan(&h.Calls, &h.Errors, &h.Timeouts, &h.P95LatencyMs, &last); err != nil {
		return h, err
	}
	var lastHook *time.Time
	if err := s.Pool.QueryRow(ctx, `SELECT count(*), max(received_at) FROM app.provider_webhook_inbox
		WHERE provider_id = $1 AND received_at > $2`, p.ID, since).Scan(&h.WebhooksVerified, &lastHook); err != nil {
		return h, err
	}
	if lastHook != nil && (last == nil || lastHook.After(*last)) {
		last = lastHook
	}
	h.LastObservedAt = last
	h.Circuit = s.ProviderCircuit(ctx, p.ID)
	open, err := outageOpen(ctx, s.Pool, p.ID)
	if err != nil {
		return h, err
	}
	h.OutageDeclared = open
	usable, err := s.Usable(ctx, p)
	if err != nil {
		return h, err
	}
	if usable {
		ok := false
		if acct, err := s.ActiveAccount(ctx, p); err == nil {
			ok = s.CredentialsUsable(ctx, acct)
		}
		h.CredentialsUsable = &ok
	}
	bad := h.Errors + h.Timeouts
	switch {
	case h.OutageDeclared:
		h.Status, h.Reason = HealthUnavailable, "OUTAGE_DECLARED"
	case h.Circuit == CircuitOpen:
		h.Status, h.Reason = HealthUnavailable, "CIRCUIT_OPEN"
	case h.CredentialsUsable != nil && !*h.CredentialsUsable:
		h.Status, h.Reason = HealthUnavailable, "CREDENTIALS_UNAVAILABLE"
	case h.Calls == 0 && h.WebhooksVerified == 0:
		h.Status, h.Reason = HealthUnknown, "NO_RECENT_OBSERVATIONS"
	case h.Circuit == CircuitHalfOpen:
		h.Status, h.Reason = HealthDegraded, "CIRCUIT_HALF_OPEN"
	case h.Calls > 0 && float64(bad)/float64(h.Calls) >= healthDegradedRatio:
		h.Status, h.Reason = HealthDegraded, "ERROR_RATE"
	default:
		h.Status, h.Reason = HealthHealthy, "RECENT_CALLS_SUCCEEDED"
		if h.Calls == 0 {
			h.Reason = "RECENT_WEBHOOKS_VERIFIED"
		}
	}
	return h, nil
}
