# Stage 10 prerequisite assessment

Date: 2026-10-13. Branch `stage-10/ledger`, created from `stage-9/provider-integrations` at `e57cbfc` (CI run
38406730970: all 8 jobs green). Inputs read: `CLAUDE.md`, `docs/LEDGER.md`, `docs/MONEY.md`,
`docs/ledger/settlement-and-custody-model.md`, `docs/architecture/ledger-invariants.md`, ADR-006, ADR-014, ADR-023,
the Stage 2 draft `design/sql/0006_ledger.sql`, `docs/stage-9/*` (in particular
[known-issues.md](../stage-9/known-issues.md) for the exact KI-S9 definitions), ADR-039 – ADR-041,
`docs/stage-handover/STAGE-9-TO-STAGE-10.md`, `docs/governance/stage-acceptance-register.md` and the Stage 8/9 code
(`internal/payments`, `internal/psp`).

Classification: **SATISFIED**, **SATISFIED_WITH_CONDITIONS**, **BLOCKED**, **NOT_VERIFIED**. No acceptance record,
legal opinion, accounting approval or provider confirmation was fabricated.

## 1. Governance

| Prerequisite | Status | Evidence / condition |
|---|---|---|
| Formal acceptance of Stages 1–9 | **NOT_VERIFIED** | [stage-acceptance-register.md](../governance/stage-acceptance-register.md): every row `NOT_RECORDED`. The owner supplied the Stage 10 master prompt but did not record acceptance. Stage 10 proceeds as **non-production engineering work**; nothing is presented as accepted |
| Stage 9 status | SATISFIED_WITH_CONDITIONS | Stage 9 reported **PARTIAL** (provider integration and sandbox certification BLOCKED, ADR-040 DECISION_DEFERRED). It remains PARTIAL; Stage 10 does not promote it |
| Owner request to start Stage 10 | SATISFIED | The owner supplied the Stage 10 master prompt |
| Accounting approval of the chart of accounts and posting rules | **NOT_VERIFIED** | No accountant or auditor review exists. The chart is the Stage 1/2 design (ADR-014, settlement model §3.2), implemented as designed; an accounting review is an external gate (KI-S10-02) |
| Legal sign-off on custody, agency and restricted funds (LR-001, LR-002, LR-003, LR-076) | **BLOCKED** | Open in [open-legal-questions.md](../compliance/open-legal-questions.md). The ledger names accounts as claims and obligations, not as FundZim cash (§2 below) |
| Production financial posting | **BLOCKED** (by design) | `ledger.ProductionPostingEnabled = false`; the database ledger environment is `TEST` and `PRODUCTION` is refused by `ck_*_no_production_stage10` |

## 2. Stage 9 known issues that bear on the ledger

The meaning of each item is its definition in [../stage-9/known-issues.md](../stage-9/known-issues.md); the table
records only its effect on Stage 10.

| Item | Stage 9 definition (abridged, exact text in the source) | Effect on Stage 10 |
|---|---|---|
| KI-S9-01 | Formal acceptance of Stages 1–8 (and 9) is not recorded | Stage 10 is non-production engineering (row 1 above). Carried as KI-S10-01 |
| KI-S9-02 | No payment provider selected (ADR-040 DECISION_DEFERRED); no merchant agreement, no sandbox credentials, no written PCR answers | No provider-reported or settlement evidence exists. The ledger can only hold synthetic TEST journals; settlement reconciliation is **NOT_PERFORMED** |
| KI-S9-03 | Production payment execution BLOCKED (live execution disabled in code; DB refuses PRODUCTION intents/payments; legal items open) | No payment is eligible for posting. The payments consumer records every `payments.payment_succeeded` as NOT_ELIGIBLE |
| KI-S9-04 | Secret manager integration done; operating a production OpenBao/Vault not done | The ledger stores no secrets; the new `fundzim_ledger` database credential follows the existing database-credential handling (environment), production handling is Stage 18/20 |
| KI-S9-05 | OpenBao in compose/CI is dev mode | No effect (the ledger uses no provider secret) |
| KI-S9-06 | Unsigned-callback / form-hash webhook verification not implemented | No effect (no webhook posts to the ledger) |
| KI-S9-07 | No refund sandbox; refund behaviour unconfirmed (PCR-006); refund requests stop at APPROVED | Refund posting rules exist (REFUND_RESERVED / SETTLED / FAILED) and are exercised only by synthetic fixtures; no refund request posts |
| KI-S9-08 | ZIPIT has no method code; ZimSwitch and ZIPIT PROVIDER_CONFIRMATION_REQUIRED | No effect on the ledger (methods are not ledger dimensions) |
| KI-S9-09 | International cards not distinguished from local cards | No effect on the ledger; currency remains USD-only |
| KI-S9-10 | Health thresholds simple | No effect |
| KI-S9-11 | Audit of provider configuration at start skipped when the DB is unreachable | No effect; ledger audit events are written in the posting transaction |
| KI-S9-12 | Provider health computed per request | No effect |
| KI-S9-13 | Provider evidence log has no retention policy (LR-012) | Same open question applies to ledger records: retention is **LEGAL_REVIEW_REQUIRED** (KI-S10-07); nothing is purged |
| KI-S9-14 | PROVIDER_SANDBOX exercised only with an in-test fake provider | No provider-sandbox evidence can be posted; evidence classes PROVIDER_REPORTED and PROVIDER_VERIFIED exist but have no source |
| KI-S9-15 | Webhook rate limits per provider unknown | No effect |
| KI-S9-16 | Public checkout disabled; no sandbox test-payment UI | No effect; synthetic ledger fixtures are posted through the staff API `POST /api/v1/admin/ledger/synthetic-fixtures` (ADMIN, TEST ledger only) |

## 3. Technical prerequisites

| Prerequisite | Status | Evidence |
|---|---|---|
| Stage 2 ledger schema design (validated draft) | SATISFIED | `design/sql/0006_ledger.sql` + harness; ported into `migrations/20261013120000_ledger.sql` |
| Money type (int64 minor units + currency; no floats) | SATISFIED | `internal/platform/money`, `docs/MONEY.md` |
| Currency registry with verified minor units | SATISFIED_WITH_CONDITIONS | `app.currencies`: USD `minor_units_verified = true`; ZWG unverified (LR-043/PCR-018) ⇒ refused by the ledger |
| Transactional outbox, audit gateways, evidence records | SATISFIED | Stage 3/5 gateways; extended for the `fundzim_ledger` role |
| Postgres-backed job queue | SATISFIED | River (ADR-024) |
| Maker-checker + step-up infrastructure | SATISFIED | Stage 4 authz policies, `ledger.adjustment.*` permissions |
| Payment facts for reconciliation | SATISFIED_WITH_CONDITIONS | `payments.PaymentFact`; only test payments exist |
| Provider settlement statements | **BLOCKED** | PCR-007/016; no provider (KI-S9-02) |
| A separate production ledger database | **NOT_VERIFIED** | Production hosting is Stage 18/20. The design (ADR-042 §1) requires PRODUCTION to be a separately designated database |

## 4. Consequence

Stage 10 builds the ledger engine, its database invariants, posting role, reversals, periods, integrity checker,
reconciliation foundation, staff UI and tests against a **TEST** ledger only, exercised with synthetic TEST_ONLY
fixtures. Production posting, settlement reconciliation and any real money remain BLOCKED.
