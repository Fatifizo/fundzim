import type { Metadata } from "next";
import Link from "next/link";
import { notFound } from "next/navigation";

import { AccountPage } from "@/components/account/account-shell";
import { ReviewCaseView } from "@/components/admin/review-case";
import { requireStaff, serverGetOr404 } from "@/lib/auth/session";
import { humanise } from "@/lib/verification/labels";
import { normaliseReviewCase } from "@/lib/verification/normalise";

export const metadata: Metadata = { title: "Verification case", robots: { index: false, follow: false, nocache: true } };

const ID = /^[A-Za-z0-9-]{1,64}$/;

export default async function ReviewCasePage({ params }: PageProps<"/admin/verification/cases/[caseId]">) {
  const me = await requireStaff();
  const { caseId } = await params;
  if (!ID.test(caseId)) notFound();
  const path = `/admin/verification/cases/${encodeURIComponent(caseId)}`;
  const raw = (await serverGetOr404<unknown>(`/api/v1/admin/verification/cases/${encodeURIComponent(caseId)}`, path)).data;
  const reviewCase = normaliseReviewCase(raw, me.id);
  return (
    <AccountPage title={`${humanise(reviewCase.type)} verification`} intro={<Link href="/admin/verification" className="text-base font-semibold text-brand-700 underline">Back to the queue</Link>}>
      <ReviewCaseView reviewCase={reviewCase} meId={me.id} />
    </AccountPage>
  );
}
