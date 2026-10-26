# Remediation phase 4 — final report

**Result: PASS WITH CONDITIONS** for the engineering phase. **Production launch: BLOCKED. Integration: BLOCKED (governance).**

## 1. Baseline

- Branch `remediation/reconciliation-corrections` at `1e167b1`, equal to origin, clean tree.
- `f85192d` (Phase 3 code) is an ancestor.
- CI run 40310152518 on `1e167b1`: success 8/8.
- `main` = `a552de2`, unprotected.
- New branch `remediation/checkpoint-integrity` from `1e167b1`. It did not exist before.

## 2. Root cause of KI-R1-07

The compose worker submits APPROVED payouts every 2 seconds. `SubmitApproved` records the pre-submission eligibility
decision, then moves the payout to SUBMITTED in the same transaction. The test keeps an approved payout APPROVED from approval
to cancellation (suspension, reinstatement, re-approval, cancellation) without any coordination. A worker pass in that window
submits it first, and the suspension then correctly leaves the submitted payout alone ("SUBMITTED round 1", CI run
40305574851).

The cause is scheduling order between two processes with no synchronisation barrier. Business behaviour is correct.

## 3. Correction and evidence of determinism

- **The barrier.** A SHARE lock on `app.payout_eligibility_decisions`, held from approval to cancellation. No submission can
  pass its decision insert. Blocked attempts are terminated: exactly the direct waiters of the test's lock that are running
  that insert, observed through `pg_blocking_pids` as superuser. Nothing of an attempt commits.
- **What is unchanged.** Every original assertion is kept. Added: the payout was never submitted (no SUBMITTED event, no
  CREATE_PAYOUT attempt).
- **Deterministic reproduction.** `TestPayoutSubmissionCommittedBeforeDestinationSuspension`.
- **Deterministic proof of interception.** `TestPayoutDecisionsBarrierStopsASubmissionInTheWindow`. Its log shows an attempt
  terminated at the decision insert, no traces left, and the approval invalidated.
- **Repeatability.** 20/20 consecutive runs against the live worker on `fundzim-r8`. In those 20 runs the worker never entered
  a window (0 intercepted); the deterministic test above is what exercises the interception.
- **Two defects found and fixed on the way:**
  - the test was not re-runnable: a shared-fixture staff link, and step-up rate limits;
  - the barrier's observer could not see other roles' query text.

  See test-evidence.md §3.

## 4. Checkpoint-integrity design and threat assumptions

- **Preventive.** Evidence per advance (range, keys, deferrals) is written in the same transaction. Deferred constraint
  triggers refuse any checkpoint position change without matching evidence, and any new cycle that does not carry the
  previous deferrals. This applies to every role, including the module's own.
- **Detective.** `reconsweep.Verify` after every sweep re-reads the scope with an independent set query (modification times)
  and checks the chain, counts, deferrals, skipped records and early completion. Results: VERIFIED, INCONSISTENT, INCOMPLETE or
  UNVERIFIABLE on the checkpoint. INCONSISTENT becomes INTEGRITY_FAILURE and a CRITICAL in-app alert.
- **Threats in scope:** engine and detector defects, stray or unauthorised position changes, evidence damage.
- **Out of scope:** a deliberate forger who writes evidence listing real keys never examined.

Details: checkpoint-integrity-design.md.

## 5. What the control detects and what it cannot

| Detects or refuses | Cannot |
|---|---|
| unevidenced forward jumps, mismatched evidence, deferrals changed outside batches, wrong cycle starts (refused) | detect forged evidence of real keys |
| skipped stable records (RECORD_SKIPPED) | flag records modified within the 5-minute margin before their batch (they are examined next cycle) |
| early completion | verify cycles that predate the evidence (UNVERIFIABLE) |
| chain gaps, duplicates, count mismatches, missing completion | guarantee VERIFIED for very large cycles within the 1-minute budget (it reports INCOMPLETE instead) |

## 6. Migration and isolation

- `20261027090000_reconciliation_sweep_evidence.sql` (additive): three append-only evidence tables, integrity columns, two
  trigger functions, constraint triggers, guard extension. Down drops only these objects (development only).
- It was applied only to disposable databases (`fundzim-r6/r7/r8`) and to the scratch `fundzim_perf_r6`.
- Round trip (Down to 0 and Up) passed.
- The shared development database's highest version is still `20261025090000` (verified after every run).
- Development images were unchanged throughout.

## 7. Tests and counts

| Test | Commit / environment | Result |
|---|---|---|
| Go `-race` | `30eb045` | 40/40 packages |
| KI-R1-07 repeat | `f7d1cf7` / `fundzim-r8` | 20/20 |
| Targeted | `f7d1cf7` / `fundzim-r8` | 10/10 |
| Full integration | `f7d1cf7` / `fundzim-r8` | **253 pass / 0 fail / 7 skip** (opt-in) |
| Coverage volume test | `f7d1cf7` / `fundzim-r8` | pass |
| Migration round trip | `f7d1cf7` / `fundzim-r8` | pass |
| Ledger volume harness, 20 000 and 100 000 journals | `30eb045` / `fundzim_perf_r6` | pass, integrity VERIFIED |

Earlier failing runs and their causes: test-evidence.md §3. Web tests were not run locally (no web change).

## 8. CI

| Commit | Run | Result |
|---|---|---|
| `f7d1cf7` (final code) | **40353861155** | **cancelled, not green.** 7/8 success, including Integration with 253 pass / 0 fail / 7 skip in CI. `Web end-to-end (Playwright)` exceeded its 20-minute limit because `playwright install --with-deps` took 12 min 47 s (environmental; no web code changed in this phase) |
| documentation head | — | reported in the completion message after the push. Its code is identical to `f7d1cf7`. Not claimed here |

A re-run of the cancelled job needs repository credentials Claude Code does not have.

## 9. Performance

- Integrity verification adds no measurable cycle time (19.7 s and 1 m 40.7 s, against 19.4 s and 1 m 46 s in Phase 1).
- Standalone verification: 69 ms at 20 000 journals, 297 ms at 100 000.
- Range re-reads are index scans.
- No blocking locks are taken.
- Evidence grows linearly; retention is undecided.
- Payout and fee volume was not measured.

No production capacity is claimed. Details: performance.md.

## 10. Open findings and owner decisions

- **Open:** the F-R4-01 residual (the forger), evidence retention (LR-012), verification cost at very large cycles (D-05),
  payout and fee load volume.
- **Owner decisions:** D-01 and D-01a (integration blocked); D-05, D-06, D-10, D-12, D-13; authorisation before migrating the
  development database.

None is decided: there is no authoritative record (remediation-register.md §3).

## 11. Confirmations

- No merge, rebase onto `main`, push to `main`, tag, release or deployment.
- No real donation, payment, payout or provider instruction: simulators only, `LOCAL_SIMULATOR`.
- No posted ledger entry was touched.
- No safeguard was weakened.
- No formal acceptance was recorded or claimed.
- No repository setting was changed.

## 12. Recommendation

**PASS WITH CONDITIONS.** KI-R1-07 is deterministic, and the checkpoint-integrity control is implemented and tested against
the specified interruption and advancement cases. Phase 1 and Phase 3 regressions and the financial regressions pass.
Isolation is proven. Conditions:
1. CI fully green on the branch head with this code (the final code commit's run was cancelled by an environmental timeout);
2. D-01 and D-01a, with independent review, before any integration;
3. D-05, D-06, D-10 and LR-012 before production volumes.

Next phase: governed integration (handover). This report does not authorise launch.
