# Remediation register — current status after Phase 2 (2026-10-26)

This register supersedes `docs/remediation/phase-1/remediation-register.md` as the **current** status. The Stage 20
registers (`docs/stage-20/production-launch-blockers.md`, `owner-decision-pack.md`) are the historical certification record and
are not edited.
- Nothing below is closed by documentation.
- No owner, legal, accounting, provider, security or operational approval exists in the repository.
- The one owner decision recorded is F-R1-07 (keep migration `20261025090000` on the development database, 2026-10-26),
  given in conversation and written into the Phase 1 known issues by Claude Code at the owner's instruction.

Status values: OPEN · IN_PROGRESS · ENGINEERING_DONE_PENDING_DECISION · BLOCKED_EXTERNAL · CLOSED (closure evidence present).

## 1. Launch blockers (Stage 20 B-01 … B-17)

| # | Blocker | Category | Status after Phase 2 | What changed in Phase 2 | Still required |
|---|---|---|---|---|---|
| B-01 | No payment or payout provider | payment-provider dependency | BLOCKED_EXTERNAL | — | D-02 selection ADR, contract, sandbox, conformance suite |
| B-02 | 31 P0 legal items | legal / regulatory approval | BLOCKED_EXTERNAL | — | counsel (D-03) |
| B-03 | Privacy (licence, DPO, notices, retention, DSR) | legal / regulatory approval | BLOCKED_EXTERNAL | — | counsel, DPO; DSR workflow after counsel |
| B-04 | Accounting review, fee model | accounting approval | BLOCKED_EXTERNAL | — | accountant (D-04) |
| B-05 | Reconciliation coverage | engineering defect + owner decision | **IN_PROGRESS** (was ENGINEERING_DONE_PENDING_DECISION) | independent review: engine sound; F-R2-01 and F-R2-02 (MEDIUM detection quality) and F-R2-03 (LOW liveness) found. Phase 2 does not fix them | fix F-R2-01/02 (or owner accepts them as conditions), D-06 coverage target, D-05 volumes, review, integration, green CI on the integrated commit |
| B-06 | Capacity targets | capacity decision | OPEN | — | D-05 |
| B-07 | Staff security conditions | security acceptance | OPEN | — | D-07 |
| B-08 | KMS / HSM | security acceptance | OPEN | — | D-07, KMS ADR |
| B-09 | Penetration test | security acceptance | BLOCKED_EXTERNAL | — | commission |
| B-10 | Web-image CVEs | security acceptance | OPEN | — (CI still reports them; fixable CRITICAL/HIGH would fail CI) | D-08 |
| B-11 | Infrastructure, RPO/RTO, DR | operational readiness | OPEN | — | D-09 |
| B-12 | Alerting, on-call, staffing | operational readiness | OPEN | F-R2-03 adds sweep-stall alerts to the list | D-10 |
| B-13 | Release governance: unprotected `main`, no baseline / tag / procedure, unsigned provenance | release governance + owner decision | **IN_PROGRESS** | protection proposal with exact UI and API steps and a verification method; dependency map and integration route; RC manifest script (exercised: positive and negative); required-check drift guard; `main` CI never cancelled; signing design (keyless attestation) | D-01 / D-01a apply protection **and verify** (API shows rules); integrate; tag a baseline with green CI; D-12 attestation |
| B-14 | Poll budget values | payment-provider dependency | OPEN | — | D-11 |
| B-15 | Concurrent index migrations | engineering | OPEN | — | procedure and rehearsal |
| B-16 | Stage acceptance | formal stage acceptance | OPEN | — (register unchanged: Stages 0–20 NOT_RECORDED; Phase 1 and Phase 2 not accepted) | D-13 |
| B-17 | Launch authorisation | formal stage acceptance / governance | OPEN | — | D-14 |

## 2. Release governance detail

| Item | Evidence | Status | Verification still required |
|---|---|---|---|
| Branch protection on `main` (F-S20-01) | API 2026-10-26: `protected: false`, rulesets `[]` | **OPEN, NOT APPLIED** | rules visible via `GET /rules/branches/main`; direct-push rejection |
| Integration strategy (F-S20-02) | phase-2/integration-plan.md (linear; conflict-free; single PR or per-stage PRs) | IN_PROGRESS (proposed) | owner D-01; PR with 8/8 on the merge ref and on `main` |
| Independent reviewer | none visible (collaborators need auth) | OPEN (new D-01a) | named reviewer; CODEOWNERS |
| Release baseline / tag | none | OPEN | tag on the merge commit after D-13 |
| RC manifest bound to commit and CI | `scripts/release/rc-manifest.py`; `evidence/rc-manifest-c8fb8c6.json` | ENGINEERING_DONE_PENDING_DECISION | used by the owner at baseline |
| Signed provenance (F-S20-06, KI-S18-08) | design only (release-candidate-procedure.md §4) | OPEN | D-12; a verified attestation (`gh attestation verify`) |
| Release / rollback procedure | release-candidate-procedure.md §3, §5; integration-plan.md §7 | IN_PROGRESS (documented, not rehearsed) | rehearsal on real infrastructure (B-11) |
| CI applies to the merged commit | ci-governance.md §2–4 | ENGINEERING_DONE_PENDING_DECISION | effective only once protection is applied |
| Dev / disposable isolation (F-R1-07) | per-project image tags, deploytest guard, proven by the `fundzim-r2` run | CLOSED for image isolation; host ports still shared (documented) | — |

## 3. Owner decision pack — current state (no decision below has been taken unless stated)

| # | Decision | State | New input from Phase 2 |
|---|---|---|---|
| D-01 | Branch protection and integration route | NOT DECIDED | exact settings and commands (branch-protection.md); route (integration-plan.md) |
| D-01a (new) | Independent reviewer(s) | NOT DECIDED | without one, a required review cannot be satisfied (GitHub forbids self-approval) |
| D-02 … D-04 | Providers, counsel, accounting | NOT DECIDED | — |
| D-05 / D-06 | Volumes; reconciliation coverage target | NOT DECIDED | the dev worker's first real cycles (reconciliation-review.md §3) |
| D-07 … D-11 | Security, CVEs, infrastructure, operations, poll values | NOT DECIDED | F-R2-03 adds sweep alerts to D-10 |
| D-12 | Signing, release and rollback | NOT DECIDED | keyless attestation recommended; the CI permission slot is already allowed by test |
| D-13 | Stage acceptance (0–20, Phase 1, Phase 2) | NOT DECIDED | — |
| D-14 | Launch authorisation | NOT DECIDED | — |
| F-R2 (new) | Fix F-R2-01/02 before integration, or accept them as conditions | NOT DECIDED | reconciliation-review.md §2 |
| F-R1-07 | Development database: keep migration `20261025090000` | **DECIDED by the owner, 2026-10-26: keep** | — |
