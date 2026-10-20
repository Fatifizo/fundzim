# Stage 16 — WhatsApp sharing

## 1. Mechanism

User-initiated share links only: `https://wa.me/?text=<percent-encoded text>` opened in a new tab (or the WhatsApp app
on phones). No WhatsApp Business API, no provider, no automated or scheduled message, no message sent by FundZim on
anyone's behalf. Stage 15's consent records and `LiveMessagingEnabled = false` are untouched: a share link is the person
choosing to message their own contacts (handover §1: "Share links need no provider and no consent; sending WhatsApp
messages does").

## 2. Text (`shareText`, unchanged from Stage 7 apart from the URL)

```
Support this FundZim campaign:
<approved title, ≤ 200 characters>
<approved summary, ≤ 140 characters, cut on a word boundary>
Read the story: <canonical URL>?ref=<code>
```

Only the approved title and summary and the canonical URL. Never beneficiary names or details, donor information,
verification details, amounts, progress or any claim beyond the campaign's own approved text. Control and bidirectional
characters are removed. The WhatsApp button is the primary (most prominent) share button on the campaign page.

## 3. URL safety

- The URL is built from `PUBLIC_SITE_URL` (never request headers); without it no share option is offered.
- The referral code (`?ref=`) is an opaque 12-character code naming only the campaign and the channel; no secret, no
  session, no personal data. Codes are requested lazily (≤ 800 ms) and the plain canonical URL is shared on any failure,
  when referrals are disabled, and always in production while LR-023 is open.
- No redirector: shared links point at the campaign page itself; `/c/<code>` short links resolve only to the canonical
  campaign URL (no open redirect).
- The share window is opened synchronously in the click (popup blockers allow it), its `opener` is cut, then it is
  navigated to WhatsApp; a blocked popup falls back to the plain link in the same tab.
- The campaign page removes `?ref=` from the address bar after counting the visit (`history.replaceState`), so a link
  copied from the address bar is the clean canonical URL.

## 4. Link previews in WhatsApp

WhatsApp builds the preview from the page's Open Graph tags: `og:title` (approved title, "(paused)"/"(ended)" marker),
`og:description` (summary ≤ 200), `og:image` (the approved cover's 1024 px variant, or the generated preview card
[social-sharing.md](social-sharing.md) §3), `og:url` (canonical). Public pages need no authentication, so crawlers can
read them; restricted or suspended campaigns return 404 to crawlers too.

**Not verified with real WhatsApp clients** (roadmap acceptance criterion "Link previews render correctly in WhatsApp"):
the development stack is not reachable from WhatsApp's servers (localhost, no public HTTPS). The tags are asserted in
E2E and the preview image is generated and tested; real-client verification needs a public staging URL (KI-S16-01).
