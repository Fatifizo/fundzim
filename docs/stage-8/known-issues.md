# Stage 8 — Known issues

| ID | Issue | Impact | Status |
|---|---|---|---|
| KI-S8-01 | Formal acceptance of Stages 1–8 is not recorded ([stage-acceptance-register.md](../governance/stage-acceptance-register.md) is a proposed template) | Process gap | OPEN — owner |
| KI-S8-02 | **Production payment execution is BLOCKED**: no provider selected (ADR), legal items open (LR-001/003/004/005/044/075), no ledger, no screening | No real donations | BLOCKED (by design) |
| KI-S8-03 | No real provider adapter or capability row; every rail in the [capability register](provider-capability-register.md) is UNKNOWN | Stage 9 | OPEN |
| KI-S8-04 | Provider secrets resolve from environment variables (`psp.EnvResolver`); no KMS / secret manager, no rotation tooling | Not acceptable for real credentials | OPEN — before Stage 9 goes live |
| KI-S8-05 | No donor checkout UI, receipts or donation history page; the public button stays disabled | Donors cannot pay (intended) | OPEN — later stage |
| KI-S8-06 | Guest donations are not supported (donor account required) | — | OPEN — LR-026, LR-035 |
| KI-S8-07 | No platform fee (fees module and LR-028 open); intents carry no fee | — | DEFERRED |
| KI-S8-08 | No ledger postings: `payments.payment_succeeded` carries the posting key for Stage 10; sandbox successes will never be posted | Stage 10 must ignore simulated events | DEFERRED (ADR-039 §4) |
| KI-S8-09 | Refund execution, disputes, chargebacks and their tables are deferred; refund requests stop at APPROVED | — | DEFERRED |
| KI-S8-10 | UNKNOWN resolution horizon is a 72 h default (no provider documents one: PCR-010) | Possibly long unknowns | OPEN — provider confirmation |
| KI-S8-11 | Circuit breaker is simple (5 consecutive failures / 60 s, 30 s cool-down) and applies to the sandbox only when enabled per server | Tuning needed with real traffic | OPEN |
| KI-S8-12 | No staff "declare outage" action (health events `OUTAGE_DECLARED` exist in the schema only) | Operations | OPEN |
| KI-S8-13 | Sandbox delays are short (1–12 s; late success 4 s instead of the Stage 2 table's 2 min) | Simulator realism | ACCEPTED |
| KI-S8-14 | The staff sandbox panel needs both `payment.view` and `payment.sandbox.control` (no role holds both by default) | Development convenience | ACCEPTED |
| KI-S8-15 | No retention or deletion for payment records, inbox rows, rejections or sandbox tables (LR-012) | Data kept indefinitely | OPEN — LEGAL_REVIEW_REQUIRED |
| KI-S8-16 | Donors are not screened (KI-S5-04, `SCREENING_PROVIDER_NOT_SELECTED`) | Production blocker | OPEN |
| KI-S8-17 | Payer instrument fingerprints use the app blind-index key; no per-provider key separation | Minor | OPEN |
| KI-S8-18 | Webhook dispatch latency depends on the outbox relay (1 s) and delivery backoff; no separate high-priority queue | Throughput under bursts: see [performance.md](performance.md) | OPEN |
| KI-S8-19 | Rejections are recorded for every refused delivery (bounded by per-IP limits only); no aggregation | Storage under attack | OPEN |
| KI-S8-20 | Stage 2 design paths PAY-02..05 removed from OpenAPI (superseded); other design payment paths remain PLANNED | Contract drift to resolve with receipts/donations | OPEN |
| KI-S8-21 | Real-stack Playwright suite has no payment flow (no checkout exists); the staff payment UI is covered by the mock suite and the API by integration tests | — | ACCEPTED |

| KI-S8-22 | A provider delivering webhooks from one IP address is limited by `global.ip` (40 per 2 s) before `payments.webhook.ip` (600/min): 260 of 300 rapid deliveries from one address got 429 in [performance.md](performance.md) §3 | Delivery retries and delays under bursts | OPEN — set limits per the selected provider's IP ranges and delivery profile (Stage 9) |
Carried from Stage 7 unchanged: KI-S7-02, 04, 05, 06, 07, 08, 10, 12, 13, 14, 15, 17, 19, 20, 21, 22, 23, 24
([stage-7/known-issues.md](../stage-7/known-issues.md)).
