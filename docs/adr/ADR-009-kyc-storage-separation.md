# ADR-009: Sensitive KYC storage separation

- **Status:** Accepted
- **Date:** 2026-10-08
- **Stage:** 0 (design), implemented Stage 5

## Context

KYC data includes identity document images, national ID and passport numbers, dates of birth, and payout
account details. Its exposure would cause serious, lasting harm, including identity theft and SIM-swap fraud,
which is prevalent with mobile money. It is likely subject to the Cyber and Data Protection Act and AML
record-keeping obligations, though the specific obligations are LEGAL_REVIEW_REQUIRED (LR-007, LR-012). KYC documents must
**not** be treated like ordinary campaign images, and administrative roles must not get blanket access.

## Decision

KYC data gets **its own security boundary** at every layer:

- **Database:** a separate PostgreSQL schema `kyc`, owned (like every schema) by `fundzim_migrator` and
  accessible at runtime only to the dedicated `fundzim_kyc` role (DML only, not owner) used by the
  `internal/kyc` module.
  - The general application role and the reporting role have **no** access to it.
  - Other modules see only verification **status and level**: `UNVERIFIED`, `BASIC_VERIFIED`,
    `IDENTITY_VERIFIED`, `PAYOUT_VERIFIED`.
- **Field encryption:** national ID and passport numbers, and other C3 identifiers, are encrypted at the
  application level with **envelope encryption** (data keys wrapped by a KMS key).
  - An **HMAC blind index** (separate key) enables duplicate-identity detection without decryption.
- **Objects:** documents live in the `private-kyc` bucket (logical name; physical names are
  environment-prefixed, e.g. `fundzim-private-kyc`). The same bucket also holds compliance documents and
  reports under separate prefixes with separate access policies; there is no third bucket.
  - The bucket has no public access and uses SSE with a **dedicated KMS key** and separate credentials.
  - Access is only via the `kyc` module, which issues **short-lived (≤5 min) presigned GETs** to staff who hold
    `kyc.document.view`.
  - Every access records an audit event with the justification.
- **Access control:** ADMIN and SUPER_ADMIN do **not** implicitly have KYC document access. Break-glass access
  is time-boxed, justified, alerted and reviewed.
- **Logging:** KYC values and document contents are never logged. Logs reference only internal IDs.
- **Retention:** retention and deletion are governed by legal requirements (LEGAL_REVIEW_REQUIRED, LR-012), with
  crypto-shredding (destroying data keys) available for deletion.
- **Vendor:** the verification vendor (ID scan or liveness) is not chosen. Any vendor integration is classified
  as a cross-border data transfer question (LEGAL_REVIEW_REQUIRED, LR-011).

## Consequences

### Positive
- A compromise of the main application role or the public bucket does not expose identity documents.
- Access to KYC data is provably audited and permission-gated.

### Negative / costs
- Key management (KMS) and an extra DB role add operational complexity.
- Encrypted fields cannot be searched except by exact match via the blind index.
- Staff workflows (review queues) need careful UI to avoid over-exposure, e.g. masking by default.

## Alternatives considered

| Alternative | Why rejected |
|---|---|
| Same schema, same bucket as other data | A single misconfiguration or role compromise exposes everything. Contradicts the master prompt. |
| Separate database server for KYC | Stronger isolation but more operational cost now. Can be adopted later by ADR if risk assessment requires it. |
| Delegate all KYC storage to the verification vendor | Unknown vendor, data residency questions, and we may still have record-keeping duties. Not ruled out for the future. |
| DB-level encryption only (TDE/disk) | Does not protect against application-role or backup-reader access to plaintext. |

## Security implications
This is the primary purpose of the ADR. It implements least privilege, defence in depth and auditable access
for C3 RESTRICTED data. Keys are C4 SECRET and held only in KMS or the secret manager.

## Financial implications
PAYOUT_VERIFIED status, including payout-account ownership and name match, gates withdrawals. Protecting the
integrity of KYC data protects against payout fraud to attacker-controlled accounts.

## Related
ADR-004, ADR-008, [SECURITY.md](../SECURITY.md), [PRIVACY.md](../PRIVACY.md),
[DATA-CLASSIFICATION.md](../DATA-CLASSIFICATION.md), [COMPLIANCE.md](../COMPLIANCE.md).
