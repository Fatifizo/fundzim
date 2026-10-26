# Remediation phase 2 — branch protection for `main` (phase B)

**Implementation status: NOT APPLIED.** No repository setting was changed. Applying protection changes the merge policy, which
is the owner's decision (D-01). The repository was not authorised to change in this conversation. Claude Code also has no
GitHub token on this host: the only credential is an SSH push key, so it cannot read or write repository administration
settings.

## 1. Current configuration (read 2026-10-26, unauthenticated public API)

| Query | Result |
|---|---|
| `GET /repos/Fatifizo/fundzim` | public repository, default branch `main` |
| `GET /repos/Fatifizo/fundzim/branches/main` | `"protected": false`, `protection.enabled: false`, required status checks `enforcement_level: off`, no contexts |
| `GET /repos/Fatifizo/fundzim/rulesets` | `[]` (no repository rulesets) |
| `GET /repos/Fatifizo/fundzim/rules/branches/main` | `[]` (no rule applies to `main`) |
| `GET /repos/Fatifizo/fundzim/collaborators` | `Requires authentication` (the reviewer pool cannot be seen without a token) |
| Workflows | one: `ci` (`.github/workflows/ci.yml`, id 379524151, active) |

Every protection below is therefore **absent**:
- required pull requests and reviews;
- required checks;
- the block on direct and force pushes;
- the block on deletion;
- bypass restrictions;
- conversation resolution;
- signature requirements.

Anyone with write access can push to `main` directly.

## 2. Proposed configuration (minimum effective set)

A single repository ruleset (or classic branch protection; both are available on public repositories) targeting `main`:

| Control | Proposed | Why / trade-off |
|---|---|---|
| Require a pull request before merging | **yes** | All change goes through a reviewable unit with CI on the merge ref |
| Required approving reviews | **1**, and dismiss stale approvals when new commits are pushed | GitHub never lets a PR author approve their own PR. With one maintainer, a required review is **impossible to satisfy** until a second reviewer exists. See decision D-01a |
| Require review from code owners | not yet | There is no CODEOWNERS file, and none is created with invented reviewers. Add it once D-01a names reviewers. Paths needing two reviewers: `internal/{ledger,payments,payouts,fees,reconciliation}`, `migrations/`, `.github/` |
| Required status checks | the 8 job names in §3, **source pinned to GitHub Actions** | Pinning the source app stops another integration from posting a same-named check. `TestWorkflowRequiredChecks` keeps job names and this list identical and fails if a second workflow appears |
| Require branches to be up to date before merging (strict) | **yes** | The checks then ran on the PR merge ref against the current `main`. That is the tree that would be merged (ci-governance.md §2) |
| Require conversation resolution | **yes** | Cheap, auditable |
| Block force pushes | **yes** | History of financial code stays immutable |
| Block deletion of `main` | **yes** | — |
| Restrict direct pushes | **yes** (implied by "require a pull request") | — |
| Apply to administrators / bypass list | **no bypass actors**. "Include administrators" is on | An emergency uses §5, which leaves an audit trail, instead of a silent bypass |
| Require linear history | **no** | Merge commits keep every stage commit and its CI run addressable (integration-plan.md) |
| Require signed commits | **not now** (D-12) | Existing history and the current authoring path are unsigned, so requiring it would block every PR. GitHub signs the merge commits it creates from the UI. Release integrity comes from attestation of the built artifacts (release-candidate-procedure.md §4), not from commit signatures |
| Merge queue | **not now** | Strict up-to-date checks give the same guarantee at today's PR volume |
| Allowed merge methods (repository setting) | merge commit only | Squash or rebase would rewrite stage commits and detach their CI evidence |

## 3. Required check names (exactly as GitHub reports them)

1. `Go (format, vet, unit tests, vulnerabilities)`
2. `Web (lint, types, unit tests, build, audit)`
3. `Web end-to-end (Playwright)`
4. `OpenAPI and SQL design drafts`
5. `Integration (real PostgreSQL, Valkey, Garage, ClamAV via docker compose)`
6. `Real-stack E2E (compose + Playwright)`
7. `Container images (build, SBOM, vulnerability scan, provenance)`
8. `Secret scanning`

## 4. How the owner applies it

**UI:** Settings → Rules → Rulesets → New branch ruleset.
- Name `main-protection`, enforcement **Active**, bypass list **empty**, target `main` (Include default branch).
- Enable: Restrict deletions, Block force pushes, Require a pull request (1 approval, dismiss stale approvals, require
  conversation resolution), and Require status checks to pass (up to date). Add the 8 checks of §3, each with source
  "GitHub Actions".
- Then Settings → General → Pull Requests: allow merge commits only.

**API** (owner's token with `administration: write`; nothing here is run by Claude Code):

```bash
curl -sS -X POST -H "Authorization: Bearer $OWNER_TOKEN" -H "Accept: application/vnd.github+json" \
  https://api.github.com/repos/Fatifizo/fundzim/rulesets -d @- <<'JSON'
{ "name": "main-protection", "target": "branch", "enforcement": "active", "bypass_actors": [],
  "conditions": { "ref_name": { "include": ["~DEFAULT_BRANCH"], "exclude": [] } },
  "rules": [
    { "type": "deletion" }, { "type": "non_fast_forward" },
    { "type": "pull_request", "parameters": { "required_approving_review_count": 1, "dismiss_stale_reviews_on_push": true,
      "require_code_owner_review": false, "require_last_push_approval": true, "required_review_thread_resolution": true } },
    { "type": "required_status_checks", "parameters": { "strict_required_status_checks_policy": true, "required_status_checks": [
      { "context": "Go (format, vet, unit tests, vulnerabilities)", "integration_id": 15368 },
      { "context": "Web (lint, types, unit tests, build, audit)", "integration_id": 15368 },
      { "context": "Web end-to-end (Playwright)", "integration_id": 15368 },
      { "context": "OpenAPI and SQL design drafts", "integration_id": 15368 },
      { "context": "Integration (real PostgreSQL, Valkey, Garage, ClamAV via docker compose)", "integration_id": 15368 },
      { "context": "Real-stack E2E (compose + Playwright)", "integration_id": 15368 },
      { "context": "Container images (build, SBOM, vulnerability scan, provenance)", "integration_id": 15368 },
      { "context": "Secret scanning", "integration_id": 15368 } ] } } ] }
JSON
```

`15368` is meant to be the GitHub Actions app. **Confirm it in the UI** (the check's source shows "GitHub Actions") before
relying on it. If D-01a is "no second reviewer yet", set `required_approving_review_count` to `0` and record that the review
control is absent (§6).

## 5. Emergency access

There are no standing bypass actors. In an emergency the owner temporarily sets the ruleset to *Disabled*, makes the change by
PR or push, and re-enables it.
- GitHub records ruleset changes in the repository's rule insights and the account security log.
- The owner also writes an incident entry naming the reason, the commit and the time window.
- Afterwards, re-read the ruleset (§6) and attach the output to the incident.

## 6. Verification (required before anyone calls it enabled)

Run after applying, without a token:

```bash
curl -s https://api.github.com/repos/Fatifizo/fundzim/rules/branches/main | jq '[.[].type]'
# expected: ["deletion","non_fast_forward","pull_request","required_status_checks"]
curl -s https://api.github.com/repos/Fatifizo/fundzim/branches/main | jq .protected   # expected: true
```

Then attempt a direct push of an empty commit to `main` from a throwaway clone. It must be rejected. The rejection message is
the evidence; nothing is pushed.

## 7. Decisions

| # | Decision | Options |
|---|---|---|
| D-01 | Apply §2 and approve the integration route (integration-plan.md) | apply as proposed / apply with changes / defer (launch stays blocked) |
| D-01a (new) | Who is the independent reviewer of financial and governance changes? | name ≥ 1 second reviewer (required review = 1, CODEOWNERS added) / accept "no second reviewer" for now (required review = 0, recorded as a residual risk; not acceptable for production) |
| D-12 | Commit-signature policy and artifact attestation | release-candidate-procedure.md §4 |

| Record | Value |
|---|---|
| Proposed rules | §2 to §4 |
| Current rules | none (§1) |
| Required owner decision | D-01, D-01a, D-12 |
| Actual implementation status | NOT APPLIED |
| Verification evidence | `evidence/github-protection-2026-10-26.txt` (absence only) |
