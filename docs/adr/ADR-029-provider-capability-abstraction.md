# ADR-029: Provider capability abstraction (`psp` module)

- **Status:** Accepted
- **Date:** 2026-10-08
- **Deciders:** Technical lead (Stage 2); pending project-owner acceptance
- **Stage:** 2 (design); sandbox Stage 8, first real adapter Stage 9

## Context

ADR-007 required a provider abstraction with capabilities and a sandbox. Stage 1 showed that providers differ
on almost every capability, including custody model, signed webhooks, refund APIs, payouts and settlement
currency, and that **no provider is selected** (provider-comparison §5). It also showed that routing must
refuse merchant settlement under Model A (ADR-013).

## Decision

1. A **`psp` module** owns the provider registry, a **versioned capability registry in the database**
   (`provider_capabilities`), adapter interfaces (`PaymentProvider`, `PayoutProvider`, `RefundProvider`,
   `ProviderWebhookVerifier`, `ProviderReconciliationSource`), the webhook inbox and provider health.
2. **Only VERIFIED capabilities are routable.** Each capability row carries its evidence status (VERIFIED /
   UNVERIFIED / NOT_SUPPORTED / REQUIRES_PROVIDER_CONFIRMATION) and a source (PCR id or document). Anything
   else is treated as absent (provider-capability-matrix rule 2).
3. **Routing guards:**
   - refuse `custody_model = MERCHANT_SETTLEMENT` for campaign donations;
   - refuse a rail whose settlement currency differs from the donation currency (ADR-018);
   - require `signed_webhooks` **or** `status_api` for any rail.
4. **Error classification** is mandatory in every adapter: `DEFINITE_FAILURE`, `RETRYABLE_NOT_SENT`,
   `INDETERMINATE` (→ `UNKNOWN`) and `CONFIGURATION_ERROR`. Timeouts after the request was sent are
   `INDETERMINATE`, never failures.
5. Provider configuration holds **secret references**, never secret values. Secrets come from the secret
   manager.
6. A **sandbox provider** implements every interface, with deterministic scenarios and selectable custody
   models. It is the only provider enabled before Stage 9, and it can never be enabled in production.

## Consequences

### Positive
- Provider choice stays open. Capability truth is data with evidence, and unsafe routing is structurally
  prevented.

### Negative / costs
- The capability matrix must be maintained per provider and re-verified before Stage 9 and Stage 20.

### Follow-up work
- Stage 8: sandbox adapter. Stage 9: first adapter after due diligence.

## Alternatives considered

| Alternative | Why rejected |
|---|---|
| Capabilities hard-coded per adapter | Unverifiable at runtime; changing a capability would need a deploy; no evidence trail |
| Interfaces inside `payments` only | `payouts` would have to import `payments` (forbidden, ADR-021) |
| A third-party payment orchestration SaaS | Adds a party to the funds flow (custody/licensing questions, LR-001) and none was found for Zimbabwean rails |

## Security implications

The webhook verifier is the T0 → T1 boundary for provider input (signature, replay window, dedupe). Providers
without signed webhooks require a status-API confirmation before any state change.

## Financial implications

Prevents Model B drift (ADR-013) and implicit FX (ADR-018) at routing time.

## Related

ADR-007, ADR-013, ADR-018, ADR-020, ADR-024; [payment-provider-interfaces.md](../architecture/payment-provider-interfaces.md);
provider-capability-matrix.md; PCR register.
