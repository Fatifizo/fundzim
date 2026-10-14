# Stage 11 prerequisite assessment

Date: 2026-10-14. Branch `stage-11/payouts`, created from `stage-10/ledger` at `dbaf500` (CI run 38441433914: all 8 jobs
green). Inputs read: `CLAUDE.md`, `README.md`, `docs/PRODUCT.md`, `docs/ARCHITECTURE.md`, `docs/SECURITY.md`,
`docs/ROADMAP.md`, `docs/stage-handover/STAGE-10-TO-STAGE-11.md`, the Stage 1 payout specifications
([payout-lifecycle.md](../payments/payout-lifecycle.md), [payout-eligibility-and-controls.md](../payments/payout-eligibility-and-controls.md)),
ADR-016, ADR-017, ADR-020, ADR-040, ADR-042, the Stage 2 draft `design/sql/0016_payouts.sql`, the legal register
[open-legal-questions.md](../compliance/open-legal-questions.md), [../stage-9/known-issues.md](../stage-9/known-issues.md),
[../stage-10/known-issues.md](../stage-10/known-issues.md), and the code of `internal/ledger`, `internal/payouts`
(destinations), `internal/psp`, `internal/payments`, `internal/campaigns`, `internal/kyc`, `internal/beneficiaries`,
`internal/compliance`, `internal/risk`, RBAC and step-up (`internal/auth`), the outbox and CI.

Classification: **SATISFIED**, **SATISFIED_WITH_CONDITIONS**, **BLOCKED**, **NOT_VERIFIED**. No acceptance, legal opinion,
provider capability or settlement evidence was fabricated.

## 1. Governance

| Prerequisite | Status | Evidence / condition |
|---|---|---|
| Formal acceptance of Stages 1–10 | **NOT_VERIFIED** | [stage-acceptance-register.md](../governance/stage-acceptance-register.md): every row `NOT_RECORDED`. Stage 9 remains **PARTIAL**; Stage 10 remains **PASS WITH CONDITIONS** (recommendation only). Stage 11 is isolated, non-production engineering |
| Owner request to start Stage 11 | SATISFIED | The owner supplied the Stage 11 master prompt |
| Production payout execution | **BLOCKED** (by design) | ADR-043 §1: TEST payouts only; constant, kill switch, configuration and CHECKs |
| Real payout submission | **BLOCKED** | No payout provider (ADR-040) |
| Production readiness | **BLOCKED** | Governance, legal, provider, accounting and settlement gates open |

## 2. Legal dependencies (exact register text abridged only by "…"; the register is authoritative)

| Item | Question (register) | Effect on Stage 11 |
|---|---|---|
| LR-020 Chargebacks & dispute liability | "Who bears chargeback loss after payout (FundZim, PSP, campaign owner)? Reserve/rolling-hold requirements imposed by PSPs/schemes; recovery rights against campaign owners." | No chargeback reserve is invented; EC-14 has no dispute source (NOT_APPLICABLE); open blocker for production |
| LR-024 Payout to third parties & nominee accounts | "May payouts go to an account not in the verified owner's name (e.g. hospital, school, funeral parlour, family member)? Required evidence?" | Payouts go only to a verified destination of the campaign owner paying the owner (SELF campaigns) or the campaign's own verified beneficiary; institution payees and overrides are not implemented |
| LR-030 Payout approval thresholds & auto-approval limits | "Above what amounts (per payout, per period, per currency) must payouts be manually approved, held or subjected to enhanced checks? Are there regulatory or PSP-imposed limits on auto-approval?" | No threshold is set: unconfigured limits fail closed (REVIEW, DUAL approval, no AUTO) |
| LR-001 / LR-003 Custody, stored value | open | Ledger balances are accounting records; no wallet; nothing withdrawable in production |
| LR-004 PSP licensing & role ("instructing payouts") | open | No real payout instruction can be sent |
| LR-005 Exchange control | open | USD only; no cross-border payout logic |
| LR-007 AML thresholds & KYC tiers | open | PAYOUT_VERIFIED required (EC-03); no thresholds invented |
| LR-009 Sanctions & PEP screening | open; no screening provider (KI-S5-04) | EC-15 records NOT_APPLICABLE for TEST payouts and FAILS any other payout |
| LR-012 Retention | open | Payout records are append-only; no purge |
| LR-019 Cancelled / frozen campaign funds | open | EC-01 refuses payouts for any campaign not ACTIVE or COMPLETED; no disposition flow |
| LR-084 Payout hold duration and communication | open | Owners see only coarse categories (no tipping off); no hold timers |

## 3. Provider dependencies (ADR-040; KI-S9-01..16 as defined in [../stage-9/known-issues.md](../stage-9/known-issues.md))

| Item | Definition (abridged) | Effect on Stage 11 |
|---|---|---|
| KI-S9-01 | Formal acceptance of Stages 1–8 (and 9) not recorded | Carried (KI-S11-01) |
| KI-S9-02 | No payment provider selected; no merchant agreement, sandbox credentials or PCR answers | **No payout provider**: payout API, rails, limits, fees, settlement times, cancellation and refund capabilities are unknown and not invented; the simulator is the only payout implementation |
| KI-S9-03 | Production payment execution BLOCKED | No real funds exist to pay out |
| KI-S9-04 | Secret manager operation not done | No payout credentials exist |
| KI-S9-05 | OpenBao dev mode | Simulator webhook secrets come from it (development) |
| KI-S9-06 | Unsigned-callback / form-hash verification not implemented | Payout webhooks are verified only by the simulator's scheme |
| KI-S9-07 | No refund sandbox; refunds stop at APPROVED | Open refund requests are subtracted as exposure (EC-09) |
| KI-S9-08 | ZIPIT/ZimSwitch unconfirmed | Not offered as payout rails by the simulator |
| KI-S9-09 | International cards not distinguished | No effect |
| KI-S9-10 | Simple health thresholds | Payout submission defers on a declared outage or open circuit (EC-18) |
| KI-S9-11 | Startup audit skipped when DB unreachable | No effect |
| KI-S9-12 | Health computed per request | Contributes to eligibility latency ([testing.md](testing.md) §6) |
| KI-S9-13 | Provider evidence log retention | Same LR-012 dependency |
| KI-S9-14 | PROVIDER_SANDBOX only with an in-test fake | Payouts refuse PROVIDER_SANDBOX entirely |
| KI-S9-15 | Webhook rate limits per provider unknown | Same for payout webhooks |
| KI-S9-16 | Public checkout disabled | No donations ⇒ only synthetic TEST funds |

## 4. Technical prerequisites

| Prerequisite | Status | Evidence |
|---|---|---|
| Ledger: payout rules, locking, payable/pending/in-transit accounts | SATISFIED | Stage 10; `PAYOUT_*` rules version 1, unused until now |
| Settlement evidence model | **BLOCKED** | `SETTLEMENT_MATCHED` exists; no statements (PCR-007/016) |
| Payout destinations, verification, cooling-off | SATISFIED | Stage 5 `internal/payouts` (destinations) |
| Beneficiaries, KYC levels, compliance restrictions | SATISFIED | Public service interfaces |
| Campaign states and owner access | SATISFIED | `campaigns.OwnerAccess`, `Restricted` |
| Provider registry, webhook inbox, simulator | SATISFIED | Stage 8/9; inbox already routes PAYOUT events |
| RBAC (`payout.approve`, `payout.reject`, `payout.hold`), step-up | SATISFIED | Stage 4 |
| Limits model | SATISFIED_WITH_CONDITIONS | `risk.limits`; no payout limit values configured (LR-030) |
| Hold registry (`risk.holds`) | **NOT PRESENT** | Not built in earlier stages; EC-12 uses campaign, ledger-account and destination states (ADR-043 §8) |
| Sanctions screening | **BLOCKED** | KI-S5-04 |
