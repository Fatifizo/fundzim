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
