# Stage 2 Prerequisite Assessment

**Date:** 2026-10-08. **Assessor:** Stage 2 architecture session (Claude Code).
**Branch:** `stage-2/system-architecture`, created from `stage-1/regulatory-architecture` at `aa9f9b3`.

Classification: **SATISFIED** · **SATISFIED_WITH_CONDITIONS** · **BLOCKED** · **NOT_VERIFIED**.
"BLOCKED" below always means "blocks a later implementation or go-live step". Nothing here blocks Stage 2
*design* work.

## 1. Repository state

| Item | Finding | Classification |
|---|---|---|
| Working tree at start | Clean. `main` = Stage 0 (`a552de2`); `stage-1/regulatory-architecture` = Stage 1 (`aa9f9b3`), pushed to `origin`, **not merged** into `main` | SATISFIED_WITH_CONDITIONS: Stage 2 branches from the Stage 1 branch. Merging order is for the project owner to decide. |
| Stage 0 deliverables (CLAUDE.md, PRODUCT, ARCHITECTURE, SECURITY, PAYMENTS, LEDGER, COMPLIANCE, ROADMAP, ADR-001 – 012) | Present and read | SATISFIED |
| Stage 1 deliverables (42 documents, ADR-013 – 020, Stage 1 → 2 handover) | Present and read; the Stage 1 completion report was produced in-session | SATISFIED |

## 2. Stage 1 acceptance

| Item | Finding | Classification |
|---|---|---|
| Stage 1 completion report | Produced on 2026-10-08 with recommendation **PASS WITH CONDITIONS** | SATISFIED |
| Stage 1 acceptance evidence | An acceptance checklist was provided. Its sign-off section (approver, date, PASS/conditions) is **blank**, and no acceptance record exists in the repository. The project owner then issued the Stage 2 prompt. That is an instruction to proceed, **not** a recorded acceptance. | **NOT_VERIFIED.** Stage 2 does not assume acceptance. It records this gap, and the Stage 2 report repeats it. |
| Stage 1 acceptance conditions (Payonify/Linkwa identity, PD-20 vs PD-32 conflict) | Not yet answered | SATISFIED_WITH_CONDITIONS: neither affects schema shape; both are kept configurable. |

## 3. Architectural prerequisites

| Prerequisite | Source | Classification | How Stage 2 handles it |
|---|---|---|---|
| Operating model (Model A, provisional) | ADR-013 | SATISFIED_WITH_CONDITIONS | Designed for Model A with the Model C variant (`campaigns.settlement_model`). Routing refuses `MERCHANT_SETTLEMENT` custody. |
| Chart of accounts and money states | ADR-014, settlement-and-custody-model | SATISFIED | Used as-is in the ledger schema |
| Payment / payout state models | ADR-020 | SATISFIED | Used as-is. The brief's `PROCESSING` payment state is mapped, not added (baseline §8). |
| KYC/KYB, beneficiary models | ADR-015, ADR-016 | SATISFIED | Turned into the `kyc`, `beneficiaries` and `payouts` schemas |
| Payout eligibility, maker-checker, SoD | ADR-017, operational-controls | SATISFIED | Turned into DB constraints and triggers |
| Currency isolation | ADR-018 | SATISFIED | Every money pair has a currency; per-currency accounts; no FX tables |
| Evidence model | ADR-019 | SATISFIED | `audit.evidence_records` |
| Module ownership questions (holds, evidence, limits, payout destinations) | Stage 1 handover §3 | SATISFIED (resolved) | [design-baseline.md §6](design-baseline.md), ADR-021 |
| PSP selection | provider-comparison §5 (all PENDING) | **BLOCKED** for any provider-specific adapter (Stage 9) | Provider-neutral interfaces and a sandbox contract only |
| P0 legal items (LR register §5) | open-legal-questions | **BLOCKED** for live operation (Stage 20) and for ZiG / international rails / minor-and-medical campaigns (feature gates) | Every LR-dependent value is configuration with the LR id; features gated by flags that default to off |
| Retention periods (LR-012) | open-legal-questions | **BLOCKED** for retention *values* | `retention_class` columns and a retention-policy table; no hard-coded periods |
| Business decisions PD-01 – PD-39 | PRODUCT §13, LR register §7 | SATISFIED_WITH_CONDITIONS | Schema designed to support each option ([Stage 1 handover §12](../stage-handover/STAGE-1-TO-STAGE-2.md)) |
| THREAT-MODEL update for Stage 1 boundaries | Stage 1 handover §14 | Open: Stage 2 task | Done in [docs/architecture/trust-boundaries.md](../architecture/trust-boundaries.md) plus a THREAT-MODEL addendum |

## 4. Tooling available to this session

| Tool | Status | Effect |
|---|---|---|
| Go toolchain | **Not installed** | Go interface sketches cannot be compiled in Stage 2. They are marked as sketches; compile-checking starts in Stage 3. NOT_VERIFIED. |
| Docker | Installed, but this user has **no permission** on the Docker socket | Real PostgreSQL could not be started. SQL drafts are validated in **PGlite** (PostgreSQL 18.3 compiled to WASM, in-memory). PGlite cannot test multi-session concurrency. |
| Node 24 + npm | Available | Used for PGlite and OpenAPI validation (Redocly CLI) |
| `make` | Not installed (Stage 0 known issue) | — |

## 5. Conclusion

Stage 2 design work may proceed. It does **not** treat Stage 1 as accepted, any legal question as answered, or
any provider as selected. Implementation steps that depend on those remain blocked exactly as listed above.
