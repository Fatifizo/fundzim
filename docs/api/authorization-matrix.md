# FundZim API Authorization Matrix

**Status:** Stage 2 design. Implemented in Stage 4 (authorization framework) and Stage 14 (admin portal).
**Companion documents:** [api-design.md](api-design.md) (endpoint catalogue; endpoint IDs used below) ·
[error-model.md](error-model.md) · [`api/openapi/fundzim-v1.yaml`](../../api/openapi/fundzim-v1.yaml)
(`x-fundzim-permission`, `x-fundzim-step-up`, `x-fundzim-maker-checker` on each operation).

**Sources:** [SECURITY.md §5](../SECURITY.md) (model, indicative matrix, SoD list),
[operational-controls.md §1–§3](../compliance/operational-controls.md) (functions, SoD matrix, maker-checker),
[identity-data-protection.md §4](../security/identity-data-protection.md) (C3 access),
[refund-and-dispute-architecture.md §6–§7](../payments/refund-and-dispute-architecture.md),
[payout-eligibility-and-controls.md §5–§6](../payments/payout-eligibility-and-controls.md) (holds, approval tiers),
[compliance-case-management.md §6](../compliance/compliance-case-management.md) (tipping-off),
[PRODUCT.md §4, §8.1](../PRODUCT.md), [design-baseline.md](../stage-2/design-baseline.md). Where Stage 1 is more
specific than Stage 0, Stage 1 wins (CLAUDE.md). Related: [authentication-authorization.md](../security/authentication-authorization.md).

---

## 1. Actors

| Column | Actor | Definition |
|---|---|---|
| Anon | Anonymous / guest | No session (there are no guest sessions, baseline §12 I-2). Guest donors act on their own donation only through its `donation_access_token` (cells marked `token`). |
| User | Donor / signed-in user | Any USER account, acting on objects it has **no** relationship with (or on its own `/me` data where the cell says `own`). |
| Owner | Campaign owner | The individual USER that owns the campaign (and its beneficiaries, destinations, payouts) in question. |
| Org ADMIN | `ORG_ADMIN` | Member with role ORG_ADMIN **of the organisation that owns the object**. |
| Org MEMBER | `ORG_MEMBER` | Member with role ORG_MEMBER of that organisation. |
| REV … BUS_A | Staff roles | `REVIEWER`, `KYC_REVIEWER`, `SUPPORT` (SUP), `COMPLIANCE` (COMP), `FINANCE` (FIN), `ADMIN` (ADM), `SECURITY_ADMIN` (SEC_A), `SUPER_ADMIN` (SUP_A), `BUSINESS_APPROVER` (BUS_A — approval-only business-owner role, baseline §12 I-4). Separate STAFF accounts (password + WebAuthn/TOTP, I-6); a staff identity is never the account used to run personal campaigns. |
| System/Provider | Jobs and providers | Background jobs (`actor_type = system`, named job) and providers authenticated by signature. Jobs call services, not HTTP routes; the only HTTP routes for this actor are webhooks and health probes. |

**Organisation roles.** Stage 0/1 define exactly two organisation roles: `ORG_ADMIN` and `ORG_MEMBER`
(PRODUCT §4; confirmed by baseline §12 I-1). Organisation financial actions (payout requests, destinations,
financial summary and statements) are `ORG_ADMIN`-only (`org.payout.request`, kyb-architecture §2). An
`ORG_FINANCE` role can be added later as data in `organisation_roles`.

A person may hold several staff roles; each sensitive action still needs its own permission, and the SoD rules
of §4 apply per person (not per account).

## 2. Staff permission catalogue

Permission names reuse SECURITY.md §5 and operational-controls.md §1 wherever they exist. Rows with source
**NEW** are introduced by this Stage 2 design and must be added to the Stage 4 permission seed. `ADMIN` and
`SUPER_ADMIN` hold **no** sensitive permission by default (no KYC documents, no ledger adjustments, no payout
approval — SECURITY §5.2). Granting any permission is itself maker-checker (ADM-08/ADM-09).

| Permission | REV | KYC_R | SUP | COMP | FIN | ADM | SEC_A | SUP_A | BUS_A | Source | Notes |
|---|---|---|---|---|---|---|---|---|---|---|---|
| `campaign.view` | ✓ |  | ✓ | ✓ | ✓ | ✓ |  | ✓ |  | S | SUPPORT/FINANCE read-only views |
| `campaign.review` | ✓ |  |  | ✓ |  |  |  |  |  | S/OC | claim, record checks, work abuse-report queue |
| `campaign.decide` | ✓ |  |  | ✓ |  |  |  |  |  | OC | APPROVE / REQUEST_CHANGES / REJECT / ESCALATE |
| `campaign.suspend` | ✓ |  |  | ✓ |  |  |  |  |  | S/OC |  |
| `campaign.unsuspend` | ✓ |  |  | ✓ |  |  |  |  |  | S/OC |  |
| `campaign.freeze` |  |  |  | ✓ | ✓ |  |  |  |  | S/OC | step-up; ledger move payable → held |
| `campaign.unfreeze.request` |  |  |  | ✓ |  |  |  |  |  | OC | maker |
| `campaign.unfreeze.approve` |  |  |  | ✓ |  |  |  |  |  | NEW | checker; different person from freezer and requester (OC §2/§3) |
| `campaign.cancel` |  |  |  | ✓ |  |  |  |  |  | NEW | staff cancellation; from FROZEN needs a second approver (PRODUCT §6.2) |
| `content.moderate` |  |  |  |  |  | ✓ |  |  |  | OC | visibility HIDDEN/PUBLIC, hide campaign updates |
| `case.create` | ✓ |  | ✓ | ✓ |  |  |  |  |  | OC |  |
| `case.manage` |  |  |  | ✓ |  |  |  |  |  | OC | STANDARD cases only; RESTRICTED_STR needs str.prepare |
| `str.prepare` |  |  |  | ✓ |  |  |  |  |  | OC | named COMPLIANCE staff only (MLRO function); LR-008/LR-060 |
| `str.approve` |  |  |  | ✓ |  |  |  |  |  | CCM | second named COMPLIANCE approver |
| `screening.review` |  |  |  | ✓ |  |  |  |  |  | NEW | sanctions/PEP hit disposition |
| `compliance.override.request` |  |  |  | ✓ |  |  |  |  |  | OC | maker |
| `compliance.override.approve` |  |  |  | ✓ |  |  |  |  |  | OC | checker ≠ maker |
| `compliance.restriction.request` |  |  |  | ✓ |  |  |  |  |  | NEW | capability restrictions (baseline §5.14) |
| `compliance.restriction.approve` |  |  |  | ✓ |  |  |  |  |  | NEW | checker ≠ maker |
| `evidence.view` |  |  |  | ✓ | ✓ |  |  |  |  | AEM | metadata only |
| `evidence.hold` |  |  |  | ✓ |  |  |  |  |  | OC | two-person legal hold |
| `kyc.status.view` | ✓ | ✓ | ✓ | ✓ | ✓ |  |  |  |  | IDP | SUPPORT: level only; FINANCE: payout context. ADMIN/SUPER_ADMIN excluded (IDP §4 wins over SECURITY §5.2 table) |
| `kyc.case.review` |  | ✓ |  | ✓ |  |  |  |  |  | OC | COMPLIANCE for escalations |
| `kyc.document.view` |  | ✓ |  | ✓ |  |  |  |  |  | S/IDP | case-bound, justification, step-up, time-boxed viewer |
| `kyc.identity_number.reveal` |  | ✓ |  | ✓ |  |  |  |  |  | S/IDP | KYC_REVIEWER only for duplicate-identity resolution; daily cap |
| `kyc.decision.record` |  | ✓ |  | ✓ |  |  |  |  |  | S/OC |  |
| `beneficiary.verification.decide` |  | ✓ |  | ✓ |  |  |  |  |  | OC/IDP | HIGH tier needs second approver |
| `org.verification.decide` |  | ✓ |  | ✓ |  |  |  |  |  | OC | KYC_REVIEWER non-final for HIGH risk |
| `user.view` |  |  | ✓ | ✓ |  | ✓ |  |  |  | OC | masked contact data |
| `organisation.view` | ✓ |  | ✓ | ✓ | ✓ | ✓ |  |  |  | NEW | staff organisation view (no KYB documents) |
| `user.suspend` |  |  |  | ✓ |  |  |  |  |  | NEW | audit action user.suspended exists; permission name new |
| `account.recovery.assist` |  |  | ✓ |  |  |  |  |  |  | OC | step-up gated; places ACCOUNT/PAYOUT hold + cooling-off |
| `donor.identity.unmask` |  |  |  | ✓ | ✓ |  |  |  |  | P/PRODUCT §8.1 | justified, audited |
| `payment.view` |  |  | ✓ | ✓ | ✓ |  |  |  |  | NEW | SUPPORT sees masked donor contact |
| `payment.status.query` |  |  |  |  | ✓ |  |  |  |  | NEW | enqueue authoritative provider status query; never changes state directly |
| `refund.view` |  |  | ✓ | ✓ | ✓ |  |  |  |  | NEW |  |
| `refund.request` |  |  | ✓ | ✓ |  |  |  |  |  | S/OC/RD | maker |
| `refund.approve` |  |  |  |  | ✓ |  |  |  |  | S/OC/RD | checker ≠ requester; step-up; also checker for bulk batches (I-24) |
| `refund.bulk.request` |  |  |  | ✓ |  |  |  |  |  | NEW (OC §3, I-24) | maker for bulk refunds (cancelled/fraud campaign) |
| `refund.bulk.approve` |  |  |  |  |  |  |  |  | ✓ | NEW (OC §3, I-24) | additional approval when the batch total exceeds the configured limit (per currency) |
| `dispute.view` |  |  |  | ✓ | ✓ |  |  |  |  | RD |  |
| `dispute.evidence.submit` |  |  |  | ✓ | ✓ |  |  |  |  | RD |  |
| `dispute.accept` |  |  |  |  | ✓ |  |  |  |  | RD | maker-checker (two FINANCE) |
| `recovery.view` |  |  |  | ✓ | ✓ |  |  |  |  | NEW |  |
| `recovery.write_off` |  |  |  |  | ✓ |  |  |  |  | RD | maker (propose write-off) |
| `recovery.write_off.approve` |  |  |  |  | ✓ |  |  |  | ✓ | NEW (RD §6, OC §3, I-4) | checker: second FINANCE up to threshold; BUSINESS_APPROVER above threshold |
| `payout.view` |  |  | ✓ | ✓ | ✓ |  |  |  |  | NEW | SUPPORT: status only, masked destination |
| `payout.approve` |  |  |  |  | ✓ |  |  |  |  | S/OC/I-20 | payout-approver bundle; step-up; SoD; DUAL = two distinct FINANCE approvers; COMPLIANCE never approves payouts |
| `payout.reject` |  |  |  |  | ✓ |  |  |  |  | OC |  |
| `payout.cancel` |  |  |  | ✓ | ✓ |  |  |  |  | NEW | only before SUBMITTED (Y5) |
| `payout.status.query` |  |  |  |  | ✓ |  |  |  |  | NEW | never resubmits |
| `payout.destination.override.request` |  |  | ✓ | ✓ |  |  |  |  |  | NEW (OC §3 maker) | with user-request evidence |
| `payout.destination.override.approve` |  |  |  |  | ✓ |  |  |  |  | OC | checker |
| `hold.view` | ✓ |  |  | ✓ | ✓ |  |  |  |  | NEW | REVIEWER/FINANCE see STR-linked holds as 'compliance review' with no reason |
| `payout.hold` | ✓ |  | ✓ | ✓ | ✓ |  |  |  |  | OC | type-scoped: see hold-type table |
| `hold.release` | ✓ |  |  | ✓ | ✓ |  |  |  |  | NEW | type-scoped; maker-checker for COMPLIANCE_HOLD / DISPUTE_HOLD / RECOVERY_HOLD (RD §6, PE §5) |
| `limit.view` |  |  |  | ✓ | ✓ |  |  |  |  | NEW |  |
| `limit.change.request` |  |  |  | ✓ | ✓ |  |  |  |  | NEW (OC §3) | maker |
| `limit.change.approve` |  |  |  | ✓ | ✓ |  |  |  | ✓ | NEW (OC §3, I-4) | checker = the limit's approval owner (BUSINESS_APPROVER for business-owned limits), ≠ maker |
| `campaign.review_policy.request` |  |  |  | ✓ |  |  |  |  |  | NEW (OC §3) | maker for campaign review policy versions |
| `campaign.review_policy.approve` |  |  |  |  |  |  |  |  | ✓ | NEW (OC §3, I-4) | checker |
| `risk.alert.view` | ✓ |  |  | ✓ |  |  |  |  |  | NEW |  |
| `risk.alert.manage` | ✓ |  |  | ✓ |  |  |  |  |  | NEW | REVIEWER: low severity only (PE EC-16) |
| `risk.assessment.view` | ✓ |  |  | ✓ |  |  |  |  |  | NEW | internal scores — staff DTO only |
| `ledger.view` |  |  |  |  | ✓ |  |  |  |  | NEW |  |
| `ledger.adjustment.create` |  |  |  |  | ✓ |  |  |  |  | S/OC | maker |
| `ledger.adjustment.approve` |  |  |  |  | ✓ |  |  |  |  | S/OC | different FINANCE person |
| `reconciliation.run` |  |  |  |  | ✓ |  |  |  |  | OC | imports and runs |
| `reconciliation.resolve` |  |  |  |  | ✓ |  |  |  |  | OC | money-moving resolutions are maker-checker |
| `report.generate` |  |  |  | ✓ | ✓ |  |  |  |  | NEW | report type scoped by role |
| `audit.read` |  |  |  | ✓ | ✓ |  | ✓ | ✓ |  | S/OC/AUDIT §7 | financial/domain audit chain, scoped: COMPLIANCE broad; FINANCE financial; SECURITY_ADMIN and SUPER_ADMIN role/admin actions only |
| `security_audit.read` |  |  |  |  |  |  | ✓ | ✓ |  | NEW (I-23) | security audit chain (auth events, role grants, KYC views/reveals, break-glass); separate from audit.read |
| `audit.export` |  |  |  | ✓ |  |  |  |  |  | NEW (AUDIT §7) | maker-checker export; LR-032 |
| `staff.support` |  |  |  |  |  | ✓ |  |  |  | OC |  |
| `config.change.request` |  |  |  |  |  | ✓ |  |  |  | OC | feature flags/config; maker |
| `config.change.approve` |  |  |  |  |  |  |  | ✓ |  | NEW | checker ≠ maker — role assignment is a proposal (open item) |
| `fee.config.request` |  |  |  |  | ✓ |  |  |  |  | OC | maker |
| `fee.config.approve` |  |  |  |  |  |  |  |  | ✓ | NEW (OC §3, I-4) | checker; future-dated changes only |
| `provider.view` |  |  |  |  | ✓ | ✓ |  |  |  | NEW | registry, capabilities, health (secret references never returned) |
| `webhook.deadletter.manage` |  |  |  |  | ✓ |  |  |  |  | NEW (PAYMENTS §9) | inspect + re-queue, never edit |
| `killswitch.activate` |  |  |  |  | ✓ |  | ✓ |  |  | OC | SECURITY_ADMIN: security scopes; FINANCE: payout/provider scopes |
| `killswitch.deactivate.request` |  |  |  |  | ✓ |  | ✓ |  |  | NEW (OC §3) | maker |
| `killswitch.deactivate.approve` |  |  |  |  | ✓ |  | ✓ |  |  | NEW (OC §3) | different senior staff |
| `session.revoke` |  |  |  |  |  |  | ✓ |  |  | OC |  |
| `staff.suspend` |  |  |  |  |  |  | ✓ |  |  | OC |  |
| `access.review.run` |  |  |  |  |  |  | ✓ |  |  | OC |  |
| `role.view` |  |  |  |  |  |  | ✓ | ✓ |  | NEW |  |
| `role.assign.request` |  |  |  |  |  |  |  | ✓ |  | OC | maker; no self-grant |
| `role.assign.approve` |  |  |  |  |  |  |  | ✓ |  | OC | different SUPER_ADMIN; step-up |
| `role.revoke` |  |  |  |  |  |  | ✓ | ✓ |  | NEW | safe direction: immediate, no checker; alerted |

Notes:

- `kyc.status.view`: SECURITY.md §5.2's indicative table also ticks ADMIN and SUPER_ADMIN; identity-data-protection.md
  §4 (Stage 1, more specific) does not. This matrix follows Stage 1.
- `BUSINESS_APPROVER` (baseline §12 I-4) is the "business owner" of operational-controls §3. It holds only
  approval permissions (`fee.config.approve`, `recovery.write_off.approve` above threshold,
  `campaign.review_policy.approve`, `limit.change.approve` for business-owned limits) and no operational or
  maker permission, so it can never be both maker and checker.
- The ADMIN/SUPER_ADMIN exclusion from `kyc.status.view` is confirmed by baseline §12 I-5.
- `payout.hold` and `hold.release` are scoped by hold type (§5).

### 2.1 Organisation-scoped permissions (ABAC)

Evaluated against `organisation_members.role` for the organisation that owns the object, in the query.

| Org permission | ORG_ADMIN | ORG_MEMBER | Meaning |
|---|---|---|---|
| `org.view` | ✓ | ✓ | organisation profile, members list |
| `org.manage` | ✓ |  | edit profile, invitations, member roles, removal |
| `org.campaign.create` | ✓ | ✓ | draft campaigns under the organisation |
| `org.campaign.edit` | ✓ | ✓ | edit/submit/publish/close the organisation's campaigns, post updates |
| `org.campaign.cancel` | ✓ |  | cancel an organisation campaign |
| `org.finance.view` | ✓ |  | financial summary, statements, payout list |
| `org.payout_destination.manage` | ✓ |  | PRODUCT §4: members cannot change payout accounts |
| `org.payout.request` | ✓ |  | kyb-architecture §2 / EC-02; requester must also be PAYOUT_VERIFIED |
| `org.verification.submit` | ✓ |  | KYB attempts, persons, fundraising authorities |
| `org.beneficiary.manage` | ✓ | ✓ | beneficiaries owned by the organisation |

Every organisation action additionally requires the acting person's own verification gate where the policy
demands it (for example an `ORG_ADMIN` requesting a payout must be `PAYOUT_VERIFIED` and the organisation
`ORG_PAYOUT_VERIFIED`, EC-03).

## 3. Object-level rules (ownership predicates)

Every handler that loads a resource by id applies the predicate **in the repository query**. A failed predicate
on a user-facing route returns `404` (§7).

| Resource | Predicate (actor may access when …) |
|---|---|
| `/me/*` | The resource belongs to the session's user. |
| Campaign (owner routes `/campaigns/{campaign_id}…`) | `campaigns.owner_user_id = actor` **or** (`campaigns.organisation_id = O` **and** actor is a member of `O` with the org permission the route needs). |
| Campaign (public routes) | `status ∈ {ACTIVE, COMPLETED}` with visibility `PUBLIC`/`UNLISTED` (UNLISTED only by direct code), or `SUSPENDED`/`FROZEN`/`CANCELLED` with a neutral notice when policy shows them; never `HIDDEN`, `DRAFT`, `SUBMITTED`, `UNDER_REVIEW`, `APPROVED`, `REJECTED`. |
| Beneficiary | `beneficiaries.owner` = actor, or owning organisation and `org.beneficiary.manage`. Linking to a campaign requires the same owner on both. |
| Donation / payment / receipt | Created by the actor's user id, **or** the request carries a valid, unexpired `X-Donation-Access-Token` for this donation. The campaign owner never reads a donation through donor routes; owners use `OwnerDonationView` (anonymity applied). |
| Refund request (donor) | `refund_requests.requested_by = actor` and `channel = DONOR`. |
| Payout destination | Owned by the actor (individual), or by the actor's organisation with `org.payout_destination.manage` (write) / `org.finance.view` (read). Responses are always masked. |
| Payout request | The campaign predicate with `Owner` or `org.finance.view` (read) / `org.payout.request` (create, cancel). |
| Organisation | Member (`org.view`); management requires `org.manage`. |
| Invitation acceptance | The invitation's target contact equals one of the actor's **verified** contacts; otherwise `404 INVITATION_NOT_FOUND`. |
| KYC attempt | `subject = actor` (user) or actor's organisation with `org.verification.submit` (KYB). |
| Staff KYC case | Permission **and** (case assigned to the actor or in a queue the actor works) for reads; C3 access additionally requires an **open** case/review **assigned to the actor** (`KYC_ACCESS_NOT_BOUND`). |
| Compliance case | `case.manage`; `RESTRICTED_STR` cases additionally require `str.prepare` (otherwise invisible: 404 and excluded from lists). |
| Report job | `report.generate` and `requested_by = actor`. |
| Audit events | `audit.read` with domain scope: COMPLIANCE all non-security domains; FINANCE financial domains (payments, payouts, refunds, ledger, reconciliation, fees); SECURITY_ADMIN and SUPER_ADMIN role/admin actions. The **security audit chain** needs the separate `security_audit.read` (SECURITY_ADMIN, SUPER_ADMIN; baseline §12 I-23). Out-of-scope events → 404. |

**Staff conflicts (all staff routes).** A staff member may not act on a campaign, user, organisation or payout
where they declared a relationship, are a donor above the configured amount, are the beneficiary, or which is
connected to their own personal account (operational-controls §2). Violations return
`403 STAFF_CONFLICT_OF_INTEREST` (non-approval actions) or `403 APPROVER_CONFLICT` (checker actions), and are
audited with `outcome = denied`.

## 4. Maker-checker and step-up rules

| Action (endpoint) | Maker | Checker | Constraint enforced (code + DB) | Step-up | Expiry |
|---|---|---|---|---|---|
| Payout approval SINGLE/DUAL (POR-08/09) | System (owner request) / FINANCE (manual) | FINANCE `payout.approve` (DUAL: a second, distinct FINANCE approver; COMPLIANCE never approves payouts — baseline §12 I-20) | approver ≠ requester ≠ initiator; ≠ last destination changer/verifier in cooling-off; DUAL approvers distinct; no declared conflict; binds to amount/currency/destination snapshot | yes | 72 h → back to PENDING_REVIEW |
| Destination override (DST-07/08; `app.payout_destination_override_requests`) | SUPPORT / COMPLIANCE | FINANCE `payout.destination.override.approve` | checker ≠ maker; evidence of user request; triggers hold + cooling-off + notification | yes | 24 h |
| Refund (REF-05/REF-01 → REF-09/10) | Donor / SUPPORT / COMPLIANCE / system | FINANCE `refund.approve` | approver ≠ requester; original rail only; platform-funded plan (C2) needs DUAL | yes | 72 h |
| Bulk refund (REF-22 → REF-23, REF-24) | COMPLIANCE `refund.bulk.request` | FINANCE `refund.approve`; plus BUSINESS_APPROVER `refund.bulk.approve` when a currency total exceeds the configured limit | all distinct people; approvals bind to the manifest hash; each refund individually idempotent (I-24) | yes | 72 h |
| Dispute acceptance (REF-16/17) | FINANCE | different FINANCE | `payment_disputes.accept_approved_by <> accept_requested_by` (DB CHECK) | yes | per provider deadline |
| Recovery write-off (REF-20/21) | FINANCE | second FINANCE (DUAL); BUSINESS_APPROVER above threshold (`recovery.write_off.approve`) | distinct people | yes | 7 d |
| Ledger adjustment (REC-16/17) | FINANCE | different FINANCE | `approved_by <> requested_by`; balanced, single-currency preview | yes | 24 h |
| Reconciliation resolution changing state/money (REC-09/10) | FINANCE | different FINANCE | evidence required; resolves UNKNOWN only with authoritative evidence | yes | 24 h |
| Campaign unfreeze (MOD-09/11) | COMPLIANCE | different COMPLIANCE / designated officer | checker ≠ requester ≠ whoever froze | yes | 72 h |
| Cancel from FROZEN (MOD-10/11) | COMPLIANCE | second approver | as above | yes | 72 h |
| Hold release — COMPLIANCE_HOLD, DISPUTE_HOLD (manual), RECOVERY_HOLD, CAMPAIGN_FREEZE (RSK-04/05; `risk.hold_release_requests`) | per type (§5) | per type (§5) | checker ≠ maker; ≠ placer for freezes | yes | 72 h |
| Compliance case decision needing checker (CMPL-09/10) | COMPLIANCE | different COMPLIANCE | no conflict; freeze/unfreeze, offboarding, sanctions, STR, money-moving decisions | yes | 72 h |
| Screening disposition at S1 (CMPL-13) | COMPLIANCE | different COMPLIANCE | — | yes | 24 h |
| Compliance restriction / override (CMPL-14/15, CMPL-17/18) | COMPLIANCE | different COMPLIANCE / designated officer | single subject, mandatory expiry, never bypasses sanctions block or legal hold | yes | 24 h |
| Limit change (RSK-07/08) | COMPLIANCE or FINANCE | the limit's approval owner (COMPLIANCE / FINANCE; BUSINESS_APPROVER for business-owned limits) | checker ≠ maker; source citation, effective and review dates | yes | 7 d |
| Campaign review policy version (ADM-27/28) | COMPLIANCE | BUSINESS_APPROVER (`campaign.review_policy.approve`) | checker ≠ maker; new version; existing reviews keep their pinned version | yes | 7 d |
| Fee change (ADM-14/15) | FINANCE | BUSINESS_APPROVER (`fee.config.approve`) | future-dated only | yes | 7 d |
| Role grant (ADM-08/09) | SUPER_ADMIN | different SUPER_ADMIN | no self-grant; R-conflicts rejected (`ROLE_COMBINATION_FORBIDDEN`) | yes | 24 h |
| Kill-switch deactivation / approval-required flag change (ADM-18/24 → ADM-19) | SECURITY_ADMIN / FINANCE (kill switch); ADMIN (`config.change.request`) | different senior staff (`killswitch.deactivate.approve` / `config.change.approve`) | `app.feature_flag_changes` CHECK `approved_by <> requested_by` | yes | — |
| KYC decision with second approval (KYC-13/14), HIGH-tier beneficiary/KYB decisions (BEN-13, KYB-11) | KYC_REVIEWER / COMPLIANCE | COMPLIANCE | ≠ decider; HIGH tier: KYC decider ≠ campaign decider | yes | 72 h |
| Legal/evidence hold (CMPL-22) | COMPLIANCE | second person | two-person | yes | — |

Safe-direction actions are **immediate** and not maker-checker: placing holds, freezing, activating a kill switch,
revoking a role or session, suspending a staff account. They still require justification and are alerted.

Expired pending approvals close as `EXPIRED` (audited); they are never auto-approved. All checker endpoints
return `403 APPROVER_CONFLICT` with a conflict category (`CONFLICT_MAKER_IS_CHECKER`, `CONFLICT_INITIATOR`,
`CONFLICT_DESTINATION_CHANGER`, `CONFLICT_DUPLICATE_APPROVER`, `CONFLICT_DECLARED_RELATIONSHIP`).

### 4.1 Role-level SoD (assignment time)

Role grants are rejected (`409 ROLE_COMBINATION_FORBIDDEN`) when they would create a combination marked **R** in
operational-controls.md §2: `SECURITY_ADMIN` with any financial, KYC or campaign-decision function; payout
approver with role-grant approver; ledger-adjustment approver with role-grant approver; KYC decision with role-grant
approver.

## 5. Hold types: who may place and release (RSK-03/04/05)

Both actions use `payout.hold` / `hold.release`, scoped by hold type ([payout-eligibility-and-controls.md §5](../payments/payout-eligibility-and-controls.md)).

| Hold type | Place | Release | Release maker-checker |
|---|---|---|---|
| `CAMPAIGN_FREEZE` | via MOD-08 (`campaign.freeze`: COMPLIANCE, FINANCE) | via unfreeze MOD-09/11 | yes (COMPLIANCE + second approver) |
| `COMPLIANCE_HOLD` | COMPLIANCE | COMPLIANCE | yes |
| `PAYOUT_HOLD` | system, REVIEWER, COMPLIANCE | COMPLIANCE; REVIEWER for low-severity system holds | no (low severity) / yes (high severity) |
| `DISPUTE_HOLD` | system | system on case close; manual: COMPLIANCE / FINANCE | yes (manual) |
| `DESTINATION_HOLD` | system | system after cooling-off **and** re-verification | n/a (no manual release) |
| `RECOVERY_HOLD` | FINANCE | FINANCE | yes |
| `ACCOUNT_HOLD` | COMPLIANCE, SUPPORT (suspected takeover) | COMPLIANCE | no |
| `PROVIDER_HOLD` | FINANCE, system | FINANCE | no |

Owners never see hold types or reasons: owner DTOs expose only `payouts_paused`, and payout errors use the single
category `PAYOUTS_PAUSED`. Staff without `str.prepare` see STR-linked hold reasons as `COMPLIANCE_REVIEW`.

## 6. Tipping-off and sensitive-data rules (all actors)

1. No owner-, donor- or beneficiary-facing endpoint returns hold reasons, case existence, screening results,
   risk scores or STR status. Templates for these audiences are pre-approved and reason-free (LR-008, LR-072).
2. `RESTRICTED_STR` cases, STR records and their links are invisible to everyone without `str.prepare` /
   `str.approve` — not just unreadable (404, excluded from lists and counts).
3. C3 access (KYC documents, full identity numbers, full payout account numbers) is possible **only** through the
   audited view-session/reveal endpoints (KYC-15, KYC-16, KYB-12; DST-10 not yet in OpenAPI), each needing the
   specific permission, an open assigned case, a justification and fresh step-up. No list or detail endpoint
   returns C3 values.
4. Donor unmasking (DON-10) is COMPLIANCE/FINANCE only, justified and audited (PRODUCT §8.1).

## 7. 404-versus-403 rule

| Situation | Response |
|---|---|
| User-facing resource does not exist | `404 <RESOURCE>_NOT_FOUND` |
| User-facing resource exists but the caller has **no relationship** with it (not owner, not member, not the donor) | `404 <RESOURCE>_NOT_FOUND` (identical body and timing class) |
| Caller can see the resource but lacks the role for the action (e.g. `ORG_MEMBER` on payout or financial routes, cells marked `403`) | `403 PERMISSION_DENIED` |
| Caller lacks a verification level or is restricted | `403 VERIFICATION_REQUIRED` / `403 ACCOUNT_RESTRICTED` (reason-free) |
| Non-staff session on any `/api/v1/admin/*` route | `404 ROUTE_NOT_FOUND` |
| Staff without the permission | `403 PERMISSION_DENIED` |
| Staff without `str.prepare` on a `RESTRICTED_STR` case or link | `404 COMPLIANCE_CASE_NOT_FOUND` |
| Staff outside audit scope | `404 AUDIT_EVENT_NOT_FOUND` |

Integration tests (Stage 4) cover a "different user's id" case on every user-facing route and assert `404`
([SECURITY.md §9.2](../SECURITY.md)).

## 8. Open points

| # | Point | Proposal |
|---|---|---|
| 1 | `payout.hold` covers non-payout holds (ACCOUNT_HOLD). | Keep the Stage 1 name; consider `hold.place` in the Stage 4 seed for clarity. |
| 2 | Many NEW permissions. | Review with the operational-controls owner before the Stage 4 seed. |
| 3 | Role-level SoD for `BUSINESS_APPROVER`. | Reject combining it with FINANCE or COMPLIANCE maker functions for the same person (add an **R** row to operational-controls §2 in Stage 4). |

## 9. Endpoint × actor matrix

Legend: `✓` allowed · `own` only the caller's own objects (others → 404) · `org` only objects of the caller's
organisation, with the org permission named in api-design.md · `token` via `X-Donation-Access-Token` or a signed
token in the body · `self` only acting on themself · `403` can see but not perform · `sig` provider signature ·
`J` justification required · `SU` step-up required · `MC` participates in maker-checker (maker or checker, never
both) · `t` scoped by hold type (§5) · `lvl` level only · `dedupe` duplicate-identity resolution only ·
`scoped` audit domain scope (§3) · `—` denied (404 on user routes and for non-staff on `/admin`; 403 for staff
lacking permission). Staff permission in backticks after the path.

### Authentication

| ID | Endpoint | ANON | USER | OWNER | ORGA | ORGM | REV | KYC_R | SUP | COMP | FIN | ADM | SEC_A | SUP_A | BUS_A | SYS |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| AUTH-01 | `GET /auth/session` | — | own | own | own | own | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | — |
| AUTH-02 | `POST /auth/otp` | ✓ | ✓ | — | — | — | — | — | — | — | — | — | — | — | — | — |
| AUTH-03 | `POST /auth/otp/verify` | ✓ | ✓ | — | — | — | — | — | — | — | — | — | — | — | — | — |
| AUTH-04 | `POST /auth/logout` | — | own | own | own | own | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | — |
| AUTH-05 | `POST /auth/step-up/challenges` | — | own | own | own | own | — | — | — | — | — | — | — | — | — | — |
| AUTH-06 | `POST /auth/step-up/verify` | — | own | own | own | own | — | — | — | — | — | — | — | — | — | — |
| AUTH-07 | `GET /me/sessions` | — | own | own | own | own | — | — | — | — | — | — | — | — | — | — |
| AUTH-08 | `DELETE /me/sessions/{session_id}` | — | own | own | own | own | — | — | — | — | — | — | — | — | — | — |
| AUTH-09 | `POST /admin/auth/login` | ✓ | — | — | — | — | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | — |
| AUTH-10 | `POST /admin/auth/mfa/verify` | — | — | — | — | — | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | — |
| AUTH-11 | `POST /admin/auth/step-up` | — | — | — | — | — | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | — |
| AUTH-12 | `POST /admin/auth/step-up/verify` | — | — | — | — | — | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | — |
| AUTH-13 | `GET /admin/me` | — | — | — | — | — | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | — |
| AUTH-14 | `POST /admin/me/mfa-methods` | — | — | — | — | — | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | — |
| AUTH-15 | `DELETE /admin/me/mfa-methods/{method_id}` | — | — | — | — | — | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | — |
| AUTH-16 | `POST /auth/password/login` | ✓ | — | — | — | — | — | — | — | — | — | — | — | — | — | — |

### User profiles

| ID | Endpoint | ANON | USER | OWNER | ORGA | ORGM | REV | KYC_R | SUP | COMP | FIN | ADM | SEC_A | SUP_A | BUS_A | SYS |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| USR-01 | `GET /me` | — | own | own | own | own | — | — | — | — | — | — | — | — | — | — |
| USR-02 | `PATCH /me/profile` | — | own | own | own | own | — | — | — | — | — | — | — | — | — | — |
| USR-03 | `POST /me/contact-changes` | — | own | own | own | own | — | — | — | — | — | — | — | — | — | — |
| USR-04 | `POST /me/contact-changes/{contact_change_id}/verify` | — | own | own | own | own | — | — | — | — | — | — | — | — | — | — |
| USR-05 | `GET /me/activity` | — | own | own | own | own | — | — | — | — | — | — | — | — | — | — |
| USR-06 | `POST /me/data-export-requests` | — | own | own | own | own | — | — | — | — | — | — | — | — | — | — |
| USR-07 | `POST /me/deletion-requests` | — | own | own | own | own | — | — | — | — | — | — | — | — | — | — |

### Organisations

| ID | Endpoint | ANON | USER | OWNER | ORGA | ORGM | REV | KYC_R | SUP | COMP | FIN | ADM | SEC_A | SUP_A | BUS_A | SYS |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| ORG-01 | `POST /organisations` | — | ✓ | ✓ | ✓ | ✓ | — | — | — | — | — | — | — | — | — | — |
| ORG-02 | `GET /me/organisations` | — | own | own | own | own | — | — | — | — | — | — | — | — | — | — |
| ORG-03 | `GET /organisations/{org_id}` | — | — | — | org | org | — | — | — | — | — | — | — | — | — | — |
| ORG-04 | `PATCH /organisations/{org_id}` | — | — | — | org | 403 | — | — | — | — | — | — | — | — | — | — |
| ORG-05 | `GET /organisations/{org_id}/members` | — | — | — | org | org | — | — | — | — | — | — | — | — | — | — |
| ORG-06 | `PATCH /organisations/{org_id}/members/{member_id}` | — | — | — | org | 403 | — | — | — | — | — | — | — | — | — | — |
| ORG-07 | `DELETE /organisations/{org_id}/members/{member_id}` | — | — | — | org | self | — | — | — | — | — | — | — | — | — | — |
| ORG-08 | `POST /organisations/{org_id}/invitations` | — | — | — | org | 403 | — | — | — | — | — | — | — | — | — | — |
| ORG-09 | `GET /organisations/{org_id}/invitations` | — | — | — | org | 403 | — | — | — | — | — | — | — | — | — | — |
| ORG-10 | `DELETE /organisations/{org_id}/invitations/{invitation_id}` | — | — | — | org | 403 | — | — | — | — | — | — | — | — | — | — |
| ORG-11 | `POST /organisation-invitations/accept` | — | ✓ | ✓ | ✓ | ✓ | — | — | — | — | — | — | — | — | — | — |

### Beneficiaries

| ID | Endpoint | ANON | USER | OWNER | ORGA | ORGM | REV | KYC_R | SUP | COMP | FIN | ADM | SEC_A | SUP_A | BUS_A | SYS |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| BEN-01 | `POST /beneficiaries` | — | ✓ | ✓ | org | org | — | — | — | — | — | — | — | — | — | — |
| BEN-02 | `GET /beneficiaries` | — | own | own | org | org | — | — | — | — | — | — | — | — | — | — |
| BEN-03 | `GET /beneficiaries/{beneficiary_id}` | — | — | own | org | org | — | — | — | — | — | — | — | — | — | — |
| BEN-04 | `PATCH /beneficiaries/{beneficiary_id}` | — | — | own | org | org | — | — | — | — | — | — | — | — | — | — |
| BEN-05 | `POST /beneficiaries/{beneficiary_id}/evidence/upload-sessions` | — | — | own | org | org | — | — | — | — | — | — | — | — | — | — |
| BEN-06 | `POST /beneficiaries/{beneficiary_id}/evidence` | — | — | own | org | org | — | — | — | — | — | — | — | — | — | — |
| BEN-07 | `POST /beneficiaries/{beneficiary_id}/consents` | — | — | own | org | org | — | — | — | — | — | — | — | — | — | — |
| BEN-08 | `POST /beneficiaries/{beneficiary_id}/verification-submissions` | — | — | own | org | org | — | — | — | — | — | — | — | — | — | — |
| BEN-09 | `PUT /campaigns/{campaign_id}/beneficiary` | — | — | own | org | org | — | — | — | — | — | — | — | — | — | — |
| BEN-10 | `GET /institution-payees` | — | ✓ | ✓ | ✓ | ✓ | — | — | — | — | — | — | — | — | — | — |
| BEN-11 | `POST /institution-payees/proposals` | — | ✓ | ✓ | ✓ | ✓ | — | — | — | — | — | — | — | — | — | — |
| BEN-12 | `GET /admin/beneficiary-verifications` `beneficiary.verification.decide` | — | — | — | — | — | — | ✓ | — | ✓ | — | — | — | — | — | — |
| BEN-13 | `POST /admin/beneficiary-verifications/{verification_id}/decisions` `beneficiary.verification.decide` | — | — | — | — | — | — | ✓·J·SU·MC | — | ✓·J·SU·MC | — | — | — | — | — | — |

### Campaign creation

| ID | Endpoint | ANON | USER | OWNER | ORGA | ORGM | REV | KYC_R | SUP | COMP | FIN | ADM | SEC_A | SUP_A | BUS_A | SYS |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| CMP-01 | `POST /campaigns` | — | ✓ | ✓ | org | org | — | — | — | — | — | — | — | — | — | — |
| CMP-02 | `GET /me/campaigns` | — | own | own | org | org | — | — | — | — | — | — | — | — | — | — |
| CMP-03 | `GET /campaigns/{campaign_id}` | — | — | own | org | org | — | — | — | — | — | — | — | — | — | — |
| CMP-04 | `PATCH /campaigns/{campaign_id}` | — | — | own | org | org | — | — | — | — | — | — | — | — | — | — |
| CMP-05 | `POST /campaigns/{campaign_id}/media/upload-sessions` | — | — | own | org | org | — | — | — | — | — | — | — | — | — | — |
| CMP-06 | `POST /campaigns/{campaign_id}/media` | — | — | own | org | org | — | — | — | — | — | — | — | — | — | — |
| CMP-07 | `DELETE /campaigns/{campaign_id}/media/{media_id}` | — | — | own | org | org | — | — | — | — | — | — | — | — | — | — |
| CMP-08 | `POST /campaigns/{campaign_id}/submit` | — | — | own | org | org | — | — | — | — | — | — | — | — | — | — |
| CMP-09 | `POST /campaigns/{campaign_id}/withdraw-submission` | — | — | own | org | org | — | — | — | — | — | — | — | — | — | — |
| CMP-10 | `POST /campaigns/{campaign_id}/publish` | — | — | own | org | org | — | — | — | — | — | — | — | — | — | — |
| CMP-11 | `POST /campaigns/{campaign_id}/close` | — | — | own | org | org | — | — | — | — | — | — | — | — | — | — |
| CMP-12 | `POST /campaigns/{campaign_id}/cancel` | — | — | own | org | 403 | — | — | — | — | — | — | — | — | — | — |
| CMP-13 | `GET /campaigns/{campaign_id}/financial-summary` | — | — | own | org | 403 | — | — | — | — | — | — | — | — | — | — |
| CMP-14 | `GET /campaigns/{campaign_id}/donations` | — | — | own | org | org | — | — | — | — | — | — | — | — | — | — |
| CMP-15 | `GET /campaigns/{campaign_id}/statement` | — | — | own | org | 403 | — | — | — | — | — | — | — | — | — | — |
| CMP-16 | `GET /campaigns/{campaign_id}/share-stats` | — | — | own | org | org | — | — | — | — | — | — | — | — | — | — |
| CMP-17 | `GET /campaigns/{campaign_id}/donations/export` | — | — | own | org | — | — | — | — | — | — | — | — | — | — | — |

### Campaign updates

| ID | Endpoint | ANON | USER | OWNER | ORGA | ORGM | REV | KYC_R | SUP | COMP | FIN | ADM | SEC_A | SUP_A | BUS_A | SYS |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| UPD-01 | `POST /campaigns/{campaign_id}/updates` | — | — | own | org | org | — | — | — | — | — | — | — | — | — | — |
| UPD-02 | `GET /campaigns/{campaign_id}/updates` | — | — | own | org | org | — | — | — | — | — | — | — | — | — | — |
| UPD-03 | `PATCH /campaigns/{campaign_id}/updates/{update_id}` | — | — | own | org | org | — | — | — | — | — | — | — | — | — | — |
| UPD-04 | `DELETE /campaigns/{campaign_id}/updates/{update_id}` | — | — | own | org | org | — | — | — | — | — | — | — | — | — | — |
| UPD-05 | `GET /public/campaigns/{public_code}/updates` | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | — |

### Campaign discovery

| ID | Endpoint | ANON | USER | OWNER | ORGA | ORGM | REV | KYC_R | SUP | COMP | FIN | ADM | SEC_A | SUP_A | BUS_A | SYS |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| DIS-01 | `GET /public/campaigns` | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | — |
| DIS-02 | `GET /public/campaigns/{public_code}` | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | — |
| DIS-03 | `GET /public/campaigns/{public_code}/donations` | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | — |
| DIS-04 | `GET /public/campaign-categories` | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | — |
| DIS-05 | `GET /public/currencies` | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | — |
| DIS-06 | `GET /public/organisations/{org_id}` | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | — |
| DIS-07 | `POST /public/campaigns/{public_code}/abuse-reports` | ✓ | ✓ | ✓ | ✓ | ✓ | — | — | — | — | — | — | — | — | — | — |

### Campaign moderation (staff)

| ID | Endpoint | ANON | USER | OWNER | ORGA | ORGM | REV | KYC_R | SUP | COMP | FIN | ADM | SEC_A | SUP_A | BUS_A | SYS |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| MOD-01 | `GET /admin/campaigns` `campaign.view` | — | — | — | — | — | ✓ | — | ✓ | ✓ | ✓ | ✓ | — | ✓ | — | — |
| MOD-02 | `GET /admin/campaigns/{campaign_id}` `campaign.view` | — | — | — | — | — | ✓ | — | ✓ | ✓ | ✓ | ✓ | — | ✓ | — | — |
| MOD-03 | `POST /admin/campaigns/{campaign_id}/review/claim` `campaign.review` | — | — | — | — | — | ✓ | — | — | ✓ | — | — | — | — | — | — |
| MOD-04 | `POST /admin/campaigns/{campaign_id}/review/checks` `campaign.review` | — | — | — | — | — | ✓ | — | — | ✓ | — | — | — | — | — | — |
| MOD-05 | `POST /admin/campaigns/{campaign_id}/review/decision` `campaign.decide` | — | — | — | — | — | ✓·J | — | — | ✓·J | — | — | — | — | — | — |
| MOD-06 | `POST /admin/campaigns/{campaign_id}/suspend` `campaign.suspend` | — | — | — | — | — | ✓·J | — | — | ✓·J | — | — | — | — | — | — |
| MOD-07 | `POST /admin/campaigns/{campaign_id}/unsuspend` `campaign.unsuspend` | — | — | — | — | — | ✓·J | — | — | ✓·J | — | — | — | — | — | — |
| MOD-08 | `POST /admin/campaigns/{campaign_id}/freeze` `campaign.freeze` | — | — | — | — | — | — | — | — | ✓·J·SU | ✓·J·SU | — | — | — | — | — |
| MOD-09 | `POST /admin/campaigns/{campaign_id}/unfreeze-requests` `campaign.unfreeze.request` | — | — | — | — | — | — | — | — | ✓·J·SU·MC | — | — | — | — | — | — |
| MOD-10 | `POST /admin/campaigns/{campaign_id}/cancel` `campaign.cancel` | — | — | — | — | — | — | — | — | ✓·J·SU·MC | — | — | — | — | — | — |
| MOD-11 | `POST /admin/campaign-moderation-actions/{action_id}/approve` `campaign.unfreeze.approve` | — | — | — | — | — | — | — | — | ✓·J·SU·MC | — | — | — | — | — | — |
| MOD-12 | `POST /admin/campaign-moderation-actions/{action_id}/reject` `campaign.unfreeze.approve` | — | — | — | — | — | — | — | — | ✓·J·MC | — | — | — | — | — | — |
| MOD-13 | `PATCH /admin/campaigns/{campaign_id}/visibility` `content.moderate` | — | — | — | — | — | — | — | — | — | — | ✓·J | — | — | — | — |
| MOD-14 | `POST /admin/campaigns/{campaign_id}/updates/{update_id}/hide` `content.moderate` | — | — | — | — | — | — | — | — | — | — | ✓·J | — | — | — | — |
| MOD-15 | `GET /admin/abuse-reports` `campaign.review` | — | — | — | — | — | ✓ | — | — | ✓ | — | — | — | — | — | — |
| MOD-16 | `POST /admin/abuse-reports/{abuse_report_id}/resolve` `campaign.review` | — | — | — | — | — | ✓·J | — | — | ✓·J | — | — | — | — | — | — |
| MOD-17 | `GET /admin/campaigns/{campaign_id}/status-history` `campaign.view` | — | — | — | — | — | ✓ | — | ✓ | ✓ | ✓ | ✓ | — | ✓ | — | — |
| MOD-18 | `GET /admin/campaigns/{campaign_id}/financial-summary` `hold.view` | — | — | — | — | — | ✓ | — | — | ✓ | ✓ | — | — | — | — | — |

### Donations

| ID | Endpoint | ANON | USER | OWNER | ORGA | ORGM | REV | KYC_R | SUP | COMP | FIN | ADM | SEC_A | SUP_A | BUS_A | SYS |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| DON-01 | `GET /public/campaigns/{public_code}/donation-options` | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | — |
| DON-02 | `POST /campaigns/{campaign_id}/donations` | ✓ | ✓ | ✓ | ✓ | ✓ | — | — | — | — | — | — | — | — | — | — |
| DON-03 | `GET /me/donations` | — | own | own | own | own | — | — | — | — | — | — | — | — | — | — |
| DON-04 | `GET /donations/{donation_id}` | token | own | own | own | own | — | — | — | — | — | — | — | — | — | — |
| DON-05 | `GET /donations/{donation_id}/receipt` | token | own | own | own | own | — | — | — | — | — | — | — | — | — | — |
| DON-06 | `PATCH /donations/{donation_id}/visibility` | — | own | own | own | own | — | — | — | — | — | — | — | — | — | — |
| DON-07 | `POST /donations/{donation_id}/claim` | — | token | token | token | token | — | — | — | — | — | — | — | — | — | — |
| DON-08 | `GET /admin/donations` `payment.view` | — | — | — | — | — | — | — | ✓ | ✓ | ✓ | — | — | — | — | — |
| DON-09 | `GET /admin/donations/{donation_id}` `payment.view` | — | — | — | — | — | — | — | ✓ | ✓ | ✓ | — | — | — | — | — |
| DON-10 | `POST /admin/donations/{donation_id}/donor-unmask` `donor.identity.unmask` | — | — | — | — | — | — | — | — | ✓·J·SU | ✓·J·SU | — | — | — | — | — |

### Payment intents

| ID | Endpoint | ANON | USER | OWNER | ORGA | ORGM | REV | KYC_R | SUP | COMP | FIN | ADM | SEC_A | SUP_A | BUS_A | SYS |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| PAY-01 | `POST /payments/{payment_id}/cancel` | — | own | own | own | own | — | — | — | — | — | — | — | — | — | — |
| PAY-02 | `GET /admin/payments` `payment.view` | — | — | — | — | — | — | — | ✓ | ✓ | ✓ | — | — | — | — | — |
| PAY-03 | `GET /admin/payments/{payment_id}` `payment.view` | — | — | — | — | — | — | — | ✓ | ✓ | ✓ | — | — | — | — | — |
| PAY-04 | `GET /admin/payments/{payment_id}/events` `payment.view` | — | — | — | — | — | — | — | ✓ | ✓ | ✓ | — | — | — | — | — |
| PAY-05 | `POST /admin/payments/{payment_id}/status-checks` `payment.status.query` | — | — | — | — | — | — | — | — | — | ✓·J | — | — | — | — | — |

### Payment status

| ID | Endpoint | ANON | USER | OWNER | ORGA | ORGM | REV | KYC_R | SUP | COMP | FIN | ADM | SEC_A | SUP_A | BUS_A | SYS |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| PST-01 | `GET /payments/{payment_id}` | token | own | own | own | own | — | — | — | — | — | — | — | — | — | — |
| PST-02 | `GET /payments/{payment_id}/stream` | token | own | own | own | own | — | — | — | — | — | — | — | — | — | — |

### Refund requests

| ID | Endpoint | ANON | USER | OWNER | ORGA | ORGM | REV | KYC_R | SUP | COMP | FIN | ADM | SEC_A | SUP_A | BUS_A | SYS |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| REF-01 | `POST /donations/{donation_id}/refund-requests` | token | own | own | own | own | — | — | — | — | — | — | — | — | — | — |
| REF-02 | `GET /me/refund-requests` | — | own | own | own | own | — | — | — | — | — | — | — | — | — | — |
| REF-03 | `GET /refund-requests/{refund_request_id}` | token | own | own | own | own | — | — | — | — | — | — | — | — | — | — |
| REF-04 | `POST /refund-requests/{refund_request_id}/withdraw` | — | own | own | own | own | — | — | — | — | — | — | — | — | — | — |
| REF-05 | `POST /admin/refund-requests` `refund.request` | — | — | — | — | — | — | — | ✓·J·MC | ✓·J·MC | — | — | — | — | — | — |
| REF-06 | `GET /admin/refund-requests` `refund.view` | — | — | — | — | — | — | — | ✓ | ✓ | ✓ | — | — | — | — | — |
| REF-07 | `GET /admin/refund-requests/{refund_request_id}` `refund.view` | — | — | — | — | — | — | — | ✓ | ✓ | ✓ | — | — | — | — | — |
| REF-08 | `POST /admin/refund-requests/{refund_request_id}/submit-for-approval` `refund.request` | — | — | — | — | — | — | — | ✓·J | ✓·J | — | — | — | — | — | — |
| REF-09 | `POST /admin/refund-requests/{refund_request_id}/approve` `refund.approve` | — | — | — | — | — | — | — | — | — | ✓·J·SU·MC | — | — | — | — | — |
| REF-10 | `POST /admin/refund-requests/{refund_request_id}/reject` `refund.approve` | — | — | — | — | — | — | — | — | — | ✓·J·SU·MC | — | — | — | — | — |
| REF-11 | `POST /admin/refund-requests/{refund_request_id}/hold` `refund.approve` | — | — | — | — | — | — | — | — | — | ✓·J | — | — | — | — | — |
| REF-12 | `POST /admin/refund-requests/{refund_request_id}/resume` `refund.approve` | — | — | — | — | — | — | — | — | — | ✓·J | — | — | — | — | — |
| REF-22 | `POST /admin/refund-requests/bulk` `refund.bulk.request` | — | — | — | — | — | — | — | — | ✓·J·SU·MC | — | — | — | — | — | — |
| REF-23 | `POST /admin/refund-batches/{batch_id}/approve` `refund.approve` | — | — | — | — | — | — | — | — | — | ✓·J·SU·MC | — | — | — | — | — |
| REF-24 | `POST /admin/refund-batches/{batch_id}/business-approve` `refund.bulk.approve` | — | — | — | — | — | — | — | — | — | — | — | — | — | ✓·J·SU·MC | — |
| REF-13 | `GET /admin/disputes` `dispute.view` | — | — | — | — | — | — | — | — | ✓ | ✓ | — | — | — | — | — |
| REF-14 | `GET /admin/disputes/{dispute_id}` `dispute.view` | — | — | — | — | — | — | — | — | ✓ | ✓ | — | — | — | — | — |
| REF-15 | `POST /admin/disputes/{dispute_id}/evidence-packs` `dispute.evidence.submit` | — | — | — | — | — | — | — | — | ✓·J·SU | ✓·J·SU | — | — | — | — | — |
| REF-16 | `POST /admin/disputes/{dispute_id}/accept` `dispute.accept` | — | — | — | — | — | — | — | — | — | ✓·J·SU·MC | — | — | — | — | — |
| REF-17 | `POST /admin/disputes/{dispute_id}/accept/approve` `dispute.accept` | — | — | — | — | — | — | — | — | — | ✓·J·SU·MC | — | — | — | — | — |
| REF-18 | `GET /admin/recovery-cases` `recovery.view` | — | — | — | — | — | — | — | — | ✓ | ✓ | — | — | — | — | — |
| REF-19 | `GET /admin/recovery-cases/{recovery_case_id}` `recovery.view` | — | — | — | — | — | — | — | — | ✓ | ✓ | — | — | — | — | — |
| REF-20 | `POST /admin/recovery-cases/{recovery_case_id}/write-off` `recovery.write_off` | — | — | — | — | — | — | — | — | — | ✓·J·SU·MC | — | — | — | — | — |
| REF-21 | `POST /admin/recovery-cases/{recovery_case_id}/write-off/approve` `recovery.write_off.approve` | — | — | — | — | — | — | — | — | — | ✓·J·SU·MC | — | — | — | ✓·J·SU·MC | — |

### Payout destinations

| ID | Endpoint | ANON | USER | OWNER | ORGA | ORGM | REV | KYC_R | SUP | COMP | FIN | ADM | SEC_A | SUP_A | BUS_A | SYS |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| DST-01 | `GET /payout-destinations` | — | own | own | org | — | — | — | — | — | — | — | — | — | — | — |
| DST-02 | `POST /payout-destinations` | — | ✓ | ✓ | org | 403 | — | — | — | — | — | — | — | — | — | — |
| DST-03 | `GET /payout-destinations/{destination_id}` | — | — | own | org | — | — | — | — | — | — | — | — | — | — | — |
| DST-04 | `POST /payout-destinations/{destination_id}/deactivate` | — | — | own | org | 403 | — | — | — | — | — | — | — | — | — | — |
| DST-05 | `PUT /campaigns/{campaign_id}/payout-destination` | — | — | own | org | 403 | — | — | — | — | — | — | — | — | — | — |
| DST-06 | `GET /admin/payout-destinations/{destination_id}` `payout.view` | — | — | — | — | — | — | — | ✓ | ✓ | ✓ | — | — | — | — | — |
| DST-07 | `POST /admin/payout-destination-override-requests` `payout.destination.override.request` | — | — | — | — | — | — | — | ✓·J·SU·MC | ✓·J·SU·MC | — | — | — | — | — | — |
| DST-08 | `POST /admin/payout-destination-override-requests/{override_request_id}/approve` `payout.destination.override.approve` | — | — | — | — | — | — | — | — | — | ✓·J·SU·MC | — | — | — | — | — |
| DST-09 | `POST /admin/payout-destination-override-requests/{override_request_id}/reject` `payout.destination.override.approve` | — | — | — | — | — | — | — | — | — | ✓·J·MC | — | — | — | — | — |
| DST-10 | `POST /admin/payout-destinations/{destination_id}/account-reveal` `kyc.identity_number.reveal` | — | — | — | — | — | — | ✓·J·SU·dedupe | — | ✓·J·SU | — | — | — | — | — | — |

### Payout requests

| ID | Endpoint | ANON | USER | OWNER | ORGA | ORGM | REV | KYC_R | SUP | COMP | FIN | ADM | SEC_A | SUP_A | BUS_A | SYS |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| POR-01 | `GET /campaigns/{campaign_id}/payout-eligibility` | — | — | own | org | 403 | — | — | — | — | — | — | — | — | — | — |
| POR-02 | `POST /campaigns/{campaign_id}/payout-requests` | — | — | own | org | 403 | — | — | — | — | — | — | — | — | — | — |
| POR-03 | `GET /campaigns/{campaign_id}/payout-requests` | — | — | own | org | 403 | — | — | — | — | — | — | — | — | — | — |
| POR-04 | `GET /payout-requests/{payout_request_id}` | — | — | own | org | — | — | — | — | — | — | — | — | — | — | — |
| POR-05 | `POST /payout-requests/{payout_request_id}/cancel` | — | — | own | org | 403 | — | — | — | — | — | — | — | — | — | — |
| POR-06 | `GET /admin/payout-requests` `payout.view` | — | — | — | — | — | — | — | ✓ | ✓ | ✓ | — | — | — | — | — |
| POR-07 | `GET /admin/payout-requests/{payout_request_id}` `payout.view` | — | — | — | — | — | — | — | ✓ | ✓ | ✓ | — | — | — | — | — |
| POR-08 | `POST /admin/payout-requests/{payout_request_id}/approve` `payout.approve` | — | — | — | — | — | — | — | — | — | ✓·J·SU·MC | — | — | — | — | — |
| POR-09 | `POST /admin/payout-requests/{payout_request_id}/reject` `payout.reject` | — | — | — | — | — | — | — | — | — | ✓·J·SU | — | — | — | — | — |
| POR-10 | `POST /admin/payout-requests/{payout_request_id}/cancel` `payout.cancel` | — | — | — | — | — | — | — | — | ✓·J·SU | ✓·J·SU | — | — | — | — | — |
| POR-11 | `POST /admin/payout-requests/{payout_request_id}/eligibility-checks` `payout.view` | — | — | — | — | — | — | — | ✓ | ✓ | ✓ | — | — | — | — | — |
| POR-12 | `POST /admin/manual-payouts` `payout.approve` | — | — | — | — | — | — | — | — | — | ✓·J·SU·MC | — | — | — | — | — |

### Payout status

| ID | Endpoint | ANON | USER | OWNER | ORGA | ORGM | REV | KYC_R | SUP | COMP | FIN | ADM | SEC_A | SUP_A | BUS_A | SYS |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| PSS-01 | `GET /payout-requests/{payout_request_id}/status` | — | — | own | org | — | — | — | — | — | — | — | — | — | — | — |
| PSS-02 | `GET /payout-requests/{payout_request_id}/timeline` | — | — | own | org | — | — | — | — | — | — | — | — | — | — | — |
| PSS-03 | `GET /admin/payout-requests/{payout_request_id}/events` `payout.view` | — | — | — | — | — | — | — | ✓ | ✓ | ✓ | — | — | — | — | — |
| PSS-04 | `POST /admin/payout-requests/{payout_request_id}/status-checks` `payout.status.query` | — | — | — | — | — | — | — | — | — | ✓·J | — | — | — | — | — |

### KYC

| ID | Endpoint | ANON | USER | OWNER | ORGA | ORGM | REV | KYC_R | SUP | COMP | FIN | ADM | SEC_A | SUP_A | BUS_A | SYS |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| KYC-01 | `GET /me/verification` | — | own | own | own | own | — | — | — | — | — | — | — | — | — | — |
| KYC-02 | `POST /me/verification/consents` | — | own | own | own | own | — | — | — | — | — | — | — | — | — | — |
| KYC-03 | `POST /me/verification/attempts` | — | own | own | own | own | — | — | — | — | — | — | — | — | — | — |
| KYC-04 | `GET /me/verification/attempts/{attempt_id}` | — | own | own | own | own | — | — | — | — | — | — | — | — | — | — |
| KYC-05 | `PUT /me/verification/attempts/{attempt_id}/identity-data` | — | own | own | own | own | — | — | — | — | — | — | — | — | — | — |
| KYC-06 | `POST /me/verification/attempts/{attempt_id}/document-uploads` | — | own | own | own | own | — | — | — | — | — | — | — | — | — | — |
| KYC-07 | `POST /me/verification/attempts/{attempt_id}/documents` | — | own | own | own | own | — | — | — | — | — | — | — | — | — | — |
| KYC-08 | `POST /me/verification/attempts/{attempt_id}/submit` | — | own | own | own | own | — | — | — | — | — | — | — | — | — | — |
| KYC-09 | `POST /me/verification/attempts/{attempt_id}/liveness-sessions` | — | own | own | own | own | — | — | — | — | — | — | — | — | — | — |
| KYC-10 | `GET /admin/kyc/cases` `kyc.case.review` | — | — | — | — | — | — | ✓ | — | ✓ | — | — | — | — | — | — |
| KYC-11 | `GET /admin/kyc/cases/{case_id}` `kyc.case.review` | — | — | — | — | — | — | ✓ | — | ✓ | — | — | — | — | — | — |
| KYC-12 | `POST /admin/kyc/cases/{case_id}/claim` `kyc.case.review` | — | — | — | — | — | — | ✓ | — | ✓ | — | — | — | — | — | — |
| KYC-13 | `POST /admin/kyc/cases/{case_id}/decisions` `kyc.decision.record` | — | — | — | — | — | — | ✓·J·SU·MC | — | ✓·J·SU·MC | — | — | — | — | — | — |
| KYC-14 | `POST /admin/kyc/decisions/{decision_id}/approve` `kyc.decision.record` | — | — | — | — | — | — | ✓·J·SU·MC | — | ✓·J·SU·MC | — | — | — | — | — | — |
| KYC-15 | `POST /admin/kyc/cases/{case_id}/documents/{document_id}/view-sessions` `kyc.document.view` | — | — | — | — | — | — | ✓·J·SU | — | ✓·J·SU | — | — | — | — | — | — |
| KYC-16 | `POST /admin/kyc/cases/{case_id}/identity-reveals` `kyc.identity_number.reveal` | — | — | — | — | — | — | ✓·J·SU·dedupe | — | ✓·J·SU | — | — | — | — | — | — |
| KYC-17 | `GET /admin/users/{user_id}/verification` `kyc.status.view` | — | — | — | — | — | ✓ | ✓ | ✓·lvl | ✓ | ✓ | — | — | — | — | — |

### KYB

| ID | Endpoint | ANON | USER | OWNER | ORGA | ORGM | REV | KYC_R | SUP | COMP | FIN | ADM | SEC_A | SUP_A | BUS_A | SYS |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| KYB-01 | `GET /organisations/{org_id}/verification` | — | — | — | org | org | — | — | — | — | — | — | — | — | — | — |
| KYB-02 | `POST /organisations/{org_id}/verification/attempts` | — | — | — | org | 403 | — | — | — | — | — | — | — | — | — | — |
| KYB-03 | `POST /organisations/{org_id}/verification/attempts/{attempt_id}/document-uploads` | — | — | — | org | 403 | — | — | — | — | — | — | — | — | — | — |
| KYB-04 | `POST /organisations/{org_id}/verification/attempts/{attempt_id}/documents` | — | — | — | org | 403 | — | — | — | — | — | — | — | — | — | — |
| KYB-05 | `POST /organisations/{org_id}/verification/attempts/{attempt_id}/persons` | — | — | — | org | 403 | — | — | — | — | — | — | — | — | — | — |
| KYB-06 | `POST /organisations/{org_id}/verification/attempts/{attempt_id}/submit` | — | — | — | org | 403 | — | — | — | — | — | — | — | — | — | — |
| KYB-07 | `POST /organisations/{org_id}/fundraising-authorities` | — | — | — | org | 403 | — | — | — | — | — | — | — | — | — | — |
| KYB-08 | `GET /organisations/{org_id}/fundraising-authorities` | — | — | — | org | org | — | — | — | — | — | — | — | — | — | — |
| KYB-09 | `GET /admin/kyb/cases` `kyc.case.review` | — | — | — | — | — | — | ✓ | — | ✓ | — | — | — | — | — | — |
| KYB-10 | `GET /admin/kyb/cases/{case_id}` `kyc.case.review` | — | — | — | — | — | — | ✓ | — | ✓ | — | — | — | — | — | — |
| KYB-11 | `POST /admin/kyb/cases/{case_id}/decisions` `org.verification.decide` | — | — | — | — | — | — | ✓·J·SU·MC | — | ✓·J·SU·MC | — | — | — | — | — | — |
| KYB-12 | `POST /admin/kyb/cases/{case_id}/documents/{document_id}/view-sessions` `kyc.document.view` | — | — | — | — | — | — | ✓·J·SU | — | ✓·J·SU | — | — | — | — | — | — |

### Compliance (staff)

| ID | Endpoint | ANON | USER | OWNER | ORGA | ORGM | REV | KYC_R | SUP | COMP | FIN | ADM | SEC_A | SUP_A | BUS_A | SYS |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| CMPL-01 | `GET /admin/compliance/cases` `case.manage` | — | — | — | — | — | — | — | — | ✓ | — | — | — | — | — | — |
| CMPL-02 | `POST /admin/compliance/cases` `case.create` | — | — | — | — | — | ✓ | — | ✓ | ✓ | — | — | — | — | — | — |
| CMPL-03 | `GET /admin/compliance/cases/{case_id}` `case.manage` | — | — | — | — | — | — | — | — | ✓ | — | — | — | — | — | — |
| CMPL-04 | `POST /admin/compliance/cases/{case_id}/assignment` `case.manage` | — | — | — | — | — | — | — | — | ✓ | — | — | — | — | — | — |
| CMPL-05 | `POST /admin/compliance/cases/{case_id}/notes` `case.manage` | — | — | — | — | — | — | — | — | ✓ | — | — | — | — | — | — |
| CMPL-06 | `GET /admin/compliance/cases/{case_id}/notes` `case.manage` | — | — | — | — | — | — | — | — | ✓ | — | — | — | — | — | — |
| CMPL-07 | `POST /admin/compliance/cases/{case_id}/links` `case.manage` | — | — | — | — | — | — | — | — | ✓ | — | — | — | — | — | — |
| CMPL-08 | `POST /admin/compliance/cases/{case_id}/transitions` `case.manage` | — | — | — | — | — | — | — | — | ✓·J | — | — | — | — | — | — |
| CMPL-09 | `POST /admin/compliance/cases/{case_id}/decision-proposals` `case.manage` | — | — | — | — | — | — | — | — | ✓·J·SU·MC | — | — | — | — | — | — |
| CMPL-10 | `POST /admin/compliance/decision-proposals/{proposal_id}/approve` `case.manage` | — | — | — | — | — | — | — | — | ✓·J·SU·MC | — | — | — | — | — | — |
| CMPL-11 | `POST /admin/compliance/decision-proposals/{proposal_id}/reject` `case.manage` | — | — | — | — | — | — | — | — | ✓·J·MC | — | — | — | — | — | — |
| CMPL-12 | `GET /admin/compliance/screening-hits` `screening.review` | — | — | — | — | — | — | — | — | ✓ | — | — | — | — | — | — |
| CMPL-13 | `POST /admin/compliance/screening-hits/{hit_id}/disposition` `screening.review` | — | — | — | — | — | — | — | — | ✓·J·SU·MC | — | — | — | — | — | — |
| CMPL-14 | `POST /admin/compliance/restrictions` `compliance.restriction.request` | — | — | — | — | — | — | — | — | ✓·J·SU·MC | — | — | — | — | — | — |
| CMPL-15 | `POST /admin/compliance/restrictions/{restriction_id}/approve` `compliance.restriction.approve` | — | — | — | — | — | — | — | — | ✓·J·SU·MC | — | — | — | — | — | — |
| CMPL-16 | `POST /admin/compliance/restrictions/{restriction_id}/lift` `compliance.restriction.request` | — | — | — | — | — | — | — | — | ✓·J·SU·MC | — | — | — | — | — | — |
| CMPL-17 | `POST /admin/compliance/overrides` `compliance.override.request` | — | — | — | — | — | — | — | — | ✓·J·SU·MC | — | — | — | — | — | — |
| CMPL-18 | `POST /admin/compliance/overrides/{override_id}/approve` `compliance.override.approve` | — | — | — | — | — | — | — | — | ✓·J·SU·MC | — | — | — | — | — | — |
| CMPL-19 | `POST /admin/compliance/cases/{case_id}/confidentiality` `str.prepare` | — | — | — | — | — | — | — | — | ✓·J·SU | — | — | — | — | — | — |
| CMPL-20 | `POST /admin/compliance/str-reports` `str.prepare` | — | — | — | — | — | — | — | — | ✓·J·SU·MC | — | — | — | — | — | — |
| CMPL-21 | `POST /admin/compliance/str-reports/{str_id}/approve` `str.approve` | — | — | — | — | — | — | — | — | ✓·J·SU·MC | — | — | — | — | — | — |
| CMPL-22 | `POST /admin/evidence-holds` `evidence.hold` | — | — | — | — | — | — | — | — | ✓·J·SU·MC | — | — | — | — | — | — |

### Risk (staff)

| ID | Endpoint | ANON | USER | OWNER | ORGA | ORGM | REV | KYC_R | SUP | COMP | FIN | ADM | SEC_A | SUP_A | BUS_A | SYS |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| RSK-01 | `GET /admin/risk/holds` `hold.view` | — | — | — | — | — | ✓ | — | — | ✓ | ✓ | — | — | — | — | — |
| RSK-02 | `GET /admin/risk/holds/{hold_id}` `hold.view` | — | — | — | — | — | ✓ | — | — | ✓ | ✓ | — | — | — | — | — |
| RSK-03 | `POST /admin/risk/holds` `payout.hold` | — | — | — | — | — | ✓·J·SU·t | — | ✓·J·SU·t | ✓·J·SU·t | ✓·J·SU·t | — | — | — | — | — |
| RSK-04 | `POST /admin/risk/holds/{hold_id}/release` `hold.release` | — | — | — | — | — | ✓·J·SU·MC·t | — | — | ✓·J·SU·MC·t | ✓·J·SU·MC·t | — | — | — | — | — |
| RSK-05 | `POST /admin/risk/hold-release-requests/{release_request_id}/approve` `hold.release` | — | — | — | — | — | ✓·J·SU·MC·t | — | — | ✓·J·SU·MC·t | ✓·J·SU·MC·t | — | — | — | — | — |
| RSK-06 | `GET /admin/risk/limits` `limit.view` | — | — | — | — | — | — | — | — | ✓ | ✓ | — | — | — | — | — |
| RSK-07 | `POST /admin/risk/limit-change-requests` `limit.change.request` | — | — | — | — | — | — | — | — | ✓·J·SU·MC | ✓·J·SU·MC | — | — | — | — | — |
| RSK-08 | `POST /admin/risk/limit-change-requests/{limit_change_id}/approve` `limit.change.approve` | — | — | — | — | — | — | — | — | ✓·J·SU·MC | ✓·J·SU·MC | — | — | — | ✓·J·SU·MC | — |
| RSK-09 | `POST /admin/risk/limit-change-requests/{limit_change_id}/reject` `limit.change.approve` | — | — | — | — | — | — | — | — | ✓·J·MC | ✓·J·MC | — | — | — | ✓·J·MC | — |
| RSK-10 | `GET /admin/risk/alerts` `risk.alert.view` | — | — | — | — | — | ✓ | — | — | ✓ | — | — | — | — | — | — |
| RSK-11 | `POST /admin/risk/alerts/{alert_id}/disposition` `risk.alert.manage` | — | — | — | — | — | ✓·J | — | — | ✓·J | — | — | — | — | — | — |
| RSK-12 | `GET /admin/risk/assessments` `risk.assessment.view` | — | — | — | — | — | ✓ | — | — | ✓ | — | — | — | — | — | — |

### Admin operations

| ID | Endpoint | ANON | USER | OWNER | ORGA | ORGM | REV | KYC_R | SUP | COMP | FIN | ADM | SEC_A | SUP_A | BUS_A | SYS |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| ADM-01 | `GET /admin/users` `user.view` | — | — | — | — | — | — | — | ✓ | ✓ | — | ✓ | — | — | — | — |
| ADM-02 | `GET /admin/users/{user_id}` `user.view` | — | — | — | — | — | — | — | ✓ | ✓ | — | ✓ | — | — | — | — |
| ADM-03 | `POST /admin/users/{user_id}/suspend` `user.suspend` | — | — | — | — | — | — | — | — | ✓·J·SU | — | — | — | — | — | — |
| ADM-04 | `POST /admin/users/{user_id}/sessions/revoke` `session.revoke` | — | — | — | — | — | — | — | — | — | — | — | ✓·J | — | — | — |
| ADM-05 | `POST /admin/users/{user_id}/account-recovery` `account.recovery.assist` | — | — | — | — | — | — | — | ✓·J·SU | — | — | — | — | — | — | — |
| ADM-06 | `GET /admin/organisations/{org_id}` `organisation.view` | — | — | — | — | — | ✓ | — | ✓ | ✓ | ✓ | ✓ | — | — | — | — |
| ADM-07 | `GET /admin/staff` `role.view` | — | — | — | — | — | — | — | — | — | — | — | ✓ | ✓ | — | — |
| ADM-08 | `POST /admin/role-assignment-requests` `role.assign.request` | — | — | — | — | — | — | — | — | — | — | — | — | ✓·J·SU·MC | — | — |
| ADM-09 | `POST /admin/role-assignment-requests/{role_request_id}/approve` `role.assign.approve` | — | — | — | — | — | — | — | — | — | — | — | — | ✓·J·SU·MC | — | — |
| ADM-10 | `POST /admin/role-assignment-requests/{role_request_id}/reject` `role.assign.approve` | — | — | — | — | — | — | — | — | — | — | — | — | ✓·J·MC | — | — |
| ADM-11 | `POST /admin/role-assignments/{role_assignment_id}/revoke` `role.revoke` | — | — | — | — | — | — | — | — | — | — | — | ✓·J·SU | ✓·J·SU | — | — |
| ADM-12 | `POST /admin/staff/{user_id}/suspend` `staff.suspend` | — | — | — | — | — | — | — | — | — | — | — | ✓·J·SU | — | — | — |
| ADM-13 | `GET /admin/fee-schedules` `fee.config.request` | — | — | — | — | — | — | — | — | — | ✓ | — | — | — | — | — |
| ADM-14 | `POST /admin/fee-schedule-change-requests` `fee.config.request` | — | — | — | — | — | — | — | — | — | ✓·J·SU·MC | — | — | — | — | — |
| ADM-15 | `POST /admin/fee-schedule-change-requests/{fee_change_id}/approve` `fee.config.approve` | — | — | — | — | — | — | — | — | — | — | — | — | — | ✓·J·SU·MC | — |
| ADM-16 | `GET /admin/killswitches` `killswitch.activate` | — | — | — | — | — | — | — | — | — | ✓ | — | ✓ | — | — | — |
| ADM-17 | `POST /admin/killswitches/{killswitch_name}/activate` `killswitch.activate` | — | — | — | — | — | — | — | — | — | ✓·J·SU | — | ✓·J·SU | — | — | — |
| ADM-18 | `POST /admin/killswitches/{killswitch_name}/deactivation-requests` `killswitch.deactivate.request` | — | — | — | — | — | — | — | — | — | ✓·J·SU·MC | — | ✓·J·SU·MC | — | — | — |
| ADM-19 | `POST /admin/feature-flag-changes/{flag_change_id}/approve` `killswitch.deactivate.approve` | — | — | — | — | — | — | — | — | — | ✓·J·SU·MC | — | ✓·J·SU·MC | — | — | — |
| ADM-20 | `GET /admin/providers` `provider.view` | — | — | — | — | — | — | — | — | — | ✓ | ✓ | — | — | — | — |
| ADM-21 | `GET /admin/webhook-dead-letters` `webhook.deadletter.manage` | — | — | — | — | — | — | — | — | — | ✓ | — | — | — | — | — |
| ADM-22 | `POST /admin/webhook-dead-letters/{dead_letter_id}/requeue` `webhook.deadletter.manage` | — | — | — | — | — | — | — | — | — | ✓·J | — | — | — | — | — |
| ADM-23 | `GET /admin/feature-flags` `config.change.request` | — | — | — | — | — | — | — | — | — | — | ✓ | — | — | — | — |
| ADM-24 | `POST /admin/feature-flag-changes` `config.change.request` | — | — | — | — | — | — | — | — | — | — | ✓·J·SU·MC | — | — | — | — |
| ADM-25 | `POST /admin/breakglass-sessions` `killswitch.activate` | — | — | — | — | — | — | — | — | — | ✓·J·SU | — | ✓·J·SU | — | — | — |
| ADM-27 | `POST /admin/review-policy-change-requests` `campaign.review_policy.request` | — | — | — | — | — | — | — | — | ✓·J·SU·MC | — | — | — | — | — | — |
| ADM-28 | `POST /admin/review-policy-change-requests/{policy_change_id}/approve` `campaign.review_policy.approve` | — | — | — | — | — | — | — | — | — | — | — | — | — | ✓·J·SU·MC | — |
| ADM-26 | `POST /admin/access-reviews` `access.review.run` | — | — | — | — | — | — | — | — | — | — | — | ✓ | — | — | — |

### Notifications

| ID | Endpoint | ANON | USER | OWNER | ORGA | ORGM | REV | KYC_R | SUP | COMP | FIN | ADM | SEC_A | SUP_A | BUS_A | SYS |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| NTF-01 | `GET /me/notification-preferences` | — | own | own | own | own | — | — | — | — | — | — | — | — | — | — |
| NTF-02 | `PUT /me/notification-preferences` | — | own | own | own | own | — | — | — | — | — | — | — | — | — | — |
| NTF-03 | `POST /notifications/unsubscribe` | token | token | token | token | token | — | — | — | — | — | — | — | — | — | — |
| NTF-04 | `GET /admin/notification-templates` `staff.support` | — | — | — | — | — | — | — | — | — | — | ✓ | — | — | — | — |
| NTF-05 | `GET /admin/users/{user_id}/notification-attempts` `user.view` | — | — | — | — | — | — | — | ✓ | ✓ | — | ✓ | — | — | — | — |

### Reports

| ID | Endpoint | ANON | USER | OWNER | ORGA | ORGM | REV | KYC_R | SUP | COMP | FIN | ADM | SEC_A | SUP_A | BUS_A | SYS |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| RPT-01 | `POST /admin/reports` `report.generate` | — | — | — | — | — | — | — | — | ✓·J | ✓·J | — | — | — | — | — |
| RPT-02 | `GET /admin/reports` `report.generate` | — | — | — | — | — | — | — | — | ✓ | ✓ | — | — | — | — | — |
| RPT-03 | `GET /admin/reports/{report_id}` `report.generate` | — | — | — | — | — | — | — | — | ✓·J | ✓·J | — | — | — | — | — |

### Reconciliation (staff)

| ID | Endpoint | ANON | USER | OWNER | ORGA | ORGM | REV | KYC_R | SUP | COMP | FIN | ADM | SEC_A | SUP_A | BUS_A | SYS |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| REC-01 | `GET /admin/reconciliation/sources` `reconciliation.run` | — | — | — | — | — | — | — | — | — | ✓ | — | — | — | — | — |
| REC-02 | `POST /admin/reconciliation/imports` `reconciliation.run` | — | — | — | — | — | — | — | — | — | ✓ | — | — | — | — | — |
| REC-03 | `GET /admin/reconciliation/imports/{import_id}` `reconciliation.run` | — | — | — | — | — | — | — | — | — | ✓ | — | — | — | — | — |
| REC-04 | `POST /admin/reconciliation/runs` `reconciliation.run` | — | — | — | — | — | — | — | — | — | ✓ | — | — | — | — | — |
| REC-05 | `GET /admin/reconciliation/runs` `reconciliation.run` | — | — | — | — | — | — | — | — | — | ✓ | — | — | — | — | — |
| REC-06 | `GET /admin/reconciliation/runs/{run_id}` `reconciliation.run` | — | — | — | — | — | — | — | — | — | ✓ | — | — | — | — | — |
| REC-07 | `GET /admin/reconciliation/discrepancies` `reconciliation.resolve` | — | — | — | — | — | — | — | — | — | ✓ | — | — | — | — | — |
| REC-08 | `GET /admin/reconciliation/discrepancies/{discrepancy_id}` `reconciliation.resolve` | — | — | — | — | — | — | — | — | — | ✓ | — | — | — | — | — |
| REC-09 | `POST /admin/reconciliation/discrepancies/{discrepancy_id}/resolutions` `reconciliation.resolve` | — | — | — | — | — | — | — | — | — | ✓·J·SU·MC | — | — | — | — | — |
| REC-10 | `POST /admin/reconciliation/resolutions/{resolution_id}/approve` `reconciliation.resolve` | — | — | — | — | — | — | — | — | — | ✓·J·SU·MC | — | — | — | — | — |
| REC-11 | `GET /admin/settlement-batches` `reconciliation.run` | — | — | — | — | — | — | — | — | — | ✓ | — | — | — | — | — |
| REC-12 | `GET /admin/settlement-batches/{batch_id}` `reconciliation.run` | — | — | — | — | — | — | — | — | — | ✓ | — | — | — | — | — |
| REC-13 | `GET /admin/ledger/accounts/{account_id}/balance` `ledger.view` | — | — | — | — | — | — | — | — | — | ✓ | — | — | — | — | — |
| REC-14 | `GET /admin/ledger/transactions/{transaction_id}` `ledger.view` | — | — | — | — | — | — | — | — | — | ✓ | — | — | — | — | — |
| REC-15 | `GET /admin/ledger/invariant-runs` `ledger.view` | — | — | — | — | — | — | — | — | — | ✓ | — | — | — | — | — |
| REC-16 | `POST /admin/ledger/adjustments` `ledger.adjustment.create` | — | — | — | — | — | — | — | — | — | ✓·J·SU·MC | — | — | — | — | — |
| REC-17 | `POST /admin/ledger/adjustments/{adjustment_id}/approve` `ledger.adjustment.approve` | — | — | — | — | — | — | — | — | — | ✓·J·SU·MC | — | — | — | — | — |
| REC-18 | `GET /admin/ledger/adjustments` `ledger.view` | — | — | — | — | — | — | — | — | — | ✓ | — | — | — | — | — |

### Audit access (staff)

| ID | Endpoint | ANON | USER | OWNER | ORGA | ORGM | REV | KYC_R | SUP | COMP | FIN | ADM | SEC_A | SUP_A | BUS_A | SYS |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| AUD-01 | `GET /admin/audit/events` `audit.read` | — | — | — | — | — | — | — | — | ✓·scoped | ✓·scoped | — | ✓·scoped | ✓·scoped | — | — |
| AUD-02 | `GET /admin/audit/events/{event_id}` `audit.read` | — | — | — | — | — | — | — | — | ✓·scoped | ✓·scoped | — | ✓·scoped | ✓·scoped | — | — |
| AUD-03 | `GET /admin/audit/security-events` `security_audit.read` | — | — | — | — | — | — | — | — | — | — | — | ✓ | ✓ | — | — |
| AUD-04 | `GET /admin/audit/chain-verifications` `audit.read` | — | — | — | — | — | — | — | — | ✓·scoped | ✓·scoped | — | ✓·scoped | ✓·scoped | — | — |
| AUD-05 | `GET /admin/evidence-records/{evidence_record_id}` `evidence.view` | — | — | — | — | — | — | — | — | ✓ | ✓ | — | — | — | — | — |
| AUD-06 | `POST /admin/audit/exports` `audit.export` | — | — | — | — | — | — | — | — | ✓·J·SU·MC | — | — | — | — | — | — |

### Webhooks

| ID | Endpoint | ANON | USER | OWNER | ORGA | ORGM | REV | KYC_R | SUP | COMP | FIN | ADM | SEC_A | SUP_A | BUS_A | SYS |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| WHK-01 | `POST /webhooks/{provider}` | — | — | — | — | — | — | — | — | — | — | — | — | — | — | sig |
| WHK-03 | `POST /webhooks/screening/{vendor}` | — | — | — | — | — | — | — | — | — | — | — | — | — | — | sig |
| WHK-02 | `POST /webhooks/kyc-vendors/{vendor}` | — | — | — | — | — | — | — | — | — | — | — | — | — | — | sig |

### Health

| ID | Endpoint | ANON | USER | OWNER | ORGA | ORGM | REV | KYC_R | SUP | COMP | FIN | ADM | SEC_A | SUP_A | BUS_A | SYS |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| HLT-01 | `GET /healthz` | — | — | — | — | — | — | — | — | — | — | — | — | — | — | ✓ |
| HLT-02 | `GET /readyz` | — | — | — | — | — | — | — | — | — | — | — | — | — | — | ✓ |

