# Stage 6 — Known issues

## 1. Earlier issues — status after Stage 6

| Issue | Status |
|---|---|
| KI-S5-01 / KI-S4-01 CI never ran | **RESOLVED (root cause)** — branch filter fixed; runs execute. Final green/red status: completion report |
| KI-S5-02 staff ↔ personal link | **RESOLVED** — link ceremony (ADR-037 §4); see KI-S6-07 |
| KI-S5-03 KYC decision trigger compared direct ids only | **RESOLVED** — uses `app.actor_identities` |
| KI-S5-17 BASIC_VERIFIED never granted | **RESOLVED with conditions** — KI-S6-01, KI-S6-02 |
| KI-S5-21 compliance resolutions not enforced | **RESOLVED for campaigns** — accounts are still gated by Stage 4 account status; other future actions must add checks |
| KI-S5-16 fundraising authorities, ORG_REGISTERED/ORG_PAYOUT levels | OPEN — KI-S6-03 |
| KI-S5-04 screening not performed | OPEN — KI-S6-06 |

## 2. Stage 6 issues

| ID | Issue | Impact | Status |
|---|---|---|---|
| KI-S6-01 | BASIC_VERIFIED does not use the device/IP-risk input of kyc-architecture.md (no such signal exists) | Weaker than the Stage 1 design | ACCEPTED (ADR-037 §2) — revisit when risk signals exist |
| KI-S6-02 | BASIC is not re-granted automatically after an identity level is revoked or expires to UNVERIFIED; it returns at the next email/phone/account event or attestation. Existing users must attest once | User friction | OPEN |
| KI-S6-03 | Fundraising-authority records (PVO registration, s8) are not built; `fundraising_basis` is declared; individuals raising for others are blocked by `campaign.individual_for_others.enabled = false` | Third-party individual campaigns unavailable | OPEN — **LEGAL_REVIEW_REQUIRED** (LR-046 – LR-048, PD-27) |
| KI-S6-04 | Media declared as depicting a minor are refused (`MINOR_MEDIA_NOT_SUPPORTED`); minors' names are never disclosed | Some campaigns cannot show images | OPEN — **LEGAL_REVIEW_REQUIRED** (LR-070, PD-39) |
| KI-S6-05 | ZWG goals unavailable (`minor_units_verified = false`) | USD only | OPEN — **LEGAL_REVIEW_REQUIRED** (LR-043) |
| KI-S6-06 | No sanctions/PEP screening of owners or beneficiaries (`SCREENING_PROVIDER_NOT_SELECTED`) | Manual review only | OPEN — **PROVIDER_CONFIRMATION_REQUIRED** |
| KI-S6-07 | Staff links cannot be removed through the API (needs a future maker-checker procedure); staff who never link are not detected | Procedural reliance on conflict declarations | OPEN |
| KI-S6-08 | `If-Match` errors differ: campaigns 422 `IF_MATCH_REQUIRED` / 409 `CAMPAIGN_STATE_CHANGED`; media 400 `PRECONDITION_REQUIRED` / 409 `MEDIA_STATE_CHANGED`; updates 422 `VALIDATION_FAILED` / 409 `VERSION_CONFLICT`; category PATCH treats it as optional | Client mapping | OPEN — align |
| KI-S6-09 | Path parameter names differ for the same resource: core `{campaign_id}`, media and updates `{id}` | Contract consistency | OPEN — align |
| KI-S6-10 | The review queue ignores `?status=` (contract §6 lists it) | Minor | OPEN |
| KI-S6-11 | Hidden updates cannot be unhidden (`HIDDEN → DELETED` only) | Moderation mistakes need a new update | OPEN |
| KI-S6-12 | `FROZEN` is reserved (needs ledger and risk holds) | — | Stage 10+ |
| KI-S6-13 | Cancelling or completing a live campaign has no funds resolution (no funds exist yet) | Must be designed before payments | OPEN — **LEGAL_REVIEW_REQUIRED** (LR-019) |
| KI-S6-14 | No campaign end dates or expiry job; completion reason `EXPIRED` unused | — | OPEN |
| KI-S6-15 | ClamAV 1.5.3 does not detect EICAR embedded in valid PNG/JPEG; images rely on re-encoding (F-S6-06) | Scanner is not image-payload protection | ACCEPTED, documented |
| KI-S6-16 | Campaign policy changes have no admin API (migration only), although `app.campaign_policies` is maker-checker ready | Policy changes need a deploy | OPEN |
| KI-S6-17 | Categories: staff can change only name, description, order and active flag; tiers/moderation policy change by migration | By design for now | ACCEPTED |
| KI-S6-18 | Search uses the `simple` text configuration (no stemming, no Shona/Ndebele handling); advanced discovery is Stage 16 | Search quality | OPEN |
| KI-S6-19 | Staff admin pages are tested on the mock API only; their backend endpoints are covered by integration tests, but the pages were not smoke-tested against the real API | UI/API shape drift possible | OPEN — real-stack staff E2E in Stage 7 |
| KI-S6-20 | `TestWorkerCrashedJobIsRescued` is load-sensitive (30 s deadline) when the whole suite shares one worker | Flake risk | OPEN |
| KI-S6-21 | Review checklists are manual; no per-check result rows (`campaign_review_check_results`) | Less structured evidence | OPEN |
| KI-S6-22 | Retention of removed media and archived campaigns is not implemented (soft delete only) | Data kept longer | OPEN — **LEGAL_REVIEW_REQUIRED** (LR-012) |
| KI-S6-23 | Stage 1–5 acceptance not recorded | Process gap | OPEN — owner |
