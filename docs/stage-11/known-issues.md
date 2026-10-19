# Stage 11 — Known issues

| ID | Issue | Impact | Status |
|---|---|---|---|
| KI-S11-01 | Acceptance of Stages 1–11 not recorded; Stage 9 PARTIAL; Stage 10 PASS WITH CONDITIONS (recommendation) | Isolated engineering only | OPEN — owner |
| KI-S11-02 | **No payout provider** (ADR-040): no real payout API, rails, limits, fees, settlement times, cancellation, idempotency semantics or statements | Production payout execution BLOCKED | BLOCKED |
| KI-S11-03 | **Production available funds BLOCKED**: settlement reconciliation NOT_PERFORMED (PCR-007/016); campaign_payable is reachable only by the TEST_ONLY fixture | No real withdrawable balance exists | BLOCKED (by design) |
| KI-S11-04 | No hold registry (`risk.holds` not built): DISPUTE_, RECOVERY_, ACCOUNT_ and PAYOUT_HOLD types do not exist; EC-12 uses campaign status, ledger account status and destination suspension; provider outages defer (EC-18) | Holds must come with Stage 13/14 | OPEN |
| KI-S11-05 | No sanctions/PEP screening provider (KI-S5-04, LR-009): EC-15 passes only TEST payouts | Production blocker | BLOCKED |
| KI-S11-06 | No payout limit is configured (LR-030): every payout is REVIEW + DUAL; no time-based approval or request expiry | Approver workload | OPEN — owner/LR-030 |
| KI-S11-07 | Third-party / institution payees and staff destination overrides not implemented (LR-024) | Payouts only to the owner (SELF campaigns) or the campaign's own verified beneficiary | OPEN — LEGAL_REVIEW_REQUIRED |
| KI-S11-08 | Chargeback/dispute exposure not modelled (no dispute source; LR-020); EC-14 NOT_APPLICABLE | Production blocker | OPEN — LEGAL_REVIEW_REQUIRED |
| KI-S11-09 | A real adapter needs the C3 account identifier; no decryption path for disbursement is designed (the simulator gets the masked reference only) | Needed with a provider | OPEN |
| KI-S11-10 | Payout cancellation after submission unsupported (simulator NOT_SUPPORTED; PCR-014) | — | OPEN — provider |
| KI-S11-11 | Payout fees not modelled (`PAYOUT_FEE` unused; PD-23, PCR-013) | — | DEFERRED (Stage 12) |
| KI-S11-12 | Late completion after FAILED raises a SEV1 exception only; the correcting journal (`payout:{id}:late_completion`, Stage 1 §5.3) is not implemented | Manual incident handling | OPEN |
| KI-S11-13 | Development-database residue from integration tests: PROVIDER_UNAVAILABLE scenario payouts stay SUBMITTED (retried within the budget, then halted with a SUBMISSION_RETRIES_EXHAUSTED exception — KI-S11-20), WORKER_RESTART payouts stay UNKNOWN (reconciliation flags them), and the perf run leaves ~850 cancelled TEST payouts with their journals | Local only (CI databases are fresh) | ACCEPTED |
| KI-S11-14 | Eligibility evaluation p50 ≈ 125 ms (provider health computed per request, many limit lookups) | Performance | OPEN |
| KI-S11-15 | A reconciliation run scans at most 5 000 payouts (no paging) | Must page before production | OPEN |
| KI-S11-16 | Organisation-member separation of duties is enforced in Go only | Defence in depth gap | OPEN |
| KI-S11-17 | Organisation-owned campaign payouts are exercised by unit tests only (integration tests use an individual owner) | Coverage | OPEN |
| KI-S11-18 | `payouts.request.user` = 10/hour is an operational starting value, not a configured limit record | — | ACCEPTED |
| KI-S11-19 | Payout reversal amounts come from the provider's report; a partial return is recorded as REVERSED with `reversed_amount_minor` (Stage 1 §6) | — | ACCEPTED |
| KI-S11-20 | (Found in Stage 15, KI-S15-19.) Same-reference resubmission after DEFINITELY_NOT_SENT outcomes had no effective limit: `recordObservation` rescheduled with a capped, unjittered backoff and opened one PROVIDER_UNAVAILABLE exception at 5 attempts, but `DispatchDue` kept selecting the payout, so it retried every 10 minutes indefinitely (720 attempts on one development payout; none could have reached the provider) | Unbounded provider calls; noisy development database | **FIXED** (Stage 15 finalisation): configurable budget `PAYOUTS_SUBMIT_RETRY_BUDGET` (default 5) with exponential backoff and ±20 % jitter (`PAYOUTS_SUBMIT_RETRY_BASE` / `_MAX`); when spent, a SEV2 `SUBMISSION_RETRIES_EXHAUSTED` exception halts automatic submission, `DispatchDue` skips the payout and the attempts guard refuses CREATE_PAYOUT (migration `20261019090000`); payouts already over budget are halted on their next dispatch without another call; resolving the exception (operator, audited) grants a fresh budget. The payout stays SUBMITTED with its reservation and in-transit journal; UNKNOWN outcomes are still never resubmitted |

Stage 9 (KI-S9-01..16) and Stage 10 (KI-S10-01..17) known issues remain as defined in their stage documents.
