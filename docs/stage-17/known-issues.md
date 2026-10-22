# Stage 17 — Known issues

| ID | Issue | Impact | Status |
|---|---|---|---|
| KI-S17-01 | Payout CREATE attempts classified RATE_LIMITED were neither re-dispatched nor counted against the KI-S15-19 retry budget; the payout sat SUBMITTED and was status-polled indefinitely without an exception | a stuck TEST payout, unbounded status queries | FIXED in Stage 17 (migration `20261022090100`, dispatcher, budget, poller; simulator scenario 13; IT) |
| KI-S17-02 | No real statement source: only FundZim's canonical format, staff uploads and simulator statements. No provider format, statement API or settlement schedule exists (ADR-040; PCR-007, PCR-016, PCR-045, PCR-051) | nothing real is reconciled; Gate B cannot lift | EXTERNAL_BLOCKER |
| KI-S17-03 | No settlement postings (`SettlementPostingEnabled = false`); payout eligibility EC-20 still reports settlement reconciliation NOT_PERFORMED (TEST) / BLOCKED (production) | available funds cannot be derived from settlement | BLOCKED (accounting review KI-S10-02, provider selection, ADR) |
| KI-S17-04 | Bank statement balances are stored but not reconciled against `asset:fundzim_operating_bank`; fee remittance postings (`FEE_REMITTANCE`) not made | bank balance drift would not be detected automatically | BLOCKED (accounting review) |
| KI-S17-05 | Operational thresholds are engineering defaults: worker DEGRADED after 30 min without success, stale run reaped after 10 min, PARSING sweep after 5 min, failed-import metric over 7 days, proposal expiry 24 h (operational-controls §3). Matching parameters are FINANCE's TEST rule set | not service levels | OPEN (owner to confirm with operations) |
| KI-S17-06 | No retention period for statement evidence, lines, matches, exceptions or resolutions; nothing is purged | storage growth | LEGAL_REVIEW_REQUIRED (LR-012, LR-076) |
| KI-S17-07 | Statement evidence for an UNKNOWN outcome always needs a second person, even when (later) the evidence is an authenticated provider API | slower resolution | ACCEPTED for Stage 17 (decide with a real provider) |
| KI-S17-08 | Refunds and chargebacks: no refund transaction table exists and refunds cannot execute (Stage 8); refund, chargeback and reversal lines are compared with the payment's status only | refund evidence is coarse | OPEN (when refunds execute) |
| KI-S17-09 | Campaign statements and regulatory report exports (ROADMAP) are not built: no format or obligation is defined | — | OPEN (legal/owner input) |
| KI-S17-10 | Payments' own reconciliation references other than the UNKNOWN-horizon kinds (amount mismatch, anomaly, late/duplicate success …) are not closed by reconciliation; they are cross-referenced by payment id | two places to look | OPEN |
| KI-S17-11 | A run's persistence is dominated by exception writes (row, history event, audit event, outbox event each): 100 000 lines with 10 000 exceptions took 71 s; matches and line statuses are batched | large first imports take minutes | OPEN (batch exception writes if field volumes need it) |
| KI-S17-12 | The development database's audit table holds 1.19 M rows (1.07 M `payments.provider.response_received` from the sandbox poller since Stage 9, ~120 000 a day); `audit.verify_chain` takes 36.6 s, so the two integration tests with a 30 s timeout fail locally (chain intact). CI uses a fresh database | local test flakiness; the per-poll audit volume will matter in production | OPEN (Stage 18: audit volume and verification strategy) |
| KI-S17-13 | Exception assignment in the UI asks for the staff member's id (no picker) | usability | OPEN |
| KI-S17-14 | Manual screen-reader audit of the reconciliation pages not performed; automated axe (WCAG 2.x A/AA serious and critical) on desktop and Pixel 7 | WCAG 2.2 AA not certified | OPEN |
| KI-S17-15 | Not exercised by tests: a database outage in the middle of a run and a ledger failure during an adjustment execution (code paths reasoned: rollback, reaper, APPROVED with `last_error` retried) | — | OPEN |
| KI-S17-16 | Stage 0–16 acceptance not recorded; no stage branch has a pull request or is merged into `main` | governance | EXTERNAL_BLOCKER (owner) |
| KI-S17-17 | GO-2026-6809 (crypto/x509) reachable on go1.27.2, found by CI govulncheck | — | FIXED (Go 1.27.3) |
| KI-S17-18 | After a proposal the page refresh removed the proposal form and, with it, the confirmation (found by the CI mobile run) | staff saw no confirmation | FIXED (the form stays mounted and keeps its notice; unit test, E2E asserts it) |
| KI-S17-19 | Stage 16 `TestDiscoveryEndToEnd` could hit the protective `global.ip` limit (40 per 2 s) with its own polling (CI: 429) | flaky integration run | FIXED (requests on the run's own address wait out a 429; the limit itself is still asserted on a separate address) |

## Earlier stages, revisited

| Item | Stage 17 finding |
|---|---|
| KI-S15-19 (payout resubmission loop) | verified fixed with data; residual RATE_LIMITED gap fixed (KI-S17-01) |
| Stage 16 real-stack discovery spec | was never in a Playwright project (never ran); wired in Stage 17 and passing |
| Stage 16 testing.md | said remote CI NOT_VERIFIED after the green run 39383009700; the acceptance register now records the run |
