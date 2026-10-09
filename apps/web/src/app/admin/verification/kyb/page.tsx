import type { Metadata } from "next";

import { ReviewQueuePage } from "@/components/admin/review-queue-page";

export const metadata: Metadata = { title: "Organisation (KYB) queue", robots: { index: false, follow: false, nocache: true } };

export default function Page({ searchParams }: PageProps<"/admin/verification/kyb">) {
  return <ReviewQueuePage basePath="/admin/verification/kyb" title="Organisation (KYB) queue" fixedType="KYB" searchParams={searchParams} />;
}
