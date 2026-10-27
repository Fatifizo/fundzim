# Remediation register — governance gate (2026-10-27)

This register supersedes `docs/remediation/phase-4/remediation-register.md` for the rows below. Earlier registers are kept.
Nothing is closed by documentation alone.

| ID | Severity | Finding | Status | Evidence | Disposition |
|---|---|---|---|---|---|
| F-R5-01 | LOW–MEDIUM | A failing integrity verification leaves the previous status on the checkpoint (a possibly stale VERIFIED) and is not counted as a failed attempt; it shows only as a job error | **OPEN** (author pre-review; not reviewed) | review packet §6 A-1; `reconsweep/integrity.go`, module `Reconcile` wiring | Proposed fix: record the failure on the checkpoint and age-check `integrity_checked_at` in Health. Needs independent review before implementation |
| F-R5-02 | LOW | Health ignores INCOMPLETE and UNVERIFIABLE, so a verifier that never completes at volume raises no signal | **OPEN** | packet A-2 | Map persistent INCOMPLETE to DEGRADED; threshold via D-06/D-10 |
| F-R5-03 | MEDIUM (deployment) | Migration `20261027090000` under pre-Phase-3 code refuses every sweep advance, so all sweeps fail | **OPEN (procedural control)** | packet D4 prerequisite 1 | Code and migrations are deployed together; recorded in the integration plan §3 step 9 |
| F-R5-04 | LOW (documentation) | The Phase 4 storage estimate "≈40 MB per detector per day" was overstated; measured ≈10 B per key and budget-bound ≈10–19 MB per ledger detector per day | **CORRECTED** in `owner-decision-register.md` | Phase 4 perf evidence | Phase 4 document kept as written |
| F-R5-05 | LOW (CI) | Web e2e job close to its 20-minute limit; the browser and system-package install took 686 to 767 s in the two latest runs (19–34 s before) | **MITIGATED for diagnosis; root cause external and unconfirmed** | `ci-reliability.md` | Install split into two steps and failure artifacts kept; timeout unchanged; caching not added |
| D-01 / D-01a | — | `main` unprotected; no independent reviewer | **OPEN** | `repository-baseline.md` §2 | Owner and administrator action |
| Phase 1–4 integrity review | — | Not independently reviewed | **NOT REVIEWED** | review packet | Needs D-01a |
| D-05, D-06, D-10, D-12, D-13, LR-012 | — | Owner, legal and security decisions | **OPEN** | `owner-decision-register.md` | External |
| KI-R1-07, F-R4-01 (Phase 4) | — | — | FIXED on the branch, pending review | Phase 4 test evidence | unchanged |
