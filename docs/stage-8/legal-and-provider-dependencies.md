# Stage 8 — Legal, regulatory and provider dependencies

Sources: [open-legal-questions.md](../compliance/open-legal-questions.md) (authoritative `LR-xxx` register, read
2026-10-12), [provider-questions.md](../payments/provider-questions.md) (`PCR-xxx`),
[operating-model-decision.md](../compliance/operating-model-decision.md) (ADR-013 Model A). Nothing here is a legal
conclusion. Every item below is **OPEN**; none was resolved in Stage 8.

## 1. The three items named in the Stage 8 brief (exact register definitions)

| ID | Register area | Register question (verbatim, without the Stage 1 research note) | Owner / priority | Stage 8 effect |
|---|---|---|---|---|
| LR-012 | Retention periods | "Minimum and maximum retention for KYC records, financial/transaction records, ledger, audit logs, compliance cases, and marketing data; start events (e.g. account closure, transaction date)." Register: "No retention numbers in code until resolved." | Counsel, DPO · P1 | No retention or deletion job is written for payment records, webhook inbox rows or payment events. Payment tables are append-only or delete-forbidden; retention is a recorded known issue |
| LR-022 | Terms of service & platform liability | "Required terms (user agreement, campaign rules, prohibited causes, privacy notice, cookie notice), platform liability for campaign content and misuse, governing law." | Counsel, Founders · **P0** | No donor-facing terms, disclosures or checkout exist. A donor checkout cannot go live without donation terms and CPA s52 disclosures. Donation intents record no `terms_version` in Stage 8 (no terms exist to accept) |
| LR-035 | Lawful basis & consent for processing | "Lawful basis for each processing purpose (account, KYC, fraud prevention incl. device/IP signals, analytics, marketing); form and records of consent where consent is the basis." | Counsel, DPO · **P0** | Donation intents store only what the payment needs (campaign, donor user id, amount, currency, method, anonymity choice). No device data, no payer PII, no marketing consent. Payer instrument identifiers from providers are stored only as HMAC fingerprints and masked values |

## 2. Other open items and how each affects Stage 8 areas

`LEGAL_REVIEW_REQUIRED` items:

| ID | Topic | Affects | Stage 8 handling |
|---|---|---|---|
| LR-001 | Custody of funds | Funds flow, settlement, provider selection | Model A only: routing refuses `MERCHANT_SETTLEMENT` in code and in the database (composite FK + CHECK). No FundZim collection account exists |
| LR-003 | Stored value / e-money | Funds flow, payouts | No balances, wallets or campaign totals. Payment records are operational, not balances |
| LR-004 | PSP licensing & FundZim's role | Provider selection, donation initiation | No real provider can be enabled (DB CHECK + `psp.LiveExecutionEnabled = false`) |
| LR-075 | FundZim as instructing party | Payout eligibility, provider selection | No payouts; payment initiation only through the sandbox |
| LR-005 / LR-044 | Cross-border donations | Provider selection, refunds | No international rail; `CARD` exists only as a sandbox capability |
| LR-043 | ZWG minor unit | Currency handling | ZWG refused for donation intents (`minor_units_verified = false`); sandbox rows for ZWG are not created |
| LR-018 | Consumer protection & refunds | Refunds, disclosures | Refund requests are recorded and approved under maker-checker but **never executed**; no donor refund self-service |
| LR-019 | Cancelled/frozen campaign funds | Refunds | No automatic refund of any campaign; reason `CAMPAIGN_CANCELLED` exists for staff requests only |
| LR-020 | Chargebacks & dispute liability | Chargebacks | Dispute and chargeback tables are deferred (ADR-039); the state machine edges exist but nothing drives them |
| LR-026 | Donor identity disclosure | Donor identity, anonymity | `is_anonymous` is a public-display choice only; the payment itself is always tied to an authenticated donor account. Anonymous payment processing is not possible |
| LR-081 | Refunds to another instrument | Refunds | `execution_method = PROVIDER_REFUND` only; manual refunds to another instrument are not possible |
| LR-012, LR-022, LR-035 | See §1 | Data protection, disclosures | See §1 |

`PROVIDER_CONFIRMATION_REQUIRED` items (no provider selected; every flag stays false in the registry for real
providers, and no real provider row exists):

| ID | Question | Affects | Stage 8 handling |
|---|---|---|---|
| PCR-003 | Who holds funds | Settlement | Only `PSP_POOL` / `SPLIT_DIRECT` capabilities are routable |
| PCR-009 | Idempotent payment creation | Payment idempotency, retries | Re-create after an unknown outcome only when the capability says `idempotent_create`, always with the same `payment_id`; otherwise status query only |
| PCR-010 | Status-query semantics, expiry | Unknown outcomes, reconciliation | `NOT_FOUND` never becomes `FAILED` without documented finality; unconfigured horizon ⇒ 72 h default, flagged |
| PCR-011 | Collection reversals | Chargebacks | Deferred with disputes |
| PCR-006 | Refund API | Refunds | Refund execution disabled |
| PCR-016 | Fee reporting | Reconciliation | Provider fee stored if reported; not used |
| PCR-017 | Webhook security | Webhook verification | The sandbox uses HMAC-SHA256 over a timestamped payload; real providers must use their **documented** procedure (no universal algorithm) |
| PCR-018 | ZiG code and precision | Currency handling | ZWG refused |
| PCR-023 | Card data scope | Payment methods | No card data fields exist anywhere; hosted flows only |
| PCR-025 | Authorisation vs capture | State machine | `AUTHORISED` only for `CARD` with `separate_capture` (sandbox rows only) |

`SCREENING_PROVIDER_NOT_SELECTED` (KI-S5-04): donors are not screened. Production donation acceptance is blocked
until screening exists (LR-009, LR-053, LR-064).

## 3. Effect by area (brief §4)

| Area | Blocking items | Stage 8 position |
|---|---|---|
| Donation initiation | LR-004, LR-022, LR-035, LR-075, screening | Sandbox only; real initiation disabled globally |
| Provider selection | LR-001, LR-004, PCR-001 – PCR-007 | None selected; register is all `UNKNOWN` |
| Funds flow | LR-001, LR-003, PCR-003 | Model A enforced; no balances |
| Customer disclosures | LR-018, LR-022 | None shown; public CTA stays disabled |
| Data protection | LR-012, LR-035 | Minimal data; no retention jobs |
| Refunds | LR-018, LR-019, LR-081, LR-083, PCR-006 | Requests and approvals only; execution disabled |
| Chargebacks | LR-020, PCR-011 | Deferred |
| Reconciliation | PCR-007, PCR-016 | Foundation records only; never marked reconciled |
| Settlement | LR-001, PCR-003 | Not modelled (no settlement balances) |
| Payout eligibility | LR-075, ledger | Not touched |
