# Review starting point — author's verification (2026-10-30)

**This is Claude Code's own verification, not part of the independent review.**
- It refreshes the facts the reviewer (`emB99`) starts from.
- It confirms nothing on the reviewer's behalf.
- Every item here is meant to be re-checked by the reviewer.

## 1. Branch and code tree

| Item | Value |
|---|---|
| Branch | `remediation/g03-real-stack-connections` |
| Head at verification | `e05f6ec` (owner's review brief), clean, equal to `origin` (12:01 UTC) |
| Code commits in the review range | `d83d1ac` (G-02), `02aac0e`, `bdbc607`, `e063113` (G-03) |
| Code tree since `e063113` | **unchanged**. `git diff --name-only e063113 <c> -- . ':!docs'` is empty for every later commit: `3c97ed6`, `812bb3c`, `ec1cdc5`, `4be36ec`, `c0d01d8`, `ceed413`, `2569e3a`, `e05f6ec` |
| Other branches | `main` `a552de2` (unchanged); `remediation/g02-fee-draft-notice` `d0be53e`; `remediation/g03-e2e-socket-hangup` `cec643d` |
| Review branches or pull requests | none (0 pull requests in the repository) |

## 2. CI runs since the handover (public API)

All of these commits change only documents, and their code tree is identical to `e063113`.

| Run | Commit | Result | Tests ran? |
|---|---|---|---|
| 41014930811 | `4be36ec` | **success 8/8** | yes: integration 255 passed / 0 failed / 8 skipped; real-stack 70 passed; mock e2e 231 passed + 1 skipped |
| 41017860591 | `c0d01d8` | **cancelled**: 7 jobs succeeded, 1 cancelled (superseded by the next push) | partly: mock e2e 231 + 1 and real-stack 70 passed; the cancelled job did not finish |
| 41019540201 | `ceed413` | **success 8/8** | yes: same counts as 41014930811 |
| 41051355419 | `2569e3a` | **cancelled**: 5 jobs succeeded, 3 cancelled (superseded) | partly |
| 41052140146 | `e05f6ec` | **success 8/8** (read 12:54 UTC) | yes: same counts as 41014930811 |

**Cancellations.** The workflow cancels a branch run when a newer push arrives (`concurrency`, `cancel-in-progress` except
on `main`). A cancelled run is neither a pass nor a failure.

**Earlier runs.** The run-to-commit table is unchanged; see `reviewer-instructions-emb99.md` §6.
- No run on the code commit `e063113` itself is fully green.
- Its identical code tree is now fully green in 40720405660, 40992515372, 41014930811 and 41019540201.
- It failed once on G-05 (40715437694).

## 3. Evidence integrity

`sha256sum -c SHA256SUMS`: **0 failures** in every evidence folder.

| Folder | Files verified |
|---|---|
| `docs/governance/evidence` | 3 |
| `docs/governance/evidence/g02` | 5 |
| `docs/governance/evidence/g03` | 63 |
| `docs/remediation/phase-6/evidence` | 15 |

## 4. Governance state (unchanged)

- `main` `protected: false`; 0 rulesets.
- The collaborator endpoint for `emB99` still needs a token (HTTP 401). The grant is recorded as owner-reported only.
- D-01, D-01a (open until a completed review record), D-15: open.
- G-02 and G-03: awaiting review. G-04 and G-05: open.

## 5. Limitations of this record

- It uses the unauthenticated public API. Job logs and artifacts are not accessible.
- During this verification the 60-request hourly limit was exhausted (by the author's own polling) and reset at 12:53 UTC.
  The `e05f6ec` run was read once after the reset.
- The commit that adds this record starts its own CI run, which is **not** recorded here.
