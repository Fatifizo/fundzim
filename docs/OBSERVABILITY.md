# FundZim — Observability Standards

> **Status:** Stage 0 standard. Implementation begins in Stage 3 (logging, request IDs, health checks,
> metrics skeleton) and is extended by every later stage. Financial alerting is completed in Stages 10, 11
> and 17.

Related: [ARCHITECTURE.md](ARCHITECTURE.md) · [AUDIT.md](AUDIT.md) · [SECURITY.md](SECURITY.md) ·
[PAYMENTS.md](PAYMENTS.md) · [LEDGER.md](LEDGER.md) · [DATA-CLASSIFICATION.md](DATA-CLASSIFICATION.md)

---

## 1. Goals

1. Any financial operation can be traced end-to-end — browser → Next.js → API → DB → job → provider →
   webhook → ledger → payout — using identifiers, **without exposing secrets or sensitive data**.
2. Operators learn about financial integrity problems (ledger imbalance, reconciliation mismatch, stuck
   payments, webhook backlog) before users or auditors do.
3. Telemetry itself never becomes a data-leak channel: logs, traces and error reports are classified data
   (C2 CONFIDENTIAL at minimum) with access control and retention.

### Logs vs audit events vs metrics

| Signal | Purpose | Store | Mutability | Retention |
|---|---|---|---|---|
| Application logs | Debugging, operations | Log pipeline (vendor TBD) | Rotated/expired | Short, operational — an ops decision ([PRIVACY.md](PRIVACY.md) §7) |
| Audit events | Accountability: who did what, why | PostgreSQL `audit_events`, append-only ([AUDIT.md](AUDIT.md)) | Immutable | Legal retention (LR-012) |
| Metrics | Rates, latencies, health, financial counters | Prometheus-compatible TSDB | Aggregated | Operational |
| Traces | Request causality, latency breakdown | OpenTelemetry backend (TBD) | Expired | Short |

Logs are **not** an audit trail. An action that must be accountable is written as an audit event in the same
DB transaction as the change, not inferred from logs later.

---

## 2. Identifiers

| ID | Origin | Propagation | Purpose |
|---|---|---|---|
| `request_id` | Accept inbound `X-Request-ID` only from trusted proxies (validated format, ≤128 chars, `[A-Za-z0-9._-]`); otherwise generate (UUIDv7). | Returned in `X-Request-ID` response header and in every API envelope `meta.request_id`; on every log line and audit event. | Support: a user quotes the ID from an error screen. |
| `correlation_id` | Created at the start of a business flow (e.g. a donation attempt); persisted on the payment/payout record. | Carried into outbox events, jobs, provider calls (where the provider allows a metadata field), webhook processing, ledger transactions. | Links all asynchronous work for one business operation across hours/days. |
| `trace_id` / `span_id` | OpenTelemetry (W3C `traceparent`). | HTTP, DB, job queue, outbound HTTP. | Latency and causality within a request/job. |
| `payment_id`, `payout_id`, `refund_id` | FundZim UUIDv7. | Logs, metrics exemplars, audit, ledger source refs. | Domain identity. |
| `provider` + `provider_reference` | Our reference sent to the provider (we use the payment/payout id). | Logs, ledger transaction source refs, reconciliation. | Matching provider records. |
| `provider_transaction_id` | Provider-issued. | Logs, ledger source refs, reconciliation, `UNIQUE(provider, provider_transaction_id)`. | Matching provider statements. |
| `provider_event_id` | Provider webhook event id. | Webhook inbox, logs. | Dedupe and investigation. |
| `ledger_transaction_id` | Ledger. | Linked from payments/payouts; audit. | Financial traceability. |

Provider references are not secrets, but provider **credentials, signatures, and raw webhook secrets** are
C4 SECRET and never logged.

---

## 3. Structured logging

- Go `log/slog`, JSON in all deployed environments (`LOG_FORMAT=json`), text locally.
- Timestamps RFC 3339 UTC with `Z`.
- Standard fields:

| Field | Required | Notes |
|---|---|---|
| `ts`, `level`, `msg` | always | `msg` is a stable, low-cardinality event description (`"payment state changed"`), not interpolated data. |
| `service` | always | `fundzim-api`, `fundzim-worker`, `fundzim-web`. |
| `env`, `version` | always | Build version/commit. |
| `module` | always | `payments`, `ledger`, … |
| `request_id`, `correlation_id`, `trace_id` | when available | |
| `user_id` | when authenticated | Internal UUID only — never email, phone or name. |
| `actor_type` | when authenticated | `user`, `staff`, `system`, `provider`. |
| `payment_id`, `payout_id`, `campaign_id`, `provider`, `provider_reference`, `provider_transaction_id` | when relevant | |
| `amount_minor`, `currency` | when relevant | Allowed: not sensitive on their own; always together. |
| `error`, `error_code` | on error | Error chain text must pass redaction. |
| `duration_ms`, `http.method`, `http.route` (template, not raw path), `http.status` | access logs | |

### 3.1 Redaction (allow-list approach)

- Structured fields are **allow-listed** per log call site; arbitrary structs/maps/request bodies are never
  logged wholesale. A `slog.Handler` wrapper additionally redacts known-sensitive keys (`password`, `otp`,
  `token`, `secret`, `authorization`, `cookie`, `pan`, `cvv`, `national_id`, `passport`, `account_number`,
  `msisdn`, `phone`, `email`, …) as defence in depth.
- **Never logged:** passwords, OTPs, session tokens, CSRF tokens, API keys, private keys, webhook signing
  secrets, `Authorization`/`Cookie` headers, card PAN/CVV/expiry, identity document numbers or images,
  full payout account numbers, raw KYC vendor responses, full raw webhook bodies.
- Phone numbers and payout accounts, when operationally needed, appear masked (`+263 77* *** 123`,
  `****6789`).
- Raw request/response bodies are never logged at any level in deployed environments. Debug-level
  body logging is disallowed in code (lint rule), not just disabled by config.
- Tests assert redaction ([TESTING.md](TESTING.md) §3.6).

### 3.2 Levels
- `error`: an operation failed and needs attention (pages only via alert rules, not per log line).
- `warn`: degraded or suspicious but handled (e.g. webhook signature failure, Redis unavailable).
- `info`: state changes of business significance (payment state changed, payout approved), lifecycle.
- `debug`: local development only; off in production.

---

## 4. Metrics

Prometheus-compatible, exposed on an internal-only listener (`METRICS_LISTEN_ADDR`), never on the public
port. Labels must be low-cardinality: never user IDs, campaign IDs, emails, phone numbers or amounts as labels.

| Category | Examples |
|---|---|
| HTTP (RED) | `http_requests_total{route,method,status}`, `http_request_duration_seconds{route}` |
| DB | pool usage, query duration by repository method, deadlock/serialization-failure counts |
| Jobs/outbox | queue depth by kind, job latency, retries, failures, oldest pending age, dead-letter count |
| Payments | `payments_created_total{provider,method,currency}`, `payments_state_transitions_total{from,to,provider}`, `payments_pending_age_seconds` (histogram), provider call latency/errors/timeouts |
| Webhooks | received/verified/rejected (`reason`), duplicates deduped, processing lag, DLQ size |
| Ledger | transactions posted, posting failures, **imbalance detections (must be 0)**, projection-vs-recompute mismatches |
| Payouts | requested/approved/paid/failed by provider and currency, approval queue age |
| Reconciliation | runs, matched/unmatched counts, mismatch amount by currency (aggregated) |
| Security | login failures, OTP sends/failures per country prefix (SMS pumping), rate-limit hits, authz denials, MFA failures, break-glass activations |
| Uploads | scanned/blocked/quarantine age |

Financial **amount** metrics are aggregated per currency only — never summed across currencies.

---

## 5. Tracing

- OpenTelemetry SDK in Go (HTTP server/client, `pgx`, job queue instrumentation) and in Next.js server code.
- W3C Trace Context is propagated from the Next.js server to the API and onward. Inbound `traceparent`
  headers from browsers are not trusted for sampling decisions.
- Span attributes follow the same allow-list as logs; no SQL parameter values, no bodies.
- Sampling: always sample errors and financial flows (payments, webhooks, ledger, payouts); probabilistic
  for public page views.
- Exporter disabled when `OTEL_EXPORTER_OTLP_ENDPOINT` is empty (local default).

---

## 6. Health and readiness

| Endpoint | Semantics | Checks | Exposure |
|---|---|---|---|
| `GET /healthz` | Liveness: process is running and not deadlocked. | No external dependencies. | Internal/load balancer. |
| `GET /readyz` | Ready to serve traffic. | PostgreSQL reachable, migrations at expected version, required config valid. Redis **not** required (non-authoritative) — reported as degraded. | Internal/load balancer. |

- Neither endpoint reveals versions, hostnames, dependency errors or config to the public internet; detailed
  status is available only on the internal listener.
- Worker processes expose equivalent health on their internal port, including job-queue connectivity.

---

## 7. Error reporting

- Sentry-compatible SDK (vendor TBD; hosting location subject to LR-011) for Go and Next.js.
- `beforeSend` scrubbing: remove request bodies, cookies, headers except an allow-list, query strings with
  tokens, user PII (only internal `user_id`), and any field matching the redaction list.
- Source maps uploaded privately, never served publicly.
- `ERROR_REPORTING_DSN` empty = disabled (local default).

---

## 8. Alerting

Severity uses the same scale as the incident process ([SECURITY.md](SECURITY.md) §22): **SEV1** = page immediately (24/7 once in pilot), **SEV2** = page (business hours before pilot; 24/7 for financial event types once in pilot), **SEV3** = ticket.

| Alert | Condition (initial; tune with data) | Severity |
|---|---|---|
| Ledger imbalance | Any imbalance detection or invariant-check failure | **SEV1** — stop-the-line; follow CLAUDE.md rule 11 |
| Projection mismatch | Materialised balance ≠ recompute | SEV1 |
| Reconciliation mismatch | Any unmatched item older than threshold, or amount/currency mismatch | SEV2 (SEV1 above amount threshold) |
| Webhook DLQ growth | DLQ size > 0 for financial event types | SEV2 |
| Webhook signature failures spike | Sudden rise per provider | SEV2 (possible attack or key rotation issue) |
| Pending payments ageing | Payments `PENDING` beyond provider-specific SLA | SEV2 |
| Payout failure spike | Failure rate per provider above baseline | SEV2 |
| Outbox/job backlog | Oldest pending job age above threshold | SEV2 |
| Provider unavailability | Error/timeout rate per provider | SEV2 |
| Auth abuse | Login/OTP failure spikes, SMS volume anomaly per prefix | SEV2 |
| Break-glass / KYC bulk access | Any break-glass activation; unusual KYC document access volume per staff member | SEV2 (notify compliance) |
| Availability | `/readyz` failing, 5xx rate, p95 latency breach | SEV1/SEV2 by impact |
| Backups | Backup job failed or restore test overdue | SEV2 |

Every alert links to a runbook (written as the feature ships; runbooks are part of the Definition of Done for
financial features).

---

## 9. Payment correlation walkthrough (target behaviour)

1. Donor clicks Donate → Next.js server action/API call with `Idempotency-Key` → API assigns `request_id`,
   creates payment `pay_…` with new `correlation_id`, state `CREATED`, audit event; this DB transaction
   commits before any provider call.
2. Still within the same HTTP request (after that commit, never inside a DB transaction), the API calls
   provider `CreatePayment` with `provider_reference = payment_id` (+ correlation in metadata if supported);
   logs `provider`, `provider_reference`, latency, outcome; state → `PENDING`/`REQUIRES_ACTION`; the response
   carries `next_action` ([PAYMENTS.md](PAYMENTS.md) §7).
3. Webhook arrives → `webhook_inbox` row with `provider_event_id`; verification result logged (not the
   signature); processing job loads payment by `provider_reference`/`provider_transaction_id`, inherits
   `correlation_id`.
4. State → `SUCCEEDED`; ledger transaction posted with source refs (`payment_id`, `provider`,
   `provider_transaction_id`); audit event; notification via outbox.
5. Reconciliation later matches the provider statement line to `provider_transaction_id` and records match.

Querying by any one of `payment_id`, `correlation_id`, `provider_transaction_id` returns the whole chain.

---

## 10. Access and retention of telemetry

- Telemetry backends require SSO + MFA; access limited to engineering/operations roles; access is logged.
- Logs are C2 CONFIDENTIAL. Application-log retention is an operational decision (short window; default
  proposed in Stage 3, see [PRIVACY.md](PRIVACY.md) §7). Logs must never be the only copy of anything with a
  legal retention requirement; such records live in the database (audit, ledger) under LR-012.
- Telemetry vendors' hosting location is subject to LR-011.
