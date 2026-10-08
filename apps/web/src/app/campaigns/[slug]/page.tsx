import type { Metadata } from "next";
import { notFound } from "next/navigation";

export const metadata: Metadata = {
  title: "Campaign pages are coming soon",
  robots: { index: false, follow: false },
};

/**
 * Campaign pages arrive in Stage 7 (rendered from API data). Until then, NO slug is a real campaign:
 * every request answers 404 with an explanatory page (./not-found.tsx). The slug is never echoed or
 * rendered, so a crafted link cannot make this site display an appeal.
 */
export default function CampaignPage() {
  notFound();
}
