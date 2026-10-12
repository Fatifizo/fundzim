# Stage 7 — Social sharing

Code: `apps/web/src/lib/share/links.ts`, `apps/web/src/components/campaigns/share-controls.tsx`, link previews in
`apps/web/src/lib/seo/metadata.ts`. Contract: [interface-contracts.md](interface-contracts.md) §5.

## 1. Principles

- **Plain links only.** No share SDKs, widgets, pixels or third-party scripts; each button is an ordinary link that
  opens the other service. External links use `target="_blank"` with `rel="noopener noreferrer"`.
- **Public, approved content only.** Share text is built from the approved title and summary and the canonical URL.
  It never contains beneficiary or donor names, amounts, totals or anything private.
- **Canonical URL from configuration.** The URL is `PUBLIC_SITE_URL` + `/campaigns/<canonical slug>`, never taken
  from request headers. Without `PUBLIC_SITE_URL` the share controls are not rendered (outside production a note
  explains why: "Sharing is turned off on this server: PUBLIC_SITE_URL is not configured …").
- **No donation calls to action that imply payment is possible.** The text invites people to read the story.

## 2. Share text

```
Support this FundZim campaign:
<title, at most 200 characters>
<summary, at most 140 characters (omitted when empty)>
Read the story: <canonical URL>
```

Truncation uses the shared `truncate` helper.

## 3. Channels

| Channel | Link |
|---|---|
| WhatsApp | `https://wa.me/?text=<encodeURIComponent(share text)>` (newlines become `%0A`) |
| Facebook | `https://www.facebook.com/sharer/sharer.php?u=<encoded URL>` |
| X | `https://x.com/intent/post?text=<encoded "Support this FundZim campaign: <title>">&url=<encoded URL>` |
| Email | `mailto:?subject=<encoded "Support this FundZim campaign: <title>">&body=<encoded share text>` (percent-encoded spaces, never `+`) |
| Copy link | Clipboard API; the result is announced in a polite live region; if copying fails the link is shown for manual copying |
| Web Share | Shown only when `navigator.share` exists (feature-detected after hydration); shares title, text without the URL line, and the URL |

Each click counts a `CAMPAIGN_SHARE_CLICK` event with its channel (first-party aggregate counter; no identifiers; not
sent under DNT/GPC).

## 4. Link previews (WhatsApp, Facebook, X, others)

Campaign pages emit Open Graph and Twitter card metadata: title (with a status suffix for paused or ended
campaigns), description (summary truncated to 200), canonical URL, `og:type` `article`, `og:site_name` FundZim,
`og:locale` `en_ZW`, and the approved cover image (with its alt text) or the generic image
`/og/fundzim-og.png` (1200×630). Card type is `summary_large_image` when an image URL exists. All URLs are absolute
from `PUBLIC_SITE_URL`; without it the URL fields are omitted rather than guessed. Cover images are served by the API
only while the campaign is public (`public, no-cache` + ETag), so a preview fetched after suspension gets a 404.

Preview rendering by WhatsApp, Facebook and X was **not verified** against the real services (they need a publicly
reachable URL; no such deployment exists) — KI-S7-06.

## 5. Short links

`/c/<public code>` redirects permanently to the canonical campaign URL and is `noindex`. Codes are the 10-character
suffix of the slug (Crockford-style alphabet without `i`, `l`, `o`, `u`). QR codes are not provided in Stage 7
(KI-S7-10).

## 6. Not included

WhatsApp Business API messaging (ROADMAP Stage 16, consent model required — LR-023), referral tracking or UTM
parameters, share counts.
