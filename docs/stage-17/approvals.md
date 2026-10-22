# Stage 17 — Maker-checker approvals

## 1. What needs two people

| Action | Maker | Checker | Step-up |
|---|---|---|---|
| Matching rule set | `reconciliation.rules.request` | `reconciliation.rules.approve` | checker |
| Resolution of an exception (every type) | `reconciliation.investigate` | `reconciliation.resolve` | checker |
| Ledger adjustment inside a resolution | the resolution's maker files the ledger request | the resolution's checker approves it in the ledger | checker |

Imports (`reconciliation.import`) and exports (`reconciliation.report.export`) need step-up but one person; on-demand runs
and replays (`reconciliation.run`) need a reason and are audited.

## 2. Separation of duties

The checker is never the maker or an account linked to the maker (Stage 5 `app.actor_identities`):

1. Go: `identities(checker)` contains the proposer → `SELF_DECISION_FORBIDDEN` (fails closed when the resolver errors);
2. CHECK: `decided_by <> proposed_by` (`ck_recon_resolutions_maker_checker`, `ck_recon_rule_sets_maker_checker`);
3. trigger `recon.checker_guard`: refuses a decision by any identity linked to the proposer.

The ledger enforces the same for its adjustment (`approved_by <> requested_by`, plus its own identity checks).

## 3. Replay and staleness

- The approval must carry the SHA-256 of the proposal the checker reviewed (`proposal_sha256`); the database requires
  `approved_sha256 = proposal_sha256` (`ck_recon_resolutions_approved`). A changed proposal → `PROPOSAL_CHANGED`.
- Proposals expire after 24 h (`ResolutionTTL`, CHECK `expires_at ≤ proposed_at + 24 h`); an expired proposal cannot be
  approved (`RESOLUTION_EXPIRED`) and the ageing job ends it.
- One live resolution per exception (unique index); a decision locks the row and requires PENDING_REVIEW, so two racing
  checkers produce exactly one approval (integration test `TestReconciliationConcurrency`).
- Execution happens once: APPROVED → EXECUTED under a row lock; the effect is keyed in the owning module; an execution
  that failed (owning module unavailable) stays APPROVED with `last_error` and is retried by the ageing job — never by
  approving again.

## 4. Stage 14 approval centre

Pending resolutions appear in the operations approval centre (`Makers` = proposer, link to the exception page); the
decision is taken only on the reconciliation route, which re-checks everything.
