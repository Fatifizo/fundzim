# ADR-040: Payment provider selection — DECISION_DEFERRED

- **Status:** Proposed (Stage 9, 2026-10-13) — **DECISION_DEFERRED**
- **Deciders:** Project owner (selection authority); technical lead (recording)
- **Related:** ADR-013 (operating model), ADR-029 (provider capability abstraction), ADR-039 (payment abstraction),
  ADR-041 (secret management); [provider-comparison.md](../payments/provider-comparison.md);
  [provider-evaluation.md](../stage-9/provider-evaluation.md); [provider-selection.md](../stage-9/provider-selection.md);
  [provider-confirmation-register.md](../stage-9/provider-confirmation-register.md); [legal-review.md](../stage-9/legal-review.md)

## Context

Stage 9 was asked to integrate Zimbabwean payment providers where official documentation, provider authorisation and
sandbox access exist, and to record a provider selection. On 2026-10-13:

- no provider passes any of the four selection gates (G-LIC, G-CON, G-CUS, G-AUTH) with written evidence;
- every PCR item that gates selection (PCR-001, PCR-002, PCR-003) is UNCONFIRMED;
- the operating-model legal questions (LR-001, LR-003, LR-004 and LR-036/037/039/041) are OPEN;
- the owner has supplied no merchant agreement, no provider sandbox credentials and no selection decision.

## Decision

1. **No payment provider is selected.** The selection is `DECISION_DEFERRED`. No `payment_providers` row has
   `selection_status = 'SELECTED'`, and no real provider row is added to the registry.
2. **No real provider adapter is implemented.** Implementing one from public documentation alone, without sandbox
   access, would encode unverified request fields, response fields and status mappings (`DOCUMENTED_BUT_UNVERIFIED`)
   as if they were contracts. Stage 9 delivers the provider-neutral preparation instead (execution environments,
   secret management, conformance suite, error categories, health, evidence capture) so that an adapter can be added
   and certified once access exists.
3. **The sandbox simulator remains the only executing provider**, in `LOCAL_SIMULATOR` and only outside production.
4. Due-diligence order (not a selection) stays as in Stage 1, with the practical notes in
   [provider-evaluation.md](../stage-9/provider-evaluation.md) §5.

## What reverses this decision

A new ADR that supersedes this one, recording for the chosen provider: the owner's decision and date; written
answers to PCR-001/002/003 reviewed by counsel; counsel's view on LR-001/003/004; the merchant agreement reference;
sandbox credentials stored in the secret manager (ADR-041); and a sandbox certification
([sandbox-certification.md](../stage-9/sandbox-certification.md)) with every scenario PASS. The registry row is then
added by migration with `selection_status = 'SELECTED'` and `selection_adr` set to that ADR.

## Consequences

- Provider integration and provider sandbox certification are **BLOCKED** in Stage 9, not PASS.
- Public checkout stays disabled; `GET /api/v1/payment-methods/available` reports no real method.
- Stage 10 (ledger) can proceed against the simulator's events, which carry `simulated = true` and are never posted.
