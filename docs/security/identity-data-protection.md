# FundZim Identity Data Protection: KYC/KYB Data Security (Stage 1 design)

> **Status:** This is a Stage 1 design. Stage 5 implements it, and Stage 18 hardens it. It builds on
> [SECURITY.md §13, §18, §21](../SECURITY.md), [DATA-CLASSIFICATION.md](../DATA-CLASSIFICATION.md),
> [PRIVACY.md](../PRIVACY.md) and [ADR-009](../adr/ADR-009-kyc-storage-separation.md). Nothing here claims
> compliance with any law or standard. Legal points marked `LEGAL_REVIEW_REQUIRED` are tracked in
> [open-legal-questions.md](../compliance/open-legal-questions.md).

---

## 1. What is protected

Identity data is classified **C3 RESTRICTED** ([DATA-CLASSIFICATION.md](../DATA-CLASSIFICATION.md)). It covers:

- legal name together with date of birth;
- national ID, passport and birth-certificate numbers;
- identity document images;
- selfies and liveness media, which are **biometric**;
- verification vendor reports;
- residential addresses;
- organisation registration documents;
- director, trustee and beneficial-owner details;
- beneficiary medical, death and guardianship evidence;
- consent records;
- full payout account numbers;
- compliance case notes, including suspicious-transaction report drafts.

Credentials and keys protecting this data are **C4 SECRET**.

Threats this design addresses ([THREAT-MODEL.md](../THREAT-MODEL.md)):

- bulk exfiltration through a compromised app role or staff account;
- an insider browsing KYC records without cause;
- documents leaking through the public media path or CDN;
- identity numbers leaking in logs, analytics or support tickets;
- vendor-side breach;
- backups restored outside controls;
- re-identification of anonymous donors.

## 2. Storage boundaries

| Asset | Where | Boundary controls |
|---|---|---|
| Structured KYC/KYB data | Postgres schema `kyc` | Separate role `fundzim_kyc` with DML only. The app role `fundzim_app` has **no** privileges on `kyc`. Separate connection pool. Accessible only from the `kyc` module ([DATABASE.md](../DATABASE.md), [ARCHITECTURE.md](../ARCHITECTURE.md)). |
| Documents, liveness media, evidence objects | `private-kyc` bucket (physical name environment-prefixed, e.g. `fundzim-private-kyc`) | No public access, enforced by bucket policy and account-level public-access block. Dedicated credentials used only by the kyc module. A dedicated KMS key provides server-side encryption. Versioning and object lock on the evidence prefix (Stage 18). Separate `reports/` prefix with its own policy for compliance reports. |
| Status mirror (level/status only) | `public.users`, `public.organisations` | No C3 fields. Written only by the kyc module through its service. |
| Search / dedupe | `kyc.identities.id_number_bidx` | HMAC-SHA-256 blind index with a dedicated key (`BLIND_INDEX_KEY`). Equality search only, no prefix search. |
| Never | `public-media` bucket, CDN, analytics, logs, error reports, emails, chat tools, tickets | Enforced by code review rules, a log redaction allow-list ([SECURITY.md §15](../SECURITY.md)), and DLP tests (§9) |

The rule from CLAUDE.md applies: **only the `kyc` module reads or writes KYC tables and private KYC objects.**
Other modules, such as campaigns and payouts, receive decisions (level, status, check codes) and opaque
evidence ids. They never receive raw documents or numbers.

## 3. Encryption

| Layer | Mechanism | Key |
|---|---|---|
| In transit | TLS 1.2+ (1.3 preferred) everywhere, including DB and object-store connections (`sslmode=verify-full`) and vendor APIs | Managed certificates |
| At rest, storage | Disk/volume encryption (provider default) + bucket SSE-KMS | Dedicated `kyc` KMS key, separate from the public-media key and the DB key |
| At rest, field | Application-level envelope encryption (AEAD, e.g. AES-256-GCM) for identity numbers, DOB, addresses, payout account numbers, and extracted vendor data. Each row stores `key_id`. | Data keys wrapped by a KMS key-encryption key (`FIELD_ENCRYPTION_KMS_KEY_ID`). Local development uses a non-production local key. |
| Searchable equality | HMAC blind index (normalised value) | Separate HMAC key. Rotating it requires a re-index job. |
| Backups | Encrypted with a backup key different from the live data keys. Backup access is limited to the restore role ([SECURITY.md §20](../SECURITY.md)). | Backup KMS key |

Key management:

- Key-encryption keys never leave KMS.
- Rotation is scheduled; old versions stay available to decrypt existing data until re-encryption.
- Key usage is logged by KMS and reviewed by `SECURITY_ADMIN`.
- Destroying a subject-specific data key is the crypto-shredding mechanism (§7).

## 4. Access control and justification

Permissions follow [operational-controls.md §1](../compliance/operational-controls.md):

| Action | Permission | Who | Extra conditions |
|---|---|---|---|
| See a user's KYC **status** and check outcomes | `kyc.status.view` | REVIEWER, KYC_REVIEWER, COMPLIANCE, FINANCE (payout context), SUPPORT (level only) | Within an assigned case, payout or review |
| View documents / liveness media | `kyc.document.view` | KYC_REVIEWER, COMPLIANCE | **Must be bound to an open case or review id.** Justification text is required. Step-up MFA must be fresh. Time-boxed viewer session (`kyc.view_session_ttl`). |
| Reveal a full identity or account number | `kyc.identity_number.reveal` | COMPLIANCE (and KYC_REVIEWER for dedupe resolution) | Bound to a case. Justification is required. Rate-limited per staff member per day (`kyc.reveal_daily_max`). |
| Export or download | none at launch | — | No bulk export feature. Regulator or court requests follow the evidence export procedure (`evidence.exported`, maker-checker, LR-032). |
| Decide verification | `kyc.decision.record`, `beneficiary.verification.decide`, `org.verification.decide` | KYC_REVIEWER, COMPLIANCE | Not for own or linked accounts. HIGH tier needs a second approver. |
| Break-glass | `breakglass.kyc` | Named COMPLIANCE lead + SECURITY_ADMIN approval | Time-limited (default 1 hour per [SECURITY.md](../SECURITY.md)). Every access is alerted and reviewed afterwards ([operational-controls.md §5](../compliance/operational-controls.md)). |

`ADMIN`, `SUPER_ADMIN`, `SECURITY_ADMIN`, `FINANCE` and `SUPPORT` **do not** have document view.
Granting `kyc.document.view` to anyone is a maker-checker role change.

## 5. Access auditing

Every access to C3 identity data produces an audit event. The events (per
[audit-evidence-model.md](../compliance/audit-evidence-model.md) and [AUDIT.md](../AUDIT.md)) are:

- `kyc.document.viewed`
- `kyc.identity_number.revealed`
- `kyc.status.viewed` (sampled, not every view)
- `kyc.review.decided`
- `evidence.exported`
- `breakglass.*`

Each event carries the actor, target subject id, evidence id, case or review id, justification, request id,
IP and device, and outcome. **The event never contains the document, number or image.**

The audit events drive detection rules, which feed SECURITY_ADMIN and COMPLIANCE dashboards:

| Rule | Signal |
|---|---|
| Views without a case | Should be impossible; if seen, it is a SEV2 (control failure) |
| Volume anomaly | Views per staff member per hour exceed baseline × factor |
| Off-hours access | Access outside the staff member's roster window |
| Out-of-assignment access | Access to subjects outside the staff member's assigned queue |
| Repeated reveals | Repeated reveals on the same subject |
| Break-glass | Any use, alerted immediately |

There is a quarterly access review of who holds `kyc.*` permissions, and a sample review of justifications
([operational-controls.md §6](../compliance/operational-controls.md)).

## 6. Document handling pipeline

```mermaid
sequenceDiagram
    participant U as User browser/app
    participant API as kyc module (API)
    participant Q as private-kyc/quarantine/
    participant S as Malware + content scanner (ClamAV, type sniffing)
    participant K as private-kyc/documents/
    participant V as Verification vendor (optional)
    U->>API: request upload slot (evidence_type, attempt_id)
    API-->>U: presigned PUT (≤5 min, size/type constraints, random key)
    U->>Q: upload object
    Q-->>API: object-created event
    API->>S: scan + verify magic bytes + strip metadata copy
    alt infected / type mismatch / oversize
        S-->>API: reject
        API->>Q: delete object; audit kyc.upload.rejected
    else clean
        S-->>API: ok (sha256)
        API->>K: copy to documents/ (SSE-KMS), delete quarantine copy
        API->>API: evidence_records row (sha256, C3, retention_class)
        opt vendor verification enabled (consent recorded)
            API->>V: send via vendor API (TLS), store vendor ref
        end
    end
```

Rules:

- **Upload.** Images are restricted to `image/jpeg` and `image/png`, plus `application/pdf` for documents.
  SVG, HEIC and other types are converted or rejected per policy. The maximum size is `UPLOAD_MAX_BYTES`.
  Keys are random UUIDs, never derived from names or numbers.
- **Quarantine.** Nothing is readable by staff until it is scanned. Quarantine objects expire automatically
  after `kyc.quarantine_ttl`.
- **No thumbnails or derivatives on the public path.** The public media processor never receives KYC
  objects. Previews for staff are rendered on demand from `private-kyc` by the kyc module and are never
  cached on the CDN.
- **Staff viewing.**
  - Documents open in an in-app viewer streamed through the kyc module.
  - Presigned GETs are only used where strictly needed, with TTL ≤ `STORAGE_KYC_PRESIGN_TTL` (5 min).
  - Each view is visibly **watermarked** with the staff member's id, the time and the case id, to deter
    screenshots and trace leaks.
  - **Downloads are disabled by default**, and right-click/save is discouraged in the UI. This is a deterrent
    rather than a control, so audit is the control.
- **Integrity.** `content_sha256` is verified on every read. A mismatch is a SEV1 incident
  ([incident-response-workflows.md](../compliance/incident-response-workflows.md)).
- **No email or chat transport.** Users and staff never exchange documents by email or WhatsApp. Support
  scripts point users to the upload flow.

## 7. Retention, deletion and crypto-shredding

FundZim does **not** set statutory retention periods in this document. The authoritative schedule is LR-012,
and conflicts are tracked under LR-074. These periods were found in sources (R2 research, accessed 2026-10-08,
HIGH) and apply **if** the relevant law covers FundZim or its client:

- MLPC Act s 24(2): identity documents and account files at least **5 years after the relationship ends**;
  transaction records at least 5 years from the transaction; certain wire information 10 years (s 27(8)).
  Applicability is conditional on LR-060 (→ LR-051).
- SI 98 of 2026 s 28: PVO transaction records 5 years; accounting records 7 years. These are PVO duties.
- CDPA s 13: personal data kept "for no longer than is necessary".

Design:

- **Retention classes.** Every evidence record and KYC row carries a `retention_class`: `KYC`, `FINANCIAL`,
  `AUDIT`, `CASE`, `CONSENT` or `OPERATIONAL`, per [audit-evidence-model.md §7](../compliance/audit-evidence-model.md).
  Each class has a configured `retention_trigger` (e.g. `relationship_end`, `transaction_date`, `case_closed`)
  and a `retention_period`. The period is **unset until approved** against LR-012. An unset period means
  **retain**, never delete.
- **Legal hold.** Records under case or legal hold (`evidence_holds`) are never deleted. Holds are
  maker-checker.
- **Deletion mechanism.**
  1. Each subject's C3 field data and documents are encrypted under a **per-subject data key**.
  2. When retention expires and no hold exists, a deletion job deletes the objects and rows.
  3. It then destroys the per-subject data key. This is crypto-shredding, which also renders backup copies
     unreadable without restoring and deleting each backup.
  4. It emits `evidence.purged` with counts, never content.

  Whether crypto-shredding satisfies deletion obligations, and how deletion interacts with retention
  mandates, is LR-074.
- **Data-subject requests.** CDPA s 14 rights (R2-23) are handled per [PRIVACY.md](../PRIVACY.md). The CDPA
  right to deletion covers "false or misleading data" (R2-23), and AML retention may apply. Where a request
  touches a subject of a suspicious-transaction report, tipping-off rules (MLPC s 31) may conflict
  (LR-072). Such requests route to COMPLIANCE, never SUPPORT.

## 8. Vendor data flows

| Vendor (none chosen) | Data sent | Minimisation | Controls required before go-live |
|---|---|---|---|
| Identity verification (PD-25) | Document images, selfie/liveness, extracted fields | Only the attempt's data; no FundZim account data beyond a pseudonymous reference | Contract with processing terms (CDPA s 18(5) requires a written contract with each processor; R2-25, HIGH). Data location disclosed. Cross-border transfer basis (CDPA s 28–29; R2-24) and POTRAZ notification (SI 155 s 10(2)(c)) — LR-011, LR-033. Vendor-side retention ≤ FundZim's needs, with a deletion API. Vendor callbacks are signature-verified. |
| Sanctions/PEP screening (PD-29) | Name, DOB, nationality, entity names | No documents, no ID numbers unless the vendor requires them | As above; list sources documented ([sanctions-screening.md](../compliance/sanctions-screening.md)) |
| PSP | Payout destination identifiers, payee name | Only what the payout rail needs | PSP contract (PCR), data processing terms |
| SMS/OTP | Phone number, OTP text (no PII in message body) | — | Sender ID and provider terms (LR-031) |

Minors' data through any foreign vendor needs the LR-070 position on prior POTRAZ authorisation for transfer
to inadequate jurisdictions. This is a POTRAZ guideline (R2-21, HIGH-MEDIUM).

## 9. Testing and verification (Stage 5/18 acceptance)

- **Schema privilege test.** As `fundzim_app`, every `SELECT` on `kyc.*` must fail. As `fundzim_kyc`, DDL
  must fail.
- **Bucket policy test.** Anonymous GET on a `private-kyc` object is denied. The public-media credentials
  cannot read `private-kyc`.
- **DLP tests.** Seeded canary identity numbers and synthetic document hashes must not appear in logs,
  traces, error reports, analytics events or the public bucket (CI log scanning).
- **Pipeline tests.** EICAR test file rejected; MIME-mismatch rejected; quarantine expiry works.
- **Audit tests.** Every viewer and reveal endpoint emits the event. Views without a case id are rejected
  (HTTP 403 with `CASE_REQUIRED`).
- **Crypto-shred test.** After shredding, the ciphertext is present but undecryptable, and no plaintext
  remains in the DB.
- **Test data rule.** Tests and fixtures use **synthetic** identities only, never real ID numbers or real
  document images ([TESTING.md](../TESTING.md)).

## 10. Breach handling for identity data

Notification duties (CDPA s 19 and SI 155 of 2024 s 17; Veritas copies, accessed 2026-10-08, HIGH; R2-25):

- notify POTRAZ "within twenty-four (24) hours of any security breach", using Form DP3;
- where the breach is "likely to result in a high risk", inform affected data subjects "within 72 hours";
- keep a breach record;
- answer POTRAZ information requests within 14 days;
- submit an investigation report within 21 days.

Operational design (playbook in [incident-response-workflows.md](../compliance/incident-response-workflows.md),
"data breach"):

1. Any suspected C3 exposure is **SEV1**. The clock starts at **awareness**, so the DPO is paged immediately.
2. Contain: revoke the affected credentials, rotate the kyc bucket and DB credentials, and block the staff
   account if insider-related. Disable presigned URL issuance if needed.
3. Scope: the audit events give exactly which subjects and evidence ids were accessed. This is why audit
   granularity in §5 is mandatory.
4. The DPO decides the notification content with counsel. The 24-hour POTRAZ notification is prepared from
   a pre-approved template. Whether FundZim acts as controller or processor for particular data (e.g.
   beneficiary data uploaded by organisation owners) affects who notifies — LR-010, LR-034.
5. Notifications to PSPs or KYC vendors follow contract terms (PCR).

Data controller licensing with POTRAZ, the DPO appointment and DPO certification (SI 155 s 3–6, s 12–13;
R2-19, R2-20) are preconditions for processing personal data at all. They are tracked as LR-010 and in the
[regulatory-readiness-checklist.md](../compliance/regulatory-readiness-checklist.md).

## 11. New legal questions

Register: [open-legal-questions.md](../compliance/open-legal-questions.md).

| ID | Question | Blocks |
|---|---|---|
| **LR-074** | Retention and deletion of identity data: (a) how to reconcile CDPA minimisation ("no longer than is necessary", s 13) and deletion rights with MLPC Act s 24 (if in scope), SI 98 of 2026 PVO-side retention, and other mandates; (b) whether crypto-shredding (destroying per-subject keys, including for backup copies) counts as deletion; (c) what retention terms are required of KYC and screening vendors, including vendor-side deletion. Complements LR-012 (periods) and LR-034 (data subject rights). | Approving the retention periods for the `KYC`, `CASE` and `CONSENT` classes; vendor contracts |

## 12. Sources cited (accessed 2026-10-08; research file R2)

| Ref | Source | URL | Confidence |
|---|---|---|---|
| R2-19–R2-25 | CDPA [Chapter 12:07] (Veritas) | `https://www.veritaszim.net/sites/veritas_d/files/Cyber%20%26%20Data%20Protection%20Act%20Cap1207%20No%205%20of%202021%20gaz%202022-03-11.pdf` | HIGH |
| R2-18–R2-25 | S.I. 155 of 2024 (Veritas) | `https://www.veritaszim.net/sites/veritas_d/files/SI%202024-155%20Cyber%20and%20Data%20Protection%20(Licensing%20of%20Data%20Controllers%20and%20Appointment%20of%20Data%20Protection%20Officers)%20Regulations,%202024.pdf` | HIGH |
| R2-21 | POTRAZ children's data guideline (Veritas) | `https://www.veritaszim.net/sites/veritas_d/files/02%20Processing%20of%20Children%27s%20Personal%20Information.pdf` | HIGH-MEDIUM |
| R2-08 | MLPC Act s 24, s 27(8) (FIU-hosted) | `https://www.fiu.co.zw/wp-content/uploads/2025/05/MLPCAct0924updated.pdf` | HIGH |
| R2-38 | S.I. 98 of 2026 s 28 | `https://www.fiu.co.zw/wp-content/uploads/2025/05/SI-98of2026-PVO-Risk-Based-Supervision.pdf` | HIGH |
