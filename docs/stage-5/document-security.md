# Stage 5 — Document Security

> Code: `internal/storage` (pipeline, scanner, tickets), `internal/platform/storage/sse.go` (SSE-C),
> `internal/kyc/documents.go` (linkage to subjects), `internal/verification/documents.go` (HTTP).
> Decisions: [ADR-035](../adr/ADR-035-restricted-data-access-and-documents.md), [ADR-034](../adr/ADR-034-verification-state-models.md) §3.
> **The development scanner is not malware protection**, and ClamAV in the local compose stack is not a
> production deployment.

## 1. Lifecycle (`stored_object_scan`)

```
'' → UPLOADED → QUARANTINED → SCANNING → CLEAN | REJECTED | FAILED_SCAN ;  FAILED_SCAN → SCANNING (retry)
UPLOADED → REJECTED (failed upload validation) ; any state → DELETED (soft; the row stays as proof)
```

| Step | What happens |
|---|---|
| Upload (API) | Multipart: form fields **first**, then the file. The subject is authorised from the fields **before** the file is read (strangers cause no storage write). The body is read with a hard cap (`UPLOAD_MAX_BYTES`, 10 MiB), hashed (SHA-256) and **sniffed by magic bytes** (allow-list JPEG, PNG, PDF; markup in the browser sniffing window is refused). The declared type must equal the sniffed type. Failures → `REJECTED` with a reason code; nothing is written to the bucket. Success → bytes in `<class>/quarantine/<id>`, `QUARANTINED`, outbox `storage.object_uploaded`, audit — one transaction |
| Scan (worker) | Claim QUARANTINED (or due FAILED_SCAN) → `SCANNING` (fenced by `scan_attempts`) → scanner → `CLEAN` (server-side copy to `objects/<id>`, quarantine copy deleted) / `REJECTED` (`MALWARE_DETECTED`) / `FAILED_SCAN` (scanner or storage error). **A scanner error never produces CLEAN.** Interrupted scans become FAILED_SCAN (`SCAN_INTERRUPTED`); the rescan job (every 30 s) retries with backoff |
| Attach | The kyc module links the object to its subject (`kyc.kyc_documents`, composite FK to a PRIVATE_KYC object) **under the subject's row lock**, so documents cannot be added after submission. If attaching fails, the stored object is soft-deleted |
| Open | Only `CLEAN` objects, from `objects/<id>`, SHA-256 re-verified while streaming |
| Cleanup | Upload sessions not completed within `UPLOAD_TTL` (15 min) → object `DELETED` and partial bytes purged |

Only `CLEAN` documents satisfy verification requirements. A `REJECTED` (e.g. EICAR) document can never be
downloaded or counted (integration-tested with the real ClamAV engine).

**Filenames are never trusted or stored.** Object keys are server-generated from the object ID; the
download filename is `document.<ext>` from the sniffed type (integration test: a `../../etc/…pdf.exe`
filename leaves no trace in any response).

## 2. Scanners

`Scanner` interface (`Scan(ctx, reader) → Verdict{Clean, Signature, Engine}`):
- `clamd` — ClamAV `INSTREAM` over TCP (`CLAMAV_ADDR`). The local stack runs `clamav/clamav:1.5.3`
  (pinned by digest) and uses it by default.
- `dev` — detects only the EICAR test string, reports engine `dev-scanner (NOT malware protection)`, and is
  refused by configuration outside development/test and by the service when `APP_ENV=production`.

## 3. Encryption at rest

Private classes (`PRIVATE_KYC`, `PRIVATE_EVIDENCE`) are written with **S3 SSE-C**: Garage encrypts each object
with AES-256 under a per-object key derived as HMAC-SHA-256(`STORAGE_SSE_C_KEY`, `fundzim/sse-c/v1/<object_id>`).
The row records the key id. Limits: the master key is a local environment key (KMS is Stage 18); object
metadata is not encrypted; the key travels in request headers, so storage must be reached over TLS outside
local development; no key rotation yet (an object under a different key id cannot be opened). Without an SSE
key (development/test only) objects are stored unencrypted and the row says so
(`none/garage-at-rest-unencrypted-dev`); `STORAGE_SSE_C_KEY` is required everywhere else.

Credentials are scoped per bucket class (public media, KYC, evidence: three different buckets and keys); the
worker holds only the KYC and evidence credentials.

## 4. Access control

| Who | Can |
|---|---|
| Owner (subject user; ORG_ADMIN for organisation subjects; beneficiary/destination editors) | list, view metadata, get a download ticket for **own** documents; delete only while the subject is editable |
| Reviewer | only with `kyc.document.view`, a **fresh step-up** and **being the assigned reviewer** of that case/beneficiary/destination (`403 NOT_ASSIGNED`) |
| Support, admin, super-admin | **no** document access (they do not hold `kyc.document.view`); `403 PERMISSION_DENIED` |
| Anyone else | `404 DOCUMENT_NOT_FOUND` (existence not revealed) |

There is **no direct or public URL** to any document. Download is two steps:
1. `POST /verification/documents/{id}/access` re-authorises and returns a ticket URL valid for
   `DOCUMENT_TICKET_TTL` (60 s; capped at 5 min). The ticket is HMAC-SHA-256 over (object, user, session,
   expiry) with `DOCUMENT_TICKET_KEY`, so it is useless for another document, user or **session**
   (integration-tested: another session of the same user → 403).
2. `GET …/content?ticket=` re-authorises again, verifies the ticket (constant-time MAC check before the expiry
   check), **writes a security audit event** `kyc.document.accessed` (document id, type, viewer kind — no
   content), and streams with `Content-Disposition: attachment`, `X-Content-Type-Options: nosniff`,
   `Content-Security-Policy: sandbox; default-src 'none'`, `Cache-Control: private, no-store`,
   `Cross-Origin-Resource-Policy: same-origin`.

Every upload writes `kyc.document.uploaded`; evidence records (`audit.evidence_records`) hold metadata, the
SHA-256 and the object reference — never content. Logs never contain document content, filenames or
identity values.

## 5. Retention

Deletion is soft (`DELETED`; the row and evidence record stay). Purging of retained identity documents and
evidence holds is not implemented: retention periods are LEGAL_REVIEW_REQUIRED (LR-012, LR-074).
