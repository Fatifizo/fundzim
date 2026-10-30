# Review brief for `emB99`: independent technical review of G-02 and G-03

> **Provenance.**
> - This is the repository owner's brief to the designated reviewer, added to the repository verbatim on 2026-10-30 by
>   Claude Code at the owner's request.
> - It is an instruction, not a review. No review has started.
> - The review record (`independent-review-decision-template.md`) is untouched and is to be completed by `emB99` only.
> - Companion documents: `reviewer-instructions-emb99.md` (where to look, commands, CI table) and
>   `independent-review-handover.md` (scope, commit ranges, checklist).
> - Note on §7: `reviewer-instructions-emb99.md` §10 suggests completing a **copy** of the template at
>   `docs/governance/reviews/<YYYY-MM-DD>-g02-g03-emB99.md`, which keeps the blank template reusable. Either way the
>   record belongs to the reviewer.

---

## 1. Review objective

Independently assess the implementation and evidence for:

- G-02 — Fee draft notice persistence.
- G-03 — Playwright mock and real-stack connection-race remediation.

The purpose is to determine whether both changes are technically correct, adequately tested, and safe to recommend for
integration.

You are expected to inspect the actual implementation, tests, commit history and CI evidence. Do not rely solely on the
implementation report.

## 2. Review scope

Repository: `Fatifizo/fundzim`

Branch: `remediation/g03-real-stack-connections`

Review the complete relevant history, including:

- G-02 implementation commits.
- G-03 mock-suite remediation.
- G-03 real-stack remediation.
- Associated regression tests.
- Import guards.
- Governance and evidence documentation.

Use the latest branch HEAD rather than assuming a previously reported commit is still current.

## 3. G-02 — Fee draft notice persistence

Inspect the fee-policy draft creation flow.

Verify:
- The success message survives the relevant form refresh or replacement.
- The form remains usable after a successful save.
- Failed saves do not show success messages.
- Duplicate submissions are handled correctly.
- Existing idempotency behaviour is preserved.
- No fee calculation, validation, payment or financial behaviour was unintentionally changed.

Review the associated unit and browser regression tests.

## 4. G-03 — Connection-race remediation

Inspect:

- `e2e-common/fresh-connections.ts`
- `e2e-common/connection-probe.ts`
- `e2e/helpers.ts`
- Mock Playwright specifications.
- Real-stack Playwright specifications.
- Playwright request fixtures.
- ESLint import restrictions and guard tests.
- Connection regression specifications.
- `e2e/csp.spec.ts`

Verify that all relevant Playwright API clients use the shared connection protection.

Pay particular attention to:
- Default browser contexts.
- Additional contexts.
- Staff and owner saved-login contexts.
- Built-in request fixtures.
- Mock and real-stack coverage.
- Protection against future direct imports bypassing the shared fixture.

Evaluate the decision not to wrap `route.fetch()`.

Determine whether its current single-call usage justifies the documented exception and whether the regression test
adequately protects that assumption.

## 5. CI and test evidence

Inspect the actual available CI results.

Relevant runs include:

- `40711057141`
- `40715437694`
- `40720405660`
- `40992515372`

Check any newer runs associated with the latest branch commits.

Distinguish:
- CI runs on code commits.
- CI runs on documentation-only commits.
- Tests that ran successfully against identical code.
- Jobs that failed before testing.
- Actual test failures.

Explicitly decide whether the available evidence is sufficient despite the absence of a fully green run directly on the
G-03 code commit.

Do not dismiss G-05 without examining the available evidence.

## 6. Separate findings

G-04 and G-05 are outside the G-02/G-03 acceptance decision.

Record any observations about them separately.

Do not close either finding as part of this review.

## 7. Required review record

Complete:

`docs/governance/independent-review-decision-template.md`

For every checklist item, record:

- PASS
- FAIL
- NEEDS CLARIFICATION

Include supporting evidence, file references, test results and any concerns.

Provide a recommendation for each finding:

- APPROVE
- APPROVE WITH CONDITIONS
- REJECT

Conclude with a recommendation for G-02 and a separate recommendation for G-03.

## 8. Submission

Submit the completed review record through a pull request.

If collaborator access is active, submit from a branch.

Otherwise, use a fork and submit a pull request.

Do not modify the implementation to make your review pass. Raise corrective actions separately.

## 9. Restrictions

This review does not authorize:

- Merging remediation branches.
- Production deployment.
- Database migrations.
- Changes to financial safeguards.
- Starting Phase 7.
- Approval of previous stages.
- Production financial operations.

The owner retains the final acceptance and integration decisions.

The review must represent your independent technical judgement.
