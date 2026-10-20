# Stage 16 — Referrals

Code: `internal/discovery/referrals.go`, `events.go`; web `lib/share/referral.ts`.

## 1. Token design

| Property | Value |
|---|---|
| Format | 12 characters, Crockford base32 lower case (60 bits, `crypto/rand`); unique index; collision → redraw |
| Content | none: an opaque key to (campaign, channel, created, expires, revoked) |
| Personal data | none — no user, account, device, address, cookie or referrer is stored with it |
| Privileges | none — not a session, not an authorisation, no discount or money meaning |
| Expiry | 90 days (engineering default; CHECK caps 180) |
| Revocation | staff (`discovery.operate`, step-up, reason, audited); a revoked code stops counting |
| Issuance | `POST /discovery/referrals {slug, channel}` for a discoverable campaign; 30 per hour per address; 503 `REFERRALS_DISABLED` when switched off |

## 2. Attribution

A campaign view that carries `ref` is counted as a referral visit for the code's channel when the code exists, is not
revoked, has not expired and belongs to that campaign; otherwise it is an ordinary view and an `INVALID_REFERRAL`
abuse indicator. Unique referral visitors are de-duplicated like views.

| Source | How it is known |
|---|---|
| WhatsApp, Facebook, X, LinkedIn, e-mail, copy link, device share | referral code channel |
| FundZim search, category, featured, trending, recommendations | discovery surface remembered in memory on the result click (no URL parameter) |
| Internal link | same-site referrer |
| Direct | no referrer |
| Unknown | anything else (the referrer itself is never sent or stored) |

FundZim does not claim exact attribution: the same person may arrive by several routes, codes can be forwarded, and
referrer headers are often absent. Figures are labelled as such.

**Donations are never attributed** to a code, a channel or a visit (there are no real donations, and attribution must
never reveal an individual donor's path — roadmap acceptance criterion).

## 3. Privacy and legal

No tracking cookie, no device identifier, no IP retention (an address is used only in memory, at most one flush interval,
to compute a daily keyed hash for de-duplication), no profile, no cross-site tracking, Do Not Track / GPC honoured.
LR-023 ("referral attribution and tracking consent") is open: **production issuance and counting are blocked in code**
(`discovery.ProductionReferralsEnabled = false`) and the flag `discovery.referrals.enabled` is a kill switch everywhere
(seeded on for development). The share panel tells people what the code is: "Shared links carry a short code that tells
the organiser which app brought visitors. It does not identify you or anyone you share with."

## 4. Abuse

Rate limits (issuance 30/h, events 240/min per address), visitor caps, codes bound to one campaign (a code for campaign
A never counts on campaign B), staff revocation, no redirector (no open redirect), referral spam visible in the abuse
indicators.
