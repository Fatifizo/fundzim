# Remediation phase 6 — governance verification, integrity-check reliability and integration gate: final report

**GOVERNANCE GATE: BLOCKED.**
- F-R5-01 and F-R5-02: **FIXED on the branch, NOT REVIEWED.**
- F-R5-03: **ANALYSED AND TESTED; procedural control open.**
- Controlled integration: **BLOCKED.**
- Production launch: **BLOCKED.**

## 1. Scope and identifiers

| Item | Value |
|---|---|
| Starting commit | `dfa91b6` (`remediation/governance-gate`, clean; CI run 40464432937 success 8/8) |
| Branch | `remediation/integrity-status` (new), pushed. `main` untouched (`a552de2`) |
| Commits | `019b245` (code, migration, tests), `b9496c3` (Down fix, test fix: **final code commit**), then the documentation commit |
| CI on the final code commit | run **40472554403: success, 8/8** (seen via the API) |

## 2. Governance gate (first step, as instructed)

Verified 2026-10-27 through the unauthenticated public API (`evidence/governance-api-check.txt`):
- `main` `protected: false`;
- rulesets `[]`; branch rules `[]`;
- 0 pull requests ever;
- no CODEOWNERS;
- no review records.

Collaborators and admin bypass cannot be seen without a token. **No GitHub setting was changed.**

**Result: BLOCKED.**
- Branch protection is not active.
- No independent reviewer exists.

Following the brief, this phase therefore did no merging, integration, deployment or migration of shared databases. All
work happened on a separate branch, awaiting independent review.

## 3. What was done

1. **F-R5-01 and F-R5-02.** The integrity status:
   - records every outcome with a sanitised code;
   - keeps the last success apart from the latest outcome;
   - keeps the latest failure after recovery and counts consecutive failures;
   - orders by start time;
   - survives cycle boundaries;
   - derives a state (NOT_RUN, VERIFIED, LATEST_FAILED, STALE, INCONSISTENT).

   It also has its own metric (never HEALTHY), a HIGH in-app alert for STALE, a portal display and an OpenAPI
   description. Design: `integrity-status-design.md`. ADR-052 Amendment 3.
2. **F-R5-03.** The compatibility matrix, the rollout order and the rollback are in `migration-compatibility.md`. They are
   proven by a scratch-database test that runs the Phase 3 and Phase 4 code's own statements at each schema level.
3. **Migration `20261028090000`.** It is additive: nullable or defaulted columns, a widened CHECK, an error-code CHECK and
   the extended attempt-only guard. Its Down is development-only, uses NOT VALID for immutable history, and is tested.
   **No applied migration was edited.**
4. **Regression on disposable stacks.** See §5.
5. **Storage review.** The Phase 5 projection stands. The checkpoint-row term was added: a 6.5 MB/day bound, about
   0.3 MB/day measured-typical. No retention period was chosen.
6. **Review evidence.**
   - The documents in this folder: design, compatibility, findings, register, test evidence and preserved logs.
   - They extend the governance review packet (`docs/governance/reconciliation-integrity-review-packet.md`) for a future
     independent reviewer.
   - Claude Code is the author; nothing here is a review.

## 4. Safety confirmations

- A failed verification never shows as a current success:
  - the latest outcome replaces VERIFIED;
  - the metric value is the age of the last **success**;
  - the state is never VERIFIED after a failure.
- A failed verification changes no checkpoint position, payout, attempt or ledger transaction. This is tested by
  fingerprinting.
- The staleness threshold (1 h) and the skew (5 min) are **engineering defaults that need owner approval**
  (D-06, D-10). No SLO was invented.
- No metric is HEALTHY merely because a process runs.
- A later success does not erase an earlier failure: `integrity_last_failure(_at)` keeps it, and alerts record
  CONDITION_CLEARED in their history.
- No test could start a real payment or payout: LOCAL_SIMULATOR only, and every gate is off.
- No force-push or history rewrite. No merge, PR, tag, release or deployment. `main` is unmodified.
- The shared development database is still at `20261025090000`: no pending migration was applied and no data was reset.
- No credentials or sensitive data are in the documents or evidence (secret scan clean).

## 5. Tests (details in `test-evidence.md`)

| Check | Result |
|---|---|
| Go race (container), `b9496c3` | exit 0, 40 packages, 0 failures |
| Web unit tests | 580/580; lint and typecheck clean |
| Targeted integration, `019b245` (`fundzim-r9`) | 19 pass, **2 FAIL** → F-R6-01, F-R6-02 fixed → 2/2 pass |
| Full integration, `b9496c3` (`fundzim-r10`) | **255 pass, 0 fail, 8 skip** (opt-ins), plus `TestRunningAPI` pass |
| Coverage volume harness | pass |
| CI 40472554403 | success 8/8 |

## 6. Final decision table

| Area | Status | Basis |
|---|---|---|
| Branch protection | **BLOCKED** | `protected: false`, no rulesets or rules (API, 2026-10-27). Specification only (`docs/governance/main-branch-protection.md`) |
| Independent reviewer | **BLOCKED** | None named (D-01a). No pull request, no CODEOWNERS |
| F-R5-01 | **FIXED ON THE BRANCH — NOT REVIEWED** | `019b245`/`b9496c3`; unit and integration tests; CI 8/8 |
| F-R5-02 | **FIXED ON THE BRANCH — NOT REVIEWED** | idem. Threshold is an engineering default pending D-06/D-10 |
| F-R5-03 | **ANALYSED AND TESTED — procedural control OPEN** | `migration-compatibility.md`; compatibility test passes. Old code on the Phase 4 schema fails closed by design |
| Reconciliation regression tests | **PASS** (on `b9496c3`, disposable `fundzim-r10`) | full suite 255/0/8; volume harness; CI 8/8 |
| Migration safety | **PASS WITH CONDITIONS** for review: additive, tested up/down/up, compatible with Phase 4 code. **BLOCKED** for application to any shared or retained database | Conditions: independent review, backup and restore drill, owner authorisation, the rollout order in `migration-compatibility.md` §3 |
| Independent review | **NOT REVIEWED / BLOCKED** | No reviewer. Author's documents prepared |
| Controlled integration | **BLOCKED** | Branch protection and independent review missing |
| Production launch | **BLOCKED** | Stage 20 blockers B-01…B-17 open; owner decisions open; no authorisation. Fixing technical findings does not authorise launch |

## 7. Exact next actions

**Owner or administrator (external; Claude Code cannot do these):**
1. D-01a: name an independent human reviewer (not the account `Fatifizo`) and grant access.
2. D-01: apply `docs/governance/main-branch-protection.md` §5 and verify it with §7. The required check names are the 8 CI
   job names.
3. The reviewer reviews Phases 1–6:
   - the review packet;
   - this folder;
   - the diffs `c8fb8c6..b9496c3` along the linear history.

   They record the outcome with the template in `docs/governance/independent-review-process.md`.
4. Decide:
   - D-06: the integrity freshness target, replacing the 1 h default;
   - D-10: alert routing;
   - D-05, D-12;
   - LR-012, with counsel.

**Repository work (only after steps 1–3):**
1. Address review findings on the branch, as new commits only.
2. Open the integration pull request per `docs/governance/controlled-integration-plan.md`. The branch history is linear:
   `remediation/integrity-status` contains every earlier remediation branch.
3. Only with owner authorisation, after a verified backup: deploy code and migrations together in the order of
   `migration-compatibility.md` §3. That covers `20261026120000`, `20261027090000` and `20261028090000` together.
