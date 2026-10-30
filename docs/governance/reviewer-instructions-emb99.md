# Reviewer instructions — `emB99`: independent review of G-02 and G-03

**Status: REVIEW NOT STARTED. Reviewer acceptance PENDING.** Prepared 2026-10-30 by Claude Code, the author of the
changes under review. These instructions tell you where to look. They are not evidence that anything is correct.
**Examine the source and re-run the tests yourself. Do not accept the implementation's reports at face value.**

## 1. Objective

Give an independent, explicit recommendation on two remediations, based on your own reading of the code and your own test
runs:
- **G-02:** the fee-policy draft confirmation was lost when the page refreshed.
- **G-03:** Playwright test API requests could fail with `socket hang up` on a reused idle connection.

## 2. Scope and exclusions

**In scope:**
- the G-02 range `d3d45c6..d0be53e`;
- the G-03 ranges `d0be53e..cec643d` and `cec643d..4be36ec`;
- the documents and evidence they cite.

**Out of scope.** Your recommendation does not cover:
- a merge into `main`, or the integration scope (owner decision D-15);
- Stages 1–20 and remediation Phases 1–6 (everything in `d3d45c6` and before);
- G-04 and G-05 (§8);
- any deployment, release or financial operation.

Please state any limitation or disagreement explicitly, including disagreement with these instructions.

## 3. Commits

`git log --oneline d3d45c6..4be36ec`. All were authored and committed by `Fatifizo`. The history is linear.

| Commit | Type | Content |
|---|---|---|
| `d83d1ac` | **code** | G-02 fix |
| `d0be53e` | documents | G-02 evidence; G-03 recorded |
| `02aac0e` | **code** | G-03: fresh connection per test API request (mock suite) |
| `8a293df` | documents | G-03 investigation report |
| `bdbc607` | **code** | G-03: built-in `request` fixture covered; browser-traffic test; import lint guard |
| `cec643d` | documents | G-03 verification |
| `e063113` | **code** | G-03: shared module, real-stack suite covered, guard extended, `route.fetch()` test |
| `3c97ed6`, `812bb3c`, `ec1cdc5`, `4be36ec` | documents | real-stack report, CI records, review package, handover |

Code check: `git diff --name-only d3d45c6..4be36ec -- . ':!docs'` lists every non-document file in the range. The author
claims that, apart from G-02's 4 files, all of them are browser-test infrastructure, the lint config or the guard test.
Please verify this.

## 4. Files to inspect

**G-02 (`d83d1ac`):**
- `apps/web/src/components/admin/finance/fee-forms.tsx`: `useFormSubmit` exposes `noticeStatus`/`errorStatus`;
  `VersionForm` gains `available`. Check that no request body, amount, validation or idempotency logic changed.
- `apps/web/src/app/admin/finance/fees/policies/[id]/page.tsx` (line 71).
- `apps/web/src/__tests__/fees-components.test.tsx` (2 new tests), `apps/web/e2e/staff-fees.spec.ts` (3 assertions).

**G-03 (`02aac0e`, `bdbc607`, `e063113`):**
- `apps/web/e2e-common/fresh-connections.ts`: the fixture.
  - `freshConnectionPerRequest` wraps `fetch`.
  - Fixtures: `context`, `request`, and the worker-scoped `browser` that wraps `newContext`.
  - A `WeakSet` guard.
- `apps/web/e2e-common/connection-probe.ts`: the probe server used by the tests.
- `apps/web/e2e/helpers.ts`: re-exports `test`.
- `apps/web/e2e/request-connections.spec.ts`, `apps/web/e2e-real/request-connections.spec.ts`.
- `apps/web/playwright.real.config.ts`: the real-stack spec is registered in two projects.
- `apps/web/eslint.config.mjs` (G-03 block), `apps/web/src/__tests__/e2e-import-guard.test.ts`.
- The 27 existing specs whose only claimed change is the `test` import (`e2e/*.spec.ts`, `e2e-real/*.spec.ts`).
- `apps/web/e2e/csp.spec.ts:75`: the only production `route.fetch()`.
- `apps/web/e2e-real/support/browser.ts`: where staff and owner contexts are created.

**Background:**
- `independent-review-handover.md`;
- `g03-review-package.md` (path-by-path coverage table in §3);
- `evidence/g03-investigation-report.md`.

Report §12.2 cites real-stack spec lines one too low (finding R-1); the package's §3 has the current lines.

## 5. Test evidence to produce yourself

Use a clean checkout of the commit you review, and record its full SHA. Commands run in `apps/web` (Node 24.21, `npm ci`,
`npx playwright install chromium`).

| What | Command | Notes |
|---|---|---|
| Unit tests (G-02 and the guard) | `npm test`; `npx vitest run src/__tests__/fees-components.test.tsx src/__tests__/e2e-import-guard.test.ts` | |
| Lint and types | `npm run lint`; `npm run typecheck` | the guard rule runs here |
| G-02 browser test | `npm run build`, then `CI=1 E2E_SKIP_BUILD=1 npx playwright test e2e/staff-fees.spec.ts` | the mock harness starts its own servers on ports 3100, 3101 and 3199 |
| G-03 mock regression | `CI=1 E2E_SKIP_BUILD=1 npx playwright test e2e/request-connections.spec.ts --repeat-each=10` | |
| Full mock suite | `CI=1 E2E_SKIP_BUILD=1 npx playwright test` | G-04 may appear (§8) |
| G-03 real-stack regression | on a **disposable** compose project only: `GIT_COMMIT=$(git rev-parse HEAD) docker compose -p <name> up -d --build --wait`, then `COMPOSE_PROJECT_NAME=<name> npx playwright test -c playwright.real.config.ts request-connections.spec.ts` | `apps/web/e2e-real/README.md`. Never against a shared development database |

**Mutation checks** (recommended; revert after each):
- Remove `connection: "close"` in `fresh-connections.ts`: the coverage and `route.fetch()` tests should fail.
- Remove the G-03 block from `eslint.config.mjs`: the two "rejects" guard tests should fail.

**Optional: reproduce the race.** Use the probe scripts in `evidence/g03/scripts/` against `node e2e/serve.mjs`. Failures
cluster at idle gaps of about 5 985–6 000 ms.

## 6. CI evidence: run-to-commit mapping

Public GitHub API; job logs and artifacts need a token. The workflow has **no path filter**, so a documentation commit
runs every suite on the code it contains.

| Run | Commit | Type | Result | Tests ran? |
|---|---|---|---|---|
| 40521729530 | `d83d1ac` | code (G-02) | 7/8 | yes. The failure was the original G-03 symptom in mock e2e |
| 40526376062 | `d0be53e` | docs (same code as `d83d1ac`) | 8/8 | yes |
| 40654287677 | `02aac0e` | code | 8/8 | yes |
| 40672295511 | `bdbc607` | code | 8/8 | yes |
| 40711057141 | `e063113` | **code** | **7/8** | Integration **did not start** (Go module proxy errors); browser suites passed |
| 40715437694 | `3c97ed6` | docs (same code as `e063113`) | **7/8** | yes: 1 integration test failed (**G-05**) |
| 40720405660 | `812bb3c` | docs (same code as `e063113`) | 8/8 | yes |
| 40992515372 | `ec1cdc5` | docs (same code as `e063113`) | 8/8 | yes |
| 41014930811 | `4be36ec` | docs (same code as `e063113`) | 8/8 | yes |

**The distinction to judge.** No CI run on the code commit `e063113` *itself* is fully green. Runs on later
documentation-only commits with a byte-identical code tree are (`git diff e063113 <commit> -- . ':!docs'` is empty). Decide
whether that is sufficient evidence, and say so in your record.

## 7. `route.fetch()`: remaining limitation

- `route.fetch()` is **not** wrapped by the fixture.
- Its only use (`csp.spec.ts:75`) is the first and only request of its test, so it starts on a fresh connection.
- Repeated `route.fetch()` calls in one test across an idle gap of about 6 s **would** be exposed. No such use exists today.
- Decide whether to accept this as documented, or to require a guard.

## 8. Separate findings: not part of this decision

- **G-04 (open; test design).** `e2e/discovery.spec.ts:220` can record the page's first `CAMPAIGN_VIEW` event (sent after
  hydration) as an offline "replay". Reproduced with CPU throttling.
- **G-05 (open; cause unknown).** `TestReconciliationPaymentsEndToEnd` failed once in CI run 40715437694 and has not been
  reproduced.

They are tracked separately. You may say whether they should block G-02/G-03 closure (checklist item 13), but they are not
being reviewed here.

## 9. Checklist

Record each item as **PASS**, **FAIL** or **NEEDS CLARIFICATION**, with the evidence you examined (files, commands, run
IDs). Items are as in `independent-review-handover.md` §8:

1. Every Playwright API client the suites use is wrapped. No unwrapped path exists except the documented `route.fetch()`
   and the control test (`request.newContext()` in `e2e/request-connections.spec.ts`).
2. Extra contexts and saved-login-state (`storageState`) contexts are covered.
3. The built-in `request` fixture is covered.
4. The import guard covers `e2e/**/*.spec.ts` and `e2e-real/**/*.spec.ts`, and leaves helpers, configs and unit tests
   alone.
5. Regression tests fail when the protection or the guard is removed.
6. No retries were introduced (no `maxRetries`; one request reaches the server per call).
7. Browser and interception traffic are unchanged.
8. No application, Go, migration or financial file is in the G-03 range.
9. The `route.fetch()` rationale matches current use, and its limitation is stated.
10. The 27 existing-spec edits change only imports.
11. The CI mapping (§6) is accurate, and no run is overstated.
12. You re-ran the regression specs and the guard test (§5).
13. Whether R-1, G-04 and G-05 block closure.
- **G2-1.** G-02: the notice survives the refresh; a failed save shows no success; a double click sends one request; a
  retry reuses the `Idempotency-Key`.
- **G2-2.** G-02: no change to fee requests, validation, amounts or server rules.

## 10. Recording and submitting

1. Copy `independent-review-decision-template.md` to
   `docs/governance/reviews/<YYYY-MM-DD>-g02-g03-emB99.md` and complete it:
   - your name and role;
   - the commit you reviewed;
   - every checklist outcome with its evidence;
   - findings;
   - corrective actions;
   - a recommendation per item (APPROVE / APPROVE WITH CONDITIONS / REJECT).
2. Submit it as a pull request.
   - From a fork, if you do not have collaborator access. The repository is public.
   - From a branch, if the administrator has granted access.
   - Raise findings as pull-request review comments or issues as well, if you prefer.
3. A GitHub approval counts toward required reviews only with collaborator access, which the administrator grants.
   Record your approval in the record either way.
4. The owner decides on closing G-02/G-03 and on acceptance. Your recommendation is an input, not the closure.

## 11. Independence

- You are a separate account from the implementation account `Fatifizo`, and you authored no commit in the range (verified
  2026-10-30: `evidence/reviewer-emb99-verification-2026-10-30.txt`). Please confirm both in your record.
- Do not rely on Claude Code's reports, tables or test logs as proof. Use them to know where to look, and verify against the
  code and your own runs.
- If you cannot verify something (for example, CI logs need a token), record it as a limitation rather than as PASS.
