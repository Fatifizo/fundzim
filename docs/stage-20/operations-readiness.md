# Stage 20 — Operations and incident readiness

**Area result: BLOCKED.** The application has health checks, persisted alerts, runbooks and an operations portal, but
no alert reaches a person: there is no delivery channel, no on-call rota and no named operator.

## 1. Readiness items

| Item | Evidence | Status |
|---|---|---|
| Health and readiness checks | `/api/v1/ready`, worker `/readyz`; categories only (Stage 18) | PASS |
| Monitoring | Prometheus metrics exposed internally; no monitoring system deployed | BLOCKED (KI-S18-01) |
| **Alert delivery** | security/operations alerts are rows in `app.ops_security_alerts` raised by the worker scan; **no paging, e-mail or chat destination is configured** (KI-S18-15) | BLOCKED — internal records are not paging |
| On-call responsibility | none named | BLOCKED |
| Severity definitions, financial and security incident handling | Stage 18 incident-response.md (framework, 10 runbooks) | PASS WITH CONDITIONS (no responders) |
| Provider outage handling | circuit/outage records, routing refuses unhealthy providers (Stage 9) | PASS WITH CONDITIONS (simulator only) |
| Payment ambiguity | UNKNOWN, poll budget, reconciliation queue | PASS (engineering) |
| Reconciliation backlog | `payments.reconciliation.open`, payout/fee/ledger exceptions on the portal | PASS WITH CONDITIONS (no SLA; none invented) |
| Payout holds | risk hold registry, payout exceptions | PASS (engineering) |
| Database recovery and backup restoration | restore drill repeated in Stage 20 (infrastructure-dr-readiness.md) | PASS WITH CONDITIONS |
| Staff access, break-glass, operational audit | roles, maker-checker, audit centre; break-glass without workflow (F-S18-04) | PASS WITH CONDITIONS |
| Support procedures, customer communications, escalation paths | support cases (Stage 14); no staffed support, no approved communication templates for incidents | NOT_PROVIDED |

Owner decisions required: named operators and on-call, alert destinations, incident severity response expectations
(no SLA is invented), support staffing.

## 2. Notification delivery (brief §8) — measured on the disposable stack

Configuration: the notifications queue gets `max(1, WORKER_CONCURRENCY/2)` workers (`internal/platform/jobs/jobs.go`);
compose sets `WORKER_CONCURRENCY=4`, so **2 workers**. Delivery uses the simulators and development senders.

Burst (2026-10-25, `TestFinancialLoad` c = 16 for 5 min on `fundzim-s20`; samples every 15 s:
`evidence/stage-20/notification-queue-burst.csv`):

| Phase | Created | Completed | Backlog | Oldest ready job |
|---|---|---|---|---|
| warm-up (08:46–08:48) | 0.7–1.8 /s | same | 0 | 0 s |
| load (08:48:17–08:53:23) | ~23–31 /s | **~19.7 /s (flat ceiling)** | grew linearly to **2 082** | up to 82 s |
| after load (08:53:23–08:55:26) | ~0.7 /s | ~19.6 /s | **2 082 → 0 in ~2 min** | max 104 s, then 0 |

No retryable or discarded job appeared; no duplicate success notice was produced (load report, correctness line).

Findings: the backlog is caused by **worker configuration** (a fixed ~20 jobs/s ceiling with 2 workers), not by
failures or duplicates; it recovers fully once the input falls below the ceiling. Stage 19's 4–9-minute delays came
from 30 minutes above the ceiling (backlog ~15 000). Notifications never decide financial truth: the donor's success
notice is emitted only from the committed `payments.payment_succeeded` outbox event, and a failed or late notification
leaves the payment state untouched (Stage 15 rule, `TestNotificationsFinancialSafety` passed in the isolated run).

No notification SLA exists and none is invented. Worker sizing can only be recommended against an approved target:
at the measured ~10 jobs/s per worker, a target throughput T needs about T/10 notification workers (plus headroom),
to be confirmed by re-running this burst. The queue's history is pruned after ~24 h by River (F-S20-05), so queue depth
and age must be retained as metrics once monitoring exists.
