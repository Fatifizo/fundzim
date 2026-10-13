package psp

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

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
		_ = s.insertHealth(ctx, providerID, op, method, currency, "CIRCUIT_OPENED", errClass, 0, map[string]any{"reason": "probe failed"})
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
		_ = s.insertHealth(ctx, providerID, op, method, currency, "CIRCUIT_OPENED", errClass, 0, map[string]any{"failures": bad})
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
