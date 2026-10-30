# Independent-review handover — G-02 and G-03

**Status: HANDOVER PREPARED. REVIEWER DESIGNATED AND ACCEPTED. REVIEW NOT STARTED.**
- The designated reviewer is `emB99` (§0).
- Acceptance and collaborator access: both as reported by the owner, 2026-10-30.
- G-02 and G-03 are fixed on their branches, **not reviewed and not closed**.
- Claude Code wrote every change listed here and this handover. Nothing in this document is a review, an approval or an
  owner decision.

Prepared 2026-10-30. Repository `Fatifizo/fundzim`. Verified state at preparation:
- branch `remediation/g03-real-stack-connections` at `ec1cdc5`, clean, equal to `origin`;
- every G-02 and G-03 commit present;
- no code change outside `docs/` since `e063113`.

Companion documents:
- the G-03 review package: `g03-review-package.md`, with the checklist reproduced in §8 below;
- the G-03 investigation report: `evidence/g03-investigation-report.md`, §1–§12.8;
- G-02's record: `review-preparation-report.md` §4;
- the review method: `independent-review-process.md`;
- the decision template: `independent-review-decision-template.md`;
- the owner decisions: `owner-decision-register.md`, "Before an independent review can start".

## 0. Designated reviewer (recorded 2026-10-30)

| Field | Value |
|---|---|
| Designated reviewer | **`emB99`**: GitHub user id 61978640, created 2020-03-09. Designated as `emb99`; GitHub usernames are case-insensitive and the canonical login is `emB99` |
| Designated by | the repository owner's instruction to Claude Code, 2026-10-30 (D-01a). Claude Code records the designation and does not make it |
| Review scope | **G-02 and G-03 only** (§1) |
| Review status | **NOT STARTED** |
| Reviewer acceptance | **ACCEPTED, as reported by the repository owner to Claude Code on 2026-10-30 (07:20 UTC); no reviewer-authored confirmation exists in the repository yet (no issue, comment or fork by `emB99` at that time).** The reviewer should confirm it in their review record (decision template §1) |
| Repository read access | **VERIFIED**: the repository is public (`private: false`), so any GitHub account can read and clone it |
| Collaborator status and permission | **GRANTED, as reported by the repository owner on 2026-10-30.** Not independently verified: the collaborator and permission endpoints still need a token (HTTP 401 at 11:53 UTC). Unknown: the permission level (a counting pull-request approval needs at least write), and whether `emB99` has accepted the GitHub invitation (access is active only then). An administrator can confirm both with `gh api repos/Fatifizo/fundzim/collaborators/emB99/permission` with an administrator's token |
| Independence (technical) | **VERIFIED as far as GitHub data allows.** `emB99` is a different account from the implementation account `Fatifizo` (id 236812721). All 11 commits in `d3d45c6..4be36ec` are authored and committed by `Fatifizo`. `emB99` appears nowhere in the repository history and is not a contributor |
| Independence (person) | **not verifiable from GitHub data.** That a different person controls `emB99` rests on the owner's designation |
| Review commit | **to be recorded when the reviewer begins** (the full SHA of the head they review) |
| Evidence | `evidence/reviewer-emb99-verification-2026-10-30.txt` |
| Reviewer instructions | `reviewer-instructions-emb99.md` |

## 1. Purpose and scope

The reviewer is asked to assess **two narrowly scoped remediations**:
- **G-02:** a presentation fix in the fee-policy admin page.
- **G-03:** a fix to the browser-test infrastructure (Playwright connection race), in the mock-API and real-stack suites.

**Out of scope, governed separately:**
- the integration of these branches into `main`;
- Stages 1–20 and remediation Phases 1–6, which are still unreviewed (D-15);
- G-04 and G-05 (§6);
- any production or financial-operation authorisation.

A PASS on G-02 or G-03 **does not** approve the integration range, any earlier commit, a merge or a deployment.

## 2. Commit ranges

The history is linear. Each branch contains the previous one.

| Item | Base → head | Branch | Code commits | Documentation-only commits |
|---|---|---|---|---|
| G-02 | `d3d45c6` → `d0be53e` | `remediation/g02-fee-draft-notice` | `d83d1ac` | `d0be53e` |
| G-03, part 1 (mock suite, guard) | `d0be53e` → `cec643d` | `remediation/g03-e2e-socket-hangup` | `02aac0e`, `bdbc607` | `8a293df`, `cec643d` |
| G-03, part 2 (real-stack, shared module) | `cec643d` → `ec1cdc5` | `remediation/g03-real-stack-connections` | `e063113` | `3c97ed6`, `812bb3c`, `ec1cdc5` |

`d3d45c6` is the head of `remediation/integrity-status` (remediation Phase 6 plus governance documents), which is
**not** part of this review. Review each range with `git diff <base>..<head>`.

## 3. Technical summaries

### G-02: the fee-draft confirmation disappeared after the page refresh

- **Defect.** After a fee-policy draft version was saved, `VersionForm` set a success notice and called
  `router.refresh()`. The refreshed page saw the new draft and **replaced the form**, unmounting the notice, so the
  confirmation existed only between the save and the refresh. Found as an intermittent CI failure
  (`staff-fees.spec.ts:50`, mobile, run 40508474321).
- **Fix (`d83d1ac`, presentation only).**
  - The page keeps `VersionForm` mounted with `available={!openDraft}`, the pattern `FeeAction` already uses.
  - The notice keeps one position above the form or the "draft already exists" text, so the element, its focus and its
    live-region announcement survive.
  - Unchanged: requests, idempotency key, step-up, validation, fee rules, and the server's one-open-draft rule (unique
    index `uq_fee_policy_versions_one_draft`).
- **Files to inspect.**
  - `apps/web/src/components/admin/finance/fee-forms.tsx`: the `useFormSubmit` hook exposes `noticeStatus` and
    `errorStatus`; `VersionForm` gains `available`. The amount fields only change indentation.
  - `apps/web/src/app/admin/finance/fees/policies/[id]/page.tsx` (line 71).
  - `apps/web/src/__tests__/fees-components.test.tsx`: 2 new unit tests.
  - `apps/web/e2e/staff-fees.spec.ts`: 3 added assertions.
- **Tests and reproduction.**
  - The new browser assertion failed 2/2 (desktop, mobile) on the unfixed code, and the cause was confirmed before the
    change.
  - After the fix, local runs:
    - fee spec 150/150 (×25, both projects);
    - unit tests 582/582;
    - full mock suite 221 passed, 1 skipped.
  - A mutation (the notice put back inside the form) made the unit test fail.
  - Evidence: `evidence/g02/` with `SHA256SUMS`.
- **Remaining limitation.** The notice stays until the next reload, even after the draft is submitted (as `FeeAction`
  does).

### G-03: Playwright test-setup requests could hang up on a reused idle connection

- **Defect.**
  - Playwright's request client reuses idle keep-alive connections.
  - The apps under test (Next standalone on Node 24, in the mock harness and in the compose `fundzim-web` container) close
    an idle connection at 6 s.
  - A request sent on a reused connection in the ~15–40 ms before that close fails with `apiRequestContext.<m>: socket
    hang up` before the server reads it.
  - Seen in CI run 40521729530. That run is linked to the mechanism by its signature only, because its log needs a token.
- **Fix.** Every Playwright API request made by the tests uses a fresh connection (`Connection: close`), through a
  shared fixture. It covers:
  - the default context;
  - every `browser.newContext()`, including saved-login-state contexts;
  - the built-in `request` fixture.

  It does not change browser traffic and adds no retries.
- **Import guard.** An ESLint `no-restricted-imports` rule stops a spec in either suite from importing `test` from
  `@playwright/test`.
- **Files to inspect.** Listed in the package §1. In short:
  - `apps/web/e2e-common/fresh-connections.ts`, `apps/web/e2e-common/connection-probe.ts`;
  - `apps/web/e2e/helpers.ts`;
  - `apps/web/e2e/request-connections.spec.ts`, `apps/web/e2e-real/request-connections.spec.ts`;
  - `apps/web/playwright.real.config.ts`;
  - `apps/web/eslint.config.mjs` (G-03 block), `apps/web/src/__tests__/e2e-import-guard.test.ts`;
  - the import-only edits in 27 existing specs (19 mock, 8 real-stack): 0 non-import lines by script check.
- **Request-client coverage.** Path by path, with current line numbers, in package §3. Report §12.2 cites spec lines one
  too low (R-1).

## 4. Regression tests and reproduction evidence

All of this is the author's own evidence, from local runs and disposable compose projects, never the shared development
database.

| Claim | Evidence | Where |
|---|---|---|
| The race exists (mock harness) | unprotected 53/120 vs fixed 120/120 at the 6 s boundary | `evidence/g03/auth-app-default-vs-fresh-connection.txt` |
| The race exists (real stack) | unprotected 45/60 and 49/60 vs fixed 60/60 and 60/60 (`fundzim-r11`, `fundzim-r12`) | `evidence/g03/verification-2026-10-28/`, `evidence/g03/real-stack-2026-10-28/` |
| Tests detect the unprotected state | mutation: without the header the coverage and `route.fetch()` tests fail (mock, both projects; re-checked on `812bb3c`); the real-stack coverage test fails | `evidence/g03/review-prep-2026-10-30/`, `evidence/g03/real-stack-2026-10-28/` |
| The guard detects a bypass | the rule removed: exactly the two "rejects" cases fail | `evidence/g03/review-prep-2026-10-30/import-guard-and-mutation-812bb3c.txt` |
| Stable when fixed | mock spec 100/100; real-stack spec 60/60; originally failing test 60/60 | as above |
| Suites intact | full real-stack 70 passed; full mock 231 + 1 skipped (CI) | §5 |

The reproduction scripts are in `evidence/g03/scripts/`. Reviewer re-run commands are in package §9, item 12.

## 5. CI run-to-commit mapping

Evidence comes from the public GitHub API; job logs and artifacts need a token. The workflow has **no path filter**, so
a documentation-only commit runs every suite on the code tree it contains.

| Run | Commit | Type | Jobs | Tests ran? | What it establishes |
|---|---|---|---|---|---|
| 40521729530 | `d83d1ac` (G-02 fix) | code | 7/8 | yes | the G-02 fee tests passed; one mock e2e failure, the original G-03 |
| 40526376062 | `d0be53e` | docs (code tree = `d83d1ac`) | 8/8 | yes | the G-02 code tree passed every suite |
| 40654287677 | `02aac0e` (G-03 fix) | code | 8/8 | yes | the G-03 part-1 code passed on its own commit |
| 40657724785 | `8a293df` | docs | 8/8 | yes | — |
| 40672295511 | `bdbc607` | code | 8/8 | yes | the `request`-fixture coverage and guard passed on their own commit |
| 40675849219 | `cec643d` | docs (code tree = `bdbc607`) | 8/8 | yes | — |
| 40711057141 | `e063113` | **code** | **7/8** | integration **did not start** (Go module proxy errors); the browser suites passed | the real-stack code passed the browser suites on its own commit; integration was not validated on this commit |
| 40715437694 | `3c97ed6` | docs (code tree = `e063113`) | **7/8** | yes: 254 passed, **1 failed** (G-05) | a test failure of unknown cause |
| 40720405660 | `812bb3c` | docs (code tree = `e063113`) | 8/8 | yes: integration 255/0/8, real-stack 70, mock 231 + 1 | the exact code tree of `e063113` passed every suite |
| 40992515372 | `ec1cdc5` | docs (code tree = `e063113`) | 8/8 | yes: same counts | same tree, second all-green run |
| 41014930811 | `4be36ec` | docs (code tree = `e063113`) | 8/8 | yes | same tree, third all-green run (recorded 2026-10-30) |

**Code commits vs documentation-only commits.**
- No run **on the code commit `e063113` itself** is fully green.
- Its identical code tree is fully green twice (40720405660, 40992515372) and failed one integration test once
  (40715437694, G-05).
- The reviewer decides whether runs on a documentation commit with a byte-identical code tree are sufficient evidence.
  `git diff e063113 ec1cdc5 -- . ':!docs'` is empty.

## 6. Separate open findings (not part of this review's decision)

| Id | Severity | Status | Summary | Next step (owner to authorise) |
|---|---|---|---|---|
| G-04 | LOW | **open**: test-design issue, established | `e2e/discovery.spec.ts:220` can record the page's first `CAMPAIGN_VIEW` (sent after hydration, while offline) as a "replay". Reproduced 20/20 with CPU throttling; the product behaves as designed | a separate branch changing the test, not the application |
| G-05 | MEDIUM | **open**: cause unknown | `TestReconciliationPaymentsEndToEnd` failed once in CI (40715437694, "simulator line for pay2 not matched: 0"). It passed in 7 other CI runs on the same code, 20 isolated local runs and 1 local full suite. Not reproduced | a separate investigation with CI log access. Reconciliation code is not changed by G-02 or G-03 |

## 7. `route.fetch()`, risks and open questions

**`route.fetch()`.**
- There is one use, in `e2e/csp.spec.ts:75`: the first and only request of its test, so it always starts on a fresh
  connection. **Not exposed.**
- It is not wrapped.
- Repeated `route.fetch()` calls in one test across an idle gap of about 6 s would be exposed. No such use exists.
- Test: `e2e/request-connections.spec.ts` "route.fetch never inherits a pooled connection …".

**Known risks.**
1. G-03's link to the original CI event rests on its signature only.
2. The fixture wraps the request client's public `fetch` method; a Playwright upgrade must keep both regression specs
   green.
3. The `route.fetch()` limitation above.
4. G-02's notice persists until reload.
5. The real-stack and race measurements are local disposable-stack evidence, not CI.
6. G-04 and G-05 are open.

**Questions for the reviewer.**
- Are runs on documentation commits with a byte-identical code tree acceptable as CI evidence for `e063113` (§5)?
- Should the `route.fetch()` limitation be enforced by a lint rule now, or left documented?
- Do R-1 (documentation line numbers), G-04 or G-05 block closure of G-02/G-03? The author's view is that they do not,
  because they are tracked separately; the reviewer decides.

## 8. Reviewer checklist (from `g03-review-package.md` §9; G-02 items added)

The author's column is Claude Code's own verification. The reviewer completes the last column (PASS / FAIL / NEEDS
CLARIFICATION) and records it with `independent-review-decision-template.md`.

| # | Check | Author's check | Reviewer |
|---|---|---|---|
| 1 | The fixture wraps every Playwright API client the suites use; no unwrapped path except the documented `route.fetch()` and the control test | done | NOT REVIEWED |
| 2 | Extra contexts and `storageState` contexts are covered | done | NOT REVIEWED |
| 3 | The built-in `request` fixture is covered | done | NOT REVIEWED |
| 4 | The import guard covers both suites' specs and leaves helpers, configs and unit tests alone | done | NOT REVIEWED |
| 5 | Regression tests fail when the protection or the guard is removed | done (local mutations) | NOT REVIEWED |
| 6 | No retries introduced | done | NOT REVIEWED |
| 7 | Browser and interception traffic unchanged | done | NOT REVIEWED |
| 8 | No application, Go, migration or financial file in the G-03 range | done | NOT REVIEWED |
| 9 | `route.fetch()` rationale matches current use; limitation stated | done | NOT REVIEWED |
| 10 | The 27 existing-spec edits change only imports | done | NOT REVIEWED |
| 11 | CI mapping (§5) accurate; no run overstated | done | NOT REVIEWED |
| 12 | Re-run the regression specs and the guard test (package §9, item 12) | — | NOT REVIEWED |
| 13 | Decide whether R-1, G-04 and G-05 block closure | — | NOT REVIEWED |
| G2-1 | G-02: the notice survives the refresh; a failed save shows no success; one request per click; the retry reuses the `Idempotency-Key` | done (unit and e2e tests) | NOT REVIEWED |
| G2-2 | G-02: no change to fee requests, validation, amounts or server rules (`fee-forms.tsx` amount fields only re-indented) | done | NOT REVIEWED |

## 9. Preconditions before a review starts

These are the owner's decisions; see `owner-decision-register.md`, "Before an independent review can start".
1. The owner names the reviewer (D-01a). **Done on 2026-10-30: `emB99` (§0).** Acceptance and collaborator access are both
   reported by the owner (2026-10-30). Unverified: the permission level, and whether the invitation has been accepted.

   The reviewer is not the implementation account, and read access is verified.
2. The owner confirms that this review covers only G-02 and G-03. The integration scope remains D-15.
3. Branch protection (D-01) is needed before any controlled integration. It is not needed for the review itself.
