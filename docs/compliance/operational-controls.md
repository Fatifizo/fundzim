# FundZim Operational Controls and Separation of Duties (Stage 1 design)

Status: **Stage 1 design.** It refines [SECURITY.md](../SECURITY.md) §5 and [PRODUCT.md](../PRODUCT.md) §4
into an operating model for staff. Decision record:
[ADR-017](../adr/ADR-017-payout-approval-segregation-of-duties.md). Permissions are implemented in Stage 4
(authorisation framework) and Stage 14 (admin portal); maker-checker is enforced in code **and** by database
checks (`approved_by <> requested_by`).

Related: [SECURITY.md](../SECURITY.md), [AUDIT.md](../AUDIT.md),
[audit-evidence-model.md](audit-evidence-model.md), [compliance-case-management.md](compliance-case-management.md),
[payout-eligibility-and-controls.md](../payments/payout-eligibility-and-controls.md),
[incident-response-workflows.md](incident-response-workflows.md),
[campaign-approval-policy.md](campaign-approval-policy.md).

---

## 1. Operational functions mapped to roles

Functions are **permission bundles**. A role is a named set of bundles. The Stage 0 roles stay; Stage 1 adds
two roles: `KYC_REVIEWER` and `SECURITY_ADMIN`.

| Function | Role | Key permissions | Explicitly excluded |
|---|---|---|---|
| Campaign reviewer | `REVIEWER` | `campaign.review`, `campaign.decide`, `campaign.suspend`, `campaign.unsuspend`, `case.create` | KYC documents, payouts, refunds, ledger, freeze |
| KYC reviewer | `KYC_REVIEWER` (new) | `kyc.case.review`, `kyc.document.view` (justified, case-bound), `kyc.decision.record`, `beneficiary.verification.decide`, `org.verification.decide` (non-final for HIGH risk) | Payouts, refunds, ledger, campaign decisions, role management |
| Compliance officer | `COMPLIANCE` | `case.manage`, `campaign.freeze`, `campaign.unfreeze.request`, `payout.hold`, `kyc.document.view` (justified), `kyc.identity_number.reveal` (justified), `compliance.override.request` / `.approve`, `str.prepare` (LR-008), `evidence.hold`, `refund.request`, `audit.read` (scoped) | Approving payouts, ledger adjustments, role grants |
| Finance operator | `FINANCE` | `reconciliation.run`, `reconciliation.resolve`, `ledger.adjustment.create`, `ledger.adjustment.approve` (different person), `refund.approve`, `campaign.freeze`, `fee.config.request` | KYC documents, campaign decisions, role grants |
| Payout approver | `FINANCE` + `payout.approve` | `payout.approve`, `payout.reject`, `payout.destination.override.approve` | Approving a payout they requested or initiated; approving for a campaign they are connected to |
| Support agent | `SUPPORT` | `user.view` (masked), `campaign.view`, `refund.request`, `case.create`, `account.recovery.assist` (step-up gated) | Unmasking anonymous donors, KYC documents, money movement approvals, changing payout destinations |
| Security administrator | `SECURITY_ADMIN` (new) | `access.review.run`, `session.revoke`, `staff.suspend`, `killswitch.activate` (security scopes), `secret.rotation.trigger`, `security.alert.manage`, `audit.read` (role/admin actions) | Any KYC data, any financial data, payouts, refunds, ledger, campaign decisions |
| Platform administrator | `ADMIN` | `config.change.request`, `content.moderate`, `staff.support` | KYC documents, ledger, payout approval, role grants |
| Role administrator | `SUPER_ADMIN` | `role.assign.request`, `role.assign.approve` (different SUPER_ADMIN) | Self-grants; sensitive permissions by default ([SECURITY.md](../SECURITY.md) §5.2) |

Staff accounts are separate from personal donor/owner accounts. A staff member who also has a personal
FundZim account can never act, as staff, on records connected to that personal account (enforced by the
conflict check, §3).

---

## 2. Separation-of-duties matrix

Pairs marked ✗ must not be held by the same person **for the same object** (and, where noted, at all).
Enforcement: the role assignment workflow rejects role combinations marked **R**; object-level conflicts
marked **O** are checked at action time.

| | Request payout | Approve payout | Create ledger adj. | Approve ledger adj. | Request refund | Approve refund | KYC decision | Campaign decision | Freeze/hold | Role grant | Change payout destination (staff) |
|---|---|---|---|---|---|---|---|---|---|---|---|
| **Approve payout** | ✗ O | — | | | | | | | | ✗ R | ✗ O |
| **Approve ledger adjustment** | | | ✗ O | — | | | | | | ✗ R | |
| **Approve refund** | | | | | ✗ O | — | | | | | |
| **Approve role grant** | | ✗ R | | ✗ R | | | ✗ R | | | ✗ O (own grant) | |
| **KYC decision** | | ✗ O (same subject) | | | | | — | ✗ O (HIGH tier: KYC and campaign decided by different people) | | | |
| **Security admin** | ✗ R | ✗ R | ✗ R | ✗ R | ✗ R | ✗ R | ✗ R | ✗ R | | | ✗ R |
| **Unfreeze approve** | | | | | | | | | ✗ O (whoever froze or requested unfreeze cannot approve) | | |

Additional conflicts (object level, all functions): a staff member may not act on a campaign, user,
organisation or payout where they declared a relationship, where they are a donor above a configured amount
to that campaign, or where they are the beneficiary. Staff declare conflicts in their profile; reviewers
attest on claim ([campaign-approval-policy.md](campaign-approval-policy.md) §8.1).

---

## 3. Maker-checker policy (initial)

All approvals are **pending objects** with an expiry. The checker sees the full request (diff, amounts,
evidence links) and must enter a justification. Approval is a separate authenticated action with **step-up
authentication** (fresh MFA within `STEP_UP_MAX_AGE`, [SECURITY.md](../SECURITY.md) §6).

| Action | Maker | Checker | Constraints | Request expiry |
|---|---|---|---|---|
| Payout above threshold or risk-flagged (LR-030) | System (owner request) / FINANCE (manual payout) | FINANCE with `payout.approve` | Checker ≠ maker, ≠ anyone who changed the destination in the last cooling-off period; eligibility re-checked at approval and before submission | 72 h → returns to `PENDING_REVIEW` |
| Payout destination override by staff | SUPPORT / COMPLIANCE (with user request evidence) | FINANCE with `payout.destination.override.approve` | Triggers cooling-off and owner notification on all channels; evidence required | 24 h |
| Refund (any) | SUPPORT / COMPLIANCE | FINANCE with `refund.approve` | Checker ≠ maker; original rail only | 72 h |
| Bulk refund (cancelled/fraud campaign) | COMPLIANCE | FINANCE + business owner above a configured total | Batch manifest hash recorded; each refund still idempotent | 72 h |
| Ledger adjustment or reversal | FINANCE | Different FINANCE | Reason code, linked mismatch/case, balanced transaction preview | 24 h |
| Write-off to loss accounts | FINANCE | Business owner or designated officer above threshold | Recovery case closed or documented as unrecoverable | 7 d |
| Campaign unfreeze | COMPLIANCE | Different COMPLIANCE or designated officer | Case resolution recorded | 72 h |
| Compliance override / exemption (e.g. bypass a check, raise a limit for one subject) | COMPLIANCE | Different COMPLIANCE or designated officer | **Mandatory expiry** (max configurable, e.g. 30 days); scope = single subject; cannot override sanctions BLOCK | 24 h; the override itself expires automatically |
| Limit or threshold change (REGULATORY / PROVIDER / INTERNAL_RISK) | COMPLIANCE or FINANCE | Approval owner named on the limit | Source citation required; effective date; review date | 7 d |
| Fee configuration change | FINANCE | Business owner | Versioned config; effective in the future, never retroactive | 7 d |
| Campaign review policy change | COMPLIANCE | Business owner or designated officer | New version; existing reviews keep their pinned version | 7 d |
| Role or permission grant | SUPER_ADMIN | Different SUPER_ADMIN | No self-grant; SoD combinations (§2) rejected | 24 h |
| Kill switch deactivation (payout freeze lifted) | SECURITY_ADMIN / FINANCE | Different senior staff | Incident status recorded | — |

Expired requests are closed as `EXPIRED` (audit event), never auto-approved. A single person can never
complete both steps, even with two different accounts: staff identity is bound to a verified person record,
and duplicate staff identities are an access-review finding.

---

## 4. Controls against specific abuse

### 4.1 Self-approval

- Code: approval services compare `checker_person_id` with every maker or initiator id on the object,
  including the owner of the campaign if the staff member has a personal account.
- Database: `CHECK (approved_by <> requested_by)` on approval tables; payout approval rows also store the
  destination-change actor and check against it.
- Denied attempts recorded as `outcome='denied'` audit events and alerted on repetition
  ([AUDIT.md](../AUDIT.md) §9).

### 4.2 Unauthorised payout changes

- Payout destinations can be changed only by the verified owner (step-up auth) or by staff override under
  maker-checker (§3).
- Any destination change → payout hold + cooling-off + notification to the owner's verified phone and email
  (old and new contact) ([payout-eligibility-and-controls.md](../payments/payout-eligibility-and-controls.md)).
- Payouts snapshot the destination at approval; the submitted destination must match the approved snapshot
  hash, otherwise submission fails closed.
- Provider dashboards: direct payouts from a PSP merchant portal bypass FundZim controls. Portal access is
  limited to named FINANCE staff, MFA-enforced, and portal-initiated payouts are detected by reconciliation as
  unmatched outgoing transactions → SEV1 ([incident-response-workflows.md](incident-response-workflows.md) §6).
  `PCR-021 — provider portal user roles and payout permissions`.

### 4.3 Silent ledger modifications

- App role cannot UPDATE/DELETE/TRUNCATE ledger journal tables; triggers block it; adjustments are new
  balanced transactions under maker-checker ([LEDGER.md](../LEDGER.md) §4).
- Direct DB access to production: no standing human write access. Break-glass DB access (§5) is read-only by
  default; write access requires incident commander approval, session recording and post-review.
- Nightly full balance recompute and audit hash-chain verification; any discrepancy is SEV1.

### 4.4 Abuse of administrative overrides

- Overrides are scoped to one subject, time-limited, cannot bypass sanctions blocks or legal holds, and
  appear on a weekly override report reviewed by the business owner.
- Override volume per staff member is monitored; outliers trigger an access review.

### 4.5 Unreviewed compliance exemptions

- Every exemption has an expiry and a named reviewer; expiry produces a task, not silent renewal.
- Renewal is a new maker-checker request with fresh justification.
- A monthly report lists active exemptions, their age and renewal count.

---

## 5. Break-glass

Follows [SECURITY.md](../SECURITY.md) §5.5, with operational detail:

| Aspect | Rule |
|---|---|
| Who may invoke | On-call SECURITY_ADMIN, FINANCE lead or COMPLIANCE lead, with a linked incident |
| What it grants | One named permission (e.g. `payout.freeze.global`, `db.read.production`) for ≤ 1 hour by default |
| Never grants | KYC document bulk access, ledger write, self-approval, role administration |
| Alerts | Security owner + a second senior staff member immediately |
| Afterwards | Every action reviewed within 2 business days by someone other than the user; findings recorded on the incident |

---

## 6. Periodic access reviews

| Review | Frequency (provisional) | Reviewer | Output |
|---|---|---|---|
| Staff role assignments vs job function | Quarterly; also on every role change and departure | SECURITY_ADMIN prepares; business owner approves | Revocations, attestation record |
| Sensitive access logs (`kyc.document.viewed`, identity number reveals, donor unmasking) | Monthly | COMPLIANCE lead (not their own views) | Justification spot checks; anomalies → case |
| Maker-checker statistics (approval latency, rubber-stamping, pairs that always approve each other) | Monthly | Business owner | Rotation of checker pairs if needed |
| Provider portal users | Quarterly | FINANCE lead + SECURITY_ADMIN | Portal access list matches staff list |
| Active overrides/exemptions and break-glass usage | Monthly | Business owner | Expiries enforced |
| Service account and API credential inventory | Quarterly | SECURITY_ADMIN | Rotation status |

Leavers: access revoked within the same business day; sessions revoked; provider portal access removed;
pending approvals reassigned.

Staff vetting before granting COMPLIANCE, FINANCE, KYC_REVIEWER or SECURITY_ADMIN — whether background
screening is required or permitted, and how — is **LR-089** (new, below).

---

## 7. New legal question (introduced here)

| ID | Area | Question | Why it matters | Impact | Owner | Status |
|---|---|---|---|---|---|---|
| LR-089 | Staff vetting | Are background or criminal-record checks required or permitted for staff with access to KYC data, payouts or ledger controls, and what consent and data-protection conditions apply? | Insider threat (T-14, T-15, T-18) is a top financial risk | Hiring process, role grant preconditions | Counsel | OPEN |
