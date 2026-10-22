# Stage 17 — Matching engine

Code: `internal/reconciliation/match.go` (pure `Match`), `runs.go` (inputs, persistence), `rules.go` (rule sets).
Unit tests: `internal/reconciliation/reconciliation_test.go`.

## 1. Principles

1. **Deterministic.** `Match` is a pure function: the same lines, records, journals, fee expectations and rule set give
   the same decisions in the same order (lines sorted by booking time, then id). Every run stores the SHA-256 of its
   decisions (`runs.decisions_sha256`); a unit test shuffles the input and checks the hash.
2. **References find candidates; amounts only verify.** A line is linked to a payment or payout only through our id
   (sent to the provider as merchant reference) or the provider's reference. Amount and currency are compared after a
   candidate is found. A line with the exact amount of a payment but no reference to it is MISSING_PAYMENT, never a match.
3. **Ambiguity is never resolved automatically.** Several candidates, references naming different records, or a
   one-reference match outside the date window → AMBIGUOUS_MATCH for a person.
4. **Explainable.** Each match records confidence, rule code, rule set id and version, matcher version (`MATCHER_V1`) and
   an explanation (reference states AGREES / NOT_KNOWN_FOR_RECORD / ABSENT, record and line status, day gap, fee basis).
5. **Versioned.** Parameters live in maker-checker rule sets; a decision keeps the version it was made under; a new
   version never rewrites earlier matches (only a superseding statement revokes matches).

## 2. Confidence

| Confidence | When |
|---|---|
| EXACT_MATCH | our reference and the provider's reference both agree on the same record |
| RULE_BASED_MATCH | one reference agrees, the other is absent or not yet known for the record, and the booking is within `date_window_days` of the record's time (rule `MERCHANT_REF_IN_WINDOW` / `PROVIDER_REF_IN_WINDOW`) |
| AMBIGUOUS_MATCH (exception) | several candidates, conflicting references, or outside the window |
| NO_MATCH (exception) | no candidate: MISSING_PAYMENT (credit) or MISSING_PAYOUT (debit) |
| MANUAL | created only by an approved MANUAL_MATCH resolution (maker ≠ checker, evidence) |

## 3. Decisions per line kind

| Line | Record state | Decision |
|---|---|---|
| PAYMENT SUCCEEDED | succeeded family, same amount | match; ledger `NOT_POSTED_TEST_PAYMENT` for test payments, else journal `payment:{id}:capture` must exist (`POSTED` / MISSING_LEDGER_ENTRY); fee compared |
| PAYMENT SUCCEEDED | different amount | AMOUNT_MISMATCH (HIGH) |
| PAYMENT SUCCEEDED / FAILED | UNKNOWN | UNKNOWN_PAYMENT_OUTCOME (HIGH) — nothing applied (unknown-outcomes.md) |
| PAYMENT SUCCEEDED | FAILED / EXPIRED / CANCELLED | STATUS_MISMATCH (CRITICAL) |
| PAYMENT FAILED | succeeded family | STATUS_MISMATCH (CRITICAL) |
| PAYMENT FAILED | failed family | match (rule `CONSISTENT_FAILURE`, ledger NOT_APPLICABLE) |
| PAYMENT any | not yet final at FundZim | STATUS_MISMATCH (LOW, closes when the record catches up) |
| REFUND SUCCEEDED | not REFUNDED / PARTIALLY_REFUNDED | STATUS_MISMATCH (CRITICAL: FundZim cannot execute refunds) |
| CHARGEBACK / REVERSAL SUCCEEDED | not DISPUTED / CHARGED_BACK / refunded | STATUS_MISMATCH (HIGH) |
| PAYOUT COMPLETED | COMPLETED / REVERSED, same amount | match; journal `payout:{id}:completed` must exist (TEST payouts post SIMULATED journals) |
| PAYOUT COMPLETED | different amount | PAYOUT_MISMATCH (CRITICAL) |
| PAYOUT COMPLETED / FAILED | UNKNOWN | UNKNOWN_PAYOUT_OUTCOME (CRITICAL) — nothing applied, nothing resubmitted |
| PAYOUT COMPLETED | FAILED / CANCELLED / REJECTED or never submitted | PAYOUT_MISMATCH (CRITICAL) |
| PAYOUT FAILED | COMPLETED | PAYOUT_MISMATCH (CRITICAL) |
| PAYOUT RETURNED / PAYOUT_REVERSAL | not REVERSED | STATUS_MISMATCH (HIGH) or PAYOUT_MISMATCH (CRITICAL) |
| FEE / ADJUSTMENT | — | PROVIDER_ADJUSTMENT (LOW / MEDIUM): needs an explanation |
| any | another line already matched the same record (same kind, same source) | DUPLICATE_TRANSACTION (HIGH); nothing deleted |
| any | currency differs from the record | CURRENCY_MISMATCH (CRITICAL); never converted |
| PENDING lines | — | informational: left UNMATCHED, decided by a later final line |

Record-level: a record (succeeded payment; completed payout for transaction reports) whose time lies inside a parsed
period of the source and is older than `settlement_horizon_days` without an active match from that source →
MISSING_SETTLEMENT (HIGH for payments, CRITICAL for payouts).

Bank statements: see bank-reconciliation.md.

Fees: the line's reported fee against FundZim's assessed provider fee for the record (fees module): `MATCHED`,
`MISMATCH` (+ FEE_MISMATCH, MEDIUM), `NOT_REPORTED` (no fee on the line) or `NO_EXPECTATION` (FundZim's fee is UNKNOWN —
an unknown fee is never 0).

## 4. Rule sets

| Key | Bounds | Used for |
|---|---|---|
| `date_window_days` | 0–14 | RULE_BASED_MATCH window |
| `settlement_horizon_days` | 1–60 | MISSING_SETTLEMENT and MISSING_BANK_CREDIT age |
| `stale_after_hours` | 1–2160 | STALE_RECONCILIATION for a used source without a newer parsed statement |
| `escalate_after_hours` | optional, 1–2160 | escalation of open exceptions (none when absent) |

Closed key set (Go and `ck_recon_rule_sets_config`), canonical JSON hashed (`config_sha256`), immutable after proposal
(column guard), maker-checker (Go, CHECK `decided_by <> proposed_by`, trigger `recon.checker_guard` with
`app.actor_identities`), approval with step-up and optionally against the hash. Approving retires the previous ACTIVE
set. **None is seeded**: these are settlement-timing assumptions nobody has confirmed, so without an ACTIVE set runs fail
(`NO_ACTIVE_RULE_SET`), scheduling stops and the dashboard shows NOT_CONFIGURED.

## 5. Runs

A run covers one source, one currency and the union window of that source's parsed statement periods (last 93 days of
periods). It considers the window's lines that are UNMATCHED or in EXCEPTION (matched and explained lines are final),
gathers the records they reference (and, for MISSING_SETTLEMENT, the records the window should contain) through the
owning modules, the expected journals (`ledger.JournalsByKeys`) and fee expectations, calls `Match`, then in one
transaction: inserts matches, opens or re-sees exceptions, updates line statuses, evaluates the touched and unsettled
settlement batches, closes timing exceptions whose condition the run no longer finds (only those whose line or record it
looked at), and completes the run with its counts and decisions hash. Kinds: SCHEDULED (worker), ON_DEMAND and REPLAY
(staff, reason, audited).

## 6. Bounds

200 000 lines per run, 5 000 references per lookup, 10 MiB per file, 100 000 lines per file.
Measured in [testing.md](testing.md) §4.
