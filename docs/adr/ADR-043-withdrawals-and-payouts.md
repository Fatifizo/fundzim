# ADR-043: Withdrawals and payouts — TEST payouts only, ledger-first reservation, derived journals, acknowledged reviews

- **Status:** Proposed (Stage 11, 2026-10-14)
- **Deciders:** Technical lead (Stage 11); owner acceptance pending; accounting and legal review pending
- **Implements:** ADR-016 (beneficiary verification before payout), ADR-017 (payout approval segregation of duties),
  ADR-020 (payout states); Stage 1 [payout-lifecycle.md](../payments/payout-lifecycle.md) and
  [payout-eligibility-and-controls.md](../payments/payout-eligibility-and-controls.md); Stage 2 draft `design/sql/0016_payouts.sql`
- **Related:** ADR-040 (no provider selected), ADR-042 (TEST ledger, posting role, asynchronous boundary);
  [stage-11/payout-architecture.md](../stage-11/payout-architecture.md)

## Context

Stage 11 builds withdrawal requests, eligibility, reservations, approvals, provider-neutral payout orchestration and
reconciliation while no payout provider is confirmed (ADR-040), no settlement evidence exists, legal items on custody,
third-party payees, chargeback liability and approval thresholds are open (LR-001, LR-004, LR-020, LR-024, LR-030), and
the ledger is a TEST ledger written by its own role (ADR-042). The Stage 1 design assumes the payout row and the
reservation journal commit in one transaction; with the ledger on its own role and pool that is not possible.

## Decision

1. **TEST payouts only.** Every payout carries `execution_environment` (`LOCAL_SIMULATOR` in Stage 11; `PRODUCTION`
   refused by CHECK `ck_payout_requests_no_production_stage11` and by configuration), `test_payout = true`
   (`ck_payout_requests_test_only_stage11`), a sandbox provider (composite FK on `is_sandbox`) and
   `ledger_environment = TEST`. Real execution is also off by the constant `payouts.LiveExecutionEnabled = false` and the
   kill switch `payouts.live_execution.enabled`. `PAYOUTS_EXECUTION_ENVIRONMENT` is `DISABLED` (default) or
   `LOCAL_SIMULATOR`; `PROVIDER_SANDBOX` and `PRODUCTION` are refused at configuration load. Funds that can be reserved
   are synthetic TEST_ONLY ledger balances; production availability is BLOCKED (no settlement evidence: EC-20 fails for
   any non-test payout).
2. **Names.** A "withdrawal request" is a row of `app.payout_requests` (Stage 2 name); states are ADR-020's
   (`PAYOUT_REQUESTED … REVERSED`). The brief's REQUESTED/RESERVED/PENDING_APPROVAL/READY_FOR_SUBMISSION/SUCCEEDED map to
   them ([withdrawal-lifecycle.md](../stage-11/withdrawal-lifecycle.md)); RECONCILIATION_REQUIRED is the
   `needs_reconciliation` flag plus an exception. API paths are the Stage 2 design paths
   (`/campaigns/{campaign_id}/payout-requests`, `/payout-requests/…`, `/admin/payout-requests/…`) plus `/admin/payouts/…`.
3. **Slot first, then the ledger reservation.** A request that passes request-time eligibility is inserted as
   `PAYOUT_REQUESTED`, which claims the single in-flight slot (`uq_payout_requests_one_inflight`) in the app
   transaction. The ledger then posts `PAYOUT_RESERVED` (`payout:{id}:reserved`, campaign_payable → payout_pending) on its
   own pool under its row locks — the ledger, not the app, decides whether the money is there. Only then does
   `payout_reservations` (cross-schema FK to the journal) appear and the payout move to `PENDING_REVIEW`. A refusal moves
   it to `REJECTED`; a transient failure leaves it `PAYOUT_REQUESTED` for the recovery job (same idempotency key).
4. **Journals derive from state.** After every transition `syncLedger` posts the journals the payout's state requires
   (`submitted`, `completed`, `failed`, `released`, `reversed`; deterministic keys, idempotent) and links them on the
   reservation. The recovery job re-drives missing journals; reconciliation reports any that stay missing
   (LEDGER_MISMATCH). Payout journals use `source_system = payouts` (new) and, in a TEST ledger, `SIMULATED` evidence
   (CHECK `ck_ledger_transactions_payouts_simulated_in_test`).
5. **Eligibility engine.** A pure function over facts gathered through owning modules' interfaces; all 22 checks
   EC-01..EC-22 recorded in a hashed, append-only decision (the database re-validates result set and outcome). It runs at
   REQUEST, PRE_SUBMIT (the database refuses `SUBMITTED` without an ELIGIBLE PRE_SUBMIT decision in the same
   transaction) and RECHECK. Unconfigured limits are REVIEW and force the DUAL tier; there is no AUTO tier (PD-24).
   **Acknowledged reviews:** at PRE_SUBMIT, a REVIEW finding that was present in the decision the approvers saw counts as
   acknowledged; only new findings send the payout back to review (Y7). Without this, fail-closed REVIEW checks would
   block every submission forever.
6. **Maker-checker.** FINANCE approvers only; the database guard refuses approvals by the requester, the campaign owner
   or anyone linked to them (`app.actor_identities`), and by whoever last changed or verified the destination; Go also
   refuses members of the owning organisation. Approvals bind to the round; destination changes, suspensions, expiry and
   pre-submission findings start a new round. Approval validity is time-bound only if `payout.approval_validity` is
   configured (no regulatory time limit is invented).
7. **Provider boundary.** `psp.PayoutProvider` (CreatePayout, GetPayout, CancelPayout, MapPayoutStatus, PayoutLimits).
   Only the sandbox simulator implements it. Every call is an attempt committed IN_FLIGHT first; the database refuses a
   CREATE_PAYOUT unless the payout is SUBMITTED, its in-transit journal exists and no earlier CREATE may have reached the
   provider. UNKNOWN keeps the reservation (`BLOCKED`) and is resolved only by an authoritative status, webhook or
   reconciliation.
8. **Holds.** No hold registry exists (`risk.holds` is not built). EC-12 uses the holds that do exist: campaign status,
   ledger account status (FROZEN/CLOSED) and destination suspension; EC-13 uses compliance restrictions.
9. **Destination security.** Creating, changing and retiring a payout destination now requires a fresh step-up; any
   change, suspension or verification expiry invalidates the approvals of open payouts using it.

## Consequences

- The owner sees `PAYOUT_REQUESTED` for the instant between slot and reservation; reconciliation and recovery cover a
  crash in between.
- Every payout in Stage 11 is a test payout of synthetic funds; nothing here can move real money.
- Approver workload is maximal (DUAL for everything) until the owner configures `payout.dual_approval_min` and the
  other limits (LR-030).

## Alternatives considered

| Alternative | Why not |
|---|---|
| Reserve in the ledger first, then insert the payout | A refused insert (in-flight slot taken, idempotency replay) would need a compensating release journal for every losing concurrent request |
| Give `fundzim_app` ledger INSERT rights to keep one transaction | Contradicts ADR-042 (only the ledger writes ledger tables) |
| A separate payout ledger or balance table | Forbidden by the brief and CLAUDE.md rule 5 |
| AUTO approval below a threshold | No approved threshold exists (LR-030, PD-24) |
