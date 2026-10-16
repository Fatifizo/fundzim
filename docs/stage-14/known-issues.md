# Stage 14 — Known issues

| ID | Issue | Impact | Status |
|---|---|---|---|
| KI-S14-01 | Acceptance of Stages 1–13 not recorded | governance | EXTERNAL_BLOCKER (owner) |
| KI-S14-02 | No error-rate store: `application_errors` is UNKNOWN, so overall health is never HEALTHY | honest but noisy | OPEN (observability stage) |
| KI-S14-03 | Health thresholds (worker 5 min, outbox 5 min) and the 3 s source timeout are engineering defaults | not service commitments | OPEN |
| KI-S14-04 | No internal SLA target is configured; only `SUPPORT_RESOLUTION` affects behaviour (support `due_at`) | targets need an owner decision | OPEN (owner) |
| KI-S14-05 | Approvals, exceptions and alerts are recomputed on each request (all module snapshots, p50 ≈ 270–390 ms on the dev stack); no caching or pagination of the aggregate | fine at TEST volumes | OPEN (Stage 17 scale) |
| KI-S14-06 | The providers snapshot dominates latency (≈ 215 ms): derived health resolves credential references in the secret manager | as above | OPEN |
| KI-S14-07 | Global search matches exact ids/numbers (and the campaign admin query) only | by design (no personal-data search) | ACCEPTED |
| KI-S14-08 | Staff ids are shown shortened; assigning needs a pasted staff id (no picker) | UX | OPEN |
| KI-S14-09 | No saved views, bulk actions or CSV export outside the audit center | not required for TEST operations | DEFERRED |
| KI-S14-10 | Task indicators are in-app only; no email/SMS/push | Stage 15 | DEFERRED (Stage 15) |
| KI-S14-11 | Incident workflow has no paging, status page or external communication | Stage 15 / later | DEFERRED |
| KI-S14-12 | Access reviews have no schedule or reminders (no review frequency is invented) | governance process | OPEN (owner) |
| KI-S14-13 | `TestPublicExperienceEndpoints` (KI-S13-20) could not be reproduced: 3 consecutive runs and the full suite passed; the recorded failure was an approval answering 202 (second approval), which needs a HIGH-tier review and EDUCATION campaigns are STANDARD | flaky history, mechanism not found | OPEN — monitor |
| KI-S14-14 | The mock E2E API (`e2e/mock-api/operations.mjs`) is a simplified stand-in; real-stack coverage is the Go integration suite (`TestOperations*`) | E2E realism | ACCEPTED |
| KI-S14-15 | Stage 13 external blockers remain (screening provider, STR duties, automated-decision law, provider signals, simulator signing) | production readiness | EXTERNAL_BLOCKER |

## Stage 13 items resolved in Stage 14

| ID | Resolution |
|---|---|
| KI-S13-11 | FIXED — rule fault isolation (see [risk-operations.md](risk-operations.md)) |
| KI-S13-18 | FIXED — compose images rebuilt from the Stage 14 tree before the integration suite (all services healthy) |
| KI-S13-20 | INVESTIGATED, not reproduced (KI-S14-13) |
| KI-S13-21 | FIXED — permission-aware risk actions and forms |
