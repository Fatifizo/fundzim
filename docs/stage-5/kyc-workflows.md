# Stage 5 — Individual KYC Workflows and the Review Engine

> Code: `internal/kyc` (cases, review, policy, views), `internal/verification` (HTTP), migration
> `20261009150200_kyc.sql`. Vocabulary: [ADR-034](../adr/ADR-034-verification-state-models.md). Design inputs:
> [compliance/kyc-architecture.md](../compliance/kyc-architecture.md) (ADR-015). Nothing in this document is a
> legal conclusion: requirements are **internal policy** (configuration), not statements of what the law
> requires.

## 1. Profile: level and status are separate

`kyc.verification_profiles` holds one profile per user: **level** (`UNVERIFIED` → `BASIC_VERIFIED` →
`IDENTITY_VERIFIED` → `PAYOUT_VERIFIED`) and **status** (`ACTIVE`, `PENDING_REVIEW`, `REJECTED`,
`SUSPENDED`, guarded by the `kyc_verification_status` machine). A level is only meaningful with status
`ACTIVE`. `GET /kyc/status` returns level, status, risk level, expiry, the current case and gates
(`create_draft`, `submit_campaign`; `withdraw` is always `false` — no payouts exist).

**`BASIC_VERIFIED` is never granted in Stage 5.** [kyc-architecture.md](../compliance/kyc-architecture.md)
defines it as EMAIL_OTP + PHONE_OTP + AGE_ATTESTATION + a device/IP risk result that is not BLOCK. Stage 4
captures no age attestation and there is no device-risk check, so granting it from email and phone alone
would fabricate a verification. Consequently `/kyc/status` shows `UNVERIFIED` until an identity approval,
and the `create_draft` gate is `false` for everyone who is not `IDENTITY_VERIFIED`. Stage 6 must add
age-attestation capture (LR-021 open) and decide the device-risk input, or change the gate by ADR
(KI-S5-17, handover).

`PAYOUT_VERIFIED` is granted only by the worker consumer `kyc.payout_ownership` when an
`IDENTITY_VERIFIED`, `ACTIVE` user's **own** payout destination is verified by a reviewer; ownership alone
grants nothing. The `app.users` columns `kyc_level`/`kyc_status` are a read-only mirror written from
`kyc.level_changed` events (ordered by event time, so a late duplicate never regresses it); the `kyc`
schema stays authoritative.

## 2. Case lifecycle (`verification_case` machine)

```
DRAFT ─submit─▶ SUBMITTED ─start-review─▶ UNDER_REVIEW ─approve─▶ APPROVED ─▶ EXPIRED | SUSPENDED | REVOKED
  │                │                        │  ├─request-info─▶ ADDITIONAL_INFORMATION_REQUIRED ─resubmit─▶ SUBMITTED
  └─withdraw─▶ WITHDRAWN                    │  ├─escalate─▶ ESCALATED ─return─▶ UNDER_REVIEW
                                            │  └─reject─▶ REJECTED (─reopen─▶ UNDER_REVIEW)
                                 SUSPENDED ─reinstate─▶ APPROVED | ─revoke─▶ REVOKED
```

The Go edge list (`internal/platform/verifcase`) is compared with the migration's `app.status_transitions`
rows by a unit test; the database rejects any other transition.

## 3. Applicant flow

1. **Prerequisites:** verified email **and** verified phone (`422 PREREQUISITES_NOT_MET`). One open case per
   user (`409 CASE_ALREADY_OPEN`, also a unique index). `target_level` is `IDENTITY_VERIFIED`.
2. **Draft:** `PATCH /kyc/cases/{id}` (DRAFT or ADDITIONAL_INFORMATION_REQUIRED only) with optimistic
   concurrency (`If-Match: "<version>"`). Names are trimmed and refuse markup; nationality/residence are ISO-2;
   ID types `ZW_NATIONAL_ID`, `ZW_PASSPORT`, `FOREIGN_PASSPORT`. The ID number is checked for **format sanity
   only** (letters, digits, separators). No national-ID structure is enforced as a legal rule — the reviewer
   checks the document. The draft is AES-256-GCM encrypted on the case row. Responses show only a masked
   number (last two characters).
3. **Documents:** `POST /verification/documents` (see [document-security.md](document-security.md)).
   Requirements come from the active policy for the case's risk level (STANDARD/LOW: one of the three ID
   document types + `SELFIE`; ENHANCED/RESTRICTED additionally `PROOF_OF_ADDRESS`). Only `CLEAN` documents
   satisfy a requirement.
4. **Submit:** completeness check (missing fields, `UNDERAGE` against the policy's `adult_age`,
   `DOCUMENT_EXPIRED`, required CLEAN documents) → `422 SUBMISSION_INCOMPLETE` with `details`. On success the
   identity is **frozen** into `kyc.identities` (status PENDING; HMAC blind index over the normalised number)
   and the case moves to SUBMITTED. Re-submitting a SUBMITTED case is an idempotent no-op (same version).
5. **Duplicate identity:** if the same document type + number exists as an ACTIVE or PENDING account-holder
   identity on another profile, the case is **not** auto-rejected. It is raised to `ENHANCED` with
   `requires_second_approval`, a `kyc.duplicate_identity_detected` event feeds the risk module, and on
   approval the unique "one ACTIVE identity per number" index refuses activation
   (`409 DUPLICATE_IDENTITY_ACTIVE`) — two accounts can never both hold the same verified identity.
6. **After submission** documents cannot be added or removed (`409 CASE_NOT_EDITABLE`; attach happens under
   the case row lock, so an upload racing a submit either lands first or is refused). The applicant may
   withdraw an open case.

## 4. Review engine

Routes: `/admin/verification/cases[...]` (queue and detail need `kyc.case.review`). Actions:

| Action | Rule |
|---|---|
| `assign` (ASSIGN_CASE) | Self by default; another assignee must be active staff holding `kyc.case.review` (`422 ASSIGNEE_NOT_ELIGIBLE`) and must not be conflicted |
| `start-review` (START_REVIEW) | Assigned reviewer; SUBMITTED → UNDER_REVIEW |
| `request-info` (REQUEST_INFORMATION) | Assigned reviewer; message + items; the case returns to the applicant; unanswered requests expire after the policy's `information_request_expiry_days` (30) |
| `approve` / `reject` (APPROVE / REJECT) | Type-specific permission (`kyc.decision.record`, `org.verification.decide`, `beneficiary.verification.decide`, `payout_destination.verify`) **and a fresh step-up**; assigned reviewer; `reason_code` (UPPER_SNAKE) and a non-empty `note` required; rejections need a `user_message` |
| `second-approval` | Four-eyes cases only; a different, unconflicted reviewer (`403 SECOND_APPROVER_MUST_DIFFER`; also CHECK + trigger on `kyc_decisions`) |
| `escalate` / `return` (ESCALATE) | Escalation emits `kyc.case_escalated`, which opens or links a compliance case |
| `suspend`, `reinstate`, `revoke`, `reopen` (SUSPEND / REOPEN) | Decision permission + step-up; post-approval actions may be taken by any authorised, unconflicted reviewer |

**Self-review is impossible:** a reviewer can never be assigned to or decide a case about themselves. The
check covers the reviewer's staff account **and** the personal account linked to it
(`app.users.staff_personal_user_id`), organisation membership (KYB), and the creator/submitter. A database
trigger on `kyc.kyc_decisions` independently refuses a decision by the case subject.

**Four eyes:** approvals of cases with `requires_second_approval` (ENHANCED/RESTRICTED risk, or policy)
and every rejection with a `FRAUD_*` reason code record a *first approval* (`202`,
`awaiting_second_approval: true`) and take effect only after `second-approval`.

**Concurrency:** decisions lock the case row and check status; exactly one of two concurrent conflicting
decisions succeeds (tested: approve vs reject → one 200, one 409, one decision row; case events stay one per
version).

**Screening:** every approval records `APPROVE_WITH_CONDITIONS` with
`sanctions_pep_screening: NOT_PERFORMED_SCREENING_PROVIDER_NOT_SELECTED` — LEGAL_REVIEW_REQUIRED (LR-009,
LR-053, LR-064).

**Case detail** (`GET /admin/verification/cases/{id}`) shows the masked case view, documents (metadata),
checks, history, encrypted notes (reviewers only), the subject summary, a `review` object
(`risk_level`, `requires_second_approval`, `pending_outcome`, `pending_decided_by`) and, for holders of
`risk.view`, the latest risk assessment. The full ID number is available only through
`POST …/reveal-identity-number` (`kyc.identity_number.reveal` — COMPLIANCE only — with step-up, a
justification of at least 10 characters and a security audit event).

**Queue:** `GET /admin/verification/cases?type=&status=&assigned=me|unassigned|any&limit=&cursor=` merges
KYC, KYB, beneficiary and payout-destination work, oldest first, with an opaque keyset cursor
`<submitted_at>|<id>` (no item is skipped or repeated at page boundaries, including timestamp ties).

## 5. Approval effects

In one transaction: the identity becomes ACTIVE (unique per number), checks and the decision are recorded
(with evidence references and the policy version), the profile moves to `IDENTITY_VERIFIED`/`ACTIVE` with
`expires_at = now + validity_days` (730 for LOW/STANDARD, 365 ENHANCED, 180 RESTRICTED), and
`kyc.case_approved` + `kyc.level_changed` are written to the outbox.

## 6. Expiry and rechecks

The worker sweep (every 15 minutes, `verification.sweep`) expires APPROVED cases past `expires_at` (profile
back to UNVERIFIED, identity INACTIVE) and ADDITIONAL_INFORMATION_REQUIRED cases whose request has gone
unanswered past the policy window; beneficiaries and payout destinations have equivalent sweeps. Expiry
emits notifications. Documents in FAILED_SCAN are rescanned every 30 seconds with backoff.

## 7. Verification policy (versioned, maker-checker)

`kyc.verification_policies` holds versioned JSON rules: per-risk KYC requirements, KYB rules by organisation
type, beneficiary rules by type, payout-destination ownership evidence and validity, information-request
expiry, `adult_age` and the screening status. v1 is seeded (`ADR-034` as the decision reference).
`POST /admin/verification/policies` (maker, `verification_policy.request`) proposes a version; another
person approves it (`verification_policy.approve`, step-up). `PolicyRules.Validate` refuses unsafe rules
(e.g. MINOR or RESTRICTED without four eyes, unknown document types, missing risk levels). Cases and
decisions record the policy version they were evaluated under; **a new policy never rewrites historical
decisions**. `adult_age` (18) is INTERNAL_POLICY configuration, not a legal finding —
LEGAL_REVIEW_REQUIRED (LR-021, LR-015).
