// Package storage is FundZim's document storage and security pipeline (module `storage`, Stage 5, interface
// contract §3, ADR-034 §3, ADR-035 §5). It owns app.stored_objects and app.upload_sessions and imports only
// internal/platform/* and internal/audit.
//
// Pipeline:
//
//	Upload (API)  : row UPLOADED + upload session PENDING → body read with a hard size cap, SHA-256 and
//	                magic-byte sniffing (allow-list JPEG, PNG, PDF; declared type must equal the sniffed type)
//	                → bytes written to <class prefix>quarantine/<id> → QUARANTINED + outbox
//	                storage.object_uploaded + audit storage.object.uploaded (one transaction).
//	                Validation failures → REJECTED with a reject_reason code; nothing is written to the bucket.
//	Scan (worker) : storage.object_uploaded → claim QUARANTINED|FAILED_SCAN → SCANNING (atomic, fenced by
//	                scan_attempts) → Scanner → CLEAN (server-side copy to objects/<id>, then the quarantine copy
//	                is deleted) | REJECTED (MALWARE_DETECTED) | FAILED_SCAN (scanner/storage error; retried by
//	                the periodic rescan job with backoff). A scanner error never produces CLEAN.
//	Open          : only CLEAN objects, read from objects/<id>, SHA-256 re-verified while streaming.
//	Cleanup       : PENDING upload sessions past UPLOAD_TTL → EXPIRED; the object UPLOADED → DELETED
//	                (UPLOAD_EXPIRED) and any partial bytes are deleted (purged_at).
//
// Client filenames are never stored or used. Keys are server-generated from the object ID.
//
// Encryption at rest (honest status): objects in the private classes (PRIVATE_KYC, PRIVATE_EVIDENCE) are
// written with S3 SSE-C — the object store (Garage v2 locally) encrypts them with AES-256 using a per-object
// key that this package derives as HMAC-SHA-256(STORAGE_SSE_C_KEY, "fundzim/sse-c/v1/" + object_id). The
// row's encryption_key_id names the application key ("sse-c/hmac-sha256/local/<fingerprint>"). Limits:
// the application key is a LOCAL key from the environment (no KMS until Stage 18); object metadata (keys,
// sizes, content types) is not encrypted by SSE-C; the key travels in request headers, so the storage
// endpoint must be TLS outside local development. When no SSE key is configured (development/test only)
// objects are stored UNENCRYPTED and encryption_key_id is recorded as "none/garage-at-rest-unencrypted-dev"
// so the database never claims encryption that does not exist. Key rotation is not implemented: an object
// whose encryption_key_id differs from the configured key cannot be opened (ErrStorageUnavailable).
//
// Malware scanning: the clamd scanner speaks the INSTREAM protocol to ClamAV. The dev scanner only detects
// the EICAR test string, reports engine "dev-scanner (NOT malware protection)" and is refused when
// APP_ENV=production (and by config outside development/test).
package storage
