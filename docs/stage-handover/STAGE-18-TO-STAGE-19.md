# Stage 18 → Stage 19 handover (Financial Concurrency, Load, Failure and Integrity Validation)

Status of Stage 18: complete, **awaiting acceptance** (`docs/governance/stage-acceptance-register.md`, row 18
NOT_RECORDED). Stage 19 must not start until the owner asks for it. This document prepares it; it implements nothing.

## 1. What Stage 19 inherits

| Area | State | Where |
|---|---|---|
| Financial gates | 11 constants false, 20 CHECKs, kill switches off — with regression tests (unit + database) | `internal/archtest/financial_gates_test.go`, `tests/integration/security_gates_test.go` |
| Audit verification | parallel ranges (8 connections: 1.3 M rows in 13 s), MAC checkpoints, incremental (ms) | ADR-050, `internal/audit/chain.go` |
| Security alerts | persisted, deduplicated, guarded lifecycle; worker scan each minute | `internal/operations/security.go` |
| Backups / restore | encrypted sets, MAC manifests, restore and failure drills, DR exercise script | `scripts/backup`, `scripts/dr` |
| Perf harnesses | ledger (`tests/perf/ledger`), payments/discovery/reconciliation (`tests/perf`), Stage 18 security sample (`TestSecurityPerformanceSample`) | docs/stage-*/testing.md |
| Hardened stack | read-only, no capabilities, segmented networks, digest-pinned | `compose.yaml`, `internal/deploytest` |

## 2. Stage 19 scenarios (inputs, not decisions)

| Topic | What to prove | Starting evidence / gap |
|---|---|---|
| Payment concurrency | concurrent intents, attempts and status writers never break `payments.apply` precedence; one success per intent | Stage 8 concurrency ITs; IN_FLIGHT guards |
| Payout concurrency | concurrent approvals, submissions, pollers and webhooks: one CREATE per budget slot, UNKNOWN never resubmitted, reservation consistent | attempts guard, retry budget (KI-S17-01), reservation machine |
| Ledger posting contention | many posters on the same accounts: balance projections equal recompute (L10), no deadlocks, deferred balance folding | `tests/perf/ledger` (scratch DB only) |
| Reconciliation races | concurrent imports, parsers, runs and checkers | Stage 17 `TestReconciliationConcurrency` |
| Duplicate provider events | duplicate/out-of-order/contradictory webhooks under load | inbox dedupe, contradiction log |
| Unknown financial outcomes | timeouts at every provider call under load; reconciliation hand-off | UNKNOWN horizon; KI-S18-22 (PENDING polled forever) |
| Worker crashes | kill workers mid-transaction and mid-provider-call; River lease recovery; in-flight sweepers | DR exercise (restart 0.3 s); no mid-call kill test yet |
| Database failover | connection loss during postings and submissions; retry safety; pool behaviour | DR: pools reconnect in 0.6 s; no failover topology exists (single node) |
| Transaction isolation | anomalies under REPEATABLE READ/serialisable where used; advisory-lock coverage | per-module locks (fees quotes, scan locks) |
| Reservation consistency | reserved ≤ available, released/consumed exactly once | payout reservation machine |
| High volume | payments, payouts, notifications and reconciliation at 10⁵–10⁶ records | Stage 17: 100 k statement lines 71 s; audit write 553/s at 8 writers (one chain lock) |
| Failure recovery | crash + restart + replay produce no duplicates | outbox exactly-once creation |
| Invariant verification | ledger L1–L15, audit chains, recon totals, fee recomputation after every scenario | `fundzimctl verify audit|ledger|objects` (Stage 18) |
| Load and endurance | hours-long runs; memory, connections, queue depth | Stage 18 security sample (login 18/s at 8 workers; Argon2id bound) |

Known constraints to respect: the single audit chain lock serialises audit writers (~550 events/s on the development
host); Argon2id caps login throughput by design; perf harnesses run only against scratch databases; nothing may weaken a
gate, CHECK, trigger or grant to make a load test pass.

## 3. Production blockers (carried)

production-readiness.md §1 — provider, accounting, legal, security conditions (WebAuthn, MFA reset, break-glass workflow,
penetration test, KMS), infrastructure, recovery objectives, governance. Production financial operations stay BLOCKED.

## 4. Constraints carried forward

All Stage 8–18 rules in CLAUDE.md; Stage 18 adds: keys only from the secret manager outside development; never remove a
previous key while data uses it; audit verification is never weakened to save time; security alerts are resolved by
people; backups never sit next to their key; restore drills before any release.
