# Stage 12 prerequisite assessment

Date: 2026-10-15. Branch `stage-12/fees`, created from `stage-11/payouts` at `491e0be` (CI run 38697408270: all 8 jobs
succeeded). Inputs read: `CLAUDE.md`, `README.md`, `docs/PRODUCT.md` (PD-01, PD-20, PD-22, PD-23), `docs/ARCHITECTURE.md`,
`docs/SECURITY.md`, `docs/ROADMAP.md`, `docs/MONEY.md` §5–§6, [STAGE-11-TO-STAGE-12.md](../stage-handover/STAGE-11-TO-STAGE-12.md),
the Stage 2 design baseline (§2, §3, §5.7) and draft `design/sql/0007_fees_psp.sql`, the ledger chart and posting rules
(`migrations/20261013120000_ledger.sql`, `internal/ledger`), the payout schema and code (`internal/payouts`), donation
intents, payments and refund requests (`internal/payments`), the provider registry (`internal/psp`), RBAC
(`migrations/20261008140300_identity_rbac.sql`: `fee.config.request`, `fee.config.approve`, role `BUSINESS_APPROVER`),
step-up, the outbox, the OpenAPI contract, the web design system and CI, the legal register
[open-legal-questions.md](../compliance/open-legal-questions.md), [provider-questions.md](../payments/provider-questions.md),
[../stage-9/known-issues.md](../stage-9/known-issues.md), ADR-040, ADR-042 and ADR-043.

Classification: **SATISFIED**, **SATISFIED_WITH_CONDITIONS**, **BLOCKED**, **NOT_VERIFIED**. No acceptance, legal or tax
opinion, accounting approval, provider fee or settlement evidence was fabricated.

## 1. Governance

| Item | Status | Evidence |
|---|---|---|
| Formal acceptance of Stages 1–11 | **NOT_VERIFIED** | [stage-acceptance-register.md](../governance/stage-acceptance-register.md): every row `NOT_RECORDED` |
| Stage 9 | PARTIAL (recommendation) | ADR-040 DECISION_DEFERRED; provider integration and certification BLOCKED |
| Stage 10, Stage 11 | PASS WITH CONDITIONS (recommendations) | TEST ledger only; TEST payouts only |
| Consequence | — | Stage 12 is isolated, non-production engineering. Production financial operations and production readiness are **BLOCKED**. No row of the register is changed to an accepted state. |

## 2. Legal, tax and accounting dependencies (register text abridged only by "…"; the register is authoritative)

| Item | Exact question (abridged) | Status | Effect on Stage 12 |
|---|---|---|---|
| LR-001 | "Can FundZim lawfully hold funds even transiently … and if so under what licence/trust structure?" | OPEN — LEGAL_REVIEW_REQUIRED | Model A assumed (ADR-013); fee collection design depends on it |
| LR-002 | "Are amounts orchestrated for campaigns FundZim balance-sheet items … How should the ledger chart of accounts map to statutory accounts?" | OPEN — ACCOUNTING_REVIEW_REQUIRED | Revenue accounts are TEST records only |
| LR-016 | "VAT and income tax treatment of platform fees; any transaction-level taxes … on donations/payouts and who withholds; tax registration requirements." | OPEN — TAX_REVIEW_REQUIRED | No tax is calculated; no VAT rate is assumed or stored. The register's Stage 1 research notes are not used as configuration |
| LR-018 | "Donor refund rights … disclosure obligations; complaint handling requirements." | OPEN — LEGAL_REVIEW_REQUIRED | Disclosure copy is a preview; refund fee treatment is configuration |
| LR-020 | "Who bears chargeback loss after payout …? Reserve/rolling-hold requirements …; recovery rights against campaign owners." | OPEN — LEGAL_REVIEW_REQUIRED | Chargeback fees: foundations only; never assumed recoverable from organisers |
| LR-028 | "fee deducted from donation vs donor-covers-fees vs optional tip; fee disclosure requirements." | OPEN (business decision + tax) | All responsibility models are configurable; none is chosen |
| LR-045 | "Are only FundZim's fees taxable supplies, or gross donations too (agency vs principal)? When must FundZim register and fiscalise? …" | OPEN — TAX_REVIEW_REQUIRED | Merchant-of-record and agency position undecided; fees not treated as taxable or exempt |
| LR-056 | "Do CPA s52 (disclosures) and s53 … apply to donations, optional tips, and platform fees? …" | OPEN — LEGAL_REVIEW_REQUIRED | Disclosure obligations unknown |
| LR-059 | "Is IMTT charged on both the collection and payout legs (and on fee remittance)? … What must be disclosed …?" | OPEN — TAX_REVIEW_REQUIRED | No transaction tax modelled |
| LR-012 | Retention periods | OPEN — LEGAL_REVIEW_REQUIRED | Fee records are never purged |
| LR-043 | ZWG minor unit | OPEN | Fees in ZWG are refused where ZWG is not usable |
| LR-076 | "May FundZim move its own funds into a PSP-held pool (fee true-ups, chargeback shortfall funding …)?" | OPEN — LEGAL_REVIEW_REQUIRED | Fee true-up journals are not posted |
| LR-080 | Beneficiary recovery (clawback) | OPEN — LEGAL_REVIEW_REQUIRED | Organiser-borne chargeback fees are exposure records, not recoveries |
| PD-01 / PD-20 / PD-23 | Fee model; fee treatment on refunds and chargebacks (provisional: platform fee reversed, campaign bears PSP and dispute fees); who bears payout fees | OPEN business decisions | Configurable per policy version; the provisional PD-20 values are not hard-coded |
| Currency conversion | FundZim never converts currencies (CLAUDE.md; currency-and-fx-policy.md) | Rule | `CURRENCY_CONVERSION_FEE` cannot be enabled |

## 3. Provider dependencies (ADR-040; KI-S9-01..16 as defined in [../stage-9/known-issues.md](../stage-9/known-issues.md))

| Item | Status | Effect |
|---|---|---|
| Payment provider selected and certified | **BLOCKED** (ADR-040, KI-S9-02) | Only the sandbox simulator exists |
| Provider transaction, settlement, refund, chargeback, payout fees, mobile-money charges, card fees, FX margins | **UNKNOWN** (PCR-005, PCR-007, PCR-013, PCR-016) | Provider fee schedules may only be `SYNTHETIC` (sandbox, TEST) or `UNVERIFIED`; `VERIFIED` is refused by CHECK |
| Provider rounding behaviour | **UNKNOWN** | An estimate records the rounding it used and is labelled `ESTIMATED` |
| Fee reported on callback / status / statement (PCR-016) | **UNKNOWN** | Provider-reported fees enter only through the TEST fixture; reconciliation flags unreported provider fees |
| Fee pass-through to donors permitted | **UNKNOWN** | Donor-covered charges are recorded on the intent, not sent to a provider |
| Refund sandbox (KI-S9-07) | **BLOCKED** | Refund fee adjustments run on TEST_ONLY fixtures |
| International cards (KI-S9-09), ZIPIT/ZimSwitch (KI-S9-08) | BLOCKED / OPEN | No method-specific fee for them |
| Public checkout (KI-S9-16) | Disabled | Donor disclosure is a signed-in preview only |

## 4. Technical prerequisites

| Prerequisite | Status | Evidence |
|---|---|---|
| Exact money (`internal/platform/money`, basis points, allocation) | SATISFIED | MONEY.md §5–§6; `MulBasisPoints`, `Allocate` |
| Currency registry and usable currencies | SATISFIED | `money.UsableCurrencies` |
| Donation intents, payments, attempts (Stage 8/9) | SATISFIED_WITH_CONDITIONS | Test payments only; payment amount is bound to the intent amount by FK, so a donor-covered surcharge cannot reach the simulator |
| Provider registry, routing, capabilities | SATISFIED | `psp.Route`, `AvailableMethods` |
| Provider fee evidence | **BLOCKED** | No provider; no evidence |
| Chart of accounts and fee posting rules | SATISFIED_WITH_CONDITIONS | `revenue:platform_fees`, `expense:psp_processing_fees`; rules `DONATION_CAPTURED` (fee line), `PSP_FEE`, `PAYOUT_FEE`, `REFUND_RESERVED`, `DISPUTE_OPENED`, `DISPUTE_FEE`; accounting review pending (KI-S10-02) |
| Posting engine, idempotency, reversals, periods | SATISFIED | Stage 10 `ledger.Service` |
| `ledger_transactions.fee_schedule_version_id` | SATISFIED | Column exists since Stage 10 (unused) |
| Payout reservations and eligibility (Stage 11) | SATISFIED | `internal/payouts`; `provider_fee_minor`, `beneficiary_received_minor` columns |
| Refund architecture | SATISFIED_WITH_CONDITIONS | Refund requests stop at APPROVED (cannot execute) |
| Chargeback / dispute model | **NOT PRESENT** | No dispute source exists (KI-S11-08) |
| Audit, maker-checker, step-up, linked identities | SATISFIED | `app.actor_identities`, `StepUpFresh` |
| Fee permissions | SATISFIED_WITH_CONDITIONS | `fee.config.request` (FINANCE), `fee.config.approve` (BUSINESS_APPROVER) exist since Stage 4; view, adjustment and fixture permissions added |
| Transactional outbox | SATISFIED | `payments.payment_succeeded` carries the intent id |
| OpenAPI, migrations, CI, web design system | SATISFIED | Stage 11 baseline green |
| Stage 2 module graph | SATISFIED_WITH_CONDITIONS | `fees` may not import `ledger`; amended by ADR-044 |
