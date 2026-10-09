import { Badge } from "@/components/ui/badge";
import { campaignStatusLabel, mediaStatusLabel } from "@/lib/campaigns/labels";

/** Status as a text badge (meaning carried by the text, never by colour alone). */
export function CampaignStatusBadge({ status, audience = "owner" }: { status: string; audience?: "owner" | "staff" }) {
  const { label, tone } = campaignStatusLabel(status, audience);
  return <Badge tone={tone}>{label}</Badge>;
}

export function MediaStatusBadge({ status }: { status: string }) {
  const { label, tone } = mediaStatusLabel(status);
  return <Badge tone={tone}>{label}</Badge>;
}
