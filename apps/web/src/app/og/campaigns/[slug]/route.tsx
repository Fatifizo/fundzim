import { createHash } from "node:crypto";

import { ImageResponse } from "next/og";

import { isSlug } from "@/lib/campaigns/paths";
import { fetchPublicCampaign } from "@/lib/campaigns/public";
import { truncate } from "@/lib/campaigns/text";

const STATUS_NOTE: Record<string, string> = { PAUSED: "Paused", COMPLETED: "Ended" };

function noStore(status: number, text: string): Response {
  return new Response(text, { status, headers: { "Cache-Control": "no-store", "Content-Type": "text/plain; charset=utf-8" } });
}

/**
 * Link-preview image (1200×630 PNG) for a public campaign without an approved cover: the FundZim name, the approved
 * title, category and coarse place, and the status when paused or ended. Never an amount, progress, a donor, a
 * beneficiary's name or a verification claim (docs/stage-16/social-sharing.md §3). Anything that is not public now is a
 * 404 (same rule as the page, checked on every request). `public, no-cache` + ETag: crawlers and caches must revalidate,
 * so a campaign that leaves public view stops producing previews at once (external caches may still hold old copies; we
 * cannot purge them).
 */
export async function GET(request: Request, { params }: RouteContext<"/og/campaigns/[slug]">): Promise<Response> {
  const { slug } = await params;
  if (!isSlug(slug)) return noStore(404, "Not found");
  let campaign;
  try {
    campaign = await fetchPublicCampaign(slug);
  } catch {
    return noStore(503, "Preview unavailable");
  }
  if (!campaign || campaign.canonical_slug !== slug) return noStore(404, "Not found");
  const place = campaign.location ? (campaign.location.kind === "TOWN" ? `${campaign.location.name}, ${campaign.location.province_name}` : `${campaign.location.province_name} province`) : "Zimbabwe";
  const title = truncate(campaign.title, 110);
  const category = campaign.category.name || campaign.category.code;
  const status = STATUS_NOTE[campaign.status] ?? "";
  const etag = `"${createHash("sha256").update(JSON.stringify([title, category, place, status])).digest("hex").slice(0, 32)}"`;
  if (request.headers.get("if-none-match")?.split(",").some((v) => v.trim() === etag)) {
    return new Response(null, { status: 304, headers: { ETag: etag, "Cache-Control": "public, no-cache" } });
  }
  return new ImageResponse(
    (
      <div style={{ width: "100%", height: "100%", display: "flex", flexDirection: "column", justifyContent: "space-between", padding: "64px", background: "#0b3b2e", color: "#ffffff" }}>
        <div style={{ display: "flex", alignItems: "center", fontSize: 36, fontWeight: 700, color: "#f5c542" }}>FundZim</div>
        <div style={{ display: "flex", flexDirection: "column" }}>
          <div style={{ display: "flex", fontSize: 30, color: "#cfe8de" }}>{`${category} · ${place}`}</div>
          <div style={{ display: "flex", marginTop: 16, fontSize: title.length > 60 ? 56 : 68, fontWeight: 700, lineHeight: 1.15 }}>{title}</div>
        </div>
        <div style={{ display: "flex", justifyContent: "space-between", fontSize: 26, color: "#cfe8de" }}>
          <span>Reviewed before publication</span>
          <span>{status}</span>
        </div>
      </div>
    ),
    { width: 1200, height: 630, headers: { "Cache-Control": "public, no-cache", ETag: etag, "X-Content-Type-Options": "nosniff" } },
  );
}
