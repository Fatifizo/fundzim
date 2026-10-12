# Stage 7 → Stage 8 Handover: Payment Abstraction Layer

> Stage 7 (public fundraising experience: discovery, campaign pages, sharing, SEO, accessibility work, PWA, reports,
> featured campaigns, first-party analytics, API consistency, real-stack E2E) is implemented on branch
> `stage-7/public-experience` and **awaits acceptance**. Do not start Stage 8 until the owner accepts Stage 7 and asks
> for Stage 8. Acceptance of Stages 1–6 is still unrecorded (KI-S7-01).

Stage 7 docs: [implementation](../stage-7/implementation.md) · [public experience](../stage-7/public-experience.md) ·
[discovery](../stage-7/campaign-discovery.md) · [sharing](../stage-7/social-sharing.md) · [SEO](../stage-7/seo.md) ·
[accessibility](../stage-7/accessibility.md) · [security](../stage-7/security-review.md) ·
[performance](../stage-7/performance.md) · [testing](../stage-7/testing.md) · [known issues](../stage-7/known-issues.md)
· [contracts](../stage-7/interface-contracts.md). ADR: 038.

Stage 8 objective (ROADMAP): the provider-neutral `payments` module with a **sandbox** provider, webhook inbox,
idempotency and the payment state machine. Stage 8 moves no real money and integrates no real provider (that is
Stage 9, after provider selection and an ADR). Nothing in Stage 8 may be described as approval to operate.

## 1. What exists today that Stage 8 builds on

| Area | State after Stage 7 |
|---|---|
| Modules | `campaigns` (no financial state), `payouts` (payout **destinations** only), `beneficiaries`, `kyc`, `compliance` (restriction projection, cases), `risk`, `audit`, `notifications`, `storage`. **No `payments`, `psp`, `fees` or `ledger` module or tables exist** |
| Design inputs (Stage 1–2, not implemented) | [payment-provider-interfaces.md](../architecture/payment-provider-interfaces.md) (`psp` placement, interfaces, routing, sandbox scenario triggers §11), [payment-state-machine.md](../architecture/payment-state-machine.md) (intent vs provider transaction, `UNKNOWN`, `applyEvent`, webhook pipeline, parking), [payment-schema.md](../database/payment-schema.md) and the non-executable drafts in `design/sql/` (fees, PSP registry, Model A routing guard), [transaction-lifecycle.md](../payments/transaction-lifecycle.md), ADR-007, ADR-013, ADR-018, ADR-020, ADR-024, ADR-025, ADR-029. Module graph: [design-baseline.md](../stage-2/design-baseline.md) §2–§3 (`payments` imports campaigns, fees, ledger, risk, psp, storage, compliance read-only; `psp` imports platform and audit only) |
| Platform | Idempotency middleware (`internal/platform/idempotency`, used optionally by campaign creation), transactional outbox and River jobs, `app.guard_transition` + `app.status_transitions` for DB-guarded state machines, audit, Valkey rate limiter whose errors now carry an accurate `Retry-After` (`RetryAfter()` interface read by `httpx.WriteError`), `If-Match` helper (428/412), keyset list conventions `{items, next_cursor}` (ADR-038 §7) |
| Public campaign page | `apps/web/src/app/campaigns/[slug]/page.tsx` shows a disabled "Donations are not yet available." control; no totals, progress or donor lists. Campaign data is fetched `no-store`; the API answers `public, no-cache` + ETag |
| Restrictions | `compliance.Service.Restrictions` (fail closed) — Stage 8 must check the `DONATE` capability the same way |
| Currency | USD only for goals; ZWG refused until minor units are verified (LR-043, PCR-018) |
| CI | Go, web, mock E2E, integration, real-stack E2E jobs. Result of the Stage 7 run: see the Stage 7 completion report |

## 2. Rules Stage 8 must keep (CLAUDE.md, binding)

- Money is `int64` minor units + currency; never floats; never combine currencies; no FX.
- Model A (ADR-013): a licensed PSP collects and holds; routing **refuses** `MERCHANT_SETTLEMENT` providers for
  donations, in code and in the database.
- A browser redirect never confirms a payment; only verified webhooks, authenticated status queries or
  reconciliation do. Unknown outcome ⇒ `UNKNOWN`, never `FAILED` by assumption.
- Every payment creation and webhook processing step is idempotent, backed by unique constraints; webhooks are
  authenticated (signature, timestamp/replay window, dedupe).
- Payment states guarded in the database (`app.guard_transition`); Go and SQL edge lists identical.
- Ledger effects go through `ledger.Post` only; Stage 8 uses the test-only stub planned in the ROADMAP (real ledger in
  Stage 10). No `campaign.balance` fields; no totals derived outside the ledger.
- No card data; hosted/tokenised flows only. Provider secrets by reference (secret manager), never in the repo.

## 3. Decisions and inputs needed for Stage 8

| Topic | Input / decision |
|---|---|
| Donation-flow UI | Not built in Stage 7 (KI-S7-10). The ROADMAP's Stage 8 testing expects a donation-flow E2E against the sandbox; the UI with "awaiting confirmation" states, and receipts, must be scoped explicitly (Stage 8 or later) — they must never show a donation as confirmed without server confirmation |
| Donation eligibility | Campaign `ACTIVE` ≠ donation-eligible: define eligibility (live PUBLIC/UNLISTED status, `DONATE` restriction, currency rail available and verified, holds) and its error codes |
| Public totals | Keep "no totals" until the ledger exists (Stage 10). If any figure is shown earlier it must come from the ledger per currency (MONEY §8) — never from campaign fields, analytics counters or the sandbox |
| Caching | Payment and donation responses are private `no-store`. If a public campaign response later gains ledger-derived figures, ADR-038 §1 (no-cache + ETag, no shared cache) still applies |
| Rate limits | New policies for intent creation per IP/donor and per campaign; follow the `Policy` + `RetryAfter()` pattern |
| Analytics | No payment events in `app.public_event_counters` without a new decision; never record amounts or donor identities there |
| Sandbox provider | Implement the scenario triggers in payment-provider-interfaces.md §11 (decline, timeout, duplicate/out-of-order webhooks, late success); it must be non-production only (refuse to start with it in production, as the KMS start-up check does today) |
| ZWG | Stays sandbox-only behind `minor_units_verified` until LR-043 / PCR-018 are resolved |

## 4. Open legal and provider items relevant to Stage 8

- **LEGAL_REVIEW_REQUIRED:** LR-001 (custody), LR-003 (stored value), LR-004 (PSP licensing and role), LR-005 and
  LR-044 (cross-border donations), LR-018 (consumer protection and refunds), LR-019 (cancelled/frozen campaign
  funds — KI-S6-13), LR-020 (chargebacks), LR-026 (donor identity disclosure), LR-028 (platform fee model), LR-043
  (ZWG minor unit), LR-075 (FundZim as instructing party), LR-081 (refunds to another instrument).
- **PROVIDER_CONFIRMATION_REQUIRED:** PCR-003 (who holds funds), PCR-009 (idempotent creation), PCR-010
  (status-query semantics and expiry), PCR-011 (collection reversals), PCR-016 (fee reporting), PCR-017 (webhook
  security), PCR-018 (ZiG code and precision), PCR-023 (card data scope), PCR-025 (authorisation vs capture).
- No provider is selected; candidate names in [provider-comparison.md](../payments/provider-comparison.md) are
  `PENDING` and none of their capabilities is asserted.

## 5. Open items to schedule

From Stage 7: KI-S7-02 (`global.ip` vs public images), KI-S7-04 (manual accessibility audit), KI-S7-05 (web
performance budget), KI-S7-07 (privacy notice and terms — LR-022, LR-035), KI-S7-09 (stale e2e-real README),
KI-S7-11 (completed staff picks), KI-S7-13 (report retention — LR-012), KI-S7-14 (Stage 5 API conventions).
From Stage 6: KI-S6-10 (review queue status filter), KI-S6-11, KI-S6-14, KI-S6-16, KI-S6-20, KI-S6-21; legal LR-046 –
LR-048 and LR-070 remain blocking for their features.
