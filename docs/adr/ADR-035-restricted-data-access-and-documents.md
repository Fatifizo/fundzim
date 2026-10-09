# ADR-035: Restricted database roles, write gateways and identity-document access

- **Status:** Accepted
- **Date:** 2026-10-09
- **Deciders:** Technical lead (Stage 5)
- **Stage:** 5

## Context

KYC data is C3 RESTRICTED (CLAUDE.md, ADR-009). The Stage 2 grant design (`design/sql/0018_grants.sql`, lead
decisions C-3/C-10) gives the `kyc` schema only to the `fundzim_kyc` role and the `compliance` schema only to
`fundzim_compliance`, with no direct privileges on `audit.*` or the outbox: they write through SECURITY
DEFINER gateway functions. Stage 5 must also serve identity documents to reviewers without public URLs or
leakable long-lived links.

## Decision

1. **Separate connection pools.** The `kyc` module uses `DATABASE_KYC_URL` (role `fundzim_kyc`); the
   `compliance` module uses `DATABASE_COMPLIANCE_URL` (role `fundzim_compliance`). The API and the worker
   both hold these pools; no other module receives them (architecture tests check imports; the composition
   root passes pools explicitly). `fundzim_app` has **no** privilege on `kyc` or `compliance`.
2. **Write gateways** (owned by `fundzim_migrator`, `SECURITY DEFINER`, pinned `search_path`, input-validated):
   `audit.append_event(…)` (business or security stream), `audit.record_evidence(…)` and
   `app.enqueue_outbox(…)`. Each validates the caller (`session_user`) against an allow-list of action and
   event-type prefixes, rejects metadata keys that would carry C3/C4 values, and appends inside the caller's
   transaction, so domain row + audit event + evidence + outbox commit atomically. The restricted roles cannot
   read other modules' audit events or outbox rows.
3. **Cross-pool consistency.** A kyc transaction cannot include `app` writes. Projections (`users.kyc_level`,
   organisation verification) and notifications are updated from outbox events by worker consumers; decisions
   that need live state (e.g. KYB approval re-checking the representative's membership) call the owning module
   synchronously before deciding.
4. **Compliance confidentiality.** `compliance.compliance_cases` and its children have row-level security for
   `fundzim_compliance`; `RESTRICTED_STR` rows are visible only inside a transaction that sets
   `fundzim.str_access` after a permission check (protects against accidental exposure, not a compromised
   module — Stage 2 note retained).
5. **Documents.** Uploads go through the API (never browser-to-bucket): size cap per purpose, magic-byte
   sniffing against an allow-list (JPEG, PNG, PDF), random server-side keys, SHA-256 recorded, quarantine
   prefix, malware scan by a `Scanner` (ClamAV `clamd` where available; a `dev` scanner that is refused in
   production). Downloads go through the API too: a caller first obtains an **access ticket**
   (`POST …/access`; HMAC over document, user, session and expiry; 60-second lifetime; bound to the session)
   and then fetches `…/content?ticket=…`, which re-authorises and writes a `kyc.document.accessed` security
   audit event. No presigned bucket URL ever reaches a browser. Responses are `attachment`,
   `X-Content-Type-Options: nosniff`, `Content-Security-Policy: sandbox`, `Cache-Control: no-store`.
6. **Keys.** KYC field encryption and blind indexes use keys separate from the Stage 4 TOTP key
   (`KYC_FIELD_ENCRYPTION_LOCAL_KEY`, `KYC_BLIND_INDEX_KEY`); compliance notes use
   `COMPLIANCE_FIELD_ENCRYPTION_LOCAL_KEY`. Local keys only; KMS is Stage 18 (production refuses `local`).

## Consequences

- Positive: a bug or injection in non-KYC code cannot read identity data through its pool; audit and outbox
  remain append-only even for restricted roles.
- Negative: more pools and configuration; eventual consistency for projections; documents stream through the
  API (bandwidth/CPU). Acceptable at current scale.

## Alternatives considered

| Alternative | Why rejected |
|---|---|
| One application role for everything | Contradicts ADR-009 / Stage 2 C-3; widens the blast radius |
| Presigned GET URLs for reviewers | URLs leak through history, logs and screenshots; cannot be bound to a session |
| Browser direct upload to the bucket | Needs bucket CORS and exposes the storage origin; content checks happen too late |

## Related

ADR-008, ADR-009, ADR-019, ADR-022, ADR-034; design/sql/0018_grants.sql; docs/stage-5/document-security.md.
