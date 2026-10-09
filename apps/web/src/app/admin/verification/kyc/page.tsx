import type { Metadata } from "next";

import { ReviewQueuePage } from "@/components/admin/review-queue-page";

export const metadata: Metadata = { title: "Identity (KYC) queue", robots: { index: false, follow: false, nocache: true } };

export default function Page({ searchParams }: PageProps<"/admin/verification/kyc">) {
  return <ReviewQueuePage basePath="/admin/verification/kyc" title="Identity (KYC) queue" fixedType="KYC" searchParams={searchParams} />;
}
