import type { Metadata } from "next";
import { notFound, permanentRedirect } from "next/navigation";

import { QrCode } from "@/components/account/qr-code";
import { PrintButton } from "@/components/discovery/print-button";
import { Container } from "@/components/ui/container";
import { isSlug } from "@/lib/campaigns/paths";
import { fetchPublicCampaign } from "@/lib/campaigns/public";
import { truncate } from "@/lib/campaigns/text";
import { absoluteUrl, siteOrigin } from "@/lib/site/url";

export const metadata: Metadata = { title: "Campaign poster", robots: { index: false, follow: false } };

/**
 * A printable poster for a public campaign (roadmap Stage 16 "QR posters"): approved title and summary, category and
 * place, a QR code of the campaign's short link (`/c/<public code>`, resolved to the canonical page; no referral code, no
 * tracking) and the plain short link for people who cannot scan. The QR code is drawn in the browser (no network).
 * Never an amount, progress or a request to pay: donations are not available. Only for public campaigns (404 otherwise).
 */
export default async function CampaignPosterPage({ params }: PageProps<"/campaigns/[slug]/poster">) {
  const { slug } = await params;
  if (!isSlug(slug)) notFound();
  const campaign = await fetchPublicCampaign(slug);
  if (!campaign) notFound();
  if (campaign.canonical_slug !== slug) permanentRedirect(`/campaigns/${campaign.canonical_slug}/poster`);
  const origin = siteOrigin();
  const short = campaign.public_code ? absoluteUrl(`/c/${campaign.public_code}`, origin) : null;
  const place = campaign.location ? (campaign.location.kind === "TOWN" ? `${campaign.location.name}, ${campaign.location.province_name}` : `${campaign.location.province_name} province`) : null;
  return (
    <Container className="py-8 sm:py-12">
      <div data-print-poster className="mx-auto max-w-2xl space-y-6 rounded-card border-2 border-ink-900 bg-surface p-8 text-center print:border-0">
        <p className="font-display text-2xl font-bold text-brand-800">FundZim</p>
        <p className="font-semibold text-ink-700">
          {campaign.category.name || campaign.category.code}
          {place ? ` · ${place}` : ""}
        </p>
        <h1 className="font-display text-4xl font-bold tracking-tight break-words text-ink-900">{truncate(campaign.title, 120)}</h1>
        <p className="text-xl break-words text-ink-700">{truncate(campaign.summary, 200)}</p>
        {short ? (
          <div className="flex flex-col items-center gap-3">
            <QrCode value={short} label={`QR code linking to ${short}`} size={256} />
            <p className="text-lg font-semibold text-ink-900">Scan to read the story, or visit</p>
            <p className="font-mono text-lg break-all text-ink-900">{short}</p>
          </div>
        ) : (
          <p className="text-ink-700">The poster needs the site address (PUBLIC_SITE_URL) to print a QR code.</p>
        )}
        <p className="text-sm text-ink-700">
          Reviewed by FundZim before publication. Donations are not yet available: never send money to anyone because of this poster.
        </p>
        <div className="print:hidden">
          <PrintButton />
        </div>
      </div>
    </Container>
  );
}
