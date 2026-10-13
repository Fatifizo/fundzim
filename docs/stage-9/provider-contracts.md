# Stage 9 — Provider contract accuracy

Every statement FundZim makes about a provider's API carries one of four bases. Adapters declare theirs through
`psp.ContractDescriber` (shown on `/admin/payments/providers/{id}`); an adapter that declares none is treated as
`DOCUMENTED_BUT_UNVERIFIED`, never better.

| Basis | Meaning | May drive routing? |
|---|---|---|
| `OFFICIAL_PROVIDER_CONTRACT` | Written in the provider's signed agreement or official technical specification supplied to FundZim | Only together with sandbox observation |
| `PROVIDER_SANDBOX_OBSERVED` | Exercised against the provider's sandbox and passed the conformance suite | Yes, in `PROVIDER_SANDBOX` |
| `DOCUMENTED_BUT_UNVERIFIED` | Public developer documentation only | No |
| `SIMULATED` | FundZim's own simulator | Only in `LOCAL_SIMULATOR` |

## Register (2026-10-13)

| Provider | Item | Content (summary of official docs) | Basis |
|---|---|---|---|
| FundZim simulator | Entire API, webhook scheme `FundZim-Sandbox-Signature` | [../stage-8/provider-interface.md §6](../stage-8/provider-interface.md) | **SIMULATED** |
| Paynow | Status update to `resulturl` (fields, SHA-512 hash, resend ×10, poll URL) | [provider-confirmation-register.md](provider-confirmation-register.md) PCR-017 | DOCUMENTED_BUT_UNVERIFIED |
| Pesepay | Unsigned callbacks; verify via Check Payment Status; sandbox base URL and methods | PCR-017, PCR-004 | DOCUMENTED_BUT_UNVERIFIED |
| Payonify | `Payonify-Signature` HMAC-SHA256 over `{t}.{body}`, 5 min tolerance, 10 retries; test keys | PCR-017 | DOCUMENTED_BUT_UNVERIFIED |
| Linkwa, Smile&Pay, ContiPay, EcoCash Open API | Not re-read in Stage 9 | Stage 1 matrix | DOCUMENTED_BUT_UNVERIFIED or UNKNOWN |

**No item is `OFFICIAL_PROVIDER_CONTRACT` or `PROVIDER_SANDBOX_OBSERVED`.** No endpoint, request field, response
field, authentication scheme, signature, error code, currency, refund behaviour or settlement guarantee of a real
provider is encoded in FundZim code. The in-test fake provider in `tests/integration/payments_stage9_test.go` is a
test double for isolation tests; it describes no provider.
