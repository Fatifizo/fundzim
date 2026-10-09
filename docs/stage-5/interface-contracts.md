# Stage 5 Interface Contracts (work-stream coordination)

Binding for every Stage 5 work stream. Decisions: [ADR-034](../adr/ADR-034-verification-state-models.md)
(state vocabularies), [ADR-035](../adr/ADR-035-restricted-data-access-and-documents.md) (roles, gateways,
documents). Conventions from Stages 3–4 apply: `/api/v1`, envelope, `UPPER_SNAKE` error codes, strict JSON
decoding (`httpx.DecodeJSON`), CSRF on unsafe methods, route policies (deny by default), `audit.Record`
semantics, outbox at-least-once, UUIDv7, `timestamptz`, no floats for money (none handled here).

## 1. Ownership

| Stream | Owns (may edit) | Must not edit |
|---|---|---|
| **Lead** | `internal/app`, `internal/platform/config`, `internal/archtest`, `go.mod/go.sum` (others may `go get`, never `go mod tidy`), `internal/audit`, `internal/kyc`, `internal/beneficiaries`, `internal/payouts`, `internal/users`, `internal/organisations`, `internal/auth`, migrations `20261009150000`, `150200`, `150300`, `150400`, `150700`, `.env.example`, `compose.yaml` (env only), CI | — |
| **S** (storage/documents) | `internal/storage`, `internal/platform/storage`, `internal/platform/httpx/bodylimit*` (route exemptions), migration `20261009150100`, ClamAV service block in `compose.yaml`, `deploy/docker/clamav/*`, `tests/integration/storage_*_test.go` | anything else (ask the lead) |
| **C** (compliance/risk) | `internal/compliance`, `internal/risk`, migrations `20261009150500` (risk) and `20261009150600` (compliance), `tests/integration/compliance_*_test.go` | anything else |
| **F** (frontend) | `apps/web/**` | backend |

Register consumers/routes by sending the lead a snippet (the lead wires `internal/app`). New config keys:
send the lead the name, default and validation.

## 2. Databases and pools

| Module | Pool / role | Schemas |
|---|---|---|
| users, auth, organisations, storage, beneficiaries, payouts, risk | `fundzim_app` (API) / `fundzim_worker` (worker) | `app`, `risk`, `audit` (via `audit.Record`), outbox (`outbox.Write`) |
| kyc | `fundzim_kyc` (`DATABASE_KYC_URL`) | `kyc` only; audit/evidence/outbox **only through gateways** |
| compliance | `fundzim_compliance` (`DATABASE_COMPLIANCE_URL`) | `compliance`; `SELECT` on `risk.risk_signals`, `risk.risk_assessments`, `risk.risk_decisions`, `risk.limits`; gateways |

`app.apply_runtime_grants()` v3 (migration `150000`) derives grants: `kyc.*` → `fundzim_kyc`,
`compliance.*` → `fundzim_compliance` (SELECT, INSERT; UPDATE unless a `forbid_mutation` UPDATE trigger
exists; never DELETE; reference tables listed in the procedure are SELECT-only). Every migration ends with
`CALL app.apply_runtime_grants();`.

### 2.1 Gateways (migration `150000`, SECURITY DEFINER)

```sql
audit.append_event(p_id uuid, p_security boolean, p_occurred_at timestamptz, p_actor_type text, p_actor_id uuid,
  p_actor_role text, p_action text, p_target_type text, p_target_id uuid, p_outcome text, p_request_id text,
  p_ip inet, p_reason text, p_justification text, p_metadata jsonb) RETURNS uuid
audit.record_evidence(p_id uuid, p_evidence_type text, p_subject_type text, p_subject_id uuid, p_related_refs jsonb,
  p_collected_at timestamptz, p_collected_by_type text, p_collected_by_id uuid, p_source text, p_storage_ref text,
  p_content_sha256 bytea, p_size_bytes bigint, p_media_type text, p_classification text, p_retention_class text,
  p_audit_event_id uuid) RETURNS uuid
app.enqueue_outbox(p_id uuid, p_aggregate_type text, p_aggregate_id uuid, p_event_type text, p_payload jsonb,
  p_correlation_id text, p_occurred_at timestamptz) RETURNS uuid
```

Allowed prefixes by `session_user`: `fundzim_kyc` → actions `kyc.*`, `kyb.*`, `beneficiary.*`, events `kyc.*`;
`fundzim_compliance` → actions `compliance.*`, `risk.*`, events `compliance.*`. Go helpers:
`audit.RecordGateway(ctx, tx, audit.Event)` and `outbox.WriteGateway(ctx, tx, outbox.Event)` (same structs as the
direct versions). The app pool keeps using `audit.Record` / `outbox.Write`.

### 2.2 Evidence

`audit.evidence_records` / `audit.evidence_holds` (Stage 2 draft 0003, migration `150000`). App-pool modules
insert via `audit.RecordEvidence(ctx, tx, audit.Evidence{...}) (id, error)`; restricted pools via the gateway
variant `audit.RecordEvidenceGateway`. Evidence rows hold metadata + SHA-256 + `storage_ref` (object id), never
content.

## 3. Storage / documents (stream S → consumers: kyc, beneficiaries, payouts, compliance)

Package `internal/storage` (module `storage`, imports platform + audit only). Migration `150100` creates
`app.stored_objects` and `app.upload_sessions` per draft 0005 with ADR-034 §3 statuses (`scan_status`:
UPLOADED, QUARANTINED, SCANNING, CLEAN, REJECTED, FAILED_SCAN, DELETED) and bucket classes
`PUBLIC_MEDIA`, `PRIVATE_KYC`, `PRIVATE_EVIDENCE`; purposes include `KYC_DOCUMENT`, `KYB_DOCUMENT`,
`BENEFICIARY_EVIDENCE`, `PAYOUT_DESTINATION_EVIDENCE` (PRIVATE_KYC) and `COMPLIANCE_EVIDENCE` (PRIVATE_EVIDENCE).

```go
type Object struct { ID, BucketClass, Purpose, OwnerModule, UploadedBy string; Status string; SniffedType string;
  SizeBytes int64; SHA256 []byte; CreatedAt time.Time; ScannedAt *time.Time; RejectReason string }
type UploadInput struct { BucketClass, Purpose, OwnerModule, UploaderUserID, DeclaredContentType string;
  Body io.Reader; MaxBytes int64 }
func (s *Service) Upload(ctx context.Context, in UploadInput) (Object, error)   // streams to quarantine, sha256, sniff; QUARANTINED; emits storage.object_uploaded
func (s *Service) Get(ctx context.Context, id string) (Object, error)
func (s *Service) Open(ctx context.Context, id string) (io.ReadCloser, Object, error) // CLEAN only, else ErrNotClean
func (s *Service) Delete(ctx context.Context, id, actorID string) error             // soft → DELETED
func (s *Service) IssueTicket(objectID, userID, sessionID string, ttl time.Duration) (ticket string, exp time.Time)
func (s *Service) VerifyTicket(ticket, objectID, userID, sessionID string) error    // ErrTicketExpired / ErrTicketInvalid
// errors (errs kinds): ErrTooLarge (413 FILE_TOO_LARGE), ErrUnsupportedType (415/422 UNSUPPORTED_FILE_TYPE),
// ErrTypeMismatch (422 FILE_TYPE_MISMATCH: declared vs sniffed), ErrEmptyFile, ErrNotClean (409 DOCUMENT_NOT_AVAILABLE),
// ErrStorageUnavailable (503)
```

Scanning: `Scanner` interface `Scan(ctx, io.Reader) (Verdict{Clean bool; Signature string; Engine string}, error)`;
implementations `clamd` (TCP to `CLAMAV_ADDR`) and `dev` (EICAR-string detection only, refused when
`APP_ENV=production`, engine reported as `dev-scanner (NOT malware protection)`). A worker job consumes
`storage.object_uploaded`: QUARANTINED → SCANNING → CLEAN (copy to `objects/` key, delete quarantine copy) |
REJECTED | FAILED_SCAN (retry with backoff; never CLEAN by default). Emits `storage.object_scanned`
`{object_id, status, owner_module, purpose}`. Expired UPLOADED rows → DELETED (cleanup job). Per-purpose size
caps: config `UPLOAD_MAX_BYTES` (default 10 MiB). The API body-limit middleware exempts upload routes
(`httpx` route option); upload handlers apply their own `http.MaxBytesReader`.

## 4. Compliance and risk (stream C)

- `internal/risk` (app pool; imports platform, audit): tables `risk.limits` (+ `risk.limit_change_requests`,
  maker-checker; types `REGULATORY`/`PROVIDER`/`INTERNAL_RISK` — the brief's REGULATORY_LIMIT/PROVIDER_LIMIT/
  INTERNAL_POLICY_LIMIT), `risk.risk_signals`, `risk.risk_assessments` (rating LOW/STANDARD/ENHANCED/
  RESTRICTED, `model_version`, contributing rule codes), `risk.risk_decisions` (NO_ACTION / MANUAL_REVIEW /
  ESCALATE, never "fraud"). Consumes events in §6 marked **risk**, records signals (dedupe on
  `(source_event_id, signal_type)`), recomputes the subject's assessment with a versioned model, and on
  ESCALATE emits `risk.escalation_recommended`. Public Go API for reviewers:
  `risk.Service.Latest(ctx, subjectType, subjectID) (Assessment, []Signal, error)`.
- `internal/compliance` (compliance pool; imports platform, audit, users, organisations, kyc, risk, storage):
  tables per draft 0013 subset (`compliance_cases`, `compliance_case_events`, `compliance_case_links`,
  `compliance_case_notes` encrypted, RLS) with ADR-034 §5 statuses. Consumes `kyc.case_escalated`,
  `risk.escalation_recommended` → opens/links a case; emits `compliance.case_opened`,
  `compliance.case_resolved {case_id, decision, links[]}`. Admin API in §7.4.

## 5. kyc / beneficiaries / payouts (lead)

Exported for other modules: `kyc.Service.Level(ctx, userID) (Level, Status, error)`,
`kyc.Service.OrganisationLevel(ctx, orgID)`, `kyc.Service.CaseSummary(ctx, caseID)` (for compliance links).

## 6. Outbox events (payloads: IDs and codes only)

| Event | Payload | Consumers |
|---|---|---|
| `storage.object_uploaded` | object_id, bucket_class, purpose | storage scan job |
| `storage.object_scanned` | object_id, status, owner_module, purpose | kyc (document status refresh; notify on REJECTED), **risk** (REJECTED) |
| `kyc.case_submitted` | case_id, kind (KYC/KYB), subject_type, subject_id | notifications (lead) |
| `kyc.information_requested` | case_id, kind, subject_type, subject_id | notifications |
| `kyc.case_approved` / `kyc.case_rejected` | case_id, kind, subject_type, subject_id, level? , reason_code | notifications; projections (lead); **risk** (rejected) |
| `kyc.case_escalated` | case_id, kind, subject_type, subject_id, reason_code | **compliance** |
| `kyc.verification_expired` / `_suspended` / `_revoked` | case_id, kind, subject_type, subject_id | notifications; projections |
| `kyc.duplicate_identity_detected` | case_id, profile_id, other_profile_count | **risk** |
| `kyc.level_changed` | subject_type (USER/ORGANISATION), subject_id, level, status | users/organisations projections (lead) |
| `beneficiaries.verification_*` (submitted, information_requested, approved, rejected, escalated, revoked) | beneficiary_id, owner_type, owner_id, reason_code? | notifications; **risk**; compliance (escalated) |
| `payouts.destination_created` / `_changed` / `_verified` / `_rejected` / `_suspended` | destination_id, owner_type, owner_id | notifications; **risk** (changed, rejected) |
| `risk.escalation_recommended` | subject_type, subject_id, assessment_id, decision_id, rating | compliance |
| `compliance.case_opened` / `compliance.case_resolved` | case_id, decision?, subject links | kyc (resolution of escalated cases) |

Subject types (shared): `USER`, `ORGANISATION`, `BENEFICIARY`, `PAYOUT_DESTINATION`, `KYC_CASE`, `KYB_CASE`.

## 7. HTTP API

### 7.1 Permissions (new rows in migration `150200`)

Existing: `kyc.status.view`, `kyc.case.review`, `kyc.document.view` (step-up), `kyc.identity_number.reveal`
(step-up), `kyc.decision.record` (step-up), `beneficiary.verification.decide` (step-up),
`org.verification.decide` (step-up), `case.create`, `case.manage`. New: `payout_destination.verify` (step-up;
KYC_REVIEWER, COMPLIANCE), `verification_policy.request` (COMPLIANCE), `verification_policy.approve` (step-up;
COMPLIANCE — maker ≠ checker), `risk.view` (KYC_REVIEWER, COMPLIANCE), `compliance.case.view` (COMPLIANCE,
SECURITY_ADMIN read-only? no — COMPLIANCE only). SUPPORT, ADMIN, SUPER_ADMIN get **none** of these.

### 7.2 User / organisation endpoints (policy `User` unless noted; organisation routes check membership)

| Method & path | Body → `data` | Notes |
|---|---|---|
| `GET /kyc/status` | → `{level, status, risk_level, case: KycCase|null, gates: {create_draft, submit_campaign, withdraw: bool}}` | `case` = current non-final case or latest decided |
| `POST /kyc/cases` | `{target_level:"IDENTITY_VERIFIED"}` → **201** `KycCase` (DRAFT) | `409 CASE_ALREADY_OPEN`; `422 PREREQUISITES_NOT_MET` (verified email + phone) |
| `GET /kyc/cases/current` | → `KycCase|null` | |
| `PATCH /kyc/cases/{case_id}` | any of `{legal_first_name, legal_last_name, date_of_birth (YYYY-MM-DD), nationality (ISO-2), country_of_residence (ISO-2), id_document_type (ZW_NATIONAL_ID|ZW_PASSPORT|FOREIGN_PASSPORT), id_document_number, id_document_expiry (date|null), residential_address {line1,line2,city,province,postal_code,country}}` → `KycCase` | DRAFT or ADDITIONAL_INFORMATION_REQUIRED only (`409 CASE_NOT_EDITABLE`); `version` optimistic check via `If-Match: "<version>"` optional |
| `POST /kyc/cases/{case_id}/submit` | → `KycCase` (SUBMITTED) | `422 SUBMISSION_INCOMPLETE` with `details[{field, code}]` (MISSING_FIELD, DOCUMENT_REQUIRED, DOCUMENT_NOT_CLEAN, UNDERAGE, DOCUMENT_EXPIRED, INVALID_FORMAT); idempotent on retry |
| `POST /kyc/cases/{case_id}/withdraw` | → `KycCase` | |
| `POST /organisations/{org_id}/kyb` | `{}` → **201** `KybCase` | ORG_ADMIN; verified email |
| `GET /organisations/{org_id}/kyb` | → `{level, status, case: KybCase|null}` | any member (documents/person details only for ORG_ADMIN) |
| `PATCH /organisations/{org_id}/kyb` | `{registered_name, trading_name, registration_number, registry, country_of_registration, registered_address{...}}` → `KybCase` | ORG_ADMIN; editable states only |
| `POST /organisations/{org_id}/kyb/persons` | `{full_name, roles:[DIRECTOR|TRUSTEE|OFFICE_BEARER|BENEFICIAL_OWNER|CONTROLLER|REPRESENTATIVE], ownership_bp?, date_of_birth?, nationality?, id_document_type?, id_document_number?}` → **201** `KybPerson` | ORG_ADMIN; `ownership_bp` 0–10000 integer |
| `DELETE /organisations/{org_id}/kyb/persons/{person_id}` | → **204** | editable states only |
| `POST /organisations/{org_id}/kyb/submit` · `/withdraw` | → `KybCase` | submitter must hold IDENTITY_VERIFIED (policy) → `422 REPRESENTATIVE_NOT_VERIFIED` |
| `POST /beneficiaries` | `{owner_organisation_id?, beneficiary_type (SELF|INDIVIDUAL|MINOR|INCAPACITATED_ADULT|ORGANISATION|COMMUNITY_GROUP), display_name, full_name?, date_of_birth?, beneficiary_organisation_id?, relationship:{type, description?}, authority_basis (NOT_REQUIRED|BENEFICIARY_CONSENT|PARENTAL_RESPONSIBILITY|GUARDIANSHIP_ORDER|LEGAL_REPRESENTATION|ORGANISATION_AUTHORITY|GROUP_MANDATE|INSTITUTION_CONFIRMATION)}` → **201** `Beneficiary` (verification DRAFT) | owner = caller or an org where caller is ORG_ADMIN |
| `GET /beneficiaries` · `GET /beneficiaries/{beneficiary_id}` | → `[Beneficiary]` / `Beneficiary` | own + orgs where member |
| `PATCH /beneficiaries/{beneficiary_id}` | display_name, full_name, date_of_birth, relationship, authority_basis | editable states only |
| `POST /beneficiaries/{beneficiary_id}/submit` | → `Beneficiary` | evidence requirements per policy (`422 SUBMISSION_INCOMPLETE`) |
| `POST /payout-destinations` | `{owner_organisation_id?, payee:{type: OWNER|BENEFICIARY, beneficiary_id?}, rail (ECOCASH|ONEMONEY|INNBUCKS|OMARI|BANK_TRANSFER|ZIMSWITCH), currency (USD|ZWG), holder_name, account_identifier, bank_code?}` → **201** `PayoutDestination` | `422 INVALID_ACCOUNT_FORMAT`; duplicate live destination → `409 DESTINATION_EXISTS` |
| `GET /payout-destinations` · `/{destination_id}` | → list / one | owner's (org: ORG_ADMIN) |
| `PATCH /payout-destinations/{destination_id}` | holder_name, account_identifier, bank_code | new version → PENDING_VERIFICATION reset; `If-Match` version |
| `POST /payout-destinations/{destination_id}/verification` | `{method: BANK_LETTER|BANK_STATEMENT|MOBILE_MONEY_STATEMENT|PROVIDER_LOOKUP, document_ids?}` (`document_ids` accepted, not used) → `PayoutDestination` (PENDING_VERIFICATION) | PROVIDER_LOOKUP without a provider → ownership `PROVIDER_CONFIRMATION_REQUIRED` |
| `DELETE /payout-destinations/{destination_id}` | → **204** (RETIRED) | |
| `POST /verification/documents` | `multipart/form-data`: `file`, `subject_type` (KYC_CASE|KYB_CASE|KYB_PERSON|BENEFICIARY|PAYOUT_DESTINATION), `subject_id`, `document_type`, `side?` → **201** `Document` | caller must be allowed to edit the subject; `413 FILE_TOO_LARGE`, `422 UNSUPPORTED_FILE_TYPE`/`FILE_TYPE_MISMATCH`, `409 CASE_NOT_EDITABLE` |
| `GET /verification/documents?subject_type=&subject_id=` · `GET /verification/documents/{document_id}` | → `[Document]` / `Document` | metadata only |
| `POST /verification/documents/{document_id}/access` | → `{url, expires_at}` | owner (own docs) or authorised reviewer (`kyc.document.view`, step-up, assigned) |
| `GET /verification/documents/{document_id}/content?ticket=` | → bytes | ticket bound to session, 60 s; audited |
| `DELETE /verification/documents/{document_id}` | → **204** | owner, editable case only |

Shapes:

```
KycCase      {id, kind:"KYC", status, target_level, policy_version, identity: {legal_first_name, legal_last_name,
              date_of_birth, nationality, country_of_residence, id_document_type, id_document_number_masked,
              id_document_expiry, residential_address}|null, documents:[Document], requirements:[{document_types:[...],
              satisfied: bool}], information_requests:[{id, message, items:[...], requested_at,
              responded_at|null}], decision:{outcome, reason_code, message, decided_at}|null, submitted_at|null,
              created_at, updated_at, version}
KybCase      {id, kind:"KYB", organisation_id, status, target_level, details:{registered_name, trading_name,
              registration_number, registry, country_of_registration, registered_address}, persons:[KybPerson],
              documents, requirements, information_requests, decision, submitted_at, created_at, updated_at, version}
KybPerson    {id, full_name, roles, ownership_bp|null, id_document_type|null, id_document_number_masked|null}
Beneficiary  {id, owner:{type, id}, beneficiary_type, kind:"INDIVIDUAL"|"ORGANISATION", display_name, full_name|null,
              relationship:{type, description}, authority_basis, verification:{status, risk_level, requires_second_approval,
              information_requests, decision}, documents, requirements, created_at, updated_at, version}
PayoutDestination {id, owner:{type, id}, payee:{type, beneficiary_id|null}, category (MOBILE_MONEY_WALLET|BANK_ACCOUNT),
              rail, provider_name, currency, holder_name, masked_identifier, status, checks:{format_validated: bool,
              ownership: NOT_STARTED|PENDING|PROVIDER_CONFIRMATION_REQUIRED|CONFIRMED|FAILED,
              compliance: PENDING|APPROVED|REJECTED}, last_reviewed_at|null, eligible_for_payout: false (always false in Stage 5),
              created_at, updated_at, version}
Document     {id, subject_type, subject_id, document_type, side, status (scan status), media_type, size_bytes,
              uploaded_at, rejected_reason|null}
```

`eligible_for_payout` is always `false` in Stage 5 (no payout processing exists).

### 7.3 Reviewer endpoints (`/admin/verification/...`)

| Method & path | Policy | Body → `data` |
|---|---|---|
| `GET /admin/verification/cases?type=KYC|KYB|BENEFICIARY|PAYOUT_DESTINATION&status=&assigned=me|unassigned|any&limit=&cursor=` | `kyc.case.review` | `{items:[ReviewCaseSummary], next_cursor}` (opaque keyset cursor `<submitted_at>|<id>`) |
| `GET /admin/verification/cases/{case_id}` | `kyc.case.review` (+ type-specific) | `ReviewCase` (identity masked, documents metadata, history, risk, `review {risk_level, requires_second_approval, pending_outcome, pending_decided_by}` for KYC/KYB) |
| `POST …/{case_id}/assign` | `kyc.case.review` | `{assignee_id?}` (default self; another assignee must be active staff with `kyc.case.review`, else `422 ASSIGNEE_NOT_ELIGIBLE`) |
| `POST …/{case_id}/start-review` | assigned reviewer | — |
| `POST …/{case_id}/request-info` | assigned reviewer | `{message, items:[...]}` |
| `POST …/{case_id}/approve` | `kyc.decision.record` / `org.verification.decide` / `beneficiary.verification.decide` / `payout_destination.verify` (step-up) | `{reason_code, note}` (note required; no `conditions` — screening conditions are recorded server-side); four-eyes cases → `202 {status, awaiting_second_approval: true}` |
| `POST …/{case_id}/second-approval` | same, different person | `{note}` |
| `POST …/{case_id}/reject` | same | `{reason_code, note, user_message}` |
| `POST …/{case_id}/escalate` | `kyc.case.review` | `{reason_code, note}` → compliance case |
| `POST …/{case_id}/suspend` · `/reopen` · `/revoke` | decision permission | `{reason_code, note}` |
| `POST …/{case_id}/reveal-identity-number` | `kyc.identity_number.reveal` (step-up) | `{justification}` → `{id_document_number}` (audited) |
| `GET /admin/verification/policies` · `POST` · `POST …/{policy_id}/approve` | `verification_policy.request` / `.approve` | versioned policy maker-checker |

Rules: no decision on own verification (subject user, organisation member/representative, beneficiary owner)
→ `403 SELF_DECISION_FORBIDDEN` (also DB-enforced); only the assigned reviewer decides (`409 NOT_ASSIGNED`);
concurrent decisions → exactly one succeeds (`409 CASE_STATE_CHANGED`); notes are reviewer-only.

### 7.4 Compliance (stream C, `/admin/compliance/cases`)

`GET` (filters status/severity/assigned), `GET /{case_id}`, `POST /{case_id}/assign|start|request-info|
escalate|notes|resolve|approve-resolution|close|reopen`, `POST /admin/compliance/cases` (manual open,
`case.create`). Permission `case.manage` (step-up for resolve/approve/close/reopen); `compliance.case.view`
to read. Notes never leave compliance endpoints.

## 8. Migrations (timestamps)

`20261009150000` audit evidence + gateways + grants v3 (lead) · `150100` storage (S) · `150200` kyc +
permissions (lead) · `150300` beneficiaries (lead) · `150400` payout destinations (lead) · `150500` risk (C) ·
`150600` compliance (C) · `150700` reserved (lead: projections / follow-ups). Format as in Stage 4 (comments,
`-- +goose Up`, `StatementBegin`, `SET LOCAL lock_timeout/statement_timeout`, end with
`CALL app.apply_runtime_grants();`, working Down that lifts append-only triggers only for its own rows).

## 9. Changes during implementation (2026-10-09)

The sections above were updated in place; this list records what differs from the first version of this contract.
`api/openapi/fundzim-v1.yaml` is the authoritative HTTP contract.

- **Requirements shape:** `requirements: [{document_types: [...], satisfied}]` — each entry is a set of
  alternative document types (one CLEAN document of any of them satisfies it); there is no `sides` field.
- **Approve has no `conditions`:** the strict decoder refuses it (`422 UNKNOWN_FIELD`). Screening conditions
  (`sanctions_pep_screening: NOT_PERFORMED_SCREENING_PROVIDER_NOT_SELECTED`) are recorded by the server.
- **Decisions require `note`** (`422 note INVALID_LENGTH` when empty); rejections also require `user_message`.
- **Queue envelope:** `data: {items, next_cursor}` (not `meta.next_cursor`). The cursor is an opaque keyset
  `<submitted_at RFC3339Nano>|<id>`; pass it back unchanged; an invalid cursor is `422 cursor INVALID_FORMAT`.
- **Case detail** gained `review: {risk_level, requires_second_approval, pending_outcome, pending_decided_by}`
  for KYC and KYB cases.
- **Resubmitting a SUBMITTED KYC case** is an idempotent `200` no-op (same version).
- **Assignment** to someone else requires an active staff account holding `kyc.case.review`
  (`422 ASSIGNEE_NOT_ELIGIBLE`). Self-review checks also cover the personal account linked to a staff account.
- **Policy rules** gained `adult_age` (INTERNAL_POLICY; LR-021 open), used for KYC `UNDERAGE` and the MINOR
  beneficiary boundary.
- **Uploads** of unsupported types return `422 UNSUPPORTED_FILE_TYPE`.
- **Beneficiary types** are the six implemented types (no `INSTITUTION`); payout verification methods include
  `BANK_STATEMENT`.
- Destination `start-review` is accepted but has no effect (assignment starts the review).

