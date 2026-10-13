# Stage 9 — Payment service provider evaluation

Date: **2026-10-13**. This updates the Stage 1 comparison
([provider-comparison.md](../payments/provider-comparison.md), public documentation of 2026-10-08) with the official
developer documentation re-read in Stage 9. **It is not a selection.** The selection is recorded as
`DECISION_DEFERRED` in [provider-selection.md](provider-selection.md) and
[ADR-040](../adr/ADR-040-payment-provider-selection-deferred.md).

Rules applied: only official provider developer documentation counts as technical evidence; marketing claims are
not scored; everything else is **UNKNOWN**. Public documentation is `DOCUMENTED_BUT_UNVERIFIED` until it is
observed in the provider's sandbox ([provider-contracts.md](provider-contracts.md)).

## 1. Selection gates (from Stage 1 §2.1)

A provider can be selected only when all four gates pass with written evidence.

| Provider | G-LIC licence (PCR-001) | G-CON contract permits crowdfunding (PCR-002) | G-CUS custody compatible with Model A (PCR-003) | G-AUTH authentic status (PCR-010, PCR-017) | Sandbox access for FundZim |
|---|---|---|---|---|---|
| Paynow | UNKNOWN | UNKNOWN (third-party-beneficiary warranty risk [P12]) | UNKNOWN (settles to merchant bank [P15]) | PARTIAL: SHA-512 hash, no replay protection; poll URL; not-found semantics undocumented | **None** (requires merchant account) |
| Pesepay | UNKNOWN | PARTIAL (mentions donations [S19]; agreement not public) | UNKNOWN (trust account mentioned, no licence [S18]) | PARTIAL: callbacks unsigned; authenticated status API | **None** (requires registration) |
| Payonify | UNKNOWN | UNKNOWN ("goods and services" [Y11]) | UNKNOWN (custody contradiction, PCR-050) | PARTIAL: HMAC-SHA256 with timestamp; finality semantics undocumented | **None** (self-serve signup exists; not performed — see §4) |
| Linkwa | UNKNOWN | PARTIAL ("Donations" [L4]) | UNKNOWN | PARTIAL (summarised page only [L2]; not re-read) | None |
| Smile&Pay (ZB) | PARTIAL (bank group; product authorisation unconfirmed) | UNKNOWN | UNKNOWN | UNKNOWN | None |
| ContiPay | UNKNOWN | UNKNOWN | UNKNOWN | UNKNOWN | None |
| EcoCash Open API | UNKNOWN | UNKNOWN | UNKNOWN | UNKNOWN (not read in Stage 9) | None |

**Result: no provider passes any gate in full.** Stage 1's conclusion stands.

## 2. Changes since Stage 1 (official documentation, 2026-10-13)

| Provider | Page | Observation | Effect |
|---|---|---|---|
| Paynow | Status update, generating hash (no last-updated date; © 2026) | Status POST fields `reference`, `amount`, `paynowreference`, `pollurl`, `status`, `hash`; up to 10 resends on HTTP error; SHA-512 uppercase hex over values in message order + integration key; polling recommended for important updates | Confirms Stage 1 [P4][P6]. W4 unchanged (3) |
| Pesepay | Verifying callbacks (updated 2026-09-01) | Callbacks unsigned and unencrypted; `Authorization` header carries the integration key; verify with Check Payment Status | Confirms [S8]. The header is a shared secret sent on every callback, so it is a filter, not authentication. W4 unchanged (1) |
| Pesepay | Sandbox environment (updated 2026-09-10) | Sandbox base `api.test.sandbox.pesepay.com/payments-engine`; USD only; EcoCash, Visa, Mastercard; InnBucks, O'Mari, ZimSwitch, PayGo not in sandbox | Sandbox coverage narrower than production claims; ZWG cannot be sandbox-tested |
| Payonify | Webhooks (modified 2026-09-10) | HMAC-SHA256 `Payonify-Signature: t=…,v1=…` over `{timestamp}.{raw_body}`, 5 min tolerance; dashboard rotation (overlap undocumented); 10 retries with exponential backoff | Confirms [Y3]. W4 unchanged (4) |
| Payonify | Get started (2026-10-13) | Self-serve test keys (`pk_test_`, `sk_test_`); EcoCash and OneMoney test numbers; cards only via hosted Checkout; Zimswitch "Coming Soon"; live use requires business verification | Lowest barrier to a sandbox, but sandbox access ≠ contract fit or custody fit |

Scores in Stage 1 §3 are therefore unchanged; no criterion moved on verified evidence.

## 3. Method and rail coverage (documentation level)

See [provider-capabilities.md](provider-capabilities.md) for the per-method mapping. Summary: EcoCash is documented by
Paynow, Pesepay and Payonify; OneMoney by Paynow and Payonify; InnBucks, O'Mari and ZimSwitch by Paynow and Pesepay
(not in Pesepay's sandbox); ZIPIT by **none** as a direct API; international cards are stated by Paynow and Linkwa
only (PCR-005). None is confirmed for production.

## 4. Why no sandbox account was opened

Opening a provider account binds FundZim to the provider's terms and requires the owner's identity and business
details. That is an owner decision and a contract act, not an engineering step. Claude Code did not sign up with any
provider and holds no provider credentials. When the owner supplies sandbox credentials, they go into the secret
manager ([secret-management.md](secret-management.md)), never into the repository.

## 5. Recommended due-diligence order (unchanged, not a selection)

The Stage 1 order for **due diligence** stands, with one practical note: Payonify and Pesepay publish self-serve or
documented sandboxes, so they are the fastest to sandbox-certify **if** the owner chooses them for due diligence.
Paynow has the broadest documented rail coverage. The gating questions (PCR-001/002/003) should be sent to all of
them in writing first; sandbox engineering without G-CON and G-CUS would build an integration that may never be
usable.
