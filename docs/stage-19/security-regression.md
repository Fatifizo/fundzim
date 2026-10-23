# Stage 19 — Security, tenancy and governance regression

Financial correctness work must not weaken authorization (brief §16). Nothing in Stage 19 changes a route, a policy,
a permission, a role, a grant, a gate constant or a CHECK on financial state (the only CHECK changed adds one
reconciliation reason). Evidence:

| Area | Evidence (this session) |
|---|---|
| Cross-tenant payout access under concurrency | `TestFinancialPayoutCrossTenantUnderConcurrency`: 50 concurrent requests by another verified, stepped-up user (read, timeline, cancel, campaign list, new request with the owner's destination): all refused, nothing changed |
| Maker-checker under concurrency | `TestFinancialPayoutConcurrentApprovals`: 12 concurrent approvals by two FINANCE checkers → 2 approvals, 2 distinct approvers, 1 transition; the same checker never counts twice (Go + DB) |
| Separation of duties, linked identities | `TestIdentityRBACStaffMakerChecker`, `TestPayout*` approval tests, `TestLedgerReversalsMakerCheckerAndNoDoubleReversal`, `TestFeePolicyMakerCheckerLifecycleAndImmutability`, `TestReconciliationRuleSetsAreMakerChecker` (full-suite rerun) |
| Retries do not inherit authorization | payout and payment mutations re-check the actor on every call (owner checks in `OwnerPayout`, policies per route); idempotent replays return the stored response only to the same scope (`TestIdempotencyKeyReusedWithDifferentBody`) |
| Background jobs enforce scope and state | Recover, reconciliation and sweeps act only through state-machine edges and idempotent keys; the routine payment sweep is now scoped to the process's execution environment |
| Customer APIs cannot set financial state | no route sets a payment, payout or reservation status directly; `ck_payout_events_owner_scope`, `ck_payment_events_*` (unchanged; full suite) |
| Internal metrics / operational endpoints | unchanged from Stage 18 (F-S18-05 residual: internal metrics endpoints are network-restricted, not authenticated) |
| Financial gates | `TestFinancialGateConstantsStayOff`, `TestFinancialGateChecksSurviveMigrations` (now including migration `20261024090000`), `TestSecurityFinancialGatesInDatabase` |
| Audit integrity | `TestSecurityAuditChainVerification`, `TestSecurityAuditChainTamperDetection`, `TestFinancialAuditVerificationInterrupted`, `assertChainsIntact` in identity/compliance tests |
| Runtime roles cannot rewrite history; backup role read-only | `TestSecurityRuntimeRolesCannotRewriteFinancialHistory`, `TestSecurityBackupRoleIsReadOnly` |
| Test isolation | barrier and fault tests touch only their own records; scratch `fundzim_perf_s19a/b` for ledger volume; load and worker-kill tests opt-in, local stack only |
| Fault-injection privilege | the local superuser is used only to terminate backends blocked by the test's own row lock (`terminate`); it is read from the generated local `.env` and never printed |

Unresolved security findings carried from Stage 18 (no change in Stage 19): F-S18-03 (no MFA reset workflow), F-S18-04
(no break-glass workflow), F-S18-05 residual (unauthenticated internal metrics), F-S18-12 (no phishing-resistant staff
MFA). No new security finding was raised by Stage 19.

Formal acceptance: Stages 0–18 NOT_RECORDED; Stage 19 NOT_RECORDED. Nothing in this stage records or implies an
acceptance.
