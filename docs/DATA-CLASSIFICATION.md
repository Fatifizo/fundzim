# FundZim Data Classification

Status: Stage 0 baseline. Applies to all code, storage, logs, exports, backups, vendors and staff tools.
Related: [PRIVACY.md](PRIVACY.md), [SECURITY.md](SECURITY.md), [AUDIT.md](AUDIT.md), ADR-009.

Every new table column, object type, log field or export must be assigned a class in the inventory (§4)
in the same change that introduces it. If unsure, choose the higher class.

---

## 1. Classes

| Class | Name | Definition | Examples |
|---|---|---|---|
| **C0** | PUBLIC | Intended for anyone on the internet, after explicit publication | Published campaign title, story, images, goal, raised amount; donor display name and amount **only** if donor opted in |
| **C1** | INTERNAL | Non-sensitive operational data; disclosure is embarrassing, not harmful | Aggregated metrics, feature flags, unpublished campaign drafts’ non-personal fields, system configuration (non-secret) |
| **C2** | CONFIDENTIAL | Personal or financial data whose disclosure harms individuals or FundZim | Email, phone, account profile, donor identity (incl. anonymous donors), payment records, ledger, payout requests, audit events, support tickets, IP addresses |
| **C3** | RESTRICTED | Data whose disclosure causes serious, often irreversible harm or legal exposure | KYC documents, national ID / passport numbers, DOB, liveness media, verification results, full payout account numbers, beneficiary medical evidence, fraud/risk investigation notes, compliance reports (e.g. suspicious-transaction reports) |
| **C4** | SECRET | Credentials and keys | Plaintext passwords, OTP codes, session tokens, API keys, webhook secrets, signing keys, KMS keys, DB passwords |

**Hashes vs secrets.** Plaintext credentials and keys are C4: they never enter the database, logs or
backups in usable form. A password **hash** (Argon2id) is C3, not C4: it is stored in the database by design and
cannot be used to log in directly, but its disclosure enables offline cracking, so it receives C3 handling.
Session-token and OTP hashes stored for verification are treated the same way.

## 2. Handling rules

| Rule | C0 | C1 | C2 | C3 | C4 |
|---|---|---|---|---|---|
| **Storage** | DB / `public-media` | DB | DB (app schema) | `kyc` schema / `private-kyc` bucket, or C3 columns with app-level encryption | Secret manager only; never DB plaintext, repo, image or ticket |
| **Encryption at rest** | Baseline disk | Baseline | Baseline disk/DB encryption | Baseline **plus** app-level envelope encryption for identifiers; dedicated KMS key for documents | Managed by secret manager/KMS |
| **Encryption in transit** | TLS | TLS | TLS (incl. internal) | TLS (incl. internal) | TLS; never in URLs |
| **Logging** | Allowed | Allowed | IDs and masked values only (`+26377****123`) | Never logged; reference by ID only | Never logged |
| **Audit metadata** | Allowed | Allowed | Redacted before/after | Never; reference IDs only | Never |
| **Access** | Anyone | Staff with need | Owner of the data + staff with role/permission | Named permission + justification + per-access audit | Deploy/runtime identities only; humans via break-glass |
| **Display in staff UI** | Yes | Yes | Yes (permissioned) | Masked by default; reveal action is audited | Never |
| **Exports** | Yes | Staff | Permissioned, audited, minimised | Not a product feature; case-by-case legal process (LEGAL_REVIEW_REQUIRED, LR-032) | Never |
| **Sharing with vendors** | Yes | Under contract | Under contract, minimised | Only vendors whose function requires it (KYC vendor, PSP), under contract (LEGAL_REVIEW_REQUIRED, LR-033) | Never (vendors issue their own credentials to us) |
| **Non-production environments** | Synthetic or public | Synthetic | Synthetic only | Synthetic only | Separate per-environment secrets |
| **Retention** | Until unpublished/deleted | Operational need | Per [PRIVACY.md](PRIVACY.md) schedule (LEGAL_REVIEW_REQUIRED, LR-012) | Per legal retention (LEGAL_REVIEW_REQUIRED), then deleted/destroyed | Rotated; old versions destroyed |
| **Caching** | CDN OK | OK | `Cache-Control: no-store`; Redis only short-lived, if needed | Never in Redis or CDN | Never cached |
| **Analytics / ML** | OK | OK | Pseudonymised only, with lawful basis | Never | Never |

## 3. Derived and combined data

- Aggregations take the class of their most sensitive input unless anonymised (k-anonymity or similar
  review), e.g. “raised per campaign” is C0 when the campaign is public, but “donations per donor” is C2.
- A masked value (last 4 digits) is C2, not C3.
- Hashes of low-entropy C3 values (phone, ID numbers) are **not** anonymous; HMAC blind indexes are C3.
- Data a user publishes (C0) about a third party (e.g. beneficiary medical details) is still personal data;
  see [PRIVACY.md](PRIVACY.md) §6.

## 4. Data inventory (planned)

Owning module = the only module that writes the data. “Stage” = when it first appears.

| Data element | Class | Owning module | Store | Stage | Notes |
|---|---|---|---|---|---|
| User ID (UUIDv7) | C1 | users | DB | 3/4 | Not secret; never used for authz alone |
| Email address | C2 | users | DB | 4 | |
| Phone number | C2 | users | DB | 4 | Masked in logs |
| Display name | C2 (C0 if shown publicly) | users | DB | 4 | |
| Password hash (Argon2id) | C3 | auth | DB | 4 | Optional factor |
| OTP code | C4 | auth | DB (hash only, short TTL) | 4 | Plain code never stored |
| Session token | C4 | auth | DB (SHA-256 hash only) | 4 | |
| MFA secrets (TOTP seeds) | C4 | auth | DB, app-level encrypted | 4 | WebAuthn public keys are C2 |
| IP address, user agent | C2 | auth/audit | DB | 3 | Retention LEGAL_REVIEW_REQUIRED (LR-012) |
| KYC level | C2 | kyc | `kyc` schema (status mirrored to users) | 5 | |
| Legal name, DOB | C3 | kyc | `kyc` schema | 5 | Legal name may be C2 when used for payout name-match display |
| National ID / passport number | C3 | kyc | `kyc` schema, app-encrypted + HMAC blind index | 5 | |
| Identity document images, selfies/liveness | C3 | kyc | `private-kyc` bucket | 5 | |
| Verification vendor results | C3 | kyc | `kyc` schema | 5 | |
| Residential address | C3 | kyc | `kyc` schema | 5 | Only where required |
| Organisation profile (name, type) | C0/C2 | organisations | DB | 5 | Public once verified and published |
| Organisation registration docs, directors, beneficial owners | C3 | kyc | `kyc` schema / `private-kyc` | 5 | |
| Campaign draft | C1/C2 | campaigns | DB | 6 | Contains personal data in story |
| Published campaign title, story, media | C0 | campaigns / storage | DB / `public-media` | 6/7 | EXIF stripped |
| Beneficiary identity and relationship | C2 | campaigns | DB | 6 | Public display only as published by owner with consent |
| Beneficiary supporting evidence (medical letters, death certificates, invoices) | C3 | kyc (compliance evidence) | `private-kyc` | 6 | Never public |
| Campaign reports from the public | C2 | risk | DB | 13 | Reporter identity protected |
| Donation (amount, currency, campaign, time) | C2 (C0 for public totals) | payments | DB | 8 | |
| Donor identity on a donation | C2 | payments | DB | 8 | Hidden from public and owner if anonymous |
| Donor public display preference | C2 | payments | DB | 8 | |
| Donor message | C0 if public | payments | DB | 7/8 | Moderated |
| Provider transaction reference | C2 | payments | DB | 8/9 | |
| Raw webhook payload (redacted) | C2 | payments | `webhook_inbox` | 8 | Sensitive fields removed before storage |
| Card data (PAN, CVV) | — | — | **Never stored or processed** | — | Hosted/tokenised PSP flows only |
| Mobile-money PIN | — | — | **Never collected** | — | Entered on handset/PSP only |
| Ledger accounts, transactions, entries | C2 | ledger | DB | 10 | Append-only |
| Payout destination (bank/wallet number) | C3 | payouts | DB, app-encrypted; masked copy C2 | 11 | |
| Payout requests and states | C2 | payouts | DB | 11 | |
| Fee configuration | C1 | fees | DB | 12 | Changes audited |
| Risk scores, signals | C2 | risk | DB | 13 | |
| Fraud/compliance investigation notes, STRs | C3 | compliance | DB | 13/17 | Strictly limited access; tipping-off concerns LEGAL_REVIEW_REQUIRED (LR-008) |
| Sanctions screening results | C3 | compliance | DB | 5/13 | |
| Audit events | C2 | audit | DB | 3 | No C3/C4 content |
| Notification content (SMS/email) | C2 | notifications | DB / provider | 15 | No C3/C4 in messages |
| Referral/share attribution | C1/C2 | campaigns (sharing) | DB | 16 | Privacy-reviewed |
| Reconciliation reports | C2 | reconciliation | DB / `private-kyc` bucket under a separate `reports/` prefix with its own access policy (no third bucket) | 17 | |
| Application logs | C1/C2 | platform | Log pipeline | 3 | Allow-list fields only |
| PSP API keys, webhook secrets, KMS keys, DB passwords | C4 | platform | Secret manager | 3/9 | |

## 5. Enforcement

- Stage 2: schema review tags each column with its class (comment or schema annotation) so tooling can
  check that C3 columns are encrypted and excluded from `fundzim_readonly` views.
- Stage 3: logger redaction tests; DTO tests ensuring C3 fields never appear in public/owner responses.
- Stage 18: data-flow review against this inventory before penetration testing.
