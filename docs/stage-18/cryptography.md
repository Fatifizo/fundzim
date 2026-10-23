# Stage 18 — Cryptography

Only established, maintained libraries are used (Go standard library `crypto/*`, `golang.org/x/crypto/argon2`, the
pgx/PostgreSQL `sha256`, OpenPGP via GnuPG for backups). No custom algorithm exists.

| Use | Mechanism | Key / parameters | Location | Notes |
|---|---|---|---|---|
| Password hashing | Argon2id (PHC string) | 64 MiB, t=3, p=2, 16-byte salt, 32-byte key | `internal/auth/passwords` | rehash when parameters change |
| Field encryption (C3/C4: TOTP secrets, payout account numbers, KYC values, compliance notes) | AES-256-GCM, 96-bit random nonce (crypto/rand), AAD binds the record and field | one key per purpose; previous keys accepted (Stage 18) | `internal/platform/crypto` | ciphertext = nonce‖ct; key id stored alongside |
| Blind indexes, OTP and recovery-code hashes | HMAC-SHA-256 with domain separation (`domain ‖ 0x00 ‖ parts`) | app and KYC keys | `crypto.Keyed` | not rotatable without re-index |
| Tokens (session, reset, verification, MFA challenge) | 32 random bytes, base64url; SHA-256 stored | — | `crypto.NewToken` | single use where applicable |
| CSRF token | HMAC-SHA-256(CSRF secret, session id), constant-time compare | ≥ 32 characters | `auth/middleware.go` | |
| TOTP | RFC 6238 HMAC-SHA-1, 6 digits, 30 s | per-method secret (sealed) | `auth/mfa.go` | step replay protection |
| Document tickets | HMAC, 60 s | ticket key | `storage` | |
| Object encryption at rest | S3 SSE-C AES-256 with a per-object key HMAC-SHA-256(master, "fundzim/sse-c/v1/" ‖ id) | master by key id; previous masters (Stage 18) | `internal/storage` | the S3 protocol's key MD5 is a checksum, not security (`#nosec` reviewed) |
| Audit hash chain | SHA-256 over the canonical jsonb rendering of each row incl. `prev_hash` | — | `audit.chain_append` | verification in ranges (Stage 18) |
| Audit checkpoints | HMAC-SHA-256 (domain `audit.chain_checkpoint/v1`) over chain, seq, hash | worker-only `AUDIT_CHECKPOINT_KEY` (fingerprint id) | `internal/audit/chain.go` | key the database does not hold |
| Evidence and statement integrity | SHA-256 of files and canonical JSON | — | storage, recon, risk evidence | |
| Webhook authentication | HMAC-SHA-256 over timestamp.body, ±300 s, constant-time | CURRENT/PREVIOUS secrets | `internal/psp/sandbox` | |
| Backups | OpenPGP symmetric AES-256 (GnuPG, integrity-protected), S2K SHA-512 × 65 011 712 | key file (64 hex) outside the backup | `scripts/backup` | manifest MAC = HMAC-SHA-256 under HMAC(key, "fundzim/backup-manifest/v1") |
| Transport | TLS 1.2+ for the secret manager and SMTP clients; system roots or a private CA; no `InsecureSkipVerify` anywhere | — | `secretstore`, `notifications/smtp.go` | production refuses non-TLS URLs |
| Random numbers | `crypto/rand` for every secret, token, nonce and key; `math/rand` only for retry jitter (`#nosec` G404 reviewed) | — | — | |

## Key separation

Every purpose has its own key and the configuration refuses any key used twice (including previous keys and the audit
checkpoint key): `keyProblems`, `checkpointKeyProblem`, tested.

## Key storage, rotation and destruction

Storage and rotation: secrets-management.md. Destruction: a retired key is removed from the secret manager (KV v2
`destroy` of the version) after its data has been re-encrypted; a backup encrypted under a retired backup key stays
readable only while that key is kept in custody (retention of backups is LEGAL_REVIEW_REQUIRED).

## Not implemented

KMS/HSM envelope encryption (KI-S18-02), artifact signing (KI-S18-08), WebAuthn (F-S18-12), external anchoring of
audit checkpoints (KI-S18-05).
