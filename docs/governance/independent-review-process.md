# Independent review process for sensitive FundZim changes

**Status: PROCESS DEFINED; NO REVIEWER AVAILABLE.** No independent reviewer has been named (D-01a). Every gate below that
needs one is therefore **BLOCKED**. Nothing in this repository is an independent review of Phases 1–4: the Phase 2 "review"
was done by an agent of the same tool on the author's behalf, and Claude Code is the author of the code.

## 1. Roles (one person may hold several roles, but never review their own change)

| Role | Responsibility | Holder today |
|---|---|---|
| Code author | writes the change, tests and evidence; states the risk; cannot approve it | Claude Code, committing as the owner's account `Fatifizo` |
| Independent technical reviewer | reads the diff and SQL, re-runs or inspects evidence, records findings and disposition; not the author, and not the account that pushed the change | **none (D-01a)** |
| Repository administrator | applies protection, CODEOWNERS and merge settings; verifies them; never approves their own change | owner (not verified) |
| Financial / accounting reviewer | posting rules, journal semantics, reservations, fee and revenue treatment, reconciliation meaning | **none** (accounting review KI-S10-02 open) |
| Security reviewer | roles and privileges, secrets and keys, supply chain, provenance | **none** (penetration test B-09 open) |
| Business owner | decisions (D-xx), acceptance (D-13), merge and release authorisation | owner |

## 2. Which changes need which review

| Area (paths) | Independent technical | Financial | Security | DB / migration focus |
|---|---|---|---|---|
| Ledger posting, journal immutability (`internal/ledger`, ledger migrations) | required | required | — | append-only triggers, grants of `fundzim_ledger` |
| Payout reservation and consumption, payment state transitions (`internal/payouts`, `internal/payments`) | required | required | — | `app.guard_transition`, status tables, locks |
| Reconciliation completeness, provider-status ambiguity, retries and idempotency (`internal/platform/reconsweep`, `internal/reconciliation`, detectors) | required | required | — | unique keys, deferrals |
| Checkpoint progression and batch evidence (reconsweep, migrations `20261025090000`, `20261026120000`, `20261027090000`) | required | advisory | advisory | constraint triggers, guard function, role privileges |
| Database triggers, constraints, indexes, roles, privileges (`migrations/`, `deploy/docker/postgres`) | required | when financial tables | required for roles | lock behaviour, Down safety |
| Migrations and rollback strategy | required | when financial tables | — | forward-only in production; Down only in development |
| Audit integrity (`internal/audit`) | required | — | required | MAC checkpoints, keys |
| Secrets, cryptographic keys (`internal/platform/crypto`, secretstore, `.env.example`) | required | — | required | — |
| Build provenance, release authorisation (`.github/`, `scripts/release`, Dockerfiles) | required | — | required | — |

## 3. What counts as evidence of an independent review

All of the following are required:
1. **A named reviewer:** a person or account other than the author and other than the last pusher.
2. **The exact commit range reviewed** (`base..head` SHAs) and the files in scope.
3. **Findings and their disposition:** fixed (commit), accepted (by whom), deferred (to where).
4. **The reviewer's statement** of what they inspected: diff, SQL, tests, evidence files, re-runs.
5. **For a PR, a GitHub approval by that reviewer on the final head commit.** It is dismissed automatically by new commits
   (protection §1, item 3).

**Not** a review:
- Claude Code reviewing its own changes, or an agent of the same tool doing so;
- passing tests or CI;
- an opened PR;
- the author's approval;
- a report that says "reviewed" without a reviewer and a scope.

## 4. Review checklist (sensitive change)

- [ ] Commit range and scope recorded; the diff is readable and matches the description.
- [ ] Financial invariants unchanged or strengthened: debits = credits per currency; no money state merged; no floats;
      currency travels with the amount.
- [ ] Idempotency backed by unique constraints; retries never resubmit an UNKNOWN outcome.
- [ ] State machines: Go and SQL edge lists identical; transitions DB-guarded.
- [ ] Database: every trigger, constraint and grant read. Who can UPDATE or DELETE what, and how is it bypassed
      (owner, superuser, `session_replication_role`)?
- [ ] Migrations: additive or not; locks and timeouts; effect on existing rows; Down behaviour; ordering; applied anywhere
      already?
- [ ] Tests: they prove the claim (mutation or negative tests where the claim is a guarantee); no weakened assertion; skips
      explained.
- [ ] Evidence files match the commits they name (`SHA256SUMS`).
- [ ] Residual risks are written down and owned.

## 5. Review record template

```markdown
# Review record — <short title>

| Field | Value |
|---|---|
| Reviewer (name, account) | <real person — never the author account> |
| Independence statement | <reviewer did not author or push any commit in the range> |
| Date | <YYYY-MM-DD> |
| Commit range | <base sha>..<head sha> (branch <name>) |
| Scope | <paths / migrations / documents reviewed> |
| Method | <diff read, SQL read, tests re-run (commands), evidence inspected> |
| Findings | <id — severity — description — file:line> |
| Disposition per finding | <fixed in <sha> / accepted by <name> / deferred to <id>> |
| Outcome | PASS / PASS WITH CONDITIONS / FAIL / BLOCKED |
| Conditions | <…> |
| GitHub approval | <PR #, approval on head <sha>> |
```

Completed records go in `docs/governance/reviews/` through a reviewed PR. **None exists today.**
