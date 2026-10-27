# Remediation register — phase 6 (2026-10-27)

This register supersedes `docs/governance/remediation-register.md` for the rows below. Earlier registers are kept.

"FIXED on the branch" means the change exists on `remediation/integrity-status` (`b9496c3`) with passing tests. It is
**not reviewed, not merged, not deployed and not accepted**. Nothing is closed by documentation alone, and nothing here is
closed by an independent review, because none exists.

| ID | Severity | Finding | Status | Evidence | Disposition |
|---|---|---|---|---|---|
| F-R5-01 | LOW–MEDIUM | A failed verification left a stale VERIFIED; no history; out-of-order recording | **FIXED on the branch — NOT REVIEWED** | `integrity-status-design.md`; `test-evidence.md` | Independent review (D-01a), then integration |
| F-R5-02 | LOW | INCOMPLETE/UNVERIFIABLE and repeated time-outs not surfaced | **FIXED on the branch — NOT REVIEWED** | idem | The 1 h staleness threshold is an engineering default; the owner sets the target (D-06) and routing (D-10) |
| F-R5-03 | MEDIUM (deployment) | Old code on the Phase 4 schema stops advancing | **ANALYSED AND TESTED; procedural control OPEN** | `migration-compatibility.md`; `TestReconciliationMigrationCompatibility` | Deploy code and migrations together, migration first, services stopped; needs authorisation |
| F-R5-04 | LOW (documentation) | Storage estimate | **REVIEWED**: the Phase 5 projection stands; checkpoint rows added (F-R6-05) | `findings-and-corrections.md` §3 | Retention LR-012 stays with the owner and counsel |
| F-R5-05 | LOW (CI) | Browser install slowdown | **WATCHED**: did not recur (run 40472554403) | `test-evidence.md` §4 | Keep watching |
| F-R6-01 | LOW | Down of `20261028090000` failed with pending trigger events | **FIXED** (`b9496c3`); never on a retained database | compatibility test | — |
| F-R6-02 | LOW (test) | Phase 4 killed-run test was data-dependent | **FIXED** (`b9496c3`) | full suite | — |
| F-R6-03 | LOW | Phase 4 recorded integrity at recording time (ordering) | **FIXED** with F-R5-01 | integration test | — |
| F-R6-04 | LOW (deployment) | A mixed Phase 4/6 fleet can show STALE transiently | **OPEN — procedural** | `migration-compatibility.md` §6 | Stop all services for the migration |
| F-R6-05 | LOW (documentation) | Checkpoint rows absent from the storage projection | **ASSESSED** (≤ 6.5 MB/day bound; ≈ 0.3 MB/day measured-typical) | `findings-and-corrections.md` §3 | — |
| F-R6-06 | INFO | INTEGRITY_UNVERIFIED until the first verification after deployment | **ACCEPTED as accurate** | design §8.2 | Operator note |
| D-01 | — | `main` unprotected | **OPEN** (verified 2026-10-27: `protected: false`, 0 rulesets, 0 rules) | `evidence/governance-api-check.txt` | Administrator |
| D-01a | — | No independent reviewer | **OPEN** | idem (0 pull requests, no CODEOWNERS) | Owner |
| Phases 1–6 review | — | Not independently reviewed | **NOT REVIEWED** | review packet + this phase's documents | Needs D-01a |
| D-05, D-06, D-10, D-12, D-13, LR-012 | — | Owner, legal and security decisions | **OPEN** | `docs/governance/owner-decision-register.md` | External |
| KI-R1-07, F-R4-01 | — | — | FIXED on earlier branches, pending review | Phase 4 | Unchanged |
