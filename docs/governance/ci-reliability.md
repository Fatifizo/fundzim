# CI reliability: the browser-test job (Work Package E)

## 1. Evidence

Step durations from the GitHub API, 16 runs from Stage 18 to Phase 4, plus this phase's run:

| Run (commit) | `Web end-to-end (Playwright)` job | Browser + system-package install | `npm run test:e2e` |
|---|---|---|---|
| 39984705098 … 40310152518 (14 runs, `801babe` … `1e167b1`) | 180–551 s (cancelled ones superseded by a push) | **19–34 s** | 139–505 s |
| 40353861155 (`f7d1cf7`) | 1 217 s, **cancelled at the 20-minute limit** | **767 s** | 432 s (cut off) |
| 40358498063 (`854e824`) | 1 188 s (19 m 48 s), success | **686 s** | 479 s |
| **40461811832 (`78b9908`, this phase)** | **512 s, success** | **13 s** (system packages) + **10 s** (browser) | 466 s |

The `Real-stack E2E` job (60-minute limit) took 777–1 377 s throughout; its install is part of a combined step (43 s in this
phase's run).

## 2. Findings

| Question | Finding |
|---|---|
| Browsers installed on every run? | yes. There is no browser cache; the npm cache is restored by `setup-node` |
| Install and tests coupled? | install and test were separate steps already; `--with-deps` coupled the system packages (apt) with the browser download |
| Genuinely slow tests? | no. Test execution is stable (340–505 s). The near-timeout came only from the install, in the two runs of 2026-10-26 ~16:00 UTC. Job logs need authentication, so which part (apt mirror or browser CDN) was slow is **not determinable** from the evidence |
| Serialised tests? | no: `fullyParallel: true`, two projects (desktop and mobile Chromium) |
| Flaky tests or races? | none observed in the browser job; `retries: 0`, so nothing is masked. KI-R1-10 (an earlier web unit-test race) is fixed and has not recurred |
| Reproducible versions? | yes: `@playwright/test` 1.63.0 pinned in `package-lock.json`; Node 24.21.0; actions pinned by SHA |
| Cancellation behaviour | branch runs are superseded by newer pushes (intended); `main` runs are never cancelled (Phase 2). A job timeout also reports "cancelled", as in 40353861155 |
| Reports and traces after a failure | **were lost**: an HTML report and traces were generated but never uploaded |

## 3. Changes (commit `78b9908`; no test semantics changed)

1. `npx playwright install --with-deps chromium` becomes `npx playwright install-deps chromium`, then
   `npx playwright install chromium`, in both browser jobs. The same packages and browser are installed. The next slow run
   will show which component is slow.
2. The web e2e job uploads `playwright-report/` and `test-results/` when it fails (14 days; the action already pinned in the
   workflow). It uses the mock API only, so no real credentials are involved.

**Deliberately not done.**
- **Timeout increase.** It would hide a problem.
- **Browser cache.** It would add a third-party action, and it is only useful if the browser download is the slow part, which
  is unproven.
- **Artifact upload for the real-stack job.** Its `.state` folder holds saved logins, and its traces carry session data of the
  CI stack. That needs a decision before such artifacts are published on a public repository.

Validation: `internal/deploytest` passes (least privilege, pinned actions, unchanged job names); the workflow parses as YAML;
both Playwright commands exist in 1.63.0 (dry runs); CI run 40461811832 passed 8/8.

## 4. Remaining risk

The install slowdown is external and may recur. The job now fails or passes with clear step timings and keeps its report. If
it recurs on the browser download, a pinned browser cache becomes justified. If it recurs on apt, a runner image with the
dependencies preinstalled is the next option.

**Gate E: PASS WITH CONDITIONS.** The condition is to watch the next runs; one green run is not proof of stability.
