# Stage 11 — Payout security review

Self-review by the implementing engineer; no external review has taken place.

## 1. Controls

| Threat | Control | Tested by |
|---|---|---|
| Real money leaves | TEST payouts only: constant `payouts.LiveExecutionEnabled=false`, kill switch off, `PAYOUTS_EXECUTION_ENVIRONMENT` refuses PROVIDER_SANDBOX/PRODUCTION, CHECKs (no PRODUCTION, test_payout, sandbox provider, TEST ledger), routing to sandbox providers only | config unit tests; `TestPayoutDatabaseGuards` |
| Unauthorised withdrawal / cross-campaign / cross-tenant access | owner or ORG_ADMIN only (`campaigns.OwnerAccess`; others get 404, existence hidden); owner views only for the campaign's members | `TestPayoutEligibilityRefusalsAndPreSubmitRecheck` (other user 404) |
| Self-approval, linked-account bypass | FINANCE only; Go + DB guard on requester, owner, linked identities, destination changer/verifier; org members (Go); one decision per approver per round | `TestPayoutApprovalSeparationOfDutiesAndInvalidation` |
| Stale approval reuse | approval rounds; immutable amount/destination snapshot; destination change/suspension/expiry and re-check findings start a new round; optional expiry | same |
| Destination substitution / payout redirection | destination snapshot (version + hash) bound at request; EC-07 at submission; step-up to create/change/retire destinations; cooling-off (EC-06); no cross-beneficiary destination (EC-05) | engine unit tests; SoD test |
| Duplicate payout instruction | single in-flight index; idempotency key UNIQUE; attempts guard (CREATE only while SUBMITTED, after the in-transit journal, never after an attempt that may have reached the provider); same reference on every retry; ledger keys UNIQUE | concurrency and UNKNOWN tests; WORKER_RESTART scenario |
| Treating a timeout as failure | INDETERMINATE ⇒ UNKNOWN; FAILED never from SYSTEM (CHECK); "not found" final only after the documented window | `TestPayoutUnknownOutcomeKeepsReservationAndIsNeverResubmitted` |
| Forged/replayed callbacks | Stage 8 inbox: signature + replay window per provider, dedupe by event id; simulation flag must match the payout; amount/currency anomalies recorded, not applied | INVALID_SIGNATURE, DUPLICATE_CALLBACK scenarios |
| Over-allocation | ledger row lock + CHECK; EC-09 | concurrency test |
| Synthetic funds shown as real | TEST_ONLY labels, production BLOCKED in every owner/staff view; fixtures only via the ADMIN staff API in a TEST ledger | owner view assertions; E2E |
| Tipping off | owner refusals carry only coarse categories; EC-13/EC-16 detail masked | engine unit test |
| Rate abuse | `payouts.request.user` 10/hour per user (operational starting value) on request and cancel | throttle unit test; integration |
| Secret leakage | payouts hold no secrets; destinations keep the C3 account identifier encrypted; the simulator receives only the masked reference; no account numbers in events, audit or logs | code review |
| CSRF | standard session + CSRF middleware on all mutations | platform |
| Direct DB tampering | append-only events, decisions, approvals, attempts (once), recon refs; state machines; no DELETE/TRUNCATE for runtime roles | `TestPayoutDatabaseGuards` |

## 2. Limits (stated, not hidden)

- A database superuser or anyone with storage access can alter data (as KI-S10-05).
- The organisation-member SoD check is in Go only (the database guard covers requester, owner, links, destination
  changer/verifier).
- Fraud detection is minimal rule-based indicators; the scored risk engine is Stage 13. Automated fraud detection is not
  complete.
- No sanctions screening exists (KI-S5-04); only TEST payouts can pass EC-15.
