# Governance gate and independent-review preparation — report (2026-10-27)

**INDEPENDENT REVIEW: PREPARED, NOT REVIEWED.** **CONTROLLED INTEGRATION: BLOCKED.** **PRODUCTION FINANCIAL OPERATIONS:
BLOCKED.**

This was a governance and review-preparation step after remediation Phase 6. It changed documents only. No Phase 7 work,
no code, no migration, no merge, no setting.

## 1. State verified (read-only)

| Item | Result | How |
|---|---|---|
| Repository | `/home/administrator/projects/fundzim`; remote `Fatifizo/fundzim` (public), default branch `main` | git, public API |
| Branch, HEAD, tree | `remediation/integrity-status`, `dfaa53b`, clean, equal to `origin` | git |
| Phase 6 commits | `019b245`, `b9496c3`, `dfaa53b` present, in order | `git merge-base --is-ancestor` |
| `main` | `a552de2` (Stage 0); contains none of Stages 1–20 or the remediation; ancestor of HEAD; 115 commits behind | git |
| Protection | `protected: false`; rulesets `[]`; branch rules `[]`; 0 PRs; no CODEOWNERS. The protection endpoint itself needs a token (401): collaborators and bypass are **not verified** | `evidence/governance-api-check-2026-10-27.txt` |
| CI | 40472554403 (`b9496c3`) success 8/8; 40476468316 (`dfaa53b`) success 8/8, the latest run | public API |
| Release-candidate manifest | `dfaa53b`: `ELIGIBLE_FOR_REVIEW`; `BUILT_BY_CI`; `UNSIGNED_NOT_VERIFIABLE`; `NOT_AUTHORISED` | `evidence/rc-manifest-dfaa53b-run-40476468316.json` |
| Shared development database | at `20261025090000` | read-only `goose_db_version` query |
| Pending migrations | `20261026120000`, `20261027090000`, `20261028090000`: present in the repository, not applied | `ls migrations`, the query above |
| Running app vs schema | compatible: images built from `07e5965` (newest migration `20261025090000`); API and worker `/readyz` ok, including the worker's migrations check | HTTP |
| Evidence integrity | `sha256sum -c` passes in `docs/remediation/phase-{1,2,3,4,6}/evidence/` and `docs/stage-20/evidence/` | shell |

GitHub access was the unauthenticated public API. Nothing was worked around: what needs a token is reported as not verified.

## 2. Documents inspected

All named documents exist. Phase 5's documents are in `docs/governance/` (there is no `docs/remediation/phase-5/`). The
8 required checks in `main-branch-protection.md` equal the 8 job names of `.github/workflows/ci.yml`.

Stale after Phase 6, and refreshed in this step:
- `controlled-integration-plan.md`: candidate `854e824` → `dfaa53b`; 52 → 53 migrations; Phases 1–5 → 1–6; regression
  evidence, manifest, migration rules.
- `reconciliation-integrity-review-packet.md`: new §D6 (Phase 6, migration `20261028090000`); A-1…A-4 statuses. A-4 and the
  D3 limitation are recorded as **still open**.
- `independent-review-process.md`: Phases 1–6 and Stages 1–20 unreviewed; migration list.
- `owner-decision-register.md`: D-15 added; D-06 names the integrity staleness default; D-13 lists Phases 1–6.

## 3. Added

- `review-scope-phases-1-6.md`: the reviewer's entry point. Ranges, production files, testable claims C1–C14 with
  enforcement points and evidence, evidence index with CI runs, re-run commands on a disposable project.
- `integration-checklist.md`: every integration gate with status, evidence and who closes it. It replaces the plan's
  current-state column.
- `evidence/`: the API check and the release-candidate manifest.

## 4. Finding of this step

**G-01 — the review scope is larger than "Phases 1–6"** (decision D-15).
- A merge into `main` carries 115 commits: 91 for Stages 1–20 (≈354 000 added lines) and 24 for remediation Phases 1–6.
- None is independently reviewed, and integration gate 4 requires the whole range.
- Reviewing Phases 1–6 alone cannot unblock integration unless the owner records how the rest is handled.

**G-02 — a pre-existing UI race made CI red on the documentation commit `5fb1931`** (run 40508474321, 7/8).
- **Failure:** `Web end-to-end (Playwright)`: `e2e/staff-fees.spec.ts:50` on mobile-chromium. The status "Draft version
  created." was not visible within 5 s (220 passed, 1 skipped, 1 failed).
- **Cause, found by reading the code:**
  - `VersionForm` (`apps/web/src/components/admin/finance/fee-forms.tsx`) keeps its success notice in the form's own state,
    then calls `router.refresh()`.
  - After the refresh, the policy page (`apps/web/src/app/admin/finance/fees/policies/[id]/page.tsx:71`) sees the new draft
    and **replaces the form**, unmounting the notice.
  - The confirmation exists only between the save and the refresh. Users can miss it, and the test fails when the refresh
    wins.
- **Reproduction:** not reproduced locally. 20 repeats of the spec on mobile-chromium all passed (60/60), on a local
  production build with the mock API.
- **Origin:** present since Stage 12 (`8c2855d`).
- **Disposition:**
  - not fixed here (documents-only step);
  - the commit changed no code, and the same code passed 8/8 on `b9496c3` and `dfaa53b`;
  - the failed run is reported as a failure, and no re-run is claimed (re-running needs a token).
  - **The fix is a small presentation change**: keep the notice above the form, or hand it to the page after the refresh.
    It needs its own reviewed commit.

**G-02 update (2026-10-27): FIXED ON BRANCH `remediation/g02-fee-draft-notice` (`d83d1ac`, from `d3d45c6`). NOT REVIEWED.
NOT CLOSED.**
- **Cause confirmed before the change.** A new e2e assertion waits until the refresh has replaced the form, then requires
  the confirmation. On the unfixed code (`d3d45c6` plus that assertion) it failed on desktop and mobile, 2/2
  (`evidence/g02/e2e-reproduction-before-fix-d3d45c6.log.txt`).
- **Fix (presentation only).**
  - The policy page keeps `VersionForm` mounted and passes `available={!openDraft}`, the pattern `FeeAction` already uses.
  - The notice keeps one position above the form or the "draft already exists" text, so its element, focus and
    live-region announcement survive the refresh.
  - Requests, idempotency, step-up, validation, fee rules and the server's one-draft rule are unchanged. Files:
    `fee-forms.tsx`, `policies/[id]/page.tsx`, `fees-components.test.tsx`, `staff-fees.spec.ts`.
- **Duplicates.** Not possible through this path:
  - the database unique index `uq_fee_policy_versions_one_draft`;
  - one `Idempotency-Key` per body until success;
  - the button disabled while saving.

  The last two are now unit-tested.
- **Local results on `d83d1ac`** (`evidence/g02/`):

  | Check | Result |
  |---|---|
  | Fee unit tests | 12/12 |
  | Mutation check | putting the notice back inside the form makes the new unit test fail |
  | `staff-fees.spec.ts` ×25 on desktop and mobile | 150/150; the G-02 test 50/50 |
  | Full web unit suite | 582/582 |
  | lint, typecheck | clean |
  | Full mock e2e | 221 passed, 1 skipped (the existing mobile-only test) |

  Local runs are not GitHub CI. The race was intermittent there, and the new assertion checks the state after the
  refresh rather than winning a race.
- **CI run 40521729530 on `d83d1ac`: failure, 7/8.**
  - In the web e2e job the fee tests passed on both projects: 220 passed, 1 skipped.
  - The one failure was `verification.spec.ts:504` [desktop]: `socket hang up` on the test helper's mock-user request
    (`e2e/helpers.ts:52`) — **G-03**, below.
  - **No green CI run exists yet for the fix commit.**

**G-03 update (2026-10-28): FIXED ON BRANCH `remediation/g03-e2e-socket-hangup` (`02aac0e`, from `d0be53e`). NOT REVIEWED.
NOT CLOSED.**
- **Cause, reproduced.** A keep-alive race: Playwright's request client reuses idle connections, and the apps under test
  close them at 6 s. A request sent in the ~40 ms before that close fails with `apiRequestContext.post: socket hang up`.
- **Fix.** Test-setup requests use a fresh connection, applied by a fixture in `e2e/helpers.ts`. Test infrastructure only.
- **Tests.** A regression spec that is red before the fix and green after it, with a mutation check.
- **Results.** Local: 60/60 repeats of the regression spec and 60/60 of the originally failing test. CI run 40654287677:
  8/8.
- **Report:** `evidence/g03-investigation-report.md`.

**G-03 — intermittent connection reset in the e2e test helper (as first recorded).**
- **Observed.** In CI run 40521729530, `createUser` (`apps/web/e2e/helpers.ts:52`) got `socket hang up` on
  `POST /api/v1/__mock/users` to the auth app (port 3101). This happened before the page under test was involved.
- **Unrelated to G-02.**
  - The failing test and request never touch fee code.
  - The mock e2e suite does not use the database or real services.
- **Not reproduced and not investigated further here.** A likely class of cause is an HTTP keep-alive connection closed by
  the server while the client reuses it; this is a hypothesis, not established.
- **History.** Not previously recorded in the repository.
- **Needed.** Its own investigation and fix, under review.

## 5. Gate status

| Area | Status |
|---|---|
| Branch protection | **BLOCKED** (D-01) |
| Independent reviewer | **BLOCKED** (D-01a) |
| Review scope | **BLOCKED** (D-15) |
| Review of Phases 1–6 | **NOT REVIEWED**; preparation complete |
| Review of Stages 1–20 | **NOT REVIEWED** |
| Candidate CI | **MET** for `dfaa53b` (8/8); **NOT MET** for the branch head `5fb1931` (7/8, G-02); merge-ref CI not run (no PR) |
| Migration readiness | **NOT REVIEWED**; **BLOCKED** for any shared database (E5, E6) |
| Controlled integration | **BLOCKED** |
| Production financial operations | **BLOCKED** |

## 6. Next actions (external)

1. The administrator applies `main-branch-protection.md` §5 and verifies it with §7 (D-01).
2. The owner names an independent reviewer (D-01a) and decides the scope with them (D-15).
3. The reviewer works from `review-scope-phases-1-6.md` and records outcomes per `independent-review-process.md` §5.
4. Integration then follows `integration-checklist.md` in its stated order.
