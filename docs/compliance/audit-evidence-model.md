# FundZim Audit and Evidence Model (Stage 1 design)

Status: **Stage 1 design.** It extends [AUDIT.md](../AUDIT.md) (Stage 0) with **evidence records**, as decided
in [ADR-019](../adr/ADR-019-regulatory-evidence-management.md). Audit events ship from Stage 3; evidence
records arrive with the first stage that collects compliance evidence (Stage 5 KYC, Stage 6 campaign review).

Related: [AUDIT.md](../AUDIT.md), [DATA-CLASSIFICATION.md](../DATA-CLASSIFICATION.md),
[PRIVACY.md](../PRIVACY.md), [identity-data-protection.md](../security/identity-data-protection.md),
[compliance-case-management.md](compliance-case-management.md),
[regulatory-requirements-register.md](regulatory-requirements-register.md),
[operational-controls.md](operational-controls.md), [LEDGER.md](../LEDGER.md).

---

## 1. Two records, two jobs

| | **Audit event** (`audit_events`) | **Evidence record** (`evidence_records`) |
|---|---|---|
| Answers | Who did what, to what, when, why, with what outcome | What material supported a decision, where it is, and that it is unchanged |
| Content | Small structured row; redacted metadata; IDs only | Pointer to an object (document, screenshot, provider report, call note, vendor result) plus its hash and classification |
| Classification | C2 (widely readable by investigators) | C2 or C3 (the referenced object may be C3) |
| Store | PostgreSQL, append-only, hash-chained ([AUDIT.md](../AUDIT.md) §4) | Metadata in PostgreSQL (append-only); objects in the **private** bucket (`private-kyc`, compliance prefix) — never public storage |
| Access | `audit.read` (scoped) | `evidence.view` per evidence type; C3 objects require the KYC/compliance permission plus justification, and every view is itself an audit event |

**Rule:** audit events reference evidence by `evidence_record_id`. They never embed the evidence.

---

## 2. Evidence record schema (conceptual)

```sql
CREATE TABLE evidence_records (
    id                 uuid        PRIMARY KEY,                -- UUIDv7
    evidence_type      text        NOT NULL,                   -- controlled list, §4
    subject_type       text        NOT NULL,                   -- 'user'|'organisation'|'campaign'|'payment'|'payout'|'case'|'reconciliation_run'|...
    subject_id         uuid        NOT NULL,
    related_refs       jsonb       NOT NULL DEFAULT '{}',      -- e.g. {"case_id":..., "payment_id":..., "provider":"...", "provider_reference":"..."}
    collected_at       timestamptz NOT NULL,                   -- UTC
    collected_by_type  text        NOT NULL CHECK (collected_by_type IN ('user','staff','system','provider','vendor')),
    collected_by_id    uuid,
    source             text        NOT NULL,                   -- 'upload'|'kyc_vendor'|'psp_report'|'webhook_inbox'|'staff_note'|'system_snapshot'|...
    storage_ref        text,                                   -- private bucket object key; NULL if the evidence is a DB row referenced in related_refs
    content_sha256     bytea       NOT NULL,                   -- hash of the object or canonical row snapshot
    size_bytes         bigint,
    media_type         text,
    classification     text        NOT NULL CHECK (classification IN ('C2','C3')),
    retention_class    text        NOT NULL,                   -- maps to retention schedule; periods pending LR-012
    legal_hold         boolean     NOT NULL DEFAULT false,     -- changed only via evidence_holds rows (below)
    supersedes_id      uuid        REFERENCES evidence_records(id), -- re-submission; the original is kept
    audit_event_id     uuid        NOT NULL                    -- the event that created this record
);

CREATE TABLE evidence_holds (                                  -- append-only hold history
    id uuid PRIMARY KEY, evidence_record_id uuid NOT NULL REFERENCES evidence_records(id),
    action text NOT NULL CHECK (action IN ('HOLD','RELEASE')), reason text NOT NULL,
    case_id uuid, requested_by uuid NOT NULL, approved_by uuid NOT NULL CHECK (approved_by <> requested_by),
    occurred_at timestamptz NOT NULL
);
```

Notes:

- `legal_hold` is a derived, cached flag maintained in the same transaction as an `evidence_holds` row. It is
  the only mutable column; the trigger allows changing it and nothing else.
- Objects are written once (object lock / versioning on the compliance prefix — Stage 18) and verified against
  `content_sha256` on every read. A mismatch is a SEV1 incident ([incident-response-workflows.md](incident-response-workflows.md)).
- `storage_ref` keys are random (UUIDs), never derived from names or ID numbers.
- C3 objects are encrypted with the dedicated key used for the private bucket
  ([identity-data-protection.md](../security/identity-data-protection.md)).

---

## 3. Common event fields

Every event below carries the [AUDIT.md](../AUDIT.md) §3 columns. The ones the master prompt requires, and
how they are filled:

| Field | Source |
|---|---|
| **Actor** | `actor_type`, `actor_id`, `actor_role`, `on_behalf_of`; `system` for automatic actions (job name in metadata); `provider` for callbacks |
| **Action** | `action` from the catalogue (Go constant set; unknown actions fail tests) |
| **Target** | `target_type`, `target_id` |
| **Timestamp** | `occurred_at` (business time, UTC) and `recorded_at` |
| **Correlation ID** | `correlation_id` (spans request → job → provider → webhook); `request_id` |
| **Reason** | `reason` (machine code); `justification` (staff text, required for **J** actions) |
| **Provider reference** | `metadata.provider`, `metadata.provider_reference`, `metadata.provider_event_id` (where applicable) |
| **Outcome** | `outcome` = `success` / `denied` / `failed` |
| **Evidence links** | `metadata.evidence_record_ids[]` |
| **Ledger links** | `metadata.ledger_transaction_ids[]` where a posting resulted |

---

## 4. Event and evidence catalogue

**J** = staff justification text required. "Evidence" lists the `evidence_type` values created or linked.

### 4.1 Account registration

| Action | Actor | Target | Reason / outcome | Evidence |
|---|---|---|---|---|
| `user.created` | user | user | outcome success/failed | `TERMS_ACCEPTANCE` (terms version, timestamp, channel — LR-027), `CONSENT_RECORD` (per purpose — LR-035) |
| `auth.otp.sent`, `auth.otp.failed_max_attempts` | system | user | rate-limit reason codes | — |
| `user.phone.changed`, `user.email.changed` | user / staff (J) | user | step-up required | `CONTACT_VERIFICATION` |

### 4.2 Identity verification (KYC / KYB / beneficiary)

| Action | Actor | Target | Reason / outcome | Evidence |
|---|---|---|---|---|
| `kyc.submission.created` | user | kyc_submission | — | `ID_DOCUMENT` (C3), `SELFIE_LIVENESS` (C3) |
| `kyc.vendor.result.received` | vendor | kyc_submission | vendor result code; vendor reference in metadata | `VENDOR_RESULT` (C3, redacted payload) |
| `kyc.decision.recorded` (J) | staff (KYC_REVIEWER/COMPLIANCE) | kyc_submission | APPROVED / REJECTED + reason code | `REVIEW_NOTE` (C3) |
| `kyc.level.changed` | system | user | old/new level | links to decision event |
| `kyc.status.changed` (J) | staff | user | ACTIVE / PENDING_REVIEW / REJECTED / SUSPENDED | `REVIEW_NOTE` |
| `kyc.document.viewed` (J), `kyc.identity_number.revealed` (J) | staff | evidence_record | justification + case id | — (the view is the evidence) |
| `organisation.verification.decided` (J) | staff | organisation | level + reason | `REGISTRATION_DOCUMENT`, `DIRECTOR_LIST`, `BENEFICIAL_OWNER_DECLARATION`, `AUTHORISATION_LETTER` (C3) |
| `beneficiary.verification.decided` (J) | staff | beneficiary | VERIFIED / REJECTED | `CONSENT_EVIDENCE`, `RELATIONSHIP_EVIDENCE`, `INSTITUTION_CONFIRMATION` |
| `payout.destination.verified` | system / staff (J) | payout_destination | name-match result | `ACCOUNT_OWNERSHIP_CHECK` (masked account) |

### 4.3 Campaign approval

| Action | Actor | Target | Reason / outcome | Evidence |
|---|---|---|---|---|
| `campaign.submitted` | user | campaign | policy version pinned | `TERMS_ACCEPTANCE` (campaign terms), `SUPPORTING_DOCUMENT` |
| `campaign.review.claimed` | staff | campaign_review | conflict-of-interest attestation | — |
| `campaign.review.check_recorded` | system / staff | review_check_result | PASS / FLAG / FAIL | `SCREENING_RESULT`, `IMAGE_MATCH_REPORT`, `CALL_NOTE` |
| `campaign.state.changed` (J when staff) | staff / user / system | campaign | from → to, reason code | `REVIEW_NOTE` |
| `campaign.material_edit.flagged` | system | campaign_version | edit type | diff snapshot (`SYSTEM_SNAPSHOT`) |

### 4.4 Donation processing and provider callbacks

| Action | Actor | Target | Reason / outcome | Evidence |
|---|---|---|---|---|
| `payment.created` | user (or anonymous session) | payment | idempotency key hash in metadata | — |
| `payment.provider.called` | system | payment | ok / timeout / error → may produce `UNKNOWN` | `provider_reference` in metadata |
| `webhook.received` | provider | webhook_inbox row | signature valid / invalid | the `webhook_inbox` row **is** the evidence (redacted raw payload); event references its id |
| `webhook.signature.failed` | provider | webhook_inbox row | failed | same |
| `payment.state.changed` | system | payment | from → to, source WEBHOOK / POLL / RECONCILIATION | inbox row id or poll response snapshot (`PROVIDER_STATUS_SNAPSHOT`) |

### 4.5 Ledger posting

| Action | Actor | Target | Reason / outcome | Evidence |
|---|---|---|---|---|
| `ledger.transaction.posted` | system | ledger_transaction | posting rule name, idempotency key | links to the triggering payment/payout event; **amounts live in the ledger only** |
| `ledger.invariant.violation` | system | ledger_transaction / account | SEV1 | `SYSTEM_SNAPSHOT` |

### 4.6 Withdrawal requests and payout approvals

| Action | Actor | Target | Reason / outcome | Evidence |
|---|---|---|---|---|
| `payout.requested` | user / org rep | payout | eligibility result summary | `ELIGIBILITY_SNAPSHOT` (checks, results, limits used — [payout-eligibility-and-controls.md](../payments/payout-eligibility-and-controls.md)) |
| `payout.approved` (J), `payout.rejected` (J) | staff (FINANCE, ≠ requester) | payout | maker/checker ids | `REVIEW_NOTE` |
| `payout.approval.denied_self` | staff | payout | outcome `denied` | — |
| `payout.state.changed` | system | payout | SUBMITTED / PROCESSING / COMPLETED / FAILED / UNKNOWN / REVERSED, provider reference | `PROVIDER_STATUS_SNAPSHOT` |
| `payout.eligibility.rechecked` | system | payout | pre-submission re-check result | `ELIGIBILITY_SNAPSHOT` |

### 4.7 Compliance overrides, suspension and freezing

| Action | Actor | Target | Reason / outcome | Evidence |
|---|---|---|---|---|
| `compliance.override.requested` (J), `compliance.override.approved` (J) | staff (maker ≠ checker) | rule / limit / case | rule code, scope, expiry | `OVERRIDE_RECORD` |
| `compliance.exemption.expired` | system | override | auto-expiry | — |
| `user.suspended` (J), `kyc.status.changed` (J) | staff | user | reason code | `REVIEW_NOTE` |
| `campaign.state.changed` → FROZEN (J) | staff (COMPLIANCE/FINANCE) | campaign | reason code; ledger transaction id of the payable→held move | `CASE_LINK` |
| `payout.hold.applied` (J), `payout.hold.released` (J) | staff | payout / campaign | hold id | `CASE_LINK` |
| `killswitch.activated` (J) | staff | system scope | incident id | `INCIDENT_LINK` |

### 4.8 Refunds and disputes

| Action | Actor | Target | Reason / outcome | Evidence |
|---|---|---|---|---|
| `payment.refund.requested` (J) | staff (SUPPORT/COMPLIANCE) | refund | reason code (DONOR_REQUEST, DUPLICATE, FRAUD, CANCELLATION) | `DONOR_REQUEST` |
| `payment.refund.approved` (J) | staff (FINANCE, ≠ requester) | refund | — | — |
| `payment.refund.state.changed` | system | refund | provider confirmed / failed / unknown | `PROVIDER_STATUS_SNAPSHOT` |
| `payment.dispute.opened`, `payment.dispute.state.changed` | provider / system | dispute | provider dispute id, reason code | `CHARGEBACK_NOTICE`, `REPRESENTMENT_PACK` |

### 4.9 Reconciliation adjustments

| Action | Actor | Target | Reason / outcome | Evidence |
|---|---|---|---|---|
| `reconciliation.run.completed` | system | reconciliation_run | matched / mismatched counts | `PSP_SETTLEMENT_REPORT` (C2, hash-verified) |
| `reconciliation.mismatch.detected` | system | mismatch | category | — |
| `ledger.adjustment.requested` (J), `ledger.adjustment.approved` (J) | staff (FINANCE maker ≠ checker) | ledger_transaction | reason code; links to the mismatch | `ADJUSTMENT_SUPPORT` |
| `reconciliation.mismatch.resolved` (J) | staff | mismatch | resolution type | links |

---

## 5. Never embedded in an audit event or evidence metadata

Restating and extending [AUDIT.md](../AUDIT.md) §6:

- secrets of any kind (passwords, OTPs, tokens, API keys, webhook secrets, keys);
- card PAN, CVV, PIN, mobile-money PIN;
- identity document images, extracted document text, full ID/passport numbers, liveness media, biometric
  templates;
- full payout account numbers (masked form plus destination id only);
- medical detail (reference the campaign version or evidence record id);
- raw webhook or vendor payloads (reference the inbox row or evidence record);
- free-text staff notes containing C3 content — those are `REVIEW_NOTE` evidence (C3) instead;
- information that would tip off an STR subject in any user-visible activity history (LR-008).

---

## 6. Immutability

- `evidence_records` and `evidence_holds`: app role INSERT + SELECT only; UPDATE allowed on
  `evidence_records.legal_hold` alone via trigger; DELETE and TRUNCATE blocked by trigger (same pattern as
  [AUDIT.md](../AUDIT.md) §4).
- Corrections and re-submissions are new rows (`supersedes_id`); the original remains.
- Each evidence record is created **in the same DB transaction** as its audit event.
- Objects: write-once prefix; overwrite or delete is blocked by bucket policy, except by the retention job
  (§7).

## 7. Retention, deletion and legal hold

| Rule | Detail |
|---|---|
| Retention classes | `KYC`, `FINANCIAL`, `AUDIT`, `CASE`, `CONSENT`, `OPERATIONAL`. Periods are **not set**: LR-012 (retention) and LR-034 (erasure conflicts). Until resolved, nothing in `KYC`, `FINANCIAL`, `AUDIT` or `CASE` is deleted. |
| Retention job (Stage 17/18) | Deletes or crypto-shreds objects whose class period has elapsed **and** `legal_hold = false`; writes a `evidence.purged` audit event keeping id, type and hash (proof of what existed) without content. |
| Legal hold | Placed by COMPLIANCE (maker) and approved by a second COMPLIANCE or designated officer (checker) for an investigation, dispute, regulator or law-enforcement request (LR-032). Overrides retention and erasure. Release is equally maker-checker. |
| Erasure requests | Do not delete evidence under retention or legal hold; restricted access is applied instead, and the response follows LR-034. |
| Export | Regulator/law-enforcement export is a `audit.exported` / `evidence.exported` J-event, with recipient and legal basis (LR-032); exports are packaged with hashes for chain of custody. |

## 8. Implementation acceptance (Stage 3 / 5 / 6 / 10 / 11)

- Every catalogue action has a test proving the audit event and its evidence record are written in the same
  transaction.
- Redaction tests feed sentinel C3/C4 values and assert they never appear in `audit_events.metadata` or
  `evidence_records.related_refs`.
- Hash verification on read of every evidence object; a tampering test proves detection.
- Legal-hold tests: the retention job skips held records; releasing a hold needs two different staff.
