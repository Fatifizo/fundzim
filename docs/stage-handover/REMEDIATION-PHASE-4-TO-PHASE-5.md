# Handover: remediation phase 4 → next remediation workstream

## Where things stand

- **Branch:** `remediation/checkpoint-integrity` (from `1e167b1`), pushed, **not merged**.
- **`main`:** `a552de2`, unprotected; no CODEOWNERS.
- **KI-R1-07:** fixed by a decisions barrier with proven interception.
- **Checkpoint integrity (F-R4-01):** evidence per advance; database constraint triggers refuse unevidenced checkpoint
  moves by any role; the independent verifier raises INTEGRITY_FAILURE and a CRITICAL in-app alert.
- **Documents:** `docs/remediation/phase-4/`.
- **Migrations:** `20261026120000` and `20261027090000` exist on the branch only. **The shared development database has
  neither** (highest version `20261025090000`). Do not `docker compose up --build` the development project from this branch
  without the owner's authorisation.
- **Development stack:** running on the pre-Phase-3 images (`fundzim/api:fundzim` = `sha256:adfb403d…`).
- **Acceptance:** NOT_RECORDED everywhere. Launch: NOT authorised. Every financial gate is off.

## Open items

1. **Governance (blocks integration).** D-01 protection, D-01a reviewer, independent review of Phases 1, 3 and 4
   (constraint-trigger SQL, verifier rules and margin, migrations).
2. **Undetectable residual.** A forger writing evidence that lists real keys never examined. Acknowledge, and rely on role
   separation.
3. **Retention.** Evidence retention (LR-012) and volumes (D-05) before production; storage arithmetic in performance.md.
4. **Thresholds and alerting.** D-06 and D-10 for thresholds and an alert destination; INTEGRITY_FAILURE alerts are in-app
   only.
5. **Payout and fee volume.** Not measured at load (only small-volume tests).

## Recommended next workstream

**Governed integration.**
1. Once D-01 and D-01a exist: one PR from this branch's head, 8/8 checks on the merge ref, independent review, merge commit,
   CI on `main`, `rc-manifest.py`, owner tag.
2. In parallel, owner decisions: D-05, D-06, D-10, LR-012.
3. Then decide whether to apply the two migrations to the development database.

No product stage starts automatically.
