/**
 * Structured data (schema.org JSON-LD), used only where it describes the page honestly:
 *  - Organization on the home page (name, URL, logo).
 *  - WebPage for a campaign page (name, description, URL, primary image, dates, publisher).
 * Never: DonateAction, MonetaryAmount totals, ratings, reviews, certifications or awards. Donations are not
 * available, and FundZim makes no claims it cannot evidence (CLAUDE.md regulatory rules).
 */
import { truncate } from "@/lib/campaigns/text";
import { absoluteUrl, campaignPath } from "@/lib/site/url";

export type JsonLd = Record<string, unknown> & { "@context": "https://schema.org"; "@type": string };

/** Types and properties this app may emit; anything else is a bug (enforced by the unit tests). */
export const FORBIDDEN_JSON_LD_KEYS = ["aggregateRating", "review", "award", "hasCredential", "potentialAction", "offers", "amount", "price", "funder", "sponsor"] as const;

export function organizationJsonLd(origin: URL): JsonLd {
  return {
    "@context": "https://schema.org",
    "@type": "Organization",
    name: "FundZim",
    url: absoluteUrl("/", origin)!,
    logo: absoluteUrl("/icons/icon-512.png", origin)!,
    description: "A Zimbabwe-first donation crowdfunding platform under development.",
  };
}

export interface CampaignJsonLdInput {
  slug: string;
  title: string;
  summary: string;
  imageUrl: string | null;
  publishedAt: string | null;
  categoryName: string | null;
}

/** WebPage describing a campaign's public page. No money, totals or donation actions. */
export function campaignJsonLd(input: CampaignJsonLdInput, origin: URL): JsonLd {
  const url = absoluteUrl(campaignPath(input.slug), origin)!;
  const data: JsonLd = {
    "@context": "https://schema.org",
    "@type": "WebPage",
    "@id": url,
    url,
    name: truncate(input.title, 200),
    description: truncate(input.summary, 300),
    isPartOf: { "@type": "WebSite", name: "FundZim", url: absoluteUrl("/", origin)! },
    publisher: { "@type": "Organization", name: "FundZim", url: absoluteUrl("/", origin)! },
  };
  if (input.imageUrl) data.primaryImageOfPage = { "@type": "ImageObject", url: input.imageUrl };
  if (input.publishedAt && !Number.isNaN(Date.parse(input.publishedAt))) data.datePublished = new Date(input.publishedAt).toISOString();
  if (input.categoryName) data.about = { "@type": "Thing", name: input.categoryName };
  return data;
}

/**
 * Serialises JSON-LD for a `<script type="application/ld+json">` body. Escapes `<`, `>`, `&` and the
 * U+2028/U+2029 separators as JSON unicode escapes so user text (a campaign title such as
 * "</script><script>…") can never end the script element or be parsed as HTML. The result is still valid
 * JSON that parses back to the same object.
 */
export function serializeJsonLd(data: JsonLd): string {
  return JSON.stringify(data)
    .replace(/</g, "\\u003c")
    .replace(/>/g, "\\u003e")
    .replace(/&/g, "\\u0026")
    .replace(/\u2028/g, "\\u2028")
    .replace(/\u2029/g, "\\u2029");
}
