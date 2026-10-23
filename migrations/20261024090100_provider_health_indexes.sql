-- Stage 19 (ADR-051) — provider health derivation indexes. Owner module: psp. Runs as fundzim_migrator. Forward-only in
-- production (ADR-028); Down is for development.
--
-- Found by the Stage 19 load run: every payout eligibility check (at request and again before submission) derives the
-- provider's health (psp.ProviderHealth), and its queries scanned the provider's whole append-only call history —
-- 1.26 M development rows: the window query 93 ms, each circuit lookup 79 ms, each distinct-currency enumeration 200 ms,
-- ~0.6 s per derivation and growing with every provider call. Three indexes serve those queries exactly; the circuit
-- currency enumeration is restricted to circuit events (psp.ProviderCircuit), which gives the same result because a
-- currency without a circuit event is CLOSED. Plain CREATE INDEX (goose runs each migration in a transaction): the
-- table holds no production data; a production table of this size would need CREATE INDEX CONCURRENTLY outside a
-- transaction, a migration procedure that does not exist yet (Stage 20 deployment input, docs/stage-19/known-issues.md).

-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '600s';
-- health window (calls, errors, timeouts, p95 latency over the last psp.HealthWindow)
CREATE INDEX ix_provider_health_events_window ON app.provider_health_events (provider_id, observed_at DESC);
-- latest circuit event per provider, operation and currency; distinct currencies that have a circuit
CREATE INDEX ix_provider_health_events_circuit ON app.provider_health_events
  (provider_id, operation, (coalesce(currency, '')), observed_at DESC, id DESC) WHERE event_kind LIKE 'CIRCUIT_%';
-- latest declared outage or resolution
CREATE INDEX ix_provider_health_events_outage ON app.provider_health_events (provider_id, observed_at DESC, id DESC)
  WHERE event_kind IN ('OUTAGE_DECLARED', 'OUTAGE_RESOLVED');
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS app.ix_provider_health_events_outage;
DROP INDEX IF EXISTS app.ix_provider_health_events_circuit;
DROP INDEX IF EXISTS app.ix_provider_health_events_window;
-- +goose StatementEnd
