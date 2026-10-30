# Owner decision register (current; governance gate, 2026-10-27; status refreshed 2026-10-30)

This register tracks the decisions that block integration or launch. The identifiers are those of
`docs/stage-20/owner-decision-pack.md`, plus D-01a (Phase 2) and LR-012 (`docs/compliance/open-legal-questions.md`). Historical
registers are not rewritten.

**No decision below has been taken.** The repository contains no authoritative record of any of them. The single owner decision
on record during the remediation is F-R1-07 (keep migration `20261025090000` on the development database, 2026-10-26), which
is not in this table.

| Decision | Required action | Required authority / evidence | Status |
|---|---|---|---|
| D-01 | Protect `main`; establish merge controls | Repository administrator; settings verified by the `main-branch-protection.md` §7 queries | **OPEN** (verified 2026-10-30: `protected: false`, 0 rulesets, 0 branch rules; the protection endpoint needs a token) |
| D-01a | Arrange an independent second reviewer | A named, available reviewer who is not the author account; `independent-review-process.md` | **OPEN — reviewer DESIGNATED: `emB99`** (owner's instruction, 2026-10-30; scope G-02 and G-03 only). Read access VERIFIED (public repository); collaborator access PENDING (needs an administrator); reviewer acceptance PENDING; no review record yet (`independent-review-handover.md` §0) |
| D-15 | Decide the review scope for integration: the whole range `a552de2..<candidate>` (Stages 1–20 and Phases 1–6), or Phases 1–6 first | Owner, with the reviewer; `review-scope-phases-1-6.md` §1 | **OPEN — scope not accepted** (added 2026-10-27; unchanged 2026-10-30) |
| D-05 | Approve expected financial and operational volumes | Owner / operations capacity decision | **OPEN** |
| D-06 | Approve reconciliation freshness and maximum full-sweep duration, including the integrity-verification staleness threshold (engineering default 1 h, Phase 6) | Owner / operations, supported by measurements | **OPEN** |
| D-10 | Approve alert thresholds and a human notification destination | Owner / operations; a delivery path verified end to end | **OPEN** |
| D-12 | Decide artifact signing and verification | Owner / security / release | **OPEN** |
| D-13 | Record formal stage and remediation acceptance | Authorised owner entries in `docs/governance/stage-acceptance-register.md` | **OPEN** (register: 21 rows NOT_RECORDED; remediation Phases 1–6 not listed) |
| LR-012 | Resolve evidence and record retention | Legal / privacy authority; documented decision | **OPEN** (LEGAL_REVIEW_REQUIRED) |

## Findings awaiting independent review, or open (status only; 2026-10-30)

These are findings, not owner decisions. They are listed here so the decisions that unblock them are visible. **None is
closed.** Closing one needs evidence for that item, and for G-02 and G-03 an independent review.

| Item | Status | Where it stands | What it needs |
|---|---|---|---|
| G-02 (fee-draft notice lost on refresh) | **AWAITING INDEPENDENT REVIEW** | fixed on `remediation/g02-fee-draft-notice` (`d83d1ac`; docs `d0be53e`) | reviewer `emB99` designated (acceptance and collaborator access pending); review per `independent-review-handover.md` |
| G-03 (Playwright connection race, mock and real-stack suites) | **AWAITING INDEPENDENT REVIEW** | fixed on `remediation/g03-e2e-socket-hangup` and `remediation/g03-real-stack-connections` (code `02aac0e`, `bdbc607`, `e063113`; head `ec1cdc5`) | reviewer `emB99` designated (acceptance and collaborator access pending); review per `independent-review-handover.md` and `g03-review-package.md` |
| G-04 (`discovery.spec.ts:220` counts the first view event as a replay) | **OPEN**: test-design issue, established (reproduced 20/20 with CPU throttling) | not fixed | owner authorises a separate test-only branch |
| G-05 (`TestReconciliationPaymentsEndToEnd` failed once in CI run 40715437694) | **OPEN**: unexplained reconciliation test failure, cause unknown | not reproduced (21 local runs); CI log needs a token | owner authorises a separate investigation, with CI log access |

## Before an independent review can start (owner decisions; none taken)

1. **Name the independent reviewer (D-01a).** **Done 2026-10-30: `emB99`.** Remaining: the reviewer's acceptance, and
   collaborator access if a counting GitHub approval is wanted.
   - The reviewer must not be the implementation account (`Fatifizo`, under which Claude Code commits) and must not have
     authored or pushed a commit in the reviewed range.
   - The reviewer needs read access to the repository. To approve a pull request later, they also need the corresponding
     GitHub permission, which the administrator grants.
   - Claude Code does not add collaborators or permissions.
2. **Confirm the review scope.** The review covers G-02 and G-03 only (`independent-review-handover.md` §1). A PASS
   approves neither a merge nor integration.
3. **Approval of G-02/G-03 does not authorise integration of the earlier stages.**
   - A merge into `main` carries Stages 1–20 and remediation Phases 1–6, none of which is independently reviewed.
   - The full integration scope must be decided separately under **D-15**.
4. **Configure and verify branch protection (D-01) before any controlled integration.** Apply
   `main-branch-protection.md` §5 and verify it with §7. The review itself does not need it; the integration does.
5. **Decide what happens to G-04 and G-05**: separate branches or investigations, each with its own authorisation.
6. **Record outcomes** (review records, closure of findings, acceptance under D-13) **in the owner's name only**. Claude
   Code records no decision, approval or acceptance.

## Details

### D-01: protect `main`
- **Why:** anyone with write access can push to `main`; no check gates a merge. It is the precondition of every controlled
  integration (B-13).
- **Available evidence:** the specification and API steps (`main-branch-protection.md`), the required-check guard
  (`TestWorkflowRequiredChecks`), the API reads.
- **Missing:** the configuration itself, and its verification.
- **Who:** the repository owner or administrator.
- **Blocks:** integration.
- **Closes it:** the §7 queries return the rules, a direct push is rejected, and the output is recorded here.
- **Type:** external (administrative) action; the technical preparation is done.

### D-01a: independent reviewer
- **Why:** GitHub does not count the author's approval, and the code is authored under the owner's account. Without a second
  person, required review is impossible and Phases 1–4 remain unreviewed.
- **Available evidence:** the process, checklist and record template; the review packet.
- **Missing:** a person.
- **Who:** the owner.
- **Blocks:** integration (Gate C) and acceptance.
- **Closes it:** a named reviewer with access, and their first completed review record.
- **Type:** external.
- **Status (2026-10-30): designated, not closed.** The owner designated `emB99` (GitHub id 61978640; distinct from
  `Fatifizo`, id 236812721) for G-02 and G-03 only. Evidence: `evidence/reviewer-emb99-verification-2026-10-30.txt`.
  - Verified: the account exists, the repository is public (read access), and `emB99` has no commit in the review range
    or the history.
  - Pending: the reviewer's acceptance; collaborator access (granted and verified by an administrator, needed for a
    counting GitHub approval); the first completed review record.
  - Not verifiable from GitHub data: that a person other than the owner controls the account.

### D-15: review scope for integration (added 2026-10-27)
- **Why:** a merge into `main` carries 115 commits. Only 24 of them are remediation Phases 1–6; the other 91 are Stages 1–20,
  which were never independently reviewed either. Integration gate 4 asks for the whole range.
- **Available evidence:** `review-scope-phases-1-6.md` (ranges, files, claims C1–C14, evidence index, re-run commands);
  `independent-review-process.md` §2 (which paths need which reviewer).
- **Missing:** the decision: whole range (per-stage PRs make it tractable), Phases 1–6 first with integration blocked until
  the rest is reviewed, or a scoped acceptance of the unreviewed range with the risk owned in writing.
- **Who:** the owner, with the reviewer named under D-01a.
- **Blocks:** the start of the review that integration needs.
- **Closes it:** a recorded decision naming the range(s) and their order.
- **Type:** external.

### D-05: volumes
- **Why:** sizes the sweep budgets, evidence storage, verifier cost and worker capacity (B-06).
- **Available evidence:** dev-host measurements at 20 000 and 100 000 journals (Phases 1 and 4); small-volume payout and fee
  measurements.
- **Missing:** the expected pilot and year-one volumes.
- **Who:** the owner (operations, finance).
- **Blocks:** launch; the D-06 and LR-012 calculations.
- **Type:** business decision.

### D-06: reconciliation freshness
- **Why:** the coverage watermark is meaningful only against a target. The health thresholds (1 h stall, 24 h slow, 24 h
  deferral age, 3 failures) are engineering defaults.
- **Available evidence:** cycle arithmetic (Phase 1 performance.md: 100 000 journals take 10 runs, about 2.5 h).
- **Missing:** the target per detector.
- **Who:** the owner (operations).
- **Blocks:** B-05 closure; launch.
- **Type:** business decision, implemented technically afterwards (configuration).

### D-10: alert thresholds and delivery
- **Why:** an **in-app alert is not a notification that reaches an accountable person.** Today:
  - RECONCILIATION_SWEEP_FAILING, _STALLED, _BLOCKED and _INTEGRITY are persisted in-app alerts;
  - no channel exists (Stage 15: no messaging provider; B-12).
- **Missing:** thresholds, an on-call owner, a delivery path, and an end-to-end test that a human received an alert.
- **Who:** the owner (operations).
- **Blocks:** launch.
- **Closes it:** a recorded test alert received by a named person.
- **Type:** business decision plus an external provider.

### D-12: signing
- **Why:** artifacts are built by CI but their provenance is an unsigned record, so a third party cannot verify that an
  artifact came from a given commit.
- **Available evidence:** the keyless-attestation design (`docs/remediation/phase-2/release-candidate-procedure.md` §4); the
  rc-manifest reports `UNSIGNED_NOT_VERIFIABLE` honestly.
- **Missing:** the decision; then a CI change (`provenance` job; `TestWorkflowLeastPrivilege` already permits it) and a
  verification test.
- **Who:** the owner with security.
- **Blocks:** a verifiable release.
- **Type:** decision first, then technical work.

### D-13: acceptance
- **Why:** nothing may be treated as accepted. Stages 0–20 and remediation Phases 1–5 are all NOT_RECORDED.
- **Who:** the owner only.
- **Closes it:** owner-authored register entries with decision, date, decider and evidence.
- **Type:** external.

### LR-012: retention, including reconciliation evidence
- **Why:** ledger, audit and now `*_sweep_batches` evidence are never deleted (append-only; no retention invented). Storage
  grows without bound. Deleting evidence would remove the ability to prove past coverage.
- **Missing:** the legal retention requirement and the owner's decision.
- **Who:** counsel and the DPO, then the owner.
- **Blocks:** launch at volume.
- **Type:** legal.

**Illustrative storage projection.** These are assumptions, not a forecast, and no retention period is selected here.

| Assumption | Value |
|---|---|
| Bytes per evidenced key | **≈10 B**, measured: 4.6 MB for ≈480 000 evidenced keys in `fundzim_perf_r6`, compressed UUIDv7 arrays (Phase 4). 20 B upper bound if the keys compressed poorly |
| Runs | one every 15 minutes, 96 per day |
| Sweep budgets per run (engineering defaults) | ledger detectors 10 000 keys each; fees 5 000; payouts 2 000 |
| Volume relationship | once a table is larger than its per-run budget, a detector evidences at most budget × 96 keys per day **whatever the table size**. Smaller tables complete a cycle every run, so they evidence table size × 96 per day |

| Detector | Max keys per day | Storage per day at 10–20 B | Storage per year at 10–20 B |
|---|---|---|---|
| PAYMENT_JOURNALS | 960 000 | 9.6–19 MB | 3.5–7.0 GB |
| PAYMENT_FACTS | 960 000 | 9.6–19 MB | 3.5–7.0 GB |
| FEE_ASSESSMENTS | 480 000 | 4.8–9.6 MB | 1.8–3.5 GB |
| PAYOUTS | 192 000 | 1.9–3.8 MB | 0.7–1.4 GB |
| **All, at the budget ceiling** | **2.6 million** | **≈26–51 MB** | **≈9.5–19 GB** |

The figures above are ceilings at today's run budgets. Real volumes (D-05) below the budgets give less; raising the budgets
(for freshness, D-06) gives proportionally more.

**Correction.** The Phase 4 register's "≈40 MB per detector per day" overstated this; the derivation above supersedes it. The
Phase 4 document is kept as written.

**Implications.**
- Retention shorter than the audit or legal requirement loses the proof of historical coverage.
- Deletion would need its own reviewed mechanism: the tables are append-only by trigger, and only a migration or the owner
  could change that.
- Recovery: the evidence is in the database backups (Stage 18), so backup retention interacts with this decision.
