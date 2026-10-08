# CLAUDE.md — FundZim engineering instructions

These are persistent, binding instructions for every Claude Code session (and every human engineer) working
in this repository. FundZim will orchestrate real people's money and hold sensitive identity data. When a rule
here conflicts with speed, convenience, or a request to "just make it work", the rule wins — stop and say so.

## What FundZim is

A Zimbabwe-first, donation-based crowdfunding platform: "Fund anyone in Zimbabwe, from anywhere."
Individuals and verified organisations raise funds; donors inside and outside Zimbabwe contribute through
licensed payment service providers (PSPs). Donation-only — **no** equity, securities, lending, interest,
profit-sharing, or rewards crowdfunding.

Read `docs/PRODUCT.md` for scope and `docs/ROADMAP.md` for the staged plan.

## Current stage

**Stage 2 — System Architecture, Database Design & API Contracts (complete; awaiting acceptance).**
Stage 0 is complete; Stage 1 is complete but its acceptance has not been recorded
(`docs/stage-2/prerequisite-assessment.md`). A documentation-stage PASS is never permission to operate a live
crowdfunding business or to move real money.
Do not start a stage until the user explicitly asks for it. Do not implement work belonging to a later stage
"while you're there". Each stage ends with a completion report (template in `docs/DEVELOPMENT.md`) and then
STOPS.

## Repository map

| Path | Purpose |
|---|---|
| `apps/web/` | Next.js 16 (App Router, TypeScript, Tailwind v4). **Presentation only.** Has its own `AGENTS.md` — read it: this Next.js version differs from older training data; consult `apps/web/node_modules/next/dist/docs/` before writing Next.js code. |
| `apps/api/` | Go API + worker entrypoint (code begins Stage 3). |
| `internal/` | Go modular-monolith domain modules (code begins Stage 3). One Go module rooted at repo root. |
| `migrations/` | SQL migrations (begin Stage 2/3). |
| `docs/` | Specifications. `docs/adr/` holds Architecture Decision Records. |
| `api/openapi/` | OpenAPI 3.1 contract for `/api/v1` (source of truth, ADR-026). |
| `design/sql/` | Stage 2 **non-executable** schema drafts + in-memory validation harness. Not migrations. |
| `deploy/`, `scripts/`, `tests/` | Deployment config, dev scripts, cross-cutting test suites. |

## Before you change anything

1. Read the docs for the subsystem you are touching **before** editing it:
   money → `docs/MONEY.md`; ledger → `docs/LEDGER.md`; payments/webhooks/payouts → `docs/PAYMENTS.md`;
   auth/permissions/uploads → `docs/SECURITY.md`; KYC/personal data → `docs/DATA-CLASSIFICATION.md` and
   `docs/PRIVACY.md`; schema → `docs/DATABASE.md`; audit → `docs/AUDIT.md`; regulatory → `docs/COMPLIANCE.md`;
   UI → `docs/FRONTEND.md`; logging/metrics → `docs/OBSERVABILITY.md`; overall → `docs/ARCHITECTURE.md`.
   Stage 1 specifications refine these and win where they are more specific:
   operating model / custody → `docs/compliance/operating-model-decision.md`, `docs/ledger/settlement-and-custody-model.md`;
   payment & payout states → `docs/payments/transaction-lifecycle.md`, `docs/payments/payout-lifecycle.md`,
   `docs/payments/payout-eligibility-and-controls.md`; refunds/disputes → `docs/payments/refund-and-dispute-architecture.md`;
   currency → `docs/payments/currency-and-fx-policy.md`; providers → `docs/payments/provider-*.md`;
   identity → `docs/compliance/kyc-architecture.md`, `kyb-architecture.md`, `beneficiary-verification.md`,
   `docs/security/identity-data-protection.md`; AML/risk → `docs/compliance/aml-risk-framework.md` and siblings;
   staff controls → `docs/compliance/operational-controls.md`; evidence → `docs/compliance/audit-evidence-model.md`.
   Stage 2 (system design) refines all of the above and wins where more specific: module list, dependency
   graph, schemas, table names and owners → `docs/stage-2/design-baseline.md` (ADR-021/022); per-domain
   schema → `docs/database/`; architecture → `docs/architecture/`; HTTP contract → `api/openapi/fundzim-v1.yaml`
   and `docs/api/`. `design/sql/` holds NON-EXECUTABLE schema drafts (validated in-memory only) — never run
   them against a real database; executable migrations go in `/migrations` (goose, ADR-028).
   Stage 3 starts from `docs/stage-handover/STAGE-2-TO-STAGE-3.md`.
2. Check the relevant ADRs in `docs/adr/`.
3. If the change contradicts a doc or ADR, do not silently diverge. Propose a new ADR (or a superseding one)
   and get agreement first.

## Non-negotiable financial rules

1. **Never use floating point for money.** Not in Go, SQL, JSON, or TypeScript. Money is an `int64` count of
   minor units **plus** an ISO 4217 currency code (`amount_minor` + `currency`). In JSON, `amount_minor` is a
   string of digits. See `docs/MONEY.md`.
2. **Currency always travels with an amount.** A bare number is never money.
3. **Never combine currencies.** USD and ZiG (`ZWG`) are never summed, compared, netted, or converted
   implicitly. There is no automatic FX.
4. **Never mutate financial history.** Ledger, payment-event, and audit records are append-only. Corrections
   are new reversing/adjusting entries that reference the original.
5. **Never bypass the ledger.** No `campaign.balance += donation`, no `user.balance -= withdrawal`. Balances are
   derived from double-entry ledger entries (or projections updated in the same DB transaction and verifiable
   by full recompute). Invariant: per transaction and per currency, **total debits == total credits**.
6. **Never trust a browser redirect** (or any client claim) as payment confirmation. Only verified webhooks,
   authenticated provider status APIs, or reconciliation confirm payments.
7. **Every financial operation is idempotent**: payment creation, webhook processing, ledger posting, refunds,
   payout requests, payout processing. Back idempotency with database unique constraints, not just code.
8. **Every external callback is authenticated and verified** (signature, timestamp/replay window, dedupe).
9. **Unknown outcome ≠ failure.** A timeout or ambiguous provider response puts a payment or payout in
   `UNKNOWN`, resolved only by authoritative status query, webhook or reconciliation — never `FAILED` by
   assumption. A payout in `UNKNOWN` is **never resubmitted**; the provider reference (`payment_id`,
   `payout_id`, `refund_id`) is reused on every retry, and a retry after an authoritative failure is a new
   request with a new id.
10. **Enforce critical invariants in the database too** (constraints, triggers, restricted grants), not only
    in application code.
11. **Money states are distinct.** Captured ≠ settled ≠ available ≠ reserved ≠ paid out. Never present them as
    one "campaign balance". Funds become available only after a settlement match and a release posting
    (`docs/ledger/settlement-and-custody-model.md`).
12. **Payouts fail closed.** Every eligibility check must pass at request time and again immediately before
    submission. An unconfigured limit, missing verification, open hold or missing fundraising-authority
    evidence routes the payout to manual review — never to automatic approval.
13. **Stop and report** if you observe a financial invariant failure (imbalanced journal, negative available
    balance, reconciliation mismatch, duplicate posting). Do not "fix" it by editing data or loosening checks.

## Security and privacy rules

- Follow least privilege everywhere: DB roles, cloud IAM, staff permissions, service credentials.
  Admin roles do **not** implicitly get KYC document access or ledger-adjustment rights.
- Preserve user/organisation/tenant boundaries. Every read and write of a user-owned resource checks
  authorisation server-side (no IDOR). Never rely on unguessable IDs for access control.
- **Never log** passwords, OTPs, session tokens, API keys, private keys, webhook secrets, card numbers, CVV,
  full identity-document numbers or images, or full payout account numbers. Use the redaction allow-list.
- KYC data is **C3 RESTRICTED**: it lives in the `kyc` schema and the `private-kyc` bucket, is only touched by
  the `kyc` module, and every access is audited. Never treat KYC documents like campaign images.
- Never store raw card data. Use hosted/tokenised PSP flows only.
- Never commit secrets. `.env` files are git-ignored; only `.env.example` (placeholders) is tracked.
  Run `scripts/check-secrets.sh` (and gitleaks when installed) before committing.
- Do not remove, weaken, or bypass a security control to get past a development problem. Fix the problem.
- Sensitive staff actions require maker-checker (the initiator cannot approve): payouts above configurable
  thresholds or flagged by risk (every payout still passes automated policy checks), refunds, ledger
  adjustments, role grants, fee-configuration changes, limit/threshold changes, compliance overrides and
  exemptions, staff changes to payout destinations, and unfreezing campaigns
  (`docs/compliance/operational-controls.md`, ADR-017).
- Holds and freezes are new records plus ledger moves (payable → held). They never edit or delete history.
- Audit events never embed identity documents, KYC values or secrets; they reference evidence records
  (`docs/compliance/audit-evidence-model.md`, ADR-019).

## Regulatory rules

- Do not invent legal or regulatory conclusions. Do not assume FundZim may hold customer funds, operate a
  wallet/stored value, or act as a payment provider.
- **Operating model A (ADR-013):** a licensed PSP collects, holds and disburses; FundZim orchestrates and keeps
  records. Only FundZim's own platform-fee income may land in a FundZim bank account. Never integrate a
  provider in a way that settles donor funds to a FundZim account (merchant settlement = Model B in
  substance): provider routing must refuse that custody model. Moving to Model B needs a new ADR plus legal
  sign-off.
- Ledger balances are accounting records, not spendable stored value. Never build wallets, user-spendable
  balances or transfers between users.
- FundZim never converts currencies (only authorised dealers may). A rail that cannot settle in the donation
  currency is not offered for that donation.
- Mark every unresolved legal/regulatory dependency `LEGAL_REVIEW_REQUIRED` with an `LR-xxx` entry in
  `docs/compliance/open-legal-questions.md` (the authoritative register). Unconfirmed provider behaviour is
  `PROVIDER_CONFIRMATION_REQUIRED` with a `PCR-xxx` entry in `docs/payments/provider-questions.md`. Unknown
  regulatory requirements go in `docs/compliance/regulatory-requirements-register.md` (`REQ-xxx`).
- Never invent a statute, section, statutory instrument, threshold, rate, fee or provider capability. Cite a
  source you actually read, with URL and access date, or mark it unverified.
- Legal thresholds and limits are configuration (type REGULATORY / PROVIDER / INTERNAL_RISK, with source,
  approval owner, effective dates and review date) — never hard-coded.
- Never write "compliant", "licensed", "approved", "PCI compliant", "certified" or similar claims anywhere
  (code, docs, UI copy) unless the user supplies evidence.

## Engineering rules

- Prefer boring, proven technology. Modular monolith; no microservices, Kubernetes, or Kafka without an ADR
  that demonstrates the need.
- Respect the module dependency graph in `docs/stage-2/design-baseline.md` §3. A module's SQL may reference
  only tables it owns (§5). Adding a dependency or moving table ownership needs an ADR.
- Every state machine (payments, refunds, disputes, payouts, campaigns, compliance cases, KYC cases, ledger
  adjustments) is guarded in the database by `app.guard_transition` + `app.status_transitions`; keep Go and SQL
  edge lists identical.
- Go modules communicate only through each other's public service interfaces — never through another
  module's tables. No circular dependencies. Only `ledger` writes ledger tables.
- PostgreSQL is the only authoritative store. Redis is cache/rate-limit/coordination only and may be lost
  without data loss. Financial background jobs use the Postgres-backed queue.
- Timestamps: `timestamptz`, UTC internally; display in Africa/Harare or user preference. Inject the clock.
- IDs: UUIDv7. API: `/api/v1/`, standard envelope and stable `UPPER_SNAKE` error codes.
- Add tests with every feature. Financial code also needs invariant, idempotency, and concurrency tests.
- **Never disable, skip, or delete a test to make a build pass.** Never weaken an assertion to make it green.
- Do not claim tests pass unless you ran them in this session and saw them pass. Report failures with output.
- Update documentation in the same change when behaviour or architecture changes. Significant architectural
  changes require an ADR (`docs/adr/README.md`).
- Do not create placeholder files to make the repo look complete. Do not fake command output or features.

## Commands

`make help` lists targets (`dev`, `test`, `lint`, `build`, `migrate`, `security`, `clean`). Until Stage 3 most
targets only cover `apps/web` or print guidance. `make` is not yet installed on the current dev machine —
see `docs/DEVELOPMENT.md`; equivalent: `npm --prefix apps/web run <script>`.
Node is managed by nvm (Node 24 LTS). Go is not yet installed.

## Stage completion

Each stage ends with a report using the template in `docs/DEVELOPMENT.md` ("Stage completion report"):
status, what was implemented, docs, ADRs, tree, controls, LEGAL_REVIEW_REQUIRED items, assumptions, tests
actually run and their results, secrets scan, known issues, technical debt, deviations, files changed, inputs
for the next stage, acceptance recommendation. Then stop and wait for the user.
