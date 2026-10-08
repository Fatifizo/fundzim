# Payout Schema

> **Status:** Stage 2 design. Nothing here is implemented; **no live payouts before Stage 20** (Gate C). The DDL
> is a **non-executable draft**: [`design/sql/0016_payouts.sql`](../../design/sql/0016_payouts.sql), validated
> only in in-memory PGlite. Invariant tests: [`design/sql/tests/payouts_test.sql`](../../design/sql/tests/payouts_test.sql)
> (74 cases, money moved through the real `0006` ledger journals).

Contract: [design-baseline.md](../stage-2/design-baseline.md) §5.17, §6 (#4), §8. Behaviour:
[payout-lifecycle.md](../payments/payout-lifecycle.md) (Y1–Y14), [payout-eligibility-and-controls.md](../payments/payout-eligibility-and-controls.md)
(EC-01–EC-22), [operational-controls.md §2–§4](../compliance/operational-controls.md), ADR-017, ADR-020. State
machine and journals: [payout-state-machine.md](../architecture/payout-state-machine.md).

---

## 1. Ownership

`payouts` owns, in schema `app`: `payout_destinations`, `payout_destination_verifications`, `payout_requests`,
`payout_attempts`, `payout_approvals`, `payout_reservations`, `payout_provider_references`, `payout_events`,
`payout_failures`, `payout_reversals`, `payout_eligibility_decisions`, `recovery_cases`, `recovery_case_events`,
and `payout_destination_override_requests` (added by baseline §12 I-3).

Declared FKs (references only, never queried by payouts SQL): `app.users`, `app.organisations`, `app.campaigns`,
`app.beneficiaries`, `app.institution_payees`, `app.currencies`, psp tables, `ledger.ledger_transactions`,
`risk.holds`, `audit.evidence_records`. **No FK into `compliance`** (blocking cases are read via the compliance
interface; ids stored only). Recovery-case sources from `payments` are `(source_type, source_id)` without FK.

## 2. Tables

### 2.1 `payout_destinations` (C3 account number, versioned)

| Column | Notes |
|---|---|
| `owner_user_id` / `owner_organisation_id` | exactly one; `owner_ref` generated = coalesce |
| `payee_type` + `beneficiary_id` / `institution_payee_id` | exactly one payee reference consistent with type (handover D4) |
| `rail`, `currency` | immutable; rails `ECOCASH, ONEMONEY, INNBUCKS, OMARI, ZIMSWITCH, BANK_TRANSFER`; bank rails need `bank_code` |
| `account_number_ciphertext`, `account_number_key_id` | app envelope encryption, key class `payout-destination` (baseline §5.17) |
| `account_number_bidx` | HMAC blind index (32 bytes): duplicate and cross-owner reuse (mule) detection |
| `masked_suffix` | 2–4 chars, the only displayable part |
| `destination_hash` | SHA-256 over canonical fields incl. version; used in the payout snapshot |
| `status` | machine `payout_destination`: `PENDING_VERIFICATION, VERIFIED, VERIFICATION_FAILED, NEEDS_REVERIFICATION, SUSPENDED, RETIRED` |
| `verified_at`, `last_verified_by`, `last_changed_by` | SoD inputs for approval |
| `cooling_off_until` | NOT NULL; restarted on every change (EC-06) |
| `version` | +1 on every change of account data |

`app.payout_destinations_guard`: owner/rail/currency immutable; any change of account data must bump `version` by 1,
reset to `PENDING_VERIFICATION`, set a future `cooling_off_until` and a new hash. `uq_payout_destinations_live_account
(owner_ref, rail, currency, account_number_bidx) WHERE status <> 'RETIRED'`. Never deleted.

### 2.2 `payout_destination_verifications` (append-only)

Per `destination_version`: `check_kind` (`OWNERSHIP, NAME_MATCH, RAIL_VALIDITY`), `method`, `result`
(`PASS, FAIL, INCONCLUSIVE`), `name_match_score_bps` (integer bps), provider ref, `performed_by`,
`evidence_record_id` (manual methods need staff + evidence).

### 2.2a `payout_destination_override_requests` (staff override, maker-checker — baseline §12 I-3)

Staff change of a destination on the owner's behalf (operational-controls §3: maker SUPPORT/COMPLIANCE with
user-request evidence; checker FINANCE `payout.destination.override.approve`; 24 h expiry).

| Column | Notes |
|---|---|
| `payout_destination_id`, `base_destination_version` | the version the maker saw |
| `proposed_holder_name`, `proposed_bank_code`, `proposed_account_ciphertext/_key_id/_bidx`, `proposed_masked_suffix`, `proposed_destination_hash` | C3 proposal, encrypted like the destination; immutable after creation |
| `reason` (≥ 10 chars), `evidence_record_ids uuid[]` (≥ 1) | user-request evidence (ids only) |
| `requested_by`, `approved_by` | `ck_pdor_maker_checker: approved_by <> requested_by`; step-up time required |
| `status` | machine `payout_destination_override`: `'' → PENDING → APPROVED \| REJECTED \| EXPIRED \| WITHDRAWN`; `APPROVED → APPLIED \| EXPIRED` |
| `expires_at` | approval after expiry rejected |
| `applied_at`, `applied_destination_version`, `cooling_off_until` | set on APPLIED |

`uq_pdor_one_open`: one open (PENDING/APPROVED) override per destination. Trigger `app.payout_destination_override_guard`:
APPLIED only if the destination already carries the proposal as version `base + 1`, `PENDING_VERIFICATION`, a
running cooling-off, and `last_changed_by = requested_by` — so **cooling-off is triggered** and the maker is
excluded from approving payouts to that destination (approvals guard). The owner notification on all channels is an
outbox event in the same transaction. Never deleted.

### 2.3 `payout_requests` (`payout_id = id`; machine `payout`)

| Column | Notes |
|---|---|
| `campaign_id`, `currency`, `amount_minor > 0`, `requested_by`, `requested_at` | immutable |
| `status`, `status_rank` | `ck_…_status_rank = app.payout_status_rank(status)` |
| `approval_policy` | `AUTO, SINGLE, DUAL` — may only be raised |
| `approval_round` | starts 1; Y7 must bump it, invalidating earlier approvals |
| `policy_version` | eligibility policy version used |
| `destination_id, destination_version, destination_hash, destination_rail, destination_masked` | **snapshot**, immutable; FK `(destination_id, currency, destination_rail) → payout_destinations (id, currency, rail)` ⇒ payout currency = destination currency |
| `provider_id, provider_account_id, capability_id, capability_routable, capability_payout_api` | FK `(capability_id, provider_id, currency, routable, payout_api)` + CHECK both true ⇒ only routable payout-capable capabilities; provider bound once submitted |
| `provider_payout_ref` | `UNIQUE (provider_id, provider_payout_ref)`, set once |
| `provider_fee_minor`, `beneficiary_received_minor` | PCR-013 / PD-23; received ≤ amount |
| `unknown_since`, `unknown_reason`, `next_poll_at`, `poll_attempts`, `poll_horizon_at`, `needs_reconciliation` | reasons incl. `CRASH_DURING_CREATE` |
| `submitted_at`, `completed_at`, `failure_code` | required by state |
| `idempotency_scope`, `idempotency_key` | `UNIQUE` (client `Idempotency-Key`) |

**EC-11:** `uq_payout_requests_one_inflight (campaign_id, currency) WHERE status IN ('PAYOUT_REQUESTED',
'PENDING_REVIEW','APPROVED','SUBMITTED','PROCESSING','UNKNOWN')`.

Triggers: `trg_payout_requests_guard` (immutability; AUTO-only skip of review; approvals count for APPROVED and
SUBMITTED — distinct FINANCE approvers ≥ 1 SINGLE / ≥ 2 DUAL (I-20), current round, unexpired, no REJECT; SUBMITTED
needs an `ELIGIBLE` `PRE_SUBMIT` decision created **in the same transaction**); `trg_payout_requests_guard_status`;
deferred `trg_payout_requests_check_consistency` (REQUEST decision exists; reservation state matches payout state;
submit journal exists once submitted); deferred `trg_payout_requests_require_event`.

### 2.4 `payout_approvals` (append-only)

`payout_request_id, approval_round, approver_id, approver_role` (**FINANCE only** — baseline §12 I-20: COMPLIANCE never approves payouts), `decision (APPROVE|REJECT),
reason, step_up_at, conflict_attested (must be true), decided_at, expires_at`.
`uq_payout_approvals_approver (payout_request_id, approval_round, approver_id)`. Trigger
`app.payout_approvals_guard`: approver ≠ requester; approver ≠ destination `last_changed_by` / `last_verified_by`;
payout must be `PENDING_REVIEW`; round must be current.

> Deviation from the brief's `UNIQUE (payout_request_id, approver_id)`: the round is included so an approver can
> re-approve after Y7 invalidated the round; within a round the brief's uniqueness holds.

### 2.5 `payout_reservations` (one per payout)

`status` (machine `payout_reservation`: `RESERVED → RELEASED | CONSUMED`), FK `(payout_request_id, campaign_id,
currency, amount_minor)`, `ledger_reserve_txn_id` NOT NULL (UNIQUE), `ledger_submit_txn_id`,
`ledger_release_txn_id` (released **or** failed journal), `ledger_consume_txn_id` (completed journal). All journal
FKs are `(txn_id, currency) → ledger.ledger_transactions (id, currency)`. Links are set once.

| Payout state | Reservation | Journals present |
|---|---|---|
| PAYOUT_REQUESTED, PENDING_REVIEW, APPROVED | RESERVED | reserved |
| SUBMITTED, PROCESSING, UNKNOWN | RESERVED | reserved, submitted |
| COMPLETED, REVERSED | CONSUMED | reserved, submitted, completed (+ reversed in `payout_reversals`) |
| REJECTED, CANCELLED | RELEASED | reserved, released |
| FAILED | RELEASED | reserved, submitted, failed |

### 2.6 `payout_attempts`

As `payment_attempts` (IN_FLIGHT first, completed once), operations `CREATE_PAYOUT, GET_STATUS, CANCEL`,
`our_reference = payout_request_id` (CHECK). Trigger `app.payout_attempts_guard`: provider = bound provider;
**`CREATE_PAYOUT` only while `SUBMITTED` and only if every earlier `CREATE_PAYOUT` was `DEFINITELY_NOT_SENT`**.
An in-flight, unknown, accepted or rejected earlier attempt blocks any further create: "never resubmit" is a DB
rule.

### 2.7 Others

- `payout_provider_references` — `UNIQUE (provider_id, ref_kind, reference)`, append-only.
- `payout_events` — append-only; `transition_code` Y1–Y14; checks: COMPLETED/REVERSED/PROCESSING only from
  `SYNC, WEBHOOK, POLL, RECON` (synchronous authoritative completion accepted — baseline §12 I-22, ADR-024 point 7);
  `FAILED` only from `SYNC, WEBHOOK, POLL, RECON, STAFF` — never `SYSTEM` (`ck_payout_events_failed_source`, I-21); UNKNOWN only from `SYSTEM`; OWNER source only for `PAYOUT_REQUESTED`/`CANCELLED`;
  SYSTEM-sourced UNKNOWN → FAILED rejected (I-21); staff FAILED needs evidence; STAFF needs actor + reason. Subject FK deferrable.
- `payout_failures` — append-only observations (`is_final`, `failure_class`, provider code verbatim; staff-confirmed
  needs evidence; `LATE_COMPLETION_AFTER_FAILED` records the SEV1 case).
- `payout_reversals` — one per payout; amount ≤ payout amount, payout must be COMPLETED, same provider;
  `ledger_transaction_id` NOT NULL (`payout:{id}:reversed`).
- `payout_eligibility_decisions` — append-only; `phase` `REQUEST | PRE_SUBMIT | RECHECK` (brief's PRE_SUBMIT =
  Stage 1 "SUBMIT"); `results` validated by `app.eligibility_results_valid`: exactly EC-01…EC-22 once each, valid
  result values, `ELIGIBLE` only if no FAIL/REVIEW/DEFER, FAIL ⇒ `REJECTED`, and at `PRE_SUBMIT` EC-22 may not be
  `NOT_APPLICABLE`. `payout_request_id` NULL only for refused requests (`REQUEST`, `REJECTED`). Deferrable FK
  `(payout_request_id, campaign_id, currency, amount_minor)` ⇒ the decision describes exactly this payout.
  `record_hash` 32 bytes.
- `recovery_cases` (machine `recovery_case`: `OPEN, PARTIALLY_RECOVERED, RECOVERED, WRITE_OFF_PENDING,
  WRITTEN_OFF`) — `UNIQUE (source_type, source_id)`; recovered + written-off ≤ amount, never decreasing; `recovery_hold_id → risk.holds`.
  **Write-off maker-checker (baseline §12 I-9):** `WRITE_OFF_PENDING` is the pending object and needs
  `write_off_requested_by`, `write_off_amount_minor` (≤ amount) and `write_off_threshold_limit_ref` (the
  `risk.limits` record applied); `WRITTEN_OFF` needs `write_off_approved_by` (≠ requester,
  `ck_recovery_cases_write_off_sod`) and `write_off_approver_role` — a second `FINANCE` below the threshold,
  `BUSINESS_APPROVER` above it (role/threshold match checked by the service against the limit; I-4, I-10). `recovery_case_events` append-only, money
  events need amount + ledger link; deferred same-transaction event rule.

## 3. Concurrency control for simultaneous withdrawal requests

### 3.1 Lock order (request, Y1)

All in **one READ COMMITTED transaction**, in this order:

1. `idempotency_keys` row (`IN_PROGRESS`, own short tx, PAYMENTS §10.1) — same key ⇒ replay or 409.
2. Eligibility reads through module interfaces (campaign, kyc level, holds, screening, limits) — no locks held on
   other modules' rows; they are re-checked at PRE_SUBMIT anyway.
3. `ledger.Post(PAYOUT_RESERVED, key 'payout:{id}:reserved')` — the **ledger service** locks the balance
   projection rows it touches **in ascending account-id order** (`UPDATE ledger_balances …` takes the row lock) and
   `ck_ledger_balances_non_negative` rejects any reservation that would take `campaign_payable` below zero (L8,
   EC-09). Two concurrent requests serialise on the `campaign_payable:{c}` balance row; the second sees the
   reduced balance.
4. INSERT `payout_eligibility_decisions` (REQUEST), `payout_events`, `payout_requests`, `payout_reservations`.
   The partial unique index rejects a second in-flight payout for the same (campaign, currency) — the loser gets
   `409 PAYOUT_IN_FLIGHT` and its whole transaction (including the reservation journal) rolls back.
5. COMMIT. Deferred checks run (reservation present, decision present, event present).

### 3.2 Why SERIALIZABLE is not needed

Each financial decision is covered by a single-row or unique-index guard: the available balance by the locked
projection row plus the non-negative CHECK; the single in-flight rule by the unique index (index insertion is
serialised by PostgreSQL even under READ COMMITTED: the second inserter waits for the first to commit/rollback);
double submission by the state transition under `FOR UPDATE` (`APPROVED → SUBMITTED` is the claim); provider
resubmission by the attempts trigger under the payout row lock; double posting by ledger idempotency keys. No
decision reads a *set* of rows that a concurrent transaction could change without touching one of those locks, so
DATABASE §12's condition for SERIALIZABLE is not met. If PD-23 later allows multiple in-flight payouts, the unique
index goes away and the balance lock alone remains sufficient.

### 3.3 Submission and resolution locks

Submission worker: `SELECT … FROM payout_requests WHERE id = $1 FOR UPDATE` → re-check (PRE_SUBMIT decision) →
ledger `payout:{id}:submitted` → reservation `ledger_submit_txn_id` → `APPROVED → SUBMITTED` + event → attempt row
IN_FLIGHT → COMMIT → provider call (outside any tx) → result in a new tx (lock payout again). Webhook/poll apply:
payout row `FOR UPDATE` → ledger → reservation → status + event. Lock order everywhere: **payout row → reservation
row → ledger balances (account-id order)**.

## 4. Fail-closed rules enforced in SQL

| Rule | Enforcement |
|---|---|
| Request-time checks ran (Y1) | deferred consistency trigger needs a non-REJECTED REQUEST decision |
| Pre-submission re-check ran now and passed | guard: ELIGIBLE PRE_SUBMIT decision with `created_at = transaction_timestamp()` |
| Approvals obtained from distinct FINANCE approvers (I-20), ≠ requester, ≠ destination changer/verifier | approvals role CHECK + approvals guard + payout guard |
| Unconfigured limit or REVIEW never auto-approves | `eligibility_results_valid` (REVIEW/DEFER ⇒ not ELIGIBLE); AUTO only from PAYOUT_REQUESTED |
| Money location matches state | consistency trigger + reservation checks |
| Never resubmit UNKNOWN | no `UNKNOWN → SUBMITTED` edge; attempts guard |

## 5. Test requirements

Implemented (`payouts_test.sql`): reservation reduces available; second in-flight rejected; over-available rejected
by the ledger; missing reservation / decision rejected; non-payout capability and destination currency mismatch
rejected; amount and snapshot immutable; self-approval and destination-verifier approval rejected; approval only in
PENDING_REVIEW; approvals append-only; DUAL needs two distinct approvers; duplicate approver rejected;
COMPLIANCE approval rejected (I-20); DUAL approved by two distinct FINANCE; non-AUTO cannot skip review; policy cannot be lowered; SUBMITTED without re-check rejected; ELIGIBLE with
FAIL/REVIEW rejected; missing check rejected; PRE_SUBMIT must check EC-22; decision facts must match payout;
refused-request decisions; decisions append-only; attempt reference = payout id; second create while in flight
rejected; timeout → UNKNOWN; **UNKNOWN → SUBMITTED rejected even with a fresh ELIGIBLE re-check**; create while
UNKNOWN rejected; status query allowed; SYSTEM-sourced UNKNOWN → FAILED rejected (I-21); staff FAILED needs evidence; UNKNOWN → FAILED needs funds returned and is
allowed on authoritative failure with the failed journal; external event cannot push into UNKNOWN; completion
source and reservation rules; COMPLETED → FAILED rejected; links set once; events append-only; same-reference retry
only after DEFINITELY_NOT_SENT; Y7 must bump the round and old approvals stop counting; owner cannot drive approval;
cancel needs release; destination versioning; payee exactly-one; verifications append-only; override
self-approval rejected; override applied only with new version + cooling-off; override maker cannot approve payouts
to the destination; recovery write-off SoD; write-off pending needs maker, amount and limit ref; written-off needs checker and role; recovery status change needs event.

Note: EC-05 (destination `VERIFIED`) and EC-06 (cooling-off elapsed) are evaluated by the eligibility engine and
recorded in the decision; the payout row itself does not re-read destination status (it pins the snapshot), so a
payout created against an unverified destination fails at the decision (FAIL ⇒ no payout row), not at a FK.

Stage 11/19 on real PostgreSQL: N concurrent requests exceeding the balance ⇒ exactly the affordable number
reserve; two submission workers ⇒ one provider call; crash between attempt insert and result ⇒ UNKNOWN
(`CRASH_DURING_CREATE`) and poll, never resubmit; late completion after FAILED ⇒ SEV1 + correcting journal.
