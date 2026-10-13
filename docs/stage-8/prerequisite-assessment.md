# Stage 8 prerequisite assessment

Date: 2026-10-12. Branch `stage-8/payments`, created from `stage-7/public-experience` at `e355968` (CI run
38220793062: all 8 jobs green). Inputs read: `CLAUDE.md`, `README.md`, `docs/PRODUCT.md`, `docs/ARCHITECTURE.md`,
`docs/SECURITY.md`, `docs/ROADMAP.md`, `docs/TESTING.md` §3.9, `docs/PAYMENTS.md`, the Stage 1 payment and compliance
documents, the Stage 2 payment design (`docs/architecture/payment-provider-interfaces.md`,
`docs/architecture/payment-state-machine.md`, `docs/database/payment-schema.md`, drafts `design/sql/0007_fees_psp.sql`
and `0015_payments.sql`, `docs/stage-2/design-baseline.md` §2–§8), ADR-007/013/018/020/024/025/029, the Stage 3–7
handovers and `docs/stage-handover/STAGE-7-TO-STAGE-8.md`, and the code listed below.

Classification: **SATISFIED**, **SATISFIED_WITH_CONDITIONS**, **BLOCKED**, **NOT_VERIFIED**. No acceptance record
was fabricated.

## 1. Governance

| Prerequisite | Status | Evidence / condition |
|---|---|---|
| Formal acceptance of Stages 1–7 | **NOT_VERIFIED** | No acceptance register existed. A proposed register was created ([../governance/stage-acceptance-register.md](../governance/stage-acceptance-register.md)) with every stage `NOT_RECORDED` and its outstanding conditions. Stage 8 proceeds as **isolated, non-production engineering work** (brief §3): financial execution disabled, production gate **BLOCKED** |
| Owner request to start Stage 8 | SATISFIED | The owner supplied the Stage 8 master prompt |
| Production payment execution | **BLOCKED** (by design) | No provider selected (no ADR), no legal sign-off (LR-001, LR-003, LR-004, LR-075), no stage acceptance. Enforced in code (`psp.LiveExecutionEnabled = false`) and data (no real provider can be `ENABLED` without `selection_status = SELECTED` + ADR, DB CHECK) |

## 2. Platform capabilities Stage 8 reuses (not duplicated)

| Capability | Status | Evidence |
|---|---|---|
| Exact money type | SATISFIED | `internal/platform/money` (int64 minor units + ISO code, string JSON), `app.currencies` with `enabled` and `minor_units_verified` (USD verified; ZWG `minor_units_verified = false`, LR-043 / PCR-018) |
| Currency definitions | SATISFIED_WITH_CONDITIONS | USD usable. ZWG stays sandbox/test-only: Stage 8 refuses ZWG for donation intents (`CURRENCY_NOT_AVAILABLE`) because the campaign goal currency is USD-only and `minor_units_verified` is false |
| Authentication middleware and RBAC | SATISFIED | `httpx.User()`, `httpx.Staff(perm)` policies, permission table, step-up (`auth` fresh MFA) used by Stage 5–7 staff actions |
| Campaign eligibility engine | SATISFIED_WITH_CONDITIONS | `campaigns.Evaluate` has per-action gates but no donation action. Stage 8 adds `ACCEPT_DONATION` (live status, visibility, approved version, owner/organisation/beneficiary restrictions) |
| Beneficiary verification | SATISFIED | `beneficiaries.StatusOf` / `StatusesOf`; campaign publication already requires a verified beneficiary |
| Compliance restrictions | SATISFIED | `compliance.Service.Restrictions` (fail closed); Stage 8 checks owner, organisation, beneficiary **and donor** |
| Audit | SATISFIED | `audit.Record` in the caller's transaction; payment audit records use it (no new audit table) |
| Transactional outbox, inbox, River jobs | SATISFIED | `platform/outbox` (Write in tx, relay, deliver with dedupe), `platform/jobs` (River in schema `queue`, worker client). Webhook processing and status polling use River jobs inserted in the same transaction (`InsertTx`) |
| Idempotency middleware | SATISFIED | `platform/idempotency` (scoped keys, request fingerprint, replay, lease, expiry); Stage 8 makes it **required** on payment mutations and adds DB unique backstops |
| PostgreSQL transaction patterns | SATISFIED | `db.WithTx`, `app.guard_transition` + `app.status_transitions`, `app.forbid_mutation`, `app.apply_runtime_grants()` |
| Redis-compatible cache | SATISFIED | Valkey limiter with protective fallback (`RetryAfter()`); no financial state in Valkey |
| Configuration and secrets | SATISFIED_WITH_CONDITIONS | `config.Secret` (redacting), env-held secrets, production refuses dev scanner/keys. **No secret manager / KMS exists**: provider secrets are `secretref://` references resolved from the environment by a `psp.SecretResolver`; a KMS-backed resolver is a Stage 9/19 item |
| OpenAPI contract | SATISFIED | `api/openapi/fundzim-v1.yaml`, redocly lint in CI |
| CI workflows | SATISFIED | 8 jobs incl. real-stack E2E, green on `e355968` |
| Security tests | SATISFIED | Allow-list, CSP, rate-limit, authz tests from Stages 4–7 |
| Ledger | **BLOCKED (out of scope, by design)** | No `ledger` module or tables (Stage 10). Stage 8 creates **no** ledger tables and **no** postings; the posting seam is an interface with a test-only recorder (TESTING.md §3.9 staging) |
| Fees | **BLOCKED (out of scope)** | No `fees` module (fee model LR-028 open). Stage 8 intents carry no platform fee; the fee columns of the Stage 2 draft are deferred |

## 3. Design inputs

| Input | Status | Note |
|---|---|---|
| Stage 2 payment design (state machine, schema drafts, provider interfaces, sandbox scenarios) | SATISFIED | Implemented with the deviations recorded in ADR-039 (donation intents as parent of payments; fees, ledger, disputes, chargebacks and refund execution deferred) |
| Payment state vocabulary | SATISFIED | Stage 2 states are authoritative (baseline §8). The brief's `VALIDATED`, `PROCESSING` and `RECONCILIATION_REQUIRED` map to Stage 2 terms (ADR-039 §3) |
| Provider capability evidence | **BLOCKED for real rails** | No provider selected; all rail capabilities `UNKNOWN` / `REQUIRES_PROVIDER_CONFIRMATION` ([provider-capability-register.md](provider-capability-register.md)) |
| Legal dependencies | **BLOCKED for production** | LR-001/003/004/005/018/019/020/026/043/044/075/081 and LR-012/022/035 open ([legal-and-provider-dependencies.md](legal-and-provider-dependencies.md)) |
| Screening | **BLOCKED for production** | `SCREENING_PROVIDER_NOT_SELECTED` (KI-S5-04); donor screening not performed |

## 4. Conclusion

Stage 8 can proceed as **sandbox-only engineering**. Real payment initiation, real refunds, ledger postings and
production use are blocked and must stay blocked until stage acceptance, legal sign-off, provider selection (ADR),
the ledger (Stage 10) and screening exist.
