import { Badge } from "@/components/ui/badge";
import { Card } from "@/components/ui/card";

export interface CampaignCardProps {
  title: string;
  summary: string;
  category: string;
  location: string;
  /**
   * Stage 3 only renders design previews. A preview card is always badged as not real and never shows
   * money, progress or a donate action. Real cards (Stage 7) will render API data, including Money
   * values per currency via src/lib/money.ts.
   */
  preview: true;
}

export function CampaignCard({ title, summary, category, location }: CampaignCardProps) {
  return (
    <Card as="article" className="flex flex-col gap-4 p-0">
      <div aria-hidden="true" className="h-36 rounded-t-card bg-[linear-gradient(135deg,var(--color-brand-100),var(--color-gold-200))]" />
      <div className="flex flex-1 flex-col gap-3 px-6 pb-6">
        <div className="flex flex-wrap gap-2">
          <Badge tone="gold">Design preview — not a real campaign</Badge>
          <Badge tone="neutral">{category}</Badge>
        </div>
        <h3 className="font-display text-xl font-semibold text-ink-900">{title}</h3>
        <p className="text-ink-600">{summary}</p>
        <p className="mt-auto text-sm text-ink-600">{location}</p>
        <p className="text-sm font-medium text-ink-700">No fundraising totals are shown: no campaign is live.</p>
      </div>
    </Card>
  );
}
