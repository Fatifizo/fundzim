# Stage 4 → Stage 5 Handover: KYC & Verification

> Stage 4 (authentication, identity, RBAC, account security and the Stage 3 remediation gate) is implemented
> on branch `stage-4/identity-access` and **awaits acceptance**. Do not start Stage 5 until the owner accepts
> Stage 4 and asks for Stage 5. This document tells Stage 5 what it can build on and what it must not assume.

Stage 4 docs: [implementation](../stage-4/implementation.md) · [authentication](../stage-4/authentication-architecture.md)
· [sessions](../stage-4/session-management.md) · [RBAC](../stage-4/rbac.md) · [MFA](../stage-4/mfa.md) ·
[security review](../stage-4/security-review.md) · [testing](../stage-4/testing.md) ·
[known issues](../stage-4/known-issues.md). ADRs: 032, 033.

## 1. Stage 5 objective (ROADMAP)

Individual (KYC) and organisation (KYB) verification with segregated, protected storage: verification levels,
`kyc` schema and role, field encryption with blind index, `private-kyc` bucket, upload quarantine and malware
scanning, a vendor adapter (vendor chosen in Stage 5) with a fake, staff review workflows with audited
document access, PVO fundraising authority records.

## 2. What Stage 5 can use

| Need | Use | Notes |
|---|---|---|
| Who is calling | `authz.PrincipalFrom(ctx)` → `UserID`, `Kind` (USER/STAFF), `Permissions`, `StepUpAt`, `MFAVerifiedAt`, `EmailVerified` | Set by `auth.SessionMiddleware`; never trust client claims |
| Protect a route | `r.HandleFunc(pattern, httpx.Permission("kyc.case.review"), h)`; `httpx.User()` for applicant routes | Deny by default; staff routes 404 for non-staff; step-up per `permissions.requires_step_up` (already true for `kyc.document.view`, `kyc.identity_number.reveal`, `kyc.decision.record`, `org.verification.decide`, `beneficiary.verification.decide`) |
| Fresh factor inside a handler | `auth.Service.requireStepUp(p)` pattern (`STEP_UP_REQUIRED`), client calls `/auth/step-up/verify` | Staff step-up is TOTP-only |
| Account data | `internal/users` (`ByID`, `ByLoginEmail`, `DisplayNames`, `PrimaryPhone`, `MaskPhone`) | Never query `app.users*` from another module (archtest enforces) |
| Organisation membership | `internal/organisations` (membership + `org.*` permissions; ORG_ADMIN) | Add a Go interface for "is caller ORG_ADMIN of X" when KYB needs it rather than reading the tables |
| Field encryption | `crypto.NewAEAD(keyID, hexKey)` (`Seal/Open` with AAD = row identity), `crypto.NewKeyed(...).Sum(domain, parts...)` for blind indexes | Local keys only; **KMS is Stage 18** and production refuses to start without it. Use a **separate** key for C3 KYC data (new config keys), not the TOTP field key |
| Audit | `audit.Record(ctx, tx, audit.Event{Stream: audit.Security, …})` in the same transaction | Metadata must not contain C3 values (rejects secret-like keys; still review KYC fields by hand) |
| Async work | `outbox.Write(ctx, tx, …)` + a consumer registered in `internal/app/consumers.go` (worker) | Handlers run outside transactions, at least once — make them idempotent (`outbox.Consume` for DB effects) |
| Idempotency | `httpx.With(idempotency.Middleware(store, idempotency.Required, "kyc.submit", logger))` | Responses that set cookies/Location must not rely on replay |
| Rate limits | add named policies to `ratelimit.DefaultPolicies()` and call through the `auth.Throttler`-style adapter or `ratelimit.Middleware` | Protective fallback is automatic |
| Email | outbox consumer + `notifications.EmailSender` (SMTP → Mailpit) | Never put tokens or C3 data in payloads |
| Storage | `internal/platform/storage` with per-bucket credentials (`private-kyc` credential exists) | Upload sessions/quarantine/ClamAV are Stage 5 work |
| Tests | `tests/integration/identity_test.go` helpers (`newITServer`, `browser`, `registerVerified`, `staffLogin`, `acceptStaffInvitation`, `resetSuperAdmins`, Mailpit `mailTo`) | Staff with a given role: bootstrap admins, invite, maker-checker grant (see `TestIdentityRBACStaffMakerChecker`) |

## 3. Constraints to respect

1. KYC is **C3**: `kyc` schema, `fundzim_kyc` DB role, `private-kyc` bucket, only the `kyc` module touches it,
   every access audited (CLAUDE.md). Add the `kyc` module to `internal/archtest` (`allowedImports`,
   `ownedTables`) in the same change.
2. Admin roles do **not** get KYC document access implicitly (SUPER_ADMIN has no `kyc.*` permission).
3. Verification decisions that are sensitive need maker-checker (operational-controls); reuse the
   role-request pattern (DB CHECKs for no self-approval).
4. A verified phone/email is **not** identity verification.
5. Do not implement payments, ledger postings or payouts.

## 4. Open items Stage 5 should know about

| Item | Where |
|---|---|
| CI not running on GitHub (owner must enable Actions); `-race` never ran on Stage 4 code | KI-S4-01 |
| Stage 1–4 acceptance not recorded | prerequisite-assessment |
| `Organisation.org_type` enum mismatch in the Stage 2 OpenAPI schemas | KI-S4-11 — reconcile with KYB |
| OpenAPI-generated web client types still absent | Stage 3 KI-14 |
| Organisation UI (invitations list, member management) not built | KI-S4-08 |
| Login throttle can delay a known user's sign-in (F-02); 429 without `Retry-After` from handler throttles (F-07) | security review |
| Staff reactivation and break-glass APIs missing | KI-S4-06 |
| Integration tests share the dev database | KI-S4-09 |

## 5. Local environment

`docker compose up -d --build` starts PostgreSQL, Valkey, Garage, Mailpit, migrate, API, **worker** and web
(fixed subnet `172.28.0.0/24`, web at `172.28.0.10`, the only trusted proxy). First admins:
`go run ./apps/api/cmd/fundzimctl bootstrap-admins --admin-a-email … --admin-a-name … --admin-b-email …
--admin-b-name … --justification "…"`, then accept both invitation emails in Mailpit (http://127.0.0.1:8025).
