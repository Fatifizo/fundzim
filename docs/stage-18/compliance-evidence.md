# Stage 18 — Compliance evidence matrix

Controls are mapped only to requirements that are **established** in the repository's registers
(`docs/compliance/regulatory-requirements-register.md` REQ-xxx, `docs/compliance/operational-controls.md`, CLAUDE.md
non-negotiable rules). No Zimbabwean regulatory obligation is invented; unknown ones stay in the registers. "Regulatory
approval" is never claimed.

| Requirement source | Control (register id) | Technical implementation | Internal review | External legal confirmation | Regulatory approval | Operational verification |
|---|---|---|---|---|---|---|
| CLAUDE.md financial rule 4/5 (immutable history, no ledger bypass) | C-FIN-02, C-FIN-04 | CHECKs, triggers, grants | Stage 18 review | n/a | none | tests + dev DB checks |
| CLAUDE.md financial rule 9 (unknown ≠ failure; no UNKNOWN payout resubmission) | C-FIN-06 | attempts guard, retry budget | Stage 17/18 | n/a | none | tests |
| CLAUDE.md financial rule 12 (payouts fail closed) | C-FIN-07 | eligibility engine, maker-checker | Stage 11 | n/a | none | tests |
| Operating model A (ADR-013) | C-FIN-01 | gates, routing refusal, bank donor-funds alarm | Stage 17 | **pending** (LR-001..LR-003) | none | — |
| operational-controls.md maker-checker | C-IAM-08, C-FIN-07 | Go + DB | Stage 18 authorization review | n/a | none | tests |
| SECURITY.md: never log secrets | C-KEY-06 | redaction + scrubber | Stage 18 | n/a | none | tests |
| DATA-CLASSIFICATION C3 handling | C-KEY-01/02, database-security | KYC pool, encryption, SSE-C | Stage 5/18 | pending (LR-010, LR-011, LR-074) | none | verify objects |
| Retention (LR-012) | C-OPS-06 | registry blocks invented periods | Stage 18 | **pending** | none | test |
| Backups and recovery (SECURITY.md §20) | C-OPS-03/04/05 | encrypted backups, drills, evidence | Stage 18 | n/a | none | drill report |
| Audit evidence model (ADR-019) | C-OPS-01 | hash chains, checkpoints, verification | Stage 18 | n/a | none | verification runs |
| Consent (Stage 15; LR-035) | notifications rules | consent records, marketing disabled | Stage 15 | **pending** | none | tests |
| Referral attribution (LR-023) | discovery gate | `ProductionReferralsEnabled=false` | Stage 16 | **pending** | none | test |
| Provider selection (ADR-040; PCR-*) | psp gates | no adapter, conformance suite | Stage 9 | **pending** | none | — |
| Accounting treatment (KI-S10-02) | ledger/recon gates | no settlement postings | Stage 17 | **pending** (accounting review) | none | — |

The four distinctions the brief asks for are the last four columns: technical implementation (code/config), internal
review (this stage's review), external legal confirmation (none received), regulatory approval (none), operational
verification (tests/runs in a non-production environment). Regulatory dependencies: production-readiness.md §3.
