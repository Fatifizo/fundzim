# Remediation phase 4 — integration readiness

**Integration remains BLOCKED** by governance: D-01 (`main` is unprotected; verified 2026-10-26), D-01a (no second reviewer)
and an independent review. Nothing was merged, rebased onto `main`, tagged or deployed.

| Item | State |
|---|---|
| Branch | `remediation/checkpoint-integrity` from `1e167b1` (Phase 3 head), pushed; linear, contains Stages 1–20 and Phases 1–4 |
| Final code commit | `f7d1cf7` (production code identical to `30eb045`; later commits change tests only) |
| Migrations | `20261026120000` (Phase 3) and `20261027090000` (Phase 4). Additive; round trip passed on throwaway databases; **never applied to the shared development database** |
| Self-review of the diff | `git diff 1e167b1..f7d1cf7`: reconsweep (evidence, integrity, health), module wiring (payouts, fees, ledger, payments facts, app adapter), the alert rule, the OpenAPI description, the migration, tests. No financial gate, posting rule, payout state machine, reservation, maker-checker or provider path changed. Archtest and the financial-gate contract pass |
| CI on the final code commit and on the documentation head | see the final report |
| Independent review | **not performed** (D-01a). Recommended scope: the constraint-trigger SQL, the verifier's rules and margin, the barrier test |

When D-01 and D-01a exist, the integration plan (phase-2 integration-plan.md, updated in Phase 3) applies with this branch's
head as the candidate:
1. one PR (or per-stage PRs in order);
2. 8/8 checks on the merge ref;
3. a merge commit;
4. CI on `main`;
5. `rc-manifest.py`;
6. the owner's tag.
