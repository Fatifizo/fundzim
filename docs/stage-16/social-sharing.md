# Stage 16 — Social sharing and link previews

## 1. Channels (`apps/web/src/lib/share/links.ts`, `components/campaigns/share-controls.tsx`)

| Channel | Mechanism | Opens |
|---|---|---|
| WhatsApp | `https://wa.me/?text=` (share text + URL) | new tab / app |
| Facebook | `https://www.facebook.com/sharer/sharer.php?u=` | new tab |
| X | `https://x.com/intent/post?text=&url=` | new tab |
| LinkedIn (new) | `https://www.linkedin.com/sharing/share-offsite/?url=` | new tab |
| Email | `mailto:?subject=&body=` (percent-encoded, `%20` not `+`) | mail client |
| Copy link | Clipboard API; selectable field fallback when refused | — |
| Device share sheet | Web Share API (`navigator.share`), offered only where it exists ("More options…") | OS sheet |

No third-party SDK, script, pixel or widget; plain links. External links carry `rel="noopener noreferrer"` and an
"(opens in a new tab)" label. Each share click is counted (aggregate, channel only).

## 2. Unsupported browsers and failures

Web Share missing → the button is not offered. Clipboard refused → a labelled read-only field with the link. Popup
blocked → the plain link opens in the same tab. Referral code slow or refused → the plain canonical URL. No share
option without a configured `PUBLIC_SITE_URL` (a development note explains it outside production).

## 3. Preview images (`/og/campaigns/[slug]`, `apps/web/src/app/og/campaigns/[slug]/route.tsx`)

- A campaign **with** an approved cover uses its 1024 px JPEG variant (public media only; served only while the campaign
  is public).
- A campaign **without** a cover gets a generated 1200×630 PNG: "FundZim", category · place, the approved title (≤ 110
  characters), "Reviewed before publication", and "Paused"/"Ended" when relevant. Never an amount, progress, donor,
  beneficiary name or verification claim.
- The route re-fetches the public campaign on every request (same rule as the page): 404 `no-store` once it is not
  public, 503 `no-store` when the API is unavailable, `public, no-cache` + ETag when served (a 304 is cheap).
- Outside the CSP proxy (an image, not a document); `X-Content-Type-Options: nosniff`.

## 4. Crawler access and caches

Public pages and images need no authentication. Security headers are unchanged. External caches (WhatsApp, Facebook,
X, LinkedIn) keep their own copies of previews for their own durations; FundZim cannot purge them. A campaign that is
suspended or restricted stops producing previews immediately, but a preview already cached by a platform can be shown
until that platform refreshes it (Facebook's Sharing Debugger can force a refresh manually). This is documented, not
promised away (KI-S16-02).
