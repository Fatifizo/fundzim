# Stage 20 — Owner decision pack

This pack prepares decisions only the owner (and the named specialists) can take. **No decision recorded here has
occurred.** Nothing in this repository records an acceptance, a risk acceptance or a launch authorisation; Claude Code
must not record one. Each decision must be recorded by the owner in `docs/governance/stage-acceptance-register.md`
(or the relevant register) with the decision, date, decider and evidence reference.

## 1. Stage acceptance (Stages 0–19)

All stages are integrated only on the stacked branch `stage-19/financial-concurrency` → `stage-20/production-certification`
(every stage branch is an ancestor; nothing is on `main`). Engineering recommendation as recorded in each stage's report.

| Stage | Engineering recommendation | Owner acceptance | Outstanding conditions (summary) | Merged to `main` | Eligible for formal acceptance? |
|---|---|---|---|---|---|
| 0–5 | see each stage report | NOT_RECORDED | earlier stages' conditions | no | yes — in order, from Stage 0 |
| 6 | PASS WITH CONDITIONS | NOT_RECORDED | stage report | no | after 0–5 |
| 7 | PASS WITH CONDITIONS | NOT_RECORDED | stage report | no | after 6 |
| 8 | PASS WITH CONDITIONS (sandbox only) | NOT_RECORDED | production BLOCKED | no | after 7 |
| 9 | PARTIAL / provider certification BLOCKED | NOT_RECORDED | no provider (ADR-040) | no | acceptance as PARTIAL only |
| 10 | TEST ledger only | NOT_RECORDED | KI-S10-02 accounting review | no | after 9 |
| 11 | TEST payouts only | NOT_RECORDED | provider, settlement evidence | no | after 10 |
| 12 | TEST fees only | NOT_RECORDED | fee model PD-01 | no | after 11 |
| 13 | TEST data | NOT_RECORDED | no screening provider | no | after 12 |
| 14 | TEST data | NOT_RECORDED | — | no | after 13 |
| 15 | simulators only | NOT_RECORDED | no messaging provider; consent LRs | no | after 14 |
| 16 | non-production | NOT_RECORDED | LR-023, LR-035, LR-090 | no | after 15 |
| 17 | TEST evidence only | NOT_RECORDED | provider statement formats; accounting review | no | after 16 |
| 18 | PASS WITH CONDITIONS; production BLOCKED | NOT_RECORDED | security conditions (F-S18-03/04/05/12), infra, KMS, pen test, RPO/RTO | no | after 17 |
| 19 | PASS WITH CONDITIONS; production BLOCKED | NOT_RECORDED | capacity targets, poll values, incremental reconciliation, worker sizing | no | after 18 |
| 20 | see stage-20-final-report.md | NOT_RECORDED | this pack | no | after 19 |

Accepting a stage *with conditions* means: the engineering work is accepted as the baseline for further work, the
listed conditions stay open and remain launch blockers where marked, and production stays blocked. It does not
authorise operation.

## 2. Decisions required before any production launch

| # | Decision | Evidence available | Outstanding risk | Consequence of deciding "accept with conditions" | Blocks launch until decided |
|---|---|---|---|---|---|
| D-01 | Configure branch protection on `main` and approve the integration PR route (F-S20-01, F-S20-02) | preflight.md | unreviewed changes to the baseline | none — prerequisite | yes |
| D-02 | Select payment and payout providers (supersede ADR-040); confirm Model A custody | provider-readiness.md | no live path exists | providers' PCR answers drive poll budget, statements, fees | yes |
| D-03 | Commission counsel on the 31 P0 legal items and privacy obligations (licence, DPO, notices, retention, DSR) | legal-regulatory-privacy-readiness.md | operating unlawfully | each resolution may change code (ADR) | yes |
| D-04 | Commission an accounting review (KI-S10-02) and decide the fee model (PD-01) | accounting-readiness.md | misstated records | posting-rule changes need new versions | yes |
| D-05 | Approve capacity, latency and volume targets (pilot and year one) | capacity-certification.md §3 | unknown fitness for load | drives audit-chain design, worker sizing, reconciliation windows | yes |
| D-06 | Decide reconciliation coverage requirement → incremental reconciliation design (KI-S19-09) | financial-certification.md §5 | undetected discrepancies outside windows | engineering work in a later stage | yes |
| D-07 | Security conditions: WebAuthn, MFA reset, break-glass workflow, internal endpoint protection — remediate or record an authorised risk acceptance; commission a penetration test; select KMS/HSM | security-certification.md | account takeover, privileged misuse | remediation work or documented acceptance | yes |
| D-08 | Risk decision on unfixed web-image CVEs (CVE-2023-45853 + 56 HIGH): accept with compensating controls or change the base image | security-certification.md §4–5 | exploitable library path (not analysed) | base-image change is engineering work | yes (decision) |
| D-09 | Approve RPO/RTO; decide single-node pilot vs HA; provision infrastructure with a health-based supervisor, off-host immutable backups, secret-manager backups | infrastructure-dr-readiness.md | data loss, long outage | infrastructure project | yes |
| D-10 | Name operators, on-call and alert destinations; approve incident response expectations and support staffing; set a notification delivery target | operations-readiness.md | incidents unnoticed | sizing and paging work | yes |
| D-11 | Approve the poll budget values (72 h / 500) or provider-specific values (KI-S19-04) | provider-readiness.md §3 | traffic or resolution delay | configuration change | yes |
| D-12 | Approve artifact signing and the release / rollback procedure (F-S20-06, F-S20-02) | ci-release-evidence.md | unverifiable artifacts | CI work | yes |
| D-13 | Formal acceptance of Stages 0–20 in order (§1) | each stage report | — | baseline for launch preparation | yes |
| D-14 | Production launch authorisation (only after D-01 … D-13 and a go/no-go record with named approvers, ROADMAP Stage 20) | this certification | — | — | yes |

## 3. Action required outside Claude Code

The owner records each decision in the register (decision, date, decider, evidence, accepted conditions), configures
repository settings (D-01), engages counsel, an accountant, providers and a penetration tester, and provisions
infrastructure. Claude Code stops before recording any of these.
