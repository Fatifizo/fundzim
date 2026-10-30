# G-03 independent-review package — Playwright connection race (mock and real-stack suites)

**Status: PREPARED. NOT REVIEWED. G-03 NOT CLOSED.**
- No independent reviewer exists (D-01a).
- Claude Code is the author of every change listed here and of this package. Nothing here is a review, an approval or
  an acceptance.
- How to review and record a review: `independent-review-process.md`. Full investigation:
  `evidence/g03-investigation-report.md` (§1–§12).

Prepared 2026-10-30 against branch `remediation/g03-real-stack-connections` at **`812bb3c`**:
- clean, and equal to `origin`;
- parent `3c97ed6`;
- G-02 commits `d83d1ac` and `d0be53e` and earlier G-03 commits preserved;
- nothing rewritten.

## 1. What to review

| Item | Value |
|---|---|
| Base | `d0be53e` (head of `remediation/g02-fee-draft-notice`; G-02 is reviewed separately) |
| G-03 range | `d0be53e..812bb3c`, along `remediation/g03-e2e-socket-hangup` (`02aac0e`, `8a293df`, `bdbc607`, `cec643d`), then `remediation/g03-real-stack-connections` (`e063113`, `3c97ed6`, `812bb3c`) |
| Code commits | `02aac0e` (fix), `bdbc607` (`request` fixture, browser-traffic test, lint guard), `e063113` (shared module, real-stack coverage, `route.fetch()` test, guard scope). The others change only `docs/` |
| Scope check (verified) | every non-documentation file in the range is under `apps/web/e2e`, `apps/web/e2e-real`, `apps/web/e2e-common`, `apps/web/eslint.config.mjs`, `apps/web/playwright.real.config.ts` or `apps/web/src/__tests__/e2e-import-guard.test.ts`. No application, Go, migration, fee, payment, ledger or payout file is in the range |

**Files to read.**
- `apps/web/e2e-common/fresh-connections.ts`: the fixture.
  - `freshConnectionPerRequest` wraps the request client's `fetch` and adds `Connection: close`.
  - Fixtures: `context`, `request` and the worker-scoped `browser` (which wraps `newContext`).
  - A `WeakSet` stops a context from being wrapped twice.
- `apps/web/e2e-common/connection-probe.ts`: the in-process probe server used by both regression specs.
- `apps/web/e2e/helpers.ts`: re-exports `test`.
- `apps/web/e2e/request-connections.spec.ts`, `apps/web/e2e-real/request-connections.spec.ts`: the regression specs.
- `apps/web/playwright.real.config.ts`: the real-stack spec is registered in `desktop-chromium` and `mobile-chromium`.
- `apps/web/eslint.config.mjs` (G-03 block) and `apps/web/src/__tests__/e2e-import-guard.test.ts`: the import guard.
- `apps/web/e2e/csp.spec.ts:75`: the only production `route.fetch()`.
- **Import-only edits:** 27 existing specs (19 in `e2e/`, 8 in `e2e-real/`). A script check (§6) found 0 non-import
  changed lines.

## 2. Root cause and how it was reproduced

**Mechanism.**
- Playwright's request client reuses idle keep-alive connections.
- The apps under test close idle connections at 6 s:
  - mock harness: Next standalone, Node 24;
  - real stack: the compose `fundzim-web` container, through Docker port publishing.

  Both use keepAliveTimeout 5 s plus keepAliveTimeoutBuffer 1 s.
- A request sent on a reused connection in the ~15–40 ms before the close fails with
  `apiRequestContext.<method>: socket hang up` or `ECONNRESET`, before the server reads it.

**Reproduction (local, disposable environments only; not CI):**

| Target | Unprotected | Protected (fix) | Evidence |
|---|---|---|---|
| Mock auth app, Playwright client, gaps 5 985–6 000 ms | 53/120 OK (resets and hang-ups) | 120/120 OK | `evidence/g03/auth-app-default-vs-fresh-connection.txt` |
| Real-stack web on `fundzim-r11`, gaps 5 990–6 000 ms | 45/60 OK | 60/60 OK | `evidence/g03/verification-2026-10-28/real-stack-r11-web-healthz-playwright-default-vs-close.txt` |
| Real-stack web on `fundzim-r12`, the shared function itself, gaps 5 985–6 000 ms | 49/60 OK | 60/60 OK | `evidence/g03/real-stack-2026-10-28/real-stack-r12-shared-fixture-vs-unprotected.txt` |

**Link to the original CI failure** (run 40521729530): a strong signature match only. It is the same message, there is no
response, and the request followed a ~6.7 s idle gap. The run's log and trace need a token. **Not conclusive.**

## 3. Request-client coverage (path by path; verified on `812bb3c`)

Line numbers here are current. Report §12.2 cites the spec lines before the import edit and is **off by one** (R-1).

| Path | Client | Created by | Covered by | Exposed without the fix |
|---|---|---|---|---|
| `e2e-real/staff-ledger.spec.ts:25,34,84,102,109,115` | `page.request` | `staffPage(browser, …)` → `browser.newContext({ storageState })` (`support/browser.ts:102`) or `:104` | `browser` fixture (worker-scoped `newContext` wrapper) | yes |
| `e2e-real/staff-moderation.spec.ts:94,184,212,253` | `page.request` | `browser.newContext` in the spec and in `ownerContext`/`staffPage` | same | yes |
| `e2e-real/staff-reconciliation.spec.ts:24,36` | `page.request` | `staffPage(browser, …)` | same | yes |
| `e2e-real/support/browser.ts:108` | `page.request` (`/api/v1/me`) | `staffPage` | same | yes |
| `e2e-real/public.spec.ts:200,211,235,260,271`; `e2e-real/discovery.spec.ts:37,41,52,56` | built-in `request` fixture | Playwright | `request` fixture override | yes |
| default `page`/`context` of every test (both suites) | `context.request` | Playwright | `context` fixture | yes |
| `e2e/` helpers and specs (72 call sites) | `page.request`, `request`, `browser.newContext` | as above | as above | yes (the original failure) |
| `e2e-real/support/http.ts:146`, `global-setup.ts:42` | Node `fetch` (undici) | global setup | not wrapped (not needed) | **no**: undici did not reuse a connection at gaps ≥ 2.9 s; 0 failures at 5 990–6 000 ms |
| `e2e-real/support/mailpit.ts:14,21` | undici → Mailpit (Go) | global setup | not wrapped | **no**: 0/48 failures at gaps 1–10 s |
| `e2e/csp.spec.ts:75` | `route.fetch()` | browser interception | **not wrapped** | **not in current use** (§4) |

The fixture is used by every spec: the 20 `e2e/` specs import `test` from `./helpers`, and the 9 `e2e-real/` specs from
`../e2e-common/fresh-connections`.

There are no other client creation paths:
- the only `request.newContext()` is the intentional control test;
- there is no browser launch;
- there is no `browser.newPage()`;
- there is no `maxRetries` anywhere.

Every `staffPage`/`ownerContext` call site passes the test's own `browser` fixture (verified by grep).

## 4. `route.fetch()`: rationale and limitation

**Measured.**
- `route.fetch()` keeps its own keep-alive connection: 4 routed requests on 1 connection.
- It does not go through the wrapped `fetch`.
- It never inherits the setup requests' connections, because the fixture closes them.

**Current use.**
- One call per test, in `csp.spec.ts` "an injected inline script without the nonce is blocked".
- That test makes no earlier API request.
- So the call always starts on a fresh connection. **Not exposed.**

**Limitation.** Repeated `route.fetch()` calls in one test, across an idle gap of about 6 s, would be exposed. No such use
exists; the limitation is documented in the fixture's comment.

**Test.** `e2e/request-connections.spec.ts` "route.fetch never inherits a pooled connection …".

**Unchanged.** Browser interception traffic: the browser-traffic tests show page loads keep `keep-alive` and reuse their
connection.

## 5. Test evidence

**Local (author's runs; disposable environments; not CI):**

| Run | Result |
|---|---|
| mock `request-connections.spec.ts` on `812bb3c` (2026-10-30) | 10/10 |
| same, with the fixture's header removed (mutation, 2026-10-30) | coverage and `route.fetch()` tests **fail** on both projects; the others pass |
| import guard with the rule block removed (mutation, 2026-10-30) | exactly the two "rejects" cases fail; the three "accepts" cases pass |
| import guard on `812bb3c` | 5/5 |
| mock spec ×10, `e063113` | 100/100 |
| real-stack full suite, `fundzim-r12` (`e063113`) | 70 passed |
| real-stack spec ×10, `fundzim-r12` | 60/60 |
| real-stack spec with the header removed (mutation), `fundzim-r12` | coverage test **fails** on both projects |
| full mock suite, `e063113` | 230 passed, 1 skipped, **1 failed (G-04)** |

**GitHub Actions** (public API; job logs and artifacts need a token):

| Run | Commit (exact) | Commit type | Jobs | Tests ran? | Result | What it establishes |
|---|---|---|---|---|---|---|
| 40675849219 | `cec643d` | documentation (previous branch) | 8/8 | yes: integration 255/0/8; mock e2e 229 + 1 skipped; real-stack 64 | success | validates the code tree of `bdbc607` (`cec643d` only adds documents), not the real-stack coverage |
| 40711057141 | `e063113` | **code** | 7/8 | integration **did not run**: Go modules from `proxy.golang.org` failed ("stream error … INTERNAL_ERROR") at "Start the API on the runner"; mock e2e 231 + 1 skipped and real-stack 70 passed | **failure** (before tests, in one job) | the web and real-stack suites passed on the code commit; the integration suite was **not** validated on this commit. External cause by its own message; no Go change in the range |
| 40715437694 | `3c97ed6` | documentation | 7/8 | yes: integration 254 passed, **1 failed** (`TestReconciliationPaymentsEndToEnd`, G-05), 8 skipped; mock e2e 231 + 1; real-stack 70 | **failure** (a test) | a test failure **whose cause is unknown** (§7) |
| 40720405660 | `812bb3c` | documentation | **8/8** | yes: integration 255/0/8; mock e2e 231 + 1 skipped; real-stack 70 | success | the **source tree** is byte-identical outside `docs/` to code commit `e063113` (`git diff e063113 812bb3c -- . ':!docs'` is empty). This run therefore executed every suite on the exact code tree. It is a run on a documentation-only commit, not a run on the code commit `e063113` |

**Reading these together:**
- No run on the **code commit** `e063113` itself is fully green.
- The **identical code tree** passed every job once (40720405660).
- The same tree failed one integration test once (40715437694, G-05).
- One run was unable to start its integration tests (40711057141).
- The workflow has no path filter, so documentation commits run every suite.

## 6. Findings from this preparation

| Id | Severity | Evidence | Impact | Next action |
|---|---|---|---|---|
| R-1 | LOW (documentation) | report §12.2 cites spec line numbers from before the import edit; each is off by +1 in the five spec files (script check, 2026-10-30). The `support/`, `global-setup.ts` and `csp.spec.ts` citations are correct | a reviewer following the citations lands one line early | corrected in §3 of this package; noted in the report (§12.8); the original text is kept |
| R-2 | INFO (process) | an earlier guard mutation (`files: []`) made all five guard tests fail, which is a config error, not discrimination; redone by removing the rule block | none on the code; the evidence is now clean | none |
| G-04 | LOW (test design) | §7 | intermittent false failure of the mock suite | separate branch: change the test, not the product |
| G-05 | MEDIUM (unknown cause; financial-reconciliation test) | §7 | an unexplained failure in a reconciliation integration test | separate investigation with CI log access; no change to reconciliation code without its own task |

## 7. G-04 and G-05

**G-04: `e2e/discovery.spec.ts:220` "poor connectivity … nothing is queued" (mock suite).** Classification: a **test-design
flaw, established**. Not a product defect.
- **Mechanism.** The page sends one `CAMPAIGN_VIEW` event from a React `useEffect` (`TrackCampaignView`), after hydration.
  The test attaches its request listener when the server-rendered heading is visible, then goes offline. If hydration
  finishes later, the event's first and only attempt is recorded, and the test reads it as a "replay".
- **Reproduced (local, 2026-10-30).**
  - With Chromium CPU throttling ×6 and ×20 (CDP `Emulation.setCPUThrottlingRate`, in a temporary copy of the spec that
    was not committed): **20/20 failed** with the same assertion as the original. Unthrottled: 10/10 passed.
  - The captured request's body was `CAMPAIGN_VIEW` with source `DIRECT`, sent about 0.5 s after the listener was attached,
    while offline.
  - Earlier: 1 failure in a full suite; 80/80 isolated and 180/180 parallel passes.
- **Product behaviour.** Matches "nothing is queued": the event is attempted once and not retried.
- **Recommendation (separate branch, not done).** Let the test wait for the view event (or for hydration) before going
  offline, or count only requests issued after reconnecting. The application is unchanged. Evidence:
  `evidence/g03/review-prep-2026-10-30/`.

**G-05: `TestReconciliationPaymentsEndToEnd` failed once in CI run 40715437694.** Classification: **cause unknown.**
- **Assertion.** `reconciliation_test.go:572` "simulator line for pay2 not matched: 0".
- **History.**
  - Passed in CI on the same tree in runs 40654287677, 40657724785, 40672295511, 40675849219 and 40720405660.
  - In Stage 19 it failed differently locally (an execute-run timeout, KI-S19-17).
- **Local reproduction attempts** (disposable `fundzim-r13` at `812bb3c`, 2026-10-30):
  - 20 isolated repeats: **20/20 passed**;
  - full integration suite (no perf opt-ins): **245 passed, 0 failed, 18 skipped**, with `TestReconciliationPaymentsEndToEnd` passing (7.8 s). The 10 skips beyond CI's 8 are the `FUNDZIM_IT_PERF` tests and two tests needing `FUNDZIM_IT_API_URL`/`FUNDZIM_IT_WEB_URL`, none in reconciliation. **Not reproduced (21 local executions).**
- **Not established.** Without the CI job log the failing run's data and timing are unknown. Hypotheses, none verified:
  - the sandbox statement window boundary;
  - de-duplication of a line for an already-matched payment;
  - run behaviour with lines from earlier tests in the window;
  - the compose worker's concurrent runs.
- **Not labelled environmental or harmless.**
- **Next.** A separate task with CI log access (an authenticated token), or repeated full-suite runs to reproduce.
  Reconciliation code is not changed here.

## 8. Governance state (verified 2026-10-30; not changed)

| Item | State |
|---|---|
| D-01 branch protection | **not configured**: `main` `protected: false`, 0 rulesets, 0 rules (public API; the protection endpoint needs a token) |
| D-01a independent reviewer | **none named** |
| D-15 review scope of the full integration range | **open**. Reviewing G-03 does not review Stages 1–20 or remediation Phases 1–6, and approval of G-03 is not approval of the integration range |
| G-02 | fixed on `remediation/g02-fee-draft-notice`; **not reviewed, not closed** |
| G-03 | fixed on the two G-03 branches; **not reviewed, not closed** |
| G-04, G-05 | **open** (§7) |
| Production financial operations | **blocked**: all financial gates off; launch blockers B-01…B-17 open; no owner authorisation |

## 9. Reviewer checklist

**Outcome columns.** "Author's check" is Claude Code's own verification and is not independent. The "Reviewer" column
must be completed by the independent reviewer: PASS / FAIL / NOT REVIEWED.

| # | Check | Author's check | Reviewer |
|---|---|---|---|
| 1 | The fixture wraps every Playwright API client the suites use (§3), with no unwrapped path except the documented `route.fetch()` and the control test | done (§3, scripts) | NOT REVIEWED |
| 2 | Extra contexts and `storageState` contexts are covered (worker-scoped `browser` override; every `staffPage`/`ownerContext` call passes the fixture `browser`) | done | NOT REVIEWED |
| 3 | The built-in `request` fixture is covered | done | NOT REVIEWED |
| 4 | The import guard covers `e2e/**/*.spec.ts` and `e2e-real/**/*.spec.ts`, and leaves helpers, configs and unit tests alone | done | NOT REVIEWED |
| 5 | Regression tests fail when the protection is removed (mock and real-stack) and when the guard is removed | done (local mutations, §5) | NOT REVIEWED |
| 6 | No retries were introduced (no `maxRetries`; one request reaches the server per call) | done | NOT REVIEWED |
| 7 | Browser and interception traffic are unchanged | done (browser-traffic tests, `route.fetch()` probe) | NOT REVIEWED |
| 8 | No application, Go, migration or financial file is in the range | done (§1) | NOT REVIEWED |
| 9 | The `route.fetch()` rationale matches current use, and its limitation is stated | done (§4) | NOT REVIEWED |
| 10 | The 27 existing-spec edits change only imports | done (0 non-import lines) | NOT REVIEWED |
| 11 | CI mapping (§5) is accurate, and no run is overstated | done | NOT REVIEWED |
| 12 | Re-run `npx playwright test e2e/request-connections.spec.ts`, `npx vitest run src/__tests__/e2e-import-guard.test.ts`, and on a disposable stack `npx playwright test -c playwright.real.config.ts request-connections.spec.ts` | — | NOT REVIEWED |
| 13 | Decide whether R-1, G-04 and G-05 block G-03 closure (recommendation: they do not, as they are separately tracked) | — | NOT REVIEWED |

Record the outcome with the template in `independent-review-process.md` §5, in `docs/governance/reviews/`, through a
reviewed pull request.
