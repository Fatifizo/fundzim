# Architecture Decision Records (ADRs)

An ADR records one significant architectural decision: the context that forced it, what was decided, what it
costs, and what was rejected. ADRs are how FundZim avoids "silent" architecture changes — if the reasoning is
not written down here, the decision has not been made.

## When an ADR is required

Write an ADR (before or alongside the change, never after the fact) when a change:

- adds, removes or replaces a core technology (language, framework, database, queue, storage, identity provider);
- changes module boundaries, data ownership, or which module may write a table;
- touches how money is represented, stored, calculated, rounded, converted or posted to the ledger;
- changes payment, webhook, idempotency, payout or reconciliation behaviour;
- changes security boundaries: authentication, session model, authorisation model, encryption, key management,
  KYC data handling, secret management;
- changes data classification, retention or cross-border data flows;
- introduces new infrastructure (e.g. a message broker, a second database, Kubernetes) or a new deployable unit;
- reverses or materially amends an existing ADR.

Bug fixes, refactors inside one module, and routine dependency upgrades do not need an ADR.

## Process

1. Copy `ADR-000-template.md` to `ADR-NNN-short-kebab-title.md` using the next unused number. Numbers are never reused.
2. Set **Status: Proposed** and open it for review with the change that depends on it.
3. Financial, security or KYC-affecting ADRs need review from the technical lead **and** a second engineer;
   anything with regulatory impact must also list the relevant `LEGAL_REVIEW_REQUIRED` items (`LR-xxx`) from
   the register in [`../compliance/open-legal-questions.md`](../compliance/open-legal-questions.md).
4. On approval set **Status: Accepted** and update the index below.
5. Accepted ADRs are not rewritten. To change a decision, write a new ADR that supersedes it, then set the old
   one to **Superseded by ADR-NNN** (the only edit allowed to an accepted ADR, plus typo fixes).

## Status lifecycle

| Status | Meaning |
|---|---|
| Proposed | Under discussion; must not be relied on yet. |
| Accepted | In force. Code and documentation must conform. |
| Superseded | Replaced by a later ADR (linked). Kept for history. |
| Deprecated | No longer relevant (e.g. the subsystem was removed) and not replaced. |

## Index

| ADR | Title | Status |
|---|---|---|
| [ADR-000](ADR-000-template.md) | Template | — |
| [ADR-001](ADR-001-modular-monolith.md) | Modular monolith architecture | Accepted |
| [ADR-002](ADR-002-go-backend.md) | Go backend | Accepted |
| [ADR-003](ADR-003-nextjs-frontend.md) | Next.js frontend (presentation layer only) | Accepted |
| [ADR-004](ADR-004-postgresql.md) | PostgreSQL as the authoritative database | Accepted |
| [ADR-005](ADR-005-exact-money-representation.md) | Integer minor-unit money representation | Accepted |
| [ADR-006](ADR-006-double-entry-ledger.md) | Double-entry ledger requirement | Accepted (amended by ADR-014) |
| [ADR-007](ADR-007-payment-provider-abstraction.md) | Payment provider abstraction | Accepted (amended by ADR-020) |
| [ADR-008](ADR-008-s3-object-storage.md) | S3-compatible object storage | Accepted |
| [ADR-009](ADR-009-kyc-storage-separation.md) | Sensitive KYC storage separation | Accepted |
| [ADR-010](ADR-010-multi-currency.md) | Multi-currency architecture | Accepted (amended by ADR-018) |
| [ADR-011](ADR-011-utc-time.md) | UTC internal time | Accepted |
| [ADR-012](ADR-012-container-first.md) | Container-first development | Accepted |
| [ADR-013](ADR-013-regulatory-operating-model.md) | Regulatory operating model: PSP-mediated (Model A) for the MVP | Accepted (provisional — subject to LR-001/LR-004 and provider confirmation) |
| [ADR-014](ADR-014-accounting-separated-from-custody.md) | Separation of financial accounting from custody | Accepted |
| [ADR-015](ADR-015-risk-based-identity-verification.md) | Risk-based identity verification | Accepted |
| [ADR-016](ADR-016-beneficiary-verification-before-payout.md) | Beneficiary verification before payout | Accepted |
| [ADR-017](ADR-017-payout-approval-segregation-of-duties.md) | Payout approval and segregation of duties | Accepted |
| [ADR-018](ADR-018-currency-isolation-and-fx.md) | Currency isolation and FX policy | Accepted |
| [ADR-019](ADR-019-regulatory-evidence-management.md) | Regulatory evidence management | Accepted |
| [ADR-020](ADR-020-payment-payout-state-model-revision.md) | Payment and payout state model revision | Accepted |
| [ADR-021](ADR-021-module-boundaries-and-ownership.md) | Domain module boundaries, dependency graph and table ownership | Accepted (Stage 2; pending owner acceptance of Stage 2) |
| [ADR-022](ADR-022-database-schema-organisation.md) | Database schema organisation and roles | Accepted (Stage 2) |
| [ADR-023](ADR-023-ledger-posting-architecture.md) | Ledger posting architecture | Accepted (Stage 2) |
| [ADR-024](ADR-024-payment-intent-and-db-guarded-state-machines.md) | Payment intent vs provider transaction; database-guarded state machines | Accepted (Stage 2) |
| [ADR-025](ADR-025-outbox-inbox-job-queue.md) | Transactional outbox, inbox and PostgreSQL job queue (River) | Accepted (Stage 2) |
| [ADR-026](ADR-026-api-versioning-contract-first.md) | API versioning and contract-first OpenAPI | Accepted (Stage 2) |
| [ADR-027](ADR-027-authentication-session-strategy.md) | Authentication and session strategy | Accepted (Stage 2) |
| [ADR-028](ADR-028-migration-strategy.md) | Database migration strategy (goose, forward-only) | Accepted (Stage 2) |
| [ADR-029](ADR-029-provider-capability-abstraction.md) | Provider capability abstraction (`psp` module) | Accepted (Stage 2) |
| [ADR-030](ADR-030-reconciliation-architecture.md) | Financial reconciliation architecture | Accepted (Stage 2) |
| [ADR-031](ADR-031-data-access-and-http-stack.md) | Data access and HTTP stack (pgx + sqlc, stdlib router) | Accepted (Stage 2) |
| [ADR-032](ADR-032-email-password-totp-authentication.md) | Email + password primary login with TOTP MFA (amends ADR-027) | Accepted (Stage 4) |
| [ADR-033](ADR-033-per-request-nonce-csp.md) | Per-request nonce CSP; Next.js `cacheComponents` disabled | Accepted (Stage 4) |
| [ADR-034](ADR-034-verification-state-models.md) | Verification state models and vocabularies (brief statuses canonical; amendments 2026-10-09) | Accepted (Stage 5) |
| [ADR-035](ADR-035-restricted-data-access-and-documents.md) | Restricted data access: kyc/compliance roles, gateways, document storage and tickets | Accepted (Stage 5) |
| [ADR-036](ADR-036-campaign-lifecycle-and-publication.md) | Campaign lifecycle (Stage 2 names + CHANGES_REQUESTED, PAUSED, ARCHIVED; FROZEN reserved), approved versions, controlled publication, restriction-driven suspension | Accepted (Stage 6) |
| [ADR-037](ADR-037-basic-verification-restrictions-and-staff-links.md) | Age attestation and BASIC_VERIFIED, compliance restriction projection and enforcement, staff ↔ personal account links | Accepted (Stage 6) |
| [ADR-038](ADR-038-public-experience-caching-reporting-and-api-consistency.md) | Public experience: caching (no-cache + ETag, errors no-store), transparent featured selection, reports that never change status, first-party aggregate analytics, live trust indicators, canonical URLs, campaign-family API consistency (428/412, `{items, next_cursor}`, `{campaign_id}`) | Accepted (Stage 7) |
| [ADR-039](ADR-039-payment-abstraction-donation-intents-and-sandbox-execution.md) | Payment abstraction: donation intents as parent of payments, Stage 2 state names, sandbox-only execution gates, deferred fees/ledger/refund execution/disputes, rejected-webhook log, stricter donation eligibility, limits | Proposed (Stage 8) |
| [ADR-040](ADR-040-payment-provider-selection-deferred.md) | Payment provider selection: DECISION_DEFERRED (no provider passes the selection gates; no adapter without a superseding ADR) | Proposed (Stage 9) |
| [ADR-041](ADR-041-secret-management.md) | Secret management: Vault KV v2 API, OpenBao by default; AppRole and HTTPS in production; environment-scoped references; rotation audit | Proposed (Stage 9) |
| [ADR-042](ADR-042-ledger-implementation.md) | Ledger implementation: environment-designated ledger databases (TEST only in Stage 10), dedicated `fundzim_ledger` posting role, asynchronous payment boundary (amends ADR-023 §7), versioned rules, account status, evidence classes, reconciliation references | Proposed (Stage 10) |
| [ADR-043](ADR-043-withdrawals-and-payouts.md) | Withdrawals and payouts: TEST payouts only (simulator; PRODUCTION refused), slot-first then ledger reservation, journals derived from state, 22-check eligibility with acknowledged reviews, FINANCE maker-checker with linked-identity SoD, provider-neutral payout interface, destination step-up | Proposed (Stage 11) |
| [ADR-044](ADR-044-fees-and-platform-revenue.md) | Fees and platform revenue: TEST fees only, immutable-scope policies with maker-checker versions, deterministic selection, pure exact calculation, distinct fee bases, quotes bound to intents, assessments, derived TEST fee journals (`fees` may import `ledger`, amending the Stage 2 graph), payout fees never deducted twice | Proposed (Stage 12) |
| [ADR-045](ADR-045-risk-engine-cases-holds-screening.md) | Risk engine: configurable, maker-checker rule versions (declarative JSON, explainable 0–100 scores) on the Stage 5 signal store; recommendation ≠ enforcement ≠ human outcome (automation limited to cases, monitoring and ≤ 7-day payout-protective holds); risk cases with immutable hashed evidence; central hold registry enforced at publication, donations and payouts; provider-neutral TEST screening foundation (amends screening table ownership) | Proposed (Stage 13) |
| [ADR-046](ADR-046-operations-portal.md) | Operations portal: `internal/operations` aggregates per-module `OpsSnapshot`s (own tables, documented sources, partial failure as UNAVAILABLE) and owns only its operational records (support cases, incidents, exception triage, access reviews, internal SLA targets); approvals and exception resolution stay with the owning module; staff directory and session revocation in `auth`, audit search/export in `audit`; effective permissions on `/me` for a permission-aware UI; risk rule fault isolation | Proposed (Stage 14) |
| [ADR-047](ADR-047-notification-engine.md) | Notification engine in `internal/notifications`: outbox-fed exactly-once creation, at-least-once recorded delivery with retries, UNKNOWN reconciliation and dead letters; consent, preference, contact-verification and suppression routing; versioned maker-checker templates; TEST_ONLY simulators and dev senders only (`LiveMessagingEnabled = false`); credential messages stay in `auth`; TEST payments/payouts never produce external messages | Proposed (Stage 15) |
| [ADR-048](ADR-048-discovery-sharing-and-referrals.md) | Discovery: new `discovery` module with a search projection whose results are always re-checked by campaigns at serve time (restriction and hold registries; fail closed); outbox indexing ordered by snapshot time plus reconciliation; PostgreSQL full-text search with trigram typo tolerance; maker-checker featured placements (replace ADR-038 §2 staff picks); published trending formula with maker-checker ranking policies; rule-based recommendations; aggregate engagement analytics with daily keyed de-duplication; referral codes blocked in production pending LR-023; coarse reviewed campaign location | Proposed (Stage 16) |
| [ADR-049](ADR-049-reconciliation-and-settlement-operations.md) | Reconciliation: `reconciliation` module owning the `recon` schema; canonical TEST statement imports (hashed, deduplicated, private evidence, scanned, all or nothing); source authority; deterministic versioned matcher (references find, amounts verify; maker-checker rule sets, none seeded); exception lifecycle; maker-checker resolutions (statement outcomes applied by the owning modules with source RECON; ledger adjustments through the ledger); no settlement postings in Stage 17; Model A bank checks; payout RATE_LIMITED retries budgeted | Proposed (Stage 17) |
| [ADR-050](ADR-050-security-hardening-and-recovery.md) | Security hardening and recovery: application keys from the secret manager (`secretstore`) with rotation overlap; parallel range and MAC-checkpointed incremental audit verification (never weakened); repeated identical status polls not re-audited; persisted, deduplicated security alerts with a guarded lifecycle; encrypted, MAC-manifested, restore-verified backups recorded as evidence; hardened, read-only, segmented, digest-pinned containers; SAST, SBOM, image-scan and provenance gates in CI | Proposed (Stage 18) |
| [ADR-051](ADR-051-financial-concurrency-and-bounded-polling.md) | Financial concurrency: payout reconciliation re-reads and settles a pending ledger sync before judging; history events only from the writer that changed the row; ledger postings lock every SYNC balance row in ascending id (no opposite-order deadlocks); payout attempts lock the payout row first and status queries of one payout never run concurrently; bounded, jittered, per-environment payment polling with POLL_BUDGET_EXHAUSTED; provider health derivation indexed; deterministic interleaving tests | Proposed (Stage 19) |
| [ADR-052](ADR-052-reconciliation-coverage-sweeps.md) | Reconciliation coverage (KI-S19-09): durable keyset sweeps over every payout, fee assessment, payment capture journal and succeeded/unknown payment, checkpoint advanced by compare-and-swap only after a batch is examined, deferrals carried instead of skipped, DB-guarded checkpoint tables per module, recency windows kept (newest first) | Proposed (remediation phase 1) |
