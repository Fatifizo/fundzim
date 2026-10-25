# Stage 20 — Certification evidence matrix

Evidence kinds: **EXEC** executed in Stage 20 · **CI** remote CI run on the named commit · **PRIOR** executed in an
earlier stage (not repeated) · **SRC** source inspection only · **DOC** document review only · **NONE** no evidence.
"Code" = candidate code `16429d2` (identical to the head `2015978` under `internal/` and `migrations/`). ISO = the
disposable compose project `fundzim-s20` (clean volumes). Owner/specialist names the role that must act.

| ID | Requirement | Source | Implementation | Test / artifact | Kind | Commit / run | Result | Evidence | Limitation | Owner | Blocks launch |
|---|---|---|---|---|---|---|---|---|---|---|---|
| CR-01 | Balanced double entry | LEDGER.md L1/L2/L9 | `internal/ledger` + deferred trigger | integrity checks, `TestLedger*`, `TestFinancialLedger*` | EXEC (ISO) + CI | `2015978`; 40034115480 | PASS | evidence/stage-20/isolated-integration-results.txt | — | — | no |
| CR-02 | Ledger immutability | L5, ADR-042 | grants, triggers | `TestSecurityRuntimeRolesCannotRewriteFinancialHistory` | EXEC (ISO) + CI | same | PASS | same | superuser can still disable triggers (KI-S18-05) | — | no |
| CR-03 | Idempotent financial operations | CLAUDE.md rule 7 | unique keys, inbox/outbox | concurrent duplicate/conflict tests | EXEC (ISO) + CI | same | PASS | same | — | — | no |
| CR-04 | Payment state machine | transaction-lifecycle.md | `payments.apply`, SQL guard | `TestPaymentEdgesMatchMigration`, `TestPayments*` | EXEC (ISO) + CI | same | PASS | same | simulator only | — | no |
| CR-05 | Payout state machine and reservations | payout-lifecycle.md, ADR-043 | guarded machines, CHECKs | `TestPayout*`, `TestFinancialReservationInvariantsInTheDatabase` | EXEC (ISO) + CI | same | PASS | same | simulator only | — | no |
| CR-06 | Maker-checker separation | ADR-017 | Go + DB + actor_identities | `TestFinancialPayoutConcurrentApprovals`, fee/ledger/recon maker-checker tests | EXEC (ISO) + CI | same | PASS | same | — | — | no |
| CR-07 | Unknown outcome ≠ failure; no resubmission | CLAUDE.md rule 9 | UNKNOWN handling, attempts guard | `TestPayoutUnknownOutcome…`, `TestPaymentsTimeoutIsUnknownNeverFailed` | EXEC (ISO) + CI | same | PASS | same | — | — | no |
| CR-08 | Bounded polling (KI-S18-22) | ADR-051 | `payments.poll` | `TestFinancialPaymentPoll*` | EXEC (ISO) + CI | same | PASS | same | values provisional (KI-S19-04) | owner + provider | **yes** (values) |
| CR-09 | Deadlock-free ledger posting (F-S19-01) | ADR-051 | `lockAndCheck` | `TestFinancialLedgerOppositeOrderPostingsDoNotDeadlock` | EXEC (ISO) + CI | same | PASS | same | — | — | no |
| CR-10 | Deadlock-free payout attempts (F-S19-12) | ADR-051 | `startAttempt` / `recordObservation` | `TestFinancialPayoutConcurrentStatusQueries`; burst 0 deadlocks | EXEC (ISO) | same | PASS | burst-c16-5min.md | — | — | no |
| CR-11 | Reconciliation correctness (KI-S18-25) | ADR-051 | `reconcileFresh` | window, connection-loss, refusal tests | EXEC (ISO) + CI | same | PASS | same | — | — | no |
| CR-12 | **Reconciliation coverage of every record** | ADR-049/051 | windowed scans (5 000 / 5 000 / 10 000 every 15 min) | code inspection | SRC | `16429d2` | **FAIL** | financial-certification.md §5 | no watermark, no re-detection | owner (volume target), tech lead | **yes** |
| CR-13 | Audit integrity | ADR-019/050 | hash chain, MAC checkpoints | tamper, interruption tests; restore drill chain check | EXEC (ISO) | same | PASS WITH CONDITIONS | restore-drill.json | not anchored externally | owner (risk) | decision |
| CR-14 | Financial gates off | Stage 18 contract | 11 constants, gate CHECKs | `TestFinancialGateConstantsStayOff`, `TestSecurityFinancialGatesInDatabase` | EXEC (ISO) + CI | same | PASS | same | — | — | no |
| CR-15 | Worker crash recovery | ADR-043 | Recover, River rescue | worker-kill exercise | PRIOR (Stage 19, dev stack) | `16429d2` | PASS WITH CONDITIONS | docs/stage-19/fault-injection.md | kill not mid-call (KI-S19-07) | — | no |
| CR-16 | No duplicate effects under concurrency | brief §6 | all of the above | burst c=16: 880 payouts, 880 donations, 0 duplicates; Stage 19: 11 763 + 11 763 | EXEC (ISO) + PRIOR | same | PASS | burst-c16-5min.md, evidence/stage-19/load-run-*.md | not a capacity claim | — | no |
| CR-17 | Provider integration | ADR-040 | sandbox simulator only | code inspection | SRC | same | **BLOCKED** | provider-readiness.md | no provider | owner | **yes** |
| CR-18 | Provider semantics (66 PCR) | provider-questions.md | — | — | DOC | — | **BLOCKED** (66 OPEN) | — | — | owner + provider | **yes** |
| CR-19 | Legal P0 items (31) | open-legal-questions.md §5 | fail-closed gates | — | DOC | — | **BLOCKED** (none reviewed by counsel) | legal-regulatory-privacy-readiness.md | — | counsel, owner | **yes** |
| CR-20 | Privacy: notices, retention, data-subject requests, licence | LR-010/012/057/074, KI-S18-13/14 | retention registry blocks invention; no DSR workflow | — | SRC + DOC | — | **BLOCKED** | same | — | counsel, DPO, owner | **yes** |
| CR-21 | Accounting model reviewed | KI-S10-02 | TEST chart and rules | — | DOC | — | **BLOCKED** | accounting-readiness.md | — | accountant | **yes** |
| CR-22 | Phishing-resistant staff MFA | F-S18-12 | none | code search | SRC | same | **OPEN** | security-certification.md | — | owner (or risk acceptance) | **yes** |
| CR-23 | MFA reset workflow | F-S18-03 | none | code search | SRC | same | **OPEN** | same | — | owner | **yes** |
| CR-24 | Break-glass workflow and tagging | F-S18-04 | DB rows only | code inspection | SRC | same | **OPEN** | same | — | owner | **yes** |
| CR-25 | Internal endpoints protected | F-S18-05 | network-only | code inspection | SRC | same | OPEN (residual) | same | needs real network evidence | SRE | **yes** |
| CR-26 | KMS/HSM and key lifecycle | KI-S18-02/03 | secretstore, rotation overlap | — | SRC | same | **BLOCKED** | same | — | owner | **yes** |
| CR-27 | Container vulnerabilities | KI-S18-10 | images | Trivy 0.75.0 re-scan | EXEC | images built from `16429d2` code | PASS WITH CONDITIONS (web: 1 CRITICAL, 56 HIGH, 0 fixable) | security-certification.md §5 | exposure not analysed | owner (risk) | decision |
| CR-28 | Independent penetration test | KI-S18-11 | — | — | NONE | — | **NOT_VERIFIED** | — | — | owner | **yes** |
| CR-29 | Branch protection | DEVELOPMENT.md §5 | — | `GET …/branches/main` | EXEC | 2026-10-25 | **FAIL** (F-S20-01) | preflight.md | — | repo owner | **yes** |
| CR-30 | Signed provenance | KI-S18-08 | unsigned record | ci.yml | SRC | same | **FAIL** | ci-release-evidence.md | — | tech lead | **yes** |
| CR-31 | Release baseline and process | F-S20-02 | none | — | SRC | — | **FAIL** | same | — | owner, tech lead | **yes** |
| CR-32 | Encrypted, verified backups | Stage 18 | backup.sh, manifest MAC | backup + restore drill + failure drills | EXEC (ISO) | `2015978` + Stage 20 fix | PASS (after F-S20-07 fix) | restore-drill.json, failure-drills.json | no off-host copy | SRE | no |
| CR-33 | Approved RPO/RTO | KI-S18-12 | proposals | — | DOC | — | **BLOCKED** | infrastructure-dr-readiness.md | — | owner | **yes** |
| CR-34 | Provisioned production infrastructure, supervisor, HA decision | KI-S18-01/19/20 | none | — | DOC | — | **BLOCKED** | same | — | owner, SRE | **yes** |
| CR-35 | Alert delivery and on-call | KI-S18-15 | internal records only | — | SRC | — | **BLOCKED** | operations-readiness.md | — | owner | **yes** |
| CR-36 | Notification delivery capacity | F-S20-04 | 2 notification workers | burst-and-drain | EXEC (ISO) | `2015978` | measured (~19.7 jobs/s; recovers) — no target | notification-queue-burst.csv | no SLA | owner | **yes** (target needed) |
| CR-37 | Capacity targets met | brief §16 | — | baselines only | EXEC + PRIOR | — | **BLOCKED** (no target) | capacity-certification.md | dev host | owner | **yes** |
| CR-38 | Test-data isolation | brief §17 | disposable project | Stage 20 practice | EXEC | — | PASS WITH CONDITIONS (dev DB stays contaminated) | infrastructure-dr-readiness.md §4 | — | developer | no |
| CR-39 | Formal acceptance Stages 0–19 | register | — | — | DOC | — | **NOT_RECORDED** | owner-decision-pack.md | — | owner | **yes** |
| CR-40 | Launch authorisation | brief §23 | — | — | NONE | — | **NOT GRANTED** | — | — | owner | **yes** |
