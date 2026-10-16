# Stage 14 — Risk operations

Sources: `risk.OpsSnapshot` and `risk.OpsSearch` (`internal/risk/ops.go`); pages stay under `/admin/risk` (Stage 13).

- Metrics: open, P1/P2, overdue and unassigned-P1 cases; active holds; active rule versions (`NOT_CONFIGURED` when none,
  `DEGRADED` when an active version is failing); screenings awaiting review (TEST); production screening `BLOCKED`.
- Approvals: rule versions, hold activations, hold releases, case decisions, screening reviews (makers listed).
- Exceptions: TEST screenings in ERROR (never treated as cleared), rule versions that fail to evaluate.
- Alerts: `RISK_EVALUATION_FAILURE`, `RISK_P1_UNASSIGNED`, `SCREENING_UNAVAILABLE`.

## Fault isolation (KI-S13-11, fixed)

Before: an ACTIVE rule version whose definition could not run made `RecordSignal` fail, so the signal was lost.
Now (`internal/risk/evaluate.go`, migration `20261017090000_risk_fault_isolation`):

1. Each rule is compiled and run in isolation (`runnable()` with panic recovery).
2. The signal and the evaluation of the remaining rules are stored; the evaluation is `degraded` with
   `failed_rule_version_ids` (CHECK: degraded ⇔ at least one failed id); the subject profile is marked degraded.
3. `risk.risk_rule_failures` records the failure (UNPARSEABLE_DEFINITION, ENGINE_VERSION_MISMATCH or EVALUATION_PANIC),
   immutable, one row per version and signal; a `RULE_EVALUATION_FAILED` monitoring event is raised once per version
   per day; risk reconciliation reports it.
4. Payout eligibility treats a degraded latest evaluation as `RISK_EVALUATION_INCOMPLETE` (EC-16 REVIEW): money fails
   closed.

`TestRiskRuleFaultIsolation` covers all of it, including recovery after the broken version is retired.

## Permission-aware actions (KI-S13-21, fixed)

`apps/web/src/lib/risk/permissions.ts` maps each risk action and form to the permission its route requires; the risk
pages show only those the caller holds (`mayRisk`, `can`). The API still refuses them (the E2E suite sends one directly
and gets 403).
