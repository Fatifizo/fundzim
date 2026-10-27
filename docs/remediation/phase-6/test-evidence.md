# Remediation phase 6 — tests executed and results

Every result below was executed in this phase. Logs are in `evidence/`, with `SHA256SUMS`. A test result is evidence for the
commit it names only.

## 0. Commits

| Commit | Content |
|---|---|
| `019b245` | code: integrity status (F-R5-01/02), migration `20261028090000`, metric, alert, portal, OpenAPI, ADR-052 Amendment 3; new tests |
| `b9496c3` | Down of `20261028090000` (F-R6-01); Phase 4 killed-run test reads its own row (F-R6-02). **The final code commit** |

Production Go code is identical in `019b245` and `b9496c3`. The later commit changes the migration's Down section and a
test.

## 1. Environment

- **Disposable compose projects.**
  - `fundzim-r9`: built from `019b245`; used for the targeted runs.
  - `fundzim-r10`: built from `b9496c3`; used for the full suite, the volume harness and `TestRunningAPI`.
- **Checked before running** (`evidence/isolation-*.txt`):
  - the project's own image tags `fundzim/{api,web}:<project>`;
  - 0 volumes before;
  - `APP_ENV=development`;
  - `PAYMENTS_` and `PAYOUTS_EXECUTION_ENVIRONMENT=LOCAL_SIMULATOR`;
  - 0 production secret references;
  - `/api/v1/version` reports the commit;
  - the schema at `20261028090000`;
  - 0 development containers running.
- **Removed afterwards** with `-v` (0 volumes left).
- **The development project** (`evidence/teardown-and-dev-restore.txt`):
  - stopped with `docker compose down` (no `-v`) and restarted with `--no-build`;
  - image IDs identical before and after;
  - `fundzim-migrate`: "no pending migrations";
  - highest applied migration still **`20261025090000`**. **None of `20261026120000`, `20261027090000` and
    `20261028090000` was applied to it.**
- **The compatibility test** created and dropped its own scratch database `fundzim_perf_compat_*` inside the disposable
  project.
- **No real payment or payout.** Only the local simulators exist, and the execution environment is LOCAL_SIMULATOR.

## 2. Results

| Run | Commit, environment | Result | Evidence |
|---|---|---|---|
| Go unit tests (host, `-count=1`) | working tree = `019b245` | pass (all packages) | — (re-run under race below) |
| Go race tests (`golang:1.27.3` container) | `b9496c3` | **exit 0, 40 packages ok, 0 failures** | `go-race-b9496c3.log.txt` |
| `go vet` (default, `integration`, `perf` tags); `gofmt -l` | `019b245`/`b9496c3` | clean | — |
| Web unit tests | `019b245` | **61 files, 580 tests pass** (incl. 2 new) | — |
| Web lint and typecheck | `019b245` | clean | — |
| OpenAPI lint (Redocly 2.54.3) | `019b245` | valid (2 pre-existing warnings) | — |
| `scripts/check-secrets.sh` | before each commit | no secrets found | — |
| Targeted integration (`TestReconciliationIntegrity*`, `…Corrections*`, `…CoverageCheckpointGuards`, `…MigrationCompatibility`, `TestOperations*`, `TestSecurityAlert*`) | `019b245`, `fundzim-r9` | **19 pass, 2 FAIL, 1 skip (perf opt-in)**. The failures were F-R6-01 (Down: pending trigger events) and F-R6-02 (data-dependent Phase 4 assertion) | `targeted-fundzim-r9-019b245*` |
| The two failed tests after the fix | fix (committed as `b9496c3`), `fundzim-r9` | **2/2 pass** | `targeted-fundzim-r9-fix*` |
| **Full integration suite** (`FUNDZIM_IT_PERF=1`, `FUNDZIM_IT_MIGRATION_COMPAT=1`) | `b9496c3`, `fundzim-r10` | **255 pass, 0 fail, 8 skip** (27.2 min) | `integration-fundzim-r10-b9496c3*` |
| `TestRunningAPI` (`FUNDZIM_IT_API_URL`; one of the 8 skips above, run separately as in Phase 4) | `b9496c3`, `fundzim-r10` | pass | `running-api-fundzim-r10-b9496c3.log.txt` |
| Reconciliation coverage volume harness (`FUNDZIM_IT_R1_VOLUME=1`) | `b9496c3`, `fundzim-r10` | pass. Payouts 870 in one run (3.5 s), fees 501 (3.6 s); 0 lock waiters | `coverage-volume-fundzim-r10-b9496c3.log.txt` |
| **CI run 40472554403** | `b9496c3` | **success, 8/8 jobs** (seen via the API) | `ci-run-40472554403-b9496c3.txt` |

The 7 remaining skips are opt-ins for the load and worker-kill harnesses, the replica profile, the web URL and stopping
Valkey, plus the volume harness, which ran separately. They are the same as in Phase 4. Phase 4's suite had 253 passes.
This phase adds three tests (`TestReconciliationIntegrityStatus*` ×2 and `TestReconciliationMigrationCompatibility`) and
ran `TestRunningAPI` separately: 253 + 3 = 255 + 1.

**Reconciliation regressions** (all in the full run, all pass):
- `TestReconciliationCoverage*`;
- `TestReconciliationCorrections*`;
- the Phase 4 `TestReconciliationIntegrity*` tests;
- `TestReconciliation*` (Stage 17);
- `TestFinancial*` (non-opt-in);
- `TestPayout*`, `TestFee*`, `TestLedger*`;
- `TestSecurity*`, `TestOperations*`;
- `TestMigrationsAppliedAndRepeatable`.

## 3. Observations on `fundzim-r10` (after the full suite)

`evidence/integrity-state-and-sizes-fundzim-r10-b9496c3.txt`:
- All four worker detectors (scope `default`, cycle 2) record VERIFIED, with `integrity_verified_at` equal to the start of
  the check and 0 failures. The worker verified after its own 15-minute runs.
- `integrity_detail`: about 460 B on average, 885 B at most. A checkpoint row is 550–850 B.
- At the worst case of 4 detectors × 96 completed cycles a day, that is ≈ 0.3 MB/day typical, against the 6.5 MB/day bound
  (`findings-and-corrections.md` §3).

## 4. CI timings (F-R5-05 watch)

Run 40472554403:
- web e2e 522 s (system dependencies 12 s, browser 9 s);
- real-stack e2e 892 s;
- integration 1 663 s.

The install slowdown of 2026-10-26 did not recur in this run or in Phase 5's two runs. One more data point, not proof of
stability.

## 5. Not run, and why

- The load (`TestFinancialLoad`) and worker-kill harnesses: no change to payment, payout or worker code paths; they need
  their dedicated setup.
- The ledger perf harness (`tests/perf/ledger`): the ledger engine and its sweeps are unchanged except for the integrity
  columns.
- No test was run against the shared development database.
