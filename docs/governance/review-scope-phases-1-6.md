# Independent review scope: remediation Phases 1–6

**Status: PREPARED FOR REVIEW. NOT REVIEWED.** No independent reviewer exists (D-01a). Claude Code wrote this document
and is the author of every change it lists. Nothing here is a review, an approval or an acceptance. How to review, and what
counts as a review: `independent-review-process.md`.

Verified 2026-10-27 against `dfaa53b` (head of `remediation/integrity-status`; CI run 40476468316, 8/8).

## 1. The scope decision to make first (owner, D-15)

A merge of the candidate into `main` carries **everything since `a552de2`**, not only the remediation work:

| Range | Commits | Files changed | Content | Independently reviewed? |
|---|---|---|---|---|
| `a552de2..6a65d70` | 91 | 1 543 (≈354 000 lines added) | Stages 1–20 | **no** |
| `6a65d70..dfaa53b` | 24 | Phases 1–6 below | Remediation Phases 1–6 | **no** |
| `a552de2..dfaa53b` | 115 | 1 688 (≈374 000 lines added) | the whole merge | **no** |

A review of Phases 1–6 alone therefore **does not satisfy** integration gate 4 (`controlled-integration-plan.md`), which asks
for the range `a552de2..<candidate>`. The owner must decide one of the following before the reviewer starts. Claude Code
recommends none of them on the owner's behalf:

- **A.** Review the whole range (per-stage PRs make this tractable: `controlled-integration-plan.md` §3 step 2).
- **B.** Review Phases 1–6 now and Stages 1–20 later. Then integration of `main` stays BLOCKED until B's second part is
  done, or the owner records a scoped acceptance of the unreviewed range, with the risk owned in writing.
- **C.** Another division the reviewer proposes.

This document covers Phases 1–6. The per-path requirements of `independent-review-process.md` §2 apply to any range.

## 2. Phases 1–6 at a glance

Production paths exclude tests and documents. Line counts are from `git diff --shortstat`.

| Phase | Range (base..head) | Commits | Production files changed | Migration | Branch |
|---|---|---|---|---|---|
| 1 — reconciliation coverage sweeps (KI-S19-09; ADR-052) | `6a65d70..c8fb8c6` | 9 | 15 (+1 211 / −138) | `20261025090000` (**applied to the shared development database**, F-R1-07) | `remediation/reconciliation-completeness` |
| 2 — release governance (per-project image tags, required-check guard, RC manifest) | `c8fb8c6..be9cfb6` | 2 | 4 (+156 / −7) | — | `remediation/release-governance` |
| 3 — reconciliation defect closure F-R2-01/02/03, F-R3-01 (ADR-052 Am. 1) | `be9cfb6..1e167b1` | 4 | 12 (+622 / −95) | `20261026120000` (not applied anywhere retained) | `remediation/reconciliation-corrections` |
| 4 — KI-R1-07 determinism; checkpoint integrity (ADR-052 Am. 2) | `1e167b1..854e824` | 4 | 11 (+934 / −24) | `20261027090000` (not applied anywhere retained) | `remediation/checkpoint-integrity` |
| 5 — governance gate (documents; CI Playwright install split) | `854e824..dfa91b6` | 2 | 1 (`.github/workflows/ci.yml`, +19 / −2) | — | `remediation/governance-gate` |
| 6 — integrity status F-R5-01/02; compatibility F-R5-03 (ADR-052 Am. 3) | `dfa91b6..dfaa53b` | 3 | 12 (+366 / −38) | `20261028090000` (not applied anywhere retained) | `remediation/integrity-status` |

The history is linear: each branch head is an ancestor of the next, and `main` is an ancestor of all of them. Review each
range as a separate diff (`git diff <base>..<head>`); together they are exactly `6a65d70..dfaa53b`.

## 3. Production files per phase

| Phase | Files |
|---|---|
| 1 | `internal/platform/reconsweep/{sweep,sqlstore}.go`; `internal/{payouts,fees,ledger}/{reconcile,ops}.go`; `internal/fees/fees.go`; `internal/ledger/ledger.go`; `internal/payments/facts.go`; `internal/payouts/destinations.go`; `internal/app/ledger.go`; `.github/workflows/ci.yml`; `migrations/20261025090000_reconciliation_coverage.sql` |
| 2 | `compose.yaml`; `.github/workflows/ci.yml`; `scripts/release/rc-manifest.py`; `scripts/dr/exercise.sh` |
| 3 | `internal/platform/reconsweep/{health,sqlstore,sweep}.go`; `internal/platform/opsview/opsview.go`; `internal/{payouts,fees,ledger}/{reconcile,ops}.go`; `internal/operations/security.go`; `migrations/20261026120000_reconciliation_sweep_attempts.sql` |
| 4 | `internal/platform/reconsweep/{integrity,health,sqlstore,sweep}.go`; `internal/{payouts,fees,ledger}/reconcile.go`; `internal/payments/facts.go`; `internal/app/ledger.go`; `internal/operations/security.go`; `migrations/20261027090000_reconciliation_sweep_evidence.sql` |
| 5 | `.github/workflows/ci.yml` |
| 6 | `internal/platform/reconsweep/{integrity,health,sqlstore,sweep}.go`; `internal/{payouts,fees,ledger}/ops.go`; `internal/operations/security.go`; `apps/web/src/components/admin/operations/ops.tsx`; `apps/web/src/lib/operations/{normalise,types}.ts`; `migrations/20261028090000_reconciliation_integrity_status.sql` |

Tests and evidence live beside each phase's documents (§5).

## 4. Claims the reviewer should test, not take on trust

Each claim names where it is enforced and where the author's evidence is. "Verify" means read the code or SQL and, where
possible, re-run the named test.

| # | Claim | Enforced in | Author's evidence |
|---|---|---|---|
| C1 | A sweep checkpoint never moves backwards and its counters never decrease | `app.reconciliation_sweep_guard()` (latest copy in `20261028090000`) | `TestReconciliationCoverageCheckpointGuards` |
| C2 | A checkpoint position change without matching batch evidence is refused at commit, for every role | `app.reconciliation_sweep_evidence_check()` (`20261027090000`), deferred constraint triggers | `TestReconciliationIntegrityDatabaseRefusesUnsupportedMoves` |
| C3 | A record the sweep cannot judge yet is deferred, never counted as examined; the deferral set is carried exactly | `reconsweep.Run`; cycle-start trigger | `internal/platform/reconsweep` unit tests; `TestReconciliationCorrections*` |
| C4 | The independent verifier detects a walk that skips a record, an early cycle completion and a forged but self-consistent advance | `reconsweep.Verify` | `TestVerifyDetects*`; `TestReconciliationIntegrityDetectsAForgedConsistentAdvance` |
| C5 | **Limit (documented, not fixed):** evidence that lists real keys which were never examined is indistinguishable from a real examination | — | review packet D3 |
| C6 | Every verification outcome is recorded; a failure never leaves an earlier VERIFIED as the latest outcome; an older check never overwrites a newer one | `reconsweep.Verify`, `SQLStore.RecordIntegrity` | `TestVerifyRecords*`; `TestReconciliationIntegrityStatus*` |
| C7 | A verification (successful or failed) changes no checkpoint position, payout, attempt or ledger transaction | `reconsweep` has no such call; attempt-only guard set | `TestReconciliationIntegrityStatusFailureAndRecovery` (fingerprints) |
| C8 | Health and the integrity metric are never HEALTHY by default; a stale or failed verification raises an in-app alert | `reconsweep/health.go`; `operations/security.go` | `TestIntegrityState`; `TestSecurityFindingsReconciliation*` |
| C9 | Older code on the Phase 4/6 schema fails closed (stops advancing); Phase 6 code fails closed on older schemas; Phase 4 code works on the Phase 6 schema | migrations, statements | `TestReconciliationMigrationCompatibility` (opt-in) |
| C10 | Reconciliation never sends a provider instruction; a failed lookup is a deferral | detector `examine` functions | `TestReconciliationCorrectionsPayoutLookupFailure`, `…UnknownPayoutLookupFailure` |
| C11 | Payout approval separation of duties holds while the worker runs (KI-R1-07 determinism) | payouts approval path; `holdEligibilityDecisions` test barrier | `TestPayoutDecisionsBarrierStopsASubmissionInTheWindow`; 20× repeat (Phase 4) |
| C12 | A disposable compose project cannot replace the development project's images | `compose.yaml` per-project tags | `internal/deploytest` |
| C13 | The required checks equal the CI job names, and `main` CI is never cancelled | `.github/workflows/ci.yml` | `TestWorkflowRequiredChecks` |
| C14 | No financial gate was turned on | Go constants and DB CHECKs | `internal/archtest/financial_gates_test.go`; `TestSecurityFinancialGatesInDatabase` |

Thresholds in C8 (1 h staleness, 5 min skew, stall and deferral ages) are **engineering defaults, not approved targets**
(D-06, D-10). The reviewer should judge whether the defaults are safe, not whether they are the right service level.

## 5. Where the author's evidence is

| Phase | Documents | Evidence (with `SHA256SUMS`) | Last full integration run | CI |
|---|---|---|---|---|
| 1 | `docs/remediation/phase-1/` | `phase-1/evidence/` | `phase-1/test-evidence.md` | 40279994161 on `c8fb8c6`, 8/8 (earlier 40211164419 on `9d16090`: 7/8, KI-R1-10, fixed in `07e5965`) |
| 2 | `docs/remediation/phase-2/` | `phase-2/evidence/` | `phase-2/test-evidence.md` | 40293847614 on `be9cfb6`, 8/8 |
| 3 | `docs/remediation/phase-3/` | `phase-3/evidence/` | `phase-3/test-evidence.md` | 40310152518 on `1e167b1`, 8/8 (earlier runs on the branch failed on KI-R1-07, fixed in Phase 4) |
| 4 | `docs/remediation/phase-4/` | `phase-4/evidence/` | 253/0/7 on `f7d1cf7` (`fundzim-r8`) | 40358498063 on `854e824`, 8/8 |
| 5 | `docs/governance/` (no `docs/remediation/phase-5/` folder) | — | none (no code change) | 40461811832 (`78b9908`), 40464432937 (`dfa91b6`), 8/8 |
| 6 | `docs/remediation/phase-6/` | `phase-6/evidence/` | 255/0/8 on `b9496c3` (`fundzim-r10`) | 40472554403 (`b9496c3`), 40476468316 (`dfaa53b`), 8/8 |

Verify the checksums with `cd <evidence dir> && sha256sum -c SHA256SUMS`. A result is evidence only for the commit it names.

## 6. Re-running the tests (reviewer)

Run against a **disposable** compose project, never the shared development database (`CLAUDE.md`, Stage 20 rules):

```bash
# from a clean checkout of the commit under review
export PATH="$HOME/.local/go/bin:$PATH"
go test -count=1 ./internal/platform/reconsweep/ ./internal/operations/ ./internal/deploytest/ ./internal/archtest/

docker compose down                                   # the development project, WITHOUT -v
GIT_COMMIT=$(git rev-parse HEAD) docker compose -p fundzim-review up -d --build --wait
set -a; . ./.env; set +a
FUNDZIM_IT_MIGRATION_COMPAT=1 go test -tags integration -count=1 -v \
  -run 'TestReconciliation|TestPayoutDecisionsBarrier|TestSecurityFindings|TestSecurityFinancialGates|TestMigrationsApplied' ./tests/integration/
docker compose -p fundzim-review down -v              # the disposable project only
docker compose up -d --no-build --wait                # restore the development project
```

The disposable project applies all migrations to its own fresh volumes. Its `fundzim-migrate` job must not run against the
shared database: check `docker compose -p fundzim-review ps` before running tests.

## 7. Recording the review

Use the template in `independent-review-process.md` §5, one record per range or per phase, in `docs/governance/reviews/`
through a reviewed pull request. Findings are fixed in new commits on top of the reviewed head; never rewrite history.
