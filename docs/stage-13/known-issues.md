# Stage 13 — Known issues

| ID | Issue | Impact | Status |
|---|---|---|---|
| KI-S13-01 | Acceptance of Stages 1–13 not recorded; Stage 9 PARTIAL; Stages 10–12 PASS WITH CONDITIONS (recommendations) | Isolated engineering only | OPEN — owner |
| KI-S13-02 | **No sanctions/PEP screening provider** (KI-S5-04, PD-29); lists, scope and timing undecided (LR-009, LR-053, LR-065), PEP definition (LR-064), freeze duties without custody (LR-066) | Production screening BLOCKED; TEST simulator only | BLOCKED — legal + provider |
| KI-S13-03 | Reporting-institution status and STR duties unknown (LR-008, LR-051, LR-060): no regulatory report is filed or drafted | Escalations end in compliance cases only | OPEN — LEGAL_REVIEW_REQUIRED |
| KI-S13-04 | Automated temporary holds may be "solely automated decisions" (LR-071); retention and data-subject rights for risk records (LR-012, LR-034, LR-074) | Holds are reviewable and ≤ 7 days; records never purged | OPEN — LEGAL_REVIEW_REQUIRED |
| KI-S13-05 | Screening lives in the `risk` schema (deviation from the Stage 2 baseline: compliance); no identity data is used | Move under `compliance` with identity data from `kyc` when a provider is selected | OPEN (ADR-045 §8) |
| KI-S13-06 | **No rule is seeded or approved**; thresholds, contributions and bands are configuration without validated values (engineering default bands 25/50/75) | The engine evaluates nothing until two people approve rules | BY DESIGN — operational configuration |
| KI-S13-07 | No accuracy, precision or recall is measured (no labelled evidence set); the quality report counts firings, outcomes and false-positive reviews | Rule tuning is judgement-based | OPEN |
| KI-S13-08 | Model v1 (Stage 5, onboarding, 0–1000) and engine v2 (0–100) coexist; v1 is not yet expressed as reviewed v2 rules | Two scores for onboarding subjects | OPEN |
| KI-S13-09 | No payment-provider risk signals (no provider, ADR-040, PCR-005); session signals limited to sign-in, MFA, password and suspension events (no device intelligence by design) | Narrower account-takeover coverage | BLOCKED (provider) / BY DESIGN (privacy) |
| KI-S13-10 | No fairness/bias review process for rules; no rule expiry dates or periodic review reminders | Governance | OPEN — operational-controls |
| KI-S13-11 | An ACTIVE rule the engine cannot run (e.g. after an engine change) blocks signal recording for its subject type until retired (atomic by design; reconciliation reports it, consumer retries) | Availability of risk recording | OPEN — add an engine-version compatibility check before deployments |
| KI-S13-12 | Case SLA targets (P1 24 h … P4 14 days) and the screening timeout (10 min, 20 polls) are engineering defaults, not configuration records | Operational | OPEN |
| KI-S13-13 | Reconciliation checks a bounded window (signals 7 days, evidence 30 days / 5 000 rows) and does not compare compliance restrictions with holds (they are independent by design); `RECON_UNPROCESSED_EVENT` (outbox lag) is not implemented in risk (the outbox has its own dead-letter monitoring) | Must page before production | OPEN |
| KI-S13-14 | One open hold per type and subject: a second reason for the same hold type links to the existing hold rather than stacking | Release of the hold releases it for all reasons | ACCEPTED (one case per subject/type carries all evidence) |
| KI-S13-15 | Payout EC-15 for TEST payouts reads the owner's, organisation's and beneficiary's latest TEST screening; a production payout still fails EC-15 (no provider) | — | BY DESIGN (Stage 13) |
| KI-S13-16 | The screening simulator's callback signing scheme is FundZim's own; a real provider's scheme is unknown (PCR) | Adapter work with a provider | BLOCKED (provider) |
| KI-S13-17 | Development-database residue: integration tests leave TEST rules (retired), signals, evaluations, cases, holds (released) and screening rows | Shared dev DB only | ACCEPTED |
| KI-S13-18 | The compose worker image predates Stage 13 until rebuilt; integration tests run the risk pipeline in-process | Local stack only | OPEN — rebuild containers |
| KI-S13-19 | Not built from the Stage 12 handover list: instrument-fingerprint signals (many donors from one payment instrument), self-donation detection, refund-abuse and chargeback-exposure signals (no dispute source; refunds cannot execute) | Narrower donation-risk coverage | OPEN (payments must emit fingerprint ids; disputes are a later stage) |
| KI-S13-20 | `TestPublicExperienceEndpoints` failed once in the full local integration run (a campaign review required a second approval) and passed alone; it exercises no risk code | Possible cross-test interaction on the shared dev database | OPEN — watch CI |
| KI-S13-21 | The risk UI offers actions the viewer may lack permission for (FINANCE sees buttons; the API refuses with 403); there is no client-side permission model in the app yet | UX only; the API is the authority | OPEN (Stage 14 permission matrix) |

