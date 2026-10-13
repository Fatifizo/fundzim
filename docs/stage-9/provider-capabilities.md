# Stage 9 — Payment method capability mapping and rail assessments

Date: 2026-10-13. Capability evidence lives in `app.provider_capabilities` (versioned, append-only; `routable` only
when `evidence_status = VERIFIED`, custody `PSP_POOL`/`SPLIT_DIRECT`, and a signed webhook or status API). **No real
provider row exists** ([ADR-040](../adr/ADR-040-payment-provider-selection-deferred.md)); the only routable rows are
the simulator's, which describe the simulator, not any rail.

## 1. Method mapping

FundZim method codes are provider-neutral (`psp.Method*`, DB CHECK). The brief's methods map as follows.

| Brief method | FundZim code / category | Documented by (official docs, not confirmed) | Sandbox access (FundZim) | Confirmation | Stage 9 state |
|---|---|---|---|---|---|
| ECOCASH | `ECOCASH` / MOBILE_MONEY | Paynow [P2], Pesepay (incl. sandbox `PZW211`), Payonify (test numbers) | None | PCR-004 UNCONFIRMED | Simulator only |
| ONEMONEY | `ONEMONEY` / MOBILE_MONEY | Paynow [P2], Payonify | None | PCR-004 UNCONFIRMED | Simulator only |
| INNBUCKS | `INNBUCKS` / MOBILE_MONEY | Paynow [P2]; Pesepay (not in sandbox) | None | UNCONFIRMED | Not routable (simulator row UNVERIFIED) |
| OMARI | `OMARI` / MOBILE_MONEY | Paynow (two-step OTP) [P2]; Pesepay (not in sandbox) | None | UNCONFIRMED | Not routable |
| ZIMSWITCH | `ZIMSWITCH` / ACCOUNT_TO_ACCOUNT | Paynow [P2]; Pesepay (not in sandbox); Payonify "Coming Soon" | None | UNCONFIRMED | **PROVIDER_CONFIRMATION_REQUIRED** (§5) |
| ZIPIT | — (no code) | **No provider documents a ZIPIT API** | None | UNCONFIRMED | **PROVIDER_CONFIRMATION_REQUIRED** (§5); no method code added |
| LOCAL_CARD | `CARD` / CARD | Paynow (Visa/MC), Pesepay (sandbox `PZW204`/`PZW205`), Payonify (hosted Checkout only) | None | UNCONFIRMED | Simulator only (redirect flow) |
| INTERNATIONAL_CARD | `CARD` (issuer origin is not a method) | Paynow and Linkwa state foreign cards [P11][L4] | None | PCR-005 UNCONFIRMED; LR-005/LR-044 OPEN | **Not supported**: no capability distinguishes it; blocked by exchange-control and cross-border questions |
| BANK_TRANSFER | `BANK_TRANSFER` / BANK_TRANSFER | No provider API for confirmed inbound transfers documented | None | UNCONFIRMED | Simulator row `MERCHANT_SETTLEMENT` (never routable) (§4) |

## 2. Mobile money

- **Flow** (all documented providers): the donor gives a wallet number; the provider pushes a prompt to the
  handset; the donor approves on the handset. FundZim's model: `REQUIRES_ACTION` with `AWAIT_HANDSET_APPROVAL`, then
  an authoritative status (webhook or status query). **FundZim never collects a mobile-money PIN**; no API, form or
  field accepts one (Stage 8 security review, unchanged).
- **Timeouts**: a timeout after the request was sent is `UNKNOWN` (`PROVIDER_TIMEOUT`, status query only). Never a
  failure, never a blind retry (PCR-009/010 open).
- **Partial collection**: Stage 1 noted Pesepay collects EcoCash amounts above a ceiling in parts [S3]. The payment
  stays one intent; partial states are non-terminal. Not exercised (no access).

## 3. Cards

- FundZim uses **hosted checkout / redirect** only. The donation UI and API never accept PAN, CVV, magnetic-stripe or
  other sensitive authentication data; there are no such fields. This keeps FundZim out of handling card data, but
  **no PCI DSS assessment has been made** and none is claimed.
- Redirect hosts are allow-listed per adapter (`RedirectHosts`; conformance scenario `redirect_hosts_well_formed`).
- 3-D Secure, chargebacks and liability: PCR-005, LR-020 OPEN. Disputes are not modelled (Stage 8 deferral).

## 4. Bank transfer

- No provider documents an API that confirms an inbound bank transfer to FundZim. **A donor-uploaded receipt is
  never a confirmation**: there is no endpoint that marks a payment succeeded from a receipt or by staff.
- The simulator's `BANK_TRANSFER` row is `MERCHANT_SETTLEMENT` and therefore never routable (DB generated column).
- A real bank rail needs a provider or bank statement source (PCR-007, PCR-016; reconciliation Stage 17).

## 5. ZIPIT and ZimSwitch

FundZim has no direct access to ZimSwitch or the ZIPIT network and must not assume any. Paynow and Pesepay list a
"ZimSwitch" method in documentation; none documents ZIPIT as an API. **Integration boundary** (documented, not
built):

- a ZIPIT or ZimSwitch payment would be a provider capability row for `ZIMSWITCH` (ACCOUNT_TO_ACCOUNT) — or a new
  method code for ZIPIT added by migration and in `psp.Method*` — with evidence `VERIFIED` only after written
  provider confirmation and sandbox observation;
- status must come from the provider (webhook or status API); instant-payment finality and reversal rules are
  PCR-010/PCR-011;
- **Status: PROVIDER_CONFIRMATION_REQUIRED. The rail is not operational and is not presented as available**
  (`GET /payment-methods/available` lists ZIMSWITCH as `NOT_AVAILABLE`).

## 6. Currency rules

| Rule | Enforcement |
|---|---|
| USD only for payments | Campaign goal currency USD; `app.currencies.minor_units_verified` true for USD only |
| ZWG restricted | `minor_units_verified = false` (LR-043, PCR-018); donation intents in ZWG are refused (`CURRENCY_NOT_AVAILABLE`); Pesepay's sandbox is USD-only, so ZWG cannot be sandbox-tested anyway |
| No guessed precision | Minor units come from the currency registry; adapters convert only to and from documented provider formats (none implemented) |
| No automatic FX | `ck_provider_capabilities_same_currency_settlement`: a rail is recorded only if it settles in the donation currency |
| No mixed balances | No balances exist (Stage 10 ledger); evidence rows record reported amount **and** currency; a currency mismatch is an anomaly (reconciliation `CURRENCY_MISMATCH`) |

## 7. Recording a confirmed capability (procedure)

1. Written provider answer (PCR-004 and the method-specific PCRs) and sandbox observation of the method.
2. Migration inserting a new `provider_capabilities` version: `evidence_status = 'VERIFIED'`, `evidence_source`
   naming the document and the sandbox certification run; flags true only where confirmed.
3. The adapter's `Limits` returns the provider's documented limits for exactly that method and currency.
4. Sandbox certification of the provider updated ([sandbox-certification.md](sandbox-certification.md)).
