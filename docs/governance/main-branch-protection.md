# `main` branch protection — specification (owner or administrator applies)

**Status: NOT ACTIVE (verified 2026-10-27).** `GET /repos/Fatifizo/fundzim/branches/main` returns `protected: false`;
rulesets and branch rules are `[]`. This document is a proposal. Nothing here has been applied, and Claude Code cannot apply
it (no administration credential).

It refines `docs/remediation/phase-2/branch-protection.md` (2026-10-26): the same proposal, now with explicit owner decisions,
availability notes and a test procedure.

## 1. Recommended default policy vs owner decisions

| # | Control | Recommended default | Owner decision needed? |
|---|---|---|---|
| 1 | Pull request required before merging into `main` | yes | no (prerequisite of D-01) |
| 2 | Approvals from someone other than the author | **at least 1** (GitHub never counts the author's own approval) | **yes: number of approvals**, and 2 for financial paths (§4) |
| 3 | Dismiss stale approvals when new commits are pushed | yes | no |
| 3a | Require approval of the most recent push (`require_last_push_approval`) | yes: the person who pushed last cannot be the approver | no |
| 4 | Required status checks | the 8 CI jobs of §3; strict (branch up to date with `main`); source pinned to GitHub Actions | no |
| 5 | No bypass for ordinary contributors | bypass list empty | no |
| 6 | Block force pushes and deletion of `main` | yes | no |
| 7 | Administrators | rules **enforced for administrators** (no standing bypass); emergency path §6 | **yes**: whether to allow an audited emergency bypass at all |
| 8 | Conversation resolution required | yes | no |
| 9 | Self-review prevention | items 2 and 3a, plus a named reviewer who is not the author (D-01a). Claude Code commits as the owner's account (`Fatifizo`), so a PR by that account **cannot** be approved by that account | **yes: D-01a, who reviews** |
| 10 | Scrutiny of workflows, migrations, release tooling, financial controls | CODEOWNERS (§4) with "require review from code owners" | yes (names) |
| 11 | Emergency exceptions | §6 | yes |
| 12 | Merge method | merge commits only (keeps every stage commit and its CI evidence addressable) | yes |
| 13 | Signed commits | not required now (D-12: artifact attestation is the proposed integrity control) | yes (D-12) |

## 2. Availability

| Feature | Availability |
|---|---|
| Rulesets and classic protection on a **public** repository | documented by GitHub as available on all plans; **not verified for this account without a token** |
| `integration_id` pinning of a required check (GitHub Actions app id `15368`) | supported in rulesets; **confirm in the UI** (the check's source shows "GitHub Actions") |
| Ruleset insights / audit of bypasses | available in repository settings; not verified |
| Collaborator list (who can review) | **not verified** (requires authentication) |

## 3. Required checks: real job names of `.github/workflows/ci.yml`

| Required check (exact) | Workflow job id |
|---|---|
| `Go (format, vet, unit tests, vulnerabilities)` | `go` |
| `Web (lint, types, unit tests, build, audit)` | `web` |
| `Web end-to-end (Playwright)` | `e2e` |
| `OpenAPI and SQL design drafts` | `contracts` |
| `Integration (real PostgreSQL, Valkey, Garage, ClamAV via docker compose)` | `integration` |
| `Real-stack E2E (compose + Playwright)` | `e2e-real` |
| `Container images (build, SBOM, vulnerability scan, provenance)` | `images` |
| `Secret scanning` | `secrets` |

`internal/deploytest` `TestWorkflowRequiredChecks` fails if a job is renamed or a second workflow is added. Change the ruleset
and that list together.

## 4. Path-based scrutiny (proposed CODEOWNERS; add only with real names, D-01a)

```
# .github/CODEOWNERS — proposal; @<reviewer> placeholders must be replaced by real, available accounts
.github/                      @<repo-admin> @<independent-reviewer>
migrations/                   @<independent-reviewer> @<db-reviewer>
internal/ledger/              @<independent-reviewer> @<finance-reviewer>
internal/payments/            @<independent-reviewer> @<finance-reviewer>
internal/payouts/             @<independent-reviewer> @<finance-reviewer>
internal/fees/                @<independent-reviewer> @<finance-reviewer>
internal/reconciliation/      @<independent-reviewer> @<finance-reviewer>
internal/platform/reconsweep/ @<independent-reviewer> @<finance-reviewer>
scripts/release/  scripts/backup/   @<repo-admin> @<independent-reviewer>
```

No CODEOWNERS file is created by this phase: an entry with an invented account would be fabricated governance.

## 5. How the owner applies it

**UI.**
1. Settings → Rules → Rulesets → New branch ruleset.
2. Name `main-protection`, enforcement **Active**, bypass list **empty**, target: Include default branch.
3. Enable: Restrict deletions; Block force pushes; Require a pull request before merging (approvals per §1 item 2; dismiss
   stale approvals; require approval of the most recent reviewable push; require conversation resolution; require review
   from Code Owners once CODEOWNERS exists).
4. Enable Require status checks to pass (Require branches to be up to date), and add the 8 checks of §3, each with source
   **GitHub Actions**.
5. Then Settings → General → Pull Requests: allow merge commits; disable squash and rebase merging.

**API** (owner token with `administration: write`). This is the JSON of `docs/remediation/phase-2/branch-protection.md` §4,
plus `"require_code_owner_review": true` once CODEOWNERS exists.

## 6. Emergency exception (only if the owner allows one)

There are no standing bypass actors. In an emergency:
1. The owner sets the ruleset to *Disabled*, makes the change, and re-enables it within the same session.
2. The owner records an incident entry: reason, commit, start and end time, who did it.
3. The owner re-reads the rules (§7) and attaches the output.
4. A retrospective review of the emergency commit by an independent reviewer follows.

GitHub's ruleset history and the account security log record the change.

## 7. Verification after configuration (anyone; no token needed)

```bash
curl -s https://api.github.com/repos/Fatifizo/fundzim/branches/main | jq .protected            # expect true
curl -s https://api.github.com/repos/Fatifizo/fundzim/rules/branches/main | jq '[.[].type]'      # expect deletion, non_fast_forward, pull_request, required_status_checks
curl -s https://api.github.com/repos/Fatifizo/fundzim/rules/branches/main \
  | jq '.[] | select(.type=="required_status_checks") | .parameters.required_status_checks[].context'   # expect the 8 names of §3
```

Functional tests:
1. Push an empty commit to `main` from a throwaway clone. It must be **rejected**; record the message.
2. Open a test PR as the author and try to approve it as the author. GitHub must refuse.
3. With a required check failing or missing, the PR must not be mergeable.

Record the outputs in the owner decision register (D-01), with date and operator.

## 8. Owner and administrator checklist

- [ ] D-01a: name the independent reviewer(s) (not the author account `Fatifizo`) and grant them write access.
- [ ] Decide the number of approvals (default 1; 2 for financial paths via CODEOWNERS).
- [ ] Decide whether an audited emergency bypass is allowed (§6).
- [ ] Apply §5 and the merge-method setting.
- [ ] Add CODEOWNERS with real accounts (a reviewed PR once protection is active).
- [ ] Run §7 and record the evidence.
