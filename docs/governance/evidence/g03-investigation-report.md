# G-03 investigation report — e2e "socket hang up" while creating a mock user

| Item | Value |
|---|---|
| Date | 2026-10-28 |
| Repository | `/home/administrator/projects/fundzim` (remote `Fatifizo/fundzim`) |
| Branch | `remediation/g03-e2e-socket-hangup`, created from `d0be53e` (head of `remediation/g02-fee-draft-notice`) |
| Commits | `02aac0e` (fix), `8a293df` (report), `bdbc607` (coverage of the `request` fixture, browser-traffic test, lint guard), then the verification report commit |
| Untouched | `main` (`a552de2`), `remediation/integrity-status` (`d3d45c6`), G-02 commits `d83d1ac` and `d0be53e` (preserved), every database. No merge, no deployment |
| Author | Claude Code. **Not independently reviewed** |
| Status | **FIXED ON THE BRANCH — NOT REVIEWED** (§9) |

Evidence files are in `evidence/g03/` with `SHA256SUMS`. The final verification of 2026-10-28 is §11; its files are in
`evidence/g03/verification-2026-10-28/`.

## 1. Original failure

From CI run **40521729530** on `d83d1ac`, job `Web end-to-end (Playwright)` (`evidence/g03/ci-40521729530-d83d1ac-web-e2e.txt`).

| Item | Value |
|---|---|
| Failing step | `Run npm run test:e2e` (456 s); 220 passed, 1 skipped, **1 failed** |
| Failing test | `[desktop-chromium] e2e/verification.spec.ts:504` "new pages keep the nonce-only CSP with no violations" |
| Error | `apiRequestContext.post: socket hang up` at `createUser` (`e2e/helpers.ts:52`), called from `verification.spec.ts:543` |
| Request | `POST http://127.0.0.1:3101/api/v1/__mock/users`, JSON, 153 bytes, with the test user's session and CSRF cookies (values redacted) |
| Response | **none**: the call log shows only `→ POST`, no `← response` line. The connection ended before any response header |

**Limits of the evidence.**
- The job log (HTTP 403) and the Playwright report/trace artifact (HTTP 401) need a token. Server logs from that run are
  therefore not available, and the timing of that run's requests could not be read.
- The annotations and step timings are the evidence used.

The next run, **40526376062** on `d0be53e` (`evidence/g03/ci-40526376062-d0be53e-web-e2e.txt`), passed 8/8. It executed
the same test and helper path, since the full mock suite runs on every push. A later pass is **not** treated as evidence
that the failure was transient.

## 2. Request and service lifecycle

1. **Test setup.** Each test gets a new browser context. `page.request` is that context's API client; extra contexts made
   with `browser.newContext()` (10 tests) have their own.
2. **Client.** Playwright 1.63 creates one `http.Agent({ keepAlive: true })` per request context (`_ensureAgent` in
   `playwright-core/lib/coreBundle.js`). Connections are reused across `page.request` calls within a test. Retries on
   connection errors (`maxRetries`) are off by default.
3. **Server.** The "auth app" on port 3101 is the Next standalone server started by `e2e/serve.mjs`. With no
   `KEEP_ALIVE_TIMEOUT` set, it uses Node 24 defaults:
   - it advertises `Keep-Alive: timeout=5`;
   - it closes an idle connection at keepAliveTimeout 5 000 ms + keepAliveTimeoutBuffer 1 000 ms = **6 000 ms**.
4. **Handling.** `/api/v1/[...path]/route.ts` proxies `/api/v1/__mock/*` to the mock API (port 3199) with `fetch`. If the
   mock API fails, the proxy answers 503 or 504 with a JSON body. **It never drops the client connection.**
5. **Failing test timeline.** Taken from a local trace of the same test on `d0be53e`
   (`evidence/g03/csp-test-trace-idle-gaps-d0be53e.txt`):
   - `signInNewUser` sends the first `page.request` POST;
   - the browser then loads six pages and works a dialog, while the request client sits idle;
   - the next `page.request` POST, the call that failed in CI, follows **6 657 ms** of idle time.

   Locally this landed just past the 6 000 ms close, so a fresh connection was opened. On a CI runner, the same gap
   varies with machine speed.

## 3. Reproduction (local only; disposable harness; no shared environment)

All runs used `node e2e/serve.mjs` (mock API and built Next apps on loopback) or probe servers in the test process.

| Experiment | Result | File |
|---|---|---|
| Plain Node (same process), gap sweep around the 6 000 ms close | gaps up to 5 950 ms: connection reused, OK. **6 000 ms: 10/10 `ECONNRESET socket hang up`** on the reused connection. ≥ 6 050 ms: new connection, OK | `node-experiments.txt` |
| Real auth app, Node keep-alive agent, 40 concurrent trials per gap | failures only from 5 960 to 6 000 ms (3/40 rising to 32/40), all on a reused connection (`read ECONNRESET`; `socket hang up` at 6 000 ms). ≥ 6 005 ms: 0 reused, 0 failures | `auth-app-node-agent-fine-sweep.txt` |
| Real auth app, **Playwright's request client** (the client behind `page.request`) | 5 985–6 000 ms: **`apiRequestContext.post: socket hang up`** (the exact CI message) and `read ECONNRESET`. 5 970 and 6 010 ms: 0 failures | `auth-app-playwright-client-sweep.txt` |
| Real auth app, default client vs the fix's per-request `Connection: close` (40 trials × 3 gaps each) | default **53/120 OK** (67 resets or hang-ups); with the fix **120/120 OK** | `auth-app-default-vs-fresh-connection.txt` |

**Failure point.** Every failure happens on a **reused** connection, **before the server reads the request**: the server
has already decided to close the idle connection. No HTTP response exists.

**What it depends on.**
- connection reuse (no failure without it);
- the idle gap landing in a window of roughly 40 ms before the server's close (wider under concurrent load).

It does not depend on server startup or on shared test state.

The CI test's own gap (6.7 s locally) is close to the 6 s boundary. A runner on which those six page loads took about
6.0 s would hit the window.

## 4. Root-cause determination and confidence

**Cause.**
- Playwright's request client reuses idle keep-alive connections up to the server's actual close.
- The apps under test close idle connections at 6 s.
- A test-setup request sent in the tens of milliseconds before that close races it and fails with
  `socket hang up` / `ECONNRESET`, without reaching the server.

**Confidence.**
- **High** that this mechanism exists in the e2e harness and produces exactly the CI error. Reproduced with Playwright's
  own client against the real auth app.
- **High-moderate** that it caused the failure in run 40521729530. The signature matches exactly: the same message, no
  response, a single failure in an otherwise healthy run, and the request follows an idle gap of about 6 s. The run's
  server log and trace are not available to confirm its exact timing.

## 5. Alternatives considered

| Hypothesis | Verdict | Evidence |
|---|---|---|
| Service not ready | rejected | earlier requests in the same test succeeded; 220 tests passed against the same servers |
| Server crashed or restarted | rejected | every later test on port 3101 passed in the same run |
| Upstream (mock API) failure | rejected | the proxy turns upstream failures into 503/504 responses, never a dropped connection |
| Shared mutable state, parallel tests | rejected | the error is transport-level with no response; unique emails per call |
| Timeouts or resource limits | rejected | Node `requestTimeout` 300 s and `headersTimeout` 60 s; the failure is immediate |
| Helper misclassifying a server failure as a network failure | rejected | no HTTP response existed; the helper asserts `201` and would report a status |
| Keep-alive race | **confirmed** | §3 |

## 6. Fix options and the change made

| Option | Decision | Reason |
|---|---|---|
| Raise or disable the server keep-alive (`KEEP_ALIVE_TIMEOUT`) in the harness | rejected | moves the window rather than removing it. Tests run up to 150 s (`test.setTimeout(150_000)`); Node's 60 s `headersTimeout` interacts; `KEEP_ALIVE_TIMEOUT=0` is ignored by Next (`if (keepAliveTimeout)`) |
| `Connection: close` via `use.extraHTTPHeaders` | rejected | measured: Chromium honours it too, so browser traffic would change (6 connections for 6 requests instead of 2). It would alter what the browser tests exercise |
| Retry (`maxRetries`) | rejected | retrying a POST needs proof it was not processed. A retry would also hide a persistent transport fault |
| **Fresh connection per test-setup request** | **chosen** | removes the precondition (reuse) entirely. Affects only the request client. No retry |

**Changes (`02aac0e`, test infrastructure only).**
- **`apps/web/e2e/helpers.ts`:** a fixture-extended `test` sends every `page.request` / `context.request` call with
  `Connection: close`. It covers the default context, and every `browser.newContext()` through a worker-scoped `browser`
  fixture.
  - It wraps the request client's `fetch`, which `post`/`get`/`put`/`patch`/`delete`/`head` all use.
  - The fixture parameter is named `provide`, because the React hooks lint rule misreads a parameter called `use`.
- **All 19 existing specs:** `test` is imported from `./helpers` instead of `@playwright/test`. This is a one-line import
  change each; no test body or assertion changed.
- **`apps/web/e2e/request-connections.spec.ts`** (new): the regression tests (§7).
- **`bdbc607`:**
  - the built-in `request` fixture, used by 6 tests, is covered too;
  - a test proves browser traffic is unchanged;
  - a lint guard is added (§11).
- **Untouched:** no application code, server configuration, financial code or CI workflow changed. Every assertion of
  every existing test is unchanged.

**Not covered.** `route.fetch()` in `csp.spec.ts` uses Playwright's internal path, which does not go through `fetch`. It
fetches immediately on interception, not after an idle gap.

## 7. Regression tests and results

**`e2e/request-connections.spec.ts`** (runs on desktop and mobile). Its probe server lives in the test process, with a
2 s idle close and no grace buffer. Below 1 s, the advertised `timeout=0` stops clients reusing connections, which would
hide the condition (measured, `node-experiments.txt`).

1. Test-setup requests from the default context, a `browser.newContext()` context **and** (from `bdbc607`) the built-in
   `request` fixture:
   - back-to-back requests and requests sent at the idle close all return 201;
   - every request uses its own connection;
   - every request carries `Connection: close`.
2. A 500 from the server is returned as a 500, sent **once**: no retry, no hidden failure.
3. Control: an unprotected client **does** reuse connections. This proves the probe detects reuse.
4. (From `bdbc607`) The browser's own page loads keep their keep-alive connections: no `Connection: close`, and the
   connection is reused.

**Results.**

| Run (local) | Result | File |
|---|---|---|
| Before the fix (spec on the unchanged harness) | test 1 **failed** on both projects ("every request on its own connection"); 2 and 3 passed | `regression-spec-before-fix.txt` |
| Mutation: fixture present, header removed | test 1 **failed** on both projects | `regression-spec-mutation-no-header.txt` |
| After the fix | 6/6 passed | `regression-spec-after-fix.txt` |
| Regression spec ×10, both projects | **60/60** | `regression-spec-repeat10-02aac0e.log.txt` |
| The originally failing CSP test ×30, both projects | **60/60** | `csp-test-repeat30-02aac0e.log.txt` |
| Full mock e2e suite | **227 passed, 1 skipped** (the existing mobile-only test; 221 before + 6 new) | `e2e-full-02aac0e.log.txt` |
| Web unit suite | 582/582 | — |
| Lint, typecheck, production build | clean | — |

Local results are not GitHub CI. The race depends on runner timing, so local repeats show the fixed path is stable but
cannot by themselves prove the absence of a race in CI. The regression test therefore asserts the deterministic
precondition (no connection reuse) rather than timing.

## 8. CI

| Run | Commit | Status |
|---|---|---|
| 40521729530 | `d83d1ac` | failure 7/8 (the original G-03 failure) |
| 40526376062 | `d0be53e` | success 8/8 (same path; failure not hit) |
| **40654287677** | `02aac0e` (fix) | **success 8/8**; web e2e 227 passed, 1 skipped (8.2 min), seen via the API |
| 40657724785 | `8a293df` (report) | success 8/8; web e2e 227 passed, 1 skipped (7.9 min) |
| 40672295511 | `bdbc607` | success 8/8; web e2e 229 passed, 1 skipped (6.4 min); the Web job runs lint (the guard) and the guard test |

## 9. Status, risks and review

**G-03: FIXED ON THE BRANCH — NOT REVIEWED. NOT CLOSED.**
- The mechanism is reproduced, and the fix removes its precondition, verified by red→green tests, mutation checks and
  real-app comparisons.
- CI on the fix commits passed (§8). Green runs are not proof that an intermittent race is gone; the regression test
  asserts the precondition instead.
- Closing needs independent review.

**Remaining risks** (updated by §11).
1. Attribution of the specific CI failure rests on its signature, not on its server log (§4).
2. **Mitigated for `e2e/`:** the lint guard (§11.3) rejects importing `test` from `@playwright/test` in `e2e/**/*.spec.ts`.
   It does not cover `e2e-real/`.
3. **The real-stack suite is exposed and unprotected (§11.4).**
   - Its web container closes idle connections at the same 6 s, and Playwright's client reproduces the hang-up there.
   - Its specs use `page.request`, the `request` fixture and `browser.newContext()` without the fixture.
   - No real-stack failure of this kind has been observed. Extending the fixture there is a recommended follow-up, not done
     here.
4. `route.fetch()` is not covered (§6).
5. Wrapping the request client's `fetch` relies on Playwright's public method. A Playwright upgrade must keep
   `request-connections.spec.ts` green.

**Independent-review requirements.**
- A reviewer who is not the author account (D-01a).
- Read `helpers.ts` (the `context`, `request` and worker-scoped `browser` fixtures; the `WeakSet` guard), the regression
  spec, the lint rule and its test.
- Confirm that the 19 import changes alter no test body (§11.2 gives the mechanical check).
- Re-run `npx playwright test e2e/request-connections.spec.ts` and `npx vitest run src/__tests__/e2e-import-guard.test.ts`.
- Decide on risk 3: extend the fixture to `e2e-real/`, or accept the risk.

The record template is in `docs/governance/independent-review-process.md` §5.

## 10. Governance boundaries respected

- No merge, release or deployment, and no change to `main`, branch protection or settings.
- No migration and no shared-database access. The investigation used the local e2e harness with the in-memory mock API
  only.
- No payment, payout, ledger or fee code changed.
- Cookie values from the CI annotation are redacted.
- Phase 7 not started.

## 11. Final verification (2026-10-28)

### 11.1 Repository and CI

- Branch `remediation/g03-e2e-socket-hangup`; `02aac0e` and `8a293df` present; the tree was clean and equal to `origin`
  before this step.
- CI is in §8. Run 40657724785 on `8a293df`, unknown when the first report was written, completed **success 8/8**.

### 11.2 The shared fixture (`e2e/helpers.ts`)

| Property | Verified how | Result |
|---|---|---|
| Default context's API client (`page.request` = `context.request`) | regression test 1 | fresh connection, `Connection: close` |
| Extra contexts (`browser.newContext()`, 10 tests) | regression test 1 | covered |
| Built-in `request` fixture (6 tests) | found uncovered in this review; covered in `bdbc607`; test 1; mutation (override removed → test 1 fails on both projects) | covered |
| Browser navigation and browser traffic unchanged | new test 4: page loads carry no `Connection: close` and reuse the connection | unchanged |
| No retry of POST requests | test 2: a 500 arrives once | no retry |
| Server errors preserved | test 2 returns the 500; helpers assert `201` | preserved |
| Used by every spec | each of the 20 specs imports `test` from `./helpers` (parsed per file) | 20/20 |
| The 19 import edits change only imports | every changed line in `git diff d0be53e..02aac0e` of the 19 specs is an import line (script check) | 0 non-import lines |

Not covered:
- `browser.newPage()` (unused);
- `request.newContext()` from the `@playwright/test` module (used only by the control test);
- `route.fetch()` (§6).

### 11.3 Direct-import guard

`eslint.config.mjs`, `no-restricted-imports` for `e2e/**/*.spec.ts`, forbids importing `test` from `@playwright/test`
(including `test as x`), with a message pointing to `./helpers`. ESLint already runs in CI (`npm run lint`).
`src/__tests__/e2e-import-guard.test.ts` lints sample sources with the repository's config:
- direct and aliased imports are rejected;
- the helpers `test` with `expect` and types from `@playwright/test` is accepted;
- `e2e/helpers.ts`, `playwright.config.ts` and Vitest test files are not affected.

Mutation check: with the rule removed, the guard test fails. All current specs pass the rule.

Not covered: specs outside `e2e/**/*.spec.ts`, notably `e2e-real/`. That suite does not use the fixture at all.

### 11.4 Real-stack coverage

| Question | Finding |
|---|---|
| Same API client? | yes: `page.request` (staff-ledger, staff-moderation, staff-reconciliation, support/browser), the built-in `request` fixture (public, discovery) and `browser.newContext()` (featured, staff-moderation) |
| Imports the shared fixture? | no: every `e2e-real` spec imports `test` from `@playwright/test` |
| Setup requests outside Playwright's client? | yes. Provisioning (`support/http.ts`, global setup) uses Node's built-in `fetch` (undici). It had 0 failures in sequential trials at gaps 3 900–6 000 ms (`real-stack-dev-web-healthz-undici-sweep.txt`) |
| Same connection lifecycle? | yes. `fundzim-web` is Next standalone on Node 24.21 with no `KEEP_ALIVE_TIMEOUT`. Through Docker's port publishing it advertises `Keep-Alive: timeout=5` and closes idle connections at 6 s. A Node keep-alive agent failed 17/40 at 5 990–6 000 ms (`real-stack-dev-web-healthz-node-agent-sweep.txt`, read-only `/healthz` of the development stack, which uses no API or database) |
| Does Playwright's client hit it there? | yes. On the disposable `fundzim-r11` web container (`bdbc607`): default **45/60 OK, 15 × `apiRequestContext.get: socket hang up`**; with `Connection: close` **60/60 OK** (`real-stack-r11-web-healthz-playwright-default-vs-close.txt`) |
| Does the regression test exercise the real-stack path? | no. It runs only in the mock suite. **The mock-suite tests do not prove real-stack coverage** |
| Real-stack suite run | disposable compose project `fundzim-r11` built from `bdbc607` (isolation: `isolation-fundzim-r11-bdbc607.txt`), lock held. The first attempt stopped in global setup before any test: the bootstrap script's `docker compose exec` targeted the stopped development project ("service fundzim-postgres is not running"), so it changed nothing. Re-run with `COMPOSE_PROJECT_NAME=fundzim-r11`: **64 passed (10.6 min)** (`e2e-real-r11-bdbc607.log.txt`). This is a baseline: those specs do not use the fix |
| Environment afterwards | `fundzim-r11` removed with its volumes (0 left). The development project was restarted without rebuilding: image IDs unchanged, "no pending migrations", still at `20261025090000`, 9/9 healthy. `e2e-real/.state` was backed up before the run and restored afterwards (`teardown-and-dev-restore.txt`) |

### 11.5 Local results in this step (`bdbc607` tree; not GitHub CI)

| Command (in `apps/web`) | Result |
|---|---|
| `CI=1 E2E_SKIP_BUILD=1 npx playwright test e2e/request-connections.spec.ts` (after) | 8/8 (4 tests × desktop, mobile) |
| same with `--repeat-each=10` | **80/80** |
| same with the `request` override removed (mutation) | test 1 failed on both projects |
| `npx playwright test e2e/verification.spec.ts:505 --repeat-each=30` | **60/60** |
| `CI=1 E2E_SKIP_BUILD=1 npx playwright test` (full mock suite) | **229 passed, 1 skipped** (the existing mobile-only test) |
| `npm test` | 585/585 (62 files) |
| `npx vitest run src/__tests__/e2e-import-guard.test.ts` (and its mutation) | 3/3 (mutation: 1 failed) |
| `npm run lint`, `npm run typecheck`, `npm run build` | clean |
| `npm run test:e2e:real` on `fundzim-r11` | 64 passed |

### 11.6 Distinctions

| Claim | Status |
|---|---|
| Root-cause mechanism | **reproduced locally**, against the mock harness and the real-stack web container |
| Exact original CI event linked to it | **not conclusively.** Strong signature match, but the run's log and trace need a token |
| Local test results | §7, §11.5 |
| GitHub CI results | §8 |
| Mock-API coverage | fixed, guarded, regression-tested |
| Real-stack coverage | **exposed, not fixed**; baseline run passed |
| Independent review | **none** (no reviewer, D-01a) |
