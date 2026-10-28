# Controlled-integration checklist (evidence-based)

**Overall: BLOCKED.** Verified 2026-10-27 against candidate `dfaa53b` (`remediation/integrity-status`). This checklist
replaces the "current state" column of `controlled-integration-plan.md` §2; the plan's sequence (§3) and rules (§4–§6)
still apply.

How to read it:
- An item is **MET** only with the evidence named. Documentation alone never meets an item.
- **NOT MET** means work exists but the condition is not satisfied.
- **BLOCKED** means it cannot be met without an input that Claude Code may not supply (a person, a setting, a decision).
- Claude Code may only update the "evidence" column with things it verified. It never marks a review, an approval or an
  owner decision as MET.

## A. Repository and GitHub (administrator)

| # | Item | Status | Evidence (2026-10-27) | Closed by |
|---|---|---|---|---|
| A1 | `main` protected per `main-branch-protection.md` §5, verified by §7 | **BLOCKED** | public API: `protected: false`; rulesets `[]`; branch rules `[]`; the protection endpoint needs a token (401). `evidence/governance-api-check-2026-10-27.txt` | administrator (D-01) |
| A2 | Required checks = the 8 CI job names | **MET in the repository; NOT ACTIVE on GitHub** | the spec names all 8 jobs of `.github/workflows/ci.yml` (checked by script); `TestWorkflowRequiredChecks` pins them | administrator applies (A1) |
| A3 | CODEOWNERS for financial, migration, workflow and release paths | **BLOCKED** | `.github/CODEOWNERS` absent (API 404). Names needed; never invented | owner (names), administrator |
| A4 | Admin bypass, collaborators and plan features known | **NOT VERIFIED** | not visible without a token | administrator |
| A5 | No force push or deletion of `main` since baseline | **MET (observed)** | `main` = `a552de2` in every check since Phase 2 | — |

## B. Review (people)

| # | Item | Status | Evidence | Closed by |
|---|---|---|---|---|
| B1 | A named independent reviewer, not the author account `Fatifizo` | **BLOCKED** | none named; no PR; no review records (`docs/governance/reviews/` absent) | owner (D-01a) |
| B2 | Review scope decided (whole range, or Phases 1–6 then Stages 1–20) | **BLOCKED** | `review-scope-phases-1-6.md` §1: the merge carries 115 commits, 91 of them Stages 1–20, none reviewed | owner (D-15) |
| B3 | Phases 1–6 reviewed, with records per `independent-review-process.md` §5 | **NOT REVIEWED** | review scope, claims C1–C14 and evidence index prepared (`review-scope-phases-1-6.md`) | reviewer |
| B4 | Stages 1–20 reviewed (or a written, owned scoped acceptance) | **NOT REVIEWED** | — | reviewer / owner |
| B5 | Financial and accounting review of posting, reservation and reconciliation semantics | **BLOCKED** | no financial reviewer (KI-S10-02 open) | owner |
| B6 | Security review of roles, secrets, supply chain | **BLOCKED** | no security reviewer (B-09 open) | owner |
| B7 | Review findings fixed in new commits, CI re-run on the new head | **N/A until B3** | — | author + reviewer |

## C. Candidate and CI (verifiable now)

| # | Item | Status | Evidence | Closed by |
|---|---|---|---|---|
| C1 | Candidate head fixed and recorded by the owner | **NOT MET** | proposed: `dfaa53b` (or the reviewed head after B7). Not recorded by the owner | owner |
| C2 | Linear history; candidate contains every stage and remediation branch; `main` is its ancestor | **MET** | `git merge-base --is-ancestor`: `a552de2` → … → `dfaa53b`, 115 commits, 0 merges; 26 stage and remediation branches on `origin` | — |
| C3 | 8/8 CI on the candidate head (push) | **MET for `dfaa53b`; NOT MET for the branch head `5fb1931`** | `dfaa53b`: run 40476468316, 8/8; code commit `b9496c3`: run 40472554403, 8/8. `5fb1931` (documents only): run 40508474321 **failure, 7/8**. Web e2e `staff-fees.spec.ts:50` [mobile] lost the "Draft version created." notice. Root cause G-02 (`review-preparation-report.md` §4). G-02 is fixed on branch `remediation/g02-fee-draft-notice` (`d83d1ac`; not reviewed): its CI run 40521729530 is 7/8 (the fee tests pass; one unrelated e2e failure, G-03). G-03 is fixed on branch `remediation/g03-e2e-socket-hangup` (`02aac0e`; not reviewed; CI run 40654287677 8/8) | reviewed G-02 and G-03 fixes, then a green run on the candidate head |
| C4 | 8/8 CI on the PR merge ref | **NOT MET** | no PR exists | after A1/B1 |
| C5 | Release-candidate manifest | **MET (eligible for review only)** | `rc-manifest.py dfaa53b --run 40476468316`: `ELIGIBLE_FOR_REVIEW`; built `BUILT_BY_CI`; provenance `UNSIGNED_NOT_VERIFIABLE`; authorisation `NOT_AUTHORISED`. `evidence/rc-manifest-dfaa53b-run-40476468316.json` | — |
| C6 | Working tree clean; candidate pushed | **MET** | `git status` clean; local = `origin/remediation/integrity-status` | — |

## D. Tests and evidence (author's evidence; the reviewer judges it)

| # | Item | Status | Evidence | Closed by |
|---|---|---|---|---|
| D1 | Full integration suite on the candidate code on a disposable project | **MET (author's run)** | 255 pass, 0 fail, 8 opt-in skips on `b9496c3` (`fundzim-r10`); `TestRunningAPI` pass. `docs/remediation/phase-6/test-evidence.md` | reviewer may re-run |
| D2 | Go race tests | **MET (author's run)** | `b9496c3`: exit 0, 40 packages | — |
| D3 | Reconciliation regression and volume | **MET (author's run) WITH LIMIT** | coverage volume harness pass (payouts 870, fees 501); ledger 20 000 and 100 000 journals (Phase 4). Payouts and fees not measured at volume | D-05 volumes; reviewer |
| D4 | Financial invariants and concurrency (`TestFinancial*`, non-opt-in) | **MET (author's run)** | in D1 | — |
| D5 | Opt-in load and worker-kill harnesses | **NOT RUN since Stage 19** | Stage 19 evidence only; no payment/payout/worker code changed since | reviewer decides |
| D6 | Evidence checksums intact | **MET** | `sha256sum -c SHA256SUMS` in each `docs/remediation/phase-*/evidence/` (verified 2026-10-27; see the governance report) | reviewer re-checks |

## E. Migrations and databases

| # | Item | Status | Evidence | Closed by |
|---|---|---|---|---|
| E1 | Migration sequence: 53 files, unique increasing timestamps, no applied migration edited | **MET (author's check)** | `controlled-integration-plan.md` §1; the four remediation migrations were each written in a single commit (`20261028090000`'s Down was corrected in `b9496c3` before any retained database held it) | reviewer |
| E2 | Remediation migrations reviewed: `20261025090000`, `20261026120000`, `20261027090000`, `20261028090000` | **NOT REVIEWED** | review packet D4/D5 | reviewer |
| E3 | Code/schema compatibility understood | **MET (author's analysis and test)** | `docs/remediation/phase-6/migration-compatibility.md`; `TestReconciliationMigrationCompatibility` pass | reviewer |
| E4 | Shared development database untouched by pending migrations | **MET (observed)** | at `20261025090000`; the running `07e5965` build expects the same; API and worker `/readyz` ok incl. the migrations check | — |
| E5 | Backup and restore drill before migrating any long-lived database | **NOT MET** | last drills Stage 18/20, on development | owner authorises; operator runs |
| E6 | Authorisation to migrate the shared development database (all three pending migrations together with their code) | **BLOCKED** | none | owner |

## F. Decisions and authorisation (owner)

| # | Item | Status | Evidence | Closed by |
|---|---|---|---|---|
| F1 | D-01, D-01a, D-15 | **OPEN** | `owner-decision-register.md` | owner, administrator |
| F2 | D-06 (freshness target; replaces engineering defaults), D-10 (alert routing) | **OPEN** | idem | owner |
| F3 | D-05, D-12, D-13, LR-012 | **OPEN** | idem | owner, counsel |
| F4 | Explicit owner authorisation of the merge | **BLOCKED** | none | owner |
| F5 | Separate authorisation for any deployment or live financial operation | **BLOCKED** | none; all financial gates off; launch BLOCKED (Stage 20 B-01…B-17) | owner |

## Order of closure

A1, B1, B2 → B3 (and B4/B5/B6 per B2) → B7 → C1 → PR → C4 → F4 → merge per `controlled-integration-plan.md` §3.
E5 and E6 are separate and come after the merge decision. F5 is not part of integration.
