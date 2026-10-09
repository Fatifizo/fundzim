# Stage 6 — Campaign security review

> Threat and abuse analysis for the campaign engine (brief §35). "Where" says which layer enforces each control:
> **API** (Go handlers/services), **DB** (constraints, triggers, grants), **UI** (presentation only, never relied on).
> Tests named are in `tests/integration/` unless stated.

## 1. Controls

| # | Threat | Control | Where | Evidence |
|---|---|---|---|---|
| S6-01 | Unauthorised campaign creation | CREATE_DRAFT gate: personal session, active account, age declaration, BASIC_VERIFIED, ORG_ADMIN for organisations, restriction matrix | API | `TestCampaignLifecycleEndToEnd` (age), unit restriction matrix |
| S6-02 | Cross-user / cross-organisation access (IDOR) | `OwnerAccess`/`lockOwned`: owner or org member to read, owner or ORG_ADMIN to write; everyone else gets 404 (existence hidden); UUID validation | API | lifecycle test (other user GET/PATCH/submit → 404; not listed); media and updates IDOR tests |
| S6-03 | Unauthorised editing / lost updates | `If-Match` required on PATCH; optimistic `version` in every UPDATE; row lock | API + DB | `TestCampaignConcurrency` (5 writers, 1 wins) |
| S6-04 | Unauthorised publication | publish only from APPROVED, PUBLISH evaluation inside the locked transaction; the public page renders the approved snapshot only | API + DB (edge guard, `ck_campaigns_approved`) | `TestCampaignSecurityRechecks` (owner and staff publish refused after KYC revocation) |
| S6-05 | Self-approval | owners have no admin routes; Go `conflicted()`; DB trigger `campaign_reviews_not_self` | API + DB | `TestCampaignSelfReviewFourEyesAndPrivacy` |
| S6-06 | Linked-account self-approval | `app.actor_identities` in Go checks and in the review, update-moderation and KYC decision triggers; link ceremony needs control of the personal account (emailed single-use token) and step-up | API + DB | same test; `TestStaffPersonalLink`, `TestKYCDecisionTriggerRefusesLinkedIdentity` |
| S6-07 | High-risk campaign approved by one person | four eyes for HIGH tier: pending approval + `campaign.decide.high` second approver ≠ first | API + DB CHECK | four-eyes test |
| S6-08 | Compliance restriction bypass | restriction read at every gated action (fail closed); automatic suspension; no auto-reactivation; reasons not disclosed | API + worker | `TestCampaignComplianceRestrictionEnforcement`, `TestCampaignFailureInjection` |
| S6-09 | Beneficiary substitution | beneficiary must belong to the owner; no change while SUBMITTED/UNDER_REVIEW/APPROVED; live change opens a re-review and keeps the old approved version public; history kept | API + DB (no-delete, one current link) | lifecycle + `TestCampaignSecurityRechecks` |
| S6-10 | Stored XSS / HTML injection / unsafe links | plain text only: HTML tags and entities refused, `javascript:`/`vbscript:` and `data:<type>` URLs refused, control, zero-width and bidi characters refused; the web renders text escaped, never as HTML or links; update tables carry DB CHECKs too | API + DB (updates) + UI | unit `TestCleanTextRefusesMarkupAndUnsafeContent`, updates unit tests, lifecycle test, e2e XSS test |
| S6-11 | Malicious or malformed media | sniffed type = declared type; header-only dimension check (8000 px, 40 MP) before decode; active-content and trailing-data refusal; full decode and **re-encode** (metadata stripped); original and derivative both scanned by ClamAV; private objects can never be linked (FK + trigger); served with nosniff and `CSP: default-src 'none'; sandbox` | API + worker + DB | media unit tests, 5 media integration tests |
| S6-12 | Slug enumeration | random 50-bit public code in the slug; every non-public, unknown or malformed slug is the same 404 | API | lifecycle test |
| S6-13 | Draft data leakage | public endpoints read only live campaigns' approved snapshots; no owner/beneficiary/reviewer ids, KYC, risk, compliance or notes in public JSON; owner views never show reviewer identities or internal notes | API | lifecycle test leak checks, `TestCampaignSecurityRechecks` |
| S6-14 | Restricted campaigns exposed through search | search joins approved snapshots of `ACTIVE`/`PAUSED`/`COMPLETED` + `PUBLIC` only; unlisted not listed | API | `TestCampaignSecurityRechecks` |
| S6-15 | CSRF | unchanged Stage 4 double-submit control on every unsafe method | API | `TestCampaignSecurityRechecks` |
| S6-16 | Privilege escalation | route policies (deny by default; staff routes 404 for users); step-up for decide/publish/suspend/unsuspend/category; assignee must hold `campaign.review` | API | lifecycle (KYC reviewer and support refused), assign test |
| S6-17 | Concurrent status changes | row lock + edge guard + optimistic version; one open review (unique index); decided reviews final | API + DB | concurrency test (create idempotency, approve vs reject, publish vs suspend, duplicate submit) |
| S6-18 | Fabricated money | no raised/total/balance columns; goals exact int64 + currency; `GOAL_REACHED` refused; UI shows a disabled CTA | API + DB + UI | lifecycle test |
| S6-19 | Notification leakage | owner emails carry fixed text, no reasons, reviewer identities or notes | worker | — |

## 2. Findings during Stage 6

| ID | Finding | Status |
|---|---|---|
| F-S6-01 | Remote CI had never run: the branch filter `stage-*` cannot match `stage-N/name` | **Fixed** (`stage-**`); runs now execute |
| F-S6-02 | Data race in a notifications test helper (found by the first `-race` run) | **Fixed** (atomic flags) |
| F-S6-03 | govulncheck on CI used Go 1.27.1 (setup-go ignored the `toolchain` directive): GO-2026-6617 reachable | **Fixed** (`go.mod` `go 1.27.2`) |
| F-S6-04 | Live-edit re-review snapshotted the pre-edit content (the in-memory row) | **Fixed** (reload in the transaction) |
| F-S6-05 | The content filter refused ordinary text such as "Our data: 12 families" | **Fixed** (only real `data:` URLs refused) |
| F-S6-06 | **ClamAV 1.5.3 does not detect the EICAR string embedded in a valid PNG/JPEG** (text chunk, comment, trailer, appended ZIP) | Mitigated: re-encoding guarantees no embedded payload is ever served; the scanner still guards stored originals. Do not represent the scanner as image-payload protection |
| F-S6-07 | Route conflict between `GET /admin/campaigns/{id}/media` and the review detail path | **Fixed** (review detail at `/admin/campaigns/{campaign_id}/review`) |

## 3. Residual risks

- Staff who never link their personal account are not caught by self-review checks (procedural control only).
- No device/IP risk input for BASIC_VERIFIED; no screening provider; no fundraising-authority records.
- Media moderation is automated (scan + re-encode); there is no human pre-approval of images before submission review.
- Text content is not checked for prohibited purposes automatically (manual review only).
- Medical and minors' data in stories are reviewed manually (PRIVACY_REVIEW); there is no automated detection.
