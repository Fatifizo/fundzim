# ADR-019: Regulatory evidence management

- **Status:** Accepted
- **Date:** 2026-10-08
- **Deciders:** Technical lead; compliance officer
- **Stage:** 1 (design). Implemented from Stage 3 (audit), Stage 5 (KYC evidence) and Stage 14 (case tooling).

## Context

Regulators, providers, auditors and courts may ask FundZim to prove what it knew and did: why a campaign was
approved, how an owner was verified, why a payout was released or held, how a reconciliation difference was
resolved. Stage 0 defined an append-only `audit_events` trail ([AUDIT.md](../AUDIT.md)) but deliberately
kept documents and secrets out of it. Stage 1 also produced a regulatory requirements register whose
readiness depends on evidence.

Constraints:

- Evidence often contains C3 RESTRICTED material (identity documents, health information about
  beneficiaries, minors' data) that must not be copied into general audit records or logs.
- Retention periods are not settled (LR-012); some instruments researched specify record periods, but which
  apply to FundZim is a legal question.
- Evidence must be tamper-evident and preservable under legal hold.

## Decision

1. **Two records, two jobs.** `audit_events` answer who did what, when, to what, with what outcome.
   `evidence_records` are pointers to the material that justified or resulted from an action. Audit events
   reference evidence record ids; they never embed documents, document numbers, health details or secrets.
2. **Evidence record schema** (authoritative definition in
   [audit-evidence-model.md](../compliance/audit-evidence-model.md) §2): id, evidence_type, subject refs,
   related refs (case, payment, payout, provider reference), collected_at, collected_by, source,
   `storage_ref` (private bucket key, never public), `content_sha256`, classification (C2/C3),
   `retention_class`, `legal_hold`, `supersedes_id`, `audit_event_id`.
3. **Immutability.** Evidence rows are append-only; the only mutable column is `legal_hold`, changed solely
   via append-only `evidence_holds` rows with maker-checker. Objects are write-once and verified against their
   hash on read; a mismatch is a SEV1 incident.
4. **Retention by class**, with periods pending LR-012. Deletion is a recorded event; legal hold blocks it.
5. **Regulatory requirements register.** Each requirement (REQ-xxx) in
   [regulatory-requirements-register.md](../compliance/regulatory-requirements-register.md) names the
   control and the evidence that demonstrates it; the readiness checklist
   ([regulatory-readiness-checklist.md](../compliance/regulatory-readiness-checklist.md)) tracks status
   (DESIGN_COMPLETE, IMPLEMENTED, TESTED, EXTERNALLY_VERIFIED, APPROVED). Design completion is never
   presented as approval.
6. Evidence export for regulators or law enforcement is a maker-checker action with a manifest hash and its
   own audit event (LR-032).

## Consequences

### Positive
- Decisions can be reconstructed without exposing restricted material in audit logs.
- Readiness claims are tied to concrete evidence, which supports future external review.

### Negative / costs
- Extra write path and storage; hash verification on read.
- Until LR-012 is answered, retention classes have no periods, so purging is disabled (storage grows).

### Follow-up work
- Stage 2: tables `evidence_records`, `evidence_holds`; private bucket prefixes per evidence type.
- Stage 18: object lock / versioning on the compliance prefix and external hash anchoring.

## Alternatives considered

| Alternative | Why rejected |
|---|---|
| Store evidence inside audit event metadata | Pulls C3 data into a broadly read trail; violates AUDIT.md rules. |
| Evidence only as files without DB records | No integrity hashes, no retention or hold control, no linkage to decisions. |

## Security implications
Evidence objects inherit private-bucket controls ([ADR-009](ADR-009-kyc-storage-separation.md)); access is
case-bound, justified and audited; storage keys are random, never derived from names or ID numbers.

## Financial implications
Ledger adjustments, write-offs and reconciliation resolutions must link evidence records; a manual financial
change without evidence cannot be approved.

## Related
[audit-evidence-model.md](../compliance/audit-evidence-model.md), [AUDIT.md](../AUDIT.md),
[regulatory-requirements-register.md](../compliance/regulatory-requirements-register.md),
[regulatory-readiness-checklist.md](../compliance/regulatory-readiness-checklist.md), ADR-009, ADR-017.
LEGAL_REVIEW_REQUIRED: LR-012, LR-032, LR-034.
