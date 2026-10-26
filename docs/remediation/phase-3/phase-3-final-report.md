# Remediation phase 3 — final report

**Result: PASS WITH CONDITIONS** for the engineering phase. **Production launch: BLOCKED.** Nothing was merged, deployed or
activated.

## 1. Executive summary

- **The two MEDIUM findings are fixed and proven.** Fee reconciliation now records every missing fee type and every distinct
  amount discrepancy. A failed provider status lookup can no longer count as a reconciled payout: the payout is deferred,
  visibly, and never inferred, changed or re-instructed.
- **The LOW finding is fixed for visibility.** A stalled, blocked or failing sweep is recorded on its checkpoint, classified,
  shown on the operations portal and raised as an in-app alert. There is no external delivery.
- **The engine no longer stalls on failures.** Failing or slow checks cannot stop the sweep: deferral retries and batches now
  respect the time budget, and the checkpoint still never passes an unexamined record.
- **A latent Stage 18 alert-scan race surfaced in CI and was fixed** with a deterministic regression test.
- **Test results on the final code commit `f85192d`:** the full integration suite passes on an isolated disposable stack
  (247/0/7).
- **Remote CI is 7/8** because of a known pre-existing test race (KI-R1-07). That race, protection, review and owner
  decisions are the conditions.

## 2. Repository baseline

`be9cfb6` (Phase 2 head; CI 40293847614 8/8) was verified local = remote with a clean tree. `main` = `a552de2`, unprotected. The
branch `remediation/reconciliation-corrections` was new. Details in preflight.md.

## 3. Root cause of F-R2-01

The fee dedupe key `kind:assessment:journal` had no check discriminator, so:
- all MISSING_FEE findings of a group collided on `MISSING_FEE:<first member>:`;
- two pairs of AMOUNT_MISMATCH comparisons collided as well (calculation vs provider report; journal lines vs posted).

The first recorded finding hid the rest until it was resolved.

The fix adds a `check` to the key for these two kinds only. Pre-fix OPEN references are matched by their stored content. There
is no data migration, and no accounting rule was invented: the expected types are still the calculation's own components.

## 4. Root cause of F-R2-02

There was no representation of "a required comparison could not be made", so an adapter error or any non-authoritative
lookup class was silently treated as examined. The fix:
- `judgeProvider` compares only ACCEPTED-with-status and NOT_FOUND answers; everything else is PROVIDER_UNVERIFIED, with the
  cause;
- the sweep defers such payouts; the reason, count, causes and sample go into the run report and audit event;
- internal mismatches are still recorded;
- payout state, reservations and attempts are never touched, and no CREATE or CANCEL path exists on it.

The engine bounds deferral retries (half the record budget, the time budget, round-robin) and stops batches at the time
budget, advancing the checkpoint only to the last examined key.

## 5. Resolution of F-R2-03

- Migration `20261026120000` records each run's outcome (time, stop reason, error, consecutive failures) on the RUNNING
  checkpoint. The guard accepts attempt-only updates.
- `Coverage.Health` distinguishes NOT_STARTED, PROGRESSING, PROGRESSING_SLOWLY, STALLED, BLOCKED_ON_DEFERRALS and FAILING,
  and the last cycle as COMPLETED_CLEAN or COMPLETED_WITH_DISCREPANCIES.
- Per-detector `*.reconciliation.sweep_health*` ops metrics feed the existing alert scan, which raises
  RECONCILIATION_SWEEP_FAILING, _STALLED and _BLOCKED (HIGH, in-app).
- PAYMENT_FACTS now runs even when PAYMENT_JOURNALS failed.
- The thresholds are labelled engineering defaults; production values are owner decisions D-06 and D-10.
- No operator is notified: no destination exists, and none was invented.

## 6. Code and migration changes

| Area | Files |
|---|---|
| Fees | `internal/fees/reconcile.go` |
| Payouts | `internal/payouts/{reconcile,ops}.go` |
| Engine | `internal/platform/reconsweep/{sweep,sqlstore,health}.go` |
| Ledger | `internal/ledger/{reconcile,ops}.go` |
| Operations | `internal/fees/ops.go`, `internal/operations/security.go`, `internal/platform/opsview/opsview.go` |
| API contract | `api/openapi/fundzim-v1.yaml` (`OpsMetricS14.state`) |
| Migration | `migrations/20261026120000_reconciliation_sweep_attempts.sql` (additive; applied only to disposable databases) |
| Design record | ADR-052 Amendment 1 (still Proposed) |

## 7. Regression tests and results

| Test | Commit / environment | Result |
|---|---|---|
| Unit and engine | `01ea615` | all pass, mutation-checked |
| Go `-race` | `01ea615` | 40/40 packages |
| Go `-race`, operations package | `f85192d` | pass |
| Targeted integration | `fundzim-r5`, `f85192d` | 5/5 |
| Scan-race regression test against the pre-fix code | `fundzim-r5` | fails as expected |
| Full integration suite | `fundzim-r5`, `f85192d` | 247 pass / 0 fail / 7 opt-in skip |
| Coverage volume test | `fundzim-r5`, `f85192d` | pass |
| Migration round trip | `fundzim-r5`, `f85192d` | pass |

Earlier failures, all explained in test-evidence.md §2:
- the payout test fixture amounts;
- the alert-scan race;
- KI-R1-07.

## 8. Financial-integrity evidence

- All Stage 19 regressions pass in the full disposable run (KI-S18-25, KI-S18-22, F-S19-01, F-S19-12), together with the
  ledger, payment, payout, fee, reconciliation, security/audit and tenant-isolation families.
- The financial-gate contract test passes.
- The payout lookup-failure tests prove:
  - no status, reservation or attempt changes;
  - no instruction;
  - no exception opened from a failure;
  - one genuine inconsistency recorded exactly once after recovery.
- No real payment or payout occurred. Simulators only; `APP_ENV=development`.

## 9. Historical coverage evidence

- The Phase 1 coverage tests pass (outside-window payouts and fees, ledger sweeps, concurrency and interruption, checkpoint
  guards).
- `TestReconciliationCoverageVolume` passes.
- The new engine tests show a 1 000-record cycle with 100 permanently failing checks completes, while every failing record
  is carried and retried round-robin.
- coverage-guarantees.md states the precise checkpoint guarantee and the remaining forward-move risk.

## 10. CI results and commit SHAs

| Commit | Run | Result |
|---|---|---|
| `01ea615` | 40300772129 | failure 7/8: fixture amounts and the alert-scan race, both fixed |
| `9ed4974` | 40305203774 | cancelled (superseded) |
| `f85192d` | 40305574851 | failure 7/8: KI-R1-07, pre-existing; Integration 246 pass / 1 fail / 7 skip; Go job with race, gosec and govulncheck success |
| documentation head | — | reported in the completion message after the push. Not claimed here |

`rc-manifest.py` REJECTS `f85192d`, as it must.

## 11. Updated remediation register

See remediation-register.md:
- F-R2-01, F-R2-02 and F-R2-03 are FIXED, pending review and integration;
- F-R3-01 is FIXED;
- KI-R1-07 and the checkpoint forward-move limit are OPEN.

## 12. Outstanding owner decisions

- D-01 and D-01a (protection, reviewer)
- D-06 (coverage target, which also sets the stall and slow thresholds)
- D-10 (alert delivery)
- D-05 (volumes)
- authorisation before migrating the development database
- acceptance of the checkpoint forward-move limit, or the detective control
- D-12, D-13, D-14, and D-02 … D-11

## 13. Remaining launch blockers

All 17 remain open. B-05 stays IN_PROGRESS: engineering is done on the branch, but review, integration, green CI on the
integrated commit and D-06 are still required. B-12 and B-13 are unchanged.

## 14. Engineering recommendation

**PASS WITH CONDITIONS.** The corrections meet every acceptance criterion with evidence from isolated environments. The
conditions before integration:
1. make KI-R1-07 deterministic, or have the reviewer accept a recorded re-run, so CI on the PR merge ref is 8/8;
2. D-01 and D-01a, with independent review of the reconciliation changes and migration `20261026120000`;
3. D-06 and D-10 set production thresholds and an alert destination.

This does not authorise launch.

## 15. Recommended next phase

Make KI-R1-07 deterministic (test only), then integrate under protection with a named reviewer: one PR, merge commit, CI on
`main`, manifest, owner-tagged baseline. See the handover.
