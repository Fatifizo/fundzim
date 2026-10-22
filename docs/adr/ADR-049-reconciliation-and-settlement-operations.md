# ADR-049: Reconciliation and settlement operations — `reconciliation` module on the `recon` schema, canonical TEST statement imports, deterministic versioned matching, maker-checker resolutions, no automatic settlement postings

- **Status:** Proposed
- **Date:** 2026-10-22
- **Deciders:** Technical lead (Stage 17)
- **Stage:** 17
- **Implements / amends:** ADR-030 (reconciliation architecture) and `docs/database/reconciliation-schema.md` (Stage 2
  draft); design-baseline §3 (module graph: `reconciliation` imports `payments`, `payouts`, `fees`, `ledger`, `psp`,
  `storage`) and §5 (table ownership: `recon.*`); Stage 5 storage content rules (CSV/JSON for `RECONCILIATION_REPORT`
  only); Stage 11 payout attempts guard (`RATE_LIMITED` treated as refused-before-processing, KI-S17-01)

## Context

ADR-030 made reconciliation a first-class module with its own `recon` schema and staged it: settlement matching was to
come with the ledger and payouts, full reconciliation in Stage 17. In practice Stages 8–12 built per-module
consistency checks (`payments` reconciliation references, `ledger` integrity and journal checks, `payouts` provider
status checks, `fees` recomputation) and every one of them reports settlement reconciliation as `NOT_PERFORMED`. The
`recon` schema exists (grants) but has no tables.

Constraints that shape every decision below:

- **No provider is selected (ADR-040).** No real statement, settlement report, bank statement, file format or
  settlement schedule exists or may be invented (PCR-007, PCR-016, PCR-045, PCR-051). Anything imported in Stage 17 is
  TEST evidence: staff-uploaded files in FundZim's own canonical format, or statements generated from the sandbox
  simulator's own records.
- **Test payments are never posted (ADR-042).** The ledger is TEST; a sandbox payment has no `DONATION_CAPTURED`
  journal, so a `SETTLEMENT_MATCHED` posting (Dr `psp_settled` · Cr `psp_clearing`) for it would credit a clearing
  balance that was never debited. The chart and posting rules still await accounting review (KI-S10-02).
- **Model A (ADR-013).** Donor funds never settle to FundZim. FundZim's own bank account receives only its platform-fee
  income, so a bank statement can only be matched against fee remittances — and anything that looks like a donation
  arriving there is itself an alarm.
- `fundzim_app` has no privilege on the `ledger` schema; only `ledger.Service` on the `fundzim_ledger` pool writes
  ledger tables. A recon row and a ledger journal can therefore never commit in one transaction.
- Unknown ≠ failed; an UNKNOWN payout is never resubmitted; a browser or a single person's claim never confirms money.

## Decision

1. **Module.** `internal/reconciliation` owns every table of the `recon` schema and nothing else. It uses the app pool
   (`fundzim_app`, which already holds `recon` grants). It reads other modules only through their public services:
   `payments` and `payouts` (new read methods returning reconciliation facts by id, provider reference and window,
   plus one narrow write each — see 7), `fees` (fee facts per source), `ledger` (journal lookup by key, integrity,
   trial balance, adjustments), `psp` (provider registry; the sandbox's statement generator) and `storage` (statement
   files as private evidence). No module imports `reconciliation` (archtest).

2. **Sources and imports.** A source (`recon.sources`) is a configured statement stream with a kind
   (`PROVIDER_TRANSACTION_REPORT`, `PROVIDER_SETTLEMENT_REPORT`, `BANK_STATEMENT`), an authority rank, a format and an
   environment. Only TEST sources exist (`ck_*_test_only_stage17`; `ProductionImportsEnabled = false`); a production
   source needs a provider selection ADR, the provider's documented format, a parser for it and stage acceptance.
   The only formats are FundZim's own **canonical** contracts `FUNDZIM_CANONICAL_CSV_V1` and
   `FUNDZIM_CANONICAL_JSON_V1` (docs/stage-17/settlement-management.md) — provider-neutral, integer minor units,
   ISO 4217 codes, no provider schema is assumed. Imports are immutable and idempotent: the file is hashed (SHA-256)
   before anything else, the hash is unique across all sources, a statement reference is unique per source unless an
   import explicitly supersedes an earlier one, and a statement line is identified by `(source, external line id)` —
   never by filename. The file is stored once in the private evidence bucket (purpose `RECONCILIATION_REPORT`,
   classification C2), scanned by ClamAV, referenced by an `audit.evidence_records` row carrying the same hash, and
   parsed only after the scan is CLEAN. Parsed lines are immutable except their match status.

3. **Source authority.** Evidence ranks, weakest first: internal state < provider acknowledgement < provider
   transaction evidence < provider settlement evidence < bank statement evidence. Manual investigation evidence is
   recorded with the resolution that relied on it, never as a source. A provider acknowledgement or transaction line
   never establishes settlement; a settlement line never establishes campaign allocation; a bank credit is never
   allocated to anything by amount alone.

4. **Matching** is deterministic, explainable and versioned. A run covers one source, one currency and one window;
   at most one run per (source, currency) is active (unique index). The pure function `reconciliation.Match`
   (matcher `MATCHER_V1`) decides, for each line, `EXACT_MATCH` (our reference and the provider reference both agree),
   `RULE_BASED_MATCH` (one reference agrees, the other is absent, the booking date is inside the rule set's window),
   `AMBIGUOUS_MATCH` (more than one candidate, or the two references point at different records — never finalised
   automatically) or `NO_MATCH`. **Amount and currency are verified, never used to find a candidate.** Every match and
   every exception records the line, the subject, the rule set id and version, the matcher version and the compared
   keys. The tunable parameters (date window, settlement horizon, staleness and escalation ages) live in
   **maker-checker matching rule sets** (`recon.matching_rule_sets`: closed bounded keys, hashed, immutable, step-up);
   **none is seeded** — they encode settlement-timing assumptions nobody has confirmed — so matching fails closed
   (`NO_ACTIVE_RULE_SET`) until FINANCE approves one. A new rule set never rewrites an earlier decision.

5. **Exceptions.** Every difference is a stored `recon.exceptions` row with a type (MISSING_PAYMENT,
   MISSING_SETTLEMENT, MISSING_PAYOUT, MISSING_LEDGER_ENTRY, MISSING_BANK_CREDIT, AMOUNT_MISMATCH, CURRENCY_MISMATCH,
   FEE_MISMATCH, PAYOUT_MISMATCH, STATUS_MISMATCH, DUPLICATE_TRANSACTION, AMBIGUOUS_MATCH, UNKNOWN_PAYMENT_OUTCOME,
   UNKNOWN_PAYOUT_OUTCOME, UNMATCHED_BANK_ENTRY, POSSIBLE_DONOR_FUNDS_IN_OPERATING_ACCOUNT, SETTLEMENT_SHORT,
   SETTLEMENT_OVER, CONFLICTING_EVIDENCE, STALE_RECONCILIATION), a severity (LOW, MEDIUM, HIGH, CRITICAL — engineering
   defaults by type, not regulatory classifications), a dedupe key (later runs bump `occurrences`) and a guarded
   lifecycle OPEN → ASSIGNED → INVESTIGATING ⇄ AWAITING_EVIDENCE → PENDING_APPROVAL → RESOLVED → CLOSED, with
   RESOLVED → REOPENED (late evidence) and a SYSTEM-only close when a later run finds the condition cleared (timing
   types only, never while an approval is pending). History is append-only (`recon.exception_events`). These names
   replace ADR-030 §4's (mapping in docs/stage-17/exceptions.md). Due dates are set by the assigning staff member; no
   SLA is configured or invented (as ADR-046).

6. **Resolutions are maker-checker.** A resolution (`recon.resolutions`) is proposed by one FINANCE member with a
   justification and evidence references, and approved by another — never the proposer or a linked account (Go, CHECK
   and an `app.actor_identities` trigger) — with step-up; the approval must match the proposal hash and happen before
   expiry (24 h); approving executes it, idempotently. Types: EXPLAINED_NO_ACTION, PROVIDER_CORRECTION_CONFIRMED,
   ESCALATED_TO_CASE, MANUAL_MATCH, PROVIDER_OUTCOME_APPLIED and LEDGER_ADJUSTMENT. A LEDGER_ADJUSTMENT creates a
   `ledger_adjustments` request through `ledger.RequestAdjustment` when proposed and posts it through
   `ledger.ApproveAdjustment` when approved: named rules only (`SETTLEMENT_DISCREPANCY_RESOLVED`, `ADJUSTMENT`,
   `WRITE_OFF`), balanced lines, one currency, the ledger's own maker-checker; posted journals are never edited.
   `ProductionAdjustmentsEnabled = false`: in a PRODUCTION ledger execution is refused until accounting rules, legal
   review, provider dependencies, owner authorisation and operational controls exist.

7. **Unknown outcomes.** A statement line for an UNKNOWN payment or payout opens UNKNOWN_PAYMENT_OUTCOME /
   UNKNOWN_PAYOUT_OUTCOME; nothing is applied automatically, because Stage 17 statements are staff-supplied TEST files.
   A PROVIDER_OUTCOME_APPLIED resolution (maker-checker, evidence) calls `payments.ApplyReconciliationOutcome` /
   `payouts.ApplyReconciliationOutcome`, which feed the owning module's **own** state machine with source `RECON`
   (already permitted by both modules' SQL source rules) and record the evidence: payments through `apply()` (provider
   evidence log, contradiction detection), payouts through `applyStatus` (its reservation transitions, journals and
   exceptions). Reconciliation never writes a payment, payout or reservation row and never resubmits anything.

8. **No automatic settlement postings in Stage 17.** `SettlementPostingEnabled = false`. A match records its ledger
   status — `POSTED` (the subject's journal exists, e.g. `payout:{id}:completed`; linked by id with a composite FK
   into `ledger.ledger_transactions`), `NOT_POSTED_TEST_PAYMENT` (test payments are never posted, ADR-042),
   `MISSING` (an exception) or `NOT_APPLICABLE`. `SETTLEMENT_MATCHED` / `SETTLEMENT_DISCREPANCY` postings wait for
   the accounting review and a PRODUCTION ledger; when enabled they post first through `ledger.PostOnce` with the key
   `settlement:{batch}:{subject}` and then record the match (idempotent replay), because the two pools cannot share a
   transaction. This amends ADR-030 §5 ("posts in the same tx as the match row").

9. **Bank statements (Model A).** A bank statement source covers FundZim's own operating account only. Bank lines are
   matched against fee-remittance lines of provider settlement reports by reference, then amount and currency. An
   unmatched credit or debit is UNMATCHED_BANK_ENTRY; a bank line whose reference names a FundZim payment is
   POSSIBLE_DONOR_FUNDS_IN_OPERATING_ACCOUNT (CRITICAL: Model A forbids donor funds reaching FundZim). The account
   identifier is never stored: only a keyed hash and the last four characters.

10. **Workers.** A dedicated River queue `reconciliation`: an outbox consumer parses an import when its file is
    scanned CLEAN (plus a sweeper), a periodic matcher, an ageing job (staleness, escalation, resolution expiry, stale
    run reaping) and per-job heartbeats. Runs are reaped from RUNNING to FAILED when their heartbeat stops; replay is
    an explicit staff action (reason, audited) that starts a new run — matches are unique per active line and
    exceptions deduplicate, so a replay never duplicates anything and never posts.

11. **Payout retry follow-up (KI-S17-01).** The Stage 15 budget (KI-S15-19) is verified fixed. A gap remains: a
    `RATE_LIMITED` CREATE_PAYOUT — which `psp` classifies as refused before processing (`RetryAfterBackoff`) — was
    neither re-dispatched nor counted against the budget, so the payout sat SUBMITTED and was status-polled
    indefinitely. The attempts guard, the dispatcher, the poller and the budget counter now treat `RATE_LIMITED`
    exactly like `DEFINITELY_NOT_SENT` (same reference, budgeted, jittered backoff, SUBMISSION_RETRIES_EXHAUSTED halt).
    An outcome that may have reached the provider is still never resubmitted.

## Consequences

### Positive
- One place where FundZim records, provider evidence, bank evidence and ledger journals meet, with every decision
  stored, explained and auditable; payouts and payments keep sole authority over their own states.
- The production path is shaped (sources, authority, rule sets, posting keys) without inventing a provider format,
  a schedule or an accounting rule.

### Negative / costs
- Until a provider is selected nothing real can be reconciled; EC-20 (payout settlement freshness) stays NOT_PERFORMED
  for TEST payouts and BLOCKED for production.
- Matching does nothing until FINANCE approves a rule set (fail closed by design).
- A canonical format means a real provider's files need a dedicated parser (and possibly a converter) later.

### Follow-up work
- Provider selection (ADR-040 successor) → provider parsers, PRODUCTION sources, settlement postings after the
  accounting review (KI-S10-02), EC-20 wired to reconciliation freshness, Gate B.

## Alternatives considered

| Alternative | Why rejected |
|---|---|
| Invent a plausible provider CSV layout | Forbidden (PCR-007); would silently encode wrong assumptions |
| Post `SETTLEMENT_MATCHED` for sandbox payments | Test payments have no capture journal (ADR-042); would create balances that mean nothing |
| Apply statement outcomes to UNKNOWN payments/payouts automatically | Stage 17 statements are staff-supplied TEST files: a single person could move money states; maker-checker required |
| Seed a default matching rule set | Encodes a settlement schedule nobody confirmed; fail closed instead |
| Keep reconciliation inside each module | ADR-030 rejected it: cross-module matching needs payments, payouts, fees and ledger together |

## Security implications

Statement files may contain personal and financial data: private bucket, ClamAV scan before parsing, C2 evidence,
size and line limits, strict parsing (no formula evaluation, no spreadsheet libraries), bank account numbers never
stored, CSV exports neutralise formula prefixes, reports bounded, step-up and audited. FINANCE-only permissions; support
roles get nothing.

## Financial implications

No balance changes in Stage 17 except maker-checker ledger adjustments in the TEST ledger. Currencies are never
combined or converted; a currency difference is an exception, never a conversion.

## Related

ADR-013, ADR-014, ADR-018, ADR-030, ADR-040, ADR-042, ADR-043, ADR-044, ADR-046, ADR-047;
docs/stage-17/reconciliation-architecture.md.
