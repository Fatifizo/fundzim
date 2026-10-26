# Remediation phase 3 — baseline (read 2026-10-26, before any change)

| Item | Observed |
|---|---|
| Branch / HEAD | `remediation/release-governance` at `be9cfb6`, equal to origin, working tree clean |
| Phase 2 commits | `efa7b62` (code: per-project image tags, required-check guard, RC manifest), `be9cfb6` (documents) — verified, as reported |
| Phase 1 head | `c8fb8c6` (ancestor) |
| `main` | `a552de2`, `protected: false` (public API) — unchanged; no ruleset |
| CI | run 40290159925 on `efa7b62`: success 8/8; run **40293847614 on `be9cfb6`: success 8/8** (latest) |
| Branch for this phase | `remediation/reconciliation-corrections` — did not exist locally or remotely; created from `be9cfb6` |
| Reconciliation implementation | `internal/platform/reconsweep` (engine, SQL store), detectors in `internal/payouts`, `internal/fees`, `internal/ledger`, `internal/payments` (facts); migration `20261025090000`; ADR-052 **Proposed** |
| Test conventions | unit tests per package (`go test -race`); integration tests `-tags integration` against a compose stack, each detector in its own `it.*` scope; disposable compose projects for runs that create data (Stage 20 rule) |
| F-R2-01 / F-R2-02 / F-R2-03 | OPEN (phase 2 reconciliation-review.md §2); root causes confirmed in code before the change (findings-and-corrections.md) |
| Development stack | project `fundzim`, images `fundzim/api:fundzim` = `sha256:adfb403d…`, `fundzim/web:fundzim` = `sha256:9c4e1ccd…`; database at migration `20261025090000` |
