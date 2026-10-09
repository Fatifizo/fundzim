# Stage 6 — Campaign media (work stream M)

> Code: `internal/campaigns/media` (module `campaigns`), wiring `internal/app/campaign_media.go`.
> Schema: `migrations/20261009171100_campaign_media.sql` (`app.campaign_media`, `app.campaign_media_events`).
> Builds on the Stage 5 storage pipeline ([document-security](../stage-5/document-security.md), ADR-035) and the
> campaigns core access API (interface-contracts §4). The development scanner is not malware protection.

## 1. Lifecycle (machine `campaign_media`)

```
'' → UPLOADED → QUARANTINED → SCANNING → APPROVED
UPLOADED | QUARANTINED | SCANNING → REJECTED      any non-removed state → REMOVED (soft; the row stays)
```

| Step | What happens |
|---|---|
| Upload (API) | Multipart, fields first: `kind` (`COVER`/`GALLERY`), `position?`, `alt_text` (plain text, 3–250, HTML refused), `depicts_minor` (`true` → 422 `MINOR_MEDIA_NOT_SUPPORTED`, LR-070), then `file`. Owner write access (`OwnerAccess`), campaign `DRAFT`/`CHANGES_REQUESTED` (or `ACTIVE`/`PAUSED`: `RecordMaterialChange(…, "MEDIA", …)` opens a re-review), no `SUSPENDED`/`OFFBOARDED` restriction (403 `ACCOUNT_RESTRICTED`; a restriction lookup error refuses), one non-removed cover (409 `COVER_ALREADY_EXISTS`), gallery limit from the campaign policy (422 `GALLERY_LIMIT_REACHED`) — all **before** the file is read. The bytes go through `storage.Upload` into the PUBLIC_MEDIA quarantine (`UPLOAD_MAX_BYTES`, magic-byte sniffing, declared type = sniffed type). The filename is never read or stored. The media row is written `UPLOADED → QUARANTINED` with its timeline, audit `campaign.media_added` and outbox `campaigns.media_added` in one transaction |
| Scan (worker) | The storage pipeline scans the original (ClamAV). Consumer `campaigns.media_object_scanned` (on `storage.object_scanned`, owner module `campaigns`, purpose `CAMPAIGN_MEDIA`) and the 30 s sweep job `campaigns.media_sweep` call `Advance`. A scan in progress or failing (scanner outage → `FAILED_SCAN`, retried by the storage rescan job) shows as `SCANNING`; a `REJECTED` original rejects the media (`MALWARE_DETECTED` / `UPLOAD_REJECTED`) |
| Process (worker) | Under the media row lock: open the CLEAN original (SHA-256 re-verified), `Process` it (§2), upload the derivative as a **new** PUBLIC_MEDIA object (quarantine + scan again), link it (`processed_object_id`, dimensions) — `SCANNING`. Content failures reject with a reason code; storage failures are retried |
| Approve (worker) | When the derivative is CLEAN the media becomes `APPROVED` (audit `campaign.media_approved`). Nothing is approved without a CLEAN verdict on both objects |
| Remove | Owner `DELETE` (editable or live campaign; live + approved media is a material change): `REMOVED` (`OWNER_REMOVED`), both objects soft-deleted. Staff `POST …/remove` (`content.moderate`, `{reason_code, note}`): `REMOVED` with the reason; objects kept as moderation evidence. Audit `campaign.media_removed`, outbox `campaigns.media_removed` |

Database guards: `app.guard_transition('campaign_media')`; one timeline row per version (deferred check); both object
references are composite foreign keys to `stored_objects (id, bucket_class)` pinned to `PUBLIC_MEDIA`, and a trigger
requires purpose `CAMPAIGN_MEDIA` / owner `campaigns` — **a KYC or evidence object can never become campaign media**;
at most one non-removed cover (unique partial index); no deletes; immutable/set-once columns.

## 2. Image processing (`Process`)

Go standard library only (`image/jpeg`, `image/png`). In order:

1. Sniff must equal the declared type (JPEG/PNG only) → `IMAGE_UNSUPPORTED` / `IMAGE_TYPE_MISMATCH`.
2. Active-content markers anywhere in the file (`<script`, `<html`, `<!doctype html`, `<?php`, `<iframe`, `javascript:`,
   `<body`) → `IMAGE_ACTIVE_CONTENT`; a PNG must end at `IEND` → `IMAGE_TRAILING_DATA` (polyglots).
3. `image.DecodeConfig` (header only) — decoder format must match; **more than 8000 px per side or 40 megapixels is
   refused before any pixel is decoded** (`IMAGE_TOO_LARGE`, decompression bombs). Limits are `media.Limits`.
4. Decode; the EXIF orientation of a JPEG is applied to the pixels.
5. Re-encode: JPEG (quality 90, lower if over the size cap) writes no APPn/COM segment; PNG writes only IHDR, (PLTE/tRNS),
   IDAT, IEND. EXIF, GPS, XMP, ICC, comments, text/time chunks and trailers are gone. One decode at a time per process.

Finding (integration-tested): ClamAV 1.5.3 does **not** flag the EICAR string hidden inside a valid PNG/JPEG (text
chunk, comment, trailer, appended ZIP). For images the re-encode is what guarantees that no embedded payload is ever
served; the scanner still guards the stored originals.

## 3. Serving

Only the derivative of `APPROVED` media is ever served; the original never is.

| Route | Who | Headers |
|---|---|---|
| `GET /campaigns/{id}/media/{media_id}/content` | owner / organisation member; staff with `campaign.view` | `private, no-store` |
| `GET /public/campaigns/{slug}/media/{media_id}` | anyone, while `PublicBySlug` succeeds **and** the id is in `campaign_versions.media_ids` of `campaigns.approved_version_id` | `public, max-age=300`, `ETag` (content SHA-256; `If-None-Match` → 304) |

Both: exact `Content-Type`, `X-Content-Type-Options: nosniff`, `Content-Security-Policy: default-src 'none'; sandbox`,
`Content-Disposition: inline; filename="image.<ext>"`. Anything else is 404. Other routes: `GET /campaigns/{id}/media`
(owner list, all statuses), `PATCH /campaigns/{id}/media/{media_id}` (`position`, `alt_text`; `If-Match` required),
`DELETE …`, `GET /admin/campaigns/{id}/media/all` (`campaign.view`; the extra segment was added when the review detail still
lived at `GET /admin/campaigns/review/{campaign_id}` — it is now `GET /admin/campaigns/{campaign_id}/review` — and is kept
for contract stability), `POST /admin/campaigns/{id}/media/{media_id}/remove` (`content.moderate`).

`MediaInfo` for the core: `Readiness` (cover approved; pending = UPLOADED/QUARANTINED/SCANNING; rejected) and
`ApprovedIDs` (cover first, then gallery by position).

## 4. Operations

The worker now holds the public-media credential (compose: `STORAGE_PUBLIC_*` on `fundzim-worker`; config requires the
trio all-or-nothing) and its memory limit is 512 MiB for decoding. Retention of removed media is LEGAL_REVIEW_REQUIRED
(LR-012); images of minors are refused until LR-070 is answered.
