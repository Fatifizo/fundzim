/**
 * Share links for a public campaign (contract §5). Built only from public, approved fields (title and
 * summary) and the canonical URL from PUBLIC_SITE_URL: no names of beneficiaries or donors, no amounts,
 * nothing private. No third-party scripts: these are plain links that open the other service.
 */
import { truncate } from "@/lib/campaigns/text";

export const SHARE_CHANNELS = ["WHATSAPP", "FACEBOOK", "X", "EMAIL", "COPY_LINK", "WEB_SHARE"] as const;
export type ShareChannel = (typeof SHARE_CHANNELS)[number];

export interface ShareInput {
  title: string;
  summary: string;
  /** Absolute canonical URL (https://…/campaigns/<slug>). */
  url: string;
}

/** Longest summary excerpt in share text: WhatsApp previews and SMS-style readers stay short. */
export const SHARE_SUMMARY_MAX = 140;

function clean(value: string, max: number): string {
  return truncate(value, max);
}

/** "Support this FundZim campaign:\n<title>\n<summary short>\nRead the story: <url>" */
export function shareText({ title, summary, url }: ShareInput): string {
  const lines = ["Support this FundZim campaign:", clean(title, 200)];
  const short = clean(summary, SHARE_SUMMARY_MAX);
  if (short) lines.push(short);
  lines.push(`Read the story: ${url}`);
  return lines.join("\n");
}

/** https://wa.me/?text=<encoded share text> (newlines become %0A). */
export function whatsappShareUrl(input: ShareInput): string {
  return `https://wa.me/?text=${encodeURIComponent(shareText(input))}`;
}

export function facebookShareUrl({ url }: Pick<ShareInput, "url">): string {
  return `https://www.facebook.com/sharer/sharer.php?u=${encodeURIComponent(url)}`;
}

export function xShareUrl({ title, url }: Pick<ShareInput, "title" | "url">): string {
  return `https://x.com/intent/post?text=${encodeURIComponent(`Support this FundZim campaign: ${clean(title, 200)}`)}&url=${encodeURIComponent(url)}`;
}

/** mailto with percent-encoded subject and body (spaces as %20, never "+", which mail clients show literally). */
export function emailShareUrl(input: ShareInput): string {
  return `mailto:?subject=${encodeURIComponent(`Support this FundZim campaign: ${clean(input.title, 200)}`)}&body=${encodeURIComponent(shareText(input))}`;
}

export interface ShareLink {
  channel: Extract<ShareChannel, "WHATSAPP" | "FACEBOOK" | "X" | "EMAIL">;
  label: string;
  href: string;
  /** Opens another site: rendered with target=_blank and announced as such. */
  external: boolean;
}

export function shareLinks(input: ShareInput): ShareLink[] {
  return [
    { channel: "WHATSAPP", label: "WhatsApp", href: whatsappShareUrl(input), external: true },
    { channel: "FACEBOOK", label: "Facebook", href: facebookShareUrl(input), external: true },
    { channel: "X", label: "X", href: xShareUrl(input), external: true },
    { channel: "EMAIL", label: "Email", href: emailShareUrl(input), external: false },
  ];
}
