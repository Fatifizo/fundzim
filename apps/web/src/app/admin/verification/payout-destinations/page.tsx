import type { Metadata } from "next";

import { ReviewQueuePage } from "@/components/admin/review-queue-page";

export const metadata: Metadata = { title: "Payout account queue", robots: { index: false, follow: false, nocache: true } };

export default function Page({ searchParams }: PageProps<"/admin/verification/payout-destinations">) {
  return <ReviewQueuePage basePath="/admin/verification/payout-destinations" title="Payout account queue" fixedType="PAYOUT_DESTINATION" searchParams={searchParams} />;
}
