# Stage 18 — Privacy engineering review

No Zimbabwean privacy obligation is concluded here; open questions stay in `docs/compliance/open-legal-questions.md`
(LR-010 registration and DPO, LR-011 cross-border transfer and hosting, LR-012 retention, LR-035 lawful basis and
consent, LR-074 identity-data retention and deletion, LR-023 referral consent).

## 1. Personal data inventory and minimisation

| Data | Classification | Where | Minimisation / protection |
|---|---|---|---|
| Donor and organiser accounts (email, display name, phone) | C2 | `app.users` | phone verified; contact data only for notifications with consent |
| Beneficiary information | C3 | beneficiaries, KYC | KYC pool only; field encryption |
| KYC documents and identity values | C3 | `kyc` schema, private-kyc bucket | `fundzim_kyc` pool only, AES-GCM fields, SSE-C objects, blind indexes for lookups, audited access, download tickets |
| Campaign content | C1/C2 | campaigns | public by design after review; location coarse (province/capital, LR-090) |
| Payment references | C2 | payments | provider references, no card data (hosted flows only); TEST data |
| Bank / payout details | C3 | payout destinations | account numbers encrypted; last four + HMAC for bank statements (Stage 17) |
| Referral analytics | C1 | discovery | aggregate counts; no IP, user id, user agent or query text; daily dedupe hashes deleted after 2 days; DNT/GPC honoured |
| Device and network metadata | C2 | `app.security_events` (IP, user agent), risk sign-in UA hash | IP kept for security events only (LR-035 / LR-012 open); risk keeps a keyed per-user UA hash, no fingerprint |
| Communications consent | C2 | notifications | user-created GRANTED records only; marketing disabled |
| Security alerts (Stage 18) | C2 | operations | counts and metric keys only — no user ids, addresses or user agents in alert evidence |
| Logs | C2 | stdout (JSON) | key redaction and free-text scrubbing; no bodies |
| Backups (Stage 18) | C3 | encrypted sets | AES-256 under a separate key; retention LEGAL_REVIEW_REQUIRED |

Least-privilege access: support roles have no KYC, bank, ledger, risk-evidence or compliance-note access; staff access
to C3 data is permissioned and audited.

## 2. Data subject workflows (assessment)

| Request | Today | Gap |
|---|---|---|
| Access / export | profile and settings pages show the user's own data; no machine-readable export | export workflow (KI-S18-14) |
| Correction | profile, email (verified change), phone | identity data corrections go through KYC re-verification |
| Account closure | no self-service closure | workflow needed; financial and audit records must be retained (immutable) |
| Deletion | not implemented; nothing financial, audit, KYC or evidence related may be deleted until retention is decided (registry CHECKs) | depends on LR-012 / LR-074 |
| Consent withdrawal | communications consent can be withdrawn by the user (Stage 15) | — |
| Legal holds | evidence holds (maker-checker) | holds per person/case beyond evidence |

## 3. Retention infrastructure

`audit.retention_policies` (security-monitoring.md §3): no invented period; 17 families LEGAL_REVIEW_REQUIRED with
NEVER_DELETE; 3 operational purges that already exist documented with the code that performs them. Deletion stays
restricted by triggers and grants.

## 4. Findings

No new personal-data flow was added in Stage 18 except security alert evidence (counts only) and backup sets (encrypted).
The ZAP finding of credentials in URLs (F-S18-13) was a privacy as well as a security issue and is fixed.
