# Stage 20 — Certification findings register

Existing IDs are preserved (their origin documents remain authoritative for history). New IDs `F-S20-nn` are items
Stage 20 found. "Blocks launch" means production launch cannot be authorised while the item is open. Nothing here was
closed because a test passed when the underlying risk needs an operational, legal or owner decision.

Categories: **IMPL** implementation defect · **TEST** test/infrastructure limitation · **PROV** provider-selection
dependency · **LEGAL** legal/regulatory · **ACCT** accounting · **SEC** security · **OPS** operational readiness ·
**GOV** governance/acceptance · **INFRA** infrastructure.

## 1. New in Stage 20

| ID | Cat | Severity | Description | Evidence | Component | Impact | Status | Required remediation / decision | Owner | Verification | Blocks launch | Closure evidence |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| F-S20-01 | GOV / SEC | HIGH | (confirms KI-S18-09, which Stage 18 could not verify) `main` has no branch protection although docs/DEVELOPMENT.md §5 requires PR, green CI and reviewers once hosted | `GET /repos/Fatifizo/fundzim/branches/main` → `protected: false` (2026-10-25) | repository | any push could change the release baseline without review or CI | OPEN | configure protection (required checks = the 8 CI jobs, ≥ 1 review, ≥ 2 for ledger/payments paths, no force push) | repository owner | API shows `protected: true` with the rule set | **yes** | — |
| F-S20-02 | GOV / INFRA | HIGH | No release baseline: all stage code lives on a stacked branch; nothing is merged, tagged or released; no release or deployment workflow exists | preflight.md §1; `.github/workflows` contains only `ci.yml` | release | nothing identifies "the" production candidate except a branch head | OPEN | owner-approved integration (PR → protected `main`, merge commit, tag), release and rollback procedure | owner, tech lead | tag + CI on `main` + documented procedure | **yes** | — |
| F-S20-03 | TEST | MEDIUM | Stage 19 load and benchmark reports existed only in the session scratchpad (not in the repository) | scratchpad files dated 2026-10-23 | evidence | certification figures not durable | FIXED | copied with SHA-256 into `docs/stage-20/evidence/stage-19/` | — | file hashes in capacity-certification.md | no | committed files |
| F-S20-04 | OPS | MEDIUM | The notifications queue gets `max(1, WORKER_CONCURRENCY/2)` workers — **2** with the compose default of 4 — which, with simulator delivery, explains the Stage 19 backlog | `internal/platform/jobs/jobs.go` `DefaultQueues`; burst measurement in operations-readiness.md §3 | worker | notices late under sustained load; financial truth unaffected | OPEN | owner-approved notification latency target, then size `WORKER_CONCURRENCY` / worker processes against it | owner, SRE | re-run the burst and drain measurement at the chosen size | no (not a correctness issue), **yes** for any launch with a delivery target |
| F-S20-05 | OPS | LOW | River prunes completed jobs (~24 h), so queue history cannot be re-derived after the fact (the Stage 19 drain figures could not be re-verified from stored records) | queries on 2026-10-25 returned 0 rows for 2026-10-23 | worker / observability | operational evidence must be captured by metrics, not job rows | OPEN | export queue depth/age metrics to the (future) monitoring system | SRE | dashboard / retained metrics | no | — |
| F-S20-06 | INFRA | MEDIUM | Supply-chain provenance is unsigned; images are not published or signed (no registry, no attestation) | `ci.yml` (no cosign/attest step); Stage 18 KI | release | provenance cannot be verified by a deployer | OPEN (confirms Stage 18 KI-S18-08) | signing + attestation at release time | tech lead | signed attestation verified at deploy | **yes** (before production) |
| F-S20-07 | IMPL (ops tooling) | HIGH | `backup.sh` mixed sources: database from the active compose project, Garage volumes by hard-coded default-project names; reported success on an inconsistent set | first Stage 20 restore drill: 311/317 objects missing (refused by the drill) | scripts/backup | a backup that cannot be restored completely, discovered only at restore time | **FIXED** | project-derived volumes, existence check, fresh-snapshot check, `compose_project` in the manifest | — | second drill PASSED 8/8, failure drills 7/7 refused | no (fixed) | `evidence/stage-20/restore-drill.json` |
| F-S20-08 | IMPL (ops tooling) | LOW | scripts used obsolete `tail -N`, rejected by uutils coreutils (Ubuntu 25.10) for multiple files: failing drill steps printed a usage error | first drill output | scripts/ | diagnosis hidden | **FIXED** | `tail -n N` | — | second drill shows step output | no | commit |

## 2. Carried items assessed in Stage 20 (IDs preserved)

| ID | Cat | Severity | Item | Stage 20 assessment | Blocks launch |
|---|---|---|---|---|---|
| ADR-040 | PROV | CRITICAL | No payment/payout provider selected; only the sandbox simulator adapter exists | verified in code (`internal/app/payments.go` registers only `m.Sandbox`) | **yes** |
| PCR-001 … PCR-066 | PROV | HIGH | 66 provider questions, none confirmed | `docs/payments/provider-questions.md` | **yes** |
| LR P0 (31 items) | LEGAL | CRITICAL | operating model/custody, PVO fundraising authority, AML, identity, data protection, terms, consumer protection, feature gates | `open-legal-questions.md` §5; "No item has been reviewed by counsel" | **yes** |
| LR (90 unique) | LEGAL | — | full register, 11 rows marked DUPLICATE | same | per item |
| KI-S10-02 | ACCT | CRITICAL | chart of accounts, posting rules and donation accounting have no accounting review | `docs/stage-10/known-issues.md` | **yes** |
| F-S18-03 | SEC | MEDIUM | no staff MFA reset workflow | no code (`grep` MFAReset: none) | **yes** (or risk acceptance) |
| F-S18-04 / KI-S18-07 | SEC | MEDIUM | break-glass grants exist only as database rows; no workflow or audit tagging | `internal/auth/sessions.go:245` reads `app.break_glass_grants`; no API | **yes** (or risk acceptance) |
| F-S18-05 / KI-S18-16 | SEC | MEDIUM (residual) | internal metrics/readiness listeners unauthenticated, network-restricted | unchanged since Stage 18 | **yes** (or compensating network control evidenced in the real deployment) |
| F-S18-12 / KI-S18-06 | SEC | MEDIUM | no phishing-resistant MFA (WebAuthn) for staff | no WebAuthn/passkey code | **yes** (or risk acceptance) |
| KI-S18-01 | INFRA | CRITICAL | no production infrastructure provisioned | Stage 18 production-readiness.md | **yes** |
| KI-S18-02 | SEC | HIGH | no KMS/HSM selected | Stage 18 | **yes** |
| KI-S18-11 | SEC | HIGH | no independent penetration test | no report exists | **yes** |
| KI-S18-12 | OPS | HIGH | RPO/RTO proposed, not approved | disaster-recovery.md | **yes** |
| KI-S18-15 | OPS | HIGH | security/operations alerts are internal records only; no delivery channel, no on-call | Stage 18 security-monitoring.md | **yes** |
| KI-S18-19 | INFRA | HIGH | Docker does not restart a container stopped with `docker kill`; no health-based supervisor exists | Stage 18 DR exercise | **yes** |
| KI-S18-10 | SEC | MEDIUM | Stage 18: web image 1 CRITICAL / 56 HIGH Debian CVEs with no published fix (Trivy gate passes on *fixable* only) | security-certification.md §4 | decision required (risk acceptance or base change) |
| KI-S19-01 | OPS | MEDIUM | audit chain lock caps posting throughput (~155–182 journals/s on the dev host) | capacity-certification.md | **yes** until a capacity target is approved and shown to be met |
| KI-S19-04 | PROV | MEDIUM | poll budget 72 h / 500 are internal defaults | provider-readiness.md | **yes** (provider semantics required) |
| KI-S19-09 | IMPL / OPS | **HIGH (raised in Stage 20)** | internal reconciliations (payouts, fees, ledger) are windowed full scans run every 15 min (newest 5 000 / 5 000 / 10 000); coverage of every record is **not guaranteed** (a burst larger than a window between two runs is never examined; later changes to records outside the window are never re-detected) | financial-certification.md §5 | **yes** |
| KI-S19-13 | OPS | MEDIUM | worker throughput; notification lag | F-S20-04 | see F-S20-04 |
| KI-S19-15 | INFRA | LOW | index migrations not concurrent | migration procedure | **yes** before data exists in production |
| KI-S19-16 | OPS | MEDIUM | Stage 17 exceptions written one by one (23 612 in 83 s) | capacity-certification.md | no (pilot volumes), decision with capacity target |
| KI-S19-17 | TEST | LOW | development database contaminated by Stage 19 load data | data-hygiene section of infrastructure-dr-readiness.md | no |
| KI-S19-02, -03, -05 … -08, -10, -11, -12, -14 | — | LOW | see docs/stage-19/known-issues.md | unchanged | no |
| KI-S18-05 | SEC | MEDIUM | audit checkpoints not anchored outside the database (tamper evidence, not tamper-proof) | Stage 18 | decision (risk acceptance or anchoring) |
| KI-S18-13 / KI-S18-14 | LEGAL / SEC | HIGH | no retention periods (LR-012, LR-074); no data-subject access/export/deletion workflows | Stage 18; no code | **yes** |
| KI-S18-20 | INFRA | HIGH | every service single-node; no HA | Stage 18 | decision (accept single-node pilot with approved RPO/RTO, or HA) |
| KI-S18-21 | SEC | MEDIUM | DAST passive only | Stage 18 | covered by KI-S18-11 |
| KI-S18-17 / Governance | GOV | CRITICAL | Stages 0–19 acceptance NOT_RECORDED; nothing merged | register; preflight | **yes** |
