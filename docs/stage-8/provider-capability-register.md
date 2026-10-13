# Stage 8 — Provider capability register

> **No production payment provider is confirmed.** The rails below are **target capabilities, not integrations**.
> Every value that has not been confirmed in writing by a selected provider is `UNKNOWN`. Public documentation
> collected in Stage 1 ([provider-capability-matrix.md](../payments/provider-capability-matrix.md), dated
> 2026-10-08) is evidence about *candidate* providers only. It is not repeated here as a capability, because it is
> not contractually confirmed (matrix rule 1) and no candidate is selected
> ([provider-comparison.md](../payments/provider-comparison.md): all `PENDING`).

The machine-readable registry is `app.payment_providers` + `app.provider_capabilities` (append-only, versioned). In
Stage 8 it contains **only the sandbox simulator** (`code = 'sandbox'`, `is_sandbox = true`, never production). No
real provider row is created, because a row would invite routing on unconfirmed facts. A provider is added in Stage 9
by migration, after an ADR selects it, with each capability's evidence (`VERIFIED` + source, or
`REQUIRES_PROVIDER_CONFIRMATION` + `PCR-xxx`).

## 1. Target rails

Columns follow the Stage 8 brief §5. "Method code" is the registry's `method` value; "Category" is the
provider-neutral method category (ADR-039 §5).

| Rail | Method code | Category | Provider | Supported currency | Country | Collection | Refund | Webhook | Settlement model | Authentication | Required credentials | Sandbox | Regulatory dependencies | Confirmation status |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| EcoCash | `ECOCASH` | MOBILE_MONEY | UNKNOWN (no provider selected) | UNKNOWN (USD and/or ZWG; PCR-018) | ZW | UNKNOWN | UNKNOWN (PCR-006) | UNKNOWN (PCR-017) | UNKNOWN (PCR-003) | UNKNOWN | UNKNOWN | UNKNOWN | LR-001, LR-004, LR-043, LR-075 | NOT_CONFIRMED |
| OneMoney | `ONEMONEY` | MOBILE_MONEY | UNKNOWN | UNKNOWN (PCR-018) | ZW | UNKNOWN | UNKNOWN (PCR-006) | UNKNOWN (PCR-017) | UNKNOWN (PCR-003) | UNKNOWN | UNKNOWN | UNKNOWN | LR-001, LR-004, LR-043, LR-075 | NOT_CONFIRMED |
| InnBucks | `INNBUCKS` | MOBILE_MONEY | UNKNOWN | UNKNOWN | ZW | UNKNOWN | UNKNOWN | UNKNOWN | UNKNOWN | UNKNOWN | UNKNOWN | UNKNOWN | LR-001, LR-003, LR-004 | NOT_CONFIRMED |
| O'Mari | `OMARI` | MOBILE_MONEY | UNKNOWN | UNKNOWN | ZW | UNKNOWN | UNKNOWN | UNKNOWN | UNKNOWN | UNKNOWN | UNKNOWN | UNKNOWN | LR-001, LR-004 | NOT_CONFIRMED |
| ZIPIT | none yet (needs a method code by migration when a provider documents it) | ACCOUNT_TO_ACCOUNT | UNKNOWN | UNKNOWN | ZW | UNKNOWN | UNKNOWN | UNKNOWN | UNKNOWN | UNKNOWN | UNKNOWN | UNKNOWN | LR-001, LR-004 | NOT_CONFIRMED |
| ZimSwitch | `ZIMSWITCH` | ACCOUNT_TO_ACCOUNT | UNKNOWN | UNKNOWN | ZW | UNKNOWN | UNKNOWN | UNKNOWN | UNKNOWN | UNKNOWN | UNKNOWN | UNKNOWN | LR-001, LR-004 | NOT_CONFIRMED |
| Bank transfer | `BANK_TRANSFER` | BANK_TRANSFER | UNKNOWN | UNKNOWN | ZW (cross-border: LR-005/044) | UNKNOWN | UNKNOWN | UNKNOWN | UNKNOWN | UNKNOWN | UNKNOWN | UNKNOWN | LR-001, LR-005, LR-044 | NOT_CONFIRMED |
| International cards | `CARD` | CARD | UNKNOWN | UNKNOWN | International (LR-005/044) | UNKNOWN | UNKNOWN | UNKNOWN | UNKNOWN | UNKNOWN (hosted only, PCR-023) | UNKNOWN | UNKNOWN | LR-005, LR-044, LR-020, PCR-023, PCR-025 | NOT_CONFIRMED |

No API endpoint, credential format or capability of any real provider is implemented or assumed.

## 2. The sandbox simulator (the only registered provider)

`sandbox` is a FundZim-built simulator. Its capabilities are **sandbox contract facts** (`evidence_source =
'sandbox contract: docs/stage-8/provider-interface.md §6'`), not statements about any real rail. Its method codes
reuse the rail codes only so that routing and UI can be exercised; a sandbox `ECOCASH` row says nothing about
EcoCash.

| Capability row | Method | Currency | Custody model | Evidence | Routable | Purpose |
|---|---|---|---|---|---|---|
| sandbox / ECOCASH / USD | ECOCASH | USD | PSP_POOL | VERIFIED (sandbox contract) | yes | Default mobile-money path |
| sandbox / ONEMONEY / USD | ONEMONEY | USD | PSP_POOL | VERIFIED (sandbox contract) | yes | Second mobile-money method |
| sandbox / CARD / USD | CARD | USD | PSP_POOL | VERIFIED (sandbox contract) | yes | Hosted-redirect path (no card data) |
| sandbox / BANK_TRANSFER / USD | BANK_TRANSFER | USD | MERCHANT_SETTLEMENT | VERIFIED (sandbox contract) | **no** (Model B) | Proves routing refuses merchant settlement |
| sandbox / INNBUCKS / USD | INNBUCKS | USD | PSP_POOL | UNVERIFIED | **no** | Proves routing refuses unverified evidence |

Flags of the routable rows: `signed_webhooks`, `status_api`, `supports_cancel`, `refund_api` (refund execution is
still disabled), `idempotent_create` (ECOCASH only, to exercise the re-create-with-same-reference branch),
`documented_expiry` 900 s (CARD only).
