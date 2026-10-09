# Stage 5 — Payout Destination Verification

> Code: `internal/payouts` (`destinations.go`, `review.go`), migration `20261009150400_payout_destinations.sql`.
> **Stage 5 verifies destinations; it does not pay anyone.** There is no payout request, eligibility engine or
> provider integration (Stage 11). `eligible_for_payout` is **always `false`** in every response.
> Design inputs: [payments/payout-eligibility-and-controls.md](../payments/payout-eligibility-and-controls.md).

## 1. What a destination is

A mobile-money wallet or bank account that a user or organisation declares as where funds could later be
sent. The account identifier is stored AES-256-GCM encrypted with an HMAC blind index; responses show
only a masked suffix (`••••567`). One live destination per owner, rail, currency and account
(`409 DESTINATION_EXISTS`). Reuse of the same account by other owners is counted into the audit metadata as
a risk observation, not a refusal.

| Rail | Category | Format validation |
|---|---|---|
| `ECOCASH`, `ONEMONEY`, `INNBUCKS`, `OMARI` | MOBILE_MONEY_WALLET | valid Zimbabwean **mobile** number (libphonenumber), normalised to E.164 |
| `BANK_TRANSFER`, `ZIMSWITCH` | BANK_ACCOUNT | 6–20 digits plus a bank code |

Currency is `USD` or `ZWG`. Which rails a provider can actually pay to is unknown —
PROVIDER_CONFIRMATION_REQUIRED (PCR-004, PCR-013). The payee is the owner or one of the owner's
beneficiaries (`payee.type = OWNER | BENEFICIARY`). Organisation destinations are visible to ORG_ADMINs only;
anyone else gets `404 DESTINATION_NOT_FOUND`.

## 2. Three separate outcomes

| Outcome | Recorded as | How it can pass |
|---|---|---|
| **FORMAT_VALIDATED** | `payout_destination_checks` FORMAT / FORMAT_RULES PASS at creation | identifier format only — **says nothing about ownership** |
| **OWNERSHIP_CONFIRMED** | OWNERSHIP check; `ownership_status` | a reviewer confirms it from **clean documentary evidence** (`BANK_LETTER`, `BANK_STATEMENT`, `MOBILE_MONEY_STATEMENT`) with an explicit holder-name match; or, in future, an approved provider lookup |
| **COMPLIANCE_APPROVED** | COMPLIANCE check / STAFF_REVIEW; `compliance_status` | reviewer decision with reason code |

Statuses: `UNVERIFIED` → `PENDING_VERIFICATION` → `VERIFIED` | `REJECTED`; `VERIFIED` → `SUSPENDED` |
`EXPIRED` (365 days by policy); plus `RETIRED` (owner removes it; a retired destination cannot change).

**Database guards** (not only code):
- `VERIFIED` requires `ownership_status = CONFIRMED`, `compliance_status = APPROVED`, `verified_at/by`, and a
  production OWNERSHIP PASS **and** COMPLIANCE PASS bound to the **current `details_version`**.
- Any change of account details requires `details_version + 1`, returns the destination to `UNVERIFIED` and
  resets the checks, so earlier checks can never verify new details.
- A `DEV_MOCK_PROVIDER` check must be flagged `non_production`, and a non-production OWNERSHIP **PASS** is
  refused outright.

## 3. Provider lookup and the development mock

`POST /payout-destinations/{id}/verification` with `method = PROVIDER_LOOKUP` records an OWNERSHIP check by
`DEV_MOCK_PROVIDER` with result `PROVIDER_CONFIRMATION_REQUIRED`, `non_production = true`, and moves the
destination to `PENDING_VERIFICATION` for human review. **The mock never confirms ownership and can never make
a destination VERIFIED or payout-eligible** — the database refuses it even if code tried (integration-tested
with direct SQL). No real account-name lookup exists (PCR-013).

With `method = BANK_LETTER | BANK_STATEMENT | MOBILE_MONEY_STATEMENT` the owner must first upload the
evidence (subject `PAYOUT_DESTINATION`) and it must be CLEAN (`422 SUBMISSION_INCOMPLETE` otherwise). The
`document_ids` field is accepted but not used; all current clean evidence on the destination is considered.

## 4. Review

Reviewers need `payout_destination.verify` + step-up (KYC_REVIEWER, COMPLIANCE). Assignment starts the review
(destinations have no separate UNDER_REVIEW state; `start-review` is accepted and does nothing). Approval
requires `name_match: true` (`422 NAME_MATCH_REQUIRED`) and clean ownership evidence
(`409 OWNERSHIP_EVIDENCE_MISSING`). The reviewer may not be the owner, creator, last changer, a member of
the owning organisation or linked to any of them through their personal account
(`403 SELF_DECISION_FORBIDDEN`). Staff changes to destinations are not possible in Stage 5.

## 5. Other controls

- **Cooling-off:** `cooling_off_until` = creation/last change + 72 h (INTERNAL_RISK starting point; moves to the
  limits registry with payouts in Stage 11). Shown to the owner; there is nothing to enforce it against yet.
- **PAYOUT_VERIFIED:** a verified destination owned by an `IDENTITY_VERIFIED` individual upgrades that
  individual's KYC level to `PAYOUT_VERIFIED` (worker consumer). Organisations are not upgraded yet (KI-S5-16).
- **Events:** `payouts.destination_{created,changed,verified,rejected,information_requested,suspended,expired}`
  with IDs only; `changed` and `rejected` feed the risk module.
- **Notifications** say "Payouts are not available on FundZim yet" on verification.
