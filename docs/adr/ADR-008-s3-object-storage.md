# ADR-008: S3-compatible object storage

- **Status:** Accepted
- **Date:** 2026-10-08
- **Stage:** 0 (design), implemented from Stage 3 (local MinIO) and Stages 5–7 (uploads)

## Context

Campaigns need images, and later video or updates. KYC and compliance need identity and registration documents.
Files must not live in PostgreSQL rows or on application server disks, because containers are ephemeral. The
hosting provider is not yet chosen, so the API must be portable.

## Decision

We will use **S3-compatible object storage** behind the `internal/storage` module. MinIO is used locally from
Stage 3. The production provider is decided later; any S3-compatible service is acceptable.

- There are **two separate buckets with separate credentials and encryption keys**:
  - `public-media`: campaign media. It is served via a CDN only **after** processing, which means re-encoding
    images, stripping EXIF/GPS metadata and generating responsive sizes for low bandwidth.
  - `private-kyc`: KYC **and** compliance documents/reports, each under its own prefix with its own access
    policy. See ADR-009 for its controls.
  - These are logical names; physical bucket names are environment-prefixed (e.g. `fundzim-public-media`).
- **Upload pipeline:**
  1. The client uploads via a short-lived presigned PUT to a **quarantine** prefix, with size and content-type
     constraints.
  2. A malware scan runs (e.g. ClamAV).
  3. Magic-byte content-type verification and image re-encoding follow.
  4. The object is promoted to its final location. Rejected objects are deleted, and the rejection is audited.
- The DB stores object keys and metadata (hash, size, type, scan result), never file bytes.
- Object keys are random (UUIDv7-based), never user-supplied filenames.

## Consequences

### Positive
- Portable across providers, with cheap, durable storage and CDN-friendly public media.
- Clear separation of public and sensitive files from day one.

### Negative / costs
- A scanning and processing pipeline (worker jobs) must be built and operated.
- Eventual consistency of processing: an upload is not immediately visible.

## Alternatives considered

| Alternative | Why rejected |
|---|---|
| Store files in PostgreSQL (bytea) | Bloats backups and the DB, and is a poor fit for a CDN. |
| Local disk / container volume | Not durable or scalable, and breaks with multiple instances. |
| Single bucket with prefixes | One credential or misconfiguration could expose KYC documents publicly. Rejected in favour of ADR-009. |
| Provider-specific SDK features (non-S3) | Lock-in. |

## Security implications
- Stored XSS via SVG/HTML uploads is prevented. Only raster image types are allowed for media, files are
  re-encoded, and they are served with a correct `Content-Type` and `X-Content-Type-Options: nosniff` from a
  separate media domain.
- Malware and metadata (GPS location) leakage are mitigated.
- Presigned URLs are short-lived and scoped to one key.
- Bucket policies deny public listing.

## Financial implications
None directly. Storage holds no monetary records, though campaign evidence documents support trust and
dispute handling.

## Related
ADR-009, ADR-012, [SECURITY.md](../SECURITY.md), [DATA-CLASSIFICATION.md](../DATA-CLASSIFICATION.md).
