import type { Metadata } from "next";

import { ReviewQueuePage } from "@/components/admin/review-queue-page";

export const metadata: Metadata = { title: "Verification queue", robots: { index: false, follow: false, nocache: true } };

export default function Page({ searchParams }: PageProps<"/admin/verification">) {
  return <ReviewQueuePage basePath="/admin/verification" title="Verification queue" searchParams={searchParams} />;
}
