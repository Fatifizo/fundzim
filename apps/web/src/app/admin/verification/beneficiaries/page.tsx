import type { Metadata } from "next";

import { ReviewQueuePage } from "@/components/admin/review-queue-page";

export const metadata: Metadata = { title: "Beneficiary queue", robots: { index: false, follow: false, nocache: true } };

export default function Page({ searchParams }: PageProps<"/admin/verification/beneficiaries">) {
  return <ReviewQueuePage basePath="/admin/verification/beneficiaries" title="Beneficiary queue" fixedType="BENEFICIARY" searchParams={searchParams} />;
}
