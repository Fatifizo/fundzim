# Remediation register (current status, 2026-10-25)

The Stage 20 registers (`docs/stage-20/production-launch-blockers.md`, `owner-decision-pack.md`, `findings-register.md`) are the
historical certification record and are not edited. This register tracks the **current** status of each launch workstream.
"Closed" requires the closure evidence named in the Stage 20 blocker register; nothing below is closed by documentation alone,
and no owner, legal, provider, accounting, security or operational approval exists in the repository.

Status values: OPEN (no work), IN_PROGRESS (engineering change exists, not yet integrated/accepted), ENGINEERING_DONE_PENDING_DECISION
(code and tests exist; closure needs an owner decision and integration), BLOCKED_EXTERNAL (needs a party outside engineering).

## 1. Repository and release governance

| Item | Existing evidence | Status | Required action | Responsible | Approval | Verification | Blocks launch |
|---|---|---|---|---|---|---|---|
| Branch protection on `main` (F-S20-01, B-13) | API 2026-10-25: `protected: false` | OPEN | apply integration-plan.md §4 | repository owner | owner (D-01) | API shows `protected: true` with rules | yes |
| Approved integration strategy (F-S20-02) | integration-plan.md (proposed) | IN_PROGRESS | owner approves PR route; merge in order | owner, tech lead | owner (D-01) | merge commits on protected `main`, CI green | yes |
| Formal stage acceptance (B-16) | register: 0–20 NOT_RECORDED | OPEN | owner records decisions in order | owner | owner (D-13) | register rows with decider, date, evidence | yes |
| Release baseline / tag (F-S20-02) | none | OPEN | tag merge commit after green CI | owner | owner | tag → commit → CI run | yes |
| Signed build provenance (F-S20-06, KI-S18-08) | CI builds SBOM + unsigned provenance | OPEN | signing + attestation at release | tech lead | owner (D-12) | verified attestation | yes |
| Release verification / rollback procedure | none | OPEN | write and rehearse | tech lead | owner (D-12) | rehearsal record | yes |

## 2. Financial integrity (this phase)

| Item | Existing evidence | Status | Required action | Responsible | Approval | Verification | Blocks launch |
|---|---|---|---|---|---|---|---|
| Reconciliation coverage (KI-S19-09, B-05, KI-S11-15) | ADR-052, migration `20261025090000`, sweep engine + tests, test-evidence.md, performance.md | ENGINEERING_DONE_PENDING_DECISION | owner sets the coverage requirement (max cycle duration per detector, D-06) and approves volumes (D-05); review and integrate; CI green on the integrated commit | tech lead, owner | owner (D-06, D-05) | CI run on the integrated commit; coverage age within the approved target on production-like infrastructure | yes until decided and integrated |
| Ledger succeeded-payment window read oldest 5 000 (F-R1-01, new) | facts.go now newest-first; PAYMENT_FACTS sweep | ENGINEERING_DONE_PENDING_DECISION | integrate with the above | tech lead | — | as above | part of B-05 |
| Batched exception writes (KI-S19-16) | not addressed | OPEN | batch upserts | tech lead | — | volume sample | no (performance) |
| Stage 17 93-day statement horizon | unchanged | OPEN (by design until a provider exists) | revisit with provider statement semantics | tech lead | owner | — | with B-01 |

## 3. Payment providers

| Item | Existing evidence | Status | Required action | Responsible | Approval | Verification | Blocks launch |
|---|---|---|---|---|---|---|---|
| Provider selection (B-01, ADR-040) | DECISION_DEFERRED | BLOCKED_EXTERNAL | select, superseding ADR, confirm Model A custody | owner | owner (D-02) | selection ADR | yes |
| Supported payment methods | PCR register | BLOCKED_EXTERNAL | provider answers (PCR-001 …) | owner | owner | PCR entries answered with source | yes |
| Contract and integration requirements | none | BLOCKED_EXTERNAL | contract | owner, counsel | owner | signed contract | yes |
| Sandbox validation | simulator only; conformance suite `internal/psp/psptest` | BLOCKED_EXTERNAL | adapter + suite against provider sandbox | tech lead | — | suite passing | yes |
| Webhook signatures | framework verified with simulator | BLOCKED_EXTERNAL | provider scheme | tech lead | — | conformance + replay tests | yes |
| Idempotency semantics | PCR open | BLOCKED_EXTERNAL | provider confirmation | owner | — | PCR answer | yes |
| Payment and payout outcome handling (UNKNOWN, never resubmit) | implemented, simulator-tested | ENGINEERING_DONE_PENDING_DECISION | provider status semantics | tech lead | — | conformance suite | yes |
| Provider-specific polling limits (B-14, KI-S19-04, PCR-010) | internal defaults 72 h / 500 | OPEN | provider values + owner approval | owner | owner (D-11) | config with source | yes |

## 4. Legal, regulatory and privacy

| Item | Existing evidence | Status | Required action | Responsible | Approval | Verification | Blocks launch |
|---|---|---|---|---|---|---|---|
| Qualified legal review (B-02; 31 P0 items) | open-legal-questions.md | BLOCKED_EXTERNAL | commission counsel | owner | counsel, owner (D-03) | written determinations | yes |
| Required authorisations | LR register | BLOCKED_EXTERNAL | counsel determination | counsel | owner | evidence of authorisation or determination none needed | yes |
| Customer and donor terms | none approved | BLOCKED_EXTERNAL | draft + counsel approval | counsel | owner | approved terms | yes |
| Privacy notices, POTRAZ licence / DPO (B-03, LR-010, LR-057) | none | BLOCKED_EXTERNAL | appoint, license, approve notices | owner, DPO | counsel | licence, appointment, notices | yes |
| Retention policy (LR-012, LR-074) | CHECK-guarded: never invented | BLOCKED_EXTERNAL | approved schedule | counsel, DPO | owner | schedule + implementation | yes |
| Data-subject requests (KI-S18-13/14) | no workflow | OPEN | implement after counsel input | tech lead | DPO | implemented workflow + tests | yes |
| Consent and communications (LR-023, LR-011) | marketing/live messaging disabled | BLOCKED_EXTERNAL | counsel | counsel | owner | determinations | yes |

## 5. Accounting

| Item | Existing evidence | Status | Required action | Responsible | Approval | Verification | Blocks launch |
|---|---|---|---|---|---|---|---|
| Independent review of the ledger model (B-04, KI-S10-02) | chart + rules documented | BLOCKED_EXTERNAL | engage accountant | owner | accountant (D-04) | written review | yes |
| Fee and revenue treatment (PD-01) | TEST fees only | BLOCKED_EXTERNAL | fee model decision | owner | accountant, owner | recorded model | yes |
| Refunds and reversals | maker-checker, not executable | BLOCKED_EXTERNAL | accounting review | accountant | owner | review | yes |
| Settlement reconciliation | NOT_PERFORMED (no evidence) | BLOCKED_EXTERNAL | provider statements | owner | accountant | reconciliation on real statements | yes |
| Payout accounting | TEST journals | BLOCKED_EXTERNAL | review | accountant | owner | review | yes |
| Accounting-period rules | periods exist; no policy approval | BLOCKED_EXTERNAL | define close policy | accountant | owner | approved policy | yes |
| Financial reporting requirements | none | BLOCKED_EXTERNAL | define | accountant | owner | approved list | yes |

## 6. Security

| Item | Existing evidence | Status | Required action | Responsible | Approval | Verification | Blocks launch |
|---|---|---|---|---|---|---|---|
| Staff MFA strength / WebAuthn (F-S18-03) | TOTP step-up | OPEN | implement or accept risk | tech lead | owner (D-07) | implementation or recorded acceptance | yes |
| MFA reset workflow (F-S18-04) | none | OPEN | implement | tech lead | owner | tests | yes |
| Break-glass controls (F-S18-12) | none | OPEN | design + implement | tech lead | owner | drill | yes |
| Internal endpoint protection (F-S18-05) | loopback only locally | OPEN | production network design | SRE | owner | review | yes |
| KMS and key lifecycle (B-08, KI-S18-02/03) | secretstore refs; no KMS; no re-encryption job | OPEN | KMS selection ADR + job | owner, tech lead | owner | ADR + implementation | yes |
| Independent penetration test (B-09) | none | BLOCKED_EXTERNAL | commission | owner | owner | report + remediation | yes |
| Unresolved web-image vulnerabilities (B-10, KI-S18-10) | Trivy findings recorded | OPEN | decision or new base image | owner | owner (D-08) | decision record or clean scan | yes |
| Artifact signing and provenance | see §1 | OPEN | — | — | — | — | yes |

## 7. Infrastructure and operations

| Item | Existing evidence | Status | Required action | Responsible | Approval | Verification | Blocks launch |
|---|---|---|---|---|---|---|---|
| Production architecture (B-11, KI-S18-01) | none provisioned | OPEN | provision | SRE | owner (D-09) | environment | yes |
| Backup and restore | scripts + local drills PASS (Stage 20) | IN_PROGRESS | off-host immutable backups, drill on target infra | SRE | owner | drill record there | yes |
| Approved RPO / RTO (KI-S18-12) | none | OPEN | owner decision | owner | owner (D-09) | recorded values | yes |
| Monitoring destinations / alert delivery (B-12, KI-S18-15) | metrics exposed; no destination | OPEN | configure; never claim delivery without evidence | SRE | owner (D-10) | test alert received by a named person | yes |
| On-call ownership | none | OPEN | rota | owner | owner (D-10) | rota | yes |
| Incident response | runbooks (Stage 18) | IN_PROGRESS | drills with named operators | owner, SRE | owner | drill record | yes |
| Worker sizing / notification backlog (F-S20-04, KI-S19-13) | measured on dev host | OPEN | size against an approved target | SRE | owner (D-05/D-10) | burst + drain at chosen size | yes (with a delivery target) |
| Approved capacity targets (B-06, KI-S19-01) | none | OPEN | owner decision | owner | owner (D-05) | recorded targets; measurements meeting them | yes |
| Reconciliation coverage freshness target (new, from ADR-052) | performance.md | OPEN | owner decision (D-06) | owner | owner | coverage age ≤ target | yes |
| Concurrent index migration procedure (B-15, KI-S19-15) | none | OPEN | procedure + rehearsal | tech lead | — | rehearsal | yes |
| Explicit launch authorisation (B-17) | none | OPEN | go/no-go with named approvers | owner | owner (D-14) | signed record | yes |
