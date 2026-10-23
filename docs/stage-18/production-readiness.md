# Stage 18 — Production readiness

**Classification: BLOCKED.** Engineering completion (Stage 18 acceptance recommendation) is separate and recorded in the
completion report. A green CI run does not establish production readiness.

## 1. Gates

| Gate | Required for production | State | Owner |
|---|---|---|---|
| Financial | live payments, payouts, settlement postings, fees, screening, messaging, referrals enabled through ADRs | **BLOCKED** by design (11 constants false, 20 CHECKs, kill switches off) | Owner + engineering |
| Provider | a selected, contracted, sandbox-certified provider (ADR-040 superseded) | **BLOCKED** — no provider selected | Owner |
| Accounting | chart of accounts and posting rules reviewed (KI-S10-02); settlement accounting | **BLOCKED** | Accountant |
| Legal | custody, stored value, retention, consent, data protection (LR-001, LR-002, LR-003, LR-010, LR-011, LR-012, LR-023, LR-035, LR-074, LR-090 and the rest of the register) | **BLOCKED** — LEGAL_REVIEW_REQUIRED | Counsel |
| Security — findings | no open CRITICAL/HIGH | met: none open | Security admin |
| Security — conditions | WebAuthn or an SSO with a phishing-resistant factor for staff (F-S18-12); MFA reset procedure owned (F-S18-03); break-glass workflow and audit tagging (F-S18-04); independent penetration test (KI-S18-11); KMS decision (KI-S18-02) | **BLOCKED** | Owner / engineering |
| Infrastructure | TLS proxy/WAF, network policy, managed PostgreSQL with PITR and replicas, HA secret manager with audit device, off-host immutable backups, monitoring with paging, orchestrator with health-based restarts (KI-S18-01, -19, -20) | **BLOCKED** — does not exist | Infrastructure |
| Recovery | business-approved RPO/RTO (KI-S18-12); production restore drill | **BLOCKED** (proposals only; development drill PASSED) | Owner |
| Governance | Stages 0–18 accepted, reviewed and merged; branch protection verified (KI-S18-09, KI-S18-17) | **BLOCKED** | Owner |
| Supply chain | signed artifacts or an accepted alternative (KI-S18-08) | NOT_VERIFIED / pending | Owner |

## 2. What Stage 18 makes possible

A hardened production configuration now validates (keys from the secret manager), so the platform could start on
production infrastructure with every financial operation still disabled — useful for Stage 20 infrastructure testing.
That is not readiness for real money or real users.

## 3. Regulatory dependencies carried forward (brief §78)

Payment provider selection (ADR-040; PCR-*), settlement accounting treatment (KI-S10-02, LR-002), financial custody and
agency (LR-001, LR-003, ADR-013), regulatory reporting (no obligation defined; REQ register), communications consent
(LR-023, LR-035), data retention (LR-012, LR-074), privacy obligations (LR-010, LR-011), production screening providers
(Stage 13: none selected), operational governance (stage acceptance, owner roles), formal stage acceptance. None is closed
by engineering work.
