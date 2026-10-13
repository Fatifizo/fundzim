# Stage 8 — Performance

Measured on 2026-10-13 with the harness `tests/perf/payments/payments_perf_test.go` (build tags `integration,perf`,
compiled into `tests/integration` through `-overlay tests/perf/payments/overlay.json`, gated by
`FUNDZIM_PERF_PAYMENTS=1`; it never runs in CI). Every number below comes from those runs.

## 1. Method and environment

- **Stack:** the local compose stack (PostgreSQL 17.11, Valkey, Garage, Mailpit; API, worker, web containers built from
  this branch). The **API under test runs in-process** (`httptest`, as in the integration tests) with the sandbox
  simulator enabled; the database, Valkey rate limiter and outbox are the real shared services.
- **Machine:** 16 CPUs (GOMAXPROCS 16), 30 GiB RAM, single host; Go 1.27.2; `DATABASE_MAX_CONNS=20`.
- **Load:** closed-loop workers (`c` = concurrent clients, `n` = requests); client-observed latency; every request
  from its own client IP (trusted loopback proxy + `X-Forwarded-For`), except the explicit "ONE client IP" rows, so the
  per-IP limits are measured separately. Donor and campaign limits (`payments.intent.user` 20/h,
  `payments.intent.campaign` 600/h) were respected by spreading load over 30 donors and 2 campaigns. Rate limiting
  stayed ON. Any non-expected status (incl. 429) counts as an error.
- **Provider:** the in-process sandbox **simulator**: provider latency is ~0. With a real PSP the attempt path is
  dominated by the provider call (its deadline is 20 s); these figures measure FundZim's own overhead only.
- **Dataset:** shared development database (sizes before/after are printed per section).

Commands (each under `flock /tmp/claude-1000/fundzim-stack.lock`, with `.env` loaded):

```bash
FUNDZIM_PERF_PAYMENTS=1 FUNDZIM_PERF_OUT=/tmp/payperf go test -tags integration,perf \
  -overlay tests/perf/payments/overlay.json -count=1 -timeout 20m -run '^TestPaymentsPerfDonorAPI$' -v ./tests/integration/
# likewise TestPaymentsPerfWebhooks, TestPaymentsPerfStaffReads
```

## 2. Donor API: intents, attempts, status, duplicate keys

dataset before: app.donation_intents=977 app.payment_intents=938 app.provider_webhook_inbox=366 app.payment_events=7257 app.payment_attempts=4740 app.outbox_events=18918

fixtures: 30 donors (≤19 intents each), 2 campaigns (≤285 intents each), every request from its own client IP

#### 1. POST /donation-intents

| scenario | c | n | p50 ms | p95 ms | p99 ms | max ms | req/s | errors | 429s | statuses |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---|
| create intent | 1 | 50 | 19.4 | 28.3 | 31.6 | 31.6 | 49 | 0 | 0 | 201×50 |
| create intent | 8 | 200 | 16.9 | 37.7 | 76.2 | 99.1 | 392 | 0 | 0 | 201×200 |
| create intent (burst) | 32 | 320 | 74.1 | 141.5 | 187.6 | 207.1 | 366 | 0 | 0 | 201×320 |

#### 2. POST /donation-intents/{id}/attempts (sandbox ECOCASH)

| scenario | c | n | p50 ms | p95 ms | p99 ms | max ms | req/s | errors | 429s | statuses |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---|
| attempt, amount 1000 (suffix 00) | 1 | 30 | 33.5 | 47.9 | 66.3 | 66.3 | 27 | 0 | 0 | 201×30 | (PENDING: 30) |
| attempt, amount 1000 (suffix 00) | 8 | 100 | 32.8 | 65.1 | 76.2 | 78.8 | 221 | 0 | 0 | 201×100 | (PENDING: 100) |
| attempt, amount 1000 (suffix 00) | 32 | 150 | 104.1 | 173.7 | 184.7 | 188.0 | 268 | 0 | 0 | 201×150 | (PENDING: 150) |
| attempt, amount 1024 (suffix 24) | 1 | 30 | 36.2 | 48.7 | 49.1 | 49.1 | 27 | 0 | 0 | 201×30 | (PENDING: 30) |
| attempt, amount 1024 (suffix 24) | 8 | 100 | 30.6 | 43.0 | 50.8 | 51.4 | 241 | 0 | 0 | 201×100 | (PENDING: 100) |
| attempt, amount 1024 (suffix 24) | 32 | 150 | 100.1 | 119.8 | 131.8 | 135.3 | 299 | 0 | 0 | 201×150 | (PENDING: 150) |

#### 5. GET /donation-intents/{id}/status

| scenario | c | n | p50 ms | p95 ms | p99 ms | max ms | req/s | errors | 429s | statuses |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---|
| status (own intent) | 1 | 300 | 2.9 | 4.7 | 6.2 | 6.7 | 330 | 0 | 0 | 200×300 |
| status (own intent) | 8 | 1000 | 2.5 | 4.6 | 6.0 | 7.2 | 2914 | 0 | 0 | 200×1000 |
| status (own intent) | 32 | 2000 | 7.7 | 12.4 | 14.5 | 18.1 | 3898 | 0 | 0 | 200×2000 |
| status, ONE donor from ONE client IP | 8 | 300 | 0.6 | 2.4 | 2.7 | 3.0 | 8896 | 260 | 260 | 200×40 429×260 |

#### 7. 100 concurrent POST /donation-intents with ONE Idempotency-Key (one donor)

| scenario | c | n | p50 ms | p95 ms | p99 ms | max ms | req/s | errors | 429s | statuses |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---|
| 100 duplicates, one client IP | 100 | 100 | 6.6 | 11.6 | 12.5 | 19.4 | 5145 | 99 | 60 | 201×1 409×39 429×60 |
| ↳ 100 duplicates, one client IP: intents stored for the key = 1; distinct ids in 201 responses = 1 |
| 100 duplicates, distinct client IPs | 100 | 100 | 20.4 | 25.2 | 26.0 | 34.2 | 2923 | 99 | 0 | 201×1 409×99 |
| ↳ 100 duplicates, distinct client IPs: intents stored for the key = 1; distinct ids in 201 responses = 1 |

dataset after: app.donation_intents=1551 app.payment_intents=1498 app.provider_webhook_inbox=485 app.payment_events=9217 app.payment_attempts=5400 app.outbox_events=20403


## 3. Webhooks: processing throughput and ingestion

Processing ("multiple workers"): each event is ingested through the real endpoint, then drained through
`psp.Service.Dispatch` (claim → `payments.ApplyWebhook` → apply P9 PENDING→SUCCEEDED → processed) by 1 or 4
concurrent dispatchers.

dataset before: app.donation_intents=1551 app.payment_intents=1498 app.provider_webhook_inbox=521 app.payment_events=9303 app.payment_attempts=5400 app.outbox_events=20525

fixtures: 300 PENDING sandbox payments (ECOCASH, amount 1024 = pending_forever)

#### 4. Webhook processing: drain N stored events through psp.Service.Dispatch (claim → apply → processed)

| dispatchers | N drained | excluded (relayed by the compose worker first) | wall ms | events/s | per-event p50 ms | p95 ms | p99 ms | max ms | dispatch errors | inbox PROCESSED | payments SUCCEEDED |
|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| 1 | 150 | 0 | 2016.8 | 74 | 12.7 | 19.8 | 24.0 | 25.2 | 0 | 150 | 150 |
| 4 | 150 | 0 | 952.9 | 157 | 23.7 | 37.4 | 80.2 | 86.4 | 0 | 150 | 150 |

#### 3. Webhook ingestion POST /webhooks/payments/sandbox (valid signature, unique event ids)

| scenario | c | n | p50 ms | p95 ms | p99 ms | max ms | req/s | errors | 429s | statuses |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---|
| ingest | 1 | 200 | 5.0 | 9.0 | 11.4 | 22.0 | 176 | 0 | 0 | 200×200 |
| ingest | 8 | 800 | 5.8 | 11.9 | 16.7 | 22.4 | 1206 | 0 | 0 | 200×800 |
| ingest | 32 | 1600 | 11.0 | 21.2 | 51.3 | 70.8 | 2481 | 0 | 0 | 200×1600 |
| ingest, burst (10× c=32) | 320 | 3200 | 112.8 | 155.1 | 159.4 | 175.3 | 2635 | 0 | 0 | 200×3200 |
| ingest, ONE client IP (a PSP sending from one address) | 8 | 300 | 0.5 | 5.2 | 5.8 | 5.8 | 7097 | 260 | 260 | 200×40 429×260 |

dataset after: app.donation_intents=1852 app.payment_intents=1798 app.provider_webhook_inbox=6661 app.payment_events=11072 app.payment_attempts=5775 app.outbox_events=27958


## 4. Staff operations queue reads

dataset at measurement: app.donation_intents=1852 app.payment_intents=1798 app.provider_webhook_inbox=6661 app.payment_events=17930 app.payment_attempts=6803 app.outbox_events=28234

payment_intents UNKNOWN=0 needs_reconciliation=11; inbox by status: PROCESSED=6549 RECEIVED=109 UNRECOGNISED=3

#### 6. Staff operations queue reads (FINANCE officer; first page, default limit 50)

| scenario | c | n | p50 ms | p95 ms | p99 ms | max ms | req/s | errors | 429s | statuses |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---|
- `/admin/payments/attempts`: 50 items on the first page, 30495 bytes
- `/admin/payments/attempts?status=PENDING`: 50 items on the first page, 30404 bytes
- `/admin/payments/unknown`: 0 items on the first page, 102 bytes
- `/admin/payments/webhooks`: 50 items on the first page, 30800 bytes
- `/admin/payments/webhooks?status=PROCESSED`: 50 items on the first page, 30800 bytes

| scenario | c | n | p50 ms | p95 ms | p99 ms | max ms | req/s | errors | 429s | statuses |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---|
| GET /admin/payments/attempts | 1 | 200 | 3.9 | 6.2 | 6.8 | 7.0 | 242 | 0 | 0 | 200×200 |
| GET /admin/payments/attempts?status=PENDING | 1 | 200 | 4.4 | 6.8 | 7.9 | 8.0 | 214 | 0 | 0 | 200×200 |
| GET /admin/payments/unknown | 1 | 200 | 3.1 | 4.9 | 6.0 | 6.0 | 307 | 0 | 0 | 200×200 |
| GET /admin/payments/webhooks | 1 | 200 | 4.3 | 7.1 | 8.3 | 8.7 | 217 | 0 | 0 | 200×200 |
| GET /admin/payments/webhooks?status=PROCESSED | 1 | 200 | 4.5 | 7.1 | 8.2 | 9.1 | 212 | 0 | 0 | 200×200 |
| GET /admin/payments/attempts | 8 | 400 | 4.5 | 6.9 | 40.6 | 49.5 | 1529 | 0 | 0 | 200×400 |
| GET /admin/payments/attempts?status=PENDING | 8 | 400 | 4.2 | 6.6 | 7.6 | 11.8 | 1784 | 0 | 0 | 200×400 |
| GET /admin/payments/unknown | 8 | 400 | 2.3 | 3.4 | 4.2 | 4.5 | 3280 | 0 | 0 | 200×400 |
| GET /admin/payments/webhooks | 8 | 400 | 4.0 | 6.5 | 8.7 | 10.4 | 1831 | 0 | 0 | 200×400 |
| GET /admin/payments/webhooks?status=PROCESSED | 8 | 400 | 3.7 | 6.0 | 7.4 | 8.8 | 1997 | 0 | 0 | 200×400 |


**Optimisation made during measurement.** The first run measured `GET /admin/payments/attempts` at 19.9 ms p50 / 27.6 ms p95 (c=1): every row looked up its provider. Response rendering now uses a 30 s display-only provider cache
(`psp.Service.ProviderForDisplay`; routing and webhook checks still read the registry directly), giving the table
above (p50 ≈ 4 ms).

## 5. Observations

- FundZim's own overhead per payment attempt is ~30–35 ms p50 sequentially (two transactions around the provider call,
  provider-call row, audit, events); ~100 ms p50 at 32 concurrent attempts on one machine.
- Webhook ingestion acknowledges in ~5 ms p50 (verify + one transaction with inbox + outbox row) and sustained
  ~2 500 requests/s at c=32 and a 10× burst with no errors; events are verified and stored, then
  processed asynchronously at ~74 events/s per dispatcher (157/s with 4).
- Duplicate protection held under load: 100 concurrent identical requests stored one intent and returned one id
  (the others got 409, or 429 when all came from one client IP).
- **Per-IP limits matter for providers:** a PSP delivering from one address is limited by `global.ip`
  (40 per 2 s) before `payments.webhook.ip` (600/min): 260 of 300 rapid deliveries got 429. Providers retry on
  non-2xx, but the limits must be revisited with the selected provider's delivery profile and IP ranges (known issue).

## 6. Limitations

Single machine; in-process API (no proxy, TLS or network latency); simulator provider; shared development database
(other test data present); no k6 or long soak test; no field data.
